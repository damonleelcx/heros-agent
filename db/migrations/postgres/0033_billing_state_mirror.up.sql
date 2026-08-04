-- The mirrored provider billing state — the one P21 store that has no table anywhere.
--
-- # What was actually missing
--
-- Migration 0013 created `webhook_delivery` when P7 landed, so the dedupe table has existed the whole
-- time and only needed Go code (the same shape as `billing_event` and ledger_pg.go — see 0024's header).
-- `billing.BillingState` is different: it arrived with P21 task 4.3, its only implementation is
-- `MemStates`, and no migration ever gave it a table. There is nothing to reuse here.
--
-- 🔴 Why that is not survivable once a deployment collects money. This is what the payment-failed,
-- past-due and dunning surfaces render from, and what tells a customer WHICH CARD is on file. Held in a
-- map, the sequence is: Stripe reports a failed payment, the platform mirrors it, the container
-- restarts, and every customer is silently back to "payment fine, no card on file". Nothing is logged,
-- because from the process's point of view nothing failed — it simply started with an empty map. The
-- provider does not re-send the event, so the state is not recovered on the next delivery either; it is
-- recovered only if that customer's payment fails a SECOND time.
--
-- # Mirrored, never computed — which decides every column here
--
-- `invoice_status` and `subscription_status` are the PROVIDER'S OWN WORDS, carried verbatim, and the
-- schema deliberately puts no CHECK on either. A CHECK would be this platform asserting it knows the
-- provider's state vocabulary; the first time Stripe adds a status, the constraint rejects a webhook
-- that is telling us the truth, the endpoint returns non-2xx, and the delivery retries forever. The
-- booleans beside them (`payment_failed`, `past_due`) are the platform's own reading, and those it may
-- constrain because it owns them.
--
-- # NO CARD DATA — the same rule 0013 states, restated because this table is where it would be tempting
--
-- `payment_method_brand` and `payment_method_last4` are DISPLAY FACTS: "Visa", "4242". They are not a
-- token, not a PAN, and not enough to charge anything. There is deliberately no column here that could
-- hold one — a `payment_method_ref` would be an opaque handle and would still be the beginning of a
-- second place card identity lives. The provider handle already lives on `account`.
--
-- last4 is CHECKed to four digits precisely so the column cannot quietly become somewhere a longer
-- number fits.
--
-- Dialect: PostgreSQL. EXPAND-ONLY: one new table, no ALTER of an existing one, nothing dropped, every
-- statement idempotent so a re-run is a no-op and a newer binary can self-heal an older database.

BEGIN;

CREATE TABLE IF NOT EXISTS billing_state (
    -- One row per customer: this is a MIRROR of current state, not a history. The event log of what
    -- happened is `billing_event`, which is append-only and already carries that.
    customer_id             TEXT        PRIMARY KEY CHECK (customer_id <> ''),

    -- The provider's words, verbatim and unconstrained. See the header.
    invoice_status          TEXT        NOT NULL DEFAULT '',
    subscription_status     TEXT        NOT NULL DEFAULT '',
    -- An opaque provider handle, exactly like billing_event.provider_ref. No amount, ever.
    last_invoice_ref        TEXT        NOT NULL DEFAULT '',

    -- The platform's own reading of the two statuses above.
    payment_failed          BOOLEAN     NOT NULL DEFAULT FALSE,
    past_due                BOOLEAN     NOT NULL DEFAULT FALSE,

    -- Display facts only.
    payment_method_present  BOOLEAN     NOT NULL DEFAULT FALSE,
    payment_method_brand    TEXT        NOT NULL DEFAULT '',
    payment_method_last4    TEXT        NOT NULL DEFAULT '',

    -- The provider's event time as mirrored, not the row's write time: a webhook that arrives late must
    -- not look newer than one that arrived on time.
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 🔴 Both guards are SCHEMA-QUALIFIED, and the bare `WHERE conname = …` this file first used is a real
-- defect rather than a style point. `pg_constraint` is DATABASE-wide: a constraint of the same name in
-- any other schema satisfies the bare check, so the ALTER is skipped and the table is created WITHOUT
-- its constraint — silently, with the migration reporting success. The live-Postgres proof caught it
-- immediately (the proofs share one database and get a schema each, which is precisely the shape that
-- triggers it), and a deployment with more than one schema would hit the same thing for real. 0032 joins
-- pg_class/pg_namespace for this reason; this follows it.
DO $$
BEGIN
    -- Four digits or nothing. The column is for rendering "•••• 4242" and must not be able to hold
    -- anything longer than that.
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class t     ON t.oid = c.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE c.conname = 'billing_state_last4_is_four_digits'
           AND t.relname = 'billing_state'
           AND n.nspname = current_schema()
    ) THEN
        ALTER TABLE billing_state ADD CONSTRAINT billing_state_last4_is_four_digits
            CHECK (payment_method_last4 = '' OR payment_method_last4 ~ '^[0-9]{4}$');
    END IF;

    -- A card's brand and last4 belong to a card that is present. Their presence without the flag would
    -- render a card on file for a customer who has none, on the page where that claim matters most.
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class t     ON t.oid = c.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE c.conname = 'billing_state_card_display_needs_a_card'
           AND t.relname = 'billing_state'
           AND n.nspname = current_schema()
    ) THEN
        ALTER TABLE billing_state ADD CONSTRAINT billing_state_card_display_needs_a_card
            CHECK (payment_method_present
                   OR (payment_method_brand = '' AND payment_method_last4 = ''));
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (33, 'billing_state_mirror')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
