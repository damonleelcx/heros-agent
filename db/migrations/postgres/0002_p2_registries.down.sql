-- Rollback for 0002_p2_registries.up.sql (task 1.1). Drops in reverse dependency order.
--
-- DROP TABLE removes each table's own triggers with it, so the row-level immutability guard does not
-- fire here — it guards published ROWS against UPDATE/DELETE, not the DDL that owns them. Dropping
-- the tables is how you un-apply the migration; deleting a row from a live registry stays rejected.
--
-- Note this is genuinely destructive of published versions, which is exactly why the up migration is
-- expand-only: rolling 0002 back is "the registries were never stood up", not "undo an edit".

BEGIN;

DROP TABLE IF EXISTS context_entry;
DROP TABLE IF EXISTS skill_entry;
DROP TABLE IF EXISTS prompt_entry;
DROP TABLE IF EXISTS model_entry;

-- Dropped after the tables that reference them (a function cannot be dropped while a trigger uses it).
DROP FUNCTION IF EXISTS registry_verify_envelope();
DROP FUNCTION IF EXISTS registry_reject_mutation();

DELETE FROM schema_migrations WHERE id = 2;

COMMIT;
