-- P0 Foundations — lineage & eval-results schema (Postgres).
-- Task 2.1. Modeled at M0; APPLIED live in P2/P2.5 (this file is the frozen model + the migration
-- that P2.5 runs when Postgres is stood up). See docs/decisions/storage-decision-record.md §6 and
-- docs/decisions/backend-invariants-and-migrations.md.
--
-- Dialect: PostgreSQL. The dev ledger (internal/db) is SQLite; per the storage decision, EVAL RESULTS
-- live in Postgres. The two dialects are two semantics (backend rule): this DDL is Postgres-only and is
-- NOT loaded by the SQLite Open() path.
--
-- Invariant this migration structurally enforces (NFR3, FR9/FR17): every eval_result row carries all
-- SEVEN tags NON-NULL, keyed by config_hash, with FKs to variant / node / case — so under-tagged or
-- dangling rows CANNOT be written even when application code forgets. The DB is the last line; the
-- emission boundary (internal/metricevent) is the first (defense in depth, task 2.2).

BEGIN;

-- Idempotent migration bookkeeping (mirrors the SQLite ledger's schema_migrations).
CREATE TABLE IF NOT EXISTS schema_migrations (
    id          BIGINT       PRIMARY KEY,
    name        TEXT         NOT NULL,
    applied_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- Reference / dimension tables (the FK targets)
-- ─────────────────────────────────────────────────────────────────────────────

-- A discovered workflow at an exact commit. node_id is unique only WITHIN a workflow, so node identity
-- is (workflow_id, node_id); eval_result carries workflow_id to make that FK well-defined.
CREATE TABLE workflow (
    workflow_id  TEXT         PRIMARY KEY,
    repo_url     TEXT         NOT NULL,
    commit_sha   TEXT         NOT NULL,
    language     TEXT         NOT NULL,
    ir_version   TEXT         NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- A variant is a STABLE LOGICAL label a human edits over time (PRD §8.4 / config-hash-spec §1).
CREATE TABLE variant (
    variant_id   TEXT         PRIMARY KEY,
    workflow_id  TEXT         NOT NULL REFERENCES workflow(workflow_id),
    label        TEXT         NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- A config is one fully-resolved configuration of a variant. config_hash IS the identity, so its
-- PRIMARY KEY IS the "uniqueness where a row is a configuration" the task requires: a second insert
-- of the same immutable configuration is rejected. One variant_id -> many immutable config_hash rows.
CREATE TABLE config (
    config_hash  TEXT         PRIMARY KEY
                              CHECK (config_hash ~ '^[0-9a-f]{64}$'),   -- full lowercase SHA-256 hex
    variant_id   TEXT         NOT NULL REFERENCES variant(variant_id),
    workflow_id  TEXT         NOT NULL REFERENCES workflow(workflow_id),
    ir_version   TEXT         NOT NULL,
    lineage_json JSONB        NOT NULL,   -- the resolved_config that was hashed (registry refs@version,
                                          -- ordering, provider params) so a run replays from lineage alone
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- A static node definition, identity (workflow_id, node_id) (IR Decision 1: node = static definition).
CREATE TABLE node (
    workflow_id  TEXT         NOT NULL REFERENCES workflow(workflow_id),
    node_id      TEXT         NOT NULL,
    kind         TEXT         NOT NULL DEFAULT 'static_definition',
    PRIMARY KEY (workflow_id, node_id)
);

-- An eval case (one item in an eval set).
CREATE TABLE eval_case (
    case_id      TEXT         PRIMARY KEY,
    workflow_id  TEXT         NOT NULL REFERENCES workflow(workflow_id),
    suite        TEXT         NOT NULL DEFAULT 'default',
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Content-addressed blobs: prompts/artifacts, keyed by SHA-256 of bytes, referenced NEVER inlined
-- (FR15). Object-store bytes live elsewhere; this row is the catalog entry.
CREATE TABLE blob (
    content_hash TEXT         PRIMARY KEY
                              CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    size_bytes   BIGINT       NOT NULL CHECK (size_bytes >= 0),
    media_type   TEXT         NOT NULL DEFAULT 'application/octet-stream',
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- Fact table — the eval result. This is where the seven-tag contract is enforced.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE eval_result (
    id           BIGINT       GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    -- ── the seven tags (metric-event.schema.json), ALL NOT NULL (NFR3) ──
    config_hash  TEXT         NOT NULL REFERENCES config(config_hash),   -- TAG: which configuration
    variant_id   TEXT         NOT NULL REFERENCES variant(variant_id),   -- TAG: which variant
    run_id       TEXT         NOT NULL,                                  -- TAG: which run batch
    node_id      TEXT         NOT NULL,                                  -- TAG: per-node attribution
    case_id      TEXT         NOT NULL REFERENCES eval_case(case_id),    -- TAG: per-case slice
    seed         INTEGER      NOT NULL CHECK (seed >= 0),                 -- TAG: multi-seed CIs
    ts           TIMESTAMPTZ  NOT NULL,                                   -- TAG: timestamp (trend)

    -- workflow_id is a structural helper (not one of the 7 tags) so (workflow_id, node_id) FK resolves.
    workflow_id  TEXT         NOT NULL,

    -- ── typed payload ──
    metric_name  TEXT         NOT NULL,
    value        DOUBLE PRECISION NOT NULL,
    unit         TEXT         NOT NULL,

    -- ── optional artifact reference (content-hashed, never inlined) ──
    blob_ref     TEXT         REFERENCES blob(content_hash),

    -- node identity FK (composite)
    FOREIGN KEY (workflow_id, node_id) REFERENCES node(workflow_id, node_id),

    -- Natural key => a re-run is idempotent: it neither double-writes a row nor loses attribution.
    -- (Groundwork for P2's idempotent executor.)
    CONSTRAINT eval_result_natural_key
        UNIQUE (config_hash, run_id, node_id, case_id, seed, metric_name)
);

-- Read paths: per-configuration comparison, per-node attribution, per-case/failure-cluster slicing.
CREATE INDEX idx_eval_result_config   ON eval_result (config_hash);
CREATE INDEX idx_eval_result_variant  ON eval_result (variant_id);
CREATE INDEX idx_eval_result_node     ON eval_result (workflow_id, node_id);
CREATE INDEX idx_eval_result_case     ON eval_result (case_id);
CREATE INDEX idx_eval_result_metric   ON eval_result (metric_name);

INSERT INTO schema_migrations (id, name) VALUES (1, 'p0_lineage')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
