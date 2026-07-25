# Kubernetes Delivery — Spec Delta (P19)

Product rationale: [`../../../../docs/prd/P19-deployment-delivery.md`](../../../../docs/prd/P19-deployment-delivery.md)
§6 (FR6–FR11) and §7. Architecture decisions: [`../../design.md`](../../design.md) Decisions 1, 3, 4.

Covers the **Kustomize `base/` + `overlays/{dev,staging,prod,airgapped}`** delivery: per-workload probes,
resource limits and rolling-update policy; a **PodDisruptionBudget** where replicas > 1; a **NetworkPolicy**
that encodes control/data-plane separation and makes egress an **allowlist**; **digest-pinned** images; and
secrets referenced via an **external-secret mechanism**, never a committed plaintext `Secret`.

## ADDED Requirements

### Requirement: The Kubernetes delivery SHALL be a Kustomize base plus environment overlays

The Kubernetes delivery SHALL be a Kustomize `base/` that is a complete, applyable description of the platform,
plus `overlays/{dev,staging,prod,airgapped}` where each overlay expresses **only its differences** from the
base.

#### Scenario: The base is complete and applyable on its own

- **WHEN** `kubectl kustomize base/` is rendered
- **THEN** the output is a complete set of manifests that can stand up the platform
- **AND** it references no value that only an overlay could supply except secrets.

#### Scenario: An overlay is a readable diff from the base

- **WHEN** an overlay is rendered and compared to the base
- **THEN** the overlay contributes only its environment's differences (replica counts, resource sizes, secret
  backend, egress target)
- **AND** the rendered manifest is what actually applies, with no templating indirection hiding it.

### Requirement: Every workload SHALL declare liveness and readiness probes, resource limits, and a bounded rolling-update policy

Every workload SHALL declare a **liveness** and a **readiness** probe that read a component **health endpoint**
(never a UI), **resource requests and limits**, and a **rolling-update** policy with bounded surge and
unavailability.

#### Scenario: Probes read a health endpoint

- **WHEN** a workload's manifest is inspected
- **THEN** its liveness and readiness probes target a health endpoint that reflects the component's actual state
- **AND** no probe treats a rendered UI page as the health verdict.

#### Scenario: A rollout halts on a real readiness signal

- **WHEN** a new revision's pod fails its readiness probe
- **THEN** the rolling update halts within its bounded unavailability rather than replacing healthy pods with
  broken ones
- **AND** the workload declares resource requests and limits.

### Requirement: Any workload with replicas greater than one SHALL declare a PodDisruptionBudget

Any workload with replicas > 1 SHALL declare a **PodDisruptionBudget** such that a voluntary disruption (a node
drain) cannot remove the last available replica of a control- or data-plane service.

#### Scenario: A node drain cannot remove the last replica

- **WHEN** a node hosting a replica of a multi-replica service is drained
- **THEN** the PodDisruptionBudget prevents eviction that would drop below the minimum available
- **AND** the service stays available through the drain.

### Requirement: A NetworkPolicy SHALL encode control/data-plane separation and make egress an allowlist

A **NetworkPolicy** SHALL default-deny and permit only the seam flows between control and data plane, and SHALL
make **egress an allowlist**: only components that must reach an external network may, and the platform's
model-call egress is confined to the gateway's client. A bare, unrestricted egress is non-conformant.

#### Scenario: Default-deny is verified by a blocked probe

- **WHEN** a pod attempts a connection the policy does not permit
- **THEN** the connection is denied
- **AND** the denial is verifiable by a test probe, not assumed.

#### Scenario: Egress is a constructed allowlist, not a filtered denylist

- **WHEN** the egress policy is inspected
- **THEN** it permits only named destinations, and the model-call egress is confined to the gateway's client
- **AND** a component not on the allowlist attempting to egress is blocked, so adding a new field or client
  fails toward omission rather than silently leaking.

### Requirement: All images SHALL be referenced by digest

Every image in the base and every overlay SHALL be referenced by **digest**, not a floating tag, so an apply is
reproducible.

#### Scenario: A mutable tag fails the lint

- **WHEN** an overlay references an image by a mutable tag instead of a digest
- **THEN** the mutable-tag lint fails the build
- **AND** the same digest applied twice yields the same running image.

### Requirement: Secrets SHALL be referenced via an external-secret mechanism, never a committed plaintext Secret

Secrets SHALL be referenced through an external-secret mechanism (external-secrets operator / CSI Secret Store
driver / a sealed reference), and a plaintext `Secret` manifest SHALL NOT be committed to the repository.

#### Scenario: A committed plaintext Secret fails CI

- **WHEN** a plaintext `Secret` manifest is committed
- **THEN** the secret-scan lint fails CI
- **AND** the intended path — an external-secret reference resolved at apply time from the operator's store — is
  the only one that passes.

#### Scenario: No credential appears in cluster-readable output

- **WHEN** an operator runs `kubectl get` against the deployed secrets
- **THEN** no platform credential value is exposed in the manifest tree that produced them
- **AND** the value lives only in the operator's external store.
