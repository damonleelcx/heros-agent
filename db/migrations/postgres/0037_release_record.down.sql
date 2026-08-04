-- Reverses 0037. A review and recovery artifact; the deployed binary embeds only `.up.sql` files.

BEGIN;

DROP INDEX IF EXISTS idx_release_artefact_version;
DROP TABLE IF EXISTS release_artefact;
DROP TABLE IF EXISTS release_record;

DELETE FROM schema_migrations WHERE id = 37;

COMMIT;
