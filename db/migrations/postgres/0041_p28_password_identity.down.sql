-- Down for 0041.
--
-- 🔴 The column is NOT dropped, and that asymmetry is deliberate.
--
-- Rolling back means running the PRIOR image against this schema, and the prior image's `scanUser` selects a
-- fixed column list that does not include `email_verified_at` — so an extra column is invisible to it. What is
-- NOT survivable is the reverse: dropping the column while any replica of the new image is still serving
-- turns every user read into `42703 column does not exist`, which takes down sign-in for every customer
-- including the ones who never used a password.
--
-- The same reasoning is why 0038 relaxed a constraint rather than dropping one. A down migration exists to
-- make a rollback safe, not to make the schema symmetric; leaving an unread nullable column costs nothing and
-- removing it costs an outage during exactly the window a rollback is happening.
--
-- The two TABLES are dropped, because a rolled-back image knows nothing about them and an orphaned table with
-- password hashes in it is a liability nobody is watching. A rollback therefore loses passwords: that is
-- stated rather than hidden, and it is why the rollback runbook says re-invite rather than re-deploy.

BEGIN;

DROP INDEX IF EXISTS idx_identity_token_user;
DROP TABLE IF EXISTS identity_token;
DROP TABLE IF EXISTS user_password;
DROP INDEX IF EXISTS idx_platform_user_email;

DELETE FROM schema_migrations WHERE id = 41;

COMMIT;
