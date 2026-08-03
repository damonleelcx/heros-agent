-- Down for 0023_run_link_eval_evidence. A review and recovery artifact — NOT something the deployed
-- binary can run: internal/pgmigrate embeds only `*.up.sql`, because a binary that can drop the
-- customer's columns on some code path is a binary that eventually does. Rollback is re-applying the
-- prior package.
--
-- ⚠️ This DISCARDS evidence, not just capability. The scores stay; what is destroyed is the case count,
-- the gate verdict and the per-node attribution behind them — and those cannot be re-derived from the
-- scores, which is the whole reason they had to be transmitted. Re-linking the affected runs from a CLI
-- that still has them is the only way back.

BEGIN;

DROP INDEX IF EXISTS idx_run_link_workflow_linked_at;

ALTER TABLE run_link DROP COLUMN IF EXISTS per_node_json;
ALTER TABLE run_link DROP COLUMN IF EXISTS eval_single_seed;
ALTER TABLE run_link DROP COLUMN IF EXISTS eval_gate_failures;
ALTER TABLE run_link DROP COLUMN IF EXISTS eval_gate_outcome;
ALTER TABLE run_link DROP COLUMN IF EXISTS eval_seed_count;
ALTER TABLE run_link DROP COLUMN IF EXISTS eval_case_count;

DELETE FROM schema_migrations WHERE id = 23;

COMMIT;
