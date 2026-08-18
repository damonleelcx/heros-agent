-- P2.5 Metrics & Observability — eval-results expansion (task 5.3).
-- Spec: openspec/changes/archive/2026-07-31-p2.5-metrics-observability/specs/metrics-observability/spec.md.
--
-- Dialect: PostgreSQL. EXPAND-ONLY: it ADDS columns to eval_result and WIDENS the natural key. It
-- removes nothing and narrows nothing, so it is safe on a populated table (a fresh run has no rows).
--
-- Why this is additive to 0001, not a rewrite: 0001 already froze the seven NOT NULL tag columns, the
-- FKs to variant/node/case, and the config_hash key — task 5.3's core requirement is already enforced
-- there. P2.5 adds only what the evaluator seam (§7) needs: which evaluator produced a row, and the
-- content-hash references to the input/output it scored (never the bytes — Decision 6).

BEGIN;

-- evaluator_name: which evaluator emitted this quality metric. NOT NULL with a default '' so the ADD is
-- safe on existing rows; new rows always set it. The CHECK forbids the empty string on any row written
-- AFTER this migration by making it part of the widened natural key below — an unnamed evaluator's
-- output is unattributable, which is the exact un-sliceability this substrate exists to prevent.
ALTER TABLE eval_result ADD COLUMN IF NOT EXISTS evaluator_name TEXT NOT NULL DEFAULT '';

-- input/output content-hash references (Decision 6: telemetry references blobs, never inlines them).
-- Nullable and FK'd to the blob catalog, exactly like node_execution's blob references, so a row cannot
-- cite I/O that nobody can read.
ALTER TABLE eval_result ADD COLUMN IF NOT EXISTS input_blob_hash  TEXT REFERENCES blob(content_hash);
ALTER TABLE eval_result ADD COLUMN IF NOT EXISTS output_blob_hash TEXT REFERENCES blob(content_hash);

-- Widen the natural key to include evaluator_name. Two DIFFERENT evaluators legitimately emit the same
-- metric_name for the same {config, run, node, case, seed} (e.g. two scorers both reporting
-- "score"); without evaluator_name in the key the second would collide with the first and be lost. This
-- REPLACES 0001's key with a strictly WIDER one — every tuple unique under the old key stays unique, so
-- no existing row is invalidated (expand-only).
ALTER TABLE eval_result DROP CONSTRAINT IF EXISTS eval_result_natural_key;
ALTER TABLE eval_result ADD  CONSTRAINT eval_result_natural_key
    UNIQUE (config_hash, run_id, node_id, case_id, seed, evaluator_name, metric_name);

CREATE INDEX IF NOT EXISTS idx_eval_result_evaluator ON eval_result (evaluator_name);

INSERT INTO schema_migrations (id, name) VALUES (8, 'p25_eval_results')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
