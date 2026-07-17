-- P2 Configuration Layer — the Variant Spec store. Task 2.1.
-- Spec: openspec/changes/p2-config-runtime/specs/config-layer/spec.md; PRD §6 (FR3, FR4) and §8.
--
-- Dialect: PostgreSQL 11+. Expand-only: ADDS one table. Alters nothing 0001 or 0002 created.
-- Depends on 0001 (FK -> config.config_hash).
--
-- ─────────────────────────────────────────────────────────────────────────────
-- Why this table holds the AUTHORED spec and NOT the resolved config
-- ─────────────────────────────────────────────────────────────────────────────
-- 0001 already models the resolved side: `config` is keyed by config_hash and its `lineage_json`
-- holds "the resolved_config that was hashed ... so a run replays from lineage alone". Storing
-- resolved_config here too would be a second copy of the same truth, free to drift from the one P2.5
-- and P4 read. So:
--
--   config       (0001)  config_hash -> the RESOLVED configuration.  The lineage. What ran.
--   variant_spec (here)  the AUTHORED delta: which registry version_ids a human/P5.5 pinned per
--                        node, the ordering, and the source_revision it targets. What was ASKED FOR.
--
-- Both are needed and neither derives the other: resolution is lossy in one direction (a resolved
-- node cannot say whether its model came from an override or from the discovered default) and the
-- spec is incomplete in the other (it is a delta; only the IR at source_revision completes it).
-- The FK makes the pairing structural — a spec cannot exist without the lineage it hashed to.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- Why the key is (config_hash, source_revision) and not config_hash alone
-- ─────────────────────────────────────────────────────────────────────────────
-- Task 2.1 says "unique on config_hash". Taken literally that is not expressible here, and the
-- reason is worth stating rather than quietly picking one:
--
-- source_revision is deliberately NOT part of config_hash (P0's include set omits it; PRD §7/FR16
-- treat reproducibility as the three axes {config_hash, source_revision, seed}). So ONE config_hash
-- can legitimately arise at TWO revisions — whenever a commit changes nothing at any rewritten call
-- site, the resolved config is identical and hashes identically. Keying on config_hash alone would
-- make the second revision's spec collide with the first and silently keep the first's
-- source_revision, which is the one field the transform is keyed by. The generated diff would then
-- be attributed to the wrong commit.
--
-- (config_hash, source_revision) is therefore the honest identity, and it is the same pair the
-- `transform` table is unique on (PRD §8) — which is what task 2.3's "same config_hash + same
-- source_revision -> byte-identical diff" is actually about. config_hash remains unique in `config`,
-- where it is the identity of a configuration; here it is half of the identity of a targeting.

BEGIN;

CREATE TABLE IF NOT EXISTS variant_spec (
    config_hash     TEXT         NOT NULL REFERENCES config(config_hash),
    source_revision TEXT         NOT NULL CHECK (source_revision <> ''),

    -- The authored VariantSpec: {node_id -> {model_ref, prompt_ref, skill_refs[], context_policy}}
    -- + order + edges. JSONB, not BYTEA: unlike a registry envelope this is not hashed, so there are
    -- no canonical bytes to preserve, and being queryable ("which specs pin model version X?") is
    -- worth more than byte-exactness. The hashed bytes live in config.lineage_json's resolved form.
    spec_json       JSONB        NOT NULL,

    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT variant_spec_pkey PRIMARY KEY (config_hash, source_revision)
);

-- Read path: "what was authored against this commit?" — the UI's spec list and P4's fan-out over the
-- variants of one base both scan by revision.
CREATE INDEX IF NOT EXISTS idx_variant_spec_source_revision ON variant_spec (source_revision);

-- A published spec is as immutable as the config_hash that identifies it: the hash addresses the
-- resolved content, so an "edit" is a different hash and therefore a different row. Reusing 0002's
-- guard rather than writing a second one keeps one definition of what immutable means.
DROP TRIGGER IF EXISTS variant_spec_immutable ON variant_spec;
CREATE TRIGGER variant_spec_immutable BEFORE UPDATE OR DELETE ON variant_spec
    FOR EACH ROW EXECUTE FUNCTION registry_reject_mutation();
DROP TRIGGER IF EXISTS variant_spec_no_truncate ON variant_spec;
CREATE TRIGGER variant_spec_no_truncate BEFORE TRUNCATE ON variant_spec
    FOR EACH STATEMENT EXECUTE FUNCTION registry_reject_mutation();

INSERT INTO schema_migrations (id, name) VALUES (3, 'p2_variant_spec')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
