-- Down for 0021_p11_workflow_ir. A review and recovery artifact — NOT something the deployed binary can
-- run: internal/pgmigrate embeds only `*.up.sql`, because a binary that can drop the customer's tables
-- on some code path is a binary that eventually does. Rollback is re-applying the prior package.

BEGIN;

DROP TABLE IF EXISTS workflow_ir;

DELETE FROM schema_migrations WHERE id = 21;

COMMIT;
