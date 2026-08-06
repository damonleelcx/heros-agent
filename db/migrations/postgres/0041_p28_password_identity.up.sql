-- P28: a person can obtain their own credential.
--
-- # What was missing, in one sentence
--
-- P27 made the person a row, gave them a membership, a revocable credential and a durable session — and left
-- them with no way to hold anything. The production console runs the `configured` seam, whose whole mechanism
-- is a JSON map of `{assertion: tenant_id}` injected from a Kubernetes secret, so obtaining a way in means an
-- operator reading that secret out of the cluster and handing the string over. This migration adds the two
-- tables and the one column that let a person choose a password, prove their address, and replace a forgotten
-- password without a human.
--
-- # 🔴 `user_password.encoded` CHECKs the argon2id tag, and that CHECK is the point
--
-- There are two hash functions in this system and each is catastrophic in the other's place. `HashSecret` is
-- SHA-256 and is correct for the 256-bit values WE mint — credentials, session tokens, device codes, and the
-- identity tokens below — because there is no dictionary to slow down. Every clause of that reasoning inverts
-- for a password a person chose, where the only defence a stored hash offers is to make each guess expensive.
--
-- Storing a password through `HashSecret` would look like ordinary reuse in review, produce a working sign-in,
-- pass every behavioural test, and leave the whole table crackable at GPU speed. A comment cannot stop that.
-- Three things do, at three layers that fail independently: an AST fence in `internal/password`, a shape check
-- in `tenancy.validateUserPassword`, and this CHECK — which is the one that cannot be bypassed by a code path
-- that forgot.
--
-- # Why `identity_token` is a table and not two more session purposes
--
-- `console_session` is the closest existing shape (opaque hashed token, purpose, user, expiry, revocation) and
-- reusing it was the first design. It fails on the data: `console_session.tenant_id` is NOT NULL REFERENCES
-- tenant, and a PASSWORD RESET is scoped to a person, who may belong to two organizations or — mid-signup — to
-- none. Satisfying that column would mean writing an arbitrary organization onto a row where it means nothing.
--
-- It also fails on a security property that is easy to miss. `internal/auth` resolves a presented secret
-- against `console_session` on every authenticated request, and until this phase it refused the browser cookie
-- BY NAME (`purpose = 'console'`) and accepted everything else. Adding a reset purpose to that table would have
-- made a password-reset link a platform API credential, silently. The Go side is changed to an ALLOWLIST in
-- the same change; a separate table means the mistake is not merely fixed but unavailable.
--
-- # Consumption is a conditional UPDATE, which is why `consumed_at` is nullable and not a boolean
--
-- `UPDATE … SET consumed_at = $3 WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > $3` — zero
-- rows affected is the refusal. Two clicks on the same link race in the database and exactly one wins. The
-- read-then-write shape lets both pass the check before either writes, which for a reset link means one link
-- setting two passwords. `invitation.accepted_at` established this pattern in 0038 and this follows it.
--
-- # `platform_user.email_verified_at` is NULL for every existing row, honestly
--
-- Nobody ever verified them, because there was no mechanism. Back-filling `now()` would assert a proof that
-- was never obtained, on the column that gates spending money and sending mail to third parties. NULL is the
-- true answer, and every surface renders it as its own state.
--
-- Dialect: PostgreSQL. EXPAND-ONLY: two new tables, one nullable column. Nothing is dropped, nothing is
-- rewritten, and a second apply is a no-op.

BEGIN;

-- ── user_password ───────────────────────────────────────────────────────────────────────────────────
-- One row per person who has a password. A federated person has none, and that is not a missing record —
-- which is why this is its own table rather than nullable columns on `platform_user`: an absent row says
-- "authenticates another way" without a NULL that every reader has to interpret.
CREATE TABLE IF NOT EXISTS user_password (
    user_id           TEXT        PRIMARY KEY REFERENCES platform_user (user_id),
    -- 🔴 The argon2id encoding, parameters included: `$argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>`.
    -- The parameters live IN the value so that raising the cost is a deploy — a sign-in verifying against a
    -- stale parameter set re-hashes on the spot — rather than a migration nobody can run, since the
    -- plaintexts are gone.
    encoded           TEXT        NOT NULL CHECK (encoded LIKE '$argon2id$%'),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Lockout state. It lives in the database rather than in a process cache for two reasons: a lock a
    -- restart clears is not a lock, and a per-replica counter turns "ten attempts" into ten per replica.
    failed_attempts   INTEGER     NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    -- When the current run of failures began. A window rather than a decaying counter, so that "ten failures
    -- within fifteen minutes" is the sentence the code implements and the sentence the copy promises.
    window_started_at TIMESTAMPTZ,
    -- NULL means not locked. Never a zero time, which every comparison would read as "locked until 1970" and
    -- therefore as "not locked" by accident.
    locked_until      TIMESTAMPTZ
);

-- ── identity_token ──────────────────────────────────────────────────────────────────────────────────
-- Single-use, expiring, purpose-bound links: confirm an address, or replace a forgotten password.
--
-- 🔴 `token_hash` is the primary key and there is no plaintext column. The value is minted with crypto/rand,
-- shown once in one email, and hashed here — so this table, which pg_dump writes into backup buckets, holds
-- nothing that can be presented.
CREATE TABLE IF NOT EXISTS identity_token (
    token_hash  TEXT        PRIMARY KEY CHECK (token_hash <> ''),
    user_id     TEXT        NOT NULL REFERENCES platform_user (user_id),
    purpose     TEXT        NOT NULL CHECK (purpose IN ('verify_email', 'reset_password')),
    -- The address this token proves control of, stored rather than read from the user, so confirming a
    -- CHANGED address is expressible without the old one having to be overwritten first.
    email       TEXT        NOT NULL CHECK (email <> ''),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ
);

-- "What outstanding links does this person have" — read when a resend is requested and when a reset
-- completes. Partial: a spent token is never looked up this way, and the index stays small forever.
CREATE INDEX IF NOT EXISTS idx_identity_token_user ON identity_token (user_id, purpose)
    WHERE consumed_at IS NULL;

-- ── platform_user.email_verified_at ─────────────────────────────────────────────────────────────────
-- 🔴 An independent ALTER, not an inline column on a CREATE TABLE. `platform_user` was deployed by 0038, so
-- a runner that has already applied 0038 would skip a modified CREATE and the column would never reach an
-- existing database — green on a fresh one and 42703 on every deployed one. This family of mistake has been
-- made three times in the sibling product; the rule is that a column for a deployed table is always its own
-- ALTER.
ALTER TABLE platform_user ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMPTZ;

-- Resolving a person by address is how sign-in works on this seam, and `lower(email)` is what the application
-- matches on — the expression index is what makes that an index scan rather than a sequential one. Not
-- UNIQUE: `(issuer, subject)` already is, and two people at one address under two different issuers is a
-- legitimate state (a federated person and a password person are not the same person).
CREATE INDEX IF NOT EXISTS idx_platform_user_email ON platform_user (issuer, lower(email));

INSERT INTO schema_migrations (id, name) VALUES (41, 'p28_password_identity')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
