-- The evidence behind a linked run's scores — what the eval board and the scorecard render.
--
-- # What was missing, and why it made two surfaces unmountable
--
-- Migration 0020 stores a linked run's SCORES. A score alone cannot fill an eval board, because a board
-- ranks variants and a ranking has to say what qualifies each number: how many cases it is over, whether
-- the customer's own gate passed, and whether it is a provisional single-seed run. None of that crossed
-- the boundary, so the platform held claims with no evidence — and the only ways to mount the board were
-- to invent a `gate_pass` boolean nothing computed, or to leave it nil. It was left nil, correctly.
--
-- The scorecard had the sharper version of the same problem: it renders PER-NODE attribution, and the
-- CLI computed per-node cost/latency/tokens, carried them in the run record, and dropped them at the
-- payload with the note "aggregate-derivable". An aggregate is not derivable back into its parts — it
-- cannot say WHICH node is expensive, which is the only question the scorecard exists to answer.
--
-- `internal/runlink/allowlist.go` now permits both, under a new `eval` category, with the justification
-- for each field and the list of what is still refused. This table is where they land.
--
-- 🔴 WHAT MAY NOT GO IN HERE
--
-- Counts, verdicts and quantities. NOT eval cases, expected answers, judge prompts, model outputs, or
-- the customer's gate THRESHOLDS (their policy is theirs). `case_count` says how much evidence there
-- was; it carries none of it. A column here holding any of the refused things is a boundary change that
-- nobody reviewed — the allowlist is the review artifact, and a column with no entry behind it has none.
--
-- # Why columns on run_link rather than a side table
--
-- This is not a new fact about a new subject; it is more of the same fact about a linked run, written at
-- the same instant by the same ingest, and read on every board query. A side table would make every read
-- a join for no isolation, and would let a run exist with scores but no evidence row — a state nothing
-- would produce and every reader would then have to handle.
--
-- Dialect: PostgreSQL.

BEGIN;

-- Nullable, and that is load-bearing rather than laziness: rows written before this migration are runs
-- linked by a CLI that never computed these fields. NULL means "this run predates the evidence", which
-- is a different fact from "zero cases" or "the gate did not pass", and the console must be able to say
-- so instead of rendering an older run as a failing one.
ALTER TABLE run_link ADD COLUMN eval_case_count INTEGER;

-- How many seeds the run used. The seed LIST already crosses (`run_metadata.seed`) and was simply
-- dropped on the floor at ingest; the board needs the count to render "n=5 seeds × 8 cases", which is
-- what turns a score into a claim a reader can weigh. Nullable for the same reason as the case count.
ALTER TABLE run_link ADD COLUMN eval_seed_count INTEGER;

-- The customer's own gate verdict, exactly as their CLI printed it locally.
--
-- 🔴 'not-configured' is a THIRD value, not a spelling of 'fail' or a missing 'pass'. A deployment with
-- no gate has not passed and has not failed; collapsing that into either one would let an unmeasured run
-- rank on the board identically to one that cleared a threshold. The CHECK is what stops a future writer
-- from inventing a fourth spelling that the console then has to guess at.
ALTER TABLE run_link ADD COLUMN eval_gate_outcome TEXT
    CONSTRAINT run_link_gate_outcome_known
    CHECK (eval_gate_outcome IS NULL OR eval_gate_outcome IN ('pass', 'fail', 'not-configured'));

-- The METRIC NAMES that failed the gate. Names only — metric names already cross under `scores.metric`.
ALTER TABLE run_link ADD COLUMN eval_gate_failures JSONB NOT NULL DEFAULT '[]'::jsonb;

-- Whether the run was below the multi-seed floor. The caveat travels with the number it qualifies, for
-- the reason the confidence interval does: a provisional result rendered like a settled one is a
-- stronger claim than anyone made.
ALTER TABLE run_link ADD COLUMN eval_single_seed BOOLEAN NOT NULL DEFAULT FALSE;

-- Per-node cost/latency/tokens, keyed by node id. Read back whole and never queried by field, so JSONB
-- for the same reason `scores_json` is: the scorecard renders the document, it does not filter it.
ALTER TABLE run_link ADD COLUMN per_node_json JSONB NOT NULL DEFAULT '{}'::jsonb;

-- The board reads "every run for this workflow, newest first" and then groups by config_hash into
-- variants. 0020's idx_run_link_workflow covers the lookup; this covers the ordering it always applies.
CREATE INDEX idx_run_link_workflow_linked_at ON run_link (tenant_id, workflow_id, linked_at DESC);

INSERT INTO schema_migrations (id, name) VALUES (23, 'run_link_eval_evidence')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
