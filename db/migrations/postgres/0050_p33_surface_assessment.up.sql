-- P33 tasks 2.3 and 2.4: the assessment and its nine findings.
--
-- # Why two tables and not one, three, or none
--
-- `careful-table-creation` says a new table is a one-way door and demands the alternatives be written
-- down. Four were considered:
--
--   A. **One table, findings as a JSONB column.** Rejected on the PREDICATE, not on taste. DevOps task
--      6.1 requires assessments broken out *per axis and per state*, and task 6.2 alerts on the rate of
--      assessments returning nine `not_measured` findings. Both are `GROUP BY axis, state` over scalars.
--      Inside JSONB they are expression scans nobody will index correctly under pressure — and 6.2 is
--      the query that MUST keep working when nobody is looking at it, because it is the earliest signal
--      that a language frontend or the sandbox broke.
--
--   B. **Reuse `heros_inference`.** Rejected on GRAIN. That table is one row per pinned inference,
--      keyed `(workflow_id, source_revision, agent_config_hash)`. An assessment is nine findings of
--      which most are STRUCTURAL and have no pin at all, so the majority of rows would carry NULL in
--      every column that makes that table what it is. Two different things in one table is how a
--      `WHERE` clause somewhere starts returning half an answer.
--
--   C. **Three tables — a third for the eval-set report.** Rejected, and this is the interesting one.
--      An eval-set report is a DOCUMENT: it is read only when rendering the one finding it belongs to,
--      it is never grouped by, never joined against, and never queried by any of its fields. Its case
--      list is variable-length and nested. That is precisely the shape JSONB is for, and giving it a
--      table would buy an index nothing would ever use and a join every read would pay for.
--
--   D. **What was built: two tables, and the eval-set report as JSONB on the finding row.** The split
--      follows the query shapes: the columns anything groups by are columns, and the payload nothing
--      groups by is a document.
--
-- # 🔴 The conditional requirements are enforced HERE too, not only in Go
--
-- `internal/assessment` makes an invalid finding unconstructable by keeping every field unexported.
-- That guards Go callers. It does nothing about a row written by a migration, a fixture, or a future
-- service in another language — so the same four rules are CHECK constraints below.
--
-- This is deliberate triplication (type, JSON schema, database) of four rules, and it is worth stating
-- why it is not redundancy: they guard three different entrances, and the phase's whole product is that
-- `not_measured` names what is missing. A rule enforced in one place is a rule that holds wherever that
-- place is on the path — and the paths differ.
--
-- # 🚫 NO COLUMN HERE CAN HOLD A COMPOSITE
--
-- There is no `score`, no `grade`, no `level`, no `overall`, and no numeric column on `assessment` other
-- than the two spend figures, which are money and not quality. Program ruling R4 is refused
-- STRUCTURALLY: a later phase proposing a composite cannot ship it as a value in an existing column,
-- because there is no column it could arrive in. `TestNoAssessmentColumnCanCarryAComposite` discovers
-- this schema rather than reading a whitelist, so a column added later is caught by the fence and not
-- by review.
--
-- # 🚫 NO COLUMN CAN HOLD SOURCE TEXT
--
-- PRD §7.4: prompt text and source are inputs to a computation on the platform's side of the boundary;
-- this phase does not store them. `claim` is a sentence ABOUT the code and `evidence_locator` names a
-- surface the platform already holds. There is deliberately no `snippet`, no `excerpt` and no
-- `source_line` — having somewhere to put one is how one ends up stored.
--
-- # Timestamps are int64 MILLISECONDS
--
-- `BIGINT`, not `TIMESTAMPTZ`, and no timestamp literal appears in this file. 0049's header states the
-- reason and it is unchanged: these values are compared against numbers Go computed, and a driver
-- rendering a TIMESTAMPTZ into a session time zone is a second clock.
--
-- Idempotent, guarded BY DEFINITION rather than by name: `CREATE TABLE IF NOT EXISTS` is satisfied by a
-- table of that name with any columns at all, so the DO blocks check the columns and constraints the
-- store actually queries.
--
-- Dialect: PostgreSQL only. See 0045's header — the SQLite store is the dev ledger and holds no part of
-- this domain; a copy there would be a second schema nothing reads.

BEGIN;

-- ── The run ────────────────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS assessment (
    assessment_id     TEXT   PRIMARY KEY,
    tenant_id         TEXT   NOT NULL,
    workflow_id       TEXT   NOT NULL,
    -- Half of the pin key. An assessment is OF a revision, which is why no finding here can be `stale`
    -- with respect to it and why P31's fourth finding state has no member in this domain.
    source_revision   TEXT   NOT NULL,
    -- The other half (FR16). NOT NULL because a finding that cannot be attributed to the configuration
    -- that produced it makes a provider upgrade indistinguishable from the repository changing.
    agent_config_hash TEXT   NOT NULL,
    started_at_ms     BIGINT NOT NULL,
    completed_at_ms   BIGINT NOT NULL,
    -- Money, not quality. §7.3: spend is bounded per assessment and attributed to the tenant.
    spend_usd         NUMERIC(12, 6) NOT NULL DEFAULT 0,
    -- 🔴 The cap is stored on the ROW and not only enforced during the run. A reader of a report that
    -- degraded to `budget_exhausted` needs to know what the cap WAS, and a configuration read at render
    -- time may since have changed — which would render "we stopped at $1.00" beside a current cap of
    -- $5.00 and make the report look like a bug.
    spend_cap_usd     NUMERIC(12, 6) NOT NULL,

    CONSTRAINT assessment_cap_is_positive CHECK (spend_cap_usd > 0),
    CONSTRAINT assessment_spend_is_not_negative CHECK (spend_usd >= 0)
);

DO $$
BEGIN
    -- "The assessments of this workflow, newest first" — the console's list and the only way the
    -- history is read.
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
         WHERE schemaname = current_schema() AND indexname = 'idx_assessment_workflow'
    ) THEN
        CREATE INDEX idx_assessment_workflow
            ON assessment (tenant_id, workflow_id, started_at_ms DESC);
    END IF;

    -- FR15's lookup: "has this exact (revision, config) already been assessed?" A repeat assessment
    -- that cannot find its predecessor is a repeat assessment that pays for the inference again.
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
         WHERE schemaname = current_schema() AND indexname = 'idx_assessment_pin'
    ) THEN
        CREATE INDEX idx_assessment_pin
            ON assessment (workflow_id, source_revision, agent_config_hash);
    END IF;
END $$;

-- ── The nine findings ──────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS assessment_finding (
    assessment_id     TEXT   NOT NULL REFERENCES assessment (assessment_id) ON DELETE CASCADE,
    -- The axis IS half the primary key. That is FR1 enforced by the database: "exactly nine, one per
    -- axis, none duplicated" becomes an insert that fails rather than a report that is one axis short
    -- and one axis doubled.
    axis              TEXT   NOT NULL,
    state             TEXT   NOT NULL,
    origin            TEXT   NOT NULL,
    -- The sentence a reader reads. NOT NULL and non-empty: even `not_measured` has a sentence, and
    -- "here is what is missing and what you could do" is the difference between a report someone acts
    -- on and one they discount.
    claim             TEXT   NOT NULL,
    evidence_surface  TEXT   NOT NULL,
    evidence_locator  TEXT   NOT NULL,
    evidence_fragment TEXT,
    missing_input     TEXT,
    refusal_cause     TEXT,
    provider_model_version TEXT,
    inference_address      TEXT,
    -- The decisiveness document, or NULL. JSONB rather than a table for alternative C's reason above.
    eval_set_json     JSONB,

    PRIMARY KEY (assessment_id, axis),

    CONSTRAINT assessment_finding_axis_known
        CHECK (axis IN ('model', 'prompt', 'skills', 'context', 'tools', 'memory', 'harness', 'loop', 'graph')),
    CONSTRAINT assessment_finding_state_known
        CHECK (state IN ('measured', 'observed', 'not_measured', 'refused')),
    CONSTRAINT assessment_finding_origin_known
        CHECK (origin IN ('structural', 'inferred')),
    CONSTRAINT assessment_finding_claim_is_not_blank
        CHECK (length(btrim(claim)) > 0),
    CONSTRAINT assessment_finding_evidence_surface_known
        CHECK (evidence_surface IN ('graph', 'board', 'scorecard')),
    CONSTRAINT assessment_finding_evidence_locator_is_not_blank
        CHECK (length(btrim(evidence_locator)) > 0),

    -- ── The four conditional requirements ─────────────────────────────────────────────────────────
    --
    -- Each is stated as an EQUIVALENCE (`=` between two booleans) rather than as two implications,
    -- because the negative half is as load-bearing as the positive one: a `refused` row carrying a
    -- missing input renders in two different message shapes depending on which column a console reads
    -- first, and that ambiguity is what a closed vocabulary was supposed to end.
    CONSTRAINT assessment_finding_not_measured_names_its_gap
        CHECK ((state = 'not_measured') = (missing_input IS NOT NULL)),
    CONSTRAINT assessment_finding_missing_input_known
        CHECK (missing_input IS NULL OR missing_input IN (
            'no_runnable_entry_point', 'missing_credential', 'sandbox_refusal', 'unsupported_language',
            'frontend_emits_no_edges', 'unresolved_in_ir', 'no_source_snapshot', 'no_call_sites_discovered',
            'not_visible_in_static_ir', 'budget_exhausted', 'inference_abstained')),
    CONSTRAINT assessment_finding_refused_names_one_of_three
        CHECK ((state = 'refused') = (refusal_cause IS NOT NULL)),
    CONSTRAINT assessment_finding_refusal_cause_known
        CHECK (refusal_cause IS NULL OR refusal_cause IN ('frontend', 'analysis', 'language')),
    -- Both attribution columns travel together. One without the other records where an answer came
    -- from but not what produced it, so a provider's upgrade would still be invisible (design D7).
    CONSTRAINT assessment_finding_inferred_is_attributed
        CHECK ((origin = 'inferred')
               = (provider_model_version IS NOT NULL AND inference_address IS NOT NULL)),
    CONSTRAINT assessment_finding_measured_carries_decisiveness
        CHECK ((state = 'measured') = (eval_set_json IS NOT NULL)),

    -- ── The two illegal cells of the four-states × two-origins matrix ─────────────────────────────
    --
    -- A measurement comes from an eval run, never from a model reading code. A refusal is a fact about
    -- THIS BUILD's capability, not a conclusion a model can reach.
    CONSTRAINT assessment_finding_no_inferred_measurement
        CHECK (NOT (state = 'measured' AND origin = 'inferred')),
    CONSTRAINT assessment_finding_no_inferred_refusal
        CHECK (NOT (state = 'refused' AND origin = 'inferred'))
);

DO $$
BEGIN
    -- 🔴 DevOps task 6.1's index: assessments broken out per axis and per state. Without it, the
    -- health endpoint's group-by is a full scan of every finding the platform has ever written, which
    -- is how an observability query becomes the reason nobody runs it.
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
         WHERE schemaname = current_schema() AND indexname = 'idx_assessment_finding_axis_state'
    ) THEN
        CREATE INDEX idx_assessment_finding_axis_state
            ON assessment_finding (axis, state);
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (50, 'p33_surface_assessment')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
