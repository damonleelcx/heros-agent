-- Rollback for 0004_p2_transform.up.sql (task 3.9).
--
-- Drops only what 0004 added. registry_reject_mutation() belongs to 0002 and is still used by the
-- four registries, variant_spec, and this table's siblings — dropping it here would make rolling back
-- 0004 break 0002.

BEGIN;

DROP TABLE IF EXISTS transform;

DELETE FROM schema_migrations WHERE id = 4;

COMMIT;
