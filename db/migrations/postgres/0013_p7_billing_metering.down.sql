-- Down-migration for 0013 (P7 billing, metering & entitlements). Drops the five P7 tables and removes
-- the schema_migrations marker. Order respects the foreign keys: dependents (webhook_delivery,
-- billing_event, billable_savings, usage_record) before the parent (account).
--
-- Note what a reversal costs here, stated rather than hidden: dropping `billing_event` discards the
-- append-only correction history, which is the one thing P7 promises never to lose in NORMAL operation.
-- That is acceptable for a down-migration (a reversal is destructive by definition) and is precisely
-- why the additive-credit path — not this file — is how a billing error is corrected.

BEGIN;

DROP TRIGGER IF EXISTS billing_event_append_only ON billing_event;
DROP FUNCTION IF EXISTS billing_event_reject_mutation();

DROP INDEX IF EXISTS idx_webhook_delivery_processed;
DROP INDEX IF EXISTS idx_billing_event_customer_period;
DROP INDEX IF EXISTS idx_billing_event_type;
DROP INDEX IF EXISTS idx_usage_record_unreported;
DROP INDEX IF EXISTS idx_usage_record_period;

DROP TABLE IF EXISTS webhook_delivery;
DROP TABLE IF EXISTS billing_event;
DROP TABLE IF EXISTS billable_savings;
DROP TABLE IF EXISTS usage_record;
DROP TABLE IF EXISTS account;

DELETE FROM schema_migrations WHERE id = 13;

COMMIT;
