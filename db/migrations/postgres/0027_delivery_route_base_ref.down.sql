-- Down for 0027_delivery_route_base_ref. A review and recovery artifact — NOT something the deployed
-- binary can run: internal/pgmigrate embeds only `*.up.sql`. Rollback is re-applying the prior package.
--
-- ⚠️ This returns delivery_route to 0026's shape, which cannot hold a valid forgedelivery.Route: without
-- `base_ref` every route reads back with an empty Target.Base, Target.Validate rejects it, and
-- Service.Pending swallows that rejection as "undeliverable" — so the rows survive, become unusable, and
-- the CI fetch reports an empty list rather than an error. See the up migration for why that is the
-- worst of the three shapes this table has had.

BEGIN;

ALTER TABLE delivery_route DROP CONSTRAINT IF EXISTS delivery_route_base_ref_is_named;
ALTER TABLE delivery_route DROP COLUMN IF EXISTS base_ref;

DELETE FROM schema_migrations WHERE id = 27;

COMMIT;
