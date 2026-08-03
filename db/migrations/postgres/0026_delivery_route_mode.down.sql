-- Down for 0026_delivery_route_mode. A review and recovery artifact — NOT something the deployed binary
-- can run: internal/pgmigrate embeds only `*.up.sql`. Rollback is re-applying the prior package.
--
-- ⚠️ This returns delivery_route to a shape that cannot hold a valid forgedelivery.Route: without
-- `mode`, every route read back has a zero Mode and Route.Validate rejects it. The rows survive and
-- become unusable.

BEGIN;

ALTER TABLE delivery_route DROP CONSTRAINT IF EXISTS delivery_route_forge_known;
ALTER TABLE delivery_route DROP CONSTRAINT IF EXISTS delivery_route_mode_known;
ALTER TABLE delivery_route DROP COLUMN IF EXISTS mode;
ALTER TABLE delivery_route ADD COLUMN IF NOT EXISTS base_ref TEXT NOT NULL DEFAULT '';

DELETE FROM schema_migrations WHERE id = 26;

COMMIT;
