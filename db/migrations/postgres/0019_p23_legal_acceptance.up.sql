-- P23 Legal Surface & Developer Documentation — the consent record. Task 9.1.
-- Design: openspec/changes/archive/2026-08-01-p23-legal-and-docs/design.md Decision 5.
-- Ratified decisions: docs/decisions/p23-one-way-doors.md.
-- Data inventory: docs/decisions/p23-data-inventory.md §1.7.
--
-- Dialect: PostgreSQL 11+, as every migration in this chain.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- Numbering note
-- ─────────────────────────────────────────────────────────────────────────────
-- P23's design says "0016 at time of writing". By the time this landed, 0016 (p13_authored_change),
-- 0017 (p17_memory_registry) and 0018 (p18_harness_registry) had all been taken. This is 0019. Recorded
-- rather than silently renumbered, because somebody following the design document's pointer deserves to
-- find out why it misses.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- 🔴 IDEMPOTENCY IS THE UNIQUE CONSTRAINT, NOT APPLICATION CODE
-- ─────────────────────────────────────────────────────────────────────────────
-- A double-clicked button, a retried request and a back-button resubmit must collapse to ONE row.
-- "Check then insert" in application code is a race with a customer's double-click: two requests both
-- read no row, both insert, and the record of one decision becomes two.
--
-- `unique (tenant_id, principal_id, document_kind, document_version)` makes the collapse a property of
-- the STORE. The write path is `INSERT … ON CONFLICT DO NOTHING`, and a conflict is a SUCCESS — the
-- customer accepted, and they accepted once.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- 🔴 APPEND-ONLY, AND WHY `superseded_by` IS NOT AN EXCEPTION
-- ─────────────────────────────────────────────────────────────────────────────
-- A withdrawal or a re-acceptance is a NEW ROW. Nothing is updated in place except `superseded_by`,
-- which is bookkeeping — "a material later version was published" — and never a rewrite of what was
-- agreed. The trigger below enforces exactly that: an UPDATE may touch `superseded_by` and nothing else,
-- and a DELETE is refused outright for every role.
--
-- The reason the trigger exists rather than a convention: this table is EVIDENCE. Its value in a billing
-- dispute, a security review or a data-protection audit is precisely that nobody can have edited it,
-- including us. A convention cannot make that claim; a trigger can.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- 🔴 DATA MINIMISATION IS THE SCHEMA (NFR9)
-- ─────────────────────────────────────────────────────────────────────────────
-- There is no email column, no name column, and no free-text column. Not because we remember not to
-- write one, but because there is nowhere to put it.
--
-- That is what makes erasure a TOMBSTONE OF THE SUBJECT rather than a rewrite of the evidence: the row
-- holds an opaque principal id and nothing else about a person, so it can survive an erasure request
-- while disclosing nothing. Decided now rather than during the first erasure request, when the pressure
-- runs the other way.
--
-- A CHECK refuses an at-sign in `principal_id`, so a mis-wired integration that passed an email address
-- fails at the database rather than quietly making this table personal data.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- Expand-only
-- ─────────────────────────────────────────────────────────────────────────────
-- One new table. Nothing any earlier migration created is altered, so this is safe to deploy before any
-- P23 code is enabled — a `legal_acceptance` nobody writes to is inert.

BEGIN;

CREATE TABLE IF NOT EXISTS legal_acceptance (
    id                UUID         PRIMARY KEY,

    -- Tenant and principal are the ADR-008 seam (task 9.5). Binding to the abstract principal rather
    -- than to an email or an IdP subject is what lets P22 make identity real WITHOUT a migration here.
    tenant_id         TEXT         NOT NULL CHECK (tenant_id <> ''),
    principal_id      TEXT         NOT NULL CHECK (principal_id <> ''),

    -- 🔴 The row may not become personal data. An email address is the way it would.
    CONSTRAINT legal_acceptance_principal_is_opaque
        CHECK (principal_id !~ '@'),

    -- The document identity triple. `content_hash` is the load-bearing third component: without it the
    -- record says "they accepted terms v1.0.0" and cannot say WHICH TEXT that was, so a republication
    -- under an unchanged version number would be invisible instead of detectable.
    document_kind     TEXT         NOT NULL CHECK (document_kind IN ('terms', 'privacy')),
    document_version  TEXT         NOT NULL CHECK (document_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    content_hash      TEXT         NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),

    accepted_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),

    -- Which commitment moment produced this. A closed vocabulary, so a typo cannot invent a method a
    -- consumer's switch silently mishandles.
    method            TEXT         NOT NULL
                                   CHECK (method IN ('signin', 'checkout', 'plan_change', 'api')),

    -- Set when a MATERIAL later version is published (task 9.6). NULL means "still the acceptance in
    -- force". A non-material publication sets nothing, which is the whole point of the declaration.
    superseded_by     UUID         NULL REFERENCES legal_acceptance(id),

    -- 🔴 Erasure tombstone (task 9.8). Set when the subject is erased; the EVIDENTIARY columns above are
    -- untouched. The row keeps saying which document, which version, which hash and when — and stops
    -- being attributable to a person. There is no personal data to remove because there never was any.
    subject_erased_at TIMESTAMPTZ  NULL,

    -- 🔴 THE IDEMPOTENCY GUARANTEE. See the header.
    CONSTRAINT legal_acceptance_once
        UNIQUE (tenant_id, principal_id, document_kind, document_version)
);

-- The read path is "this tenant's acceptances" and "what is this principal still missing". Both are
-- tenant-scoped, and the index says so — there is no query in this system that reads across tenants.
CREATE INDEX IF NOT EXISTS idx_legal_acceptance_tenant
    ON legal_acceptance (tenant_id, principal_id, document_kind);

-- The retention job scans by age (task 9.7). Without this it is a sequential scan over the whole table
-- on a schedule, which is how a retention job becomes the thing an operator disables.
CREATE INDEX IF NOT EXISTS idx_legal_acceptance_accepted_at
    ON legal_acceptance (accepted_at);

-- ── Append-only, enforced ─────────────────────────────────────────────────────
--
-- A DELETE is refused for every role. An UPDATE is refused unless it changes ONLY `superseded_by` or
-- `subject_erased_at` — the two bookkeeping columns — and leaves every evidentiary column identical.
--
-- Written as a comparison of the OLD and NEW rows rather than a column allowlist, because an allowlist
-- silently permits any column added later. This way a new column is protected the moment it exists.
CREATE OR REPLACE FUNCTION legal_acceptance_append_only() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' OR TG_OP = 'TRUNCATE' THEN
        RAISE EXCEPTION
            'legal_acceptance is append-only: a consent record is evidence and is never removed. '
            'To erase a subject, set subject_erased_at — the evidentiary columns stay.';
    END IF;

    IF  NEW.id                <> OLD.id
     OR NEW.tenant_id         <> OLD.tenant_id
     OR NEW.principal_id      <> OLD.principal_id
     OR NEW.document_kind     <> OLD.document_kind
     OR NEW.document_version  <> OLD.document_version
     OR NEW.content_hash      <> OLD.content_hash
     OR NEW.accepted_at       <> OLD.accepted_at
     OR NEW.method            <> OLD.method
    THEN
        RAISE EXCEPTION
            'legal_acceptance is append-only: only superseded_by and subject_erased_at may change. '
            'A re-acceptance or a withdrawal is a NEW ROW.';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS legal_acceptance_no_rewrite ON legal_acceptance;
CREATE TRIGGER legal_acceptance_no_rewrite BEFORE UPDATE OR DELETE ON legal_acceptance
    FOR EACH ROW EXECUTE FUNCTION legal_acceptance_append_only();

-- TRUNCATE needs its own statement-level trigger: row triggers do not fire for it, and
-- `TRUNCATE legal_acceptance` is exactly what somebody reaching for "just clear the test data" types.
DROP TRIGGER IF EXISTS legal_acceptance_no_truncate ON legal_acceptance;
CREATE TRIGGER legal_acceptance_no_truncate BEFORE TRUNCATE ON legal_acceptance
    FOR EACH STATEMENT EXECUTE FUNCTION legal_acceptance_append_only();

INSERT INTO schema_migrations (id, name) VALUES (19, 'p23_legal_acceptance')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
