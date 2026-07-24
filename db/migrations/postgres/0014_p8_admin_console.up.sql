-- P8 Admin & Operations Console — admin_principal, admin_role_grant, admin_session, permission,
-- audit_entry, impersonation_session, kill_switch_state, gdpr_request.
-- Spec:   openspec/changes/p8-admin-console/specs/{admin-rbac,admin-operations,admin-observability-audit}/spec.md
-- Design: openspec/changes/p8-admin-console/design.md "Data model sketch".
--
-- Dialect: PostgreSQL 11+. EXPAND-ONLY. It ADDS eight new tables, ALTERs no existing column, drops
-- nothing, and rewrites no row. Every statement is idempotent (`IF NOT EXISTS` / `ON CONFLICT DO
-- NOTHING`), so a re-run is a no-op — the guard the migration-script rule requires, and the reason a
-- new binary can self-heal an older database. This is admin-OWNED data only: everything the console
-- reads/administers elsewhere (P2.5 usage/cost, P4/P6 jobs, P6 merge ledger, P7 accounts/billing) is
-- reached through the subsystem that owns it, never copied here (design Decision 8).
--
-- NO PRICED VALUE IS IN THIS FILE. Plan definitions and the model-registry price references the console
-- repoints ship through the CONFIG STORE, never a migration (design Decision 9). `TestNoPricedValue…`
-- fences the git index.
--
-- Load-bearing properties, each enforced BY CONSTRUCTION:
--
--   * ADMIN IDENTITY IS SEPARATE — `admin_principal` has no tenant column and no foreign key into any
--     customer/tenant table. An admin is never a tenant principal (FR1).
--   * ROLE GRANTS ARE APPEND-ONLY — `admin_role_grant` is written, never updated: a grant and a revoke
--     are each a NEW row (FR5). The write-once trigger below refuses UPDATE/DELETE.
--   * SESSIONS ARE SHORT-LIVED + REVOCABLE — `admin_session` carries `expires_at` and a nullable
--     `revoked_at`; a request authorizes only against a live, unexpired, unrevoked session (FR2).
--   * THE AUDIT LOG IS APPEND-ONLY + HASH-CHAINED — `audit_entry` has a monotonic `seq`, a `prev_hash`
--     and an `entry_hash`; the write-once trigger refuses UPDATE/DELETE for EVERY role including
--     superuser-at-the-app-layer, so tampering breaks the chain and is detectable (FR15). There is no
--     mutate/delete path in the app because there is none in the schema.
--   * GDPR TOMBSTONE KEEPS THE CHAIN INTACT — `gdpr_request` holds a NON-PII `tombstone_ref`; the
--     erasure removes content in the owning stores and appends a tombstone entry, never removing an
--     audit row (FR17).

BEGIN;

-- ── Admin identity (SEPARATE from customer/tenant auth) ─────────────────────────────────────────
CREATE TABLE IF NOT EXISTS admin_principal (
    admin_id      TEXT        PRIMARY KEY,
    -- The stable subject the admin IdP asserts. Opaque handle, never a password/token/MFA seed.
    sso_subject   TEXT        NOT NULL UNIQUE,
    mfa_enrolled  BOOLEAN     NOT NULL DEFAULT FALSE,
    status        TEXT        NOT NULL DEFAULT 'active'
                              CHECK (status IN ('active','disabled')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
    -- NO tenant_id, and NO foreign key into any tenant table: an admin principal is categorically not
    -- a tenant principal (FR1).
);

-- ── Role grants — APPEND-ONLY, Superadmin-gated, audited ────────────────────────────────────────
CREATE TABLE IF NOT EXISTS admin_role_grant (
    grant_id    TEXT        PRIMARY KEY,
    admin_id    TEXT        NOT NULL REFERENCES admin_principal(admin_id),
    role        TEXT        NOT NULL
                            CHECK (role IN ('support','billing_ops','platform_sre','superadmin')),
    action      TEXT        NOT NULL DEFAULT 'grant'
                            CHECK (action IN ('grant','revoke')),
    granted_by  TEXT        NOT NULL,
    reason      TEXT        NOT NULL,
    -- Exactly one of these is set per row: a grant stamps granted_at, a revoke stamps revoked_at.
    granted_at  TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ,
    revokes     TEXT,
    CONSTRAINT admin_role_grant_action_time CHECK (
        (action = 'grant'  AND granted_at IS NOT NULL AND revoked_at IS NULL) OR
        (action = 'revoke' AND revoked_at IS NOT NULL AND granted_at IS NULL)
    )
);
CREATE INDEX IF NOT EXISTS idx_admin_role_grant_admin ON admin_role_grant(admin_id);

-- ── Sessions — short TTL, immediately revocable ─────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS admin_session (
    session_id  TEXT        PRIMARY KEY,
    admin_id    TEXT        NOT NULL REFERENCES admin_principal(admin_id),
    issued_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    -- NULL means live; a set value denies the session at the next request with no grace (FR2).
    revoked_at  TIMESTAMPTZ,
    mfa_factor  TEXT,
    CONSTRAINT admin_session_ttl CHECK (expires_at > issued_at)
);
CREATE INDEX IF NOT EXISTS idx_admin_session_admin ON admin_session(admin_id);

-- ── Permission map — DENY BY DEFAULT (an unlisted pair is denied) ───────────────────────────────
CREATE TABLE IF NOT EXISTS permission (
    role        TEXT NOT NULL
                     CHECK (role IN ('support','billing_ops','platform_sre','superadmin')),
    capability  TEXT NOT NULL,
    PRIMARY KEY (role, capability)
    -- A capability with no row for a role is DENIED. The map is CONFIGURATION mirrored from
    -- internal/adminrbac; the app's deny-by-default gate is the source of truth and this table lets an
    -- auditor read the same map. It is intentionally not the enforcement point.
);

-- ── Audit log — APPEND-ONLY, HASH-CHAINED, tamper-evident ───────────────────────────────────────
CREATE TABLE IF NOT EXISTS audit_entry (
    seq             BIGINT      PRIMARY KEY,
    prev_hash       TEXT        NOT NULL,
    entry_hash      TEXT        NOT NULL,
    actor_admin_id  TEXT        NOT NULL,
    target          TEXT        NOT NULL,
    action          TEXT        NOT NULL,
    reason          TEXT,
    params_digest   TEXT,
    result          TEXT        NOT NULL,
    impersonation_id TEXT,
    evidence        JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
    -- entry_hash = H(prev_hash || canonical(payload)); a mutation or deletion breaks the chain and is
    -- detected by Audit.Verify(). The trigger below makes UPDATE/DELETE impossible, so there is no
    -- mutate path for any role including the most privileged.
);

-- ── Impersonation sessions — reason-required, time-bounded ──────────────────────────────────────
CREATE TABLE IF NOT EXISTS impersonation_session (
    imp_id          TEXT        PRIMARY KEY,
    actor_admin_id  TEXT        NOT NULL REFERENCES admin_principal(admin_id),
    tenant_id       TEXT        NOT NULL,
    reason          TEXT        NOT NULL,
    scope           TEXT        NOT NULL DEFAULT 'read'
                                CHECK (scope IN ('read','elevated_write')),
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    ended_at        TIMESTAMPTZ,
    CONSTRAINT impersonation_bounded CHECK (expires_at > started_at)
);

-- ── Kill-switch state — the P6 loop consults this BEFORE every merge, read FAIL-CLOSED to halt ──
CREATE TABLE IF NOT EXISTS kill_switch_state (
    scope   TEXT        PRIMARY KEY,  -- 'global' | 'tenant:<id>'
    armed   BOOLEAN     NOT NULL DEFAULT FALSE,
    set_by  TEXT,
    reason  TEXT,
    set_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── GDPR requests — actionable + verifiable, non-PII tombstone keeps the chain intact ───────────
CREATE TABLE IF NOT EXISTS gdpr_request (
    request_id       TEXT        PRIMARY KEY,
    subject_ref      TEXT        NOT NULL,
    status           TEXT        NOT NULL DEFAULT 'received'
                                 CHECK (status IN ('received','executing','completed')),
    actor            TEXT        NOT NULL,
    reason           TEXT        NOT NULL,
    verification_ref TEXT,
    -- NON-PII reference kept in/around the audit chain so no audit entry is removed on erasure (FR17).
    tombstone_ref    TEXT,
    removed_count    INTEGER     NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at     TIMESTAMPTZ
);

-- ── Write-once enforcement for the append-only tables ───────────────────────────────────────────
-- A trigger, not a convention: `audit_entry` and `admin_role_grant` refuse UPDATE and DELETE for
-- every role. This is what makes "no mutate/delete path, including Superadmin" a property of the
-- STORE rather than of application care (FR5, FR15). Corrections are new appended rows.
CREATE OR REPLACE FUNCTION p8_refuse_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'append-only: % on % is refused — corrections are new rows, never edits', TG_OP, TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_audit_entry_append_only ON audit_entry;
CREATE TRIGGER trg_audit_entry_append_only
    BEFORE UPDATE OR DELETE ON audit_entry
    FOR EACH ROW EXECUTE FUNCTION p8_refuse_mutation();

DROP TRIGGER IF EXISTS trg_admin_role_grant_append_only ON admin_role_grant;
CREATE TRIGGER trg_admin_role_grant_append_only
    BEFORE UPDATE OR DELETE ON admin_role_grant
    FOR EACH ROW EXECUTE FUNCTION p8_refuse_mutation();

-- ── Migration registry row (idempotent) ─────────────────────────────────────────────────────────
INSERT INTO schema_migrations (id, name) VALUES (14, 'p8_admin_console')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
