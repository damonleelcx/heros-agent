-- P5 Typed contracts + Re-arrangement + Dynamic tracing — lineage, adapters, reconciliation, behavioral
-- labels, anti-patterns.
-- Spec:   openspec/changes/archive/2026-07-23-p5-contracts-rearrange-tracing/specs/{typed-contracts,rearrangement,dynamic-tracing}/spec.md
-- Design: openspec/changes/archive/2026-07-23-p5-contracts-rearrange-tracing/design.md "Data model sketch" (tasks 8.1, 8.4).
--
-- Dialect: PostgreSQL 11+. EXPAND-ONLY. It ADDS one nullable column to variant_spec (lineage) and six
-- new tables. It ALTERs no existing column, drops nothing, and rewrites no row — the additive-evolution
-- rule P0 mandated, and the backend "给已部署表加列必须走独立 ALTER" 铁律: the lineage column is a
-- standalone ADD COLUMN, nullable, so every pre-P5 row is a valid root spec with NULL parent.
--
-- Load-bearing properties (tasks 8.1, 8.4):
--
--   * Adding parent_config_hash is DDL, not a row UPDATE, so variant_spec's immutability trigger
--     (0003) does not block it; existing rows keep their bytes and read as roots.
--   * Every payload is CONTENT-HASHED, never inline: logged call inputs, call stacks, adapter defs, and
--     reconciliation reports live in the object store keyed by hash; these rows hold only the hash and
--     the P0 tag set. A trace input that might carry PII therefore never becomes column text here.
--   * The reconciliation tables have NO write path into a Variant Spec, a registry, or a node config —
--     reconciliation ENRICHES the IR additively (in the IR document / blob), it does not mutate config.

BEGIN;

-- ─────────────────────────────────────────────────────────────────────────────
-- Lineage: parent_config_hash on variant_spec (task 8.1)
-- ─────────────────────────────────────────────────────────────────────────────
-- A re-arrangement edit derives a new spec FROM a parent. The lineage is the parent's config_hash.
-- Nullable: a root spec (discovered graph, or a spec authored from scratch) has no parent. It is NOT
-- part of config_hash — lineage is how a spec was AUTHORED, not the configuration it denotes — so two
-- specs reached by different edit paths that resolve identically still share a hash (Decision, §8).
-- FK to config so a parent that is named actually exists; ON DELETE is irrelevant (config is immutable).
ALTER TABLE variant_spec
    ADD COLUMN IF NOT EXISTS parent_config_hash TEXT NULL REFERENCES config(config_hash);

CREATE INDEX IF NOT EXISTS idx_variant_spec_parent ON variant_spec (parent_config_hash)
    WHERE parent_config_hash IS NOT NULL;

-- ─────────────────────────────────────────────────────────────────────────────
-- Inserted adapters: the explicit, inspectable nodes the validator added (task 8.1)
-- ─────────────────────────────────────────────────────────────────────────────
-- An adapter is a real change to data flow, so it is recorded as a first-class row, not buried in the
-- spec JSON: which edge it bridges, which fixed catalog kind it is, and the content hash of its own
-- io_contract. params_json is the kind's parameters (e.g. rename from→to); it is small and structural,
-- so it is queryable JSONB rather than a blob.
CREATE TABLE IF NOT EXISTS inserted_adapter (
    adapter_node_id  TEXT NOT NULL,
    config_hash      TEXT NOT NULL,
    source_revision  TEXT NOT NULL,
    from_node_id     TEXT NOT NULL,
    to_node_id       TEXT NOT NULL,
    catalog_kind     TEXT NOT NULL CHECK (catalog_kind IN
        ('rename','projection','wrap','unwrap','default_fill','coerce')),
    io_contract_hash TEXT NOT NULL,          -- content hash of the adapter's in/out schema pair
    params_json      JSONB NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT inserted_adapter_pkey PRIMARY KEY (config_hash, source_revision, adapter_node_id),
    CONSTRAINT inserted_adapter_spec_fk FOREIGN KEY (config_hash, source_revision)
        REFERENCES variant_spec (config_hash, source_revision)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- Reconciliation: static candidate ↔ real run (tasks 8.1, 8.4)
-- ─────────────────────────────────────────────────────────────────────────────
-- One row per traced run's reconciliation, content-addressed by report_blob_hash so it is attributable
-- to the exact run and reproducible (§5, task 5.4).
CREATE TABLE IF NOT EXISTS reconciliation (
    run_id           TEXT NOT NULL,
    config_hash      TEXT NOT NULL,
    seed             BIGINT NOT NULL,
    ir_ref           TEXT NOT NULL,          -- content hash of the IR reconciled against
    report_blob_hash TEXT NOT NULL,          -- content hash of the full reconciliation report
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT reconciliation_pkey PRIMARY KEY (run_id)
);

-- Each static node's verdict on the traced run. confirmed = observed; unconfirmed = not seen (NOT
-- deleted — additive reconciliation, Decision 4).
CREATE TABLE IF NOT EXISTS recon_node (
    run_id  TEXT NOT NULL REFERENCES reconciliation(run_id),
    node_id TEXT NOT NULL,
    status  TEXT NOT NULL CHECK (status IN ('confirmed','unconfirmed')),
    invocation_count INT NOT NULL DEFAULT 0,
    CONSTRAINT recon_node_pkey PRIMARY KEY (run_id, node_id)
);

-- Each OBSERVED call, mapped (or not) to a static definition. invocation_index is 0..n−1: a loop is one
-- definition with n invocations, never n definitions (task 5.3). inputs/stack are content-hashed blobs.
CREATE TABLE IF NOT EXISTS recon_call (
    run_id           TEXT NOT NULL REFERENCES reconciliation(run_id),
    observed_call_id TEXT NOT NULL,
    node_id          TEXT NULL,              -- NULL for a runtime-only call with no static definition
    status           TEXT NOT NULL CHECK (status IN ('matched','runtime_only')),
    invocation_index INT  NOT NULL,
    inputs_blob_hash TEXT NULL,
    stack_blob_hash  TEXT NULL,
    CONSTRAINT recon_call_pkey PRIMARY KEY (run_id, observed_call_id)
);

-- Each reconciled edge, tagged with its origin: static, or a runtime-only edge static analysis missed
-- and the reconciler added additively.
CREATE TABLE IF NOT EXISTS recon_edge (
    run_id       TEXT NOT NULL REFERENCES reconciliation(run_id),
    from_node_id TEXT NOT NULL,
    to_node_id   TEXT NOT NULL,
    origin       TEXT NOT NULL CHECK (origin IN ('static','runtime_only')),
    CONSTRAINT recon_edge_pkey PRIMARY KEY (run_id, from_node_id, to_node_id)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- Behavioral labels + anti-patterns (tasks 8.1, 8.4)
-- ─────────────────────────────────────────────────────────────────────────────
-- A behavioral pattern CONFIRMED from trace evidence (source='behavioral'). Written back into the IR
-- additively too; this row is the queryable index. evidence lives in a content-hashed blob.
CREATE TABLE IF NOT EXISTS behavioral_label (
    subgraph_ref     TEXT NOT NULL,
    pattern          TEXT NOT NULL,
    source           TEXT NOT NULL DEFAULT 'behavioral' CHECK (source = 'behavioral'),
    confidence       DOUBLE PRECISION NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    evidence_blob_hash TEXT NOT NULL,
    taxonomy_version TEXT NOT NULL,
    run_id           TEXT NULL REFERENCES reconciliation(run_id),
    CONSTRAINT behavioral_label_pkey PRIMARY KEY (subgraph_ref, pattern)
);

-- A typed anti-pattern DIAGNOSIS (not a fix). Consumable by P5.5. evidence content-hashed.
CREATE TABLE IF NOT EXISTS anti_pattern (
    id               BIGSERIAL PRIMARY KEY,
    subgraph_ref     TEXT NOT NULL,
    kind             TEXT NOT NULL CHECK (kind IN
        ('reflection_no_improve','router_one_way','parallel_no_independence','plan_not_followed')),
    evidence_blob_hash TEXT NOT NULL,
    run_id           TEXT NULL REFERENCES reconciliation(run_id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO schema_migrations (id, name) VALUES (11, 'p5_contracts_tracing')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
