-- P34 Harness/Loop/Graph split — the seventh registry (loop). Tasks 3.2, 8.1, 8.2.
-- Spec: openspec/changes/p34-harness-loop-graph-split/specs/loop-strategy/spec.md;
-- PRD docs/prd/P34-harness-loop-graph-split.md §6 (FR1–FR4); decisions.md D-34.1; ADR-014.
--
-- Dialect: PostgreSQL 11+, exactly as 0002 and 0018. This is 0018 with a different Kind, and being
-- boringly identical to it is the design: the seventh registry must not acquire a seventh set of guards
-- that can drift from the other six.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- What this migration does NOT do, and why that is the whole of task 8.1
-- ─────────────────────────────────────────────────────────────────────────────
-- It adds ONE TABLE and NO COLUMN to any table already deployed.
--
-- That is not a stylistic preference. ADR-014 refuses the contract half of expand-contract on the
-- record: removing the loop fields from a harness entry would change its `version_id`, which changes the
-- `config_hash` of every spec referencing it, which makes every measurement taken on a multi-turn node
-- UNREACHABLE from any spec anyone can construct. A migration that altered `harness_entry` would be that
-- same chain arriving through the database instead of through the seal path. So `harness_entry` is not
-- touched here, in either direction, ever.
--
-- The legacy path is therefore inert with respect to this file: a deployment that never registers a loop
-- entry has a `loop_entry` nobody writes to, and behaves exactly as it did before.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- IDEMPOTENCY GUARD (task 8.1 — the commit body must name it)
-- ─────────────────────────────────────────────────────────────────────────────
-- Three, one per statement class, because `IF NOT EXISTS` does not exist for every one of them:
--   1. CREATE TABLE IF NOT EXISTS            — the table.
--   2. DROP TRIGGER IF EXISTS + CREATE       — the triggers. Not `CREATE OR REPLACE TRIGGER`, which is
--                                              PG14+; this pair is re-runnable AND re-points an existing
--                                              trigger at the current function definition.
--   3. INSERT ... ON CONFLICT (id) DO NOTHING — the schema_migrations marker.
-- A second run of this file returns success and changes nothing.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- EDITION SCOPE (task 8.2)
-- ─────────────────────────────────────────────────────────────────────────────
-- The registries are a control-plane concern. This migration runs wherever 0002 and 0018 ran and
-- nowhere else — it depends on 0002's `registry_verify_envelope` and `registry_reject_mutation`, so a
-- component that never created those cannot run this, and the dependency is what enforces the scope
-- rather than a list somebody maintains.

BEGIN;

CREATE TABLE IF NOT EXISTS loop_entry (
    version_id   TEXT         PRIMARY KEY
                              CHECK (version_id ~ '^[0-9a-f]{64}$'),
    name         TEXT         NOT NULL CHECK (name <> ''),
    envelope     BYTEA        NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT loop_entry_content_addressed
        CHECK (version_id = encode(sha256(envelope), 'hex')),
    CONSTRAINT loop_entry_name_version_unique UNIQUE (name, version_id)
);

-- Attach 0002's guards, unchanged in intent:
--   1. CHECK loop_entry_content_addressed — version_id = sha256(envelope), so "mutate a published
--      version by re-inserting it" is not expressible.
--   2. TRIGGER loop_entry_immutable / _no_truncate — BEFORE UPDATE OR DELETE (row) and BEFORE TRUNCATE
--      (statement). TRUNCATE needs its own trigger because row-level triggers do not fire for it, and
--      `TRUNCATE loop_entry` is exactly what someone reaching for "just clear the registry" types.
--   3. TRIGGER loop_entry_coherent — BEFORE INSERT: the denormalized name column must equal what the
--      hashed envelope says, and the envelope's kind must be 'loop'. 🔴 That last clause is the database
--      half of the fail-closed guarantee: a HARNESS envelope cannot be filed here even by SQL that
--      bypasses internal/registry, so the two id spaces cannot be crossed by any path.
DROP TRIGGER IF EXISTS loop_entry_coherent ON loop_entry;
CREATE TRIGGER loop_entry_coherent BEFORE INSERT ON loop_entry
    FOR EACH ROW EXECUTE FUNCTION registry_verify_envelope('loop');
DROP TRIGGER IF EXISTS loop_entry_immutable ON loop_entry;
CREATE TRIGGER loop_entry_immutable BEFORE UPDATE OR DELETE ON loop_entry
    FOR EACH ROW EXECUTE FUNCTION registry_reject_mutation();
DROP TRIGGER IF EXISTS loop_entry_no_truncate ON loop_entry;
CREATE TRIGGER loop_entry_no_truncate BEFORE TRUNCATE ON loop_entry
    FOR EACH STATEMENT EXECUTE FUNCTION registry_reject_mutation();

INSERT INTO schema_migrations (id, name) VALUES (51, 'p34_loop_registry')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
