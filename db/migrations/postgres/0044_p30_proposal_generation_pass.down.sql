-- Down for 0044.
--
-- The table is dropped. A rolled-back image reads no pass rows: the surface it feeds falls back to
-- counting proposal rows, which is what it did before this migration, so nothing that is running breaks
-- and nothing is left behind that nobody is watching.
--
-- ⚠️ A rollback LOSES the record of what each workflow's last generation pass found, and the consequence
-- is stated rather than hidden: the surface goes back to being unable to distinguish "never analysed"
-- from "analysed and healthy". It is fully re-obtainable — a pass is a pure read over linked runs and
-- the discovered graph, so running one again reproduces the row — which is why this table can be
-- dropped and 0041's password tables cannot.

BEGIN;

DROP TABLE IF EXISTS proposal_generation_pass;

DELETE FROM schema_migrations WHERE id = 44;

COMMIT;
