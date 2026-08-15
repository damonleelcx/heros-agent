# Deploying the platform

This is the self-contained runbook for standing the platform up. Every term is defined before it is
used; capacity is given as **ranges with the configuration that moves them**, never a single number;
and the one single point of failure is named beside its backup precondition, not implied away.

The platform is **one deployment unit** expressed on **two substrates** from **one digest-pinned image
set** ([`images.env`](images.env)):

- **Docker Compose** — a single host. The open-core / evaluation path.
- **Kubernetes (Kustomize)** — a cluster. The managed and enterprise path.

Both reference the *same* image digests and the *same* environment contract, so a behaviour that
reproduces on one and not the other is a **topology** question, never an **image** one. A CI check
(`make deploy-lint`) fails the build if the two ever diverge.

> **One deployment = one tenant boundary.** Isolation between customers is **deployment-level
> isolation, not software multi-tenancy**: one deployment serves one tenant boundary. Do not read the
> operator console's multi-tenant views as an invitation to share one deployment across untrusting
> organizations.

---

## Terms

| Term | Meaning here |
|---|---|
| **Control plane** | policy, config, entitlement, admin. If it is down, no *new* policy/config applies. |
| **Data plane** | discovery, apply, run, eval. Keeps serving already-established work with the control plane down. |
| **Substrate** | the thing that runs the containers: Docker Compose (one host) or Kubernetes (a cluster). |
| **Overlay** | a Kustomize layer expressing only one environment's *differences* from the base. |
| **Origin** | scheme+host+port. The two consoles are on **different origins**, which is what keeps their cookie jars disjoint. |
| **External secret** | a reference resolved at apply time from *your* store; the value never lives in this repo. |
| **`/readyz`** | the aggregated readiness endpoint. Not a dashboard — a machine-readable truth every layer above reads. |

---

## Docker Compose (single host / open-core)

Requires Docker with the Compose plugin. **Nothing else** — no registry, no published images, no secret
store.

### The one command

```bash
make deploy-up
```

That is the whole thing: it builds the three platform images from this checkout, generates the
credentials once, starts the backend **and both consoles**, waits for the aggregated `/readyz`, proves
the platform is refusing unauthenticated calls, and prints the three URLs. Re-run it any time — it is
idempotent, and it will not regenerate credentials that already exist.

| | |
|---|---|
| backend (platform API) | `http://127.0.0.1:4321` — `/readyz`, `/healthz` |
| customer console | `http://127.0.0.1:4320` |
| operator console | `http://127.0.0.1:4310` — its own origin, a disjoint cookie jar |

Ports are already taken? `AGENTD_PORT=… CONSOLE_PORT=… ADMIN_CONSOLE_PORT=… make deploy-up`. Slow box?
`READY_TIMEOUT=900`. Stop with `make deploy-down` (data survives) or `make deploy-down-hard` (data does
not — it is the "start over" button, not a rollback).

**First install determines credentials.** `deploy/.env.local` and `deploy/config/config.json` are written
once, at 0600, and are git-ignored. The script refuses to rewrite them, because rotating a credential
under a running deployment leaves the consoles holding a key the platform no longer honours — which
presents as *"the console is broken"*, not as *"the credential changed"*. To rotate deliberately: stop
the stack, delete both files, re-run. Your data is in named volumes and survives.

### The explicit form (published images, your own secret store)

For a deployment that must run **exactly the images a release published**, supply them and the secrets
yourself. Note the single `up` covering both units — the platform's secrets are needed whether or not
the operator console is in the command, so **both env files go on every invocation**:

```bash
cp deploy/images.env                 deploy/.env.images          # digest-pinned; published by the release pipeline
cp deploy/.env.platform.example      deploy/.env.platform        # then fill from YOUR secret store
cp deploy/.env.admin-console.example deploy/.env.admin-console   # then fill from YOUR secret store

docker compose --project-directory deploy \
  --env-file deploy/.env.images \
  --env-file deploy/.env.platform \
  --env-file deploy/.env.admin-console \
  -f deploy/docker-compose.platform.yml \
  -f deploy/docker-compose.admin-console.yml up -d
```

⚠️ `deploy/images.env` carries **placeholder digests** for the three platform images until a release
replaces them. Until then this form cannot pull, which is exactly why `make deploy-up` builds instead.

**A missing secret refuses to start.** Every required secret uses the `${VAR:?}` form: leave one unset
and `up` fails with the name of what is missing, rather than coming up misconfigured and failing later
somewhere less obvious. Nothing in `deploy/` — no manifest, no `.env.*.example`, no log line — carries
a secret value; those files document *names* only.

**Check it is up:** `curl -fsS http://127.0.0.1:4321/readyz` reports `ready` only when every component
(Postgres, the local ledger, object store, queue, vector/graph stores, both consoles, the secret source)
is reachable, and names any degraded one.

**Run the consent-retention job** (P23). It is a scheduled unit, not a background thread, and it refuses
by default — see [Retention](#retention-a-legal-clock-you-have-to-set) below:

```bash
docker compose --env-file deploy/.env.images --env-file deploy/.env.platform \
  -f deploy/docker-compose.platform.yml --profile jobs run --rm legal-retention -window 61320h
```

---

## What a fresh install actually serves

Read this before you install. A deployment whose capability set can only be discovered by calling it is
not self-describing, and the difference below is a real one you will meet in the console.

**Every capability's routes are registered.** A capability this deployment has no source for answers
**503 with a not-mounted body** — never 404. That distinction is load-bearing: *"this capability is not
installed on this deployment"* and *"that identifier does not resolve"* have completely different next
actions, and a console that receives the second when the first is true will tell you your workflow does
not exist. `agentd` prints the whole table at boot; `docker compose logs agentd | grep -E 'served|not mounted'`.

| Capability | State on a fresh install | Why |
|---|---|---|
| `/healthz`, `/readyz` | **served** | always. Registered by `api.New` before any capability is mounted, and left outside the auth gate, so a probe works on a deployment that is otherwise entirely unconfigured |
| `/api/v1/coverage`, `/api/v1/change-delivery`, `/api/v1/memory`, `/api/v1/install` | **served** | always, and for one reason: each is a property of this BUILD rather than of a tenant. They take no tenant, no plan and no role — which is what makes "coverage is identical on every plan" structural instead of a policy somebody has to keep. Registered by `api.New` beside health, so they need no database and no catalog |
| `p10_prompt_registry` | **served** with a platform database | `registry` is Postgres-backed |
| `p10_studio_matrix` | **served** with a platform database | models and render only. The workflow catalog, bindings and test-run are separate parts and each reports its own 503 |
| `p2_config_runtime` | **served** with a platform database | Postgres-backed read views. The **submit** write path stays unmounted: it needs a target repository to transform |
| `p11_run_linking` | **served** with a platform database | the `heros` CLI's `login`/`link` surface (migration 0020). The derived metering series is still in-memory, so a restart loses the spend figure, not the links |
| `p11_workflow_ir` | **served** with a platform database | opt-in structure, transmitted only by `heros link --with-ir` |
| `p29_link_coverage` | **served** with a platform database | `GET /api/v1/link-coverage` — how many of the runs your CLI observed were linked, readable with no plan, no account and no invoice. UNKNOWN renders distinctly from complete, permanently |
| `p29_axis_projection` | **served** with a platform database | `coverage × your nodes` — the total coverage table crossed with the structure this organization reported. A read, not a table. A node the platform was not told about renders `not reported`, never `not applicable` |
| `p29_subject_index` | **served** with a platform database | `GET /api/v1/workflows`, `/variants`, `/transforms` — what this organization has, scoped to the authenticated principal. Replaces a process-local map that answered an empty list on every real deployment |
| `p29_transform_receipts` | **served** with a platform database | opt-in transform outcomes — per-node applied/refused and a diffstat, transmitted only by `heros apply --link-receipt`. Never a diff: the payload has three integers where one would go |
| `p30_heros_agent` | **served** with a platform database | the analysis agent's customer-facing half: `GET /api/v1/agent-definition`, which a `customer`-placed organization's CLI reads so `heros analyse` can run the platform's own definition on their machine under their own provider key. 🔴 Served does NOT mean anything is analysed — placement defaults to `disabled` for every organization and `heros_tenant_placement` starts empty, so on a fresh deployment this answers `{"placement":"disabled"}` to everyone until an operator sets one. The result comes back through `p11_workflow_ir`, not a route of its own |
| `p1_source_discovery` | **served** with a platform database | customer-pushed snapshots; discovery and pattern classification run here |
| `p35_pattern_graph` | **served** with a platform database | labelled when source has been pushed, drawn from opt-in structure otherwise |
| `p4_eval_board` | **served** with a platform database | assembled from LINKED runs. No statistical tie detection: the bootstrap replicates stay on the machine that computed them, and the board says so rather than implying none were tied |
| `p30_eval_set` | **served** with a platform database | the board's denominator opened up: how many cases, the reference-label split, how many oracles can decide anything, and which coverage axes had no obligations at all. 🔴 The CASES themselves stay on the customer's machine — the wire permits `eval.case_count`, "a count, never the cases" — so the surface reports `counts_only` and names that rule rather than drawing an empty table that would read as a broken eval |
| `p45_scorecard` | **served** with a platform database | per-node cost and latency from a linked run. Failure attribution is reported `unavailable` — it needs per-node correctness, which is eval data and does not cross |
| `p5_graph_editor` | **served** with a platform database | the IR is re-derived from the pushed snapshot, so this needs SOURCE and not just a graph |
| `p55_verdict_ingest` | **served** with a platform database | the endpoint `heros report-verdict` transmits to. The only way a stored verdict can say `pass`: this platform can generate a proposal and can never measure one |
| `p55_proposals` | **served** with a platform database | the recommendation surface over reported verdicts. Nothing here has a diff, so the open-PR action refuses by name |
| `p55_proposal_compile` | **served** with a platform database | AST codemod over the pushed snapshot. With `HEROS_SANDBOX_CONTAINED` unset the diff is parsed, not built — no isolate can hold a customer's compiler |
| `p55_proposal_generation` | **served** with a database *and* a published `models.json` | cost-bottleneck operators only. A diagnosis needs the eval cases, which stay with the customer |
| `p12_forge_delivery` | **served** with a database *and* a published `plans.json` | routes, conditions and history. Every delivery is withheld as `no_diff` until proposals carry one. The catalog is the gate because delivery opens a pull request in your repository |
| `p7_billing` | **served** with a database *and* a published `plans.json` | durable ledger, accounts and meters — read model plus consent. See the note below on what it shows with no payment provider |
| `p23_consent` | **served** with a database *and* a customer console | the manifest is read from the console's origin |
| `p27_account_system` | **served** with a published `plans.json` | organizations, members, invitations and API credentials. It needs the catalog because a members page that cannot state the seat allowance invites a support ticket on its first use; it does **not** need a database — with none, identity is in-memory and `/readyz` says so under `account_system.store`. **Self-serve sign-up is off unless `HEROS_SELF_SERVE_SIGNUP` says otherwise**: an air-gapped or single-customer install must not grow a registration form by upgrading, and the effective value is on `/readyz` rather than inferred from whether an identity provider is configured |
| `p6_optimizer` | **registered, not mounted** | no persistent adapter outside a demo binary (PRD Q6) |
| `p25_run_monitor` | **registered, not mounted** | and it cannot be. This platform never executes your workflow — it learns of a run when the CLI links it, which is after the run finished, so there is nothing live to stream. Per-node state is derived from per-node correctness, which is eval data. What a linked run CAN show is served: its scores (`p11_run_linking`) and its per-node cost and latency (`p45_scorecard`) |
| `p21_payments` | **registered, not mounted** *until `BILLING_MODE` is set* | its presence mounts checkout, plan changes and the provider webhook; `test` or `live`, declared and never inferred. Anything else is refused rather than defaulted. The two credentials resolve through the same secrets seam as the model-provider keys, under `billing_provider` and `billing_webhook`. ⚠️ `live` moves real money |
| `p13_authoring` | **registered, not mounted** | its only store implementation is in-memory, so mounting it would record and then forget |
| `POST /billing/webhook` | **not registered** | the single inbound-from-internet path is mounted only where a deployment collects payments; it is not published to answer 503 |

> **An install with no payment provider has no billing account, and that is the intended state — not a
> gap.** A billing account is created by exactly one thing: checkout, which is P21. With no provider
> configured there is no checkout, so `/app/billing` and `/app/account` will never show one, for any
> tenant, ever. The platform says so in its own words (`reason_code: collection_not_configured`) and both
> pages render it as a configured state rather than as a lookup that failed — because "no such account"
> reads as a missing record and sends an operator looking for a button that does not exist.
>
> **Everything billing is for still works without it.** Entitlements resolve from `plans.json`, usage is
> metered against the plan's allowances, and the ledger records what was used. The absent part is
> COLLECTION. A deployment that wants accounts configures a payment provider; there is no second path
> that mints one, deliberately — an account carries a provider customer handle
> (`account.NewHandle` refuses an empty one, so that "this customer cannot be billed" is discovered at
> provisioning rather than at the first charge), and inventing a placeholder handle would be a provider
> reference that references no provider.

Without `DATABASE_URL` the four Postgres-backed rows join the unmounted set and say so. That is a
supported single-binary form, not a misconfiguration.

### Pointing a provider somewhere other than its vendor

Set `HEROS_PROVIDER_<NAME>_BASE_URL` to route one provider through an API relay, a regional gateway or
a corporate egress proxy. `<NAME>` is `OPENAI`, `ANTHROPIC` or `BEDROCK`, and the credential for that
provider is resolved exactly as before — only the address changes.

```
HEROS_PROVIDER_OPENAI_BASE_URL=https://relay.example.com/v1
```

Unset means the vendor endpoint, which is what every deployment did before this existed. Each override
is named on the boot log, because the console shows a provider's *name* and never its address — if it
is not said at boot it is not said anywhere.

⚠️ On Kubernetes, set it in **`deploy/k8s/overlays/prod/kustomization.yaml`** and deploy — not with
`kubectl set env`. The deploy applies a full render, so an out-of-band edit is reverted by the next one
and the redirect disappears at the least convenient moment. It is deliberately not declared with an
empty value in the manifests: an unset variable and one set to `""` behave identically here, so a dead
placeholder would add env-contract surface to every deployment to save one line in the one that uses it.

Four values are **refused at boot** rather than ignored, because a base URL is where this deployment's
provider credential gets sent and a silent fallback means the operator believes traffic goes to their
relay while the key goes to the vendor:

| Refused | Why |
|---|---|
| `http://` to any non-loopback host | the provider key would cross the network in clear text. Loopback may use `http` — a relay on the same host puts nothing on a wire |
| a URL with no scheme or host | there is nothing to send to; `relay.example.com/v1` is not an address |
| userinfo in the URL (`https://user:pass@…`) | anything that logs the endpoint then logs the credential, and this logs it at boot |
| a query or fragment | the adapters append a path (`/chat/completions`), so `?key=abc` would silently become a URL nobody wrote |

A variable naming something that is not a provider — `HEROS_PROVIDER_OPENAI2_BASE_URL` — also fails the
boot. An ignored override leaves an operator certain they redirected traffic they did not redirect, and
the evidence that they did not is a bill from the vendor they were trying to stop using.

> 🔴 **A relay sees what you send it.** Under a `platform` placement this deployment reads a customer's
> source and posts it to whichever endpoint the provider points at. Redirecting `openai` therefore puts
> customer source code in the hands of whoever runs that relay. The agent's *rehearsal* only ever sends
> the pinned calibration fixtures, which are this repository's own test trees — so measuring a
> definition through a relay exposes nothing of a customer's, while serving one does. Those are
> separable, and worth separating deliberately.

---

### Provisioned ahead of use

The object store, queue, vector store and graph store are **started but not yet connected** by any
platform process. They are stood up so the storage exists and is backed up before the data does. They
are labelled here and in the manifests rather than left for you to infer from a quiet log — but do not
size or tune them for a load that is not arriving yet.

---

## The build gate, and why it is off by default

P5.5 compiles proposals into reviewable diffs on every deployment with a database and a pushed source
snapshot. Whether it also **builds** them is a separate switch, and it is off unless you turn it on.

The gate compiles a **customer's repository**, so it runs inside `internal/sandbox`'s isolate: a scrubbed
environment, no ambient credentials, a filesystem scoped to the working set, bounded CPU/memory/wall
clock, and denied egress. If those cannot be established the gate **fails closed** — it reports
`unbuilt` with the reason and never falls back to compiling on the host.

Two things have to be true, and neither is something the process can detect for itself:

| | |
|---|---|
| `HEROS_GO_TOOLCHAIN` | the pinned `go` binary. `deploy/Dockerfile.compile` sets it; the API image (distroless) has no toolchain, and the gate reports that rather than guessing |
| `HEROS_SANDBOX_CONTAINED=1` | your **declaration** that this container denies network egress and mounts a read-only working set |

> **The declaration is a claim, and setting it wrongly is the failure the whole design exists to
> prevent.** `sandbox.NewContainedEnforcer` *advertises* egress denial and filesystem scope as in force —
> it does not provide them; your runtime does. Set it on a container that has no route out, never on the
> API server, which needs the database.
>
> ⚠️ **This is not fully closed today, and here is the exact gap.** A compile worker must reach Postgres
> to read proposals and write diffs, so its container cannot be `network_mode: none`. Closing it properly
> means the isolate denying egress **per child** — a network namespace on the compiler process itself —
> which `internal/sandbox` does not implement: `SubprocessEnforcer` reports `NetworkDeny=false` and there
> is no platform enforcer above it. So on the supported posture the filesystem scope, the credential
> scrub and the resource bounds are the isolate's; the egress denial is the **container's network
> policy**, and it is only as tight as you write it. Permit the database and nothing else.

Without both, the gate reports what it did and did not prove, the proposal stays `unbuilt`, and P12
delivery stays withheld — which is the honest state, not a broken one. The diff is still generated and
still reviewable.

## Kubernetes (Kustomize)

> **Deploying on AWS?** [`AWS.md`](AWS.md) is the end-to-end EKS runbook — ECR, IRSA, Secrets Manager,
> storage, ingress and verification. Read its §0 first: four things this tree does not yet contain will
> stop an AWS deploy, and three of them fail *silently*.

The base is a complete, applyable description; `kubectl kustomize deploy/k8s/base` renders exactly what
applies — no templating hides it. Pick the overlay for your environment:

```bash
kubectl apply -k deploy/k8s/overlays/dev        # single-node kind/k3d
kubectl apply -k deploy/k8s/overlays/staging
kubectl apply -k deploy/k8s/overlays/prod
kubectl apply -k deploy/k8s/overlays/airgapped  # on-prem gateway, no public egress
```

Each overlay contributes **only its differences** (replica counts, resource sizes, the secret backend,
the egress target). Secrets are referenced through the [External Secrets Operator](https://external-secrets.io);
install it and configure the `SecretStore` for your environment first — the platform never carries a
plaintext `Secret`, and CI fails if one is ever committed.

- **Probes** read a health endpoint, never a rendered page; a bad revision that fails readiness halts
  the rollout within a bounded window instead of replacing healthy pods with broken ones.
- **PodDisruptionBudgets** on every multi-replica workload mean a node drain cannot remove a service's
  last replica.
- **`agentd` runs ONE replica, on a PersistentVolumeClaim, and an upgrade is briefly disruptive.** This
  is deliberate and it is the honest shape of the service today: its ledger is a single SQLite file
  (tenant credentials, the registries, memory, the tool index, the inbox) on a ReadWriteOnce claim, so a
  second replica would be a second database answering different questions behind one Service — and on a
  ReadWriteOnce claim it would not schedule at all. The update strategy is therefore `Recreate`, which
  means a few seconds of unavailability on each apply. **Do not raise `spec.replicas`**: it buys no
  availability and costs you a split ledger. When the ledger's contents move to the platform database
  the count goes back to being a value you set, exactly as FR4 intends.
- **The customer console runs ONE replica too, and for a different reason than `agentd`.** Its sessions
  are a `Map` in process memory (`web/console/src/lib/session.ts`), so two replicas are two disjoint
  session stores behind one Service: a user signs in on one pod and roughly half their later requests
  reach the other, find no session, and are redirected to `/signin?reason=session_ended`. It logs
  nothing — from the server's side, answering "no such session" is correct — so it presents only as *the
  console keeps logging me out*. **Do not raise `spec.replicas`** until sessions move to a shared store.
  Note that one replica does not make sessions durable either: a rollout replaces the pod and everyone is
  signed out. The **operator** console does not share this — its sessions are rows in `admin_session`,
  which is why that Deployment runs two.
- **Consent retention** runs as a weekly `CronJob` that is a **dry run with no window set** until you
  configure both — see [Retention](#retention-a-legal-clock-you-have-to-set).
- **Inbound is one door.** The only route that accepts unsolicited internet traffic is Stripe's
  webhook, `POST /billing/webhook`, and it is mounted only where a deployment collects payments. Its
  posture, its secret wiring and its runbook are
  [`docs/decisions/p21-billing-webhook-ingress.md`](../docs/decisions/p21-billing-webhook-ingress.md).
- **NetworkPolicy** default-denies and permits only the seam flows; **egress is an allowlist** — only
  the platform service reaches an external provider, and the air-gapped overlay replaces even that with
  an on-prem gateway and no public egress.

---

## Capacity (ranges, with what moves them — these are lab baselines, not production guarantees)

Capacity is a function of node count **and** per-node CPU/memory, and of replica counts you set in the
overlay. The figures below are **lab baselines measured on a reference cluster**; they are a starting
point to size from, **not a throughput guarantee** for your workload.

| Component | Scales by | Lab baseline (label, not a promise) |
|---|---|---|
| `agentd` | **fixed at 1** — vertical only (see the bullet above: a second replica is a second ledger) | 1 everywhere |
| customer console | **fixed at 1** — vertical only, until sessions move to a shared store | 1 everywhere |
| operator console (stateless) | replica count | dev 1 · staging 2 · prod 3 |
| Postgres (stateful) | vertical only — **single writer** | 1 (see SPOF below) |
| object / queue / vector / graph stores | vertical; capacity by attached volume size | 1 each |

Raising a stateless component's throughput is raising its `replicas` in the overlay. The stateful
stores do not scale out here; size their volumes for retention.

⚠️ **Two of the three rows above used to say `dev 1 · staging 2 · prod 3`, and both were wrong** — they
described the shape these services are meant to reach, in a table an operator reads as what to set
today. `agentd` has run one replica since its ledger was found to be per-pod SQLite; the customer
console now does too, for a different per-process store (sessions). Only the operator console is
genuinely stateless, because its sessions are rows in `admin_session`.

---

## Backups and the single point of failure

**Postgres is a single point of failure.** It is a single-writer instance (no split-brain), and its
blast radius is: if the Postgres volume is lost and there is no backup, eval and lineage history since
the last backup is gone (RPO = time since last successful dump). The stateless data plane and the other
stores are unaffected in availability, but the platform's durable record lives here.

**Accepting that SPOF is a sound call *only because backup ships with the deploy.*** It is not a
follow-up:

- **Compose:** the `postgres-backup` service runs a daily dump to the `postgres-backups` named volume.
- **Kubernetes:** the `postgres-backup` CronJob dumps daily to a PVC.

The dump **fails loud on empty** — every failure path deletes the partial file, so a zero-byte backup
can never form a poisoned rollback chain. A dump smaller than a plausible minimum is treated as a
failure, not a success.

**Restore** is a plain `pg_restore` from a dump — no bespoke tool to trust:

```bash
# Compose: restore the latest dump into the running postgres container.
deploy/scripts/doctor.sh --restore /path/to/heros-YYYYMMDDTHHMMSSZ.dump
```

**Zero-window failover** (Patroni HA) is a future overlay — the stateless service layer connects to
standard Postgres HA with no service-code change. The door is left open; it is not walked through here.

**`agentd`'s ledger is the *second* thing to back up**, and it is not covered by the Postgres dump. It
holds tenant credentials, the registries, memory, the tool index and the inbox, in a SQLite file under
`HEROS_DATA_DIR`. On Compose it is the `agentd-data` named volume; on Kubernetes it is the
`agentd-ledger` PersistentVolumeClaim. Snapshot it with the same schedule you give Postgres — losing it
loses every tenant's credential, and no amount of Postgres backup brings that back.

---

## Retention: a legal clock you have to set

The consent-retention job removes acceptance records older than a retention window. It ships scheduled
on both substrates, and it **refuses twice by default**, on purpose:

1. **With no window it deletes nothing** and says so. The retention period is a *legal* answer — seven
   years, per [`p23-one-way-doors.md`](../docs/decisions/p23-one-way-doors.md) §1.4 — not an engineering
   default. A job that invented one would delete the wrong things confidently.
2. **Every run is a dry run** until an operator adds `-apply`. A deletion job whose first production run
   is also its first run ever is a defect waiting for a quiet weekend.

Set the window once your counsel has stated it — `61320h` is seven years — in
`LEGAL_RETENTION_WINDOW` (Compose) or the CronJob's `args` (an overlay patch on Kubernetes). Then run it
once by hand with `-apply` and read the output before you let the schedule do it.

Retention is **not** erasure. Erasure tombstones a subject and keeps the evidentiary row; retention
removes the row entirely. They are separate operations with separate entry points, deliberately.

---

## Upgrade, rollback, air-gapped

- **Upgrade = apply a new package with the same command.** Compose `up -d` and `kubectl apply -k` are
  declarative and idempotent: a second apply converges to the same state and re-prompts for nothing.
  There is no version-specific teardown script.
- **User/tenant state survives an upgrade byte-for-byte** — tenant credentials, the session secret, the
  admin identity map, and eval/lineage data. System-derived state may be re-rendered but regenerates
  from user-state + templates. Both stores are user state: the platform database **and** `agentd`'s
  ledger volume, which is why the Kubernetes base gives the ledger a PersistentVolumeClaim rather than
  an `emptyDir`. If you carried a local `deploy/k8s` fork from before that change, check it.
- **Schema migrations apply at boot, and a second boot applies none.** `agentd` reads the applied-version
  ledger before it considers any migration, so re-running `up -d` or `kubectl apply -k` changes no
  schema. It is a *read*, not just a write: a ledger that is only written cannot tell "never applied"
  from "applied and since reverted", and this DDL is bare `CREATE TABLE` — a re-run is an error, not a
  no-op, so idempotence has to come from the ledger. A migration failure names the migration and how far
  the run got, and the process does not start.
- **Rollback = re-apply the prior package.** The same verb, the older package. It is non-destructive to
  legitimate data produced during the upgrade window and **never** falls back to a same-version backup.
- **Air-gapped** delivery is a self-contained package (binaries + `docker save` images + manifests +
  checksums) built by [`scripts/package-airgapped.sh`](scripts/package-airgapped.sh), verified before
  apply by [`scripts/verify-package.sh`](scripts/verify-package.sh), installed with no public egress by
  [`scripts/install-airgapped.sh`](scripts/install-airgapped.sh), and operated with
  [`scripts/doctor.sh`](scripts/doctor.sh) (preflight, backup, restore) — all without contacting the
  platform team.

---

## The commercial boundary

The **open-core** overlay stands up the self-hostable core: the local execution layer and the base
control-plane modelling. **Managed and enterprise capabilities are gated by entitlement**, by plan
**name** — there is no price value in any manifest, doc, or bundle here, and none will be added. What a
plan costs lives in the commercial system, not in a deploy artifact.
