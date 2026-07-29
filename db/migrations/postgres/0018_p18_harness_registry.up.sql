-- P18 Harness Strategy Optimization — the sixth registry (harness). Task 2.1.
-- Spec: openspec/changes/p18-harness-strategy-optimization/specs/harness-strategy/spec.md;
-- PRD docs/prd/P18-harness-strategy-optimization.md §6 (FR1–FR7); decisions.md D-1, D-3.
--
-- Dialect: PostgreSQL 11+, exactly as 0002 and 0017. Expand-only: this migration ADDS one table and
-- attaches the guards 0002 already defined. It alters nothing any earlier migration created, so it is
-- safe to deploy before any P18 code is enabled — a `harness_entry` nobody writes to is inert.
--
-- Depends on 0002_p2_registries, and depends on it for more than ordering: `registry_verify_envelope`
-- and `registry_reject_mutation` are 0002's functions, reused verbatim. That reuse is the whole design
-- of this file — the sixth registry is the fifth registry with a different Kind, so it must not acquire
-- a sixth set of guards that could drift from the other five (single source of truth).
--
-- ─────────────────────────────────────────────────────────────────────────────
-- Why a NEW TABLE rather than a discriminator on memory_entry (decisions.md D-1)
-- ─────────────────────────────────────────────────────────────────────────────
-- The registry `Kind` is hashed into every version_id. That is what makes a harness ref pasted into the
-- memory or context dimension FAIL CLOSED instead of resolving to the wrong entry. Dimensions sharing
-- one table would share one id namespace, and the guarantee would be gone — permanently, because once
-- customers' stored config_hashes reference rows in that shared table the dimensions cannot be separated
-- without rewriting ids. One CREATE TABLE is the cheap side of that trade.
--
-- The three structural guards are 0002's, unchanged in intent:
--   1. CHECK harness_entry_content_addressed — version_id = sha256(envelope). New content = new id, so
--      "mutate a published version by re-inserting it" is not expressible.
--   2. TRIGGER harness_entry_immutable / _no_truncate — BEFORE UPDATE OR DELETE (row) and BEFORE TRUNCATE
--      (statement). TRUNCATE needs its own trigger because row-level triggers do not fire for it, and
--      `TRUNCATE harness_entry` is exactly what someone reaching for "just clear the registry" types.
--   3. TRIGGER harness_entry_coherent — BEFORE INSERT: the denormalized name column must equal what the
--      hashed envelope says, and the envelope's kind must be 'harness'. A memory envelope cannot be filed
--      here even by SQL that bypasses internal/registry.

BEGIN;

CREATE TABLE IF NOT EXISTS harness_entry (
    version_id   TEXT         PRIMARY KEY
                              CHECK (version_id ~ '^[0-9a-f]{64}$'),
    name         TEXT         NOT NULL CHECK (name <> ''),
    envelope     BYTEA        NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT harness_entry_content_addressed
        CHECK (version_id = encode(sha256(envelope), 'hex')),
    CONSTRAINT harness_entry_name_version_unique UNIQUE (name, version_id)
);

-- Attach 0002's guards. DROP IF EXISTS + CREATE (not CREATE OR REPLACE TRIGGER, which is PG14+) keeps
-- the migration re-runnable AND re-points an existing trigger at the current function definition.
DROP TRIGGER IF EXISTS harness_entry_coherent ON harness_entry;
CREATE TRIGGER harness_entry_coherent BEFORE INSERT ON harness_entry
    FOR EACH ROW EXECUTE FUNCTION registry_verify_envelope('harness');
DROP TRIGGER IF EXISTS harness_entry_immutable ON harness_entry;
CREATE TRIGGER harness_entry_immutable BEFORE UPDATE OR DELETE ON harness_entry
    FOR EACH ROW EXECUTE FUNCTION registry_reject_mutation();
DROP TRIGGER IF EXISTS harness_entry_no_truncate ON harness_entry;
CREATE TRIGGER harness_entry_no_truncate BEFORE TRUNCATE ON harness_entry
    FOR EACH STATEMENT EXECUTE FUNCTION registry_reject_mutation();

INSERT INTO schema_migrations (id, name) VALUES (18, 'p18_harness_registry')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
