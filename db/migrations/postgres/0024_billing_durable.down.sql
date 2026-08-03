-- Down for 0024_billing_durable. A review and recovery artifact — NOT something the deployed binary can
-- run: internal/pgmigrate embeds only `*.up.sql`, because a binary that can drop the customer's columns
-- on some code path is a binary that eventually does. Rollback is re-applying the prior package.
--
-- ⚠️ This discards operator state: which tenants are suspended, why, and every per-tenant allowance
-- override. The audit chain still records that the suspensions HAPPENED, but the accounts come back
-- active, so a tenant halted for non-payment or abuse resumes serving on the next boot.

BEGIN;

ALTER TABLE account DROP CONSTRAINT IF EXISTS account_suspension_is_explained;
ALTER TABLE account DROP CONSTRAINT IF EXISTS account_status_known;

ALTER TABLE account DROP COLUMN IF EXISTS quota_overrides;
ALTER TABLE account DROP COLUMN IF EXISTS suspended_at;
ALTER TABLE account DROP COLUMN IF EXISTS suspension_reason;
ALTER TABLE account DROP COLUMN IF EXISTS status;

DELETE FROM schema_migrations WHERE id = 24;

COMMIT;
