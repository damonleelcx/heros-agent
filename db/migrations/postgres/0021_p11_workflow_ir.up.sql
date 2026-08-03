-- P11 opt-in workflow structure — the durable store behind `heros link --with-ir`.
--
-- # Why this table exists at all
--
-- The platform could show a linked run's NUMBERS and nothing else, because numbers were the only thing
-- it was ever sent. Every view that draws a workflow — the pattern graph most of all — needs the shape:
-- which symbol a call sits in, which file and lines, which model, and the edges between nodes. None of
-- that crosses on a run link, by design, so the hosted graph rendered as unlabelled dots or was left
-- unmounted entirely. This is where the opt-in second payload lands so it can be drawn.
--
-- # What this table may and may not hold
--
-- SHAPE, never CONTENT. The projection that fills it (internal/cli/workflowir.go) reads named fields off
-- a discovered IR and refuses prompt text, prompt variable names, I/O-contract schemas, in-scope symbol
-- sets, and tool NAMES — a tool COUNT crosses instead. A file path and a line range say where a call is;
-- they do not carry what it says, and nothing in this schema can hold what it says.
--
-- 🔴 If a future column here would hold customer text, it does not belong in this table. The allowlist
-- in internal/runlink/workflowir.go is the review artifact, and a column with no allowlist entry behind
-- it is a boundary change that nobody reviewed.
--
-- Dialect: PostgreSQL.

BEGIN;

CREATE TABLE workflow_ir (
    tenant_id       TEXT        NOT NULL,
    workflow_id     TEXT        NOT NULL,
    -- The revision the structure was discovered at. Part of the identity rather than a column that
    -- gets overwritten: a graph drawn at one revision and scored at another is a picture of neither,
    -- so two revisions are two rows and the reader is told which one they are looking at.
    source_revision TEXT        NOT NULL,
    ir_version      TEXT        NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL,
    -- The allowlisted node and edge projections, read back whole and never queried by field. JSONB for
    -- the same reason `run_link.scores_json` is: the console renders the document, it does not filter it.
    nodes_json      JSONB       NOT NULL DEFAULT '[]'::jsonb,
    edges_json      JSONB       NOT NULL DEFAULT '[]'::jsonb,

    -- Re-uploading the same revision REPLACES it rather than accumulating: a developer re-running
    -- discover at the same commit has not produced a second workflow, and two rows for one revision
    -- would make "which structure is this graph drawn from" unanswerable.
    PRIMARY KEY (tenant_id, workflow_id, source_revision)
);

-- The console asks "the newest structure for this workflow" on every graph view.
CREATE INDEX idx_workflow_ir_latest ON workflow_ir (tenant_id, workflow_id, received_at DESC);

INSERT INTO schema_migrations (id, name) VALUES (21, 'p11_workflow_ir')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
