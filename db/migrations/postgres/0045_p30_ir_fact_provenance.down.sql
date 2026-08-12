-- Down for 0045.
--
-- 🔴 THE COLUMN IS DROPPED AND NO FACT IS LOST, and that is the property task 2.7 asserts rather than
-- assumes: per-fact authorship lives INSIDE `view_json`, on every edge and every label. This column is
-- a derived index over those facts, not the facts. Dropping it costs a query shape — "which graphs
-- contain something the agent wrote" goes back to being a document walk — and costs no information.
--
-- Every IR readable before 0045 stays readable after this runs, because the reader resolves an absent
-- author to `legacy` (internal/discovery.AuthorOf) and never consults this column to decide what a
-- document means.
--
-- Compare 0041's password tables, which cannot be dropped: those hold the only copy of what they store.

BEGIN;

ALTER TABLE platform_workflow_graph DROP COLUMN IF EXISTS provenance;

DELETE FROM schema_migrations WHERE id = 45;

COMMIT;
