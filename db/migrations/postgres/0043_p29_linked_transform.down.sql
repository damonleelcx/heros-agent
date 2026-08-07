-- Down for 0043.
--
-- The TABLE is dropped, unlike 0042's column, and the difference is which direction the risk runs.
--
-- A rolled-back image knows nothing about `linked_transform`: no store queries it, no surface reads it,
-- and no code path would notice it either present or absent. Dropping it therefore breaks nothing that
-- is running, and leaving it would leave a table nobody is watching.
--
-- ⚠️ A rollback LOSES transmitted transform receipts. That is stated rather than hidden. They are
-- re-obtainable — `heros apply --link-receipt` regenerates one from the same configuration and revision,
-- because the engine is a pure function of the two — so the recovery is a command a customer can run
-- rather than data that is gone. That is precisely why this table can be dropped and 0041's password
-- tables could not.

BEGIN;

DROP INDEX IF EXISTS idx_linked_transform_tenant;
DROP TABLE IF EXISTS linked_transform;

DELETE FROM schema_migrations WHERE id = 43;

COMMIT;
