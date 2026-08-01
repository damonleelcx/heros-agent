-- P11 run linking — the durable store (P19 §11, task 8).
--
-- Until this existed, `linkingest` had exactly ONE Store implementation and it was in-memory, which is
-- why the capability was registered-but-not-mounted on every deployment: mounting it would have
-- accepted a developer's linked run, answered 200, and forgotten it on the next pod restart. "Not
-- installed" is a worse-sounding but better-behaved state than "installed and quietly lossy".
--
-- Dialect: PostgreSQL. The local SQLite ledger is a different store and does not load this.
--
-- Two tables because they answer two different questions and fail differently. `run_link` is the
-- numerator — which runs a tenant actually linked, and the identity that makes a re-link idempotent.
-- `run_link_coverage` is the DENOMINATOR the CLI reported, which is a claim about activity we did not
-- observe. Keeping them apart is what lets coverage report UNKNOWN rather than zero: a tenant with
-- links and no reported denominator has a numerator and no ratio, and that is a real state.

BEGIN;

CREATE TABLE run_link (
    tenant_id       TEXT        NOT NULL,
    run_id          TEXT        NOT NULL,
    workflow_id     TEXT        NOT NULL,
    config_hash     TEXT        NOT NULL,
    source_revision TEXT        NOT NULL,
    tool_version    TEXT        NOT NULL,
    linked_at       TIMESTAMPTZ NOT NULL,
    -- The eval scores AS COMPUTED by the CLI's harness, stored so a linked run's scorecard in the
    -- console shows the same numbers the developer saw locally. Parity is a STORED FACT here, never a
    -- re-derivation — a re-derivation can drift from what was reported and nobody would know which is
    -- right. JSONB, because it is read back whole and never queried by field.
    scores_json     JSONB       NOT NULL DEFAULT '[]'::jsonb,

    -- 🔴 The idempotency key IS the primary key. FR14 requires that re-linking a run reports
    -- `already=true` rather than double-counting it, and a UNIQUE index enforces that even when
    -- application code forgets — two CI jobs racing on the same run cannot both insert.
    PRIMARY KEY (tenant_id, run_id)
);

CREATE INDEX idx_run_link_tenant_linked_at ON run_link (tenant_id, linked_at DESC);
CREATE INDEX idx_run_link_workflow         ON run_link (tenant_id, workflow_id);

CREATE TABLE run_link_coverage (
    tenant_id     TEXT    PRIMARY KEY,
    -- The MAXIMUM denominator ever reported, never the latest. A CLI that reports 40 after reporting
    -- 100 has not made the tenant's activity smaller — it has reported a narrower slice, and taking the
    -- latest would silently improve the coverage ratio. Monotonic by CHECK and by the writer's GREATEST.
    runs_reported INTEGER NOT NULL DEFAULT 0 CHECK (runs_reported >= 0),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO schema_migrations (id, name) VALUES (20, 'p11_run_links')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
