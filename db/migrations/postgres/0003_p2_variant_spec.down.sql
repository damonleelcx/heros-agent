-- Rollback for 0003_p2_variant_spec.up.sql (task 2.1).
--
-- DROP TABLE takes the table's own triggers with it; it does not drop registry_reject_mutation(),
-- which 0002 owns and the four registries still use. Dropping a function 0002 defined would make
-- rolling back 0003 break 0002 — expand-only cuts both ways.

BEGIN;

DROP TABLE IF EXISTS variant_spec;

DELETE FROM schema_migrations WHERE id = 3;

COMMIT;
