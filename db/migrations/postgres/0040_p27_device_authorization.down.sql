-- Reverses 0040. A review and recovery artifact; the deployed binary embeds only `.up.sql` files.
--
-- Dropping this table loses in-flight logins and nothing else: every authorization is at most ten minutes
-- old, and the credentials it issued live in `api_credential` and are untouched. That is the one table in
-- this phase where a drop is genuinely cheap, and saying so is more useful than implying it never is.

BEGIN;

DROP INDEX IF EXISTS idx_device_expiry;
DROP INDEX IF EXISTS idx_device_device_code;
DROP INDEX IF EXISTS idx_device_user_code;
DROP TABLE IF EXISTS device_authorization;

DELETE FROM schema_migrations WHERE id = 40;

COMMIT;
