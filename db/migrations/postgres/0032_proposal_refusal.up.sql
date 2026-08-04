-- The transform's REFUSAL, which `proposal` has nowhere to record.
--
-- # The state that was being lost
--
-- 0012's build_status CHECK admits exactly `unbuilt | built | build_failed`. proposal.BuildStatus has a
-- fourth value — `refused`, the transform declining to write code it could not stand behind — and its
-- own doc explains at length why that is a STATUS rather than an error: "a refusal returned as an error
-- aborts the whole batch and disappears into a log, so the one change the engine deliberately declined
-- to make is the one the user never hears about."
--
-- 🔴 Recording it as `unbuilt` re-creates exactly that disappearance one layer down. Once a deployment
-- compiles proposals, `unbuilt` has to mean three different things at once:
--
--     never compiled            nothing to show yet          → compile it
--     compiled, not built       a real diff, no build gate   → review it; delivery needs a toolchain
--     the transform refused     no diff, by decision, named  → read the reason
--
-- and the card for each is different. Collapsing them made the surface render a REFUSED card for a
-- proposal whose diff had been generated — a narration saying the transform declined a change it had
-- in fact made, with the diff dropped.
--
-- # Why a reason column rather than widening the CHECK
--
-- Widening build_status to admit `refused` would distinguish the three states and still throw away the
-- part that matters. api.Card renders RefusedNodeID / RefusedDimension / RefusedReason, and its comment
-- says why: "a refusal means we declined to write code we could not stand behind — a limit, named, and
-- the reason is the next thing to read". A status with no reason is a refusal the user cannot act on.
--
-- So the reason IS the marker: refusal_reason non-empty means refused, and it carries what to read.
--
-- Dialect: PostgreSQL.

BEGIN;

ALTER TABLE proposal ADD COLUMN IF NOT EXISTS refusal_reason    TEXT NOT NULL DEFAULT '';
ALTER TABLE proposal ADD COLUMN IF NOT EXISTS refusal_dimension TEXT NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class     t ON t.oid = c.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE c.conname = 'proposal_refusal_has_no_diff'
           AND t.relname = 'proposal'
           AND n.nspname = current_schema()
    ) THEN
        -- A refusal that shipped a diff is the "looks complete" failure D-14.3 refuses: the surface
        -- would render a change next to a sentence saying we declined to make it. The database refuses
        -- the row so no writer can produce that pair.
        ALTER TABLE proposal ADD CONSTRAINT proposal_refusal_has_no_diff
            CHECK (refusal_reason = '' OR source_diff_blob_hash IS NULL);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class     t ON t.oid = c.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE c.conname = 'proposal_refusal_dimension_is_explained'
           AND t.relname = 'proposal'
           AND n.nspname = current_schema()
    ) THEN
        -- A dimension with no reason names where without saying what. "node X, dimension skills" is a
        -- location, and the reason is the thing a reader acts on.
        ALTER TABLE proposal ADD CONSTRAINT proposal_refusal_dimension_is_explained
            CHECK (refusal_dimension = '' OR refusal_reason <> '');
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (32, 'proposal_refusal')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
