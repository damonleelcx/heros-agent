-- Repair `billing_event_settled_has_refs`, which refuses every AUDIT row the ledger writes.
--
-- # The defect, and the exact request that hits it
--
-- 0013 wrote the constraint as a biconditional over the WHOLE table:
--
--     CHECK ((status = 'recorded') = (provider_ref IS NOT NULL AND settled_at IS NOT NULL))
--
-- and its comment states the intent precisely: "A settled row carries the provider's receipt; a pending
-- one cannot pretend to." That is right for the four types that MOVE MONEY. `billing_event` also holds
-- two types that do not — `plan_change` and `subscription_change` — and the Go model has always said so:
-- `EventType.ChargeBearing()` returns true for charge, gainshare_charge, credit and refund, and false
-- for these two.
--
-- An audit row is written `recorded` because it IS a completed fact; it has no provider_ref and no
-- settled_at because no money moved and there is no receipt to carry. Under the biconditional that is
-- unrepresentable, so the INSERT fails with 23514.
--
-- 🔴 The reachable path is the one that matters most on a paying deployment. A self-serve upgrade
-- arrives as `invoice.paid` carrying the plan in the subscription's metadata; the webhook mirrors the
-- state, then syncs the entitlement, and the sync's FIRST step is the audited plan_change row (audit
-- before effect, so a packaging change cannot take effect unexplained). That append is refused, so the
-- webhook returns non-2xx, the whole delivery is released and retried, and it fails identically every
-- time. The customer's card is charged, Stripe reports the subscription active, and the plan is never
-- granted — forever, with the endpoint looking like an outage rather than a bug.
--
-- # Why no test caught it
--
-- The suites that exercise the plan-change path run against `MemLedger`, and a Go map has no CHECK
-- constraints — including the P21 run against a REAL Stripe test account, which is real in every respect
-- except the ledger it writes to. The constraint is only reachable with a real Postgres behind the real
-- webhook, which is the configuration this repair was found in.
--
-- # Why a repair migration rather than an edit to 0013
--
-- internal/pgmigrate reads the ledger and applies only what is missing, so editing an applied file
-- changes nothing on a database that already ran it. Same reasoning as 0028.
--
-- # What is NOT being relaxed
--
-- The rule is unchanged for every charge-bearing row: a `recorded` charge still MUST carry both the
-- provider's reference and its settlement time, and a `pending` one still cannot pretend to. What the
-- repair does is stop applying a money rule to rows that are not money. The complement is constrained
-- too rather than left open — an audit row may not carry a receipt it never had, which keeps
-- `provider_ref IS NOT NULL` meaning "the provider acknowledged this" everywhere in the table.
--
-- Dialect: PostgreSQL.

BEGIN;

ALTER TABLE billing_event DROP CONSTRAINT IF EXISTS billing_event_settled_has_refs;

DO $$
BEGIN
    -- Schema-qualified, for the reason 0028 documents at length: pg_constraint is database-wide, and a
    -- bare name check is satisfied by a same-named constraint in another schema.
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class t     ON t.oid = c.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE c.conname = 'billing_event_settled_has_refs'
           AND t.relname = 'billing_event'
           AND n.nspname = current_schema()
    ) THEN
        ALTER TABLE billing_event ADD CONSTRAINT billing_event_settled_has_refs
            CHECK (
                CASE WHEN type IN ('charge', 'gainshare_charge', 'credit', 'refund')
                     -- Money rows: unchanged. Settled iff receipted.
                     THEN (status = 'recorded') = (provider_ref IS NOT NULL AND settled_at IS NOT NULL)
                     -- Audit rows: no money moved, so there is no receipt to carry — and carrying one
                     -- would make provider_ref mean two different things in one column.
                     ELSE provider_ref IS NULL AND settled_at IS NULL
                END
            );
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (34, 'repair_settled_refs_for_audit_rows')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
