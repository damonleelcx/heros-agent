# PRD — P19: Deployment & Delivery (the platform as a thing you can stand up)

| Field | Value |
|---|---|
| Phase / Milestone | P19 / M15 (cross-cutting; GA & private-deploy gate) |
| Target window | Hardens from P2.5 onward; the deliverable is GA-gating and lands as a wave alongside P7/P8/P9 |
| Lead role(s) | DevOps Engineer + System Designer (co-leads) |
| Supporting role(s) | Backend, Frontend, AI Engineer, QA Engineer, Product Designer, Sales Operations |
| Status | Draft |
| OpenSpec change | `p19-deployment` |

> **Scope discipline.** P19 owns **how the platform is deployed** — the composition, the container
> images, the Kubernetes delivery, the internal LLM-access posture, and the second console's deploy
> unit. It does **not** re-specify what any subsystem *does*; every behavior is owned by its phase
> (P0–P18). P19 is downstream of all of them. It adds **no product feature** and **no statistic** — it
> makes the specified system installable, operable, upgradable, and — for the private-deploy and
> air-gapped customer — deliverable to a machine we will never see.

> **The one-sentence job.** *Deliver "anyone who receives it can run it", not "it runs on my
> machine"* — the DevOps first principle this phase is organized around
> ([senior-devops-engineer-workflow](../../../aikeylabs-skills/senior-devops-engineer-workflow/SKILL.md)).

## 1. Summary

The repository can already be *built* and, in pieces, *run*: `deploy/` holds a two-container customer
console unit ([`docker-compose.console.yml`](../../deploy/docker-compose.console.yml), governed by
[ADR-006](../adr/ADR-006-console-deploy-packaging.md)), two least-privilege one-shot jobs (the P1
discovery worker and the P3 sandbox isolate), and a backing-services stack (NATS, Qdrant, Neo4j). What
it does **not** have is a way to stand up *the whole platform* as one coherent, versioned, operable
system — and it has **no Kubernetes anything at all** (`find` for kube/kustomize/helm/manifest returns
zero). Three concrete surfaces are simply missing: a **unified deployment topology** that brings the Go
service, its stateful stores, its telemetry substrate, and the customer console up together with one
readiness truth; a **Kubernetes delivery** for the customers who run one; and a **deploy artifact for
the P8 operator console**, which today can only be started by hand with `npm` and yet must live on its
own origin ([P8 Decision 11](../../openspec/changes/p8-admin-console/design.md)). A fourth surface is
under-specified: **how the platform's own LLM-using components get model access** in a real cluster —
the diagnosis, eval, verification and optimizer stages call providers through the platform gateway
([ADR-002](../adr/ADR-002-provider-gateway-serves-platform-callers.md)), and that credential path has a
secrets baseline ([`secrets-baseline.md`](../decisions/secrets-baseline.md)) but no deployment story.

P19 delivers all four as **specification and manifests, not a new runtime**: a documented
control-plane/data-plane topology; a **Kustomize base + overlays** delivery (`dev` / `staging` / `prod`
/ `airgapped`) — chosen over Helm deliberately (§8, D1) so that *what applies is what you can read*,
matching the repository's existing `${VAR:?}`-refuses-to-start compose posture; a two-container,
separate-origin deploy unit for the **admin console** that inherits ADR-006 rather than reopening it;
and an **internal-LLM-access** posture that keeps provider credentials in a secret store, reports the
live source on `/readyz`, and — the load-bearing invariant — **never** puts the platform in a
customer's production request path (ADR-002) and **never** lets a key reach a browser or a log. On top
of that it writes the two things a private-deploy customer cannot install without: an **air-gapped
delivery** posture (self-contained package, declarative-idempotent apply, rollback = re-apply the prior
package) and the **honest commercial boundary** (§9, Sales lens) between the self-hostable open core and
the managed/enterprise surfaces. **M15 — deployable** means an engineer who has never seen this system
can, from the package and the docs alone, stand it up on Docker **or** Kubernetes, watch one readiness
endpoint tell the truth about every component, upgrade it without losing tenant state, and roll it back
— without asking us.

## 2. Problem & context

Everything above P19 assumes a running platform; nothing owns making it run reproducibly. Six problems
block "deployable", and each maps to a design commitment rather than a script.

- **🔴 There is no whole-platform deployment, only fragments.** `deploy/` has five compose files, and
  none of them composes *the platform*. `docker-compose.console.yml` brings up `agentd` + `console` but
  treats `agentd`'s image as an opaque `${AGENTD_IMAGE:?}` and stands up **none** of the stores that
  image needs at runtime — Postgres (eval results + lineage, per
  [`storage-decision-record.md`](../decisions/storage-decision-record.md)), the object store (content-
  addressed blobs), the telemetry substrate (P2.5), and the queue. `docker-compose.enterprise.yml`
  stands up NATS/Qdrant/Neo4j but wires them to nothing. A new operator has no single artifact that says
  *this is the platform*, which means the first thing every deployment does is reinvent the wiring — the
  precise failure the first principle names.
- **🔴 There is no Kubernetes delivery, and compose cannot express what a cluster operator needs.**
  Restart policy, rolling upgrade with surge/unavailable bounds, a **PodDisruptionBudget** so a node
  drain does not take the last replica, a **NetworkPolicy** that makes control-plane/data-plane
  separation a *runtime* fact and egress an *allowlist*, horizontal scaling of the stateless hot path,
  and secrets sourced from an external store rather than a file — none of these have a compose
  expression, and the customers who buy the managed and enterprise tiers run Kubernetes. Shipping only
  compose is shipping only the demo substrate.
- **🔴 The operator console has no deploy artifact at all.** `web/admin-console`
  ([`package.json`](../../web/admin-console/package.json): `heros-admin-console`, "a separate Next.js
  application on its own origin with its own BFF", `next start --port 4310`) can be run only by hand.
  It must be deployed on a **separate origin** from the customer console — isolation is enforced by the
  browser's origin boundary, not by routing (P8 Decision 11, [ADR-008](../adr/ADR-008-console-tenant-identity-seam.md)) —
  so it cannot simply be folded into the P9 unit. There is no Dockerfile, no compose, no image, and no
  manifest for it. A capability that exists only when a human runs `npm` by hand is, by this repo's own
  rule, a bug (`edition-awareness`).
- **The platform's internal LLM access has a baseline but no deployment posture.** The diagnosis (P4.5),
  eval (P4), verification (P5.5) and optimizer (P6) stages call models through the platform gateway
  (ADR-002), which resolves credentials through a `providergateway.Secrets` seam selected by
  `HEROS_SECRETS_SOURCE=env|aws-secrets-manager` and reports the live source on `/readyz`
  (`secrets-baseline.md` §1.1). That is a code seam; it is not yet a *deployment* — nothing says how the
  secret store is mounted in a pod, how egress is confined to the one client that needs it, or how an
  air-gapped customer with no public egress points those stages at an on-prem gateway. And the single
  most important property is unstated in deploy terms: this call path is **platform-internal** — the
  customer's transformed program calls its *own* providers directly (ADR-002), so **our uptime is never
  in a customer's production path**, and no deploy artifact may quietly make it so.
- **There is no air-gapped / private-deploy story, and it is a different product than "push to our
  cloud".** A private-deploy customer needs a **self-contained package** (binaries + `docker save`
  images + manifests + checksums), a **declarative-idempotent** apply (the operator declares the desired
  topology; upgrade = re-apply the same command with a new package), and a rollback that is **unpack the
  prior package and re-apply** — not a bespoke teardown. None of this exists, and the honest boundary it
  implies ("one deployment = one tenant boundary, by deployment-level isolation, not software multi-
  tenancy") is unwritten, which is exactly the kind of gap that becomes a support escalation after the
  sale.
- **Health, secrets, and upgrade have rules the deploy layer must physically enforce, not merely
  document.** This codebase's own history says a rule that is only written down gets violated while a
  rule that turns a light red does not. Readiness must **aggregate** every component and be **externally
  readable** — a dashboard is never the health verdict (🔴 `health-signal-surface`). Secrets must
  **never** appear in a manifest, a log, a trace attribute, or a client bundle. Upgrade is a **separate
  axis** from fresh install, user-state fields (tenant credentials, session secret, admin identity map)
  must survive it byte-for-byte, and Postgres is a documented **single point of failure** whose backup
  is the *precondition* for accepting it, not an afterthought.

## 3. Goals & non-goals

### Goals

- **G1 — One platform, one command, two substrates.** A single deployment description brings up the
  full platform (Go service + Postgres + object store + telemetry substrate + queue + vector/graph
  stores + customer console) on **Docker Compose** and on **Kubernetes (Kustomize)**, from the same
  pinned images, with the same secret contract.
- **G2 — Kubernetes delivery as base + overlays.** A Kustomize `base/` plus `overlays/{dev,staging,prod,airgapped}`,
  each component a Deployment or StatefulSet with its own liveness/readiness probe, resource requests/
  limits, a rolling-update policy, a PodDisruptionBudget where replicas > 1, a NetworkPolicy that
  encodes control-plane/data-plane separation and egress allowlisting, and secrets referenced from an
  external store — never inlined.
- **G3 — The operator console gets its own deploy unit, on its own origin.** A `Dockerfile.admin-console`
  and a compose/manifest pair that stand the P8 console up as a two-container unit (admin BFF `:4310` +
  admin API `:4311`) on a **separate origin**, with a **disjoint cookie jar** and its own BFF, unreachable
  from the P9 console by construction — inheriting ADR-006, not reopening it.
- **G4 — Internal LLM access is a deployable, confined seam.** The platform's model-calling stages get
  provider credentials from a secret store via the existing `Secrets` seam; `/readyz` reports
  `secrets_source`; egress is a declared allowlist confined to the gateway's own client; and no artifact
  places the platform in a customer's production request path or a key in a browser.
- **G5 — Health aggregates and is externally readable.** `/readyz` reports **not ready** and **names the
  degraded component** when any aggregated dependency (a store, the console, the secret source) is
  unreachable; the deploy probes read that endpoint, not a UI.
- **G6 — Upgrade and rollback are first-class and non-destructive.** Upgrade is a separate, tested axis
  from fresh install; tenant/user-state survives it; rollback is re-applying the prior package/manifest;
  Postgres SPOF is documented and its backup automation ships with the deploy, because "accept single
  Postgres" is only sound if disaster recovery from backup is real.
- **G7 — Air-gapped delivery is self-contained and self-serviceable.** A private-deploy customer can
  install (offline), operate (upgrade + backup/restore + a `doctor` check), and understand the system
  from the package and docs alone — the self-service acceptance test is the single pass/fail judge.
- **G8 — Secrets never leak on any path.** Not into git, a manifest, a log line, a trace attribute, or a
  client bundle — enforced as **build-/apply-time gates**, not review habits.
- **G9 — The commercial boundary is honest and legible.** The self-hostable open core (local execution
  layer + base control-plane modelling) is distinguished from the managed/enterprise surfaces (hosting,
  analytics, multi-tenant, audit) by **name**; no price value lives in git; and "one deployment = one
  tenant boundary via deployment-level isolation" is stated plainly, not implied as software multi-tenancy.

### Non-goals (explicitly deferred, with the owner)

- **Choosing the concrete TSDB and span-store products** — still **OQ1** in
  [`storage-decision-record.md`](../decisions/storage-decision-record.md). P19 keeps the substrate
  **OTel-compatible** and **bring-your-own**: it will not force a Prometheus/Grafana operational burden
  onto an air-gapped customer who did not ask for one; it exposes JSON/OTel endpoints and lets the
  operator attach their own collector. (Owner: P2.5 + a future decision record.)
- **Helm charts.** Deliberately not shipped (§8, D1). A future customer requirement for Helm is a
  packaging addition on top of the Kustomize base, not a re-litigation.
- **A hosted control plane / SaaS operations runbook** (multi-region, tenant provisioning automation,
  on-call rotation). P19 makes the platform deployable; running *our* SaaS instance of it is an
  operations concern owned by P7/P8 plus an internal runbook, not this PRD.
- **A CI/CD release pipeline and provenance fence.** The release **state machine**, the Makefile-routed
  fences, and the provenance gate are their own concern (P11/P12 CI + a release phase); P19 consumes
  pinned image digests, it does not define how they are built and published.
- **The self-hosted serving of open models** (vLLM/Ollama/TGI as an in-cluster provider). Out of scope
  by the platform's own principle — customers use their **own** provider keys and the platform never
  resells tokens (P7). P19's LLM scope is *internal platform access*, not model hosting.

## 4. Users & personas

- **The customer's platform / DevOps engineer** — runs the managed or enterprise tier on their own
  Kubernetes. Needs manifests they can read, probes they can trust, secrets from their own store, and an
  upgrade that does not lose data. Judges the delivery by whether they can operate it *without asking us*.
- **The private-deploy / air-gapped operator** — installs into a network with no public egress from a
  self-contained package. Needs offline install, backup/restore, a `doctor`, and honest capacity/boundary
  docs. The self-service acceptance test is written for exactly this person.
- **The self-hosting individual / small team (open core)** — runs the open-source tier with Docker
  Compose on one host. Needs a one-command bring-up and a truthful statement of what they operate
  themselves versus what the managed tier would do for them.
- **The platform's own on-call operator (us)** — reads `/readyz`, the aggregated component health, and
  the deploy's alerts. Needs the health signal to be a machine assertion, not a screenshot.
- **Downstream: every P0–P18 subsystem** — consumes the deployment contract (where its store is, how its
  secret arrives, how its readiness is aggregated). The deploy is the substrate they assume.

## 5. User stories / jobs-to-be-done

**Customer DevOps engineer**
- As a platform engineer, I want **one Kustomize overlay per environment**, so that `kubectl apply -k overlays/prod`
  stands up the whole platform with production probes, limits, and my secret store — and I can read
  exactly what it applied.
- As a platform engineer, I want **secrets referenced from my external store**, so that no credential is
  ever in a manifest, in git, or in a `kubectl get` output.
- As a platform engineer, I want **`/readyz` to name the degraded component**, so that a rollout halts on
  a real signal and my alert points at the actual fault.
- As a platform engineer, I want **an upgrade that preserves tenant state and a rollback that is a
  re-apply**, so that I can move versions without a migration cliff or a bespoke teardown.

**Private-deploy / air-gapped operator**
- As an air-gapped operator, I want a **self-contained package** with images, manifests, and checksums,
  so that I can install with no public egress and verify integrity before I apply.
- As an air-gapped operator, I want **backup/restore and a `doctor` check that ship with the deploy**, so
  that I can recover Postgres and diagnose the system myself.

**Self-hosting open-core user**
- As a self-hoster, I want **one `docker compose up` for the whole platform**, so that I can evaluate it
  on a single host without standing up Kubernetes.
- As a self-hoster, I want **an honest statement of what I operate myself**, so that I am not surprised
  by an operational burden the managed tier would have absorbed.

**Platform operator (us) & downstream subsystems**
- As an on-call operator, I want **the internal LLM secret source reported on `/readyz`**, so that a
  missing provider credential is visible before a diagnosis run fails.
- As a subsystem owner, I want **the deploy to place my store and aggregate my readiness**, so that my
  phase assumes a substrate rather than reinventing it.

## 6. Functional requirements

These map 1:1 to OpenSpec requirements in `p19-deployment` across four capabilities:
`deployment-topology`, `kubernetes-delivery`, `platform-llm-access`, `admin-console-deploy`.

### Topology & composition (`deployment-topology`)
- **FR1** The deploy SHALL define **one platform deployment unit** that brings up the Go service, Postgres,
  the object store, the telemetry substrate, the queue, the vector and graph stores, and the customer
  console, from pinned images and one secret contract, on both Docker Compose and Kubernetes.
- **FR2** The topology SHALL separate **control plane** (policy, config, entitlement, admin) from **data
  plane** (discovery, apply, run, eval) such that the data plane can serve already-established work when
  the control plane is unavailable, and this separation SHALL be expressed as a runtime boundary
  (NetworkPolicy on Kubernetes), not merely a diagram.
- **FR3** Each stateful store SHALL be declared as a stateful component with a named volume/claim, and
  **Postgres SHALL be documented as a single point of failure** with its backup automation shipped as part
  of the deploy; a deploy that accepts single-Postgres without shipping backup is non-conformant.
- **FR4** Stateless hot-path components SHALL be **horizontally scalable** (replica count is a value, not a
  code change), and no stateless component SHALL hold local state that a second replica would diverge on.
- **FR5** Secrets SHALL be sourced from the environment or an external secret store and SHALL never appear
  in a manifest, an env-example file, a repository file, a log line, a trace attribute, or a client
  bundle; the `${VAR:?}` / apply-time-required form SHALL be used so a missing secret **refuses to start**
  rather than starting misconfigured.

### Kubernetes delivery (`kubernetes-delivery`)
- **FR6** The Kubernetes delivery SHALL be a **Kustomize `base/` + `overlays/{dev,staging,prod,airgapped}`**;
  the base SHALL be a complete, applyable description and each overlay SHALL express only its differences.
- **FR7** Every workload SHALL declare a **liveness** and a **readiness** probe that read a component health
  endpoint (not a UI), **resource requests and limits**, and a **rolling-update** policy with bounded surge
  and unavailability.
- **FR8** Any workload with replicas > 1 SHALL declare a **PodDisruptionBudget**; a node drain SHALL NOT be
  able to remove the last available replica of a control- or data-plane service.
- **FR9** A **NetworkPolicy** SHALL encode the control/data-plane separation (FR2) and SHALL make egress an
  **allowlist**: only the components that must reach an external network may, and the platform's model-call
  egress is confined to the gateway's client. A bare, unrestricted egress is non-conformant.
- **FR10** All images SHALL be referenced by **digest** (not a floating tag), so an apply is reproducible;
  an overlay pointing at a mutable tag is non-conformant.
- **FR11** Secrets SHALL be referenced via an external-secret mechanism (external-secrets operator / CSI /
  a sealed reference), never a plaintext `Secret` manifest committed to git.

### Internal LLM access (`platform-llm-access`)
- **FR12** The platform's model-calling stages (P4/P4.5/P5.5/P6) SHALL obtain provider credentials only
  through the `providergateway.Secrets` seam, selected by `HEROS_SECRETS_SOURCE`, and the deploy SHALL wire
  that seam to a secret store (env / AWS Secrets Manager / an air-gapped on-prem equivalent) without a
  bootstrap secret in the manifest.
- **FR13** `/readyz` SHALL report the **live secret source** (`secrets_source: {kind, detail}`) and SHALL
  report **not ready** when a required provider credential is unresolvable — fail-closed, no env fallback
  from an external store.
- **FR14** No deploy artifact SHALL place the platform in a **customer's production request path**: the
  internal LLM-call path is platform-internal (ADR-002), the customer's transformed program calls its own
  providers, and the deploy SHALL NOT introduce a runtime dependency of customer traffic on platform uptime.
- **FR15** In an **air-gapped** deployment with no public egress, the LLM-access seam SHALL be pointable at
  an operator-provided on-prem gateway endpoint through configuration, and SHALL fail **loud and static**
  (last-known-good retained, degraded reported) when it is unreachable — never fail-open, never a startup
  dependency ([ADR-004](../adr/ADR-004-runtime-config-binding.md)).

### Operator console deploy (`admin-console-deploy`)
- **FR16** The P8 operator console SHALL ship as a **two-container deployment unit** (admin BFF + platform
  API) on its **own origin**, with its own image and probes, inheriting ADR-006 (a dead process is a dead
  container; no in-container supervisor).
- **FR17** The operator console SHALL be **unreachable from the customer console's origin**: separate origin,
  **disjoint cookie jar**, separate BFF and credential, no shared session. An admin capability SHALL NOT be
  reachable from a P9 route.
- **FR18** The operator console's BFF SHALL hold its **own** platform/admin credential server-side, distinct
  from the customer BFF's, and SHALL refuse to start in production under a `dev` identity seam (ADR-008).
- **FR19** The platform readiness SHALL aggregate the operator console's health the same way it aggregates
  the customer console's, naming it when degraded.

### Delivery, upgrade & operability (cross-cutting, `deployment-topology`)
- **FR20** The deploy SHALL define an **air-gapped package** (platform binaries + `docker save` images +
  manifests + checksums) that installs with no public egress and whose integrity is verifiable before apply;
  the package SHALL be reproducible from pinned inputs.
- **FR21** Deployment SHALL be **declarative and idempotent**: applying the same description twice converges
  to the same state; **upgrade = apply a new package with the same command**; there is no bespoke teardown
  path.
- **FR22** **Upgrade SHALL preserve user/tenant state** — tenant credentials, the session secret, the admin
  identity map, and eval/lineage data survive an upgrade byte-for-byte; system-derived state may be
  re-rendered but SHALL be regenerable from user-state + templates.
- **FR23** **Rollback SHALL be re-applying the prior package/manifest**, and SHALL be non-destructive to
  legitimate data produced during the upgrade window; a rollback SHALL NOT fall back to a same-version
  backup.
- **FR24** The deploy SHALL ship a **`doctor` / preflight check** and **backup + restore** procedures that an
  operator runs without us, and the backup procedure SHALL fail loud on an empty/failed dump rather than
  writing a zero-byte file that poisons the rollback chain.

### Capability carriage (`deployment-topology`) — added 2026-08-01

FR1–FR24 were written before P20–P26 landed, and they describe a deployment that *composes* the platform.
An audit of the artifacts against the code they run found that the composition is the part that was never
finished: the deployed process (`cmd/agentd` → `internal/launch`) registered six routes, mounted no
capability surface, applied none of the platform's nineteen Postgres migrations, and opened no Postgres
connection at all — while the manifests started Postgres and named the SQLite ledger's ping after it. The
requirements below close that, and they are written as requirements rather than as a bug list because each
one is a property the deployment has to keep having.

- **FR25** The deployed process SHALL apply the platform's Postgres schema at boot, **idempotently**: a boot
  against an already-migrated database is a no-op, and the applied-version ledger SHALL be **read** before
  each migration is considered, not merely written after it. A ledger that is written and never read cannot
  distinguish "never applied" from "applied and since reverted", and re-runs the migration either way.
- **FR26** `/readyz` SHALL name a store only if it **probes that store**. Naming one component's probe after
  a different component is non-conformant — it produces a signal that stays green while the named store is
  dead, which is the precise failure `health-signal-surface` exists to forbid.
- **FR27** Every capability surface the platform ships SHALL be **registered** by the deployed process. A
  capability that has no source on this deployment SHALL answer **503 not-mounted**; leaving its routes
  unregistered so the mux answers **404** is non-conformant. "This capability is not installed on this
  deployment" and "that identifier does not resolve" are two different facts with two different next
  actions, and a console that receives the second when the first is true tells its user their workflow does
  not exist.
- **FR28** The deploy documentation SHALL state, in one readable place, which capabilities a fresh install
  **actually serves**, which are registered-but-unsourced, and what makes the difference. A deployment whose
  capability set can only be discovered by calling it is not self-describing.
- **FR29** The **environment contract** SHALL cover every variable the deployed processes read, and the two
  substrates SHALL NOT diverge on it. A CI check SHALL fail on divergence. (FR1 and Decision 2 already
  promised "the same digest set **and the same env-var contract**"; only the digest half was ever gated,
  and the two substrates had already drifted apart on `ADMIN_CONSOLE_HEALTH_URL` by the time this was
  written.)
- **FR30** Each capability that landed after FR1–FR24 were written — P20 installable packages, P21 payments,
  P22 SSO/identity, P23 legal & docs, P26 operator surfaces — SHALL have its deployment contract expressed:
  the names of the secrets it reads, its aggregation into `/readyz`, and any scheduled job it requires. A
  capability that is implemented, tested, and unreachable on every deployment form is not shipped.
  **Exception, and it is a real one: P24's analytics and error-reporting switches
  (`HEROS_ERROR_REPORTING_DSN`, `HEROS_GA4_*`, `HEROS_CLARITY_PROJECT_ID`, `HEROS_SOURCEMAP_UPLOAD_TOKEN`)
  SHALL NOT appear in any deployment manifest or `.env` example, even empty.** P24 task 7.4 already decided
  this and gates it (`internal/deploy`): an empty slot is one `--set` from being filled in a file a customer
  edits without reading, and a default nobody chose takes effect the day somebody's shell exports the
  variable. Those integrations belong to the platform's own hosted deployment, configured from its own
  environment. FR30 is about capabilities a *customer* deploys; P24's reporting is not one, and a customer
  install that reports to nobody is the correct state — `/readyz` says `absent` and stays silent about it.
- **FR31** The consent-record **retention job** SHALL ship as a scheduled unit on both substrates, defaulting
  to a dry run and refusing to act with no retention window configured. Retention is a legal obligation with
  a clock on it; a job that exists only as a binary an operator has never been told to run does not satisfy it.
- **FR32** A backing service the deployment **starts** SHALL either be connected by a deployed process, or be
  labelled in both the manifest and the runbook as provisioned ahead of the capability that will use it.
  Aggregating an unused component into `/readyz` implies the platform depends on it, and an operator who
  reads that will keep it alive for a reason that does not exist.

## 7. Non-functional requirements

- **NFR1 — Reproducibility.** Every image is digest-pinned; the same package + overlay applied twice yields
  the same running system. (Gate: an overlay referencing a mutable tag fails a lint.)
- **NFR2 — Health is externally readable and aggregated.** `/readyz` returns structured per-component health
  and reports not-ready with the degraded component named; probes and release checks read the endpoint, and
  a UI dashboard is never a health verdict (🔴 `health-signal-surface`).
- **NFR3 — Secret hygiene is a gate, not a habit.** No secret in git, manifest, env-example, log, trace, or
  bundle — enforced at build/apply time. A committed plaintext `Secret` fails CI.
- **NFR4 — Least privilege throughout.** Containers run non-root with `no-new-privileges`, drop capabilities,
  and are read-only where they can be (the console already is); the LLM-access IAM/role grants only
  `read one secret` scope; NetworkPolicy default-denies and allowlists.
- **NFR5 — Availability posture is honest.** Single-Postgres SPOF is documented with its blast radius and
  backup precondition; the deploy states what is HA and what is a documented single point, rather than
  implying uniform redundancy. HA-via-replicas is available for stateless components; stateful HA (Postgres
  Patroni) is a documented evolution path, not claimed as shipped.
- **NFR6 — Upgrade safety is tested as its own axis.** Fresh-install, sequential-upgrade, and rollback are
  three distinct test tracks; user-state preservation is asserted, not assumed (§12).
- **NFR7 — Air-gapped self-service is the acceptance judge.** An engineer who has never seen the system
  installs, operates, and understands it from the package and docs alone; any of the three failing means the
  delivery is not yet done.
- **NFR8 — Docs are a delivery artifact.** The deploy runbook is self-contained, defines each term before
  using it, states capacity requirements *with* their configuration (nodes *and* CPU/memory), and labels
  lab-baseline numbers as baselines — never as production guarantees.
- **NFR9 — No price value and no internal mechanism leak.** Plans are referenced by name; no dollar amount or
  percentage exists in any manifest, doc, or bundle; deploy-facing messages do not leak internal profile,
  bundle, or script names.

## 8. System design summary

The platform is a **control plane** and a **data plane** with a narrow seam between them, plus a set of
**stateful stores**, a **telemetry substrate**, and **two consoles on two origins**. P19 does not add a
component; it *composes* the existing ones and expresses the composition twice — once as Docker Compose (the
single-host and open-core substrate) and once as Kustomize (the cluster substrate) — from one set of pinned
images and one secret contract.

```
                      ┌─────────────────────────── control plane ───────────────────────────┐
   operator ─▶ admin console (:4310 BFF / :4311 API, own origin, own cookie jar) ─┐          │
                                                                                  ▼          │
   customer ─▶ customer console (:4320 BFF, own origin) ─────────▶  agentd (:4321 /healthz /readyz)
                     no key in browser                               │   auth · entitlement · config
                                                                     │
   ┌──────────────────────────────── data plane ──────────────────  ▼  ──────────────────────────────┐
   │  discovery (one-shot, netns=none)   apply/codemod   runtime (sandbox isolate, netns=none)        │
   │  eval · attribution · diagnosis · verification · optimizer ── model calls ─▶ provider gateway ──▶ │
   │                                                                 (Secrets seam, egress allowlist)  │
   └──────────────────────────────────────────────────────────────────────────────────────────────────┘
        │              │                 │                  │                    │
     Postgres      object store      TSDB (BYO)        span store (BYO)      NATS / Qdrant / Neo4j
   (eval+lineage) (content-hash)   OTel-compatible    OTel-compatible        (queue · vector · graph)
        ▲ SPOF, documented + backup CronJob
```

Eight decisions carry the design; each is recorded in
[`../../openspec/changes/p19-deployment/design.md`](../../openspec/changes/p19-deployment/design.md) with
the alternative that lost and the arbitration level (the **八级法则**: 安全 > 稳定 > UX > 运维 > 可演进 >
可扩展 > 维护 > 实现) at which it lost.

- **D1 — Kustomize base + overlays, not Helm** (arbitrated **L5 可演进 / L4 运维** over **L8 实现**). Helm's
  templating hides *what actually applies* behind values and conditionals; the repository's whole posture is
  the opposite — `${VAR:?}` that refuses to start, explicit-config-over-magic. Kustomize keeps the applied
  manifest readable and diffable, which is what a private-deploy customer auditing our manifests needs.
- **D2 — One image set, two substrates** (**L5**). Compose and Kustomize reference the *same* digest-pinned
  images; the substrate is expressed twice, the artifacts once, so the two can never skew.
- **D3 — Control/data-plane separation is a NetworkPolicy, not a diagram** (**L1 安全 / L2 稳定**). The
  compile-time no-cross-import discipline (System Designer lens) gets a runtime enforcement: the data plane
  keeps serving established work with the control plane down, and "pull the control plane, does the data
  plane still run?" has a yes answer by construction.
- **D4 — Internal LLM access stays platform-internal and egress-confined** (**L1 / L2**). ADR-002 already
  says the customer's program calls its own providers; the deploy enforces that our model egress is an
  allowlist on one client, `/readyz` reports the source, and nothing puts our uptime in customer traffic.
- **D5 — The operator console is a second origin, second unit** (**L1**). Isolation is the browser origin
  boundary; a role-gated section of one app is rejected (P8 Decision 11). It inherits ADR-006's two-container
  packaging rather than reopening it.
- **D6 — Single-Postgres SPOF is accepted *because* backup ships** (**L2**). The arbitration is explicit: the
  premise of "accept single Postgres + short recovery window" is "disaster recovery from backup is real"; if
  backup does not ship, the premise fails and the decision is void. So the backup CronJob and a fail-loud dump
  are part of the deploy, not a follow-up.
- **D7 — Upgrade is declarative-idempotent; rollback is re-apply** (**L2 / L4**). No teardown path; user-state
  preserved across the version change; rollback unpacks the prior package and re-applies. This is the
  private-deploy upgrade story and the SaaS upgrade story, unified.
- **D8 — TSDB/span-store/object-store are bring-your-own, OTel-compatible** (**L4 运维**). An air-gapped
  customer does not inherit a Prometheus/Grafana operational burden they did not choose; the platform exposes
  OTel/JSON and the operator attaches their own collector, or uses the bundled optional overlay.

## 9. Design by role lens

**DevOps Engineer (co-lead) — *deliver "anyone can run it", and make every safety rule turn a light red.***
The deliverable is not a set of YAML files, it is the property that an engineer who has never seen this system
can stand it up, operate it, and roll it back without asking us — the self-service acceptance test is the only
pass/fail judge (NFR7). Every red-line this role owns becomes a machine gate rather than a sentence: readiness
**aggregates** and is externally readable, and a UI is never the verdict (🔴 `health-signal-surface`, NFR2);
secrets come from the environment or a secret store and **refuse to start** when unset (`${VAR:?}`), never
reaching git, a manifest, a log, a trace, or a bundle (🔴, NFR3); and the backup that makes single-Postgres
survivable ships *with* the deploy, with a dump that **fails loud** rather than writing the zero-byte file that
silently poisons a rollback chain (D6, FR24). Upgrade is treated as the separate axis it is — fresh-install is
not upgrade, and a private-deploy customer only ever runs the installer, never our Go tests — so user-state
(tenant credentials, session secret, admin map) is asserted to survive it (FR22), and rollback is *unpack the
prior package and re-apply*, never a same-version fallback (FR23). One parity note this role is emphatic about:
Docker Compose already carries `restart:` policy and Kubernetes carries a real restart controller, so the
"nginx/PostgreSQL have no `Restart=` by default" trap lives only on the bare-systemd path — but the *principle*
it teaches (the orchestrator's restart must be **real**, not a supervisor lying about a dead process) is exactly
ADR-006, and it governs every probe here.

**System Designer (co-lead) — *state the two planes, then make the seam a runtime fact.***
P19's architectural content is one separation and one composition. The separation: control plane decides *what
is allowed*, data plane *does the work*, and the only flows across the seam are policy/config/token going down
and audit/cost/trace/asset coming up. The compile-time discipline (control-plane code does not import data-plane
code and vice-versa — the import ban is the *proof* the two could be split into two processes) gets a **runtime**
twin here: a NetworkPolicy that makes "pull the control plane, does the data plane still serve established work?"
answerable *yes* by construction (D3, FR2, FR9). The composition: one image set expressed on two substrates so
they cannot skew (D2). The one-way doors are named in the PRD, not discovered in the cluster — the packaging
choice (D1) and the origin split (D5) are cheap now and expensive later, so they are decided here with the
alternative that lost and the level it lost at, so a future reviewer can tell a considered trade-off from an
accident.

**Backend Dev (support) — *the secret seam and the migration are the whole risk surface.***
The credential path is small and therefore must be exactly right: the model-calling stages resolve provider
credentials only through the `providergateway.Secrets` seam (never `config.Config`), selected by
`HEROS_SECRETS_SOURCE`, resolved at call time with a short in-memory TTL and never written to disk
(`secrets-baseline.md` §1.1) — and the deploy wires that seam to a store with an *ambient* identity (IRSA / a
workload identity), so there is **no bootstrap secret** in the manifest (FR12). `/readyz` reports the live
source and fails **closed** with no env fallback when a required credential is unresolvable (FR13) — because a
fail-closed signal must measure the right dimension (reachability, not traffic freshness) and must never depend
on the very traffic it gates. The migration posture the deploy assumes is the P0 one: schema is the source of
truth over any tracking table, migrations are idempotent and edition-isolated, and a column added to a deployed
table is its own `ALTER` — because "un-released ≠ no deployed database". The deploy's job is to run those
migrations on upgrade and preserve the user-state fields around them (FR22).

**AI Engineer (support) — *the model path is internal, confined, and never mocked into "done".***
The stages that call models — eval (P4), attribution/diagnosis (P4.5), verification (P5.5), the optimizer (P6)
— reach providers only through the platform gateway (ADR-002), and the deploy makes three properties physical.
First, **egress is an allowlist confined to the gateway's client** (FR9): no bare `http.Client{}` to the
internet, and in an air-gapped deployment only the one client that must egress is given a route, or is pointed
at an operator's on-prem gateway (FR15). Second, the path is **platform-internal** — the customer's transformed
program calls *its own* providers, so a diagnosis run failing never touches customer production traffic, and no
overlay may quietly wire it otherwise (FR14, D4). Third, the deploy must not let a **mock** masquerade as a live
component: a model-access seam that is "configured" is only proven by a live call plus a readiness assertion, so
`/readyz`'s `secrets_source` is the externally checkable claim that the path is real, not a log line that says
so. Pricing/cost attribution (unknown model = unpriced, never zero; immutable price snapshots) is P7's; the
deploy just carries its secret and its egress.

**Frontend Dev (support) — *build-vs-runtime config, and no key in either browser.***
Both consoles are Next.js BFFs, and the deploy honors the frontend law that **build artifact and runtime config
are separate** — image is built once, and `apiBase` / tenant-identity seam / session TTL / branding arrive as
**runtime** environment injection, because that separation *is* the boundary between a private-deploy delivery
and "rebuild per environment" (which is rejected). Neither browser ever receives a platform credential: the
customer BFF holds one credential for `agentd`, the admin BFF holds its **own** distinct credential, and the two
live on two origins with disjoint cookie jars (FR17, FR18). The console images are already the right shape —
digest-pinned Node 22, `npm ci` from the lockfile, `read_only` with a `tmpfs`, `no-new-privileges`, a real
`HEALTHCHECK` — and P19's admin-console image inherits that posture verbatim rather than inventing a second one.
The token-system build fences (`scan:tokens`, `scan:bundle` for credential/priced literals) stay *inside* the
image build, so a bundle carrying a secret or a price fails the build, not review.

**QA Engineer (support) — *the apply passing is not the deployment; upgrade and rollback are their own axes.***
Acceptance is behavioral: a green `kubectl apply` is compatible with a platform that is up and serving nothing,
so the gate is that `/readyz` reports every component healthy **and** a live path works end-to-end (the four-
layer live-event assertion, not an HTTP 200). Upgrade is tested as the separate axis it is: a **sequential
upgrade** chain, a **rollback** that round-trips, and an explicit assertion that user-state fields survive —
because "fresh install works" says nothing about "upgrade preserves the admin's session secret". The health
assertions read the **endpoint**, comparing component status and, for images, comparing **content digest** not
mtime (the "reused an old artifact with a new timestamp" trap). Idempotency is asserted — a second apply is a
no-op, a re-install says "already present" exactly once and does not re-prompt for a master password. And each
deployment **form** (open-core compose, managed Kustomize, air-gapped package) is exercised, because a capability
that works in only one form is, by rule, a bug — the deploy carries a **deployment-form impact matrix** the way
every other PR here carries an edition matrix.

**Product Designer (support) — *the runbook is the product; define every term and be honest about the boundary.***
The deployment documentation is a **delivery artifact**, not an appendix — a version behavior that changes
without the runbook changing is a shipped bug. So the runbook defines each term before using it (a reader who
cannot follow it means the doc failed, not the reader), states capacity requirements *with* their configuration
("100 users → 2 nodes" is incomplete without "at 8 vCPU / 8 GB each"), and — the rule most easily lost and most
expensive to lose — **labels lab-baseline numbers as baselines**, never as production guarantees, and writes the
boundary (single-Postgres SPOF, one-deployment-one-tenant, air-gapped LLM needs an on-prem gateway) *beside* the
benefit. It leaks no internal mechanism: no install-profile name, bundle name, or internal script name in an
operator-facing message, and it links terms to a single naming source so a wording change propagates instead of
forking.

**Sales Operations (support) — *sell only what the deploy delivers; keep the open/paid boundary honest.***
The deployment boundary *is* the commercial boundary, and it is stated by **name**: the open-source self-host
tier keeps the **local credential/execution layer and base control-plane modelling**; hosting, analytics,
advanced FinOps, multi-tenant, and audit are the managed/enterprise surfaces. That principle, once said to a
customer, the product may not violate — so the deploy artifacts encode it (an open-core overlay that stands up
the self-hostable core; enterprise capabilities gated by entitlement, not silently present). Three honesty
rules bind the deploy docs: **no price value in git** — plans are names, limits are runtime config (P7); **"one
deployment = one tenant boundary via deployment-level isolation, not software multi-tenancy"** is told plainly
to the operator, not left as a comforting ambiguity; and capacity numbers are given as **ranges with their test
environment**, never a point value implying a production guarantee (virtualization noise alone is ±50%). The word
"风险可控" never appears; observability of risk is what the platform offers, and the docs say exactly that.

## 10. Dependencies

- **Upstream (must exist for P19 to compose them):** P0 storage/lineage schema and migrations; P2 config +
  provider gateway; P2.5 telemetry substrate and the seven-tag event contract; the `providergateway.Secrets`
  seam and `secrets-baseline.md`; P7 entitlement (plans-by-name, no-price-in-git); P8 operator console;
  P9 customer console + ADR-006/007/008; ADR-002 (gateway serves platform callers) and ADR-004 (fail-static
  config binding).
- **P19 unblocks:** M15 GA / private-deploy readiness; the air-gapped enterprise motion (Sales); a future
  release-pipeline phase that publishes the digest-pinned images P19 consumes; a future Postgres-HA (Patroni)
  evolution that D6/NFR5 leaves the door open for.
- **Deliberately not depended on:** a concrete TSDB/span-store product (OQ1, kept BYO); Helm; a hosted SaaS
  operations runbook.

## 11. Risks & mitigations

| # | Risk | Mitigation |
|---|---|---|
| R1 | Kustomize overlays drift from the compose stack, so "two substrates" silently diverge. | One image set (D2); a CI check that both substrates reference the same digests and the same env contract; the deployment-form impact matrix (QA lens). |
| R2 | A secret lands in a committed `Secret` manifest or an env-example. | FR5/FR11/NFR3 as build-/apply-time gates; external-secret references only; a lint that fails on a plaintext `Secret` in git — the same posture the console bundle scan already enforces. |
| R3 | Single-Postgres SPOF becomes real data loss. | D6: backup CronJob ships with the deploy, dump fails loud on empty, SPOF documented with blast radius; Patroni evolution path stated (NFR5). |
| R4 | An overlay quietly wires the platform into a customer's production path or a broad egress. | FR14/FR9 NetworkPolicy allowlist; ADR-002 invariant; a review rule that egress is constructed, never a denylist. |
| R5 | Upgrade loses tenant/user state, or rollback poisons the chain. | FR22/FR23; upgrade + rollback tested as their own axes (§12); no same-version fallback; two-phase backup lifecycle. |
| R6 | The admin console gets folded into the customer origin "for convenience". | FR16–FR19 + P8 Decision 11 + ADR-006; origin isolation is the security boundary and is asserted in tests, not assumed. |
| R7 | Air-gapped customer cannot self-serve. | NFR7 self-service acceptance; package carries binaries + images + checksums + `doctor` + backup/restore; docs are self-contained (NFR8). |
| R8 | Deploy docs overstate performance or leak internals. | NFR8/NFR9; lab-baseline labelled, ranges not points, no price/mechanism leak; Sales honesty rules. |

## 12. Rollout & test strategy

- **Compose first, then Kustomize.** Land the unified `docker-compose.platform.yml` (open-core / single-host)
  and validate the full bring-up and `/readyz` aggregation; then the Kustomize `base/` + `overlays/` expressing
  the same topology, validated on a throwaway cluster (kind/k3d) with NetworkPolicy enforced.
- **Three test axes, not one.** (1) **Fresh install** on each form (compose, Kustomize, air-gapped package) to
  a healthy `/readyz` + one live end-to-end path. (2) **Sequential upgrade** across a version boundary, asserting
  user-state fields survive byte-for-byte and migrations apply idempotently. (3) **Rollback** round-trip via
  re-applying the prior package, asserting non-destruction of upgrade-window data.
- **Health and secret assertions are machine checks.** `/readyz` names each component and its `secrets_source`;
  probes read the endpoint; a deliberately-broken component (stopped store, missing credential) turns readiness
  red and names it. Image identity is asserted by **digest**, not mtime.
- **Security assertions are tests.** No secret in any bundle/manifest/log (build-time scan + apply-time lint);
  admin origin unreachable from customer origin; NetworkPolicy default-deny verified by a blocked probe;
  egress allowlist verified by a denied off-allowlist call.
- **Self-service acceptance (air-gapped).** An engineer with no prior exposure installs, upgrades, backs up and
  restores, and runs `doctor`, from the package and docs alone. All three (install / operate / understand) must
  pass; any failure means the delivery is not done.
- **Deployment-form impact matrix** on every P19 PR (open-core compose / managed Kustomize / air-gapped), with
  every "not affected" row explaining *why* it is not affected.

## 13. Success metrics & acceptance criteria (M15 exit checklist)

- [ ] `docker compose -f deploy/docker-compose.platform.yml up -d` brings the **whole platform** to a healthy,
      aggregated `/readyz` on one host, from digest-pinned images, with every secret refused-if-unset.
- [ ] `kubectl apply -k deploy/k8s/overlays/prod` stands up the same topology; every workload has liveness +
      readiness probes reading a health endpoint, resource limits, a rolling-update policy, and a PDB where
      replicas > 1; NetworkPolicy default-denies and allowlists egress.
- [ ] All images are **digest-pinned** in both substrates (mutable-tag lint is green); the two substrates
      reference the same image set and the same env contract.
- [ ] Secrets are external-store references only; the committed tree contains **no** plaintext `Secret`, no
      credential in any env-example, and the bundle/manifest secret scan is green.
- [ ] `/readyz` reports **not ready** and **names the component** when a store, the secret source, or either
      console is unreachable; it reports the live `secrets_source`.
- [ ] The **operator console** deploys as its own two-container unit on its own origin, with a disjoint cookie
      jar and its own credential, **unreachable** from the customer origin (asserted by test); its health is
      aggregated by `/readyz`.
- [ ] Internal LLM access resolves through the `Secrets` seam with an ambient identity and **no bootstrap
      secret**; egress is a confined allowlist; the air-gapped overlay points the seam at an on-prem gateway and
      fails static when unreachable; **no artifact** places the platform in a customer production path.
- [ ] **Upgrade** across a version boundary preserves tenant credentials, session secret, admin identity map,
      and eval/lineage data; migrations apply idempotently; **rollback** = re-apply prior package, non-destructive.
- [ ] The **air-gapped package** installs with no public egress, verifies checksums before apply, and ships
      `doctor` + backup/restore; the backup dump **fails loud** on empty; single-Postgres SPOF is documented with
      its backup precondition.
- [ ] The **self-service acceptance** test passes (install / operate / understand, by an engineer new to the
      system), and every P19 PR carries a passing **deployment-form impact matrix**.
- [ ] Deploy docs define every term, give capacity as **ranges with configuration**, label lab baselines as
      baselines, carry **no price value**, and leak **no internal mechanism**; the open/paid boundary is stated
      by name.
- [ ] The deployed process **applies the Postgres schema at boot** and a second boot is a no-op; `/readyz`
      probes the Postgres it names (FR25, FR26).
- [ ] **Every capability surface is registered** by the deployed process, an unsourced one answers 503 rather
      than 404, and the runbook lists which capabilities a fresh install serves (FR27, FR28).
- [ ] The **env-contract parity gate** is green, and every post-P19 capability (P20–P26) has its secret names,
      readiness aggregation and scheduled jobs expressed on both substrates (FR29, FR30, FR31).

## 14. Open questions

- **Q1 — Which external-secret mechanism is the default reference?** external-secrets operator, CSI Secret Store
  driver, or a sealed reference — the spec requires *an* external reference (FR11); the default the base overlay
  ships with is open. *Recommendation: external-secrets operator for the cluster default, with the AWS Secrets
  Manager path (already in `secrets-baseline.md`) as the reference implementation; air-gapped uses a sealed
  reference.*
- **Q2 — Does the open-core / air-gapped overlay bundle an optional TSDB/span-store, or stay strictly BYO?** D8
  keeps it BYO to avoid an unwanted operational burden; a bundled *optional* overlay (a minimal collector) may
  still be worth shipping for the evaluator. *Recommendation: strictly BYO in `base`; a clearly-labelled optional
  overlay that an operator opts into.*
- **Q3 — Object store for single-host / air-gapped: MinIO in-cluster, or a filesystem-backed content store?** The
  content-addressed blob store (storage-decision-record) needs an implementation for the no-external-S3 case.
  *Recommendation: MinIO in the bundled overlay; a filesystem-backed store for the smallest single-host footprint,
  decided with the object-store owner.*
- **Q4 — Postgres HA (Patroni) timing.** NFR5/D6 leave the door open; when a customer requires zero-window
  failover, is that a P19 follow-up overlay or its own phase? *Recommendation: a documented evolution overlay,
  not in the M15 scope, since the stateless data plane already scales and single-Postgres + backup meets the
  stated RPO.*
- **Q5 — Air-gapped LLM access with no on-prem gateway at all.** FR15 assumes an operator can provide an on-prem
  gateway; a customer with *no* model access wants the model-dependent stages (diagnosis/optimizer) to degrade
  gracefully rather than fail. *Recommendation: those stages report degraded-not-available on `/readyz` and the
  rest of the platform (discovery/apply/run/eval-without-judge) stays fully functional — the fail-static rule.*
- **Q6 — Six read surfaces have no persistent source, and P19 cannot invent one.** The eval board (P4), the
  scorecard (P4.5), the graph editor's IR (P5), the proposals surface (P5.5), the optimizer monitor (P6) and
  the pattern graph (P3.5) are each satisfied today only by an adapter inside a `cmd/demo` or `cmd/proof`
  binary holding one workflow in memory. Their domain packages are real and their Postgres tables exist
  (0008–0012); what is missing is the adapter that resolves a `workflow_id` to stored artifacts. Under FR27
  these are now **registered with a nil source**, so they answer 503 not-mounted rather than 404 — which is
  the honest state, not the finished one. *Recommendation: each adapter belongs to its own phase's owner, not
  to P19; P19's obligation is that the deployment says so instead of implying the capability is broken. This
  is the one place where the deployment is deliberately still incomplete, and it is listed in the runbook.*
