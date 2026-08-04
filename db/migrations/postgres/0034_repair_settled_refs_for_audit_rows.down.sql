-- Down for 0034_repair_settled_refs_for_audit_rows. A review and recovery artifact — NOT something the
-- deployed binary can run: internal/pgmigrate embeds only `*.up.sql`.
--
-- ⚠️ This restores 0013's biconditional, which refuses every `plan_change` and `subscription_change`
-- row the ledger writes. The visible consequence is that self-serve upgrades stop completing: the
-- webhook's entitlement sync cannot write its audit row, the delivery is released and retried, and a
-- customer who has been charged is never granted the plan they bought.
--
-- It will also FAIL outright if any audit row already exists, because the restored constraint is
-- validated against the current table — which is the honest outcome: there is no way back to a rule
-- those rows never satisfied.

BEGIN;

ALTER TABLE billing_event DROP CONSTRAINT IF EXISTS billing_event_settled_has_refs;

ALTER TABLE billing_event ADD CONSTRAINT billing_event_settled_has_refs
    CHECK ((status = 'recorded') = (provider_ref IS NOT NULL AND settled_at IS NOT NULL));

DELETE FROM schema_migrations WHERE id = 34;

COMMIT;
