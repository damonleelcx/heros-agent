-- P30 task 2.4: the four tables HEROS needs — the published definition, the pinned inference, the
-- abstentions, and the spend.
--
-- # Why four tables and not one
--
-- They have four different GRAINS and three of them are unrepresentable in the others:
--
--   heros_agent_version   per PUBLISHED DEFINITION, keyed by config_hash. Immutable.
--   heros_inference       per (workflow, revision, config_hash). This IS D2's determinism guarantee.
--   heros_abstention      per DECLINED SUBJECT within an inference — many rows per inference.
--   heros_spend           per (tenant, inference). Caps read it; the inference row is not tenant-keyed.
--
-- Folding the abstentions into `heros_inference` as JSON would make "what did the agent decline, across
-- this tenant's workflows" a scan-and-parse of every result document — and FR3.4's whole point is that
-- not knowing is an OUTPUT, which means it has to be queryable like one.
--
-- # 🔴 The idempotency fence, and why its test must CONTEND
--
-- `UNIQUE (workflow_id, source_revision, agent_config_hash)` on `heros_inference` is D2 made
-- structural: the same three inputs cannot produce two stored results, so "the same revision always
-- shows you the same graph" is a property of the store rather than a claim about a model.
--
-- Its test is a CONCURRENT double-submit against real Postgres. A unique index is invisible to a test
-- that never contends — two sequential inserts exercise the writer's own check and never reach the
-- constraint — and this codebase has already paid for that lesson once.
--
-- # Timestamps are int64 MILLISECONDS
--
-- `BIGINT`, not `TIMESTAMPTZ`, and 🚫 no timestamp literal appears anywhere in this file or in the
-- statements that read these tables. The standing rule, and it earns itself here: these rows are
-- compared against values a runner computed in Go, and a driver rendering a TIMESTAMPTZ into a session
-- time zone is a second clock — which is exactly how four tests in this repository went red on the
-- calendar alone.
--
-- # 🔴 NO COLUMN HERE CAN HOLD A PROVIDER KEY
--
-- `credential_ref` is a PROVIDER NAME resolved through providergateway's Secrets source (D5). There is
-- no `api_key`, no `secret`, no `token`, and no free-text column an operator could paste one into —
-- `spec_json` is the resolved Variant Spec, whose shape is closed by internal/variantspec. The absence
-- is the mechanism, and `TestNoStorageFieldCanCarryAKey` discovers this schema rather than reading a
-- whitelist, so a column added later is caught by the fence and not by review.
--
-- Idempotent, guarded BY DEFINITION: `CREATE TABLE IF NOT EXISTS` is a NAME guard, satisfied by a table
-- of that name with any columns at all, so the DO block checks the keys and the columns the stores
-- actually query.
--
-- Dialect: PostgreSQL only. See 0045's header — the SQLite store is the dev ledger and holds no part of
-- this domain; a copy there would be a second schema nothing reads.

BEGIN;

-- ── The published definition ────────────────────────────────────────────────────────────────────────
--
-- IMMUTABLE. One row per published Variant Spec, addressed by its content hash, so "which definition
-- produced this inference" is answerable forever rather than for as long as nobody edits a config.
CREATE TABLE IF NOT EXISTS heros_agent_version (
    -- internal/confighash over the RESOLVED spec. Content determines identity: there is no mutation
    -- API, and re-publishing an identical definition is the same row rather than a second one.
    config_hash      TEXT   PRIMARY KEY,
    -- The Variant Spec in canonical form. Closed shape (internal/variantspec) — not a free-text field.
    spec_json        TEXT   NOT NULL,
    -- FK-by-value into the operator model registry. By value rather than a real FK because a model may
    -- be deprecated and REMOVED from the registry while inferences that used it must stay interpretable.
    model_ref        TEXT   NOT NULL,
    -- 🔴 A PROVIDER NAME. Never a key value. Resolved at use through providergateway's Secrets source.
    credential_ref   TEXT   NOT NULL,
    -- pending | passed | failed. A definition is INACTIVE until it has run against the pinned fixtures
    -- and met the floor on every one individually (D7).
    rehearsal_state  TEXT   NOT NULL DEFAULT 'pending',
    -- Per-fixture precision/recall. NULL while pending — distinct from an empty report, which would
    -- claim a rehearsal ran and found nothing.
    rehearsal_report TEXT,
    -- NULL unless active. At most one row may be non-NULL; the activation transaction enforces it and
    -- the partial unique index below makes that structural rather than a promise.
    activated_at_ms  BIGINT,
    created_at_ms    BIGINT NOT NULL,

    CONSTRAINT heros_agent_version_rehearsal_state
        CHECK (rehearsal_state IN ('pending', 'passed', 'failed')),
    -- 🔴 A definition cannot be active without having PASSED. The activation path checks this too; both
    -- fail independently, and a future writer that bypasses the Go path still cannot activate an
    -- unrehearsed agent.
    CONSTRAINT heros_agent_version_active_requires_pass
        CHECK (activated_at_ms IS NULL OR rehearsal_state = 'passed')
);

-- EXACTLY ONE ACTIVE VERSION (task 3.7), in the database rather than only in the transaction.
-- A partial unique index over a constant is the standard way to say "at most one row satisfying this
-- predicate": two concurrent activations cannot both win, whatever the application does.
CREATE UNIQUE INDEX IF NOT EXISTS uq_heros_agent_version_active
    ON heros_agent_version ((activated_at_ms IS NOT NULL)) WHERE activated_at_ms IS NOT NULL;

-- ── The pinned inference ────────────────────────────────────────────────────────────────────────────
--
-- D2's whole guarantee. First request infers and stores; every later request reads.
CREATE TABLE IF NOT EXISTS heros_inference (
    inference_id      TEXT   PRIMARY KEY,
    workflow_id       TEXT   NOT NULL,
    source_revision   TEXT   NOT NULL,
    agent_config_hash TEXT   NOT NULL,
    tenant_id         TEXT   NOT NULL,
    -- platform | customer. Which HOST ran it. Both write here — customer-side results arrive through
    -- P29's structure ingest — and the parity test asserts the two produce the same EDGE SET.
    placement         TEXT   NOT NULL,
    -- The facts, each carrying its own author and confidence. Read back whole.
    edges_json        TEXT   NOT NULL DEFAULT '[]',
    labels_json       TEXT   NOT NULL DEFAULT '[]',
    -- ASSESSED, never measured. NULL when the agent produced none.
    narrative         TEXT,
    tokens_in         BIGINT NOT NULL DEFAULT 0,
    tokens_out        BIGINT NOT NULL DEFAULT 0,
    created_at_ms     BIGINT NOT NULL,

    -- 🔴 THE IDEMPOTENCY FENCE. See this file's header for why its test must contend.
    CONSTRAINT uq_heros_inference_key UNIQUE (workflow_id, source_revision, agent_config_hash),
    CONSTRAINT heros_inference_placement CHECK (placement IN ('platform', 'customer')),
    CONSTRAINT heros_inference_tokens_nonneg CHECK (tokens_in >= 0 AND tokens_out >= 0)
);

-- The cache read: "have we already inferred this?" is the first thing every analysis asks.
CREATE INDEX IF NOT EXISTS idx_heros_inference_tenant
    ON heros_inference (tenant_id, workflow_id, created_at_ms DESC);

-- ── Abstentions ─────────────────────────────────────────────────────────────────────────────────────
--
-- FR3.4: NOT KNOWING IS AN OUTPUT. An agent that declines is doing its job, and a store with nowhere to
-- record that would make an abstention indistinguishable from a fact nobody looked for.
CREATE TABLE IF NOT EXISTS heros_abstention (
    inference_id TEXT NOT NULL REFERENCES heros_inference (inference_id) ON DELETE CASCADE,
    -- A node id, or "a→b" for a pair the agent declined to connect.
    subject      TEXT NOT NULL,
    -- 🔴 From a CLOSED ENUM, not prose. A free-text reason is a reason nothing can aggregate, and
    -- "which abstention dominates" is the question that tells an operator what to fix. The vocabulary
    -- is owned in Go (internal/herosagent) rather than pinned in a CHECK here, for the reason
    -- 0044's `state` is not pinned: a ninth reason must be a code change, not a schema migration.
    reason       TEXT NOT NULL,
    -- The confidence that fell below the floor, when there was one. NULL when the agent declined
    -- without producing a candidate at all — a different thing from declining at 0.0.
    confidence   DOUBLE PRECISION,

    PRIMARY KEY (inference_id, subject, reason),
    CONSTRAINT heros_abstention_reason_nonempty CHECK (reason <> ''),
    CONSTRAINT heros_abstention_confidence_range
        CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1))
);

-- ── Spend ───────────────────────────────────────────────────────────────────────────────────────────
--
-- Per tenant per inference. The caps read this BEFORE the provider call (task 9.2), which is why it is
-- its own table: `heros_inference` is keyed by the inference and a cap is asked about a TENANT.
CREATE TABLE IF NOT EXISTS heros_spend (
    tenant_id      TEXT   NOT NULL,
    inference_id   TEXT   NOT NULL REFERENCES heros_inference (inference_id) ON DELETE CASCADE,
    tokens_in      BIGINT NOT NULL DEFAULT 0,
    tokens_out     BIGINT NOT NULL DEFAULT 0,
    -- The estimate, in the deployment's currency. Meaningful ONLY when priced is true.
    estimated_cost DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- 🔴 `priced` is what keeps `unpriced` from rendering as `0` (task 6.5). A model with no published
    -- price produces a real token count and NO cost, and a surface that showed 0 there would report a
    -- spend nobody incurred — the most reassuring possible lie about a bill.
    priced         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at_ms  BIGINT NOT NULL,

    PRIMARY KEY (tenant_id, inference_id),
    CONSTRAINT heros_spend_tokens_nonneg CHECK (tokens_in >= 0 AND tokens_out >= 0),
    CONSTRAINT heros_spend_cost_nonneg CHECK (estimated_cost >= 0),
    -- An unpriced row must not carry a cost: that is the state the boolean exists to make visible, and
    -- a row claiming both would let a reader take the number.
    CONSTRAINT heros_spend_unpriced_has_no_cost
        CHECK (priced OR estimated_cost = 0)
);

-- The per-tenant meter and the cap check, which read by tenant over a window.
CREATE INDEX IF NOT EXISTS idx_heros_spend_tenant_time
    ON heros_spend (tenant_id, created_at_ms DESC);

DO $$
DECLARE
    missing TEXT;
    n INTEGER;
BEGIN
    -- Every table exists. `CREATE TABLE IF NOT EXISTS` is a NAME guard, so a pre-existing table of one
    -- of these names with different columns would have been silently accepted.
    SELECT string_agg(c.name, ', ') INTO missing
      FROM (VALUES ('heros_agent_version'), ('heros_inference'), ('heros_abstention'), ('heros_spend'))
           AS c(name)
     WHERE NOT EXISTS (
        SELECT 1 FROM information_schema.tables WHERE table_name = c.name);
    IF missing IS NOT NULL THEN
        RAISE EXCEPTION 'P30 table(s) missing after this migration: %', missing;
    END IF;

    -- The columns the stores actually query.
    SELECT string_agg(c.name, ', ') INTO missing
      FROM (VALUES ('config_hash'), ('spec_json'), ('model_ref'), ('credential_ref'),
                   ('rehearsal_state'), ('rehearsal_report'), ('activated_at_ms'), ('created_at_ms'))
           AS c(name)
     WHERE NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_name = 'heros_agent_version' AND column_name = c.name);
    IF missing IS NOT NULL THEN
        RAISE EXCEPTION 'heros_agent_version is missing column(s): %', missing;
    END IF;

    SELECT string_agg(c.name, ', ') INTO missing
      FROM (VALUES ('inference_id'), ('workflow_id'), ('source_revision'), ('agent_config_hash'),
                   ('tenant_id'), ('placement'), ('edges_json'), ('labels_json'), ('narrative'),
                   ('tokens_in'), ('tokens_out'), ('created_at_ms'))
           AS c(name)
     WHERE NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_name = 'heros_inference' AND column_name = c.name);
    IF missing IS NOT NULL THEN
        RAISE EXCEPTION 'heros_inference is missing column(s): %', missing;
    END IF;

    -- 🔴 The idempotency fence EXISTS. Without it the writer's own conflict path is decoration: two
    -- concurrent inserts both succeed and the store holds two answers for one key.
    SELECT count(*) INTO n
      FROM pg_constraint
     WHERE conrelid = 'heros_inference'::regclass
       AND contype = 'u'
       AND conname = 'uq_heros_inference_key';
    IF n <> 1 THEN
        RAISE EXCEPTION 'heros_inference has no UNIQUE (workflow_id, source_revision, agent_config_hash). '
                        'That constraint IS D2 — without it the same three inputs can produce two stored '
                        'results and "the same revision always shows you the same graph" is a claim about '
                        'a model rather than a property of the store';
    END IF;

    -- 🔴 At most one ACTIVE definition, structurally.
    -- Resolved through `regclass`, exactly as the constraint check above is, and NOT through
    -- `pg_indexes` — which is database-wide. The first draft used it and passed on a fresh database and
    -- failed on the second schema in the same run, because it was counting an identically-named index
    -- belonging to a different schema. That is the same class of defect 0028 exists to repair: a
    -- catalog guard that matches by NAME alone is satisfied by somebody else's object.
    SELECT count(*) INTO n
      FROM pg_index i
      JOIN pg_class c ON c.oid = i.indexrelid
     WHERE i.indrelid = 'heros_agent_version'::regclass
       AND c.relname = 'uq_heros_agent_version_active';
    IF n <> 1 THEN
        RAISE EXCEPTION 'heros_agent_version has no partial unique index on the active row — two '
                        'concurrent activations could both win, and "which definition is serving '
                        'inference" would depend on which transaction committed last';
    END IF;

    -- 🔴 NO COLUMN CAN HOLD A KEY. Discovered from the catalog, not read from a list, so a column added
    -- later is caught here as well as by the Go fence.
    --
    -- The pattern matches NAME COMPONENTS, not substrings, and that is not fussiness: the first draft
    -- used a plain substring match and it flagged `tokens_in` and `tokens_out`, which are COUNTS. A
    -- fence that fires on the columns it exists to permit is a fence somebody switches off — and this
    -- one caught itself only because these migrations run against a real database before they ship.
    SELECT string_agg(column_name, ', ') INTO missing
      FROM information_schema.columns
     WHERE table_name IN ('heros_agent_version', 'heros_inference', 'heros_abstention', 'heros_spend')
       AND (column_name ~ '(^|_)(api_?key|apikey|secret|password|passwd|bearer|token|credential_value)(_|$)')
       AND column_name <> 'credential_ref';
    IF missing IS NOT NULL THEN
        RAISE EXCEPTION 'a P30 table has column(s) capable of carrying a provider key: %. D5 is that the '
                        'credential is BOUND, never entered — the absence of a field is the mechanism',
                        missing;
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (46, 'p30_heros_agent')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
