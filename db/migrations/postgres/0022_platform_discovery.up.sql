-- Platform-side discovery — the source the platform was never given, and the graph it can finally draw.
--
-- # Why this exists
--
-- Migration 0021 landed the OPT-IN structure a customer transmits with `heros link --with-ir`, and it
-- was enough to draw a workflow as nodes and edges. It was not enough to LABEL them. The pattern
-- classifier reads prompt text and tool names, and 0021's allowlist refuses both by construction — so
-- the hosted graph rendered every region as `unclassified`, correctly, forever. The same missing input
-- is why the eval board, the scorecard, proposals, the optimizer and the run monitor were all mounted
-- nil: each renders evidence produced by RUNNING something over source, and the platform had no source.
--
-- These two tables are the other half. A customer pushes a source snapshot for one revision; the
-- platform extracts it, runs discovery over it, classifies the result, and stores the graph. Prompts and
-- tool names are then inputs to a computation on the platform's own side of the boundary — never
-- transmitted, and (see below) never stored here.
--
-- # The distinction from workflow_ir, which is NOT superseded
--
-- `workflow_ir` (0021) holds what the CUSTOMER CHOSE TO SEND: an allowlisted projection, reviewable
-- field by field, that a customer can point at and say "this is what crossed". That property is worth
-- more than any deduplication, so this migration does not fold the two together and does not migrate one
-- into the other. A tenant may have either, both, or neither, and the graph adapter prefers the richer
-- one when it is present. Two tables is the honest shape: they hold different things, obtained different
-- ways, with different consent behind them.
--
-- 🔴 WHAT MAY NOT GO IN platform_workflow_graph
--
-- The stored view is the CLASSIFIER'S OUTPUT — node ids, symbols, model refs, policy names, tool counts,
-- edges, labels, regions. It is not the IR. Prompt text, tool names, I/O-contract schemas and in-scope
-- symbol sets are read during discovery, live in memory for the duration of the job, and are dropped
-- with the extracted tree when the job releases it. The platform holds the source only while it is being
-- analysed, and holds the CONCLUSION afterwards.
--
-- That rule is worth stating in the schema because it is not enforced by the schema: `view_json` is a
-- JSONB column and would accept a prompt without complaint. What enforces it is the projection in
-- internal/hostdiscovery, which builds the view from named classifier fields. A column added here that
-- holds customer text is a boundary change nobody reviewed.
--
-- Dialect: PostgreSQL.

BEGIN;

-- The pushed source snapshot. METADATA ONLY — the bytes live in the blob store, keyed by content_hash,
-- for the reason every large payload does: a 2 GiB row is a replication and backup problem, and Postgres
-- is a poor filesystem. The row is what makes the blob findable and, more importantly, DELETABLE.
CREATE TABLE source_bundle (
    tenant_id       TEXT        NOT NULL,
    workflow_id     TEXT        NOT NULL,
    -- The revision this snapshot is of. Part of the key, exactly as in workflow_ir: a graph discovered
    -- at one revision and scored at another is a picture of neither.
    source_revision TEXT        NOT NULL,
    content_hash    TEXT        NOT NULL,
    size_bytes      BIGINT      NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL,

    -- Re-pushing the same revision REPLACES it. A customer re-running the push at the same commit has
    -- not produced a second snapshot.
    PRIMARY KEY (tenant_id, workflow_id, source_revision),

    -- A zero-byte bundle is not a snapshot, it is a failed upload that would otherwise be recorded as a
    -- successful one and then discovered as "a repository with no files in it".
    CONSTRAINT source_bundle_size_positive CHECK (size_bytes > 0)
);

-- "What source is this tenant holding on our disks" — the question a deletion request asks, and the one
-- a retention job asks. Indexed by age because both read it that way.
CREATE INDEX idx_source_bundle_age ON source_bundle (received_at);

-- The discovered, classified graph. One row per (tenant, workflow, revision).
CREATE TABLE platform_workflow_graph (
    tenant_id        TEXT        NOT NULL,
    workflow_id      TEXT        NOT NULL,
    source_revision  TEXT        NOT NULL,
    ir_version       TEXT        NOT NULL,
    -- The taxonomy the labels were produced under. Stored rather than assumed from the running binary:
    -- a label means what the taxonomy in force when it was computed said it meant, and a console that
    -- renders yesterday's labels under today's taxonomy is quietly relabelling a customer's workflow.
    taxonomy_version TEXT        NOT NULL,
    discovered_at    TIMESTAMPTZ NOT NULL,
    -- How many times the constrained LLM fallback was consulted. 0 means fully rule-covered — the
    -- determinism claim made countable, and the console says which it was rather than implying.
    llm_calls        INTEGER     NOT NULL DEFAULT 0,
    -- The classifier's GraphView, rendered whole and never queried by field, for the reason
    -- workflow_ir.nodes_json is JSONB: the console draws the document, it does not filter it.
    view_json        JSONB       NOT NULL,

    PRIMARY KEY (tenant_id, workflow_id, source_revision),

    CONSTRAINT platform_workflow_graph_llm_calls_nonneg CHECK (llm_calls >= 0)
);

-- The console asks "the newest graph for this workflow" on every view.
CREATE INDEX idx_platform_workflow_graph_latest
    ON platform_workflow_graph (tenant_id, workflow_id, discovered_at DESC);

INSERT INTO schema_migrations (id, name) VALUES (22, 'platform_discovery')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
