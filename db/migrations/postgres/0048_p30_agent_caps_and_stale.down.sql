-- Down for 0048.
--
-- 🔴 DROPPING THE CAPS REMOVES EVERY CEILING, and that is the opposite direction from every other down
-- migration in this phase. 0047's rollback returns tenants to `disabled`, which analyses nothing;
-- rolling this one back leaves whatever placements exist analysing with NO ceiling at all.
--
-- The two are safe together and neither is safe alone, so the operational order matters and is stated
-- rather than left to be worked out during an incident: roll 0047 back FIRST (or set every enabled
-- tenant to `disabled`), then this. Rolling this back on a deployment with enabled tenants is the one
-- sequence that produces unbounded spend, and nothing in the schema can refuse it — a down migration
-- cannot read the state of another table's rows and decline.
--
-- What is NOT lost: the meter. `heros_spend` is untouched, so every token already spent stays recorded
-- and attributable. What is lost is the operator's decisions about ceilings, recoverable only from the
-- audit log — the same trade 0047's rollback makes for placements, and worth the same warning.

BEGIN;

DROP TABLE IF EXISTS heros_cap;

-- The stale marks go with it. A rolled-back image has no writer for them and no reader, so leaving the
-- columns would leave a fact nothing maintains and nothing renders — which is the definition of the
-- stale state, applied to the mechanism that records it.
ALTER TABLE heros_inference DROP CONSTRAINT IF EXISTS heros_inference_stale_pair;
ALTER TABLE heros_inference DROP CONSTRAINT IF EXISTS heros_inference_stale_reason;
ALTER TABLE heros_inference DROP COLUMN IF EXISTS stale_at_ms;
ALTER TABLE heros_inference DROP COLUMN IF EXISTS stale_reason;

DELETE FROM schema_migrations WHERE id = 48;

COMMIT;
