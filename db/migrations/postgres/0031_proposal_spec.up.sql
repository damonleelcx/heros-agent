-- The candidate Variant Spec, without which a stored proposal cannot be compiled.
--
-- # What is missing
--
-- P5.5 design Decision 1 opens with "a proposal IS a candidate Variant Spec". `proposal` stores its
-- id, its scope, its operator, its node, its status and its hashes — and not the spec. So a row
-- describes a change without recording WHAT the change is: which registry entry the node would bind
-- instead of the one it binds today.
--
-- Nothing noticed for the same reason 0025 and 0030 went unnoticed on this table: the demo held the
-- candidate in memory and compiled it in the same function that emitted it, so the spec was a field on
-- a value rather than something anybody had to persist. It becomes load-bearing the moment the two
-- steps are separated in time, which is what a platform that proposes now and compiles later does.
--
-- 🔴 Without it the codemod has nothing to apply. `transform.Generate` rewrites a call site to the
-- RESOLVED spec; a proposal that cannot produce one can only be re-derived by re-running the generator
-- against whatever the inputs happen to be now — which would silently mint a different change under an
-- id a customer may already be verifying.
--
-- # Why a blob hash rather than a JSONB column
--
-- This table's discipline is content hashes, and the bytes live in the object store: `source_diff_blob_hash`
-- and `grounding_blob_hash` both carry the same `NULL OR 64-hex` CHECK, so an accidental inline payload
-- is refused by the DATABASE rather than by review. A spec is small enough that a JSONB column would
-- work and would be queryable — and would make this the one column on the table with a different rule,
-- which is how a table stops having a rule. Nothing queries into a spec; it is fetched whole to compile.
--
-- Dialect: PostgreSQL.

BEGIN;

ALTER TABLE proposal ADD COLUMN IF NOT EXISTS spec_blob_hash TEXT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class     t ON t.oid = c.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE c.conname = 'proposal_spec_blob_hash_is_a_hash'
           AND t.relname = 'proposal'
           AND n.nspname = current_schema()
    ) THEN
        ALTER TABLE proposal ADD CONSTRAINT proposal_spec_blob_hash_is_a_hash
            CHECK (spec_blob_hash IS NULL OR spec_blob_hash ~ '^[0-9a-f]{64}$');
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (31, 'proposal_spec')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
