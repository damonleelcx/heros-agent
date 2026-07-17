-- P2 Runtime — the transform record. Task 3.9 (transform half).
-- Spec: openspec/changes/p2-config-runtime/specs/config-layer/spec.md; PRD §8 (storage), §6 (FR18).
--
-- Dialect: PostgreSQL 11+. Expand-only: ADDS one table. Depends on 0003 (FK -> variant_spec).
--
-- ─────────────────────────────────────────────────────────────────────────────
-- What this row is, and why (config_hash, source_revision) is its identity
-- ─────────────────────────────────────────────────────────────────────────────
-- PRD §8: "a unique (config_hash, source_revision) makes the generated diff a durable, deduplicated
-- artifact." That is this table's whole job. The pair is the identity because the transform is a pure
-- function of exactly those two things (task 2.3: "same config_hash + same source_revision ->
-- byte-identical diff") — so a second generation of the same pair MUST produce the same diff, and
-- storing it twice would be storing the same bytes under two rows.
--
-- It also FKs variant_spec on the same pair, which is what makes "a transform of a configuration
-- nobody authored" unrepresentable.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- Why the diff is a blob reference and not a column
-- ─────────────────────────────────────────────────────────────────────────────
-- PRD §7: generated diffs are content-hashed in the object store and referenced by hash. A diff is
-- the user's source code — it can carry secrets, PII, and proprietary logic, and it is exactly the
-- kind of payload that must not be inlined into a row that gets logged, replicated, and dumped. The
-- FK to blob(content_hash) (from 0001) makes the reference structural: a transform cannot cite a diff
-- that was never catalogued.

BEGIN;

CREATE TABLE IF NOT EXISTS transform (
    config_hash     TEXT         NOT NULL,
    source_revision TEXT         NOT NULL CHECK (source_revision <> ''),

    -- The generated patch, content-addressed. NULLABLE on purpose: a build-rejected transform still
    -- gets a row (the UI renders build-rejected as its own first-class state), and a spec with no
    -- overrides legitimately produces an empty diff with nothing to store.
    diff_blob_hash  TEXT         REFERENCES blob(content_hash),

    -- The build gate's verdict (FR5b). A transform that is not 'built' is never proposed and never
    -- run — the CHECK keeps the vocabulary closed so a typo cannot invent a third state that the
    -- executor's switch silently treats as runnable.
    build_status    TEXT         NOT NULL CHECK (build_status IN ('built', 'build-rejected')),

    -- The isolated worktree the codemod was applied in, and the single commit rollback reverts
    -- (task 3.10). worktree_ref is a cache location, not a durable artifact: the build cache is
    -- evictable (PRD §7), so this can point at a directory that no longer exists. The commit is the
    -- durable one — it lives in the mirror's object store.
    worktree_ref    TEXT,
    variant_branch  TEXT,
    variant_commit  TEXT,

    -- The compiler's reason, when it rejected. Kept in the row rather than a blob: it is short, it is
    -- not user source, and a rejection nobody can read is a dead end for whoever has to fix the spec.
    build_log       TEXT         NOT NULL DEFAULT '',
    -- Which rewrite the compiler blamed, when it pointed at a line we rewrote (FR5b). Nullable
    -- because attribution is only recorded when it is certain — see internal/worktree.attribute.
    rejected_node_id TEXT,
    rejected_dim     TEXT,

    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT transform_pkey PRIMARY KEY (config_hash, source_revision),
    CONSTRAINT transform_variant_spec_fk
        FOREIGN KEY (config_hash, source_revision) REFERENCES variant_spec (config_hash, source_revision),
    -- A rejection must say why, and a success must not pretend it was rejected. Without this a
    -- build-rejected row with an empty log is writable, and that row is useless to the one person who
    -- needs it.
    CONSTRAINT transform_rejection_has_a_reason
        CHECK (build_status <> 'build-rejected' OR build_log <> '')
);

-- Read path: "what has been transformed against this commit?" — P4's fan-out and the UI's list.
CREATE INDEX IF NOT EXISTS idx_transform_source_revision ON transform (source_revision);

-- A transform is as immutable as the (config_hash, source_revision) that identifies it: the diff is a
-- pure function of the pair, so a "changed" transform is a different pair and therefore a different
-- row. Reusing 0002's guard rather than defining a second one keeps one definition of immutable.
--
-- Note this makes a re-run of a rejected transform a no-op rather than an update — which is correct:
-- the same pair cannot build differently unless the toolchain changed, and a toolchain change that
-- silently flips a stored verdict is precisely what a pinned toolchain (PRD §7) exists to prevent.
DROP TRIGGER IF EXISTS transform_immutable ON transform;
CREATE TRIGGER transform_immutable BEFORE UPDATE OR DELETE ON transform
    FOR EACH ROW EXECUTE FUNCTION registry_reject_mutation();
DROP TRIGGER IF EXISTS transform_no_truncate ON transform;
CREATE TRIGGER transform_no_truncate BEFORE TRUNCATE ON transform
    FOR EACH STATEMENT EXECUTE FUNCTION registry_reject_mutation();

INSERT INTO schema_migrations (id, name) VALUES (4, 'p2_transform')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
