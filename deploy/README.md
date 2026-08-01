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
| `/healthz`, `/readyz` | **served** | always |
| P13 coverage & delivery, P17 memory, P20 install | **served** | no external store needed |
| P10 prompt registry | **served** with a platform database | `registry` is Postgres-backed |
| P10 studio matrix — models, render | **served** with a platform database | same store; the workflow catalog, bindings and test-run are separate parts and each reports its own 503 |
| P2 config/runtime — transforms, runs, specs | **served** with a platform database | Postgres-backed read views. The **submit** write path stays unmounted: it needs a target repository to transform |
| P23 consent | **served** with a platform database *and* a customer console | the manifest is read from the console's origin |
| P4 eval board, P4.5 scorecard, P5 graph editor, P5.5 proposals, P6 optimizer, P3.5 pattern graph, P2.5 run monitor | **registered, not mounted** | their Postgres tables exist, but the adapter from a `workflow_id` to stored artifacts lives only inside a demo binary today |
| P11 run linking (`/api/v1/whoami`, `/api/v1/run-links`) | **served** with a platform database | the `heros` CLI's `login`/`link` surface. Postgres-backed since migration 0020. A store read that fails fails its caller, so a coverage figure can never be quietly wrong |
| P7 billing, P21 payments, P13 authoring | **registered, not mounted** | each has exactly one store implementation and it is in-memory — mounting it would record your data and forget it on restart |
| P12 forge delivery | **registered, not mounted** | its gate and pending providers read verification state that has no store yet |
| `POST /billing/webhook` | **not registered** | the single inbound-from-internet path is mounted only where a deployment collects payments; it is not published to answer 503 |

Without `DATABASE_URL` the four Postgres-backed rows join the unmounted set and say so. That is a
supported single-binary form, not a misconfiguration.

### Provisioned ahead of use

The object store, queue, vector store and graph store are **started but not yet connected** by any
platform process. They are stood up so the storage exists and is backed up before the data does. They
are labelled here and in the manifests rather than left for you to infer from a quiet log — but do not
size or tune them for a load that is not arriving yet.

---

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
| `agentd` (stateless) | replica count (a *value*, no code change) | dev 1 · staging 2 · prod 3 |
| customer / operator console (stateless) | replica count | dev 1 · staging 2 · prod 3 |
| Postgres (stateful) | vertical only — **single writer** | 1 (see SPOF below) |
| object / queue / vector / graph stores | vertical; capacity by attached volume size | 1 each |

Raising a stateless component's throughput is raising its `replicas` in the overlay. The stateful
stores do not scale out here; size their volumes for retention.

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
