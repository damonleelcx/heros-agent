# Operator Console Deploy — Spec Delta (P19)

Product rationale: [`../../../../docs/prd/P19-deployment-delivery.md`](../../../../docs/prd/P19-deployment-delivery.md)
§6 (FR16–FR19) and §7. Architecture decisions: [`../../design.md`](../../design.md) Decision 5. Inherits
[ADR-006](../../../../docs/adr/ADR-006-console-deploy-packaging.md) (a dead process is a dead container),
[ADR-008](../../../../docs/adr/ADR-008-console-tenant-identity-seam.md) (tenant identity seam), and
**P8 Decision 11** (operator/customer isolation is the browser origin boundary, not routing).

Covers the **deploy artifact** the P8 operator console has never had: a two-container unit on its **own
origin**, with a **disjoint cookie jar**, its **own** credential, a `dev`-refuses-production identity seam,
and health **aggregated** by the platform `/readyz`. It is unreachable from the customer console by
construction.

> The operator console can today be started only by hand (`npm run start --port 4310`). It must not be folded
> into the customer console's origin — a role-gated `/admin` route would put cross-tenant operator capability
> one authorization bug away from a customer session — so it needs its own deployment unit, not a route.

## ADDED Requirements

### Requirement: The operator console SHALL ship as a two-container unit on its own origin

The P8 operator console SHALL ship as a **two-container deployment unit** (admin BFF on `:4310` + platform/admin
API on `:4311`) on its **own origin**, each container independently built, probed, restarted and versioned,
with **no in-container supervisor** — inheriting ADR-006 (a dead process is a dead container).

#### Scenario: The operator console has a deploy artifact

- **WHEN** an operator deploys the platform
- **THEN** a `Dockerfile.admin-console` and a compose/manifest pair exist that stand the operator console up as
  its own unit, not a hand-run `npm` process
- **AND** the admin BFF and the admin API are two independently probed and restarted containers.

#### Scenario: A dead operator-console process is a dead container

- **WHEN** the admin BFF process dies
- **THEN** its container dies and the orchestrator's restart policy fires
- **AND** no in-container supervisor keeps a dead process's container reporting healthy.

### Requirement: The operator console SHALL be unreachable from the customer console's origin

The operator console SHALL run on a **separate origin** from the customer console, with a **disjoint cookie
jar**, a separate BFF, and a separate credential; no admin capability SHALL be reachable from a customer-console
route, and no session SHALL be shared between the two.

#### Scenario: Origins and cookie jars are disjoint

- **WHEN** the deployed origins of the two consoles are compared
- **THEN** they are different origins with separate cookie jars
- **AND** a customer-console session cookie is not valid at the operator console and vice-versa.

#### Scenario: No admin capability is reachable from a customer route

- **WHEN** a request from the customer console's origin attempts to reach an operator-console capability
- **THEN** it is refused
- **AND** the refusal is a construction of the origin boundary, verified by a test, not a routing convention.

### Requirement: The operator console BFF SHALL hold its own credential and refuse a dev identity in production

The operator console BFF SHALL hold its **own** platform/admin credential server-side, distinct from the
customer BFF's, and SHALL refuse to start in production under a `dev` tenant-identity seam (ADR-008).

#### Scenario: The operator BFF's credential is its own

- **WHEN** the operator console BFF is deployed
- **THEN** it holds a credential distinct from the customer console BFF's, in its own process environment
- **AND** neither BFF can act with the other's credential.

#### Scenario: The dev identity seam refuses production

- **WHEN** the operator console is deployed with `NODE_ENV=production` and a `dev` tenant-identity seam
- **THEN** it refuses to start
- **AND** only the `configured` seam (reading the deployment's assertion→tenant map) is accepted in production.

### Requirement: The platform readiness SHALL aggregate the operator console's health

The platform `/readyz` SHALL aggregate the operator console's health the same way it aggregates the customer
console's, reporting **not ready** and **naming** the operator console when it is unreachable.

#### Scenario: A dead operator console makes the deployment report not-ready

- **WHEN** the operator console is unreachable
- **THEN** `/readyz` reports not ready and names the operator console as the degraded component
- **AND** a healthy platform in front of a dead operator console does not report ready.
