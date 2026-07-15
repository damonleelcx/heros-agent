-- Rollback for 0001_p0_lineage.up.sql (task 2.1). Drops in reverse dependency order.
-- Everything P0 ships is versioned text; rollback is a git revert + this down migration.

BEGIN;

DROP TABLE IF EXISTS eval_result;
DROP TABLE IF EXISTS blob;
DROP TABLE IF EXISTS eval_case;
DROP TABLE IF EXISTS node;
DROP TABLE IF EXISTS config;
DROP TABLE IF EXISTS variant;
DROP TABLE IF EXISTS workflow;

DELETE FROM schema_migrations WHERE id = 1;

COMMIT;
