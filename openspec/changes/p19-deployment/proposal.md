## Why

The platform can be built, and — in fragments — run, but it cannot be *stood up*. `deploy/` holds a
two-container customer-console unit ([`docker-compose.console.yml`](../../../deploy/docker-compose.console.yml),
governed by [ADR-006](../../../docs/adr/ADR-006-console-deploy-packaging.md)), two least-privilege
one-shot jobs (the P1 discovery worker, the P3 sandbox isolate), and a backing-services stack
(NATS / Qdrant / Neo4j) that is wired to nothing. There is **no artifact that composes the platform** —
the Go service together with Postgres (eval + lineage), the object store, the telemetry substrate
(P2.5), the queue, the vector/graph stores, and the customer console, under one readiness truth and one
secret contract. And there is **no Kubernetes anything at all**: a repository-wide search for
kube/kustomize/helm/manifest returns zero, so the managed and enterprise customers — who run Kubernetes —
have nothing to apply. This is the DevOps first principle inverted: today we deliver "it runs on my
machine", not "anyone who receives it can run it".

Three further gaps are structural. The **P8 operator console has no deploy artifact whatsoever**
([`web/admin-console/package.json`](../../../web/admin-console/package.json) can only be started by hand
with `npm run start --port 4310`), yet it must live on its **own origin**, isolated from the customer
console by the browser's origin boundary rather than by routing (P8 Decision 11,
[ADR-008](../../../docs/adr/ADR-008-console-tenant-identity-seam.md)) — so it cannot be folded into the
P9 unit. The **platform's internal LLM access** — the eval, diagnosis, verification and optimizer stages
call providers through the platform gateway ([ADR-002](../../../docs/adr/ADR-002-provider-gateway-serves-platform-callers.md)),
resolving credentials through a `providergateway.Secrets` seam selected by `HEROS_SECRETS_SOURCE`
([`secrets-baseline.md`](../../../docs/decisions/secrets-baseline.md) §1.1) — has a code seam but no
*deployment* posture: nothing says how the secret store is mounted, how egress is confined to the one
client that needs it, or how an air-gapped operator points those stages at an on-prem gateway. And there
is **no air-gapped / private-deploy story** — no self-contained package, no declarative-idempotent apply,
no rollback-by-re-apply, and none of the honest commercial boundary ("one deployment = one tenant
boundary via deployment-level isolation, not software multi-tenancy") that the private-deploy motion
requires be told plainly.

P19 closes all four as **specification and manifests** — it adds no product feature and no statistic. It
is downstream of P0–P18 and composes them: a documented control-plane/data-plane topology; a Kustomize
`base/` + `overlays/{dev,staging,prod,airgapped}` chosen over Helm so *what applies is what you can read*;
a second-origin deploy unit for the operator console that inherits ADR-006; an internal-LLM-access posture
that keeps credentials in a secret store, reports the live source on `/readyz`, confines egress to an
allowlist, and — the load-bearing invariant — **never** puts the platform in a customer's production path
(ADR-002); and the air-gapped delivery + honest boundary the private-deploy customer cannot install
without. Product rationale: [`../../../docs/prd/P19-deployment-delivery.md`](../../../docs/prd/P19-deployment-delivery.md).

## What Changes

- **New capability `deployment-topology`.** One platform deployment unit, expressed on **two substrates**
  (Docker Compose for single-host / open-core, Kubernetes for the cluster) from **one digest-pinned image
  set** and one secret contract. It separates **control plane** (policy, config, entitlement, admin) from
  **data plane** (discovery, apply, run, eval) as a *runtime* boundary, not a diagram — the data plane
  keeps serving established work with the control plane down. It declares each stateful store with a named
  volume/claim, documents **Postgres as a single point of failure whose backup automation ships with the
  deploy** (accepting the SPOF is void without the backup), makes stateless hot-path components
  horizontally scalable, and requires every secret to come from the environment or an external store in the
  `${VAR:?}` refuse-to-start form — **never** from a manifest, an env-example, a repo file, a log, a trace,
  or a bundle. It defines the **air-gapped package** (binaries + `docker save` images + manifests +
  checksums), makes deployment **declarative-idempotent** (upgrade = apply a new package with the same
  command; no teardown), makes **upgrade preserve user/tenant state** and **rollback = re-apply the prior
  package** (never a same-version fallback), and ships a **`doctor` preflight + backup/restore** whose dump
  **fails loud** on empty rather than writing a rollback-poisoning zero-byte file.
- **New capability `kubernetes-delivery`.** A **Kustomize `base/` + `overlays/{dev,staging,prod,airgapped}`**;
  the base is a complete applyable description and each overlay expresses only its differences. Every
  workload declares **liveness + readiness probes reading a health endpoint** (not a UI), **resource
  requests/limits**, and a **bounded rolling-update** policy; any workload with replicas > 1 declares a
  **PodDisruptionBudget** so a node drain cannot remove the last replica; a **NetworkPolicy** encodes the
  control/data-plane separation and makes **egress an allowlist** (the platform's model-call egress confined
  to the gateway's client; a bare unrestricted egress is non-conformant); all images are referenced by
  **digest**, not a floating tag; and secrets are referenced via an **external-secret mechanism**, never a
  committed plaintext `Secret`.
- **New capability `platform-llm-access`.** The model-calling stages (P4/P4.5/P5.5/P6) obtain provider
  credentials **only** through the `providergateway.Secrets` seam, selected by `HEROS_SECRETS_SOURCE`, wired
  to a secret store with an **ambient identity** so there is **no bootstrap secret** in the manifest.
  `/readyz` reports the live `secrets_source` and fails **closed** (no env fallback) when a required
  credential is unresolvable. **No artifact places the platform in a customer's production request path** —
  the path is platform-internal, the customer's transformed program calls its own providers (ADR-002). In an
  **air-gapped** deployment the seam is pointable at an operator-provided on-prem gateway and fails **loud and
  static** (last-known-good retained, degraded reported) when unreachable — never fail-open, never a startup
  dependency ([ADR-004](../../../docs/adr/ADR-004-runtime-config-binding.md)).
- **New capability `admin-console-deploy`.** The P8 operator console ships as a **two-container unit on its
  own origin** (admin BFF `:4310` + platform/admin API `:4311`), inheriting ADR-006 (a dead process is a
  dead container; no in-container supervisor). It is **unreachable from the customer console's origin** —
  separate origin, **disjoint cookie jar**, separate BFF and credential — and refuses to start in production
  under a `dev` identity seam (ADR-008). Its health is aggregated by the platform `/readyz` the same way the
  customer console's is.

- **Extended 2026-08-01 — `deployment-topology` gains capability carriage.** The artifacts above compose the
  platform on paper; an audit against the process they run found the composition unperformed. `internal/launch`
  registered six routes, mounted **no** capability surface, applied **none** of the nineteen Postgres
  migrations in [`db/migrations/postgres/`](../../../db/migrations/postgres/), and opened no Postgres
  connection — while the manifests started Postgres and set `HEROS_DATASTORE_NAME=postgres`, which only
  *renames* the SQLite ledger's ping, so `/readyz` reported a database it had never connected to. The customer
  console's `/api/p2`, `/api/p10`, `/api/p12`, `/api/p21` and `/api/p25` calls all fell through to **404**,
  which it renders as *"No such workflow"*. Meanwhile every capability that landed after these artifacts were
  written — P20 packages, P21 payments, P22 SSO, P23 legal, P24 analytics/error monitoring, P26 operator
  surfaces — had **no deployment contract at all**: no `STRIPE_*`, no DSN, no `HEROS_ERROR_REPORTING_DSN`
  (which can *refuse the boot* when set and unusable), no consent-gate or content-root, and no scheduled unit
  for the seven-year consent-retention job. The change now also: applies the schema at boot through a ledger it
  **reads**; probes the store it names; **registers every capability surface**, DB-backed where a store exists
  and nil-sourced where none does so the answer is **503 not-mounted** rather than 404; extends the environment
  contract to every variable the deployed processes read and **gates the two substrates against diverging on
  it** (the half of Decision 2 `check-image-parity.sh` never covered); ships the retention job; and corrects
  the Kubernetes `agentd` from `replicas: 2` over a per-pod `emptyDir` — two divergent SQLite ledgers, lost on
  every rollout — to one replica on a persistent claim. Decisions 9 and 10 in `design.md`; PRD FR25–FR32.

**No breaking changes to existing artifacts.** The console unit, the sandbox/discovery one-shots, and the
enterprise backing-services stack are unchanged; P19 adds a composing layer above them and a second
substrate beside them.

## Impact

- **Affected capabilities:** new — `deployment-topology`, `kubernetes-delivery`, `platform-llm-access`,
  `admin-console-deploy`. Consumes (does not modify) `console-bff`, `web-console`, `admin-console-surface`,
  the P0 storage/lineage schema, P2.5 telemetry, P7 entitlement.
- **Affected code / systems:** `deploy/` (new `docker-compose.platform.yml`, `Dockerfile.admin-console`,
  `docker-compose.admin-console.yml`, `.env.platform.example`, `.env.admin-console.example`, and a new
  `deploy/k8s/{base,overlays}` Kustomize tree); `internal/api/server.go` `/readyz` aggregation extended to
  the admin console and the secret source; `internal/launch` boot wiring for the seam; docs runbook under
  `docs/release/` or `deploy/README.md`.
- **Dependencies:** upstream — P0 (schema/migrations), P2 (gateway), P2.5 (telemetry), P7 (entitlement,
  plans-by-name), P8 (operator console), P9 (customer console), ADR-002/004/006/007/008. Unblocks — M15 GA
  and private-deploy readiness, the air-gapped enterprise motion, a future release pipeline that publishes
  the digest-pinned images this change consumes, and a future Postgres-HA (Patroni) evolution overlay.
- **Explicitly out of scope (owned elsewhere):** the concrete TSDB/span-store product (OQ1, kept
  OTel-compatible + BYO); Helm; a hosted-SaaS operations runbook; the CI/CD release state machine and
  provenance fence; self-hosted open-model serving (out by the platform's own "never resell tokens"
  principle).
