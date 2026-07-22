-- P4 Eval Harness — eval sets, statistics, judge calibration, score cache, gates, spend.
-- Spec: openspec/changes/p4-eval-harness/specs/{eval-harness,eval-set-generation,scoring}/spec.md
-- Design: openspec/changes/p4-eval-harness/design.md "Data model sketch".
--
-- Dialect: PostgreSQL. EXPAND-ONLY: it ADDS tables and ADDS columns to eval_case / eval_result, and
-- WIDENS eval_result's natural key. Nothing is dropped or narrowed, so a deploy that runs this while
-- P2.5 writers are live keeps working.
--
-- Two invariants this migration structurally enforces:
--
--   1. TAG COMPLETENESS (task 6.2). eval_result already carried the seven P0 tags NOT NULL from
--      0001. P4 adds eval_set_hash NOT NULL, so a result row is attributable to an exact
--      {eval_set_hash, config_hash} pair and every leaderboard slice is a QUERY rather than a
--      re-run. A row that cannot say which eval set produced it cannot be written.
--
--   2. NO INLINE PAYLOADS (task 6.5). Case inputs, reference outputs and rendered judge prompts are
--      referenced by content hash into blob(content_hash) and never stored as column text. A
--      rendered judge prompt carries the case input verbatim; inlining it here (or in a log) would
--      scatter user data across stores with different retention.

BEGIN;

-- ─────────────────────────────────────────────────────────────────────────────
-- Eval sets (content-addressed)
-- ─────────────────────────────────────────────────────────────────────────────

-- An eval set is identified BY ITS CONTENT. Two boards citing the same eval_set_hash were measured
-- on demonstrably the same questions; a set whose reference for one case was edited gets a different
-- hash rather than silently invalidating every comparison already made against the old one.
CREATE TABLE IF NOT EXISTS eval_set (
    eval_set_hash   TEXT         PRIMARY KEY
                                 CHECK (eval_set_hash ~ '^[0-9a-f]{64}$'),
    workflow_id     TEXT         NOT NULL REFERENCES workflow(workflow_id),
    version         TEXT         NOT NULL,          -- the hashing-scheme version (heros.evalset.v1)
    ir_ref          TEXT         NOT NULL DEFAULT '',
    thresholds_json JSONB        NOT NULL DEFAULT '{}'::jsonb,
    -- Measured, not asserted. difficulty_measured distinguishes "the set is trivial" from "nobody
    -- ran a baseline"; collapsing those two is how an unmeasured set passes as a measured easy one.
    difficulty          DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (difficulty BETWEEN 0 AND 1),
    difficulty_measured BOOLEAN          NOT NULL DEFAULT FALSE,
    diversity           DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (diversity BETWEEN 0 AND 1),
    low_confidence      BOOLEAN          NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- eval_case gains its P4 columns. It existed in 0001 as (case_id, workflow_id, suite); everything
-- below is additive.
ALTER TABLE eval_case ADD COLUMN IF NOT EXISTS eval_set_hash TEXT REFERENCES eval_set(eval_set_hash);

-- Content-hashed payloads, never inline (see invariant 2).
ALTER TABLE eval_case ADD COLUMN IF NOT EXISTS input_blob_hash     TEXT REFERENCES blob(content_hash);
ALTER TABLE eval_case ADD COLUMN IF NOT EXISTS reference_blob_hash TEXT REFERENCES blob(content_hash);

-- gold | weak | none. A CHECK rather than a comment: a weak reference that could be mislabeled gold
-- by a typo would silently drive a gate, which is the exact failure the label exists to prevent.
ALTER TABLE eval_case ADD COLUMN IF NOT EXISTS reference_label TEXT NOT NULL DEFAULT 'none';
ALTER TABLE eval_case DROP CONSTRAINT IF EXISTS eval_case_reference_label_check;
ALTER TABLE eval_case ADD  CONSTRAINT eval_case_reference_label_check
    CHECK (reference_label IN ('gold', 'weak', 'none'));

-- The CLOSED failure taxonomy. Enumerated in the constraint so a generator cannot invent a slot the
-- coverage report does not track.
ALTER TABLE eval_case ADD COLUMN IF NOT EXISTS edge_case_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE eval_case DROP CONSTRAINT IF EXISTS eval_case_edge_case_kind_check;
ALTER TABLE eval_case ADD  CONSTRAINT eval_case_edge_case_kind_check
    CHECK (edge_case_kind IN ('', 'empty_input', 'malformed_input', 'tool_returns_nothing',
                              'retrieval_miss', 'context_overflow', 'adversarial_injection',
                              'boundary_value'));

ALTER TABLE eval_case ADD COLUMN IF NOT EXISTS origin         TEXT NOT NULL DEFAULT 'hand_authored';
ALTER TABLE eval_case ADD COLUMN IF NOT EXISTS path_tags_json JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE eval_case ADD COLUMN IF NOT EXISTS difficulty     DOUBLE PRECISION NOT NULL DEFAULT 0
                                               CHECK (difficulty BETWEEN 0 AND 1);

CREATE INDEX IF NOT EXISTS idx_eval_case_set   ON eval_case (eval_set_hash);
CREATE INDEX IF NOT EXISTS idx_eval_case_label ON eval_case (reference_label);
CREATE INDEX IF NOT EXISTS idx_eval_case_edge  ON eval_case (edge_case_kind);

-- ─────────────────────────────────────────────────────────────────────────────
-- eval_result — the P4 expansion of the P0/P2.5 fact table
-- ─────────────────────────────────────────────────────────────────────────────

-- NOT NULL with no default would fail against existing P2.5 rows, so the column lands with a default
-- and the CHECK below enforces the real contract on P4 rows. Every P4 writer supplies it (the Go
-- boundary refuses a row without it), and the CHECK stops anything else from writing a P4-shaped row
-- that cannot say which eval set produced it.
ALTER TABLE eval_result ADD COLUMN IF NOT EXISTS eval_set_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE eval_result DROP CONSTRAINT IF EXISTS eval_result_eval_set_hash_check;
ALTER TABLE eval_result ADD  CONSTRAINT eval_result_eval_set_hash_check
    CHECK (eval_set_hash = '' OR eval_set_hash ~ '^[0-9a-f]{64}$');

-- The case's reference label rides on the RESULT row so "exclude weak-labeled evidence" is a WHERE
-- clause instead of a join that a caller can forget to write.
ALTER TABLE eval_result ADD COLUMN IF NOT EXISTS reference_label TEXT NOT NULL DEFAULT 'none';
ALTER TABLE eval_result DROP CONSTRAINT IF EXISTS eval_result_reference_label_check;
ALTER TABLE eval_result ADD  CONSTRAINT eval_result_reference_label_check
    CHECK (reference_label IN ('gold', 'weak', 'none'));

-- The node's P3.5 pattern label, so "slice the board by pattern" is a WHERE clause.
ALTER TABLE eval_result ADD COLUMN IF NOT EXISTS pattern TEXT;

-- The rendered judge prompt, by reference.
ALTER TABLE eval_result ADD COLUMN IF NOT EXISTS judge_prompt_ref TEXT REFERENCES blob(content_hash);

CREATE INDEX IF NOT EXISTS idx_eval_result_set     ON eval_result (eval_set_hash);
CREATE INDEX IF NOT EXISTS idx_eval_result_pattern ON eval_result (pattern);
CREATE INDEX IF NOT EXISTS idx_eval_result_seed    ON eval_result (seed);
-- The leaderboard's own read path: one variant's run-scoped series for one metric on one eval set.
CREATE INDEX IF NOT EXISTS idx_eval_result_series
    ON eval_result (eval_set_hash, variant_id, metric_name, node_id);

-- ─────────────────────────────────────────────────────────────────────────────
-- Statistics
-- ─────────────────────────────────────────────────────────────────────────────

-- The aggregated mean + CI per variant x metric. n_seeds is NOT NULL because an interval without its
-- n is a number a reader cannot weigh, and method is NOT NULL because a stored interval has to stay
-- interpretable years after the code that produced it changed.
CREATE TABLE IF NOT EXISTS metric_stat (
    eval_set_hash TEXT NOT NULL REFERENCES eval_set(eval_set_hash),
    variant_id    TEXT NOT NULL REFERENCES variant(variant_id),
    config_hash   TEXT NOT NULL REFERENCES config(config_hash),
    metric_name   TEXT NOT NULL,
    mean          DOUBLE PRECISION NOT NULL,
    ci_low        DOUBLE PRECISION NOT NULL,
    ci_high       DOUBLE PRECISION NOT NULL,
    n_seeds       INTEGER NOT NULL CHECK (n_seeds >= 0),
    n_cases       INTEGER NOT NULL CHECK (n_cases >= 0),
    n_obs         INTEGER NOT NULL CHECK (n_obs  >= 0),
    method        TEXT    NOT NULL,
    confidence    DOUBLE PRECISION NOT NULL CHECK (confidence > 0 AND confidence < 1),
    computed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ci_low <= ci_high),
    PRIMARY KEY (eval_set_hash, variant_id, metric_name)
);

-- Per-seed values, retained (task 2.1). A CI whose per-seed inputs are gone cannot be audited, and
-- an unauditable CI is a number a reader has to take on faith.
CREATE TABLE IF NOT EXISTS metric_seed_value (
    eval_set_hash TEXT NOT NULL REFERENCES eval_set(eval_set_hash),
    variant_id    TEXT NOT NULL REFERENCES variant(variant_id),
    metric_name   TEXT NOT NULL,
    seed          INTEGER NOT NULL CHECK (seed >= 0),
    mean          DOUBLE PRECISION NOT NULL,
    n_cases       INTEGER NOT NULL CHECK (n_cases >= 0),
    PRIMARY KEY (eval_set_hash, variant_id, metric_name, seed)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- Judge calibration
-- ─────────────────────────────────────────────────────────────────────────────

-- n_human is NOT NULL and CHECKed >= 0 because an agreement of 0.9 over n=3 is not evidence, and
-- hiding n is how it gets treated as if it were. `calibrated` is a stored fact, not a derived one:
-- it is FALSE until a human-labeled subset was actually scored.
CREATE TABLE IF NOT EXISTS judge_calibration (
    judge_metric_name TEXT PRIMARY KEY,
    agreement         DOUBLE PRECISION NOT NULL CHECK (agreement BETWEEN -1 AND 1),
    percent_agreement DOUBLE PRECISION NOT NULL CHECK (percent_agreement BETWEEN 0 AND 1),
    n_human           INTEGER NOT NULL CHECK (n_human >= 0),
    floor             DOUBLE PRECISION NOT NULL,
    calibrated        BOOLEAN NOT NULL DEFAULT FALSE,
    computed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- A judge cannot be marked calibrated with no human labels. The gate-eligibility rule lives in
    -- one Go predicate; this is the database refusing to store the state that would fake it.
    CHECK (NOT calibrated OR n_human > 0)
);

-- The human labels themselves, so an agreement can be recomputed rather than believed.
CREATE TABLE IF NOT EXISTS judge_human_label (
    judge_metric_name TEXT NOT NULL REFERENCES judge_calibration(judge_metric_name) ON DELETE CASCADE,
    case_id           TEXT NOT NULL REFERENCES eval_case(case_id),
    score             DOUBLE PRECISION NOT NULL CHECK (score BETWEEN 0 AND 1),
    labeler           TEXT NOT NULL DEFAULT '',
    labeled_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (judge_metric_name, case_id)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- Scoring
-- ─────────────────────────────────────────────────────────────────────────────

-- The per-variant normalized value cache. Keyed by {variant, eval_set_hash} and invalidated when
-- either changes: a normalized value is only meaningful against the set it was measured on.
CREATE TABLE IF NOT EXISTS score_cache (
    variant_id       TEXT NOT NULL REFERENCES variant(variant_id),
    eval_set_hash    TEXT NOT NULL REFERENCES eval_set(eval_set_hash),
    metric_name      TEXT NOT NULL,
    normalized_value DOUBLE PRECISION NOT NULL CHECK (normalized_value BETWEEN 0 AND 1),
    raw_mean         DOUBLE PRECISION NOT NULL,
    -- The reference scale this value was normalized against, recorded so two boards can be compared
    -- by CHECKING they share a scale rather than by assuming it (design Q4: normalization is
    -- set-dependent, so adding a variant may re-normalize the others).
    scale_min        DOUBLE PRECISION NOT NULL,
    scale_max        DOUBLE PRECISION NOT NULL,
    degenerate       BOOLEAN NOT NULL DEFAULT FALSE,
    computed_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (variant_id, eval_set_hash, metric_name)
);

-- Named weight profiles. The weights are stored so a board rendered months ago can be reproduced
-- under the profile it actually used, not under today's edit of that profile's name.
CREATE TABLE IF NOT EXISTS weight_profile (
    name           TEXT PRIMARY KEY,
    w_quality      DOUBLE PRECISION NOT NULL CHECK (w_quality     >= 0),
    w_cost         DOUBLE PRECISION NOT NULL CHECK (w_cost        >= 0),
    w_latency      DOUBLE PRECISION NOT NULL CHECK (w_latency     >= 0),
    w_reliability  DOUBLE PRECISION NOT NULL CHECK (w_reliability >= 0),
    penalties_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Hard-constraint gate sets. Every limit is NULLABLE on purpose: NULL means "not configured", which
-- is a different thing from a limit of zero. A max-cost gate of 0.0 disqualifies everything, so
-- treating unset as zero would empty the board.
CREATE TABLE IF NOT EXISTS gate_set (
    name                    TEXT PRIMARY KEY,
    max_cost_per_run        DOUBLE PRECISION,
    latency_sla_ms          DOUBLE PRECISION,
    min_quality             DOUBLE PRECISION,
    quality_metric          TEXT,
    provider_allowlist_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- Eval runs and their spend
-- ─────────────────────────────────────────────────────────────────────────────

-- One measurement campaign: a variant set x an eval set x N seeds. The spend columns are on the run
-- itself so "how much did measuring this cost?" is a SELECT rather than a reconciliation.
CREATE TABLE IF NOT EXISTS eval_run (
    eval_run_id     TEXT PRIMARY KEY,
    eval_set_hash   TEXT NOT NULL REFERENCES eval_set(eval_set_hash),
    workflow_id     TEXT NOT NULL REFERENCES workflow(workflow_id),
    n_seeds         INTEGER NOT NULL CHECK (n_seeds > 0),
    units_planned   INTEGER NOT NULL DEFAULT 0 CHECK (units_planned   >= 0),
    units_completed INTEGER NOT NULL DEFAULT 0 CHECK (units_completed >= 0),
    budget_json     JSONB NOT NULL DEFAULT '{}'::jsonb,
    spend_execution_usd  DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (spend_execution_usd  >= 0),
    spend_judge_usd      DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (spend_judge_usd      >= 0),
    spend_generation_usd DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (spend_generation_usd >= 0),
    judge_calls      INTEGER NOT NULL DEFAULT 0 CHECK (judge_calls      >= 0),
    generation_calls INTEGER NOT NULL DEFAULT 0 CHECK (generation_calls >= 0),
    exhausted_json   JSONB NOT NULL DEFAULT '[]'::jsonb,
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_eval_run_set ON eval_run (eval_set_hash);

-- ─────────────────────────────────────────────────────────────────────────────
-- Coverage report, persisted per eval set
-- ─────────────────────────────────────────────────────────────────────────────

-- Coverage obligations are stored INDIVIDUALLY rather than as an achieved percentage, because the
-- percentage alone cannot answer "which branch is uncovered?" — and an unreachable obligation that
-- is merely dropped from the denominator produces a false 100%. Here it stays a row, uncovered, with
-- unreachable = TRUE.
CREATE TABLE IF NOT EXISTS coverage_item (
    eval_set_hash TEXT NOT NULL REFERENCES eval_set(eval_set_hash),
    dimension     TEXT NOT NULL CHECK (dimension IN ('path', 'node', 'edge_case')),
    item_id       TEXT NOT NULL,
    kind          TEXT NOT NULL,
    covered       BOOLEAN NOT NULL,
    unreachable   BOOLEAN NOT NULL DEFAULT FALSE,
    case_ids_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    PRIMARY KEY (eval_set_hash, dimension, item_id),
    -- A covered obligation cannot also be unreachable; that combination is a measurement bug, and
    -- storing it would let a report contradict itself.
    CHECK (NOT (covered AND unreachable))
);

INSERT INTO schema_migrations (id, name) VALUES (9, 'p4_eval_harness')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
