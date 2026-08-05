-- P27: a console session and a scoped upstream token are two things in one table.
--
-- # Why they share a table and must not share a meaning
--
-- 0038 created `console_session` for both, and they genuinely are the same shape: an opaque token, an
-- organization, a person, an expiry, a revocation. They are NOT the same credential:
--
--   * A CONSOLE session is what the browser holds, for eight hours, in an HttpOnly cookie. It proves
--     which browser this is to the console, and nothing more.
--   * An UPSTREAM token is what the console's server side holds, for ten minutes, and presents to the
--     platform API on the browser's behalf. It is a credential.
--
-- 🔴 Without this column they are interchangeable, and that is a real regression rather than an
-- untidiness: a stolen console cookie would authenticate directly against the platform API. Today a
-- stolen cookie only reaches the console, which holds the platform credential and exposes a closed set
-- of routes. Collapsing the two would hand every cookie the whole API surface.
--
-- So `auth` resolves ONLY `upstream` rows as credentials, and a `console` row presented as an API
-- credential is refused exactly as an unknown one is.
--
-- # Why the default is `upstream` and not `console`
--
-- Expand-only: every row 0038 created was minted by the token exchange, which is the upstream path.
-- Defaulting to `console` would silently reclassify them as browser sessions — and, because `auth`
-- refuses those, would log every existing scoped token out at once on upgrade.
--
-- Dialect: PostgreSQL. EXPAND-ONLY: one column with a default, one index. Nothing dropped, nothing
-- rewritten, and a second apply is a no-op.

BEGIN;

ALTER TABLE console_session
    ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'upstream';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class     t ON t.oid = c.conrelid
          JOIN pg_namespace n ON n.oid = t.relnamespace
         WHERE c.conname = 'console_session_purpose_known'
           AND t.relname = 'console_session'
           AND n.nspname = current_schema()
    ) THEN
        -- A closed vocabulary, for the reason every other status CHECK in this schema is closed: an
        -- invented value would be read by `auth`'s switch as "something else", and the safe reading of
        -- "something else" for a credential is not obvious enough to leave to a reader.
        ALTER TABLE console_session ADD CONSTRAINT console_session_purpose_known
            CHECK (purpose IN ('console', 'upstream'));
    END IF;
END $$;

-- Resolution is by token hash and always filters on purpose, so the index carries both. Without the
-- purpose in the index every credential lookup would read a row it is about to reject.
CREATE INDEX IF NOT EXISTS idx_console_session_purpose ON console_session (purpose, expires_at);

INSERT INTO schema_migrations (id, name) VALUES (39, 'p27_console_session_purpose')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
