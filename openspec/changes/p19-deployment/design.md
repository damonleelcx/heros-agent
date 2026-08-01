# Design — P19: Deployment & Delivery

Product rationale: [`../../../docs/prd/P19-deployment-delivery.md`](../../../docs/prd/P19-deployment-delivery.md).
Inherits [ADR-002](../../../docs/adr/ADR-002-provider-gateway-serves-platform-callers.md) (gateway serves
platform callers), [ADR-004](../../../docs/adr/ADR-004-runtime-config-binding.md) (fail-static config),
[ADR-006](../../../docs/adr/ADR-006-console-deploy-packaging.md) (a dead process is a dead container),
[ADR-008](../../../docs/adr/ADR-008-console-tenant-identity-seam.md) (tenant identity seam), and
[`secrets-baseline.md`](../../../docs/decisions/secrets-baseline.md).

Every decision below is arbitrated on the **八级法则** — the single trade-off law this project uses:

> **安全 > 稳定 > UX > 运维 > 可演进 > 可扩展 > 维护 > 实现**

with its three iron laws: (L1) a higher level's degradation is never traded for a lower level's
convenience; (L2) decide at the highest level that separates the options and do not fall back down for a
lower-level convenience; (L3) 实现 (single-shot implementation cost) is always the floor and never
outranks anything above it.

## Context

P19 is downstream of everything. It adds no component and no statistic; it **composes** the P0–P18
components and expresses that composition on two substrates. What already exists (and does not change): the
customer-console two-container unit (ADR-006), the sandbox/discovery least-privilege one-shots, the
NATS/Qdrant/Neo4j backing stack, and the `providergateway.Secrets` seam + `secrets-baseline.md`. What is
absent and P19-shaped: any whole-platform composition, any Kubernetes delivery, any deploy artifact for the
operator console, and any deployment posture for internal LLM access or air-gapped delivery.

Three properties from the rulebook are non-negotiable and shape every decision: **health is an externally
readable, aggregated endpoint, never a dashboard**; **secrets never appear in git / manifest / log / trace /
bundle**; and **upgrade is a separate axis from fresh install, with user-state preserved and rollback by
re-apply**.

## Decision 1 — Kustomize base + overlays, not Helm

**Chosen:** a Kustomize `base/` (a complete applyable description of the platform) plus
`overlays/{dev,staging,prod,airgapped}`, each overlay expressing only its differences.

**Why (L5 可演进 / L4 运维 over L8 实现).** Helm is the more common tool and the marginally cheaper first
write (L8), which is exactly why L3 says that cannot decide it. The property that matters is that a
private-deploy customer — and our own reviewer — can read *what actually applies*. Helm's templating puts the
applied manifest behind values, conditionals and named templates, so the artifact you audit is not the
artifact that runs; Kustomize's overlay output *is* the manifest, `kubectl kustomize` renders it verbatim,
and a diff between two overlays is a real diff. This is the same posture the repository already took with
`${VAR:?}` compose (refuse to start over start-misconfigured) and with "explicit config over magic". For a
2B/air-gapped customer who must satisfy their own security review of our manifests, readable-and-diffable is a
可演进/运维 property, not a taste.

**Rejected — Helm.** Reopens only if a customer's platform *requires* a Helm chart to fit their internal
delivery (some do). The answer then is a chart that packages the Kustomize base as a thin wrapper, not a
re-templating of the whole tree — the readable base stays the source of truth.

## Decision 2 — One image set, two substrates

**Chosen:** Docker Compose and Kustomize reference the **same digest-pinned images**; the topology is
expressed twice, the artifacts built once.

**Why (L5).** Two substrates that build their own images can skew — a bug reproduced on compose that does not
reproduce on Kubernetes because the images differ is a class of failure that costs days. Pinning both to the
same digest makes "it works on compose but not the cluster" a *topology* question, never an *image* question.
A CI check asserts the two substrates reference the same digest set and the same env-var contract.

**Rejected — substrate-specific images** (e.g. a "k8s-optimized" image). The optimization is illusory; the
skew is real.

## Decision 3 — Control/data-plane separation is a NetworkPolicy, not a diagram

**Chosen:** the compile-time no-cross-import discipline (control-plane code does not import data-plane code
and vice-versa) gets a **runtime** twin — a NetworkPolicy that default-denies and only allows the flows the
seam permits (policy/config/token down, audit/cost/trace up), so the data plane keeps serving already-
established work with the control plane unreachable.

**Why (L1 安全 / L2 稳定).** The System Designer discipline says the import ban is the *proof* the two could be
split into two processes; a deployment that then lets every pod talk to every pod has erased that proof at
runtime. The test — "pull the control plane, does the data plane still serve?" — must have a *yes* answer by
construction, because a control plane that is a hard dependency of the data plane is a single point of failure
wearing a separation diagram. Egress is part of this policy (Decision 4).

**Rejected — a flat network with the separation "enforced by convention".** Convention is not a runtime fact;
the first service that reaches across the seam does so silently.

## Decision 4 — Internal LLM access stays platform-internal and egress-confined

**Chosen:** the model-calling stages resolve credentials only through the `providergateway.Secrets` seam
(`HEROS_SECRETS_SOURCE`), wired to a store with an **ambient identity** (IRSA / workload identity) so there is
no bootstrap secret in the manifest; `/readyz` reports the live `secrets_source`; egress to providers is an
**allowlist confined to the gateway's client**; and no artifact places the platform in a customer's production
request path.

**Why (L1 安全 / L2 稳定).** Two invariants converge here. Security: a bootstrap secret in a manifest is a
plaintext credential in git the moment the manifest is committed — an irreversible one-way door — so the store
must authenticate ambiently and the seam must fail *closed* with no env fallback. Stability: ADR-002 already
established that the customer's transformed program calls its *own* providers, so the platform's model calls
are internal (eval/diagnosis/verify/optimizer) and a diagnosis run failing must never touch customer
production traffic; the deploy makes that physical by confining egress to the gateway's one client rather than
opening the internet to the whole data plane. A bare `http.Client{}` to the internet, or a NetworkPolicy with
open egress, is non-conformant — egress is a **constructed allowlist**, never a filtered denylist, because a
denylist fails silently when a field is added while an allowlist fails toward omission.

**Air-gapped variant.** With no public egress, the seam is pointed at an operator-provided on-prem gateway via
configuration; when that is unreachable the stages fail **static** (last-known-good retained, degraded
reported on `/readyz`) per ADR-004 — never fail-open, never a startup dependency. A customer with *no* model
access at all keeps discovery/apply/run/eval-without-judge fully functional; only the model-dependent stages
report degraded-not-available (see PRD Q5).

**Rejected — a per-language secret injection into each SDK, or a global `HTTPS_PROXY`.** The audit/cost/egress
boundary belongs at the gateway, not smeared across every caller; a global proxy env pulls *every* client onto
the egress lane, which is the opposite of confinement.

## Decision 5 — The operator console is a second origin, second unit

**Chosen:** the P8 console deploys as its own two-container unit (admin BFF `:4310` + admin API `:4311`) on
its **own origin**, disjoint cookie jar, own BFF and credential, unreachable from the customer console —
inheriting ADR-006's two-container packaging.

**Why (L1 安全).** P8 Decision 11 already ruled that operator/customer isolation is enforced by the **browser
origin boundary**, not by routing inside one app — a role-gated section of the customer console would put
cross-tenant operator capability one authorization bug away from a customer session. The deploy must therefore
give the operator console a *different origin*, which means a *different deployment unit*; folding it into the
P9 unit "for convenience" (one fewer image) is precisely the L8-over-L1 inversion L3 forbids. It inherits
ADR-006 rather than reopening it: two containers, each independently probed and restarted, no supervisor,
because a dead BFF must be a dead container the orchestrator can see.

**Rejected — one console app with an `/admin` route tree.** Rejected at L1 by P8 Decision 11; recorded here so
this design is not read as reopening it.

## Decision 6 — Single-Postgres SPOF is accepted *because* backup ships

**Chosen:** Postgres is deployed as a single stateful instance (single-writer, no split-brain), documented as
a **single point of failure** with its blast radius; the backup CronJob and restore procedure **ship with the
deploy**, and the dump **fails loud** on an empty/failed backup rather than writing a zero-byte file.

**Why (L2 稳定).** The arbitration is explicit and conditional: "accept single Postgres + a short recovery
window" is a sound L2 call *only if* "disaster recovery from backup is real". If backup does not ship, RPO is
infinite and the premise of the decision is false — so the backup is not a follow-up, it is the thing that
makes the decision valid. The zero-byte-dump trap is called out because it is a real failure mode: a
`pg_dump` that fails after the `>` has already created the file leaves a plausible-looking empty backup that
silently poisons the rollback chain; every failure path in the dump deletes the empty file. The stateless data
plane scales horizontally (Decision 8 of the PRD); the stateful store stays a documented single point whose
availability rests on restart + backup.

**Evolution path (not in M15).** When a customer requires zero-window failover, the decoupled, stateless
service layer connects to standard Postgres HA (Patroni) without a service-code change — the door is left open,
not walked through now.

**Rejected — claiming uniform HA, or shipping single-Postgres without backup.** The first is a false promise
(NFR5 honesty); the second voids its own premise.

## Decision 7 — Upgrade is declarative-idempotent; rollback is re-apply

**Chosen:** deployment is declarative and idempotent (apply twice → same state); **upgrade = apply a new
package with the same command**; there is no bespoke teardown; **rollback = unpack and re-apply the prior
package**; user/tenant state survives the version change.

**Why (L2 稳定 / L4 运维).** A private-deploy customer only ever runs the installer/apply — never our test
suite — so upgrade correctness has to be a property of the apply, not of a CI job they cannot run. Making
upgrade "the same command with a newer package" and rollback "the same command with the older package" gives
one operational verb for both directions, which is what an air-gapped operator can actually execute without
us. User-state fields (tenant credentials, session secret, admin identity map) are preserved byte-for-byte
because they were "first-install determined"; system-derived state may be re-rendered but must regenerate from
user-state + templates. Rollback never falls back to a *same-version* backup — that poisons the chain — and is
non-destructive to legitimate data produced during the upgrade window.

**Rejected — a bespoke upgrade/teardown script per version.** Every version-specific teardown is a new,
untested one-way door; declarative-idempotent apply is the same code path every time.

## Decision 8 — TSDB / span-store / object-store are bring-your-own, OTel-compatible

**Chosen:** the telemetry substrate stays **OTel-compatible and bring-your-own**; `base` assumes an operator-
attached collector; a clearly-labelled *optional* overlay bundles a minimal stack for the evaluator; the
object store is MinIO in the bundled overlay or a filesystem-backed content store for the smallest footprint
(PRD Q2/Q3).

**Why (L4 运维).** A private-deploy customer should not inherit a Prometheus/Grafana/Tempo operational burden
they did not choose — every extra component is a deployment, storage and upgrade surface in a machine room we
will never touch. The platform exposes OTel/JSON endpoints and lets the operator point their own collector at
it; the concrete TSDB/span-store product is still OQ1 in `storage-decision-record.md` and P19 deliberately does
not close it. This is the "留好接口，客户要接自己包 exporter" posture.

**Rejected — hard-wiring Prometheus/Grafana into `base`.** Forces an operational burden and prematurely closes
OQ1.

## Decision 9 — The deployed process owns the schema, and `/readyz` probes what it names

**Chosen:** `internal/launch` opens the Postgres the deployment declares, applies `db/migrations/postgres/`
at boot through a ledger it **reads**, and `/readyz` probes that database under the name `postgres`. The
SQLite ledger keeps its own honest name.

**Why (L2 稳定 / L1 安全).** The artifacts written first assumed a composition that the boot path never
performed: the manifests started Postgres, set `HEROS_DATASTORE_NAME=postgres`, and the process opened
SQLite and pinged *that* — so `/readyz` reported `components.postgres: ready` for a database it had never
connected to, and would have kept reporting it while that database was down. That is not a documentation
defect; it is a health signal that is structurally incapable of failing, which is the one thing
`health-signal-surface` forbids outright. The same gap left the platform's nineteen migrations applied by
nothing except a demo binary reading files from a relative path with no ledger, so "upgrade preserves user
state" (D7) had no mechanism behind it at all.

The migration ledger is **read**, not merely written, because a write-only ledger cannot distinguish "never
applied" from "applied and since reverted" — it re-runs either way, and the DDL here is bare `CREATE TABLE`
with no `IF NOT EXISTS`, so a re-run is an error rather than a no-op. Idempotence therefore has to come from
the ledger; it cannot be borrowed from the DDL's tolerance.

**Rejected — keeping `HEROS_DATASTORE_NAME` as a display name.** It is cheaper (L8) and it is exactly the
inversion L3 forbids: it buys tidy output by making the readiness signal lie.

**Rejected — teaching `/readyz` to probe Postgres without connecting the stores to it.** That fixes the
signal and leaves the platform unwired. The probe should be true *because* the dependency is real.

## Decision 10 — Every capability is registered; an unsourced one answers 503, never 404

**Chosen:** `internal/launch` registers every capability surface the platform ships. Where a DB-backed store
exists it is constructed and mounted for real; where none exists the surface is registered with a **nil
source**, so it answers 503 with a not-mounted body.

**Why (L3 UX / L2 稳定).** An unregistered route is not a neutral absence — it is a **404**, and 404 already
means something else. The customer console calls `/api/p2`, `/api/p10`, `/api/p12`, `/api/p21` and
`/api/p25`; on a fresh install every one of them fell through to 404, which the console renders as *"No such
workflow"* for a workflow that plainly exists. The operator's next action for "this capability is not
installed on this deployment" (deploy the capability, or accept the boundary) and for "that identifier does
not resolve" (check the id) are completely different, and collapsing them sends every operator down the
wrong one. `cmd/proof/customerconsole` had already discovered this and mounted its unsourced subsystems with
nil for exactly this reason — the deployment simply never inherited the lesson.

This decision does **not** claim the unsourced capabilities work. Six read surfaces (P4 board, P4.5
scorecard, P5 IR, P5.5 proposals, P6 optimizer monitor, P3.5 pattern graph) have no persistent adapter
anywhere outside a demo binary; P19 registers them honestly and records them as PRD Q6 rather than inventing
a store for them, because inventing one would be a product decision wearing a deployment label.

**Rejected — mounting only what has a source and leaving the rest unregistered.** Cheapest (L8), and it is
the state that produced the misleading 404.

**Rejected — a capability-list flag that half-enables an endpoint.** `MountBillingWebhook`'s own comment
already rules this out: a deployment that does not expose a path simply does not mount it; there is no flag
that half-enables one.

## Interfaces sketch

```
deploy/
  docker-compose.platform.yml        # whole-platform, single-host / open-core; one /readyz truth
  docker-compose.admin-console.yml   # operator console, second origin (ADR-006 shape)
  Dockerfile.admin-console           # inherits Dockerfile.console posture: pinned node:22, npm ci, read_only
  .env.platform.example              # NO SECRETS; ${VAR:?} contract mirrored from .env.console.example
  .env.admin-console.example         # NO SECRETS
  k8s/
    base/                            # complete applyable description
      agentd.yaml postgres.yaml objectstore.yaml queue.yaml
      console.yaml admin-console.yaml networkpolicy.yaml ...
      kustomization.yaml
    overlays/
      dev/  staging/  prod/  airgapped/   # each: only its differences
  README.md                          # self-contained runbook (NFR8): terms defined, capacity as ranges+config
```

`/readyz` (extended, `internal/api/server.go`):

```json
{
  "status": "not_ready",
  "components": {
    "postgres":       {"status": "ready"},
    "object_store":   {"status": "ready"},
    "queue":          {"status": "ready"},
    "customer_console": {"status": "ready", "url": "http://console:4320/api/health"},
    "admin_console":  {"status": "not_ready", "url": "http://admin-console:4310/api/health"},
    "secrets_source": {"status": "ready", "kind": "aws-secrets-manager", "detail": "prefix=heros/prod/"}
  },
  "degraded": ["admin_console"]
}
```

## Risks

- **Two substrates drift** → one image set (D2) + a CI digest/env-contract equality check + the
  deployment-form impact matrix on every PR.
- **A secret reaches git** → external-secret references only (D4/kubernetes-delivery); a lint that fails on a
  committed plaintext `Secret`; the bundle scan the console build already runs.
- **Single-Postgres data loss** → D6: backup ships, dump fails loud, SPOF documented, Patroni path stated.
- **Egress opened too wide, or platform put in customer path** → D4 allowlist + ADR-002 invariant + a review
  rule that egress is constructed not filtered.
- **Admin console folded into customer origin** → D5 + P8 Decision 11 + a test asserting cross-origin
  unreachability.
- **Air-gapped customer cannot self-serve** → the self-service acceptance test is the pass/fail judge (NFR7);
  package carries binaries + images + checksums + `doctor` + backup/restore.
