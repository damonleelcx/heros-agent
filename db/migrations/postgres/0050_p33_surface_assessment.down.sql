-- Down for 0050. A review and recovery artifact only — `db/migrations/embed.go` embeds `*.up.sql` and
-- nothing else, so the deployed process cannot run this. P19 Decision 7 makes rollback "re-apply the
-- prior package", and a binary that can drop a customer's tables on some code path is a binary that
-- eventually does.

BEGIN;

DROP TABLE IF EXISTS assessment_finding;
DROP TABLE IF EXISTS assessment;

DELETE FROM schema_migrations WHERE id = 50;

COMMIT;
