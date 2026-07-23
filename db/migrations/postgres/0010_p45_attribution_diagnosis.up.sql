-- P4.5 Attribution + Diagnosis — the read-only report schema.
-- Spec: openspec/changes/p4.5-attribution-diagnosis/specs/{attribution,diagnosis}/spec.md
-- Design: openspec/changes/p4.5-attribution-diagnosis/design.md "Data model sketch" + Decision 1.
--
-- Dialect: PostgreSQL. EXPAND-ONLY: it ADDS six report tables and nothing else. It ALTERs no table
-- P0/P2/P4 created; in particular it adds NO column, constraint, trigger, or FK to variant, config,
-- variant_spec, or any registry table.
--
-- The load-bearing property of this migration is what it does NOT contain (Decision 1 / task 8.2):
--
--   * No report table has a write path into a Variant Spec, a registry, or a node config. The only
--     references these tables make to variant / config / eval_set / eval_case / blob are FOREIGN KEYS
--     — read-side integrity checks that a referenced row EXISTS. A foreign key cannot write its
--     target; there is no trigger here that updates variant/config, and no column any config store
--     reads back. A full attribution + diagnosis run therefore leaves every Variant Spec / registry /
--     config BYTE-IDENTICAL (same config_hash), which the Go load-bearing test asserts directly.
--
--   * There is no proposal table and no apply/change table. Turning a diagnosis into a change is a
--     DIFFERENT capability behind a verification gate (P5.5); the boundary lives in the data model, so
--     P4.5 cannot emit a proposal even by accident.
--
--   * Payloads are content-hashed, never inline (task 8.1). Trace excerpts, analyst prompts/rubrics,
--     and cluster embeddings are referenced by blob(content_hash); the rows hold the hash and the
--     tags, so possibly-PII payloads never scatter across the report store as column text.
--
-- Intended DB grant for the attribution/diagnosis engine (enforced operationally, documented here as
-- the contract): WRITE (INSERT) only on the six tables below; READ on the trace/span store and
-- eval_result; and NO privilege of any kind on variant, config, variant_spec, or the registries.

BEGIN;

-- ─────────────────────────────────────────────────────────────────────────────
-- attribution — per-node contribution + first-divergence, keyed per {variant, eval set, config,
-- node, case}. Append-only: a row is a measured fact about one case, never edited.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS attribution (
    variant_id      TEXT NOT NULL REFERENCES variant(variant_id),
    eval_set_hash   TEXT NOT NULL REFERENCES eval_set(eval_set_hash),
    config_hash     TEXT NOT NULL REFERENCES config(config_hash),
    node_id         TEXT NOT NULL,
    case_id         TEXT NOT NULL REFERENCES eval_case(case_id),
    contrib_success  DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (contrib_success  BETWEEN 0 AND 1),
    contrib_cost     DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (contrib_cost     BETWEEN 0 AND 1),
    contrib_latency  DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (contrib_latency  BETWEEN 0 AND 1),
    first_divergence BOOLEAN NOT NULL DEFAULT FALSE,
    computed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (variant_id, eval_set_hash, config_hash, node_id, case_id)
);
CREATE INDEX IF NOT EXISTS idx_attribution_node  ON attribution (variant_id, eval_set_hash, node_id);
CREATE INDEX IF NOT EXISTS idx_attribution_case  ON attribution (variant_id, eval_set_hash, case_id);
CREATE INDEX IF NOT EXISTS idx_attribution_first ON attribution (variant_id, eval_set_hash, first_divergence);

-- ─────────────────────────────────────────────────────────────────────────────
-- failure_cluster — named categories. cluster_id is a content hash of {variant, eval set, signature},
-- so re-running is idempotent. Embeddings live in the object store; the row holds the ref only.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS failure_cluster (
    cluster_id            TEXT PRIMARY KEY,
    variant_id            TEXT NOT NULL REFERENCES variant(variant_id),
    eval_set_hash         TEXT NOT NULL REFERENCES eval_set(eval_set_hash),
    config_hash           TEXT NOT NULL REFERENCES config(config_hash),
    signature             TEXT NOT NULL,
    label                 TEXT NOT NULL,
    size                  INTEGER NOT NULL CHECK (size >= 0),
    representative_case_id TEXT NOT NULL REFERENCES eval_case(case_id),
    member_case_ids_json  JSONB NOT NULL DEFAULT '[]'::jsonb,
    embedding_ref         TEXT REFERENCES blob(content_hash),
    computed_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_failure_cluster_variant ON failure_cluster (variant_id, eval_set_hash);

-- ─────────────────────────────────────────────────────────────────────────────
-- ablation_result — ephemeral counterfactual measurements. `ephemeral` is CHECK-pinned TRUE: an
-- ablation is NEVER a user variant, and the database refuses to store it as one. swapped_config_ref is
-- a content ref, never an applied config.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS ablation_result (
    ablation_id        TEXT PRIMARY KEY,
    variant_id         TEXT NOT NULL REFERENCES variant(variant_id),
    eval_set_hash      TEXT NOT NULL REFERENCES eval_set(eval_set_hash),
    config_hash        TEXT NOT NULL REFERENCES config(config_hash),
    node_id            TEXT NOT NULL,
    swapped_config_ref TEXT NOT NULL,
    metric             TEXT NOT NULL,
    delta_mean         DOUBLE PRECISION NOT NULL,
    ci_low             DOUBLE PRECISION NOT NULL,
    ci_high            DOUBLE PRECISION NOT NULL,
    n_seeds            INTEGER NOT NULL CHECK (n_seeds >= 0),
    verdict            TEXT NOT NULL CHECK (verdict IN ('bottleneck', 'inconclusive')),
    ephemeral          BOOLEAN NOT NULL DEFAULT TRUE CHECK (ephemeral),
    computed_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ci_low <= ci_high)
);
CREATE INDEX IF NOT EXISTS idx_ablation_variant ON ablation_result (variant_id, eval_set_hash, node_id);

-- ─────────────────────────────────────────────────────────────────────────────
-- bottleneck_flag — cost/latency Pareto dominators, keyed per {variant, eval set, config, node,
-- dimension}.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS bottleneck_flag (
    variant_id    TEXT NOT NULL REFERENCES variant(variant_id),
    eval_set_hash TEXT NOT NULL REFERENCES eval_set(eval_set_hash),
    config_hash   TEXT NOT NULL REFERENCES config(config_hash),
    node_id       TEXT NOT NULL,
    dimension     TEXT NOT NULL CHECK (dimension IN ('cost', 'latency')),
    dominance     DOUBLE PRECISION NOT NULL CHECK (dominance BETWEEN 0 AND 1),
    computed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (variant_id, eval_set_hash, config_hash, node_id, dimension)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- diagnosis — the FROZEN diagnosis record (design Q7): the exact contract P5.5's operators consume.
-- taxonomy_code + node + evidence + confidence + agreement, plus provenance and the analyst flags.
-- Prompts / trace excerpts are content-hashed refs, never inline.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS diagnosis (
    diag_id            TEXT PRIMARY KEY,
    variant_id         TEXT NOT NULL REFERENCES variant(variant_id),
    eval_set_hash      TEXT NOT NULL REFERENCES eval_set(eval_set_hash),
    config_hash        TEXT NOT NULL REFERENCES config(config_hash),
    node_id            TEXT NOT NULL,
    taxonomy_code      TEXT NOT NULL,
    taxonomy_version   TEXT NOT NULL,
    source             TEXT NOT NULL CHECK (source IN ('rule', 'analyst')),
    confidence         DOUBLE PRECISION NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    evidence_case_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- Analyst-only trust fields, reported alongside every analyst diagnosis. Zero/false for a rule
    -- diagnosis, which needs no calibration.
    agreement          DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (agreement BETWEEN -1 AND 1),
    n_human            INTEGER NOT NULL DEFAULT 0 CHECK (n_human >= 0),
    calibrated         BOOLEAN NOT NULL DEFAULT FALSE,
    analyst_flagged    BOOLEAN NOT NULL DEFAULT FALSE,
    low_confidence     BOOLEAN NOT NULL DEFAULT FALSE,
    prompt_ref         TEXT REFERENCES blob(content_hash),
    trace_excerpt_ref  TEXT REFERENCES blob(content_hash),
    computed_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- A diagnosis is never a bare label: it must carry at least one evidence case (task 6.x). The
    -- database refuses an evidence-free diagnosis.
    CHECK (jsonb_array_length(evidence_case_ids_json) >= 1),
    -- A rule diagnosis is reproducible and carries no analyst trust fields; an analyst diagnosis may.
    CHECK (source = 'analyst' OR (agreement = 0 AND n_human = 0 AND NOT analyst_flagged))
);
CREATE INDEX IF NOT EXISTS idx_diagnosis_variant ON diagnosis (variant_id, eval_set_hash, node_id);
CREATE INDEX IF NOT EXISTS idx_diagnosis_code    ON diagnosis (taxonomy_code);

-- ─────────────────────────────────────────────────────────────────────────────
-- analyst_cal — the analyst's measured trust. n_human NOT NULL and CHECKed because an agreement over
-- n=3 is not evidence, and `calibrated` cannot be true with zero human labels — the same discipline
-- as judge_calibration (0009).
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS analyst_cal (
    analyst_metric    TEXT PRIMARY KEY,
    agreement         DOUBLE PRECISION NOT NULL CHECK (agreement BETWEEN -1 AND 1),
    percent_agreement DOUBLE PRECISION NOT NULL CHECK (percent_agreement BETWEEN 0 AND 1),
    n_human           INTEGER NOT NULL CHECK (n_human >= 0),
    calibrated        BOOLEAN NOT NULL DEFAULT FALSE,
    floor             DOUBLE PRECISION NOT NULL,
    computed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (NOT calibrated OR n_human > 0)
);

INSERT INTO schema_migrations (id, name) VALUES (10, 'p45_attribution_diagnosis')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
