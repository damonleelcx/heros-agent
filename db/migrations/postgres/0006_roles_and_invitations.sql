-- 0006_roles_and_invitations — what a person may do, and the three ways a link stands in for a password.
--
-- # Why a role column and not a memberships table
--
-- A user already belongs to exactly one tenant (see 0005). Splitting the role into a join table would
-- model a person who belongs to several organizations, which this product does not have and would then
-- have to keep consistent with a users table that still carries a tenant. One column says the same thing
-- and cannot disagree with itself. If multi-org membership ever arrives it arrives as a real change, not
-- as a table that has been sitting there half-used.
--
-- # 🔴 Why invitations, password resets and email verifications are THREE tables
--
-- They are all "a random token that stands in for proof of identity", and folding them into one table
-- with a `purpose` column is the obvious economy. It is also the shape of a defect this project has
-- already shipped once: a lookup that forgets to filter on purpose turns the weakest token into the
-- strongest one. An email-verification link is sent liberally, to unproven addresses, and is worth
-- nothing; a password-reset link is a complete account takeover. Sharing storage between them means the
-- only thing keeping them apart is a WHERE clause in every query that reads either.
--
-- Separate tables make the confusion unrepresentable rather than merely tested against. The cost is two
-- extra CREATE TABLE statements. The cost of the other choice is paid once, by a customer.
--
-- # Why every token table stores a hash, under a SEPARATE opaque id
--
-- The hash, for the same reason sessions store one: these rows ARE credentials at rest, and anybody who
-- can read a backup, a replica or a support query could otherwise reset every password in the system.
-- The token exists in one place — the mail that was sent — and nowhere else.
--
-- The separate `id` because invitations are LISTED in the console, and a primary key that is the hash of
-- a live secret is a hash that ends up in a URL, a log line and somebody's screenshot. It confirms
-- nothing on its own, but it turns "guess the token" into "guess the token and check your work offline",
-- which is the difference between impossible and merely expensive. All three tables are shaped the same
-- way so no future listing has to notice the distinction.

ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'member';

-- email_verified_at records that somebody proved they receive mail at this address, by following a link
-- or by accepting an invitation sent to it. NULL means unproven, which is the honest state for an
-- address an operator typed in.
--
-- 🚫 It deliberately gates nothing. Mail is the least reliable component in this deployment, and making
-- a session depend on it converts a mail outage into a lockout — including for the one account that
-- could fix the mail. It is recorded and shown, and what it gates is a decision for the day there is a
-- reason, not a default chosen because the column exists.
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMPTZ;

-- DROP then ADD, because Postgres has no ADD CONSTRAINT IF NOT EXISTS and this migration runs on every
-- boot. Two statements rather than a DO block: the migration splitter is not a SQL parser and cannot see
-- dollar-quoted bodies, and a migration that needs one should say so loudly rather than work by luck.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_is_known;
ALTER TABLE users ADD CONSTRAINT users_role_is_known CHECK (role IN ('owner', 'admin', 'member', 'viewer'));

-- 🔴 Promote the founding account of each tenant to owner.
--
-- The column defaults to 'member', which is the right default for somebody arriving by invitation and
-- the wrong one for everybody who already exists: an install upgraded from 0005 would find its only
-- account demoted to member, and nobody left who could invite anyone or promote them back. The
-- organization would be locked out of its own administration by a schema change.
--
-- Conditional on there being no owner, so re-running this migration cannot re-promote somebody a real
-- owner has since demoted. That is what makes it idempotent rather than merely repeatable.
UPDATE users u SET role = 'owner'
WHERE NOT EXISTS (SELECT 1 FROM users o WHERE o.tenant = u.tenant AND o.role = 'owner')
  AND u.id = (SELECT x.id FROM users x WHERE x.tenant = u.tenant ORDER BY x.created_at, x.id LIMIT 1);

-- ── invitations ─────────────────────────────────────────────────────────────────────────────────────
--
-- An invitation is the ONLY way a person joins an organization. There is no self-serve signup: an
-- address cannot put itself inside a tenant, so the set of people who can read a customer's code is
-- exactly the set somebody inside that customer chose.
CREATE TABLE IF NOT EXISTS invitations (
    -- id is opaque and safe to show. token_hash is the SHA-256 of the emailed token, hex encoded; the
    -- token itself is never stored.
    id          TEXT PRIMARY KEY,
    token_hash  TEXT        NOT NULL UNIQUE,
    tenant      TEXT        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email       TEXT        NOT NULL,
    -- role is chosen by the inviter and fixed at invitation time. 🔴 The person accepting does not send
    -- it: a role in the accept request is a role the recipient can change, and the first thing anybody
    -- would try is 'owner'.
    role        TEXT        NOT NULL,
    invited_by  TEXT        REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    CONSTRAINT invitations_expire CHECK (expires_at > created_at),
    -- 🔴 An invitation cannot mint an owner, at the storage layer, whatever any handler believes.
    -- Ownership is transferred by an owner in the console, deliberately, to somebody already inside the
    -- organization — never by a link in an email, which is a credential that travels through a mailbox
    -- this organization does not control.
    CONSTRAINT invitations_cannot_mint_an_owner CHECK (role IN ('admin', 'member', 'viewer'))
);

-- 🔴 At most one live invitation per address per organization. Re-inviting somebody replaces the old
-- link rather than adding a second one, so a revoked or superseded invitation cannot still be accepted
-- by whoever kept the first mail. Partial, so the history of accepted invitations is kept.
CREATE UNIQUE INDEX IF NOT EXISTS invitations_one_live_per_email
    ON invitations (tenant, lower(email)) WHERE accepted_at IS NULL;

CREATE INDEX IF NOT EXISTS invitations_by_tenant ON invitations (tenant);

-- ── password resets ─────────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS password_resets (
    id         TEXT PRIMARY KEY,
    token_hash TEXT        NOT NULL UNIQUE,
    tenant     TEXT        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id    TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    -- 🔴 One hour, not a fortnight. A reset link sits in a mailbox, gets forwarded, gets synced to a
    -- phone that later gets sold. Its whole job takes about ninety seconds.
    expires_at TIMESTAMPTZ NOT NULL,
    -- used_at makes the token single-use. Claimed by a conditional UPDATE rather than a read followed by
    -- a write, so two clicks on the same link cannot both succeed.
    used_at    TIMESTAMPTZ,
    CONSTRAINT password_resets_expire CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS password_resets_by_user ON password_resets (user_id);

-- ── email verifications ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS email_verifications (
    id         TEXT PRIMARY KEY,
    token_hash TEXT        NOT NULL UNIQUE,
    tenant     TEXT        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id    TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- email is recorded as it was when the link was sent, so a link cannot verify an address the account
    -- has since been changed to.
    email      TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    CONSTRAINT email_verifications_expire CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS email_verifications_by_user ON email_verifications (user_id);
