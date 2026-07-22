-- Rollback for 0009. Removes ONLY what 0009 added: 0008's natural key and columns, 0001's seven tag
-- columns and FKs, and the eval_case columns predating P4 are untouched.
--
-- Order matters: dependent tables before the tables they reference, and eval_case's FK to eval_set
-- before eval_set itself.

BEGIN;

DROP TABLE IF EXISTS coverage_item;
DROP TABLE IF EXISTS eval_run;
DROP TABLE IF EXISTS gate_set;
DROP TABLE IF EXISTS weight_profile;
DROP TABLE IF EXISTS score_cache;
DROP TABLE IF EXISTS judge_human_label;
DROP TABLE IF EXISTS judge_calibration;
DROP TABLE IF EXISTS metric_seed_value;
DROP TABLE IF EXISTS metric_stat;

DROP INDEX IF EXISTS idx_eval_result_series;
DROP INDEX IF EXISTS idx_eval_result_seed;
DROP INDEX IF EXISTS idx_eval_result_pattern;
DROP INDEX IF EXISTS idx_eval_result_set;

ALTER TABLE eval_result DROP CONSTRAINT IF EXISTS eval_result_reference_label_check;
ALTER TABLE eval_result DROP CONSTRAINT IF EXISTS eval_result_eval_set_hash_check;
ALTER TABLE eval_result DROP COLUMN IF EXISTS judge_prompt_ref;
ALTER TABLE eval_result DROP COLUMN IF EXISTS pattern;
ALTER TABLE eval_result DROP COLUMN IF EXISTS reference_label;
ALTER TABLE eval_result DROP COLUMN IF EXISTS eval_set_hash;

DROP INDEX IF EXISTS idx_eval_case_edge;
DROP INDEX IF EXISTS idx_eval_case_label;
DROP INDEX IF EXISTS idx_eval_case_set;

ALTER TABLE eval_case DROP CONSTRAINT IF EXISTS eval_case_edge_case_kind_check;
ALTER TABLE eval_case DROP CONSTRAINT IF EXISTS eval_case_reference_label_check;
ALTER TABLE eval_case DROP COLUMN IF EXISTS difficulty;
ALTER TABLE eval_case DROP COLUMN IF EXISTS path_tags_json;
ALTER TABLE eval_case DROP COLUMN IF EXISTS origin;
ALTER TABLE eval_case DROP COLUMN IF EXISTS edge_case_kind;
ALTER TABLE eval_case DROP COLUMN IF EXISTS reference_label;
ALTER TABLE eval_case DROP COLUMN IF EXISTS reference_blob_hash;
ALTER TABLE eval_case DROP COLUMN IF EXISTS input_blob_hash;
ALTER TABLE eval_case DROP COLUMN IF EXISTS eval_set_hash;

DROP TABLE IF EXISTS eval_set;

DELETE FROM schema_migrations WHERE id = 9;

COMMIT;
