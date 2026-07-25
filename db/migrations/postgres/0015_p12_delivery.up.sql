-- P12 Forge Delivery — the `delivery` record. Tasks 1.2, 4.1–4.6.
-- Spec:   openspec/changes/p12-forge-delivery/specs/delivery-record/spec.md
-- Design: openspec/changes/p12-forge-delivery/design.md Decision 4; ADR-005 blocker #2.
-- Contract: docs/decisions/p12-contracts.md §2.
--
-- Dialect: PostgreSQL 11+. EXPAND-ONLY. It ADDS exactly ONE new table (careful-table-creation: ADR-005
-- decided this one table against two alternatives — do not add a second), ALTERs no existing column,
-- drops nothing, and rewrites no row. Every statement is idempotent, so a re-run is a no-op and a new
-- binary self-heals an older database.
--
-- `transform` is NOT touched. Its immutability is what makes config_hash reproducibility checkable
-- (TestPG_Immutability_* must stay green); a delivery is a different kind of fact — a transform is
-- produced ONCE and is immutable by nature, whereas a delivery has a LIFECYCLE, and the same transform
-- may legitimately be delivered to more than one target. Forcing a lifecycle-bearing fact into an
-- immutable row was the wrong shape before the blocker existed.
--
-- Load-bearing properties, each enforced BY CONSTRUCTION rather than by application care:
--
--   * APPEND-ONLY — every delivery and every state change is a NEW row. A trigger rejects UPDATE and
--     DELETE outright, so the delivery history of a proposal is reconstructable in order and a merge
--     that is later reverted reads as a SEQUENCE rather than a silently overwritten field (Decision 4).
--   * ONE PULL REQUEST PER TARGET — a PARTIAL UNIQUE INDEX on (delivery_id) WHERE state = 'opened'
--     makes a second open pull request for one logical delivery physically impossible. This is what
--     makes idempotency hold under CONCURRENCY: two racing opens contend for one row, the loser updates
--     (Decision 5 / task 2.3 / 7.1). delivery_id is the deterministic hash of
--     (config_hash, source_revision, target).
--   * MODE IS RECORDED — every row carries whether the CI-mediated or the hosted App path opened it, so
--     an audit can answer which credential path opened a given pull request (task 4.3).
--   * A CLOSE IS NOT A MERGE — the state vocabulary is closed by CHECK, and 'merged' is recorded only
--     from an OBSERVATION (application-side, observe.go), never inferred from a pull request closing.

BEGIN;

CREATE TABLE IF NOT EXISTS delivery (
    -- seq is the append order and the row's own identity. A monotonic sequence is what lets the history
    -- be reconstructed "in order" (task 4.6) independent of clock skew on `at`.
    seq             BIGSERIAL   PRIMARY KEY,

    -- delivery_id groups every row of one logical delivery: the deterministic hash of
    -- (config_hash, source_revision, target). Same change, same target -> same id -> same lifecycle.
    delivery_id     TEXT        NOT NULL CHECK (delivery_id <> ''),

    tenant_id       TEXT        NOT NULL CHECK (tenant_id <> ''),

    -- The lifecycle key P7's gainshare join reads: (config_hash, source_revision, forge_ref). A 'merged'
    -- row for this triple is billable savings' observable input.
    config_hash     TEXT        NOT NULL CHECK (config_hash <> ''),
    source_revision TEXT        NOT NULL CHECK (source_revision <> ''),

    -- target is the canonical route string: "owner/repo" or "owner/repo#workflow". It is the third
    -- component of the idempotency key.
    target          TEXT        NOT NULL CHECK (target <> ''),

    -- forge_ref is the forge's own citation of the pull request, e.g. "owner/repo#42". Known when the
    -- pull request is opened; NON-NULL on every row because a record entry always concerns a specific
    -- pull request.
    forge_ref       TEXT        NOT NULL CHECK (forge_ref <> ''),

    -- Which credential path performed this delivery (task 4.3).
    mode            TEXT        NOT NULL CHECK (mode IN ('ci', 'app')),

    -- The lifecycle state. The vocabulary is closed so a typo cannot invent a state a consumer's switch
    -- silently mishandles. 'merged' is an observed fact, not an inference from closing.
    state           TEXT        NOT NULL
                                CHECK (state IN ('opened', 'updated', 'superseded', 'closed', 'merged', 'reverted')),

    -- actor names who or what produced this entry (the CI job id, the App installation, an operator).
    actor           TEXT        NOT NULL CHECK (actor <> ''),

    -- reason is stated on the states that need one: a supersession says why it closed the older PR; a
    -- revert says what was reverted. A supersession with no reason leaves a reviewer guessing, so the
    -- CHECK requires it.
    reason          TEXT,

    -- merge_commit is the observed merge commit, present only on a 'merged' row. A merged row with no
    -- merge commit would be an inference, not an observation; the CHECK forbids it.
    merge_commit    TEXT,

    at              TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT delivery_supersession_has_reason
        CHECK (state <> 'superseded' OR (reason IS NOT NULL AND reason <> '')),
    CONSTRAINT delivery_merge_is_observed
        CHECK (state <> 'merged' OR (merge_commit IS NOT NULL AND merge_commit <> '')),
    CONSTRAINT delivery_only_merged_has_commit
        CHECK (state = 'merged' OR merge_commit IS NULL)
);

-- THE idempotency constraint. At most one 'opened' row per logical delivery. Two concurrent opens both
-- try to insert 'opened'; one wins, the other gets a unique violation and takes the update path — so
-- exactly one pull request exists, enforced by the database rather than by a check-then-act the race
-- defeats.
CREATE UNIQUE INDEX IF NOT EXISTS delivery_one_open_pr
    ON delivery (delivery_id) WHERE state = 'opened';

-- Read paths: a delivery's full history; the console's per-tenant list; P7's join on the lifecycle key.
CREATE INDEX IF NOT EXISTS idx_delivery_id ON delivery (delivery_id, seq);
CREATE INDEX IF NOT EXISTS idx_delivery_tenant ON delivery (tenant_id, seq DESC);
CREATE INDEX IF NOT EXISTS idx_delivery_lifecycle_key
    ON delivery (config_hash, source_revision, forge_ref);

-- APPEND-ONLY, enforced rather than documented. A custom SQLSTATE so the proof can assert WHICH guard
-- fired rather than accepting "some error". Postgres reserves no class beginning 'HD'.
--   HD001 — delivery_append_only_violation
CREATE OR REPLACE FUNCTION delivery_reject_mutation() RETURNS TRIGGER
    LANGUAGE plpgsql AS $fn$
BEGIN
    RAISE EXCEPTION
        'delivery is append-only: a state change is a NEW row, never an edit; % on row % rejected',
        TG_OP, COALESCE(OLD.seq::text, '?')
        USING ERRCODE = 'HD001',
              HINT = 'Append the new state (updated/closed/merged/reverted). The prior rows are the audit trail.';
END;
$fn$;

DROP TRIGGER IF EXISTS delivery_append_only ON delivery;
CREATE TRIGGER delivery_append_only
    BEFORE UPDATE OR DELETE ON delivery
    FOR EACH ROW EXECUTE FUNCTION delivery_reject_mutation();
DROP TRIGGER IF EXISTS delivery_no_truncate ON delivery;
CREATE TRIGGER delivery_no_truncate
    BEFORE TRUNCATE ON delivery
    FOR EACH STATEMENT EXECUTE FUNCTION delivery_reject_mutation();

INSERT INTO schema_migrations (id, name) VALUES (15, 'p12_delivery')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
