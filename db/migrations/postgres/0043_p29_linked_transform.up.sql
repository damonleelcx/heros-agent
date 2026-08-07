-- P29: what a transform the customer generated on their own machine actually did.
--
-- # Why a NEW TABLE, which is a one-way door
--
-- `careful-table-creation` requires the alternatives to be named and rejected, so here they are:
--
--   a column set on `workflow_ir`  — keyed (tenant, workflow, revision). A transform is keyed by
--                                    (tenant, CONFIGURATION, revision): two configurations applied at
--                                    one revision are two transforms, and that table cannot hold two.
--   a column set on `run_link`     — keyed by RUN. `heros apply` produces a transform with no run at
--                                    all, so the row would have nowhere to live for the common case.
--   a JSON blob on either          — the surface addresses it by (config_hash, source_revision); a blob
--                                    cannot be indexed by a key it does not have, and every read would
--                                    become a scan-and-filter of somebody else's document.
--
-- The grain is genuinely new, and it is exactly the key
-- `/app/transforms/{config_hash}/{source_revision}` addresses — the surface that could not resolve for
-- anything before this existed.
--
-- # What this table may and may not hold
--
-- 🔴 NEVER A DIFF. Not a hunk, not a line, not a file path. `files_changed`, `lines_added` and
-- `lines_removed` are three integers standing exactly where a diff would go, and there is no column here
-- one could occupy. That is the same construction argument `workflow_ir` rests on, and it is why the
-- allowlist in `internal/runlink/transformreceipt.go` is the review artifact: a column with no allowlist
-- entry behind it is a boundary change nobody reviewed.
--
-- `node_outcomes_json` holds `[{node_id, outcome, cause?}]` — identifiers and closed-set members. The
-- engine's refusal SENTENCES, which name arguments and symbols out of the customer's source, have no
-- column and are refused at the ingest boundary.
--
-- # `coverage_version` is NULLABLE for the reason 0042's is
--
-- A client that reports no version has its absence recorded. The platform never substitutes its own
-- table version: that would date somebody else's outcomes to a table they were never computed against,
-- and it is the projection's staleness label that would be suppressed by the lie.
--
-- # Idempotent, guarded by DEFINITION
--
-- `CREATE TABLE IF NOT EXISTS` is a NAME guard — it is satisfied by a table of that name with any
-- columns at all. The DO block below checks the primary key and the columns the stores actually query,
-- so a second run against a half-created or hand-patched schema fails loudly instead of agreeing.
--
-- Dialect: PostgreSQL, and ONLY PostgreSQL — see 0042's header for why there is no second dialect for
-- this table family.

BEGIN;

CREATE TABLE IF NOT EXISTS linked_transform (
    tenant_id       TEXT        NOT NULL,
    -- The configuration and the revision. Together they ARE the transform: the engine is a pure function
    -- of (bytes at revision, resolved config), so the same pair cannot describe two different diffs and
    -- a second row for one pair would be the same answer twice.
    config_hash     TEXT        NOT NULL,
    source_revision TEXT        NOT NULL,
    workflow_id     TEXT        NOT NULL,
    tool_version    TEXT        NOT NULL DEFAULT '',
    -- NULL = the client did not report one. Never defaulted to this build's table: see the header.
    coverage_version TEXT,
    -- The transform's own terminal state, verbatim from the engine. A closed value, not a sentence.
    status          TEXT        NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL,
    -- [{node_id, outcome, cause?}] — read back whole, never queried by field, exactly as
    -- workflow_ir.nodes_json is.
    node_outcomes_json JSONB    NOT NULL DEFAULT '[]'::jsonb,
    -- 🔴 THE ENTIRE DIFF, as it crosses this boundary. Three integers.
    files_changed   INTEGER     NOT NULL DEFAULT 0,
    lines_added     INTEGER     NOT NULL DEFAULT 0,
    lines_removed   INTEGER     NOT NULL DEFAULT 0,

    -- Re-transmitting the same receipt REPLACES it. The engine is deterministic in these two inputs, so
    -- a second transmission is the same answer, and two rows would make "which transform is this"
    -- depend on insertion order — the same reason workflow_ir upserts rather than appends.
    PRIMARY KEY (tenant_id, config_hash, source_revision),

    -- A count cannot be negative. Checked in the database as well as at the ingest boundary because the
    -- two fail independently: a future writer that bypasses the handler still cannot store a diffstat
    -- that renders as "-3 files changed" on a paid surface.
    CONSTRAINT linked_transform_counts_nonneg
        CHECK (files_changed >= 0 AND lines_added >= 0 AND lines_removed >= 0)
);

-- The tenant's transform list, newest first — what /app/transforms enumerates.
CREATE INDEX IF NOT EXISTS idx_linked_transform_tenant
    ON linked_transform (tenant_id, received_at DESC);

DO $$
DECLARE
    pk TEXT;
    missing TEXT;
BEGIN
    SELECT string_agg(a.attname, ',' ORDER BY x.ordinality) INTO pk
      FROM pg_constraint c
      JOIN LATERAL unnest(c.conkey) WITH ORDINALITY AS x(attnum, ordinality) ON TRUE
      JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = x.attnum
     WHERE c.conrelid = 'linked_transform'::regclass AND c.contype = 'p';

    IF pk IS DISTINCT FROM 'tenant_id,config_hash,source_revision' THEN
        RAISE EXCEPTION 'linked_transform primary key is (%), expected (tenant_id,config_hash,source_revision) '
                        '— the grain IS the key, and a different one makes the upsert append instead', pk;
    END IF;

    SELECT string_agg(c.name, ', ') INTO missing
      FROM (VALUES ('workflow_id'), ('tool_version'), ('coverage_version'), ('status'),
                   ('received_at'), ('node_outcomes_json'), ('files_changed'),
                   ('lines_added'), ('lines_removed')) AS c(name)
     WHERE NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_name = 'linked_transform' AND column_name = c.name);

    IF missing IS NOT NULL THEN
        RAISE EXCEPTION 'linked_transform is missing column(s): % — CREATE TABLE IF NOT EXISTS is a NAME '
                        'guard, so a pre-existing table of this name with different columns would have '
                        'been silently accepted', missing;
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (43, 'p29_linked_transform')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
