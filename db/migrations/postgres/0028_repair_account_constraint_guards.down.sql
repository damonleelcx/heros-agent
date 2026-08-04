-- Down for 0028_repair_account_constraint_guards. A review and recovery artifact — NOT something the
-- deployed binary can run: internal/pgmigrate embeds only `*.up.sql`.
--
-- ⚠️ It drops the constraints rather than restoring 0024's guard, because there is nothing to restore:
-- on a database where 0024's guard worked, these constraints are 0024's and dropping them removes a
-- protection this migration did not add. Re-applying the prior package is the correct rollback; this
-- file exists to say what would be lost.

BEGIN;

ALTER TABLE account DROP CONSTRAINT IF EXISTS account_suspension_is_explained;
ALTER TABLE account DROP CONSTRAINT IF EXISTS account_status_known;

DELETE FROM schema_migrations WHERE id = 28;

COMMIT;
