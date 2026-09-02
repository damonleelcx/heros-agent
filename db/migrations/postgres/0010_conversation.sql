-- 0010_conversation — the transcript, so a conversation can refer to itself.
--
-- # The gap this closes
--
-- `POST /api/ask` took `{text}` and nothing else. There was no turn history anywhere: not on the
-- server, not in the browser beyond the current page. The console LOOKED like a conversation and was
-- structurally a sequence of unrelated single-shot requests, so "and what about tools?" could not
-- work — there was no "and" for it to attach to.
--
-- The frontend already admitted half of this out loud (web/static/app/index.html, "rebuilding the
-- thread"): a refresh replayed durable GOALS and said plainly that free answers were not written down.
-- That honesty was correct and the omission was the bug. This table is the missing half.
--
-- # Why a fifth memory table rather than a `class` column on an existing one
--
-- 0002_memory argued for four tables over one because the classes differ in lifetime, in who may
-- write, and in what a row must carry to be trustworthy. A turn differs again on all three: it lives
-- for as long as the person keeps the tab, it is written by the platform on behalf of one human
-- utterance, and what makes it trustworthy is knowing WHO said it and HOW the reply was decided.
-- Folding it into `episodes` would also have required a goal id, and most turns never become a goal.
--
-- # 🔴 `decided_by` exists because the reply path can now degrade
--
-- Understanding moved to a model call, and a model call can fail — rate limit, timeout, bad JSON. The
-- product requirement is that the console keeps working by falling back to the deterministic keyword
-- router, which means two turns that look identical in the transcript may have been produced by two
-- completely different mechanisms. A support question ("why did it answer that?") is unanswerable
-- without this column, and an evaluation that cannot exclude degraded turns is measuring the wrong
-- population.
--
-- # 🔴 `cost_micro_cents` exists because conversation is no longer free
--
-- It used to be true that a Tier-B answer cost nothing, and both the code and the UI said so. It is
-- not true any more. Micro-cents, matching provider.MicroCentsPerCent: a turn costs a small fraction
-- of a cent, and a ledger denominated in cents would record every one of them as zero.
CREATE TABLE IF NOT EXISTS conversation_turns (
    tenant          TEXT        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    conversation_id TEXT        NOT NULL,
    -- seq orders turns within one conversation. Assigned by the store under a lock, never by the
    -- caller: two tabs posting at once would otherwise choose the same number, and the order of a
    -- conversation is the one thing a transcript is for.
    seq             BIGINT      NOT NULL,
    role            TEXT        NOT NULL,
    body            TEXT        NOT NULL,

    -- kind mirrors the API's response shape ('say', 'answer', 'goal', 'confirm', 'refusal', …) so a
    -- replayed transcript renders as what the person originally saw, rather than as flat text that
    -- silently drops the cards.
    kind            TEXT        NOT NULL DEFAULT '',
    -- capability is the named intent this turn resolved to, empty when the agent simply talked. Kept
    -- as free text rather than a foreign key: the closed set lives in Go (internal/intent), and a
    -- CHECK constraint here would be a second copy of it that drifts.
    capability      TEXT        NOT NULL DEFAULT '',
    decided_by      TEXT        NOT NULL DEFAULT '',
    cost_micro_cents BIGINT     NOT NULL DEFAULT 0,
    at              TIMESTAMPTZ NOT NULL,

    PRIMARY KEY (tenant, conversation_id, seq),
    CONSTRAINT conversation_turns_role_known CHECK (role IN ('user','agent')),
    CONSTRAINT conversation_turns_have_a_conversation CHECK (conversation_id <> '')
);

-- Finding the conversation to resume after a refresh: newest turn for this tenant. The primary key
-- cannot serve this — it leads with conversation_id, which is the thing being looked up.
CREATE INDEX IF NOT EXISTS conversation_turns_recent
    ON conversation_turns (tenant, at DESC);
