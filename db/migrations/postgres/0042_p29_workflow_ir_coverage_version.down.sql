-- Down for 0042.
--
-- 🔴 The column is NOT dropped, and the asymmetry is the same one 0041 records.
--
-- Rolling back means running the PRIOR image against this schema. That image's `PGWorkflowIRStore`
-- selects a fixed column list that does not include `coverage_version`, so an extra nullable column is
-- invisible to it. What is NOT survivable is the reverse: dropping the column while any replica of the
-- new image is still serving turns every structure read into `42703 column does not exist`, which takes
-- the graph, the projection and every axis surface down at once — during the exact window a rollback is
-- meant to be making things better.
--
-- A down migration exists to make a rollback safe, not to make the schema symmetric. An unread nullable
-- column costs nothing.

BEGIN;

DELETE FROM schema_migrations WHERE id = 42;

COMMIT;
