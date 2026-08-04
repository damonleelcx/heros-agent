-- Reverses 0035. A review and recovery artifact: the deployed binary embeds only `.up.sql` files and
-- cannot run this (db/migrations/embed.go), because P19 Decision 7 makes rollback "re-apply the prior
-- package" rather than "let the process drop the customer's tables on some code path".
--
-- 🔴 Dropping this table locks every operator out of the console permanently, not temporarily: the
-- enrolment it holds cannot be recreated from the console, because enrolling a factor requires a session
-- and issuing a session requires a factor. Recovery is the bootstrap command, run again, out of band.

BEGIN;

DROP INDEX IF EXISTS idx_admin_factor_admin;
DROP TABLE IF EXISTS admin_factor;

DELETE FROM schema_migrations WHERE id = 35;

COMMIT;
