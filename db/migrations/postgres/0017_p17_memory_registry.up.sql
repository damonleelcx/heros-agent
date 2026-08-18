-- P17 Memory Strategy Optimization — the fifth registry (memory). Task 5.1.
-- Spec: openspec/changes/archive/2026-08-01-p17-memory-strategy-optimization/specs/memory-store/spec.md;
-- PRD docs/prd/P17-memory-strategy-optimization.md §6 (FR1–FR6); decisions.md D1.
--
-- Dialect: PostgreSQL 11+, exactly as 0002. Expand-only: this migration ADDS one table and attaches the
-- guards 0002 already defined. It alters nothing any earlier migration created, so it is safe to deploy
-- before any P17 code is enabled — a `memory_entry` nobody writes to is inert.
--
-- Depends on 0002_p2_registries, and depends on it for more than ordering: `registry_verify_envelope`
-- and `registry_reject_mutation` are 0002's functions, reused verbatim. That reuse is the whole design
-- of this file — the fifth registry is the fourth registry with a different Kind, so it must not acquire
-- a fifth set of guards that could drift from the other four (禁止分裂 source-of-truth).
--
-- ─────────────────────────────────────────────────────────────────────────────
-- Why a NEW TABLE rather than a discriminator on context_entry (decisions.md D1)
-- ─────────────────────────────────────────────────────────────────────────────
-- The registry `Kind` is hashed into every version_id. That is what makes a memory ref pasted into the
-- context dimension FAIL CLOSED instead of resolving to the wrong entry. Two dimensions sharing one
-- table would share one id namespace, and the guarantee would be gone — permanently, because once
-- customers' stored config_hashes reference rows in that shared table the two dimensions cannot be
-- separated without rewriting ids. One CREATE TABLE is the cheap side of that trade.
--
-- The three structural guards are 0002's, unchanged in intent:
--   1. CHECK memory_entry_content_addressed — version_id = sha256(envelope). New content = new id, so
--      "mutate a published version by re-inserting it" is not expressible.
--   2. TRIGGER memory_entry_immutable / _no_truncate — BEFORE UPDATE OR DELETE (row) and BEFORE TRUNCATE
--      (statement). TRUNCATE needs its own trigger because row-level triggers do not fire for it, and
--      `TRUNCATE memory_entry` is exactly what someone reaching for "just clear the registry" types.
--   3. TRIGGER memory_entry_coherent — BEFORE INSERT: the denormalized name column must equal what the
--      hashed envelope says, and the envelope's kind must be 'memory'. A context envelope cannot be
--      filed here even by SQL that bypasses internal/registry.

BEGIN;

CREATE TABLE IF NOT EXISTS memory_entry (
    version_id   TEXT         PRIMARY KEY
                              CHECK (version_id ~ '^[0-9a-f]{64}$'),
    name         TEXT         NOT NULL CHECK (name <> ''),
    envelope     BYTEA        NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT memory_entry_content_addressed
        CHECK (version_id = encode(sha256(envelope), 'hex')),
    CONSTRAINT memory_entry_name_version_unique UNIQUE (name, version_id)
);

-- Attach 0002's guards. DROP IF EXISTS + CREATE (not CREATE OR REPLACE TRIGGER, which is PG14+) keeps
-- the migration re-runnable AND re-points an existing trigger at the current function definition.
DROP TRIGGER IF EXISTS memory_entry_coherent ON memory_entry;
CREATE TRIGGER memory_entry_coherent BEFORE INSERT ON memory_entry
    FOR EACH ROW EXECUTE FUNCTION registry_verify_envelope('memory');
DROP TRIGGER IF EXISTS memory_entry_immutable ON memory_entry;
CREATE TRIGGER memory_entry_immutable BEFORE UPDATE OR DELETE ON memory_entry
    FOR EACH ROW EXECUTE FUNCTION registry_reject_mutation();
DROP TRIGGER IF EXISTS memory_entry_no_truncate ON memory_entry;
CREATE TRIGGER memory_entry_no_truncate BEFORE TRUNCATE ON memory_entry
    FOR EACH STATEMENT EXECUTE FUNCTION registry_reject_mutation();

INSERT INTO schema_migrations (id, name) VALUES (17, 'p17_memory_registry')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
