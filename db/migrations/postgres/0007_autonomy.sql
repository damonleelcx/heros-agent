-- 0007_autonomy — how much of a run each organization allows to proceed without a person.
--
-- # Why a column on tenants and not a table
--
-- One value per organization, set by an owner, read on every effect-bearing task. A settings table would
-- model many values per organization, which is a thing this product does not have — and the first thing
-- an empty settings table does is make every read a LEFT JOIN with a default in Go, which is a second
-- place for the default to live and disagree with this one.
--
-- 🔴 The default is the most restrictive value, not the most convenient. Every organization that already
-- exists gets `supervised` — the behaviour they have today, where every effect waits for a person — so
-- this migration changes nobody's behaviour on the day it runs. A default of `assisted` would silently
-- widen what every existing customer's runs may do, in a schema change, without anybody choosing it.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS autonomy TEXT NOT NULL DEFAULT 'supervised';

-- DROP then ADD, because Postgres has no ADD CONSTRAINT IF NOT EXISTS and every migration runs on every
-- boot. See 0006 for the same idiom, and embed.go for the advisory lock that makes it safe when two
-- processes migrate at once.
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_autonomy_is_known;
ALTER TABLE tenants ADD CONSTRAINT tenants_autonomy_is_known
    CHECK (autonomy IN ('supervised', 'assisted', 'autonomous'));
