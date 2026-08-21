-- P32 tasks 2.4, 2.7, 2.8: the repository CONNECTION, its append-only read ledger, and the two columns
-- that make a cloned snapshot revocable and expirable.
--
-- # Why two tables and two columns, and not four tables
--
-- `careful-table-creation` says a new table is a one-way door and demands the alternatives be written
-- down. Three were considered:
--
--   A. **A third table for cloned snapshots.** Rejected. A cloned snapshot has EXACTLY the grain
--      `source_bundle` already has — (tenant, workflow, revision) → bytes in the blob store — so a
--      second table would be a second answer to "what source is this tenant holding on our disks",
--      which is the question a deletion request asks. Two tables answering it is how a deletion misses
--      half the data. It also would have forced a second extractor, and mode parity (§7.8) would then
--      be a claim about two code paths agreeing rather than a property.
--
--   B. **A JSONB blob on `source_bundle` carrying the connection and expiry.** Rejected for the
--      predicate: the cascade is `DELETE ... WHERE connection_id = $1` and the sweep is
--      `DELETE ... WHERE expires_at_ms <= $1`. Both are indexed scans over a scalar; inside JSONB they
--      are expression scans nobody will index correctly under pressure, and the sweep is the one thing
--      that MUST keep working when nobody is looking at it.
--
--   C. **What was built: three new tables + two nullable columns.** `source_connection`,
--      `source_clone_record` and `source_local_pairing` have grains nothing existing has (per grant;
--      per read; per console↔agent pairing). The two columns go on `source_bundle` because a cloned
--      snapshot IS a snapshot.
--
-- The third table earned its own argument, because it is the one that looks most reusable. A local
-- pairing was tried on `source_connection` with a `forge = 'local'` value, and it is the wrong table:
-- a pairing has no repository, no grant kind, no external id and no credential, and it HAS a state, a
-- user code, a machine name and an expiry that `source_connection` has no column for. Overloading
-- would make five columns nullable on the grant table, weaken the CHECK that keeps `forge` closed, and
-- make `TestConnectionHasNoFieldThatCanExpressWriteOrBreadth` guard a struct that had become two
-- different things. It was also tried as an in-process map and refused for P27's device-authorization
-- reason, quoted here because it is the same flow: *"the CLI polls: it requests a code against one
-- replica and polls against whichever the load balancer picks next, so a map means a login that
-- succeeds or hangs depending on routing, intermittently, with nothing logged."*
--
-- # 🔴 The two columns are NULLABLE, and the NULLs mean something
--
--   `connection_id IS NULL`  → a PUSHED bundle. It came from a customer act and no grant governs it.
--   `expires_at_ms IS NULL`  → no expiry. The pushed-bundle rule (PRD §14 A4): held until the customer
--                              deletes it, because expiring it would delete an artifact they chose to
--                              hand over and would have to hand over again.
--
-- That is what makes Mode 1 UNCHANGED by this migration (§7.11): every existing row gets NULL in both
-- columns, the cascade predicate never matches it, and the retention predicate never matches it. A
-- default of `0` on `expires_at_ms` would have expired every bundle ever pushed at the moment this
-- migration ran, which is the shape of accident this comment exists to prevent.
--
-- # 🚫 NO COLUMN HERE CAN HOLD A FORGE CREDENTIAL
--
-- `source_connection` has no `token`, no `secret`, no `api_key`, and no free-text column an operator
-- could paste one into. `external_id` is the FORGE's own identifier for the grant — an installation id
-- — which names the grant and does not authenticate it. The credential lives in the deployment's
-- secret store, reached only through `providergateway.ForgeSecrets`, whose custody shape hands it to a
-- closure and never returns it. `TestNoConnectionColumnCanCarryACredential` discovers this schema
-- rather than reading a whitelist, so a column added later is caught by the fence and not by review.
--
-- # 🚫 NO COLUMN CAN EXPRESS A SCOPE
--
-- There is no `scope`, no `permissions`, no `all_repositories`. ADR-013 Option B (organization-wide
-- access) is refused on the record, and the refusal is structural: a later phase proposing it cannot
-- ship it as a string in an existing column, because there is no column it could arrive in.
--
-- # Timestamps are int64 MILLISECONDS
--
-- `BIGINT`, not `TIMESTAMPTZ`, and no timestamp literal appears in this file. These values are compared
-- against numbers Go computed, and a driver rendering a TIMESTAMPTZ into a session time zone is a
-- second clock — which is how four tests in this repository once went red on the calendar alone.
--
-- ⚠️ `source_bundle.received_at` stays TIMESTAMPTZ. It is a shipped column read by shipped code and
-- this migration does not touch it: converting it would be a rewrite of a table for tidiness, which is
-- the trade `careful-table-creation` exists to refuse.
--
-- Idempotent, guarded BY DEFINITION rather than by name: `CREATE TABLE IF NOT EXISTS` is satisfied by a
-- table of that name with any columns at all, so the DO blocks check the columns and constraints the
-- stores actually query.
--
-- Dialect: PostgreSQL only. See 0045's header — the SQLite store is the dev ledger and holds no part of
-- this domain; a copy there would be a second schema nothing reads.

BEGIN;

-- ── The grant ───────────────────────────────────────────────────────────────────────────────────────
--
-- One row per authorization. One repository, read-only, revocable.
CREATE TABLE IF NOT EXISTS source_connection (
    connection_id  TEXT   PRIMARY KEY,
    tenant_id      TEXT   NOT NULL,
    -- One connection per workflow, enforced by the unique index below rather than by a check in Go: a
    -- second connection for one workflow makes "which repository is this graph from" unanswerable, and
    -- a race between two browser tabs is exactly how a second row arrives.
    workflow_id    TEXT   NOT NULL,
    forge          TEXT   NOT NULL,
    -- `owner/name`. Exactly one, checked by shape here as well as in Go — a repository value with no
    -- slash reaches the clone as a malformed URL and fails as `repository_not_found`, which sends the
    -- customer to look at a repository that is fine.
    repository     TEXT   NOT NULL,
    -- The directory a snapshot is rooted at within the repository, or NULL for the whole repository
    -- (PRD §14 A3). The GRANT stays repository-scoped because no forge issues a narrower one; this
    -- bounds what is actually READ.
    sub_path       TEXT,
    grant_kind     TEXT   NOT NULL,
    -- The forge's own id for the grant, so a customer can find and revoke it on THEIR side too. Not a
    -- secret: it names the grant, it does not authenticate it.
    external_id    TEXT,
    created_by     TEXT   NOT NULL,
    created_at_ms  BIGINT NOT NULL,

    CONSTRAINT source_connection_forge_known
        CHECK (forge IN ('github', 'gitlab', 'bitbucket')),
    CONSTRAINT source_connection_grant_kind_known
        CHECK (grant_kind IN ('app_installation', 'access_token')),
    CONSTRAINT source_connection_repository_shape
        CHECK (repository LIKE '%/%' AND repository NOT LIKE '/%' AND repository NOT LIKE '%/' AND repository NOT LIKE '%/%/%'),
    -- A sub-path that climbs would move the extraction ROOT outside the scratch directory, and every
    -- per-entry traversal check downstream would then be measuring against the wrong root and passing.
    CONSTRAINT source_connection_sub_path_relative
        CHECK (sub_path IS NULL OR (sub_path NOT LIKE '/%' AND sub_path NOT LIKE '%..%' AND sub_path NOT LIKE '%\%'))
);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
         WHERE schemaname = current_schema() AND indexname = 'uq_source_connection_workflow'
    ) THEN
        CREATE UNIQUE INDEX uq_source_connection_workflow
            ON source_connection (tenant_id, workflow_id);
    END IF;

    -- "What has this tenant connected" — the console's list, and the first question a revocation asks.
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
         WHERE schemaname = current_schema() AND indexname = 'idx_source_connection_tenant'
    ) THEN
        CREATE INDEX idx_source_connection_tenant
            ON source_connection (tenant_id, created_at_ms);
    END IF;
END $$;

-- ── The read ledger ─────────────────────────────────────────────────────────────────────────────────
--
-- APPEND-ONLY, and customer-readable. It is the whole justification for admitting a standing
-- capability: "usable without the customer present" is acceptable only if the customer can afterwards
-- read exactly when it was used and for what.
--
-- 🔴 FAILURES are recorded here too, on the same table, with the cause in `outcome`. A ledger of
-- successes only cannot answer "when did it start failing", which is the question asked immediately
-- after a token is rotated — and that question arriving with no answer is what makes a customer stop
-- trusting the ledger for the successes as well.
CREATE TABLE IF NOT EXISTS source_clone_record (
    record_id      TEXT   PRIMARY KEY,
    tenant_id      TEXT   NOT NULL,
    -- ON DELETE CASCADE: revoking a grant removes its ledger. Deliberate, and it is a real trade —
    -- see the store's `Revoke` comment. The capability is gone, so the evidence about which of the
    -- customer's repositories we read and when goes too; what survives is the per-forge aggregate,
    -- which names no repository.
    connection_id  TEXT   NOT NULL REFERENCES source_connection (connection_id) ON DELETE CASCADE,
    repository     TEXT   NOT NULL,
    revision       TEXT   NOT NULL,
    -- `person` or `scheduled` — FR9's distinction, and the reason this table exists at all.
    actor          TEXT   NOT NULL,
    actor_id       TEXT,
    reason         TEXT,
    -- `succeeded` or one of the four causes. Constrained, so a fifth spelling cannot arrive and make
    -- the console's four-message switch fall through to a blank card.
    outcome        TEXT   NOT NULL,
    bytes          BIGINT NOT NULL DEFAULT 0,
    entries        BIGINT NOT NULL DEFAULT 0,
    duration_ms    BIGINT NOT NULL DEFAULT 0,
    at_ms          BIGINT NOT NULL,

    CONSTRAINT source_clone_record_actor_known
        CHECK (actor IN ('person', 'scheduled')),
    CONSTRAINT source_clone_record_outcome_known
        CHECK (outcome IN ('succeeded', 'credential_rejected', 'repository_not_found', 'revision_not_found', 'network'))
);

DO $$
BEGIN
    -- The ledger is read newest-first, per connection. That is the only way it is read.
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
         WHERE schemaname = current_schema() AND indexname = 'idx_source_clone_record_connection'
    ) THEN
        CREATE INDEX idx_source_clone_record_connection
            ON source_clone_record (connection_id, at_ms DESC);
    END IF;
END $$;

-- ── The local-mode pairing (Mode 3) ────────────────────────────────────────────────────────────────
--
-- The console hands out a code; a person types it into a terminal on the machine that already holds the
-- repository. The tree is read THERE and never transmitted — see `localpair.go` for why a browser file
-- picker was refused (design D5).
--
-- 🚫 NO COLUMN CAN HOLD A PATH OR ANYTHING FROM THE TREE. `machine_name` is what the agent calls
-- itself and `revision` is a commit id. There is deliberately no `repository_path`: a local filesystem
-- path is the customer's own layout, it tells the platform nothing it needs, and having somewhere to
-- put it is how it ends up transmitted.
--
-- 🔴 The expiry is a COLUMN and not a sweeper's job. `Pairing.StateAt` computes expiry at READ, so an
-- unclaimed code stops being claimable at its deadline whether or not any background job has run — a
-- pairing flow whose safety depends on a sweeper having run recently is unsafe exactly when the
-- deployment is unhealthy.
CREATE TABLE IF NOT EXISTS source_local_pairing (
    pairing_id     TEXT   PRIMARY KEY,
    tenant_id      TEXT   NOT NULL,
    workflow_id    TEXT   NOT NULL,
    state          TEXT   NOT NULL,
    -- The code the person types. UNIQUE across the table, not per tenant: the agent claims by code
    -- alone and names no tenant (it cannot know one), so a code that meant two things would resolve to
    -- whichever row the planner returned first.
    user_code      TEXT   NOT NULL,
    machine_name   TEXT,
    revision       TEXT,
    created_at_ms  BIGINT NOT NULL,
    claimed_at_ms  BIGINT,
    expires_at_ms  BIGINT NOT NULL,

    CONSTRAINT source_local_pairing_state_known
        CHECK (state IN ('pending', 'paired', 'expired')),
    -- A paired row names its machine and says when. A `paired` row with neither cannot answer "which
    -- machine reads this workflow", which is the only question this table exists to answer.
    CONSTRAINT source_local_pairing_paired_is_complete
        CHECK (state <> 'paired' OR (machine_name IS NOT NULL AND claimed_at_ms IS NOT NULL))
);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
         WHERE schemaname = current_schema() AND indexname = 'uq_source_local_pairing_code'
    ) THEN
        CREATE UNIQUE INDEX uq_source_local_pairing_code ON source_local_pairing (user_code);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
         WHERE schemaname = current_schema() AND indexname = 'idx_source_local_pairing_tenant'
    ) THEN
        CREATE INDEX idx_source_local_pairing_tenant
            ON source_local_pairing (tenant_id, created_at_ms DESC);
    END IF;
END $$;

-- ── The two columns that make a cloned snapshot revocable and expirable ─────────────────────────────
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_schema = current_schema() AND table_name = 'source_bundle' AND column_name = 'connection_id'
    ) THEN
        -- No FK to source_connection. Deliberate: the cascade is performed EXPLICITLY by
        -- `sourceingest.Service.Revoke`, snapshots first, so that a partial failure leaves the grant
        -- row in place and the revocation retryable. An ON DELETE CASCADE here would delete the trees
        -- as a side effect of deleting the row — which reverses that order and, worse, would make the
        -- cascade invisible in the code that is responsible for it.
        ALTER TABLE source_bundle ADD COLUMN connection_id TEXT;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_schema = current_schema() AND table_name = 'source_bundle' AND column_name = 'expires_at_ms'
    ) THEN
        -- NULL = no expiry = the pushed-bundle rule. Every pre-existing row gets NULL, so this
        -- migration changes nothing about Mode 1.
        ALTER TABLE source_bundle ADD COLUMN expires_at_ms BIGINT;
    END IF;

    -- The cascade's predicate, indexed. Partial, because the overwhelming majority of rows are pushed
    -- bundles with a NULL here and there is no reason to carry them in this index.
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
         WHERE schemaname = current_schema() AND indexname = 'idx_source_bundle_connection'
    ) THEN
        CREATE INDEX idx_source_bundle_connection
            ON source_bundle (connection_id) WHERE connection_id IS NOT NULL;
    END IF;

    -- The retention sweep's predicate, indexed and partial for the same reason. 🔴 This index is what
    -- makes the sweep a bounded operation rather than a full scan of every snapshot the platform has
    -- ever held — and the sweep is the one thing that must keep working when nobody is looking at it.
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
         WHERE schemaname = current_schema() AND indexname = 'idx_source_bundle_expiry'
    ) THEN
        CREATE INDEX idx_source_bundle_expiry
            ON source_bundle (expires_at_ms) WHERE expires_at_ms IS NOT NULL;
    END IF;

    -- A derived snapshot must name its connection AND carry an expiry; a pushed one must have neither.
    -- Stated as one constraint because the two NULLs travel together: a row with a connection and no
    -- expiry is a tree held forever under a grant, which is precisely the standing capability ADR-013
    -- bounded, and a row with an expiry and no connection is a bundle that will vanish under a
    -- customer who was told it is kept until they delete it.
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
          JOIN pg_class      t  ON t.oid  = c.conrelid
          JOIN pg_namespace  ns ON ns.oid = t.relnamespace
         WHERE c.conname = 'source_bundle_derived_pair'
           AND t.relname = 'source_bundle'
           AND ns.nspname = current_schema()
           AND c.contype = 'c'
    ) THEN
        ALTER TABLE source_bundle ADD CONSTRAINT source_bundle_derived_pair
            CHECK ((connection_id IS NULL) = (expires_at_ms IS NULL));
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (49, 'p32_repo_intake')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
