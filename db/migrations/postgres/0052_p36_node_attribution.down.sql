-- Down for 0052.
--
-- Drops `heros_inference.nodes_json`. A rolled-back image knows nothing about per-node attribution: no
-- store reads the column, and every surface renders the same inference without naming which node
-- produced which edge.
--
-- ⚠️ IT LOSES THE ATTRIBUTION, and the consequence is stated rather than hidden. The edges, labels,
-- abstentions and narrative all survive — they are in their own columns — so no FINDING is lost. What is
-- lost is the answer to "which of the five nodes wrote this edge", for every inference produced while
-- the column existed. That answer is not re-derivable: it was observed at run time, and re-running the
-- assessment would produce a new inference rather than recover the old one's provenance.
--
-- 🚫 The definitions are NOT touched here, in either direction. `heros_agent_version.spec_json` was
-- never migrated up (see the up file), so there is nothing to migrate down — and a rolled-back binary
-- reads a single-node row byte-identically, which is the property decisions.md D-36.0 exists to
-- guarantee.

BEGIN;

ALTER TABLE heros_inference
    DROP COLUMN IF EXISTS nodes_json;

DELETE FROM schema_migrations WHERE id = 52;

COMMIT;
