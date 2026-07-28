-- Down-migration for 0016 (P13 13c authored-change record). Drops the one table, its append-only
-- triggers and function, its indexes, and the schema_migrations marker.
--
-- What a reversal costs, stated rather than hidden: dropping `authored_change` discards the append-only
-- record of who changed what, from which parent, and whether it was ever verified — the attribution an
-- audit asks for and the filter every aggregate uses to exclude unverified changes. That is acceptable
-- for a down-migration (a reversal is destructive by definition) and is exactly why there is NO in-band
-- mutate/delete path: the only way to lose the record is to roll the schema back, which is a deliberate
-- operator act, not an application path.
--
-- 🔴 Rolling this back does NOT change any config_hash, any golden vector, or any existing measurement.
-- Authorship was never in the hashed shape, so removing the place it was recorded returns the system to
-- exactly its pre-13c behaviour — which is what makes 13c independently revertible. `transform`,
-- `delivery`, and `resolved_config` are untouched by both directions.

BEGIN;

DROP TRIGGER IF EXISTS authored_change_no_truncate ON authored_change;
DROP TRIGGER IF EXISTS authored_change_append_only ON authored_change;
DROP FUNCTION IF EXISTS authored_change_reject_mutation();

DROP INDEX IF EXISTS idx_authored_change_verification;
DROP INDEX IF EXISTS idx_authored_change_tenant;
DROP INDEX IF EXISTS idx_authored_change_id;
DROP INDEX IF EXISTS authored_change_one_submit;

DROP TABLE IF EXISTS authored_change;

DELETE FROM schema_migrations WHERE id = 16;

COMMIT;
