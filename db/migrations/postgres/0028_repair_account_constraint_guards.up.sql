-- Repair the two constraints 0024_billing_durable can silently skip.
--
-- # The defect
--
-- 0024 guards its two ALTER TABLE ... ADD CONSTRAINT statements with
--
--     IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'account_status_known') THEN
--
-- `pg_constraint` is a DATABASE-WIDE catalog, and constraint names are unique per table rather than per
-- database. So that predicate is satisfied as soon as ANY schema in the database holds a constraint by
-- that name — the guard asks "does this name exist anywhere" when it means "does this table have it".
--
-- 🔴 The proof harness is where this bites. internal/pgtest gives every test package its own schema in
-- ONE shared database, and says in its own doc that the isolation is structural rather than a naming
-- convention. This predicate reaches straight through it: the first package to apply 0024 creates the
-- constraints, every other schema skips them, and `go test` runs packages concurrently — so which one
-- wins changes run to run. It was found via the same pattern in 0026, where internal/deliveryroute's
-- proof that the table refuses a misspelled forge failed only when run alongside internal/pgmigrate and
-- passed when run alone.
--
-- 0026 is unreleased, so its guard was corrected in place. 0024 is not: the runner reads the ledger and
-- applies only what is missing (see internal/pgmigrate's package doc), so editing an applied file
-- changes nothing on any database that already ran it. A repair has to be its own migration.
--
-- # Why this is a no-op almost everywhere
--
-- On a real deployment — one schema — 0024's guard behaved correctly and both constraints exist. This
-- finds them and does nothing. It is written for the schema that lost the race, and to leave a
-- correctly-scoped guard in the file a future author will copy.
--
-- A plain ADD CONSTRAINT, deliberately, not NOT VALID: if a row somewhere violates the constraint, that
-- is a fact worth stopping for, and NOT VALID would record the constraint while exempting exactly the
-- rows that motivated it.
--
-- Dialect: PostgreSQL.

BEGIN;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class     t ON t.oid = c.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE c.conname = 'account_status_known'
           AND t.relname = 'account'
           AND n.nspname = current_schema()
    ) THEN
        -- Copied CHARACTER FOR CHARACTER from 0024. A repair that installs a constraint the original
        -- did not would leave two databases disagreeing about what `account` permits, under one name —
        -- worse than the skip it repairs. (My first draft of this file invented `'closed'` and dropped
        -- `''` from the permitted set, which is exactly that mistake.)
        ALTER TABLE account ADD CONSTRAINT account_status_known
            CHECK (status IN ('', 'active', 'suspended'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class     t ON t.oid = c.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE c.conname = 'account_suspension_is_explained'
           AND t.relname = 'account'
           AND n.nspname = current_schema()
    ) THEN
        ALTER TABLE account ADD CONSTRAINT account_suspension_is_explained
            CHECK (status <> 'suspended'
                   OR (suspension_reason <> '' AND suspended_at IS NOT NULL));
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (28, 'repair_account_constraint_guards')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
