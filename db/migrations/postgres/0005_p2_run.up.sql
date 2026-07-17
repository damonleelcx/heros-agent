-- P2 Runtime — run and node_execution records. Tasks 3.9 (run half), 5.2.
-- Spec: openspec/changes/p2-config-runtime/specs/config-layer/spec.md; PRD §6 (FR17, FR18), §8, §9.
--
-- Dialect: PostgreSQL 11+. Expand-only: ADDS two tables. Depends on 0001 (config, node, blob) and
-- 0004 (transform).

BEGIN;

-- ─────────────────────────────────────────────────────────────────────────────
-- run — one execution of one transformed working copy.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS run (
    run_id          TEXT         PRIMARY KEY,

    -- FK to the configuration, so "a run cannot reference an absent variant" (PRD §8) is structural.
    config_hash     TEXT         NOT NULL REFERENCES config(config_hash),
    source_revision TEXT         NOT NULL CHECK (source_revision <> ''),

    -- The seed this run supplied. It lives HERE and not in config_hash on purpose: config-hash-spec
    -- §5 requires that seeds 1..5 of one configuration share a hash so multi-seed results roll up for
    -- confidence intervals. The reproducibility unit is {config_hash, source_revision, seed}, and
    -- this column is the third axis.
    seed            BIGINT       NOT NULL CHECK (seed >= 0),

    -- Terminal status (FR18). The vocabulary is closed by CHECK: an invented status would be read by
    -- the UI's switch as "something else" and by P4 as neither success nor failure.
    --   running          — dispatched, not yet terminal. The only non-terminal value.
    --   succeeded        — the transformed copy ran to completion.
    --   failed           — it ran and did not complete (non-zero exit, crash, timeout).
    --   halted           — stopped BY US on a typed-contract violation (FR15/3.8). Distinct from
    --                      `failed`: the workflow did not break, we stopped it, and the difference is
    --                      what tells a user whether to look at their code or at their contract.
    --   build-rejected   — the transform never ran (FR5b). Recorded as a run status so a submitted
    --                      spec always has a terminal state to query, rather than the UI having to
    --                      infer "no run row means it never built".
    status          TEXT         NOT NULL DEFAULT 'running'
                                 CHECK (status IN ('running','succeeded','failed','halted','build-rejected')),

    -- Set when status = 'halted': which node's output violated its contract, and on which dimension.
    -- PRD §9 (Product Designer): a contract halt must tell the user WHICH node and WHICH dimension.
    halted_node_id  TEXT,
    halted_reason   TEXT,

    started_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ,

    CONSTRAINT run_transform_fk
        FOREIGN KEY (config_hash, source_revision) REFERENCES transform (config_hash, source_revision),

    -- A halt with no explanation is a dead end for whoever has to fix it, and a non-halted run with a
    -- halt reason is a lie. Both are unwritable.
    CONSTRAINT run_halt_names_the_node
        CHECK ((status = 'halted') = (halted_node_id IS NOT NULL)),
    -- A terminal run has an end time; a running one does not. Without this, "finished" and "still
    -- going" become indistinguishable after a crash, and the UI's loading state never resolves.
    CONSTRAINT run_terminal_has_finished_at
        CHECK ((status = 'running') = (finished_at IS NULL))
);

CREATE INDEX IF NOT EXISTS idx_run_config ON run (config_hash);
CREATE INDEX IF NOT EXISTS idx_run_status ON run (status);

-- ─────────────────────────────────────────────────────────────────────────────
-- node_execution — one node's I/O within a run.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS node_execution (
    run_id            TEXT       NOT NULL REFERENCES run(run_id),
    node_id           TEXT       NOT NULL,

    -- attempt_group is the retry unit (PRD OQ1 pins it at {run_id, node_id, attempt_group}). A node
    -- invocation that is retried keeps its attempt_group, so every retry of one logical call collapses
    -- to ONE row and ONE charge; a genuinely new invocation (a loop's next iteration) gets a new one.
    attempt_group     INTEGER    NOT NULL CHECK (attempt_group >= 0),

    status            TEXT       NOT NULL
                                 CHECK (status IN ('succeeded','failed','halted')),

    -- I/O as content-addressed blob references, never inline (PRD §7): node inputs and outputs are
    -- prompts and completions, which is exactly the PII-bearing payload that must not sit in a row
    -- that gets logged, replicated, and dumped.
    input_blob_hash   TEXT       REFERENCES blob(content_hash),
    output_blob_hash  TEXT       REFERENCES blob(content_hash),

    -- The key sent to the provider so a retry is de-duplicated rather than billed twice (FR17/5.1).
    -- Stored so a billing dispute can be answered from the record: "this charge and that charge carry
    -- one key, so the provider should have counted them once."
    idempotency_key   TEXT       NOT NULL CHECK (idempotency_key <> ''),

    error             TEXT       NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Task 5.2, and the load-bearing constraint of this file: "a double-write race is a caught
    -- conflict, not a duplicate row."
    --
    -- Two executors racing the same node — an at-least-once queue delivering twice (PRD OQ4) — must
    -- not both write a result. Without this, a retried node produces two rows, P4 scores the same
    -- invocation twice, and the duplicate is invisible because both rows look correct. The DB is the
    -- last line: application code that forgets is caught here.
    CONSTRAINT node_execution_pkey PRIMARY KEY (run_id, node_id, attempt_group),

    -- A failure must say why, for the same reason a build rejection must.
    CONSTRAINT node_execution_failure_has_a_reason
        CHECK (status = 'succeeded' OR error <> '')
);

CREATE INDEX IF NOT EXISTS idx_node_execution_run ON node_execution (run_id);

-- Run records are the audit trail of what was executed and what it cost; an edited one is a
-- falsified measurement. Same guard as everywhere else in P2 — one definition of immutable.
--
-- The exception is `run` itself, which legitimately transitions running -> terminal exactly once, so
-- it is NOT immutable. That transition is guarded in the application (internal/executor) by a
-- conditional UPDATE on status='running', which turns a double-finish into 0 rows affected rather
-- than a silent overwrite of the first verdict.
DROP TRIGGER IF EXISTS node_execution_immutable ON node_execution;
CREATE TRIGGER node_execution_immutable BEFORE UPDATE OR DELETE ON node_execution
    FOR EACH ROW EXECUTE FUNCTION registry_reject_mutation();
DROP TRIGGER IF EXISTS node_execution_no_truncate ON node_execution;
CREATE TRIGGER node_execution_no_truncate BEFORE TRUNCATE ON node_execution
    FOR EACH STATEMENT EXECUTE FUNCTION registry_reject_mutation();

INSERT INTO schema_migrations (id, name) VALUES (5, 'p2_run')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
