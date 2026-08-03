-- Correct `delivery_route` to match `forgedelivery.Route`.
--
-- # What 0025 got wrong
--
-- I designed that table from the console's vocabulary rather than from the type it has to persist.
-- `forgedelivery.Route` is `{Mode, Target, ForgeKind}`, and `Route.Validate` refuses a route whose Mode
-- is not one of `ci` | `app`. The table 0025 created has neither a mode column nor any way to express
-- one — and it carries a `base_ref` column that is not a field of Route at all.
--
-- 🔴 A store over 0025's shape could not construct a valid Route. Every read would produce a zero Mode,
-- `Validate` would reject it, and the delivery surface would report every configured route as
-- unusable — a schema that can only hold invalid rows. It never shipped that way only because the store
-- was not written before the mismatch was noticed.
--
-- The lesson is the same one migration 0024 taught, in a smaller register: read the type before writing
-- the table. There is no CI job that can catch a column nothing has ever selected, which is why the
-- pgproof column fence in internal/pgmigrate lists what the STORES name — and `mode` now joins it.
--
-- Dialect: PostgreSQL.

BEGIN;

-- The delivery mode. `ci` means the customer's CI performs the delivery holding its own forge
-- credential; `app` means a hosted forge app does. The distinction decides who holds a token that can
-- write to the customer's repository, which is the whole subject of P12, so it is not defaultable —
-- a route with no mode names nobody as the deliverer.
ALTER TABLE delivery_route ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'ci';

-- The default exists only so the column can be added to a table that may already carry rows. New rows
-- are written explicitly by the store; the CHECK is what keeps the value meaningful either way.
--
-- 🔴 THE GUARDS ARE SCOPED TO THIS SCHEMA'S TABLE. `pg_constraint` is a DATABASE-WIDE catalog and
-- constraint names are unique per table, not per database — so a bare `WHERE conname = '…'` is true as
-- soon as ANY schema in the database has a constraint by that name. The proof harness (internal/pgtest)
-- gives every test package its own schema in one shared database precisely so the packages cannot see
-- each other; this predicate reached straight through that. Whichever package applied 0026 first got the
-- constraints, every other schema silently skipped them, and `go test` runs packages concurrently — so
-- which one won changed run to run. A CHECK asserted under those conditions is a coin flip, and it came
-- up tails: internal/deliveryroute's proof that the table refuses a misspelled forge failed only when
-- run alongside internal/pgmigrate, and passed alone.
--
-- The lesson is not about Postgres. An idempotency guard has to ask about the object it is guarding —
-- this one asked a question whose answer another test's schema could change.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class     t ON t.oid = c.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE c.conname = 'delivery_route_mode_known'
           AND t.relname = 'delivery_route'
           AND n.nspname = current_schema()
    ) THEN
        ALTER TABLE delivery_route ADD CONSTRAINT delivery_route_mode_known
            CHECK (mode IN ('ci', 'app'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class     t ON t.oid = c.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE c.conname = 'delivery_route_forge_known'
           AND t.relname = 'delivery_route'
           AND n.nspname = current_schema()
    ) THEN
        -- The forges Route.ForgeKind can name. gitlab and bitbucket are DECLARED but not implemented in
        -- P12; storing one is legitimate (a customer can configure it) and delivering to it is refused
        -- in Go, where the reason can be stated. The constraint's job is to keep a typo out, not to
        -- re-decide what is implemented.
        ALTER TABLE delivery_route ADD CONSTRAINT delivery_route_forge_known
            CHECK (forge IN ('github', 'gitlab', 'bitbucket'));
    END IF;
END $$;

-- base_ref is not a field of forgedelivery.Route. Dropped rather than left as an unread column: a
-- column nothing writes and nothing reads is one a future author will assume means something.
ALTER TABLE delivery_route DROP COLUMN IF EXISTS base_ref;

INSERT INTO schema_migrations (id, name) VALUES (26, 'delivery_route_mode')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
