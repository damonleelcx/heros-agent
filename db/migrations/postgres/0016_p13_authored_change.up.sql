-- P13 13c — the `authored_change` record. Tasks 9.1, 9.4, 9.6.
-- Spec:   openspec/changes/archive/2026-08-01-p13-prompt-model-optimization/specs/authored-change/spec.md
-- Design: openspec/changes/archive/2026-08-01-p13-prompt-model-optimization/design.md Decisions 9 and 12.
--
-- Dialect: PostgreSQL 11+. EXPAND-ONLY. It ADDS exactly ONE new table, ALTERs no existing column, drops
-- nothing, and rewrites no row. Every statement is idempotent, so a re-run is a no-op and a new binary
-- self-heals an older database.
--
-- 🔴 What this table is NOT: it is not part of a configuration's identity. `config_hash` is purely
-- structural, so a configuration a person authored and a byte-identical one an operator proposed hash
-- the SAME and are the SAME measurement. Authorship answers "who did this, and may we say anything about
-- it"; it never touches the bytes that answer "which configuration is this". Nothing here is hashed, and
-- `resolved_config` gains no column. That is why adding a second origin moves no golden vector.
--
-- Why a table at all (careful-table-creation): the alternative was a column on `transform`. It is the
-- wrong shape for the same reason P12's delivery record was: a transform is produced ONCE and is
-- immutable by nature, while an authored change has a LIFECYCLE — submitted, later verified, later
-- reverted. Forcing a lifecycle-bearing fact into an immutable row loses the sequence, which is exactly
-- what an audit asks for.
--
-- Load-bearing properties, each enforced BY CONSTRUCTION rather than by application care:
--
--   * APPEND-ONLY — every state change is a NEW row. A trigger rejects UPDATE and DELETE, so a change
--     that is later reverted reads as a SEQUENCE rather than a silently overwritten field, and the
--     original record of a superseded change stays retrievable unchanged (spec: "The audit record is
--     append-only").
--   * ONE SUBMISSION PER CHANGE — a PARTIAL UNIQUE INDEX on (change_id) WHERE action = 'submitted'
--     makes a duplicate submit physically impossible. change_id is deterministic
--     (workflow_id, config_hash, actor_id), so a retried submit after a dropped response collides with
--     the row it already wrote instead of creating a second one.
--   * UNVERIFIED IS A STATE, NOT A BADGE — verification_state is a CHECKed column every aggregate
--     FILTERS ON. A badge is cosmetic and a refactor can drop it; a filter condition that disappears
--     makes a query fail loudly rather than quietly start counting changes nobody measured.
--   * ORIGIN IS RECORDED — 'user' here, and 'operator' reserved so a future operator-side record shares
--     one vocabulary rather than inventing a second.

BEGIN;

CREATE TABLE IF NOT EXISTS authored_change (
    -- seq is the append order and the row's own identity. A monotonic sequence is what lets the history
    -- be reconstructed in order independently of clock skew on `at`.
    seq                  BIGSERIAL   PRIMARY KEY,

    -- change_id groups every row of one logical authored change: the deterministic hash of
    -- (workflow_id, config_hash, actor_id).
    change_id            TEXT        NOT NULL CHECK (change_id <> ''),

    -- The lifecycle action. Closed vocabulary so a typo cannot invent a state whose consumers silently
    -- mishandle it.
    action               TEXT        NOT NULL CHECK (action IN ('submitted', 'verified', 'reverted')),

    tenant_id            TEXT        NOT NULL CHECK (tenant_id <> ''),
    -- actor_id is who authored it. NOT NULL and non-empty: an authored change nobody can attribute is
    -- the state this feature exists to end.
    actor_id             TEXT        NOT NULL CHECK (actor_id <> ''),

    workflow_id          TEXT        NOT NULL CHECK (workflow_id <> ''),

    -- parent_variant_id is the IMMUTABLE variant this change departed from. A revert re-derives from
    -- exactly this, which is what makes the undo byte-identical rather than approximate.
    parent_variant_id    TEXT        NOT NULL CHECK (parent_variant_id <> ''),

    -- config_hash is the resolved hash of the configuration this change produced. It is recorded, not
    -- computed here — the resolver is the single source of it.
    config_hash          TEXT        NOT NULL CHECK (config_hash <> ''),

    -- axis names the dimensions touched, comma-joined in the closed enum's order.
    axis                 TEXT        NOT NULL CHECK (axis <> ''),

    -- diff_ref cites the transform output. An INDIRECT reference by design: a transform is
    -- content-addressed and produced once, so a foreign key would tie a lifecycle-bearing row to an
    -- immutable one and make the immutable one undeletable for the wrong reason.
    diff_ref             TEXT,

    origin               TEXT        NOT NULL CHECK (origin IN ('user', 'operator')),

    -- forked_from_proposal records that a person corrected an operator's proposal. Both lineages stay
    -- visible — but the outcome belongs to the person, and the originating operator is NOT credited with
    -- it, or an operator's win rate degrades into a measure of how often humans fix it.
    forked_from_proposal TEXT,

    -- 🔴 THE honesty column. Every aggregate improvement / savings / quality figure filters on it.
    verification_state   TEXT        NOT NULL CHECK (verification_state IN ('unverified', 'verified')),

    -- revert_of is the change_id this row undoes, present only on a 'reverted' row.
    revert_of            TEXT,

    at                   TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- A submitted change is unverified by definition: it cannot assert its own quality, and a verdict
    -- only exists after the harness has run. The CHECK makes "submitted and already verified"
    -- unrepresentable rather than merely discouraged.
    CONSTRAINT authored_change_submit_is_unverified
        CHECK (action <> 'submitted' OR verification_state = 'unverified'),
    -- A revert names what it reverts. A revert with no subject is not an audit trail.
    CONSTRAINT authored_change_revert_names_subject
        CHECK (action <> 'reverted' OR (revert_of IS NOT NULL AND revert_of <> '')),
    CONSTRAINT authored_change_only_revert_has_subject
        CHECK (action = 'reverted' OR revert_of IS NULL)
);

-- THE idempotency constraint. At most one 'submitted' row per logical change. Two concurrent submits of
-- the same deterministic change_id contend for one row; one wins, the other gets a unique violation and
-- reports the change as already submitted — enforced by the database rather than by a check-then-act the
-- race defeats.
CREATE UNIQUE INDEX IF NOT EXISTS authored_change_one_submit
    ON authored_change (change_id) WHERE action = 'submitted';

-- Read paths: one change's full history; the tenant's list, newest first; the ledger's honesty filter.
CREATE INDEX IF NOT EXISTS idx_authored_change_id ON authored_change (change_id, seq);
CREATE INDEX IF NOT EXISTS idx_authored_change_tenant ON authored_change (tenant_id, seq DESC);
CREATE INDEX IF NOT EXISTS idx_authored_change_verification
    ON authored_change (tenant_id, verification_state);

-- APPEND-ONLY, enforced rather than documented. A custom SQLSTATE so a proof can assert WHICH guard
-- fired rather than accepting "some error". Postgres reserves no class beginning 'HD'.
--   HD002 — authored_change_append_only_violation
CREATE OR REPLACE FUNCTION authored_change_reject_mutation() RETURNS TRIGGER
    LANGUAGE plpgsql AS $fn$
BEGIN
    RAISE EXCEPTION
        'authored_change is append-only: a state change is a NEW row, never an edit; % on row % rejected',
        TG_OP, COALESCE(OLD.seq::text, '?')
        USING ERRCODE = 'HD002',
              HINT = 'Append the new action (verified/reverted). The prior rows are the audit trail.';
END;
$fn$;

DROP TRIGGER IF EXISTS authored_change_append_only ON authored_change;
CREATE TRIGGER authored_change_append_only
    BEFORE UPDATE OR DELETE ON authored_change
    FOR EACH ROW EXECUTE FUNCTION authored_change_reject_mutation();
DROP TRIGGER IF EXISTS authored_change_no_truncate ON authored_change;
CREATE TRIGGER authored_change_no_truncate
    BEFORE TRUNCATE ON authored_change
    FOR EACH STATEMENT EXECUTE FUNCTION authored_change_reject_mutation();

INSERT INTO schema_migrations (id, name) VALUES (16, 'p13_authored_change')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
