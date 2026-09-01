-- 0002_memory — the four memory classes, as four tables.
--
-- # Why four tables and not one with a `class` column
--
-- They differ in three ways that make a single table actively harmful: lifetime (one goal, versus
-- across goals, versus until changed), who may write (the system, versus a promotion citing evidence,
-- versus only a person), and what a row must carry to be trustworthy.
--
-- One table forces one shape and one write path onto all four. The concrete failure is knowledge: an
-- agent able to INSERT into a shared table launders its own speculation into fact, and the next goal
-- reads that fact as if somebody had established it. Nothing looks wrong — the sentence is well-formed
-- and confidently stated — and the error compounds across every future run for that tenant.
--
-- Separate tables let the constraints differ, and the constraints are the whole point.

-- ── episodic: what happened, within one goal ─────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS episodes (
    goal_id       TEXT        NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
    seq           BIGINT      NOT NULL,
    task_id       TEXT        NOT NULL DEFAULT '',
    kind          TEXT        NOT NULL,
    summary       TEXT        NOT NULL,
    detail        TEXT        NOT NULL DEFAULT '',
    at            TIMESTAMPTZ NOT NULL,
    -- summarised_by points at the summary now covering this episode. The episode is NOT deleted:
    -- compression must be auditable, and "what did the summary leave out" is unanswerable once the
    -- source is gone.
    summarised_by BIGINT,

    PRIMARY KEY (goal_id, seq),
    CONSTRAINT episodes_kind_known CHECK (kind IN ('observation','decision','failure','effect'))
);

CREATE INDEX IF NOT EXISTS episodes_by_goal ON episodes (goal_id, seq);

-- ── summaries: compressed runs of episodes ───────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS episode_summaries (
    goal_id  TEXT        NOT NULL REFERENCES goals(id) ON DELETE CASCADE,
    id       BIGSERIAL   NOT NULL,
    from_seq BIGINT      NOT NULL,
    to_seq   BIGINT      NOT NULL,
    content  TEXT        NOT NULL,
    dropped  INTEGER     NOT NULL DEFAULT 0,
    at       TIMESTAMPTZ NOT NULL,

    PRIMARY KEY (goal_id, id),
    -- A range that runs backwards is a coverage claim nobody can check.
    CONSTRAINT summaries_range_forward CHECK (to_seq >= from_seq)
);

-- ── knowledge: reusable across goals, PROMOTED never written ─────────────────────────────────────
CREATE TABLE IF NOT EXISTS knowledge (
    tenant           TEXT        NOT NULL,
    subject          TEXT        NOT NULL,
    key              TEXT        NOT NULL,
    value            TEXT        NOT NULL,
    -- 🔴 Evidence is REQUIRED by the schema, not merely by convention. A claim with no evidence is
    -- indistinguishable from a guess six months later, including to the person deciding whether to
    -- trust it — and the whole reason knowledge is a separate class is that it must be answerable for.
    evidence_goal_id TEXT        NOT NULL,
    evidence_seqs    JSONB       NOT NULL,
    at               TIMESTAMPTZ NOT NULL,
    -- superseded_by is set when a later promotion contradicts this one. The old claim is KEPT: knowing
    -- a belief changed, and on what evidence, is what lets somebody audit a decision made while it was
    -- still held.
    superseded_by    TEXT,

    PRIMARY KEY (tenant, subject, key, at),
    CONSTRAINT knowledge_has_evidence CHECK (evidence_goal_id <> '' AND jsonb_array_length(evidence_seqs) > 0)
);

CREATE INDEX IF NOT EXISTS knowledge_current ON knowledge (tenant, subject, at DESC)
    WHERE superseded_by IS NULL;

-- ── preferences: authored by a person, never inferred ────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS preferences (
    tenant      TEXT        NOT NULL,
    key         TEXT        NOT NULL,
    value       TEXT        NOT NULL,
    -- 🔴 A human author is required. The type in Go refuses system identities; the column refuses an
    -- empty one. An agent that infers "they seem to prefer aggressive refactors" and then acts on it has
    -- invented a mandate, and the schema is where that becomes impossible rather than discouraged.
    authored_by TEXT        NOT NULL,
    at          TIMESTAMPTZ NOT NULL,

    PRIMARY KEY (tenant, key),
    CONSTRAINT preferences_have_a_human_author CHECK (authored_by <> '')
);
