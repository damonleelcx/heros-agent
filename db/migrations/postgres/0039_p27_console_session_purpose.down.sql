-- Reverses 0039. A review and recovery artifact; the deployed binary embeds only `.up.sql` files.
--
-- 🔴 Dropping this column makes a console cookie indistinguishable from an API credential again. The
-- prior binary does not read the column, so a rollback needs none of this — P19 Decision 7's "re-apply
-- the prior package" is the supported path.

BEGIN;

DROP INDEX IF EXISTS idx_console_session_purpose;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class     t ON t.oid = c.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE c.conname = 'console_session_purpose_known'
           AND t.relname = 'console_session'
           AND n.nspname = current_schema()
    ) THEN
        ALTER TABLE console_session DROP CONSTRAINT console_session_purpose_known;
    END IF;
END $$;

ALTER TABLE console_session DROP COLUMN IF EXISTS purpose;

DELETE FROM schema_migrations WHERE id = 39;

COMMIT;
