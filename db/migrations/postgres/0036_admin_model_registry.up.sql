-- The operator model registry — models and the opaque price references SUM is derived from.
--
-- # Why a new table rather than reusing `model_entry`
--
-- `model_entry` (migration 0001) already exists and looks like it should serve: it holds
-- `version_id, name, envelope, created_at`. It is the P10 PROMPT/MODEL ENVELOPE store — an opaque blob
-- keyed by version — and has no column for a provider, a price reference, or a deprecation. The
-- operator registry's record (`adminops.ModelRecord`) is a different thing that happens to share a
-- noun: model id, provider, the opaque price handle, whether it is deprecated, and a revision counter.
-- Forcing one onto the other would mean packing the operator's fields into `envelope` as JSON, which
-- makes them unqueryable and makes two unrelated write paths share a primary key.
--
-- 🔴 Why the map could not ship. `adminops.ModelRegistry` is an in-memory map with no store, and the
-- registry is a WRITE surface: an operator adds a model and its price reference, the pod restarts, and
-- the work is gone with nothing logged. Worse than losing it: SUM is derived from these price
-- references, so a restart silently changes what the platform believes a run costs.
--
-- # NO AMOUNT, EVER — which decides the price column
--
-- `price_ref` is an OPAQUE HANDLE into the provider's price catalogue ("price_ref_team_metered"), never
-- a number. The console says so on its own page, and there is deliberately no column here a currency
-- amount would fit in. A platform that stored its own copy of a price would have two answers to what a
-- run cost, and the provider's is the one that gets invoiced.
--
-- # Non-retroactivity is what `closed_period_price` is for
--
-- Repointing a model at a new price reference must NOT change what a closed metering period cost.
-- `adminops.ModelRegistry` keeps that as `closed[periodID][modelID] -> priceRef`, and without a table
-- the guarantee lasted exactly as long as the process. A closed period keeps the reference it closed
-- with, permanently, which is why this is a second table and not a column.
--
-- Dialect: PostgreSQL. EXPAND-ONLY: two new tables, no ALTER of an existing one, nothing dropped, every
-- statement idempotent so a re-run is a no-op and a newer binary can self-heal an older database.

BEGIN;

CREATE TABLE IF NOT EXISTS admin_model (
    model_id       TEXT        PRIMARY KEY CHECK (model_id <> ''),
    provider       TEXT        NOT NULL CHECK (provider <> ''),
    -- An OPAQUE provider handle. Never an amount — see the header.
    price_ref      TEXT        NOT NULL DEFAULT '',
    -- Deprecation is NOT deletion: a closed period that used this model still has to resolve, and a
    -- registry that forgot a model could not explain last quarter's SUM.
    deprecated     BOOLEAN     NOT NULL DEFAULT FALSE,
    deprecated_at  TIMESTAMPTZ,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Increments on every administered change, so an operator can tell a stale read from a current one
    -- without comparing every field.
    revision       INTEGER     NOT NULL DEFAULT 1 CHECK (revision >= 1),

    -- The two must agree, or a deprecated model has no date and the registry cannot say when it stopped
    -- being selectable — which is the one question a closed period's explanation turns on.
    CONSTRAINT admin_model_deprecated_time CHECK (
        (deprecated = TRUE AND deprecated_at IS NOT NULL) OR
        (deprecated = FALSE AND deprecated_at IS NULL)
    )
);

-- The whole of non-retroactivity: what each price reference WAS at the moment a period closed.
CREATE TABLE IF NOT EXISTS admin_model_closed_price (
    period_id  TEXT NOT NULL CHECK (period_id <> ''),
    model_id   TEXT NOT NULL CHECK (model_id <> ''),
    price_ref  TEXT NOT NULL,
    closed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (period_id, model_id)
    -- 🔴 NO foreign key to admin_model. A closed period must keep resolving even if the model is later
    -- removed from the registry entirely — that is precisely the case non-retroactivity exists for, and
    -- an FK would make the historical record deletable by editing the current one.
);

INSERT INTO schema_migrations (id, name) VALUES (36, 'admin_model_registry')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
