-- P29: the coverage table version the per-node verdicts were computed against.
--
-- # Why this is a COLUMN and the per-node fields are not
--
-- The opt-in structure payload gains three things in P29: per-node `language`, per-node `axis_verdicts`,
-- and a payload-level `coverage_version`. The first two need no DDL at all — they live inside the
-- existing `nodes_json` document, which the console renders whole and never filters by field, exactly as
-- 0021 designed it to.
--
-- `coverage_version` is different in KIND, not merely in convenience. It is a property of the PAYLOAD:
-- one transmission's verdicts were all computed against one table. Writing it into every node's blob
-- would be storing a payload-level fact at per-node grain, where nothing stops two nodes in one document
-- from disagreeing about which table they came from — a state the wire cannot produce and the reader
-- would have no rule for resolving.
--
-- # Why NULLABLE, and why there is NO BACKFILL
--
-- 🔴 NULL means NOT REPORTED, and that is the true answer for every row already in this table. They were
-- written by a CLI that had no verdicts to compute a version for. Backfilling this build's own
-- `CoverageTableVersion()` would date somebody else's structure to a table it was never computed
-- against — and the projection reads exactly this column to decide whether to label a result STALE, so
-- the fabricated value would suppress the label that exists to catch it.
--
-- The nullability is also what makes the deploy safe: `ADD COLUMN ... NULL` with no default is a
-- catalog-only change on every supported PostgreSQL. No table rewrite, no lock held while rows are
-- touched, on a table that grows with every customer who opts in.
--
-- # Idempotent, and guarded BY DEFINITION rather than by object name
--
-- `IF NOT EXISTS` is the guard PostgreSQL offers for a column, and it is a NAME guard: it would be
-- satisfied by a column called `coverage_version` of any type at all. The DO block below checks the
-- definition — name AND type AND nullability — and fails loudly if a column of that name exists with a
-- different shape, because "a column with the right name" is exactly how a migration silently agrees
-- with a schema it does not describe.
--
-- Dialect: PostgreSQL, and ONLY PostgreSQL. `db/migrations/README.md` is explicit that two dialects are
-- two semantics and that a migration lives under the dialect it targets. `workflow_ir` exists in one
-- dialect: the SQLite store in `internal/db/db.go` is the DEV LEDGER — a different database holding
-- registries, memory and API keys — and it has never carried this table. Adding a SQLite copy would
-- create a second schema nothing reads, which is the drift that README warns about, not symmetry.

BEGIN;

ALTER TABLE workflow_ir ADD COLUMN IF NOT EXISTS coverage_version TEXT;

DO $$
DECLARE
    col_type TEXT;
    col_null TEXT;
BEGIN
    SELECT data_type, is_nullable INTO col_type, col_null
      FROM information_schema.columns
     WHERE table_name = 'workflow_ir' AND column_name = 'coverage_version';

    IF col_type IS NULL THEN
        RAISE EXCEPTION 'workflow_ir.coverage_version was not created';
    END IF;
    IF col_type <> 'text' THEN
        RAISE EXCEPTION 'workflow_ir.coverage_version is %, expected text — a column with the right NAME '
                        'and the wrong TYPE is how a migration agrees with a schema it does not describe',
                        col_type;
    END IF;
    IF col_null <> 'YES' THEN
        RAISE EXCEPTION 'workflow_ir.coverage_version is NOT NULL — NULL is the only honest value for '
                        'every row written before this change, and the projection reads NULL as '
                        '"not reported"';
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (42, 'p29_workflow_ir_coverage_version')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
