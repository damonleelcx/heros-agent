-- P2 Configuration Layer — the four registries (model / prompt / skill / context). Task 1.1.
-- Spec: openspec/changes/p2-config-runtime/specs/registries/spec.md; PRD docs/prd/P2-config-runtime.md
-- §6 (FR6–FR10) and §8 (storage). Applies ADR-001's framing: a registry version pins the values a
-- codemod rewrites into a call site, so a version that can drift makes a generated diff undrifted
-- only by luck.
--
-- Dialect: PostgreSQL 11+ (the content-address CHECK uses the built-in sha256(bytea); CI and the
-- proof harnesses run 16). Expand-only: this migration ADDS four tables and two functions. It alters
-- nothing 0001 created, so it is safe to deploy before any P2 code is enabled.
--
-- Depends on 0001_p0_lineage (prompt_entry.body_blob_hash -> blob.content_hash).
--
-- ─────────────────────────────────────────────────────────────────────────────
-- The invariant this migration structurally enforces (FR6)
-- ─────────────────────────────────────────────────────────────────────────────
-- A registry version_id IS the SHA-256 of the entry's content. Three independent structural guards
-- make "a published version_id addresses exactly these bytes, forever" impossible to violate — even
-- when application code forgets. The DB is the last line; internal/registry is the first.
--
--   1. CHECK <t>_content_addressed  — version_id = sha256(envelope). An INSERT that claims a
--      version_id for content that does not hash to it is rejected. This is what makes "mutate a
--      published version by re-inserting it with new content" impossible: new content = new id.
--   2. TRIGGER <t>_immutable / <t>_no_truncate — BEFORE UPDATE OR DELETE (row) and BEFORE TRUNCATE
--      (statement) ... RAISE. This is what makes "mutate a published version in place" impossible.
--      A change must publish a new version. TRUNCATE needs its own trigger: row-level triggers do
--      NOT fire for it, so without one, `TRUNCATE model_entry` would silently erase every published
--      version — the exact statement someone reaching for "just clear the registry" types.
--   3. TRIGGER <t>_coherent         — BEFORE INSERT: the denormalized name/kind columns must equal
--      what the hashed envelope says. Columns cannot drift from the content they index.
--
-- Why `envelope` is BYTEA and not TEXT/JSONB — this is load-bearing, do not "clean it up":
--   * version_id addresses BYTES, so the column stores the exact bytes that were hashed. sha256()
--     and encode() are both IMMUTABLE, so guard 1 is a plain CHECK with no caveats.
--   * TEXT would force sha256(convert_to(envelope,'UTF8')), and convert_to is only STABLE. (PG
--     accepts a stable function in a CHECK, but relying on that laxness for the one invariant the
--     whole registry rests on is a bad trade.) The `::bytea` cast is worse than useless here: it
--     runs byteain, which DECODES backslash escapes — '{"a":"b\\c"}' (12 chars) casts to 11 bytes,
--     so every template containing \n or \" would hash wrong.
--   * JSONB cannot work at all: it re-serializes (its key order is length-then-byte, RFC 8785's is
--     byte-wise), so jsonb::text is not the canonical form and would never re-hash to version_id.
-- Read the envelope in psql with:  SELECT convert_from(envelope,'UTF8') FROM model_entry;
--
-- Envelope shape (canonical JSON per internal/confighash — recursively sorted keys, no whitespace):
--   {"kind":"model|prompt|skill|context","name":"<entry name>","spec":{ ...per-kind... }}
-- `kind` is inside the hash so a model and a prompt with an identical name+spec cannot collide onto
-- one version_id: a version_id is globally unique across the four registries, so a ref pasted into
-- the wrong dimension fails closed instead of silently resolving.

BEGIN;

-- ─────────────────────────────────────────────────────────────────────────────
-- Guard functions (shared by all four registries — one definition, four uses)
-- ─────────────────────────────────────────────────────────────────────────────

-- Custom SQLSTATEs so a caller (and the proof script) can assert WHICH guard fired, rather than
-- accepting "some error" as proof. Postgres reserves no class beginning 'HR'.
--   HR001 — registry_immutable_violation
--   HR002 — registry_envelope_incoherent
CREATE OR REPLACE FUNCTION registry_reject_mutation() RETURNS trigger
    LANGUAGE plpgsql AS $fn$
BEGIN
    RAISE EXCEPTION
        'registry: % is published and immutable; % rejected (publish a new version instead)',
        TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'HR001',
              HINT = 'A published version_id addresses fixed content. Register the changed entry to get a new version_id.';
END;
$fn$;

COMMENT ON FUNCTION registry_reject_mutation() IS
    'FR6: rejects UPDATE/DELETE on any published registry row. Immutability is what lets a Variant '
    'Spec pinned months ago still resolve to the same values and generate the same diff (FR10).';

-- BEFORE INSERT: the indexed columns must agree with the hashed envelope they are projected from.
-- Guard 1 (the CHECK) proves version_id addresses the envelope; this proves the columns do too, so
-- there is exactly one truth source (the envelope) and no way to index it wrongly.
CREATE OR REPLACE FUNCTION registry_verify_envelope() RETURNS trigger
    LANGUAGE plpgsql AS $fn$
DECLARE
    env       jsonb;
    want_kind text := TG_ARGV[0];
BEGIN
    -- convert_from is only STABLE, which is fine in a trigger (volatility is restricted in CHECKs
    -- and index expressions, not here) — this is why the coherence guards are triggers, not CHECKs.
    BEGIN
        env := convert_from(NEW.envelope, 'UTF8')::jsonb;
    EXCEPTION WHEN others THEN
        RAISE EXCEPTION 'registry: %.envelope is not valid UTF-8 JSON', TG_TABLE_NAME
            USING ERRCODE = 'HR002';
    END;

    IF env ->> 'kind' IS DISTINCT FROM want_kind THEN
        RAISE EXCEPTION 'registry: %.envelope kind is % but this registry holds %',
            TG_TABLE_NAME, coalesce(env ->> 'kind', '<null>'), want_kind
            USING ERRCODE = 'HR002';
    END IF;

    IF env ->> 'name' IS DISTINCT FROM NEW.name THEN
        RAISE EXCEPTION 'registry: %.name is % but the hashed envelope names %',
            TG_TABLE_NAME, NEW.name, coalesce(env ->> 'name', '<null>')
            USING ERRCODE = 'HR002';
    END IF;

    -- prompt_entry projects one more column out of the envelope: the body blob it references. The
    -- FK proves the blob exists; this proves the FK guards the blob the CONTENT actually names.
    --
    -- The table test MUST be its own outer IF, not `TG_TABLE_NAME = 'prompt_entry' AND NEW.body_blob_hash …`.
    -- plpgsql evaluates a condition as ONE SQL expression, so a NEW.<column> reference in it is
    -- resolved whether or not the left side is true — on the other three tables that is
    -- `42703: record "new" has no field "body_blob_hash"`, i.e. every model/skill/context INSERT
    -- fails. Nesting keeps the reference inside a statement that only executes for prompt_entry.
    IF TG_TABLE_NAME = 'prompt_entry' THEN
        IF env #>> '{spec,body_blob_hash}' IS DISTINCT FROM NEW.body_blob_hash THEN
            RAISE EXCEPTION 'registry: prompt_entry.body_blob_hash is % but the hashed envelope names %',
                NEW.body_blob_hash, coalesce(env #>> '{spec,body_blob_hash}', '<null>')
                USING ERRCODE = 'HR002';
        END IF;
    END IF;

    RETURN NEW;
END;
$fn$;

COMMENT ON FUNCTION registry_verify_envelope() IS
    'Rejects an INSERT whose denormalized columns disagree with the content-addressed envelope. '
    'TG_ARGV[0] is the kind this registry holds.';

-- ─────────────────────────────────────────────────────────────────────────────
-- Model registry (FR9) — provider + model ID + inference params as ONE versioned unit.
-- spec: {"provider":"anthropic","model_id":"claude-opus-4-8",
--        "params":{"temperature":0,"max_tokens":1024,"thinking_budget":0,"seed":7}}
-- Pinning the params INSIDE the version is the point: a model_ref resolves provider, id, and every
-- inference param exactly as stored, and changing any one of them requires a new version_id.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS model_entry (
    version_id   TEXT         PRIMARY KEY
                              CHECK (version_id ~ '^[0-9a-f]{64}$'),   -- full lowercase SHA-256 hex
    name         TEXT         NOT NULL CHECK (name <> ''),
    envelope     BYTEA        NOT NULL,   -- exact canonical-JSON bytes; see header
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),  -- first publish; NOT hashed (see below)

    CONSTRAINT model_entry_content_addressed
        CHECK (version_id = encode(sha256(envelope), 'hex')),
    -- Task 1.1's literal requirement. Redundant against the PK for uniqueness, kept because it is
    -- also the (name, ...) index the "which versions exist for this name?" read path needs.
    CONSTRAINT model_entry_name_version_unique UNIQUE (name, version_id)
);

-- created_at is deliberately OUTSIDE the hash: hashing a timestamp would give the same content two
-- version_ids and break dedup. It is stable regardless, because UPDATE is rejected — so a re-publish
-- of identical content is a no-op that keeps the FIRST publish time (ON CONFLICT DO NOTHING).
COMMENT ON COLUMN model_entry.created_at IS 'First publish time. Not part of version_id.';

-- ─────────────────────────────────────────────────────────────────────────────
-- Prompt registry (FR7) — template with named variable slots.
-- spec: {"body_blob_hash":"<sha256 of template bytes>","slots":["query","tone"]}
-- The BODY IS NOT IN THE ROW (PRD §7 security / §8 storage): prompts may carry PII, so the bytes
-- live in the object store under their content hash and the DB holds only the reference. The slot
-- list is hashed so a template's declared interface is pinned by its version, not re-derived.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS prompt_entry (
    version_id     TEXT         PRIMARY KEY
                                CHECK (version_id ~ '^[0-9a-f]{64}$'),
    name           TEXT         NOT NULL CHECK (name <> ''),
    envelope       BYTEA        NOT NULL,
    -- FK => a prompt version cannot reference a body that was never catalogued. Without it a
    -- "resolvable" prompt_ref could still have no renderable template behind it.
    body_blob_hash TEXT         NOT NULL REFERENCES blob(content_hash),
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT prompt_entry_content_addressed
        CHECK (version_id = encode(sha256(envelope), 'hex')),
    CONSTRAINT prompt_entry_name_version_unique UNIQUE (name, version_id)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- Skill registry (FR8) — name -> JSON-schema input/output contract + impl handle.
-- spec: {"impl_handle":"builtin:search","input_schema":{...},"output_schema":{...}}
-- Hashing the schemas into the version means a skill_ref pins the CONTRACT, not just the name: a
-- contract change is a new version, so it cannot silently invalidate a Variant Spec already pinned.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS skill_entry (
    version_id   TEXT         PRIMARY KEY
                              CHECK (version_id ~ '^[0-9a-f]{64}$'),
    name         TEXT         NOT NULL CHECK (name <> ''),
    envelope     BYTEA        NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT skill_entry_content_addressed
        CHECK (version_id = encode(sha256(envelope), 'hex')),
    CONSTRAINT skill_entry_name_version_unique UNIQUE (name, version_id)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- Context registry (FR-P2 §3 non-goal boundary) — named policy + params.
-- spec: {"policy":"full","params":{}}
-- P2 implements only the `full` policy (PRD §3: sliding-window / summarization / RAG are P3), but
-- the ENTRY SHAPE is already policy-generic, so a P3 policy is a new row, not a schema change. The
-- set of legal `policy` values is owned by internal/registry's policy registry, not a CHECK here —
-- a DB-level enum would make every P3 policy a migration.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS context_entry (
    version_id   TEXT         PRIMARY KEY
                              CHECK (version_id ~ '^[0-9a-f]{64}$'),
    name         TEXT         NOT NULL CHECK (name <> ''),
    envelope     BYTEA        NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT context_entry_content_addressed
        CHECK (version_id = encode(sha256(envelope), 'hex')),
    CONSTRAINT context_entry_name_version_unique UNIQUE (name, version_id)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- Attach the guards. DROP IF EXISTS + CREATE (not CREATE OR REPLACE TRIGGER, which is PG14+) keeps
-- the migration re-runnable AND re-points an existing trigger at the current function definition —
-- an IF NOT EXISTS-style name check would skip a redefinition silently.
-- ─────────────────────────────────────────────────────────────────────────────
DROP TRIGGER IF EXISTS model_entry_coherent ON model_entry;
CREATE TRIGGER model_entry_coherent BEFORE INSERT ON model_entry
    FOR EACH ROW EXECUTE FUNCTION registry_verify_envelope('model');
DROP TRIGGER IF EXISTS model_entry_immutable ON model_entry;
CREATE TRIGGER model_entry_immutable BEFORE UPDATE OR DELETE ON model_entry
    FOR EACH ROW EXECUTE FUNCTION registry_reject_mutation();
DROP TRIGGER IF EXISTS model_entry_no_truncate ON model_entry;
CREATE TRIGGER model_entry_no_truncate BEFORE TRUNCATE ON model_entry
    FOR EACH STATEMENT EXECUTE FUNCTION registry_reject_mutation();

DROP TRIGGER IF EXISTS prompt_entry_coherent ON prompt_entry;
CREATE TRIGGER prompt_entry_coherent BEFORE INSERT ON prompt_entry
    FOR EACH ROW EXECUTE FUNCTION registry_verify_envelope('prompt');
DROP TRIGGER IF EXISTS prompt_entry_immutable ON prompt_entry;
CREATE TRIGGER prompt_entry_immutable BEFORE UPDATE OR DELETE ON prompt_entry
    FOR EACH ROW EXECUTE FUNCTION registry_reject_mutation();
DROP TRIGGER IF EXISTS prompt_entry_no_truncate ON prompt_entry;
CREATE TRIGGER prompt_entry_no_truncate BEFORE TRUNCATE ON prompt_entry
    FOR EACH STATEMENT EXECUTE FUNCTION registry_reject_mutation();

DROP TRIGGER IF EXISTS skill_entry_coherent ON skill_entry;
CREATE TRIGGER skill_entry_coherent BEFORE INSERT ON skill_entry
    FOR EACH ROW EXECUTE FUNCTION registry_verify_envelope('skill');
DROP TRIGGER IF EXISTS skill_entry_immutable ON skill_entry;
CREATE TRIGGER skill_entry_immutable BEFORE UPDATE OR DELETE ON skill_entry
    FOR EACH ROW EXECUTE FUNCTION registry_reject_mutation();
DROP TRIGGER IF EXISTS skill_entry_no_truncate ON skill_entry;
CREATE TRIGGER skill_entry_no_truncate BEFORE TRUNCATE ON skill_entry
    FOR EACH STATEMENT EXECUTE FUNCTION registry_reject_mutation();

DROP TRIGGER IF EXISTS context_entry_coherent ON context_entry;
CREATE TRIGGER context_entry_coherent BEFORE INSERT ON context_entry
    FOR EACH ROW EXECUTE FUNCTION registry_verify_envelope('context');
DROP TRIGGER IF EXISTS context_entry_immutable ON context_entry;
CREATE TRIGGER context_entry_immutable BEFORE UPDATE OR DELETE ON context_entry
    FOR EACH ROW EXECUTE FUNCTION registry_reject_mutation();
DROP TRIGGER IF EXISTS context_entry_no_truncate ON context_entry;
CREATE TRIGGER context_entry_no_truncate BEFORE TRUNCATE ON context_entry
    FOR EACH STATEMENT EXECUTE FUNCTION registry_reject_mutation();

INSERT INTO schema_migrations (id, name) VALUES (2, 'p2_registries')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
