-- Down for 0025_proposal_scope_and_routes. A review and recovery artifact — NOT something the deployed
-- binary can run: internal/pgmigrate embeds only `*.up.sql`, because a binary that can drop the
-- customer's tables on some code path is a binary that eventually does. Rollback is re-applying the
-- prior package.
--
-- ⚠️ Dropping tenant_id from `proposal` does not merely remove a column — it removes the only thing that
-- says WHOSE each proposal is. The rows survive, unowned. Anything reading them afterwards reads every
-- tenant's proposals at once, which is why the up migration refuses to invent a scope for rows it cannot
-- attribute.

BEGIN;

DROP TABLE IF EXISTS delivery_route;

DROP INDEX IF EXISTS idx_proposal_scope;
ALTER TABLE proposal DROP CONSTRAINT IF EXISTS proposal_scope_is_named;
ALTER TABLE proposal DROP COLUMN IF EXISTS workflow_id;
ALTER TABLE proposal DROP COLUMN IF EXISTS tenant_id;

DELETE FROM schema_migrations WHERE id = 25;

COMMIT;
