-- P2 DevOps — the run queue. Task 6.3.
-- PRD §7 ("runs are enqueued (run-queue seed)"), §12; OQ4 (at-least-once vs exactly-once).
--
-- Dialect: PostgreSQL 11+. Expand-only: ADDS one table. Depends on 0004 (FK -> transform).
--
-- ─────────────────────────────────────────────────────────────────────────────
-- Why Postgres and not a broker
-- ─────────────────────────────────────────────────────────────────────────────
-- P2 is internal-only, single-run-per-run, ~10 optimization runs/day (storage-decision-record A6).
-- A broker would be a second stateful system to operate, back up, and reason about for a workload
-- this size — and, decisively, the queue's whole correctness argument rests on the run records that
-- live HERE: dispatch is at-least-once, and what makes redelivery safe is node_execution's primary
-- key and run's conditional finish. A queue in another system could not share a transaction with the
-- state that makes it safe.
--
-- `SELECT ... FOR UPDATE SKIP LOCKED` is the standard pattern and is exactly right for fan-out: N
-- workers pull disjoint items with no coordination and no lost wakeups.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- At-least-once, on purpose (OQ4)
-- ─────────────────────────────────────────────────────────────────────────────
-- A worker that dies mid-run cannot be distinguished from one that is merely slow. Exactly-once
-- delivery would require the queue to know which — it cannot — so the honest options are:
--
--   at-most-once   drop the run when a worker dies. A silently-missing result is the worst outcome
--                  for an eval platform: P4 scores a variant that never ran as if it had no data.
--   at-least-once  redeliver on lease expiry, and make execution idempotent so redelivery is free.
--
-- P2 takes at-least-once, and it is only defensible because the idempotency is already built and
-- proved: node_execution's PRIMARY KEY (run_id, node_id, attempt_group) turns a double-write into a
-- caught conflict (task 5.2), the provider-facing idempotency key turns a re-execution into one
-- charge (task 5.1), and run.Finish refuses to overwrite a terminal verdict. Without those three,
-- this table would be a machine for double-billing.

BEGIN;

CREATE TABLE IF NOT EXISTS run_queue (
    -- The run_id the worker will execute under. Allocated at ENQUEUE, not at dispatch, so that a
    -- redelivery re-executes the SAME run_id — which is what makes every idempotency guarantee below
    -- apply. A run_id minted per delivery would make each redelivery a brand-new run, and every one
    -- of them would be billed.
    run_id           TEXT         PRIMARY KEY,

    config_hash      TEXT         NOT NULL,
    source_revision  TEXT         NOT NULL CHECK (source_revision <> ''),
    seed             BIGINT       NOT NULL CHECK (seed >= 0),

    --   ready   — dispatchable at/after visible_at
    --   leased  — a worker holds it until lease_expires_at
    --   done    — terminal, acked
    --   dead    — exhausted its attempts; parked for a human, never silently dropped
    state            TEXT         NOT NULL DEFAULT 'ready'
                                  CHECK (state IN ('ready','leased','done','dead')),

    attempts         INTEGER      NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    visible_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    leased_by        TEXT,
    lease_expires_at TIMESTAMPTZ,
    last_error       TEXT         NOT NULL DEFAULT '',
    enqueued_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),

    -- You can only enqueue a run of a transform that exists and built. A queue item for a
    -- configuration that was never transformed would be dispatched and then fail on every attempt
    -- until it dead-lettered — a slow, expensive way to discover a spec was never applied.
    CONSTRAINT run_queue_transform_fk
        FOREIGN KEY (config_hash, source_revision) REFERENCES transform (config_hash, source_revision),

    -- A lease with no expiry never expires, so its run would never be redelivered — the one failure
    -- at-least-once exists to prevent. An expiry on an unleased item is meaningless.
    CONSTRAINT run_queue_lease_coherent
        CHECK ((state = 'leased') = (lease_expires_at IS NOT NULL)),
    -- A dead letter must say why. A parked item nobody can diagnose is one nobody will ever revive.
    CONSTRAINT run_queue_dead_has_a_reason
        CHECK (state <> 'dead' OR last_error <> '')
);

-- The dispatch path's only index. Partial, on the two states a claim can touch: `done` items
-- accumulate forever and must not sit in the hot index that every worker scans.
CREATE INDEX IF NOT EXISTS idx_run_queue_dispatchable
    ON run_queue (visible_at, enqueued_at)
    WHERE state IN ('ready', 'leased');

-- NOTE: run_queue is deliberately NOT immutable, unlike every other P2 table. It is the one piece of
-- MUTABLE state in the phase — a work item legitimately moves ready -> leased -> done. The records it
-- produces (run, node_execution, transform) are the immutable ones; the queue is scaffolding that
-- points at them.

INSERT INTO schema_migrations (id, name) VALUES (6, 'p2_run_queue')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
