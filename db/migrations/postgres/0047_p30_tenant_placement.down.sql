-- Down for 0047.
--
-- Dropping the table returns every tenant to the DEFAULT placement, which is `disabled` (Q2). A
-- rolled-back deployment therefore analyses nothing, platform-side or customer-side, and every surface
-- renders exactly the rule-derived facts it rendered before — which is the correct behaviour for an
-- image that does not have this capability.
--
-- ⚠️ What is lost is the OPERATOR'S DECISIONS, and it is worth stating plainly because the failure is
-- quiet: re-applying 0047 gives an empty table, so every tenant somebody deliberately placed
-- `customer` reads as `defaulted` again. Nothing breaks and nothing errors — analyses simply stop, and
-- the console reports a fleet nobody has reviewed. The audit log still holds every SetPlacement act, so
-- the decisions are recoverable by reading it; they are not recoverable by re-running anything.
--
-- 🚫 Stored inferences are NOT dropped. A `customer`-placed tenant's submitted facts live in
-- `heros_inference` with `placement = 'customer'` stamped, and they stay attributable after a rollback
-- — dropping the record of who ran an analysis while keeping the analysis is the one outcome worse
-- than either.

BEGIN;

DROP TABLE IF EXISTS heros_tenant_placement;

DELETE FROM schema_migrations WHERE id = 47;

COMMIT;
