-- Down for 0020_p11_run_links. A review and recovery artifact — NOT something the deployed binary can
-- run: internal/pgmigrate embeds only `*.up.sql`, because a binary that can drop the customer's tables
-- on some code path is a binary that eventually does. Rollback is re-applying the prior package.

BEGIN;

DROP TABLE IF EXISTS run_link_coverage;
DROP TABLE IF EXISTS run_link;

DELETE FROM schema_migrations WHERE id = 20;

COMMIT;
