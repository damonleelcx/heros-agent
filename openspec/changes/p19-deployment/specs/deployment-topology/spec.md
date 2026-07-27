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
