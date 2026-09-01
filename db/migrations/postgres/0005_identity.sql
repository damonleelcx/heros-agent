-- 0005_identity — tenants, people, and the credentials they act with.
--
-- # Why sessions store a HASH and not the token
--
-- A session token in the database is a credential at rest: anyone who can read the table — a backup, a
-- replica, a support query, a leaked dump — can act as every logged-in user. Storing only its hash means
-- a stolen dump yields nothing usable, exactly as with passwords. The token exists in one place, the
-- customer's cookie, and nowhere else.
--
-- # Why the tenant is on the session and not only on the user
--
-- Authorisation reads the SESSION. Putting the tenant there means a request's scope is decided by the
-- credential presented, not by a second lookup that some path could skip — and a user later moved
-- between tenants cannot carry an old session's authority with them.

CREATE TABLE IF NOT EXISTS tenants (
    id         TEXT PRIMARY KEY,
    name       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT tenants_id_not_empty CHECK (id <> '')
);

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    tenant        TEXT        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email         TEXT        NOT NULL,
    -- password_hash is an argon2id encoded string carrying its own salt and parameters, so the cost can
    -- be raised later without invalidating existing passwords.
    password_hash TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    CONSTRAINT users_have_a_password CHECK (password_hash <> '')
);

-- 🔴 Email is unique per TENANT, not globally. Two organisations may legitimately contain the same
-- person, and a global unique constraint would let one tenant's signup deny another's.
CREATE UNIQUE INDEX IF NOT EXISTS users_email_per_tenant ON users (tenant, lower(email));

CREATE TABLE IF NOT EXISTS sessions (
    -- id is the token's SHA-256, hex encoded. The token itself is never stored.
    id         TEXT PRIMARY KEY,
    tenant     TEXT        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id    TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    -- 🔴 Every session expires. A credential with no expiry is one that outlives the reason it was
    -- issued, and the only way to end it is to notice it exists.
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT sessions_expire CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS sessions_by_user ON sessions (user_id);
