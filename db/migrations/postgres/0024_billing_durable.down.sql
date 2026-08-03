-- Down for 0024_billing_durable. A review and recovery artifact — NOT something the deployed binary can
-- run: internal/pgmigrate embeds only `*.up.sql`, because a binary that can drop the customer's tables
-- on some code path is a binary that eventually does. Rollback is re-applying the prior package.
--
-- 🔴 THIS DESTROYS THE BILLING RECORD. Not a cache, not a derived view — the append-only ledger of what
-- every customer was charged, and the consent state that authorised it. The provider still holds its own
-- side of each transaction, so dropping this does not un-charge anyone; it destroys OUR ability to
-- explain, reconcile or correct a charge that already happened, and it destroys the record of who
-- consented to gainshare billing.
--
-- If you are reaching for this to undo a bad deploy: re-apply the prior package instead. If you are
-- reaching for it to delete a customer's data, that is a GDPR erasure and it is a scoped DELETE with an
-- audit entry, not a DROP TABLE.

BEGIN;

DROP TABLE IF EXISTS billing_account;
DROP TABLE IF EXISTS billing_event;

DELETE FROM schema_migrations WHERE id = 24;

COMMIT;
