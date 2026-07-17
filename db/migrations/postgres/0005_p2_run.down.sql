-- Rollback for 0005_p2_run.up.sql (tasks 3.9, 5.2). Drops in reverse dependency order.
--
-- registry_reject_mutation() belongs to 0002 and is still used by the registries, variant_spec, and
-- transform — dropping it here would make rolling back 0005 break every migration below it.

BEGIN;

DROP TABLE IF EXISTS node_execution;
DROP TABLE IF EXISTS run;

DELETE FROM schema_migrations WHERE id = 5;

COMMIT;
