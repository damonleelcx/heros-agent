-- P7 Billing, Metering & Entitlements — account, usage_record, billable_savings, billing_event,
-- webhook_delivery.
-- Spec:   openspec/changes/p7-billing-metering/specs/{metering,entitlements,billing}/spec.md
-- Design: openspec/changes/p7-billing-metering/design.md "Data model sketch" (tasks 2.2, 3.2, 4.1,
--         6.2, 8.2).
--
-- Dialect: PostgreSQL 11+. EXPAND-ONLY. It ADDS five new tables, ALTERs no existing column, drops
-- nothing, and rewrites no row. Every statement is idempotent (`IF NOT EXISTS`), so a re-run is a
-- no-op — the guard the migration-script rule requires, and the reason a new binary can self-heal an
-- older database.
--
-- NO PLAN CONFIGURATION IS IN THIS FILE. Plan definitions — limits, SUM band, seat/retention
-- allowances and price references — ship through the CONFIG STORE, never a migration (design
-- Decision 3). The database holds only the `plan_id` + `plan_config_version` an account points at, so
-- a plan or price change needs neither a deploy nor a migration. `TestNoPlanCatalogInGitTrackedFile`
-- enumerates the git index and fails if a catalog or a priced value is ever committed.
--
-- Load-bearing properties, each enforced BY CONSTRUCTION rather than by application care:
--
--   * NEVER DOUBLE-COUNT — `usage_record` is keyed PRIMARY KEY (customer_id, period, metric). A second
--     charge-bearing row for the same tuple is not "prevented by careful code", it is unrepresentable.
--     Re-reporting a period upserts the one row (design Decision 2).
--   * NEVER DOUBLE-CHARGE — `billing_event.idempotency_key` is UNIQUE. The same key is passed to the
--     billing provider, so the duplicate is refused at BOTH layers: the row cannot persist and the
--     provider itself declines it. That closes the check-then-insert race application-level dedupe
--     leaves open (design Decision 5).
--   * WEBHOOKS PROCESSED ONCE — `webhook_delivery.provider_event_id` is the PRIMARY KEY, so a
--     redelivered webhook cannot be processed twice (design Decision 5 / FR14).
--   * CORRECTIONS ARE ADDITIVE — `billing_event` is append-only. A credit or refund is a NEW row; no
--     correction path deletes or mutates a prior record, so "what was charged, when, and why" survives
--     every correction (design Decision 6). The append-only property is enforced by a trigger below,
--     not by convention.
--   * NO CARD DATA, NO AMOUNTS — `account.provider_customer_handle`, `billing_event.provider_ref` and
--     `billing_event.amount_ref` are OPAQUE PROVIDER HANDLES. There is deliberately no money column
--     anywhere in this schema: card data stays with the PCI-compliant provider, and an amount the
--     platform never stores is an amount that cannot leak from a row into a log (design Decision 10).
--   * GAINSHARE TRACES TO ITS EVIDENCE — `billable_savings` carries `verified_delta_refs` +
--     `merge_commits`, and a row with neither is rejected by CHECK. A billed saving that cannot name
--     the verification and the merge behind it is exactly the "confident guessing" this phase forbids
--     (design Decision 8).

BEGIN;

-- ── account ──────────────────────────────────────────────────────────────────
-- One billable customer. Holds the provider's opaque customer handle, the active plan id, and the
-- config version that plan was resolved under. Both plan columns are needed: the id says WHICH plan,
-- the version says which PUBLISHED DEFINITION of it was in force, without which a closed period stops
-- being explainable after the next config publish.
CREATE TABLE IF NOT EXISTS account (
    customer_id              TEXT        PRIMARY KEY CHECK (customer_id <> ''),
    -- OPAQUE billing-provider handle. Never card data. The CHECK rejects the PAN family outright: a
    -- 12–19 digit run (with or without the separators a paste carries) cannot be stored here, so a
    -- mis-wired integration fails at the database rather than putting the platform in PCI scope.
    provider_customer_handle TEXT        NOT NULL CHECK (provider_customer_handle <> ''),
    CONSTRAINT account_handle_is_not_card_data
        CHECK (replace(replace(replace(provider_customer_handle, ' ', ''), '-', ''), '.', '')
               !~ '^[0-9]{12,19}$'),
    active_plan_id           TEXT        NOT NULL CHECK (active_plan_id <> ''),
    plan_config_version      TEXT        NOT NULL CHECK (plan_config_version <> ''),
    gainshare_consent        BOOLEAN     NOT NULL DEFAULT FALSE,
    consented_at             TIMESTAMPTZ NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Consent and its timestamp move together. A revocation that left a stale consented_at behind
    -- would read, in an audit, as still-consented.
    CONSTRAINT account_consent_timestamped
        CHECK (gainshare_consent = (consented_at IS NOT NULL))
);

-- ── usage_record ─────────────────────────────────────────────────────────────
-- The platform's system of record for WHAT WAS USED. One row per {customer, period, metric}; the
-- primary key IS the never-double-count guarantee.
CREATE TABLE IF NOT EXISTS usage_record (
    customer_id          TEXT             NOT NULL REFERENCES account (customer_id),
    -- period is the billing-period key ("2026-07"). Stored as the derived id, never re-typed at a call
    -- site, so two spellings of one month cannot become two rows.
    period               TEXT             NOT NULL CHECK (period ~ '^[0-9]{4}-[0-9]{2}$'),
    metric               TEXT             NOT NULL
                                          CHECK (metric IN ('sum', 'seats', 'retention', 'eval_compute')),
    quantity             DOUBLE PRECISION NOT NULL CHECK (quantity >= 0),
    -- source_digest is the content hash of the inputs that produced quantity, so an identical
    -- re-derivation is recognized as a no-op instead of churning the row.
    source_digest        TEXT             NOT NULL CHECK (source_digest <> ''),
    reported_to_provider BOOLEAN          NOT NULL DEFAULT FALSE,
    -- OPAQUE provider usage handle; present only once the usage has actually been reported.
    provider_usage_ref   TEXT             NULL,
    updated_at           TIMESTAMPTZ      NOT NULL DEFAULT now(),
    CONSTRAINT usage_record_reported_has_ref
        CHECK (NOT reported_to_provider OR provider_usage_ref IS NOT NULL),
    PRIMARY KEY (customer_id, period, metric)
);

-- The reconciler and the revenue dashboard both sweep a period across customers.
CREATE INDEX IF NOT EXISTS idx_usage_record_period ON usage_record (period, metric);
-- The provider hand-off sweep asks for exactly one thing: what has not been reported yet.
CREATE INDEX IF NOT EXISTS idx_usage_record_unreported
    ON usage_record (customer_id, period) WHERE NOT reported_to_provider;

-- ── billable_savings ─────────────────────────────────────────────────────────
-- Verified, MERGED savings — the only thing gainshare may be billed on (design Decision 8). One row per
-- {customer, period}, so a period cannot produce two gainshare figures.
--
-- The evidence columns are not decoration. `verified_delta_refs` names the P5.5 ledger entries that
-- verified the saving and `merge_commits` names the git facts that shipped it; the CHECK below refuses
-- a non-zero saving that carries neither. A billed saving that cannot name its verification and its
-- merge is the "confident guessing" this phase exists to forbid — so the database will not store one.
CREATE TABLE IF NOT EXISTS billable_savings (
    customer_id        TEXT             NOT NULL REFERENCES account (customer_id),
    period             TEXT             NOT NULL CHECK (period ~ '^[0-9]{4}-[0-9]{2}$'),
    baseline_sum       DOUBLE PRECISION NOT NULL CHECK (baseline_sum >= 0),
    optimized_sum      DOUBLE PRECISION NOT NULL CHECK (optimized_sum >= 0),
    savings            DOUBLE PRECISION NOT NULL CHECK (savings >= 0),
    verified_delta_refs JSONB           NOT NULL DEFAULT '[]'::jsonb,
    merge_commits      JSONB            NOT NULL DEFAULT '[]'::jsonb,
    -- excluded_json records the deltas that were CONSIDERED and contributed zero, with the reason.
    -- "We looked at this and did not bill it" is the claim the trust story rests on, and an invisible
    -- exclusion is indistinguishable from an oversight.
    excluded_json      JSONB            NOT NULL DEFAULT '[]'::jsonb,
    updated_at         TIMESTAMPTZ      NOT NULL DEFAULT now(),
    CONSTRAINT billable_savings_traces_to_evidence
        CHECK (savings = 0 OR (jsonb_array_length(verified_delta_refs) > 0
                               AND jsonb_array_length(merge_commits) > 0)),
    -- savings is derived; storing it AND its components lets a reader check the arithmetic without
    -- re-running the computation, and this constraint keeps the three from ever disagreeing.
    CONSTRAINT billable_savings_arithmetic
        CHECK (savings = baseline_sum - optimized_sum),
    PRIMARY KEY (customer_id, period)
);

-- ── billing_event ────────────────────────────────────────────────────────────
-- The APPEND-ONLY audit of what was charged, when, and why. Its UNIQUE idempotency_key is the
-- never-double-charge guarantee at the storage layer; the same key is handed to the provider, so a
-- duplicate is refused at both layers and the check-then-insert race is closed by construction.
--
-- There is deliberately NO amount column. `amount_ref` and `provider_ref` are opaque provider handles,
-- so the platform can point AT an amount without ever storing money — an amount never held is an
-- amount that cannot leak from a row into a log.
CREATE TABLE IF NOT EXISTS billing_event (
    event_id        TEXT             PRIMARY KEY,
    customer_id     TEXT             NOT NULL REFERENCES account (customer_id),
    period          TEXT             NULL CHECK (period IS NULL OR period ~ '^[0-9]{4}-[0-9]{2}$'),
    type            TEXT             NOT NULL
                                     CHECK (type IN ('charge', 'gainshare_charge', 'credit', 'refund',
                                                     'subscription_change', 'plan_change')),
    kind            TEXT             NULL CHECK (kind IS NULL OR kind IN ('subscription', 'metered', 'gainshare')),
    -- THE never-double-charge constraint.
    idempotency_key TEXT             NOT NULL UNIQUE CHECK (idempotency_key <> ''),
    -- Opaque provider handles. NULL until the provider confirms (the write-ahead `pending` state).
    provider_ref    TEXT             NULL,
    amount_ref      TEXT             NULL,
    -- caused_by names the platform record that justified this row — what makes a period reconstructable.
    caused_by       TEXT             NOT NULL CHECK (caused_by <> ''),
    reason          TEXT             NULL,
    quantity        DOUBLE PRECISION NOT NULL DEFAULT 0,
    status          TEXT             NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'recorded')),
    -- evidence_json holds the verified-delta refs + merge commits behind a gainshare charge.
    evidence_json   JSONB            NOT NULL DEFAULT '[]'::jsonb,
    created_at      TIMESTAMPTZ      NOT NULL DEFAULT now(),
    settled_at      TIMESTAMPTZ      NULL,
    -- A correction that explains nothing is indistinguishable from a mistake.
    CONSTRAINT billing_event_correction_has_reason
        CHECK (type NOT IN ('credit', 'refund') OR (reason IS NOT NULL AND reason <> '')),
    -- A settled row carries the provider's receipt; a pending one cannot pretend to.
    CONSTRAINT billing_event_settled_has_refs
        CHECK ((status = 'recorded') = (provider_ref IS NOT NULL AND settled_at IS NOT NULL)),
    -- A gainshare charge MUST trace to the verified-delta ledger entries and merges behind it. A billed
    -- saving that cannot name its evidence is the "confident guessing" this phase exists to forbid.
    CONSTRAINT gainshare_charge_traces_to_evidence
        CHECK (type <> 'gainshare_charge' OR jsonb_array_length(evidence_json) > 0)
);

CREATE INDEX IF NOT EXISTS idx_billing_event_customer_period ON billing_event (customer_id, period);
CREATE INDEX IF NOT EXISTS idx_billing_event_type ON billing_event (type, status);

-- APPEND-ONLY, enforced rather than documented.
--
-- The trigger permits exactly ONE transition: completing a write-ahead row with the provider's receipt
-- (pending -> recorded, provider_ref/amount_ref/settled_at NULL -> value). Everything else — changing a
-- customer, a period, a type, a kind, a key, a quantity, a cause, or DELETING a row — is rejected, so a
-- correction has no choice but to be a new row.
--
-- Why a trigger and not a convention: reversibility must not depend on anyone remembering what they
-- mutated. A revoked UPDATE grant would also work, but it would block the legitimate settlement too;
-- this permits the one completion and forbids the rest.
CREATE OR REPLACE FUNCTION billing_event_reject_mutation() RETURNS TRIGGER AS $$
BEGIN
    IF (TG_OP = 'DELETE') THEN
        RAISE EXCEPTION 'billing_event is append-only: a billing error is corrected by an additive credit or refund, never by deleting row %', OLD.event_id
            USING ERRCODE = 'restrict_violation';
    END IF;
    IF (OLD.event_id, OLD.customer_id, OLD.type, OLD.idempotency_key, OLD.caused_by, OLD.quantity)
       IS DISTINCT FROM
       (NEW.event_id, NEW.customer_id, NEW.type, NEW.idempotency_key, NEW.caused_by, NEW.quantity)
       OR OLD.period IS DISTINCT FROM NEW.period
       OR OLD.kind IS DISTINCT FROM NEW.kind
       OR OLD.created_at IS DISTINCT FROM NEW.created_at THEN
        RAISE EXCEPTION 'billing_event is append-only: row % may not be revised (only the provider receipt may be completed)', OLD.event_id
            USING ERRCODE = 'restrict_violation';
    END IF;
    IF OLD.status = 'recorded' THEN
        RAISE EXCEPTION 'billing_event % is already settled: a second provider receipt may not overwrite the first', OLD.event_id
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS billing_event_append_only ON billing_event;
CREATE TRIGGER billing_event_append_only
    BEFORE UPDATE OR DELETE ON billing_event
    FOR EACH ROW EXECUTE FUNCTION billing_event_reject_mutation();

-- ── webhook_delivery ─────────────────────────────────────────────────────────
-- The webhook dedupe table. `provider_event_id` is the PRIMARY KEY, so a redelivered webhook cannot be
-- processed twice — the claim is atomic at the database, which is what makes "exactly once" survive two
-- concurrent redeliveries that would both pass a check-then-insert in application code.
--
-- Deliberately narrow: it records THAT a delivery was processed, never the payload. A provider webhook
-- body can carry customer and payment detail, and a dedupe table is not the place for it.
CREATE TABLE IF NOT EXISTS webhook_delivery (
    provider_event_id TEXT        PRIMARY KEY CHECK (provider_event_id <> ''),
    type              TEXT        NOT NULL,
    processed_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_webhook_delivery_processed ON webhook_delivery (processed_at);

INSERT INTO schema_migrations (id, name) VALUES (13, 'p7_billing_metering')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
