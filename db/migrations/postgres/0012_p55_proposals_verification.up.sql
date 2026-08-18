-- P5.5 Proposal operators + Verification gate — proposals, evidence, verdicts, rank entries.
-- Spec:   openspec/changes/archive/2026-07-23-p5.5-proposals-verification/specs/{proposal-engine,verification}/spec.md
-- Design: openspec/changes/archive/2026-07-23-p5.5-proposals-verification/design.md "Data model sketch" (tasks 5.4).
--
-- Dialect: PostgreSQL 11+. EXPAND-ONLY. It ADDS four new tables and one nullable column to eval_result
-- (split), ALTERs no existing column, drops nothing, and rewrites no row — the additive-evolution rule
-- P0 mandated and the backend "给已部署表加列必须走独立 ALTER" 铁律 (the split column is a standalone,
-- nullable ADD COLUMN, so every pre-P5.5 eval_result row stays valid with a NULL split).
--
-- Load-bearing properties (task 5.4, 5.5):
--
--   * Verification runs are ORDINARY eval_result rows — there is NO second copy of the traces. This
--     migration only adds the `split` tag to eval_result so a verification run is attributable to an
--     exact (candidate_config_hash, eval_set_hash, split, seed). Proposals / evidence / verdicts / rank
--     entries reference those runs by their existing tags, never by duplicating them.
--   * Every large or possibly-PII payload is CONTENT-HASHED, never inline: the candidate source diff,
--     rendered candidate prompts, and prompt-optimizer grounding bundles live in the object store keyed
--     by hash; these rows hold only the hash (source_diff_blob_hash, grounding_blob_hash).
--   * A build_failed proposal is retained for diagnostics but is never ranked or surfaced — the CHECK
--     on `status` keeps 'build_failed' a first-class terminal state, and a rank_entry / verdict for it
--     is a data error, not a UI decision.
--   * Every verdict row is a predicate over `gate_result`; the recommendation surface reads only
--     gate_result='pass' (the nothing-unverified guarantee lives in the query, not the renderer).

BEGIN;

-- Attribute a verification eval_result to the exact split it ran on (held_out / generating / full).
ALTER TABLE eval_result ADD COLUMN IF NOT EXISTS split TEXT NULL
    CHECK (split IS NULL OR split IN ('held_out', 'generating', 'full'));

-- A proposal is a candidate Variant Spec + the source diff its codemod produced, born from a diagnosis.
CREATE TABLE IF NOT EXISTS proposal (
    proposal_id           TEXT        PRIMARY KEY,
    diagnosis_id          TEXT        NOT NULL,
    operator              TEXT        NOT NULL,
    base_variant_id       TEXT        NOT NULL,
    candidate_config_hash TEXT        NOT NULL CHECK (candidate_config_hash ~ '^[0-9a-f]{64}$'),
    source_revision       TEXT        NOT NULL CHECK (source_revision <> ''),
    -- Content-hash references into the object store; the bytes are never inline here.
    source_diff_blob_hash TEXT        NULL CHECK (source_diff_blob_hash IS NULL OR source_diff_blob_hash ~ '^[0-9a-f]{64}$'),
    grounding_blob_hash   TEXT        NULL CHECK (grounding_blob_hash IS NULL OR grounding_blob_hash ~ '^[0-9a-f]{64}$'),
    build_status          TEXT        NOT NULL DEFAULT 'unbuilt'
                                      CHECK (build_status IN ('unbuilt', 'built', 'build_failed')),
    status                TEXT        NOT NULL DEFAULT 'candidate'
                                      CHECK (status IN ('candidate', 'build_failed', 'verifying', 'verified',
                                                        'gate_failed', 'constraint_excluded')),
    -- A build_failed proposal must record its build status too — the two states are consistent.
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT proposal_build_failed_consistent
        CHECK (status <> 'build_failed' OR build_status = 'build_failed')
);

-- The specific failing cases attached to a proposal as evidence, tagged by the split role each plays.
CREATE TABLE IF NOT EXISTS proposal_evidence (
    proposal_id TEXT NOT NULL REFERENCES proposal (proposal_id),
    case_id     TEXT NOT NULL,
    role        TEXT NOT NULL CHECK (role IN ('generating', 'held_out')),
    PRIMARY KEY (proposal_id, case_id)
);

-- The measured verdict — the source of truth. Held-out delta with CI, cost/latency impact, cases
-- fixed/broken, and the terminal gate_result. The recommendation surface reads only gate_result='pass'.
CREATE TABLE IF NOT EXISTS verdict (
    proposal_id     TEXT             PRIMARY KEY REFERENCES proposal (proposal_id),
    metric          TEXT             NOT NULL,
    delta           DOUBLE PRECISION NOT NULL,
    ci_low          DOUBLE PRECISION NOT NULL,
    ci_high         DOUBLE PRECISION NOT NULL,
    significant     BOOLEAN          NOT NULL,
    held_out        BOOLEAN          NOT NULL,
    cost_delta      DOUBLE PRECISION NOT NULL,
    latency_delta   DOUBLE PRECISION NOT NULL,
    regression_pass BOOLEAN          NOT NULL,
    cases_fixed_json  JSONB          NOT NULL DEFAULT '[]'::jsonb,
    cases_broken_json JSONB          NOT NULL DEFAULT '[]'::jsonb,
    gate_result     TEXT             NOT NULL
                                     CHECK (gate_result IN ('pass', 'fail_significance', 'fail_regression',
                                                            'fail_constraint')),
    -- A CI is an interval: low <= high, always.
    CONSTRAINT verdict_ci_ordered CHECK (ci_low <= ci_high)
);

-- One ranking entry per proposal per ranking context (pre- vs post-verification). A constraint-excluded
-- entry names the violated constraint and is not part of the recommendation order.
CREATE TABLE IF NOT EXISTS rank_entry (
    proposal_id         TEXT             NOT NULL REFERENCES proposal (proposal_id),
    ranking_context     TEXT             NOT NULL,
    expected_gain       DOUBLE PRECISION NOT NULL,
    cost_of_change      DOUBLE PRECISION NOT NULL,
    score               DOUBLE PRECISION NOT NULL,
    constraint_status   TEXT             NOT NULL DEFAULT 'ok'
                                         CHECK (constraint_status IN ('ok', 'excluded')),
    violated_constraint TEXT             NULL,
    -- An excluded entry must name why; an ok entry must not pretend one.
    CONSTRAINT rank_entry_excluded_has_reason
        CHECK ((constraint_status = 'excluded') = (violated_constraint IS NOT NULL)),
    PRIMARY KEY (proposal_id, ranking_context)
);

CREATE INDEX IF NOT EXISTS idx_verdict_gate_result ON verdict (gate_result);
CREATE INDEX IF NOT EXISTS idx_proposal_diagnosis ON proposal (diagnosis_id);

INSERT INTO schema_migrations (id, name) VALUES (12, 'p55_proposals_verification')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
