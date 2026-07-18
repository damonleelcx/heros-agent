-- Rollback for 0008 (task 5.3). Restores 0001's natural key and drops the P2.5-added columns.
-- Expand-only in reverse: it removes ONLY what 0008 added; 0001's seven tag columns, FKs, and
-- config_hash key are untouched.

BEGIN;

DROP INDEX IF EXISTS idx_eval_result_evaluator;

-- Restore 0001's narrower natural key before dropping evaluator_name (the wide key references it).
ALTER TABLE eval_result DROP CONSTRAINT IF EXISTS eval_result_natural_key;
ALTER TABLE eval_result ADD  CONSTRAINT eval_result_natural_key
    UNIQUE (config_hash, run_id, node_id, case_id, seed, metric_name);

ALTER TABLE eval_result DROP COLUMN IF EXISTS output_blob_hash;
ALTER TABLE eval_result DROP COLUMN IF EXISTS input_blob_hash;
ALTER TABLE eval_result DROP COLUMN IF EXISTS evaluator_name;

DELETE FROM schema_migrations WHERE id = 8;

COMMIT;
