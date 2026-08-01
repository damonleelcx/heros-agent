# Deployment Topology — Spec Delta (P19)

Product rationale: [`../../../../docs/prd/P19-deployment-delivery.md`](../../../../docs/prd/P19-deployment-delivery.md)
§6 (FR1–FR5, FR20–FR24) and §7. Architecture decisions:
[`../../design.md`](../../design.md) Decisions 2, 3, 6, 7, 8.

Covers the composition of the platform into **one deployment unit** expressed on **two substrates** (Docker
Compose and Kubernetes) from **one digest-pinned image set** and one secret contract; the **control-plane /
data-plane** separation as a runtime boundary; the **stateful stores** and the **single-Postgres SPOF** whose
backup is its precondition; the **air-gapped package**; and **declarative-idempotent** deployment with
user-state-preserving upgrade and re-apply rollback.

## ADDED Requirements

### Requirement: The platform SHALL be deployable as one unit on both Docker Compose and Kubernetes from one image set

The deploy SHALL define a single platform deployment description that brings up the Go service, Postgres, the
object store, the telemetry substrate, the queue, the vector and graph stores, and the customer console, and
SHALL express it on both Docker Compose and Kubernetes referencing the **same digest-pinned images** and the
**same secret/environment contract**.

#### Scenario: One command brings the whole platform up on a single host

- **WHEN** an operator runs the compose bring-up on a clean host with the required secrets provided
- **THEN** the Go service, Postgres, the object store, the telemetry substrate, the queue, the vector/graph
  stores, and the customer console all start
- **AND** `/readyz` reports the whole platform ready, aggregating every component.

#### Scenario: Both substrates reference the same images

- **WHEN** the Docker Compose stack and the Kubernetes base are compared
- **THEN** they reference the same image **digests** and the same environment-variable contract
- **AND** a CI check fails if the two substrates diverge on either.

### Requirement: The topology SHALL separate control plane from data plane as a runtime boundary

The deploy SHALL distinguish **control plane** (policy, config, entitlement, admin) from **data plane**
(discovery, apply, run, eval) such that the data plane continues serving already-established work when the
control plane is unreachable, and this separation SHALL be a runtime boundary, not only a diagram.

#### Scenario: The data plane survives control-plane loss

- **WHEN** the control-plane components are made unreachable
- **THEN** the data plane continues to serve work that was already established
- **AND** only new control-plane-dependent operations (new policy, new config) are affected.

#### Scenario: The separation is enforced, not conventional

- **WHEN** the deployed network is inspected
- **THEN** cross-plane traffic is limited to the permitted seam flows (policy/config/token down; audit/cost/
  trace up)
- **AND** a component reaching across the seam outside those flows is blocked, not merely discouraged.

### Requirement: Every secret SHALL come from the environment or an external store and SHALL refuse to start when unset

No secret SHALL appear in a manifest, an env-example file, any repository file, a log line, a trace attribute,
or a client bundle. A required secret that is unset SHALL cause the deployment to **refuse to start** (the
`${VAR:?}` / apply-time-required form), not to start misconfigured.

#### Scenario: A missing secret refuses to start

- **WHEN** a required secret is not provided at bring-up
- **THEN** the deployment refuses to start and reports which secret is missing
- **AND** the platform does not come up in a misconfigured state that fails later somewhere less obvious.

#### Scenario: No secret in the committed tree or any observable surface

- **WHEN** the repository, the manifests, the env-example files, the logs, the traces, and the client bundles
  are scanned for secret material
- **THEN** none is found
- **AND** the scan is a build-/apply-time gate, so a secret in any of them fails the build rather than being
  caught in review.

### Requirement: Each stateful store SHALL be declared, and Postgres SHALL be a documented SPOF whose backup ships with the deploy

Each stateful store SHALL be a stateful component with a named volume/claim. **Postgres SHALL be documented as
a single point of failure** with its blast radius, and its **backup automation SHALL ship as part of the
deploy**; a deploy that accepts single-Postgres without shipping backup is non-conformant.

#### Scenario: Backup ships and the dump fails loud

- **WHEN** the platform is deployed
- **THEN** a backup procedure for Postgres is present and scheduled
- **AND** a failed or empty dump does not leave a zero-byte backup file — every failure path deletes it — so a
  poisoned rollback chain cannot form.

#### Scenario: The SPOF is documented, not implied away

- **WHEN** the deploy documentation is read
- **THEN** single-Postgres is named as a single point of failure with its blast radius and its backup
  precondition
- **AND** the docs do not imply uniform redundancy the deploy does not provide.

### Requirement: Stateless hot-path components SHALL be horizontally scalable

Stateless components on the hot path SHALL scale horizontally by replica count as a **value**, not a code
change, and SHALL NOT hold local state that a second replica would diverge on.

#### Scenario: Adding a replica does not diverge

- **WHEN** the replica count of a stateless hot-path component is increased
- **THEN** the new replica serves correctly with no code change
- **AND** no local state on one replica produces a different answer than another.

### Requirement: Deployment SHALL be declarative and idempotent, with upgrade by re-apply and no teardown

Applying the deployment description twice SHALL converge to the same state. **Upgrade SHALL be applying a new
package with the same command**, with no bespoke teardown path.

#### Scenario: A second apply is a no-op

- **WHEN** the same deployment description is applied twice
- **THEN** the second apply converges to the same state and reports no destructive change
- **AND** a re-install reports "already present" exactly once and does not re-prompt for a master password.

#### Scenario: Upgrade is the same command with a new package

- **WHEN** an operator upgrades to a new version
- **THEN** the operation is the same apply command with a newer package, not a version-specific teardown script.

### Requirement: Upgrade SHALL preserve user/tenant state and rollback SHALL be re-applying the prior package

An upgrade SHALL preserve tenant credentials, the session secret, the admin identity map, and eval/lineage
data **byte-for-byte**; system-derived state may be re-rendered but SHALL be regenerable from user-state +
templates. **Rollback SHALL be re-applying the prior package/manifest**, non-destructive to legitimate data
produced during the upgrade window, and SHALL NOT fall back to a same-version backup.

#### Scenario: User-state survives an upgrade

- **WHEN** the platform is upgraded across a version boundary
- **THEN** tenant credentials, the session secret, the admin identity map, and eval/lineage data are unchanged
- **AND** any system-derived state that was re-rendered is regenerable from user-state and templates.

#### Scenario: Rollback re-applies the prior package non-destructively

- **WHEN** an operator rolls back
- **THEN** the operation re-applies the prior package/manifest
- **AND** legitimate data produced during the upgrade window is not destroyed
- **AND** the rollback never falls back to a same-version backup.

### Requirement: An air-gapped package SHALL install with no public egress and be integrity-verifiable before apply

The deploy SHALL define a **self-contained package** (platform binaries + `docker save` images + manifests +
checksums) that installs with no public egress, whose integrity is **verifiable before apply**, and that is
reproducible from pinned inputs. The package SHALL ship a **`doctor`/preflight** check and **backup + restore**
procedures an operator runs without the platform team.

#### Scenario: Offline install with pre-apply verification

- **WHEN** an operator installs from the package on a host with no public egress
- **THEN** the install completes using only the package contents
- **AND** the operator can verify the package checksums before applying anything.

#### Scenario: The operator can self-serve operations

- **WHEN** the operator needs to diagnose, back up, or restore the system
- **THEN** a `doctor`/preflight check and backup + restore procedures are present in the package
- **AND** the operator runs them without contacting the platform team.

### Requirement: The deployed process SHALL apply the platform schema at boot, idempotently

The process the deployment actually runs SHALL apply the platform's Postgres migrations at boot and SHALL
record each applied version in a ledger it **reads** before deciding whether to apply. A second boot against an
already-migrated database SHALL make no schema change.

#### Scenario: A second boot applies nothing

- **WHEN** the platform is started twice against the same database
- **THEN** the first boot applies the outstanding migrations and the second applies none
- **AND** the decision comes from reading the applied-version ledger, not from the DDL's own tolerance for
  re-running — a ledger that is written and never read cannot tell "never applied" from "applied and since
  reverted".

#### Scenario: An upgrade carries the schema forward without touching user state

- **WHEN** a new version is deployed over an existing database
- **THEN** only the migrations that have not been applied are applied
- **AND** tenant credentials, the session secret, the admin identity map and eval/lineage data are unchanged.

### Requirement: `/readyz` SHALL name a store only if it probes that store

A readiness component name SHALL correspond to the dependency actually probed. Presenting one dependency's
probe under another dependency's name is non-conformant.

#### Scenario: A dead store turns readiness red under its own name

- **WHEN** the Postgres the deployment declares is made unreachable
- **THEN** `/readyz` reports not-ready and names `postgres`
- **AND** the verdict comes from probing that database, not from renaming a different store's probe.

### Requirement: Every capability surface SHALL be registered, and an unsourced one SHALL answer 503

The deployed process SHALL register the routes of every capability the platform ships. A capability with no
source on this deployment SHALL answer **503 not-mounted**; leaving its routes unregistered so the request
falls through to a **404** is non-conformant.

#### Scenario: An unsourced capability is distinguishable from a missing identifier

- **WHEN** a client calls a capability this deployment has no source for
- **THEN** the response is 503 with a not-mounted body
- **AND** a call for an identifier that does not exist still returns 404, so the two remain distinguishable.

#### Scenario: The console does not report a real workflow as missing

- **WHEN** the customer console loads a page backed by an unsourced capability, for a workflow that exists
- **THEN** it renders "this capability is not installed on this deployment"
- **AND** it does not render "no such workflow".

### Requirement: The deployment SHALL declare which capabilities a fresh install serves

The deploy documentation SHALL state which capabilities are served, which are registered-but-unsourced, and
what makes the difference, in one place an operator reads before installing.

#### Scenario: The capability set is readable without calling the system

- **WHEN** an operator reads the deploy runbook
- **THEN** the served and the registered-but-unsourced capabilities are both listed
- **AND** the list does not require the operator to probe the running system to discover it.

### Requirement: The two substrates SHALL NOT diverge on the environment contract

Every environment variable the deployed processes read SHALL appear in the deployment's documented contract,
and the Compose and Kubernetes descriptions SHALL agree on it. A CI check SHALL fail on divergence.

#### Scenario: A variable added to one substrate fails the gate

- **WHEN** an environment variable is added to the Kubernetes base and not to the Compose file (or the reverse)
- **THEN** the parity check fails and names the variable and the substrate that is missing it
- **AND** the failure is a build-time gate, not a review observation.

### Requirement: Each capability that landed after this change was written SHALL have a deployment contract

Every capability shipped after the initial P19 artifacts — installable packages, payments, SSO/identity, legal
and developer docs, analytics and error monitoring, and the operator surfaces — SHALL have its secret names,
its readiness aggregation and any scheduled job it requires expressed on both substrates.

#### Scenario: A capability is configurable on the deployment that ships it

- **WHEN** an operator deploys a form that includes a given capability
- **THEN** every credential and setting that capability reads is present in the documented environment contract
- **AND** a setting whose misconfiguration can refuse the boot is documented beside the others rather than
  discovered from a crash.

#### Scenario: The platform's own reporting integrations are NOT in the contract

- **WHEN** a deployment manifest or env-example is scanned for the analytics and error-reporting switches
- **THEN** none of their variable names appears, not even set to an empty value
- **AND** this is the deliberate exception to the requirement above: those integrations belong to the
  platform's own hosted deployment rather than to a customer's install, an empty slot is one `--set` from
  being filled in a file a customer edits without reading, and a customer install that reports to nobody is
  the correct state — reported as `absent` and deliberately silent.

### Requirement: The consent-record retention job SHALL ship as a scheduled unit

The retention job SHALL be deployed on both substrates as a scheduled unit, SHALL default to a dry run, and
SHALL refuse to act when no retention window is configured.

#### Scenario: Retention runs without an operator remembering it

- **WHEN** the platform is deployed
- **THEN** the retention job is present and scheduled on that substrate
- **AND** with no window configured it reports what it would remove and removes nothing.

### Requirement: A started backing service SHALL be connected or labelled as provisioned ahead of use

A component the deployment starts SHALL either be connected by a deployed process, or be labelled — in the
manifest and in the runbook — as provisioned ahead of the capability that will use it. An unused component
SHALL NOT be aggregated into `/readyz` as though the platform depended on it.

#### Scenario: An operator can tell what the platform actually uses

- **WHEN** an operator reads the manifests and the runbook
- **THEN** each started backing service is either shown to be connected or labelled as provisioned ahead of use
- **AND** readiness does not imply a dependency the platform does not have.
