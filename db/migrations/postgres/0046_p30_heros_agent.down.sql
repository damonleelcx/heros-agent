-- Down for 0046.
--
-- All four tables are dropped, in FK order. A rolled-back image knows nothing about HEROS: no store
-- queries these, no surface reads them, and the default placement is `disabled`, so a deployment
-- without them behaves exactly as it did before P30 — rule-derived facts on every surface.
--
-- ⚠️ A rollback LOSES stored inferences, and the consequence is stated rather than hidden: the
-- determinism guarantee is a property of THIS STORE (D2), so the same workflow at the same revision
-- would be inferred again and could come back different. It is re-obtainable — that is what
-- re-inference is — but it is not free, and it is not silent: the caller pays provider tokens again.
--
-- 🚫 The IR facts the agent wrote are NOT dropped here. They live in `platform_workflow_graph.view_json`
-- with their `author` stamped, so a rolled-back deployment still renders them and still says who wrote
-- them. That is deliberate: dropping the record of an authored fact while leaving the fact would be the
-- one outcome worse than either.

BEGIN;

DROP INDEX IF EXISTS idx_heros_spend_tenant_time;
DROP TABLE IF EXISTS heros_spend;
DROP TABLE IF EXISTS heros_abstention;
DROP INDEX IF EXISTS idx_heros_inference_tenant;
DROP TABLE IF EXISTS heros_inference;
DROP INDEX IF EXISTS uq_heros_agent_version_active;
DROP TABLE IF EXISTS heros_agent_version;

DELETE FROM schema_migrations WHERE id = 46;

COMMIT;
