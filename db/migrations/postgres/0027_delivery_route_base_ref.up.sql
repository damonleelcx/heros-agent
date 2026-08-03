-- Restore `base_ref` on delivery_route. 0026 dropped it on a reason that reads right and is false.
--
-- # What 0026 got wrong
--
-- Its stated ground was that "base_ref is not a field of forgedelivery.Route at all". Route's fields are
-- {Mode, Target, ForgeKind} — so the claim is true of the STRUCT and false of the TYPE. `Target` carries
-- `Base`, and `Target.Validate` refuses a target whose Base is empty:
--
--     if strings.TrimSpace(t.Base) == "" {
--         return fmt.Errorf("target %s has no base branch to open a pull request against", t.Key())
--     }
--
-- The base branch is not recoverable from any other column, and that is by design rather than by
-- oversight: `Target.Key()` — which IS the `target` column — deliberately omits Base, because two
-- deliveries to one (repo, workflow) against different bases are still the same idempotent delivery.
-- The one column that could have carried it is the one 0026 removed.
--
-- 🔴 So 0026 reproduced the exact defect it was written to fix, one field over. A store over its shape
-- reads back a Route whose Target.Base is empty; `Deliverer.Prepare` calls `route.Validate()` on line 206
-- and returns that error — and `Service.Pending` treats a Prepare error as "undeliverable" and
-- `continue`s. The customer's CI would fetch its pending deliveries, get 200 and an EMPTY LIST, and
-- nothing anywhere would say why. A table that can only hold invalid rows is bad; a table that can only
-- hold invalid rows and whose invalidity is swallowed by a `continue` is the version nobody finds.
--
-- # Why the lesson did not take the first time
--
-- 0026 said "read the type before writing the table" and I read one struct definition. A field of a
-- field is still a field the store must persist, and `Validate` — not the struct literal — is the
-- authority on what a complete value is. The fence that now enforces this is not another column list:
-- it is TestARouteSurvivesTheRoundTripThroughItsColumns in internal/deliveryroute, which builds a Route,
-- reduces it to exactly the columns this table has, reconstructs it, and calls Validate. It fails on any
-- future migration that drops a column a valid Route needs, whatever the reasoning in the header says.
--
-- Dialect: PostgreSQL.

BEGIN;

-- NOT NULL with NO DEFAULT, and that is the point.
--
-- There is no honest default for a base branch. 'main' is a guess, and a wrong guess here does not
-- degrade a read — it opens a customer's pull request against the wrong branch, which is a write into
-- their repository. 0026 could give `mode` a default because 'ci' is the mode that holds no credential,
-- so the defaulted value is the SAFE one; there is no safe branch name.
--
-- On an empty table this succeeds. On a non-empty one it fails and the deployment stops, which is
-- correct: rows would exist that no writer created (this store is the first one), and a human should
-- decide what branch they meant rather than inherit a guess. Same posture as 0025's scope columns.
ALTER TABLE delivery_route ADD COLUMN base_ref TEXT NOT NULL;

ALTER TABLE delivery_route ADD CONSTRAINT delivery_route_base_ref_is_named
    CHECK (base_ref <> '');

INSERT INTO schema_migrations (id, name) VALUES (27, 'delivery_route_base_ref')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
