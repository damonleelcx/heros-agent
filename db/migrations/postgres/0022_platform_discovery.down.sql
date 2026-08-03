-- Down for 0022_platform_discovery. A review and recovery artifact — NOT something the deployed binary
-- can run: internal/pgmigrate embeds only `*.up.sql`, because a binary that can drop the customer's
-- tables on some code path is a binary that eventually does. Rollback is re-applying the prior package.
--
-- ⚠️ Dropping source_bundle removes the platform's RECORD of which source blobs it holds, not the blobs.
-- The bytes live in the blob store keyed by content_hash, and this table is what makes them findable. An
-- operator running this down migration to stop holding a customer's source must delete the blobs FIRST —
-- afterwards there is nothing left that says which ones they were.

BEGIN;

DROP TABLE IF EXISTS platform_workflow_graph;
DROP TABLE IF EXISTS source_bundle;

DELETE FROM schema_migrations WHERE id = 22;

COMMIT;
