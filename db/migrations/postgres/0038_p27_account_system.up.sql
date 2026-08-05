-- P27: the tenant becomes a row, the person becomes real, and the run gets an owner.
--
-- # What was missing, in one sentence per table
--
-- A tenant is not a row anywhere in the thirty-seven migrations before this one. `auth.Registry` builds
-- a `map[string]Principal` from the configuration file at boot, so onboarding a customer is a deploy and
-- self-serve sign-up is impossible. Meanwhile `delivery.tenant_id`, `workflow_ir.tenant_id` and
-- `legal_acceptance.tenant_id` have been foreign keys into nothing for phases. This migration adds the
-- row those columns were always naming, plus the four records that were unrepresentable without it: a
-- person, their membership, an invitation, and a credential that can be revoked.
--
-- # 🔴 The table is `platform_user`, not `user`, and that is not a style choice
--
-- `USER` is a reserved word in PostgreSQL. `CREATE TABLE user` is a syntax error, and the fix people
-- reach for — quoting `"user"` at every call site — is a rule that holds until one query forgets the
-- quotes and fails at runtime instead of at review. The prefix follows `platform_workflow_graph`, which
-- is already in this schema. The COLUMN stays `user_id` everywhere, because that is what every other
-- table references and there is nothing reserved about it.
--
-- # Foreign keys inside the identity domain; none crossing out of it
--
-- `membership`, `invitation`, `api_credential` and `console_session` all carry real foreign keys to
-- `tenant` and `platform_user`. They are written by one service, and an orphan in any of them is a bug
-- with no legitimate reading, so the database is the cheapest place to make it impossible.
--
-- Deliberately NOT foreign keys:
--
--   * `account.customer_id` -> `tenant.tenant_id`. Same value, two bounded contexts. A constraint here
--     means an identity migration cannot run without a billing outage, and it makes the billing tables
--     undroppable from a deployment that does not bill. P7 already relates `usage_record` to `account`
--     the same way.
--
--   * `run.tenant_id`, `variant_spec.tenant_id`, `eval_run.tenant_id`. These are the DATA PLANE. A
--     foreign key would put an identity-table lookup in the run write path, which is the coupling the
--     design forbids, and it would make the NULL (pre-ownership) need a special case.
--
--   * Every pre-existing `tenant_id` column — `delivery`, `workflow_ir`, `legal_acceptance`, `run_link`,
--     `authored_change`, `source_bundle`, `platform_workflow_graph` and `proposal` — is left exactly as
--     it is. Retrofitting constraints onto eight subsystems for a property none of them currently
--     violates is scope creep, not tidying.
--
-- # 🔴 The operator domain is not touched, and must never join to these tables
--
-- `admin_principal` has no tenant column and no foreign key into any customer table (P8 FR1). An
-- operator is not a user, and no row here gives one a membership. A future migration that connects those
-- two halves is a review failure, not a modelling improvement.
--
-- # NULL owner means PRE-OWNERSHIP, never "unowned"
--
-- Rows created before this migration have no recoverable owner — the information was never written.
-- Inferring one from a neighbouring table produces a CONFIDENT WRONG owner, and a run is billed usage,
-- so a wrong owner is a money error that is unfalsifiable after the fact. NULL is the honest answer, and
-- every listing surface renders it as its own state rather than as "you have no runs".
--
-- The three ownership indexes are PARTIAL — `WHERE tenant_id IS NOT NULL`. Two reasons, and the second is the one
-- that matters here: every query filters `tenant_id = $1` and never `IS NULL`, so the partial index is
-- the correct index; and at creation time it indexes ZERO rows, so the build is instant on a deployed
-- table with millions of rows. `CREATE INDEX CONCURRENTLY` is not available to this runner — every
-- migration file is executed as one batch inside its own transaction (see internal/pgmigrate) and
-- CONCURRENTLY cannot run in a transaction block. The partial index makes that constraint costless
-- rather than worked around.
--
-- # `account`: a Free customer has no billing-provider customer yet
--
-- `provider_customer_handle` was NOT NULL and non-empty, which is correct for a BILLABLE account and
-- makes a free one inexpressible — so sign-up would have to either fail or register a customer object at a payment
-- provider for every person who ever tries the free tier and never returns. Neither is acceptable, so
-- the column becomes nullable and the original guarantee is preserved by stating the condition it
-- actually was: A HANDLE MAY BE ABSENT ONLY WHILE THE PLAN CHARGES NOTHING.
--
-- The database cannot read plan configuration, so `plan_charges` carries the answer, written by the same
-- statement that sets the plan. It defaults TRUE, so every existing row — all of which have handles —
-- stays valid. The card-data CHECK is UNTOUCHED: it still refuses any 12-19 digit run, and a NULL passes
-- it the way a CHECK always passes NULL, which is the correct reading of "there is nothing here to be a
-- card number".
--
-- ⚠️ A divergence found while proving this, recorded rather than fixed. The DATABASE check is a plain
-- shape test — `replace(...) !~ '^[0-9]{12,19}$'` — with no Luhn step, so it refuses EVERY 12-19 digit
-- handle. `account.NewHandle` in Go requires the digits AND a valid Luhn checksum, and its comment says
-- "a legitimate all-digit provider id is not rejected by accident". That sentence is false against this
-- schema: the database is stricter, and the database is the last line. P27 does not change 0013's
-- constraint — narrowing a CHECK on a deployed table is a one-way door with no P27 reason behind it —
-- but the Go comment overclaims and the proof below asserts the STRICTER, actual behaviour so nobody
-- discovers the difference from a customer's failed insert.
--
-- Dialect: PostgreSQL. EXPAND-ONLY: five new tables, three nullable columns, one column relaxed, one
-- column added with a default. Nothing is dropped, nothing is rewritten, and a second apply is a no-op.

BEGIN;

-- ── tenant ──────────────────────────────────────────────────────────────────────────────────────────
-- The row the configuration file has been standing in for. `status` deliberately shares the vocabulary
-- of `account.status` (see 0024's `account_status_known`): one lifecycle with two enums is a place for
-- two answers to disagree, and the one that matters — may this tenant run? — gets read from whichever
-- the caller happened to know about.
CREATE TABLE IF NOT EXISTS tenant (
    tenant_id  TEXT        PRIMARY KEY CHECK (tenant_id <> ''),
    -- What the customer calls their organization. The config file had nowhere to put this, which is why
    -- the operator console shows raw ids today; a support conversation starts with a name.
    name       TEXT        NOT NULL CHECK (name <> ''),
    status     TEXT        NOT NULL DEFAULT 'active'
                           CHECK (status IN ('active', 'suspended')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── platform_user ───────────────────────────────────────────────────────────────────────────────────
-- The person P22 made provable. ADR-008 recorded that the console session holds a tenant and not a user
-- "because the platform cannot currently prove one" — true when it was written, made false when P22
-- shipped a verified `(issuer, subject)`, and never revisited.
--
-- 🔴 `email` is a DISPLAY ATTRIBUTE, never the identity. An address is reassigned inside a company; a
-- subject is not. Keying on email means the new hire who inherits `sales@` inherits the previous
-- holder's account. The UNIQUE constraint is on the federated pair, and the primary key is internal so
-- that a customer changing identity provider does not rewrite every row that references a person.
CREATE TABLE IF NOT EXISTS platform_user (
    user_id    TEXT        PRIMARY KEY CHECK (user_id <> ''),
    issuer     TEXT        NOT NULL CHECK (issuer <> ''),
    subject    TEXT        NOT NULL CHECK (subject <> ''),
    email      TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT platform_user_federated_identity UNIQUE (issuer, subject)
);

-- ── membership ──────────────────────────────────────────────────────────────────────────────────────
-- A join table rather than a column on `platform_user`, because a contractor works for two customers and
-- a `tenant_id` column cannot express that. It is also what makes a seat a property of the ORGANIZATION
-- rather than of the person: one person in two organizations occupies two seats.
--
-- Removal is a STATE, not a delete. The audit chain has to keep resolving to a name, and a hard delete
-- orphans every attribution at exactly the moment somebody is asking who did something.
CREATE TABLE IF NOT EXISTS membership (
    user_id    TEXT        NOT NULL REFERENCES platform_user (user_id),
    tenant_id  TEXT        NOT NULL REFERENCES tenant (tenant_id),
    role       TEXT        NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    status     TEXT        NOT NULL DEFAULT 'active'
                           CHECK (status IN ('active', 'removed')),
    -- Which user issued the invitation. "How did this person get here" is the first question asked when
    -- an unexpected member appears.
    invited_by TEXT        NOT NULL DEFAULT '',
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    removed_at TIMESTAMPTZ,
    PRIMARY KEY (user_id, tenant_id)
);

-- "Who is in this organization" and "which organizations is this person in" are both hot: the first is
-- the members page and the seat count, the second runs on every sign-in.
CREATE INDEX IF NOT EXISTS idx_membership_tenant ON membership (tenant_id, status);

-- ── invitation ──────────────────────────────────────────────────────────────────────────────────────
-- 🔴 A pending membership that is NOT a membership. The link pre-fills the organization and the address;
-- it grants nothing. Membership is created only when a completed SSO sign-in yields a VERIFIED address
-- matching `email` — so forwarding the invitation to the wrong person creates nothing.
--
-- `accepted_at` makes single-use a property of the database rather than of application logic, and
-- `expires_at` exists because a standing offer sitting in an inbox is a way in that nobody is tracking.
CREATE TABLE IF NOT EXISTS invitation (
    invitation_id TEXT        PRIMARY KEY CHECK (invitation_id <> ''),
    tenant_id     TEXT        NOT NULL REFERENCES tenant (tenant_id),
    email         TEXT        NOT NULL CHECK (email <> ''),
    role          TEXT        NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    invited_by    TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    accepted_at   TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_invitation_tenant ON invitation (tenant_id, created_at DESC);

-- ── api_credential ──────────────────────────────────────────────────────────────────────────────────
-- The keys the configuration map holds today, made durable, hashed and revocable.
--
-- 🔴 `hash` only. The plaintext is returned exactly once, in the creation response, and is never
-- readable again from any surface, log, export or trace attribute. This table is dumped by pg_dump and
-- lives in backup buckets.
--
-- 🔴 `tenant_id` is the whole isolation fix. The tenant lives INSIDE the credential, so `auth` derives
-- scope from a value the platform verified rather than from a header the caller supplied. The header
-- (`X-Console-Tenant`) is deleted rather than made authoritative: trusting it would let any holder of
-- the console's one credential name any tenant, which is a request describing its own authority.
--
-- `user_id` NULL means MACHINE CREDENTIAL — a CI key, a service — not "unknown owner". That distinction
-- is what lets member removal revoke a person's keys without breaking a customer's build pipeline, and
-- it is why the removal preview can list, by name, what it is NOT revoking.
CREATE TABLE IF NOT EXISTS api_credential (
    credential_id TEXT        PRIMARY KEY CHECK (credential_id <> ''),
    tenant_id     TEXT        NOT NULL REFERENCES tenant (tenant_id),
    user_id       TEXT        NULL REFERENCES platform_user (user_id),
    -- What the human called it. A revocation screen listing eight opaque ids is a screen where the wrong
    -- key gets revoked.
    label         TEXT        NOT NULL DEFAULT '',
    role          TEXT        NOT NULL DEFAULT 'member',
    hash          TEXT        NOT NULL CHECK (hash <> ''),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at    TIMESTAMPTZ
);

-- Verification looks up by hash on every request; this is the index that decides whether that is
-- affordable. UNIQUE because two credentials hashing identically is a collision we want to hear about.
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_credential_hash ON api_credential (hash);
CREATE INDEX IF NOT EXISTS idx_api_credential_tenant ON api_credential (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_api_credential_user ON api_credential (user_id) WHERE user_id IS NOT NULL;

-- ── console_session ─────────────────────────────────────────────────────────────────────────────────
-- The map a restart empties. Console sessions live in a `globalThis`-anchored Map today, which is honest
-- for the one-container deployment ADR-006 describes and is a mass logout on every release — and P19's
-- Kubernetes overlay declares `replicas: 2`, under which a user signs in against one pod and is signed
-- out by the next request that lands on the other.
--
-- The shape mirrors `admin_session`, which has been durable on the operator side since P8. That asymmetry
-- — the operator's sessions survive a restart and the customer's do not — is the argument for this table.
--
-- 🔴 `token_hash` is the PRIMARY KEY and `session_id` is separate, exactly as the in-memory store already
-- separates them: the id is what appears in logs, the token is the bearer, and a log line naming a
-- session must not be replayable as that session.
--
-- `user_id` NULL means the principal is not a person. Never a placeholder.
CREATE TABLE IF NOT EXISTS console_session (
    token_hash TEXT   PRIMARY KEY CHECK (token_hash <> ''),
    session_id TEXT   NOT NULL CHECK (session_id <> ''),
    tenant_id  TEXT   NOT NULL REFERENCES tenant (tenant_id),
    user_id    TEXT   NULL REFERENCES platform_user (user_id),
    issued_at  BIGINT NOT NULL,
    expires_at BIGINT NOT NULL,
    revoked_at BIGINT
);

CREATE INDEX IF NOT EXISTS idx_console_session_user ON console_session (user_id, tenant_id)
    WHERE user_id IS NOT NULL;

-- ── ownership on the data plane ─────────────────────────────────────────────────────────────────────
-- Nullable-first, no default, no rewrite. NULL is PRE-OWNERSHIP (see the header). The indexes are
-- partial so that they index zero rows at creation and stay small forever — a pre-ownership row never
-- enters them, and no query ever looks for one.
-- 🔴 `proposal` is NOT in this list, and the reason is worth reading before adding it back.
--
-- Migration 0025 already gave `proposal` a `tenant_id`, and gave it NOT NULL, with
-- `idx_proposal_scope (tenant_id, workflow_id, created_at DESC)` beside it. So proposals have been
-- tenant-scoped since P5.5's console work and have NO pre-ownership state — every proposal row that
-- exists already names its owner. Adding a nullable column here would have been a no-op that read as
-- work, and the partial index would have duplicated 0025's. This was found by the fence in
-- `p27_account_system_pgproof_test.go`, not by reading, which is the argument for the fence.
--
-- The same is true of `delivery`, `workflow_ir`, `legal_acceptance`, `run_link`, `authored_change`,
-- `source_bundle` and `platform_workflow_graph`. What was missing was never tenant-awareness on every
-- table; it was the tenant ROW those columns name, and the three data-plane tables below that never got
-- one.
ALTER TABLE run          ADD COLUMN IF NOT EXISTS tenant_id TEXT NULL;
ALTER TABLE variant_spec ADD COLUMN IF NOT EXISTS tenant_id TEXT NULL;
ALTER TABLE eval_run     ADD COLUMN IF NOT EXISTS tenant_id TEXT NULL;

-- Each table orders by its own timestamp: `run` and `eval_run` by when they started, `variant_spec` by
-- when it was created. Using one name for all three would have meant adding a column.
CREATE INDEX IF NOT EXISTS idx_run_tenant          ON run          (tenant_id, started_at DESC) WHERE tenant_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_variant_spec_tenant ON variant_spec (tenant_id, created_at DESC) WHERE tenant_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_eval_run_tenant     ON eval_run     (tenant_id, started_at DESC) WHERE tenant_id IS NOT NULL;

-- ── account: the Free customer with no provider handle ──────────────────────────────────────────────
-- 🔴 The handle stays NOT NULL, and ABSENCE is the EMPTY STRING. This is D3 as amended by task 10.1.
--
-- D3 first chose NULL and rejected `''` as "a sentinel every consumer has to learn". Rollback proved that
-- wrong at a level D3's three alternatives never reached: `deploy/scripts/prove-rollback-is-reapply.sh`
-- deploys the PRIOR image against this schema, and the prior `scanAccount` reads this column into a Go
-- `string`. A NULL is `converting NULL to string is unsupported` — and because `List()` scans every row,
-- ONE such account takes down the operator console's tenant, delivery and cross-tenant views, adminlaunch
-- and the billing webhook, for EVERY customer. The window is not "until the first free sign-up" either:
-- `ensureSeededAccounts` writes handle-less accounts at BOOT, so the unreadable rows arrive with the
-- upgrade itself.
--
-- `''` scans into a `string` on both sides, so rollback is re-apply again. The sentinel objection is also
-- weaker than it was when D3 was written, because D3 itself introduced the column that carries the
-- meaning: no consumer reads the handle to learn whether an account is billable, it reads `plan_charges`.
-- And no provider customer is minted for a free user, which is the level-1 reasoning D3 was decided on —
-- that is untouched.
--
-- So 0013's `<> ''` CHECK is dropped (it is what makes `''` unwritable) and replaced by the conditional
-- form below. The CARD-DATA check is NOT touched: `''` passes it, as it must, because there is nothing
-- there to be a card number.
DO $$
DECLARE
    conname_ TEXT;
BEGIN
    -- 0013 wrote the check inline, so PostgreSQL named it. Found by shape rather than by a guessed name:
    -- an inline CHECK's generated name is an implementation detail we did not choose and must not assume.
    --
    -- 🔴 Bounded by `conrelid = 'account'::regclass`, NOT by a join to pg_class/pg_namespace, and the
    -- difference is not stylistic. `pg_get_constraintdef(c.oid)` is a FUNCTION over a catalog row, and the
    -- planner may evaluate it before the namespace filter — so the join form calls it on constraints in
    -- every schema in the database. `internal/pgtest` gives each test package its own schema in ONE shared
    -- database and `go test` runs packages concurrently, so another package dropping its schema mid-scan
    -- makes this fail with `could not open relation with OID nnn`.
    --
    -- That is not hypothetical: it is how this file first failed, and only once three more packages joined
    -- `make pg-proof` — the same shape 0028 was written to repair, one catalog function further along.
    -- `'account'::regclass` resolves through search_path, so the scan can only ever see THIS schema's
    -- table and the function is called on a handful of rows that are certain to still exist.
    SELECT c.conname INTO conname_
      FROM pg_constraint c
     WHERE c.conrelid = 'account'::regclass
       AND c.contype = 'c'
       AND pg_get_constraintdef(c.oid) = 'CHECK ((provider_customer_handle <> ''''::text))';
    IF conname_ IS NOT NULL THEN
        EXECUTE format('ALTER TABLE account DROP CONSTRAINT %I', conname_);
    END IF;
END $$;

-- 0013 also carries an inline `CHECK (provider_customer_handle <> '')`. A NULL passes it, which is the
-- correct reading: there is nothing here to be empty. The empty STRING is still refused, so "absent" has
-- exactly one spelling.
ALTER TABLE account ADD COLUMN IF NOT EXISTS plan_charges BOOLEAN NOT NULL DEFAULT TRUE;

-- The guard is SCHEMA-SCOPED, copied from 0028's repair rather than from 0024's original.
-- `pg_constraint` is a database-wide catalog and constraint names are unique per TABLE, so an unscoped
-- `WHERE conname = …` is satisfied as soon as any schema in the database holds that name — which reaches
-- straight through internal/pgtest's schema-per-package isolation and makes the outcome depend on which
-- test package won the race.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
         WHERE c.conrelid = 'account'::regclass
           AND c.conname = 'account_handle_required_when_plan_charges'
    ) THEN
        -- The invariant, stated as the condition the original NOT NULL actually meant: a customer who
        -- cannot be billed must not look billable. A paid plan with no provider handle is not a state to
        -- detect — it is a row the database refuses to hold.
        ALTER TABLE account ADD CONSTRAINT account_handle_required_when_plan_charges
            CHECK (provider_customer_handle <> '' OR plan_charges = FALSE);
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (38, 'p27_account_system')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
