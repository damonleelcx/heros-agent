-- P30 task 1.6: what the last proposal-generation pass FOUND, per tenant per workflow.
--
-- # The defect this table removes
--
-- `internal/proposalgen` already returns a closed State and a sentence — "you have linked no runs",
-- "you have pushed no source", "this deployment publishes no model catalog", "nothing here is a cost
-- bottleneck" — and every one of them was discarded the instant the HTTP response was written. The
-- recommendation surface, which is read minutes or days later and from a different process, had exactly
-- one input: how many proposal rows exist. Zero rows rendered as `empty`, and `empty` rendered as
-- "Nothing is pending." — the same sentence for a workflow nobody has ever analysed and a workflow that
-- was analysed and is genuinely healthy.
--
-- 🔴 Those two are opposites. One means "press the button"; the other means "you are done". A surface
-- that cannot tell them apart tells half its readers to stop and the other half to keep looking.
--
-- # Why a NEW TABLE, which is a one-way door
--
-- `careful-table-creation` requires the alternatives to be named and rejected:
--
--   a column set on `proposal`   — keyed by PROPOSAL. The states worth recording are precisely the ones
--                                  where NO proposal row was written, so the fact would have nowhere to
--                                  live in exactly the cases it exists for.
--   a column set on `workflow_ir` — keyed (tenant, workflow, revision). A pass is not pinned to a
--                                  revision: it reads the newest linked run and the newest graph, and
--                                  `revision_mismatch` is one of its OUTCOMES. Keying by revision would
--                                  make the state that reports a revision disagreement unstorable.
--   a JSON blob on either        — the surface reads it by (tenant, workflow), which is the key it
--                                  would not have.
--
-- The grain is (tenant, workflow) and there is exactly one row per pair: a pass REPLACES its
-- predecessor. History of passes is not what the surface asks for — it asks "what does the platform
-- currently believe about this workflow", and a log would answer a different question while making the
-- current answer a query with an ORDER BY that somebody eventually gets wrong.
--
-- # `state` is not constrained to a value list, deliberately
--
-- proposalgen's State is a closed set in Go and the writer refuses an empty one, but pinning the eight
-- current members into a CHECK here would mean a ninth state — a real possibility; the set has grown
-- twice — is a schema migration before it is a code change, on a table whose whole purpose is to record
-- what the generator said. The NOT NULL and the non-empty CHECK are the invariants that matter: a row
-- that does not say what the pass found is the row this table exists to prevent.
--
-- # Timestamps are int64 milliseconds
--
-- The standing rule, and it is the reason this column is BIGINT rather than TIMESTAMPTZ like `0043`'s:
-- P30's tables are read by code that must produce the same value on both dialects, and a driver that
-- renders a TIMESTAMPTZ into a session time zone is a second clock.
--
-- Idempotent, guarded by DEFINITION — `CREATE TABLE IF NOT EXISTS` is a NAME guard, so the DO block
-- below checks the key and the columns the store actually queries.
--
-- Dialect: PostgreSQL only, matching this table family.

BEGIN;

CREATE TABLE IF NOT EXISTS proposal_generation_pass (
    tenant_id   TEXT   NOT NULL,
    workflow_id TEXT   NOT NULL,
    -- proposalgen.State, verbatim. A closed value in Go, not a sentence.
    state       TEXT   NOT NULL,
    -- The sentence the generator wrote for this state. Stored rather than re-derived: the generator
    -- composes it from what the pass actually saw ("your newest linked run is at revision X and your
    -- graph is at Y"), and a console re-deriving it would have to re-run the pass to know.
    detail      TEXT   NOT NULL DEFAULT '',
    -- How many proposals this pass RECORDED. Distinct from "how many rows exist now", which is what the
    -- surface counts today and which includes every earlier pass's output.
    proposals   INTEGER NOT NULL DEFAULT 0,
    ran_at_ms   BIGINT NOT NULL,

    PRIMARY KEY (tenant_id, workflow_id),

    -- A pass that does not say what it found is the exact row this table exists to prevent, so it is
    -- refused by the database and not only by the writer. The two fail independently.
    CONSTRAINT proposal_generation_pass_state_nonempty CHECK (state <> ''),
    CONSTRAINT proposal_generation_pass_counts_nonneg  CHECK (proposals >= 0)
);

DO $$
DECLARE
    pk TEXT;
    missing TEXT;
BEGIN
    SELECT string_agg(a.attname, ',' ORDER BY x.ordinality) INTO pk
      FROM pg_constraint c
      JOIN LATERAL unnest(c.conkey) WITH ORDINALITY AS x(attnum, ordinality) ON TRUE
      JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = x.attnum
     WHERE c.conrelid = 'proposal_generation_pass'::regclass AND c.contype = 'p';

    IF pk IS DISTINCT FROM 'tenant_id,workflow_id' THEN
        RAISE EXCEPTION 'proposal_generation_pass primary key is (%), expected (tenant_id,workflow_id) '
                        '— one row per pair is what makes a pass REPLACE its predecessor instead of '
                        'appending a log the surface would have to sort', pk;
    END IF;

    SELECT string_agg(c.name, ', ') INTO missing
      FROM (VALUES ('state'), ('detail'), ('proposals'), ('ran_at_ms')) AS c(name)
     WHERE NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_name = 'proposal_generation_pass' AND column_name = c.name);

    IF missing IS NOT NULL THEN
        RAISE EXCEPTION 'proposal_generation_pass is missing column(s): % — CREATE TABLE IF NOT EXISTS '
                        'is a NAME guard, so a pre-existing table of this name with different columns '
                        'would have been silently accepted', missing;
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (44, 'p30_proposal_generation_pass')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
