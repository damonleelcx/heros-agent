# Platform LLM Access — Spec Delta (P19)

Product rationale: [`../../../../docs/prd/P19-deployment-delivery.md`](../../../../docs/prd/P19-deployment-delivery.md)
§6 (FR12–FR15) and §7. Architecture decisions: [`../../design.md`](../../design.md) Decision 4. Inherits
[ADR-002](../../../../docs/adr/ADR-002-provider-gateway-serves-platform-callers.md) (gateway serves platform
callers), [ADR-004](../../../../docs/adr/ADR-004-runtime-config-binding.md) (fail-static), and
[`secrets-baseline.md`](../../../../docs/decisions/secrets-baseline.md) §1.1.

Covers the **deployment posture** for the platform's own LLM-using stages (eval P4, attribution/diagnosis
P4.5, verification P5.5, optimizer P6): credentials from a secret store through the `providergateway.Secrets`
seam with **no bootstrap secret**, `/readyz` reporting the live source and failing **closed**, **egress
confined** to the gateway's client, the path kept **platform-internal** (never in a customer production path),
and an **air-gapped** on-prem-gateway target that fails **static** when unreachable.

> This capability is a *deployment* posture, not a new call path. The seam already exists
> (`internal/providergateway`, `HEROS_SECRETS_SOURCE`); P19 says how it is wired, confined, and reported in a
> real deployment — and enforces the ADR-002 invariant that the platform is never in a customer's production
> request path.

## ADDED Requirements

### Requirement: Platform model-calling stages SHALL obtain provider credentials only through the Secrets seam, with no bootstrap secret

The stages that call models (P4/P4.5/P5.5/P6) SHALL resolve provider credentials **only** through the
`providergateway.Secrets` seam selected by `HEROS_SECRETS_SOURCE`, and the deploy SHALL wire that seam to a
secret store authenticated by an **ambient identity** (a workload identity / instance role), so **no bootstrap
secret** exists in any manifest.

#### Scenario: No bootstrap secret in the manifest

- **WHEN** the deployment manifests are inspected
- **THEN** no provider credential and no bootstrap secret for the secret store appears in them
- **AND** the secret store is reached with an ambient workload identity, not a credential shipped in the deploy.

#### Scenario: Credentials resolve at call time and are not persisted

- **WHEN** a model-calling stage needs a provider credential
- **THEN** it resolves the credential through the `Secrets` seam at call time
- **AND** the credential is not written to disk, a log, a trace attribute, or a client bundle.

### Requirement: /readyz SHALL report the live secret source and SHALL fail closed on an unresolvable credential

`/readyz` SHALL report the **live secret source** as `secrets_source: {kind, detail}`, and SHALL report
**not ready** when a required provider credential is unresolvable — **fail-closed**, with no environment
fallback from an external store.

#### Scenario: The live secret source is externally readable

- **WHEN** an operator reads `/readyz`
- **THEN** it reports the active `secrets_source` kind and a non-sensitive detail
- **AND** the claim that the model path is provisioned is checkable at an endpoint, not inferred from a log line.

#### Scenario: An unresolvable required credential fails closed

- **WHEN** a required provider credential cannot be resolved from the configured external store
- **THEN** `/readyz` reports not ready and names the secret source as degraded
- **AND** the platform does **not** silently fall back to an environment credential.

### Requirement: No deploy artifact SHALL place the platform in a customer's production request path

The internal LLM-call path SHALL remain **platform-internal** — the customer's transformed program calls its
own providers directly (ADR-002) — and no deploy artifact SHALL introduce a runtime dependency of customer
production traffic on platform uptime.

#### Scenario: The customer's runtime does not depend on platform uptime

- **WHEN** the platform's model-calling stages are unavailable
- **THEN** a customer's transformed program continues calling its own providers unaffected
- **AND** no overlay or manifest routes customer production traffic through the platform.

#### Scenario: Model-call egress is confined to the gateway's client

- **WHEN** the egress policy is inspected
- **THEN** only the gateway's client may reach a provider endpoint
- **AND** no other data-plane component holds an unconfined route to a provider.

### Requirement: In an air-gapped deployment the seam SHALL target an on-prem gateway and fail static when unreachable

In an air-gapped deployment with no public egress, the LLM-access seam SHALL be pointable at an operator-
provided **on-prem gateway** through configuration, and SHALL fail **loud and static** (last-known-good
retained, degraded reported) when it is unreachable — never fail-open, never a startup dependency.

#### Scenario: The air-gapped overlay targets an on-prem gateway

- **WHEN** the air-gapped overlay is applied with an on-prem gateway endpoint configured
- **THEN** the model-calling stages reach providers only through that endpoint
- **AND** no stage attempts a public-internet egress.

#### Scenario: An unreachable on-prem gateway degrades, it does not crash

- **WHEN** the configured on-prem gateway is unreachable
- **THEN** the model-dependent stages report **degraded-not-available** on `/readyz` and retain last-known-good
- **AND** the rest of the platform (discovery, apply, run, eval-without-judge) stays fully functional
- **AND** the seam is never a startup dependency that prevents the platform from booting.
