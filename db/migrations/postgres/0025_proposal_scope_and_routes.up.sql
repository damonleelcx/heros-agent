-- Tenant and workflow scope for proposals, and the delivery-route registry that has never existed.
--
-- # Why P5.5's tables cannot be mounted as they stand
--
-- `0012_p55_proposals_verification` created `proposal`, `proposal_evidence`, `verdict` and `rank_entry`
-- with ZERO `tenant_id` and ZERO `workflow_id` columns between them. It was designed single-tenant and
-- workflow-implicit: a proposal keys on `diagnosis_id` and `base_variant_id`, so there is no join to a
-- workflow at all — `api.ProposalsSource.Surface(workflowID)` is unanswerable from this schema, never
-- mind scoped to a tenant.
--
-- Nothing noticed because no Go code has ever written to these tables (`internal/proposal` and
-- `internal/verification` contain no `sql.DB` reference of any kind). The schema and the read model were
-- built in different phases against different assumptions, and the gap only appears when something
-- tries to serve one from the other.
--
-- 🔴 THE SCOPE GOES ON `proposal` ONLY. `proposal_evidence`, `verdict` and `rank_entry` all reference
-- `proposal(proposal_id)`, so the scope reaches them through the join. Copying tenant_id onto each would
-- create four places for one fact to be wrong in — and the failure of a denormalised tenant id is that a
-- row is readable by a tenant that does not own it.
--
-- Dialect: PostgreSQL.

BEGIN;

-- NOT NULL with NO DEFAULT, deliberately.
--
-- On an empty table this succeeds. On a non-empty one it FAILS — and that failure is the correct
-- outcome, not an inconvenience to engineer around: rows exist that this migration's author did not
-- know about, and back-filling them with an empty tenant would make somebody's proposals readable by
-- every tenant on the platform. A loud failure at boot is the cheap version of that discovery.
--
-- The table is empty on every deployment because no writer has ever existed. If that turns out to be
-- false somewhere, the deployment stops and a human decides who owns those rows.
ALTER TABLE proposal ADD COLUMN tenant_id TEXT NOT NULL;
ALTER TABLE proposal ADD COLUMN workflow_id TEXT NOT NULL;

ALTER TABLE proposal ADD CONSTRAINT proposal_scope_is_named
    CHECK (tenant_id <> '' AND workflow_id <> '');

-- The read the surface performs: "this tenant's proposals for this workflow, newest first".
CREATE INDEX idx_proposal_scope ON proposal (tenant_id, workflow_id, created_at DESC);

-- The delivery-route registry. `forgedelivery.RouteRegistry` has been an interface with no table since
-- P12 landed, which is the other half of why delivery could not mount.
--
-- A route says WHERE a tenant's verified proposals are delivered for a given target. It holds no
-- credential: the forge credential lives with the CI that performs the delivery, which is the whole
-- point of the CI-mediated design — the platform prepares, CI delivers, and the platform never holds a
-- token that can write to a customer's repository.
CREATE TABLE delivery_route (
    tenant_id  TEXT        NOT NULL,
    -- The delivery target ("owner/repo"), as forgedelivery.Target names it.
    target     TEXT        NOT NULL CHECK (target <> ''),
    forge      TEXT        NOT NULL CHECK (forge <> ''),
    -- The base branch deliveries open against.
    base_ref   TEXT        NOT NULL CHECK (base_ref <> ''),
    -- capability_kind is EMPTY when delivery capability is intact, and names the lost-capability
    -- condition otherwise. A third state, not a boolean: "configured and working", "configured but
    -- degraded" and "revoked" lead to three different next actions, and a boolean can hold two.
    capability_kind   TEXT        NOT NULL DEFAULT '',
    capability_detail TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, target),

    CONSTRAINT delivery_route_capability_known
        CHECK (capability_kind IN ('', 'degraded', 'revoked')),
    -- A lost capability must say WHAT was lost. "Delivery is degraded" with no detail is a banner
    -- nobody can act on.
    CONSTRAINT delivery_route_lost_capability_is_explained
        CHECK (capability_kind = '' OR capability_detail <> '')
);

-- "Does this tenant have a lost-capability condition anywhere" — the question RouteConditionFor asks
-- first, because a degraded route is more urgent than a missing one.
CREATE INDEX idx_delivery_route_capability ON delivery_route (tenant_id)
    WHERE capability_kind <> '';

INSERT INTO schema_migrations (id, name) VALUES (25, 'proposal_scope_and_routes')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
