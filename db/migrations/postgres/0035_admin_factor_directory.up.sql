-- The operator MFA enrolment directory — the one admin-identity store that has no table anywhere.
--
-- # What was actually missing
--
-- Migration 0014 created `admin_principal`, `admin_role_grant` and `admin_session` when P8 landed, so
-- the identity and authorization records have had tables the whole time and needed only Go code. The
-- ENROLMENT directory is different: `adminidentity.FactorStore` arrived with P22 task 6.2, its only
-- implementation is a `map[string][]EnrolledFactor`, and no migration ever gave it a table.
--
-- 🔴 Why that is not survivable on a federated deployment. `NewAuthenticatorFor` refuses to construct a
-- real OIDC/SAML provider without a platform-verified factor, so on this deployment EVERY sign-in reads
-- this directory. Held in a map the sequence is: the operator enrols, the pod restarts for any reason at
-- all, and the directory is empty. Nothing is logged, because from the process's point of view nothing
-- failed — it started with an empty map. The operator is then locked out permanently rather than
-- temporarily: enrolling a factor requires a session, issuing a session requires a factor, and the
-- bootstrap that broke the deadlock the first time has already been run. A restart is unrecoverable
-- without a redeploy. That is the failure this table exists to remove.
--
-- # AN INDEX, NEVER A CREDENTIAL — which decides every column here
--
-- `secret_name` is the reserved LOGICAL NAME the secrets manager holds a TOTP seed under
-- (`admin_totp_seed/<admin_id>`), and the seed itself is deliberately absent. A directory row carrying a
-- seed would make this table a credential store with an ordinary backup policy — dumped by pg_dump,
-- copied to a laptop, kept in a backup bucket for a year — which is exactly what routing the seed
-- through the secrets manager exists to prevent. There is no column here a seed would fit in, and the
-- CHECK below makes a TOTP row without its logical name impossible rather than merely discouraged.
--
-- A WebAuthn row is the opposite case and holds its material openly: `credential_id` and
-- `public_key_spki` are a handle and a PUBLIC key. Neither authenticates anybody on its own, which is
-- the property that makes WebAuthn worth preferring over TOTP in the first place.
--
-- `sign_count` is the authenticator's last-seen counter, and persisting it is the point of clone
-- detection: a counter that resets to zero on every restart detects a cloned authenticator only inside
-- one process lifetime, which on a Kubernetes deployment is a guarantee of approximately nothing.
--
-- Dialect: PostgreSQL. EXPAND-ONLY: one new table, no ALTER of an existing one, nothing dropped, every
-- statement idempotent so a re-run is a no-op and a newer binary can self-heal an older database.

BEGIN;

CREATE TABLE IF NOT EXISTS admin_factor (
    -- Derived, not free-form: a principal holds at most one row per (kind, credential), so a repeated
    -- enrolment of the same authenticator updates rather than accumulating duplicates that all verify.
    factor_id       TEXT        PRIMARY KEY CHECK (factor_id <> ''),
    admin_id        TEXT        NOT NULL REFERENCES admin_principal(admin_id),
    kind            TEXT        NOT NULL CHECK (kind IN ('webauthn','totp')),

    -- WebAuthn: a handle and a PUBLIC key. Empty for TOTP.
    credential_id   BYTEA       NOT NULL DEFAULT ''::bytea,
    public_key_spki BYTEA       NOT NULL DEFAULT ''::bytea,

    -- TOTP: the reserved LOGICAL NAME its seed is held under in the secrets manager. Never the seed.
    secret_name     TEXT        NOT NULL DEFAULT '',

    sign_count      BIGINT      NOT NULL DEFAULT 0 CHECK (sign_count >= 0),
    enrolled_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Each kind must carry what its verification needs, or the row is a factor that can never verify —
    -- which on this surface reads to an operator as "my key stopped working" rather than "that row was
    -- never valid". Refused at write time instead.
    CONSTRAINT admin_factor_material CHECK (
        (kind = 'webauthn' AND length(credential_id) > 0 AND length(public_key_spki) > 0) OR
        (kind = 'totp'     AND secret_name <> '')
    )
);
CREATE INDEX IF NOT EXISTS idx_admin_factor_admin ON admin_factor(admin_id);

INSERT INTO schema_migrations (id, name) VALUES (35, 'admin_factor_directory')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
