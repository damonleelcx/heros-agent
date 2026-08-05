-- Reverses 0038. A review and recovery artifact; the deployed binary embeds only `.up.sql` files.
--
-- 🔴 Note what this cannot undo and does not pretend to. Dropping `platform_user` and `membership`
-- destroys every person and every organization created after the upgrade — which is exactly why P19
-- Decision 7 makes rollback "re-apply the prior package" rather than "run the down migration". The
-- prior binary ignores every column and table below, so a rollback needs none of this.

BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint c
         WHERE c.conrelid = 'account'::regclass
           AND c.conname = 'account_handle_required_when_plan_charges'
    ) THEN
        ALTER TABLE account DROP CONSTRAINT account_handle_required_when_plan_charges;
    END IF;
END $$;

ALTER TABLE account DROP COLUMN IF EXISTS plan_charges;

-- Restore 0013's non-empty CHECK, but ONLY if nothing violates it. A free account holds `''` in this
-- column, and re-adding the constraint over one would abort the whole down-migration — which is the wrong
-- failure: `plan_charges` has already been dropped by the line above, so there is no longer any way to
-- express "absent because the plan charges nothing", and the row is not repairable from inside this file.
--
-- 🔴 The honest posture is the one the header already states: rollback is RE-APPLY THE PRIOR PACKAGE, and
-- the prior binary reads `''` perfectly well (task 10.1 — that is the whole reason absence is `''` and not
-- NULL). This file is a review and recovery artifact. It restores the constraint where it can, and says so
-- where it cannot, rather than refusing to run.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM account WHERE provider_customer_handle = '') THEN
        RAISE NOTICE 'account: leaving the non-empty handle CHECK off — % row(s) hold an empty handle '
                     '(free accounts). They are readable by every binary; the constraint is not.',
                     (SELECT count(*) FROM account WHERE provider_customer_handle = '');
    ELSE
        ALTER TABLE account ADD CONSTRAINT account_provider_customer_handle_check
            CHECK (provider_customer_handle <> '');
    END IF;
END $$;

DROP INDEX IF EXISTS idx_eval_run_tenant;
DROP INDEX IF EXISTS idx_variant_spec_tenant;
DROP INDEX IF EXISTS idx_run_tenant;

-- proposal.tenant_id is 0025's, NOT this migration's. Dropping it here would delete another phase's
-- column and take the proposals console with it.
ALTER TABLE eval_run     DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE variant_spec DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE run          DROP COLUMN IF EXISTS tenant_id;

DROP INDEX IF EXISTS idx_console_session_user;
DROP TABLE IF EXISTS console_session;

DROP INDEX IF EXISTS idx_api_credential_user;
DROP INDEX IF EXISTS idx_api_credential_tenant;
DROP INDEX IF EXISTS idx_api_credential_hash;
DROP TABLE IF EXISTS api_credential;

DROP INDEX IF EXISTS idx_invitation_tenant;
DROP TABLE IF EXISTS invitation;

DROP INDEX IF EXISTS idx_membership_tenant;
DROP TABLE IF EXISTS membership;

DROP TABLE IF EXISTS platform_user;
DROP TABLE IF EXISTS tenant;

DELETE FROM schema_migrations WHERE id = 38;

COMMIT;
