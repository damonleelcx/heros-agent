-- 0001_baseline — goals, tasks, checkpoints.
--
-- # Why the lease lives on the task row
--
-- A lease is the fact "task T is claimed by worker W until time X". The only place that fact can be
-- evaluated atomically against the work it guards is the row itself. A separate lease table, or a
-- broker holding leases in its own memory, reintroduces the failure it was meant to prevent: the
-- holder restarts, its leases vanish, and two workers hold one task while each believes it is alone.
--
-- # Why there is no lease-expiry sweeper
--
-- Expiry is evaluated in the claim query's WHERE clause. A sweeper is itself a process that can be
-- down, and while it is down every crashed worker's task stays claimed — a silent, total stall that
-- looks exactly like "there is no work to do".
--
-- # Idempotency
--
-- The partial unique index on (goal_id, idempotency_key) is what makes a retried side effect safe at
-- the DATABASE rather than in application logic. Application-side de-duplication loses the race it
-- exists to win: two workers both read "no existing PR", both proceed, and the customer finds two.
--
-- Every statement is idempotent so the migration can be re-run.

CREATE TABLE IF NOT EXISTS goals (
    id                   TEXT PRIMARY KEY,
    tenant               TEXT        NOT NULL,
    intent               TEXT        NOT NULL,
    objective            TEXT        NOT NULL DEFAULT '',
    repo_url             TEXT        NOT NULL,
    revision             TEXT        NOT NULL,
    workflow_id          TEXT        NOT NULL DEFAULT '',
    axes                 JSONB       NOT NULL DEFAULT '[]'::jsonb,
    ceilings             JSONB       NOT NULL,
    spend                JSONB       NOT NULL DEFAULT '{}'::jsonb,
    criteria             JSONB       NOT NULL DEFAULT '[]'::jsonb,
    milestones           JSONB       NOT NULL DEFAULT '[]'::jsonb,
    state                TEXT        NOT NULL,
    refusal              JSONB,
    expected_duration_ns BIGINT      NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL,
    last_checkpoint      TIMESTAMPTZ,

    -- A goal must be bounded before it can be admitted. Enforced here as well as in Go because a
    -- ceiling that only exists in application code is one bypassing writer away from an unbounded run.
    CONSTRAINT goals_axes_is_array CHECK (jsonb_typeof(axes) = 'array'),
    CONSTRAINT goals_criteria_is_array CHECK (jsonb_typeof(criteria) = 'array'),
    CONSTRAINT goals_state_known CHECK (state IN
        ('draft','running','paused','succeeded','failed','refused','cancelled')),
    -- 🔴 A refused goal must say why. A refusal with no cause is a dead end for whoever reads the row.
    CONSTRAINT goals_refusal_has_cause CHECK (state <> 'refused' OR refusal IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS goals_claimable ON goals (state) WHERE state = 'running';
CREATE INDEX IF NOT EXISTS goals_by_tenant ON goals (tenant, created_at DESC);

CREATE TABLE IF NOT EXISTS tasks (
    goal_id         TEXT        NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
    id              TEXT        NOT NULL,
    kind            TEXT        NOT NULL,
    depends_on      JSONB       NOT NULL DEFAULT '[]'::jsonb,
    state           TEXT        NOT NULL,
    attempt         INTEGER     NOT NULL DEFAULT 0,
    spawn_depth     INTEGER     NOT NULL DEFAULT 0,
    idempotency_key TEXT        NOT NULL DEFAULT '',
    result          BYTEA,
    failure         TEXT        NOT NULL DEFAULT '',
    leased_by       TEXT        NOT NULL DEFAULT '',
    lease_expiry    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,

    PRIMARY KEY (goal_id, id),
    CONSTRAINT tasks_state_known CHECK (state IN
        ('pending','ready','running','awaiting_approval','succeeded','failed','blocked','cancelled')),
    -- A claim is either whole or absent: a holder with no expiry can never be reclaimed, and an expiry
    -- with no holder cannot be attributed.
    -- 🔴 depends_on must be an ARRAY, never the scalar `null`. A nil Go slice marshals to `null`, and
    -- `jsonb_array_elements_text` on a scalar raises an error at CLAIM time — far from the write that
    -- caused it, on the dependency-free task that every DAG starts with. Fail at INSERT instead.
    CONSTRAINT tasks_depends_on_is_array CHECK (jsonb_typeof(depends_on) = 'array'),
    CONSTRAINT tasks_lease_is_whole CHECK
        ((leased_by = '' AND lease_expiry IS NULL) OR (leased_by <> '' AND lease_expiry IS NOT NULL))
);

-- The claim query's driving index: ready work for one goal, cheapest first.
CREATE INDEX IF NOT EXISTS tasks_claimable ON tasks (goal_id, state)
    WHERE state IN ('pending','ready','running');

-- 🔴 Idempotency enforced by the DATABASE. Application-side de-duplication loses the race it exists to
-- win: two workers both read "no existing pull request", both proceed, and the customer finds two.
CREATE UNIQUE INDEX IF NOT EXISTS tasks_idempotency ON tasks (goal_id, idempotency_key)
    WHERE idempotency_key <> '';

CREATE TABLE IF NOT EXISTS checkpoints (
    goal_id   TEXT        NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
    iteration INTEGER     NOT NULL,
    note      TEXT        NOT NULL DEFAULT '',
    spend     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    at        TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (goal_id, iteration)
);

CREATE INDEX IF NOT EXISTS checkpoints_latest ON checkpoints (goal_id, iteration DESC);
