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

Requires Docker with the Compose plugin.

```bash
cp deploy/images.env            deploy/.env.images        # the digest-pinned image set
cp deploy/.env.platform.example deploy/.env.platform      # then fill from YOUR secret store
docker compose --env-file deploy/.env.images --env-file deploy/.env.platform \
  -f deploy/docker-compose.platform.yml up -d
```

The operator console is a **separate origin, separate unit** — add it beside the platform:

```bash
cp deploy/.env.admin-console.example deploy/.env.admin-console   # then fill from your secret store
docker compose --env-file deploy/.env.images --env-file deploy/.env.admin-console \
  -f deploy/docker-compose.platform.yml -f deploy/docker-compose.admin-console.yml up -d
```

**A missing secret refuses to start.** Every required secret uses the `${VAR:?}` form: leave one unset
and `up` fails with the name of what is missing, rather than coming up misconfigured and failing later
somewhere less obvious. Nothing in `deploy/` — no manifest, no `.env.*.example`, no log line — carries
a secret value; those files document *names* only.

**Check it is up:** `curl -fsS http://127.0.0.1:4321/readyz` reports `ready` only when every component
(Postgres, object store, queue, vector/graph stores, both consoles, the secret source) is reachable,
and names any degraded one.

---

## Kubernetes (Kustomize)

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

---

## Upgrade, rollback, air-gapped

- **Upgrade = apply a new package with the same command.** Compose `up -d` and `kubectl apply -k` are
  declarative and idempotent: a second apply converges to the same state and re-prompts for nothing.
  There is no version-specific teardown script.
- **User/tenant state survives an upgrade byte-for-byte** — tenant credentials, the session secret, the
  admin identity map, and eval/lineage data. System-derived state may be re-rendered but regenerates
  from user-state + templates.
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
