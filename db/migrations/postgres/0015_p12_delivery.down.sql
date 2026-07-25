-- Down-migration for 0015 (P12 forge delivery record). Drops the one table, its append-only triggers
-- and function, its indexes, and the schema_migrations marker.
--
-- Note what a reversal costs, stated rather than hidden: dropping `delivery` discards the append-only
-- record of what was delivered where and whether it merged — the observable input P7 gainshare bills
-- on. That is acceptable for a down-migration (a reversal is destructive by definition) and is exactly
-- why there is NO in-band mutate/delete path: the only way to lose the record is to roll the schema
-- back, which is a deliberate operator act, not an app path. `transform` is untouched by both
-- directions.

BEGIN;

DROP TRIGGER IF EXISTS delivery_no_truncate ON delivery;
DROP TRIGGER IF EXISTS delivery_append_only ON delivery;
DROP FUNCTION IF EXISTS delivery_reject_mutation();

DROP INDEX IF EXISTS idx_delivery_lifecycle_key;
DROP INDEX IF EXISTS idx_delivery_tenant;
DROP INDEX IF EXISTS idx_delivery_id;
DROP INDEX IF EXISTS delivery_one_open_pr;

DROP TABLE IF EXISTS delivery;

DELETE FROM schema_migrations WHERE id = 15;

COMMIT;
