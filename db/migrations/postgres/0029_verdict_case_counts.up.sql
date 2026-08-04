-- The case COUNTS on `verdict`, which is what a reported verdict actually carries.
--
-- # Why the JSON id arrays are not enough
--
-- 0012 gave `verdict` two columns for cases: `cases_fixed_json` and `cases_broken_json`, arrays of case
-- IDS. That was written when the whole P5.5 loop ran in one place — the engine proposed, built a
-- worktree, ran the harness, and knew every case by name.
--
-- A hosted deployment cannot run that loop: the gate needs the eval cases, the traces and a provider,
-- and all three stay in the customer's environment. So the verdict is REPORTED back over the P11
-- boundary (internal/runlink/verdict.go), and the case ids do not cross it — a case id is
-- customer-authored text, so the counts cross instead and the ids do not.
--
-- 🔴 Without these columns a reported verdict lands with `cases_fixed_json = '[]'`, and every reader
-- derives the count with `len()`. `len([]) = 0`, so a change that fixed four cases is stored, read and
-- rendered as having fixed NOTHING — in the console, and in the body of the pull request P12 opens. It
-- is not a missing feature; it is a wrong number with no marker on it, which is the failure mode this
-- schema is otherwise careful about.
--
-- The id arrays stay. They are still populated where the verdict is produced beside the cases, and they
-- are the detail a same-environment deployment can show. What changes is which column is AUTHORITATIVE:
-- the count is, and internal/verification.Verdict says so in the field's own doc.
--
-- Dialect: PostgreSQL.

BEGIN;

-- DEFAULT 0 and NOT NULL. The default is honest here in a way it would not be for a base branch: every
-- existing row was written by the same-environment path, where the ids ARE the whole record — so the
-- backfill below sets the count from the array, and 0 is only ever the value for an empty array.
ALTER TABLE verdict ADD COLUMN IF NOT EXISTS cases_fixed_count  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE verdict ADD COLUMN IF NOT EXISTS cases_broken_count INTEGER NOT NULL DEFAULT 0;

-- Backfill from the arrays, for rows written before the count existed. This is the one moment the two
-- representations are guaranteed to agree, because until now the array was the only writer.
UPDATE verdict
   SET cases_fixed_count  = COALESCE(jsonb_array_length(cases_fixed_json), 0),
       cases_broken_count = COALESCE(jsonb_array_length(cases_broken_json), 0);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class     t ON t.oid = c.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE c.conname = 'verdict_case_counts_are_counts'
           AND t.relname = 'verdict'
           AND n.nspname = current_schema()
    ) THEN
        -- A negative count is not a smaller number, it is a corrupt one.
        ALTER TABLE verdict ADD CONSTRAINT verdict_case_counts_are_counts
            CHECK (cases_fixed_count >= 0 AND cases_broken_count >= 0);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class     t ON t.oid = c.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE c.conname = 'verdict_counts_cover_their_ids'
           AND t.relname = 'verdict'
           AND n.nspname = current_schema()
    ) THEN
        -- Where ids ARE present they must be consistent with the count. Stated as `>=` rather than `=`
        -- because the two are not equivalent claims: a reported verdict has a count and no ids (0 ids,
        -- count 4 — legitimate), and no verdict may ever list more ids than it counts, which would mean
        -- the count understates its own evidence.
        ALTER TABLE verdict ADD CONSTRAINT verdict_counts_cover_their_ids
            CHECK (cases_fixed_count  >= COALESCE(jsonb_array_length(cases_fixed_json), 0)
               AND cases_broken_count >= COALESCE(jsonb_array_length(cases_broken_json), 0));
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (29, 'verdict_case_counts')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
