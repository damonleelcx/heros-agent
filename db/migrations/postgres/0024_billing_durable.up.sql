-- The four account columns P8 added to the MODEL and never to the schema.
--
-- # What this migration originally was, and why it was wrong
--
-- Its first draft created `billing_event` and `billing_account` from scratch, to back the durable
-- billing stores. Both already existed: migration 0013 created `account`, `usage_record`,
-- `billable_savings`, `billing_event` and `webhook_delivery` when P7 landed. The tables were there the
-- whole time; what was missing was Go code that used them.
--
-- 🔴 The bare `CREATE TABLE billing_event` would have failed with 42P07 on the first real deployment —
-- and `pgmigrate` applies migrations at BOOT, so the platform would not have started. It passed every
-- test because NO CI JOB APPLIES MIGRATIONS PAST ~0009: prove_constraints.py and prove_slices.py stop at
-- 0001, the pgproof tests hand-list their files, and internal/pgmigrate is not in the CI package list.
-- A migration that has never been applied is not a tested migration.
--
-- # What is actually missing
--
-- `account.Account` grew four fields for P8's operator console — Status, SuspensionReason, SuspendedAt
-- and QuotaOverrides — and 0013's `account` table has none of them. The in-memory store held them, so
-- nothing noticed. A durable store that silently dropped them would let an operator suspend a tenant,
-- see it applied, and find the account active again after the next restart.
--
-- Dialect: PostgreSQL.

BEGIN;

-- Empty means active, matching account.Status's zero value. NOT NULL with a default rather than
-- nullable: "no status" and "active" are the same state here, and two spellings for one state is how a
-- suspended-check eventually misses a row.
ALTER TABLE account ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT '';

-- The operator's recorded reason, and when it was applied. Both are cleared on reactivation.
--
-- A suspension REQUIRES a reason — "why is this tenant halted" is the first question asked when the
-- customer calls, and an unexplained suspension is indistinguishable from a mistake. The CHECK makes
-- that unrepresentable rather than merely enforced in Go, which is where 0013 put every other invariant
-- on this table.
ALTER TABLE account ADD COLUMN IF NOT EXISTS suspension_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE account ADD COLUMN IF NOT EXISTS suspended_at TIMESTAMPTZ;

-- Operator-set per-tenant allowance overrides, keyed by limit name.
--
-- A sparse document, not a column per limit: an ABSENT key means "resolve from the plan" and a zero
-- means "an allowance of nothing". Those are opposite instructions, and only a sparse shape can express
-- the first one.
ALTER TABLE account ADD COLUMN IF NOT EXISTS quota_overrides JSONB NOT NULL DEFAULT '{}'::jsonb;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'account_status_known') THEN
        ALTER TABLE account ADD CONSTRAINT account_status_known
            CHECK (status IN ('', 'active', 'suspended'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'account_suspension_is_explained') THEN
        ALTER TABLE account ADD CONSTRAINT account_suspension_is_explained
            CHECK (status <> 'suspended'
                   OR (suspension_reason <> '' AND suspended_at IS NOT NULL));
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (24, 'billing_durable')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
