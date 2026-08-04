-- The published-release record the operator console's Releases & trust page reads.
--
-- # Why a table, when the interface's own comment says "not a table"
--
-- `adminops.ReleaseSource` carries the note "It is an interface rather than a table (design D7 — no new
-- table in this phase)". D7 was a scoping rule for P26, not a claim that a release record should never
-- be durable — and the consequence of having no implementation at all is a page that tells an operator
-- "this deployment does not carry the release oversight surface", forever, on a platform that does
-- publish releases. The seam stays; this is one implementation of it.
--
-- # What it holds, and the one thing it must never hold
--
-- A version, a channel, when it was published, and WHICH SIGNING KEY was active — by identifier, and
-- the fingerprint of the artefact. 🔴 NO KEY MATERIAL. The console's own page says a key is shown
-- "by identifier and fingerprint only", and there is deliberately no column here that a private key
-- would fit in. This table is dumped by pg_dump and lives in backup buckets.
--
-- `verified` is the platform's own verification verdict, and it is NULLABLE on purpose: NULL means "not
-- checked", which is a different answer from FALSE ("checked and failed"). A page that renders those the
-- same way would show an unverified artefact as a failing one, and an operator would go looking for a
-- compromise that did not happen.
--
-- Dialect: PostgreSQL. EXPAND-ONLY: two new tables, nothing altered, nothing dropped, idempotent.

BEGIN;

CREATE TABLE IF NOT EXISTS release_record (
    version         TEXT        NOT NULL CHECK (version <> ''),
    channel         TEXT        NOT NULL CHECK (channel <> ''),
    published_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The signing key's IDENTIFIER. Never the key.
    signing_key_id  TEXT        NOT NULL DEFAULT '',
    PRIMARY KEY (version, channel)
);

CREATE TABLE IF NOT EXISTS release_artefact (
    version    TEXT NOT NULL CHECK (version <> ''),
    channel    TEXT NOT NULL CHECK (channel <> ''),
    platform   TEXT NOT NULL CHECK (platform <> ''),
    name       TEXT NOT NULL CHECK (name <> ''),
    -- The artefact's content digest. A public value by construction.
    digest     TEXT NOT NULL DEFAULT '',
    -- NULL = not checked; FALSE = checked and FAILED. Collapsing them would report an unverified
    -- artefact as a failing one. See the header.
    verified   BOOLEAN,
    published  BOOLEAN NOT NULL DEFAULT TRUE,
    PRIMARY KEY (version, channel, platform, name),
    FOREIGN KEY (version, channel) REFERENCES release_record(version, channel) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_release_artefact_version ON release_artefact(version, channel);

INSERT INTO schema_migrations (id, name) VALUES (37, 'release_record')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
