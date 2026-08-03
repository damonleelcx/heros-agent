-- The durable billing ledger and account store — the two tables that let P7/P21 mount at all.
--
-- # Why these were the last things blocking a whole capability
--
-- internal/launch/capabilities.go left billing unmounted with the reason "its only store implementation
-- is in-memory, so mounting it would record and then forget". That is exactly right and it is worth
-- restating, because billing is the one surface where the failure is not a blank page: a charge appended
-- to a map, acknowledged to a provider, and lost on the next pod restart is a customer billed with no
-- record that it happened. The honest choice was to serve 503. This removes the reason for it.
--
-- It also gates the Stripe webhook. internal/api/p21.go refuses to register that route without a
-- durable ledger, on the grounds that publishing an internet-facing path on every deployment — including
-- air-gapped ones — to answer 503 is worse than not publishing it. That decision stands; the ledger
-- existing is what changes its input.
--
-- # The invariants that live in the schema rather than in Go
--
-- The Go ledger says the `byKey` map IS the unique constraint. Here the UNIQUE INDEX is, and that is the
-- stronger version of the same claim: two pods racing a retry cannot both insert, even if application
-- code forgets to check. An idempotency key is the SAME key handed to the payment provider, so a second
-- row is a second charge.
--
-- 🔴 WHAT MAY NOT GO IN EITHER TABLE
--
-- No card numbers, no bank details, no provider secrets. `provider_customer_handle`, `provider_ref` and
-- `amount_ref` are OPAQUE handles the provider issues — they identify a record on the provider's side
-- and carry no instrument data. A column here holding an instrument is a PCI surface this system has
-- never had and must not acquire by accident.
--
-- Dialect: PostgreSQL.

BEGIN;

CREATE TABLE billing_event (
    event_id        TEXT        PRIMARY KEY,
    customer_id     TEXT        NOT NULL,
    period          TEXT        NOT NULL,
    type            TEXT        NOT NULL,
    kind            TEXT        NOT NULL DEFAULT '',
    -- UNIQUE, and this is the load-bearing constraint of the whole table: it is the same key handed to
    -- the payment provider, so a duplicate row is a duplicate CHARGE. Enforced by the database rather
    -- than by the writer, because the writer is what races.
    idempotency_key TEXT        NOT NULL UNIQUE,
    provider_ref    TEXT        NOT NULL DEFAULT '',
    amount_ref      TEXT        NOT NULL DEFAULT '',
    -- What justified this row ("usage_record:cus_a/2026-07/sum"). NOT NULL and CHECKed non-empty: a
    -- period is reconstructable only if every row in it names its cause, and a charge nobody can trace
    -- to a reason is indistinguishable from a mistake.
    caused_by       TEXT        NOT NULL,
    reason          TEXT        NOT NULL DEFAULT '',
    quantity        DOUBLE PRECISION NOT NULL,
    status          TEXT        NOT NULL,
    -- Verified-delta refs / merge commits, for gainshare. JSONB: read back whole, never filtered.
    evidence        JSONB       NOT NULL DEFAULT '[]'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL,
    -- NULL until Settle stamps it. Nullable rather than a zero time: "not yet settled" and "settled at
    -- the zero instant" must not be the same value on a table that decides what a customer was charged.
    settled_at      TIMESTAMPTZ,

    CONSTRAINT billing_event_has_cause CHECK (length(trim(caused_by)) > 0)
);

-- The ledger is APPEND-ONLY by contract. Postgres cannot express that as a constraint, so it is stated
-- here and enforced by the store having no UPDATE except Settle, which touches only the provider refs
-- and settled_at. An UPDATE that changed customer_id, period, quantity or caused_by would rewrite what a
-- customer was charged after the fact, which is the one thing an append-only ledger exists to prevent.

-- "Every row for this customer-period, in append order" — the reconciliation read.
CREATE INDEX idx_billing_event_customer_period ON billing_event (customer_id, period, created_at);
-- "Everything still awaiting the provider" — the buffer an outage fills and recovery drains. Partial,
-- because the pending set is tiny next to the settled history and this index is read on every recovery
-- sweep.
CREATE INDEX idx_billing_event_pending ON billing_event (created_at) WHERE settled_at IS NULL;

CREATE TABLE billing_account (
    customer_id              TEXT        PRIMARY KEY,
    -- The provider's OPAQUE customer reference. Never card data.
    provider_customer_handle TEXT        NOT NULL DEFAULT '',
    active_plan_id           TEXT        NOT NULL DEFAULT '',
    -- The plan id and the config version travel together, because a plan id without its version cannot
    -- explain a closed period: the plan's contents may have changed since.
    plan_config_version      TEXT        NOT NULL DEFAULT '',
    -- Informed, recorded, REVOCABLE consent to verified-savings billing. A contract state, not a
    -- preference: gainshare may not be charged without it. consented_at is NULL when consent is absent
    -- or revoked, so the two-column pair can never say "consented, but we do not know when".
    gainshare_consent        BOOLEAN     NOT NULL DEFAULT FALSE,
    consented_at             TIMESTAMPTZ,
    created_at               TIMESTAMPTZ NOT NULL,
    status                   TEXT        NOT NULL DEFAULT '',
    suspension_reason        TEXT        NOT NULL DEFAULT '',
    suspended_at             TIMESTAMPTZ,
    -- Operator-set per-tenant allowance overrides, keyed by limit name. An ABSENT key means "no
    -- override" and resolves from plan configuration; it must never read as an allowance of zero, which
    -- is why this is a sparse document rather than a column per limit.
    quota_overrides          JSONB       NOT NULL DEFAULT '{}'::jsonb,

    CONSTRAINT billing_account_consent_has_timestamp
        CHECK (gainshare_consent = FALSE OR consented_at IS NOT NULL)
);

INSERT INTO schema_migrations (id, name) VALUES (24, 'billing_durable')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
