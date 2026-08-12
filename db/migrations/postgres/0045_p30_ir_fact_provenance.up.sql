-- P30 task 2.1: which AUTHORS wrote the facts in a stored graph.
--
-- # What is stored here, and what is emphatically NOT
--
-- 🔴 Per-fact provenance lives IN THE DOCUMENT — `author` on every edge and every pattern label inside
-- `view_json` (P30 D4, schemas/workflow-ir.schema.json). It has to: D4's whole argument is that a
-- run-level `contains_inferred_facts` boolean "cannot answer 'who authored THIS edge', which is the
-- only question an incident asks".
--
-- This column is not that boolean and must never become it. It is the DISTINCT SET of authors present
-- in the row's facts, canonicalised — `detector,frontend`, `frontend,heros` — derived from the document
-- at write time in one place, so it cannot disagree with what it indexes. A fence asserts
-- derived-equals-stored, because a summary that can drift is worse than no summary.
--
-- # What it buys, which parsing JSON in the application cannot
--
-- "Which of this tenant's graphs contain a fact the agent wrote?" is the question asked when an
-- inference is found to be wrong, and it is asked across every row at once. Answering it by fetching
-- every `view_json` and walking it in Go is a full scan of customer documents to compute a fact the
-- writer already knew. This makes it `WHERE provenance LIKE '%heros%'`.
--
-- # NULL means `legacy`, and there is NO BACKFILL
--
-- 🔴 Every row already in this table was written before authorship was recorded, so its facts carry no
-- author at all. NULL is the honest value for that and `internal/discovery.AuthorOf` maps it to
-- `legacy` on the way out — a value a query can select on, and one no writer may ever produce.
--
-- Back-filling `frontend` would be the tempting move and it is the wrong one twice: it asserts
-- something about facts nobody examined, and it makes the pre-P30 rows INDISTINGUISHABLE from stamped
-- ones — which is precisely the distinction this column exists to create. Same reasoning as 0042's
-- `coverage_version`, which is a NULL-means-not-reported column on the neighbouring table.
--
-- The nullability is also what makes the deploy safe: `ADD COLUMN ... NULL` with no default is a
-- catalog-only change on every supported PostgreSQL — no rewrite, no lock held while rows are touched.
--
-- # Idempotent, guarded BY DEFINITION rather than by object name
--
-- `IF NOT EXISTS` on a column is a NAME guard: it is satisfied by a column called `provenance` of any
-- type at all. The DO block checks name AND type AND nullability, because "a column with the right
-- name" is exactly how a migration silently agrees with a schema it does not describe.
--
-- Dialect: PostgreSQL, and ONLY PostgreSQL — the same answer 0042 gives for the same reason.
-- `platform_workflow_graph` exists in one dialect. The SQLite store in `internal/db/db.go` is the DEV
-- LEDGER (API keys, registries, the retired agent's memory tables); it has never carried this table and
-- holds no IR facts. A SQLite copy would be a second schema nothing reads, which is the drift
-- `db/migrations/README.md` warns about, not symmetry.

BEGIN;

ALTER TABLE platform_workflow_graph ADD COLUMN IF NOT EXISTS provenance TEXT;

DO $$
DECLARE
    col_type TEXT;
    col_null TEXT;
BEGIN
    SELECT data_type, is_nullable INTO col_type, col_null
      FROM information_schema.columns
     WHERE table_name = 'platform_workflow_graph' AND column_name = 'provenance';

    IF col_type IS NULL THEN
        RAISE EXCEPTION 'platform_workflow_graph.provenance was not created';
    END IF;
    IF col_type <> 'text' THEN
        RAISE EXCEPTION 'platform_workflow_graph.provenance is %, expected text — a column with the '
                        'right NAME and the wrong definition is how a migration silently agrees with a '
                        'schema it does not describe', col_type;
    END IF;
    IF col_null <> 'YES' THEN
        RAISE EXCEPTION 'platform_workflow_graph.provenance is NOT NULL. NULL is the value that means '
                        '`legacy` — a row whose facts predate authorship — and making it required would '
                        'force a back-fill that asserts something about facts nobody examined';
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (45, 'p30_ir_fact_provenance')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
