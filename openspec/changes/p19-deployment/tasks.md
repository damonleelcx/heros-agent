# Tasks — P19: Deployment & Delivery

Ordered by workstream. P19 is downstream of P0–P18; these tasks compose existing components and add a second
substrate. Each task is independently verifiable. Every PR carries a **deployment-form impact matrix**
(open-core compose / managed Kustomize / air-gapped) with every "not affected" row explaining *why*.

## 1. System Designer + DevOps — Decide the one-way doors first (blocks everything else)
- [x] 1.1 Ratify **D1 (Kustomize over Helm)**, **D2 (one image set, two substrates)**, and **D5 (operator
      console = second origin)** in `design.md`; these are cheap now and expensive later, so they are decided
      before any manifest is written.
- [x] 1.2 Write the control/data-plane component inventory: which components are control plane, which are data
      plane, which are stateful stores, and the exact flows permitted across the seam (policy/config/token down;
      audit/cost/trace up). This inventory is the single source the NetworkPolicy and the topology derive from.
- [x] 1.3 Confirm the **digest-pinned image set** the two substrates share (agentd, customer console, admin
      console, and each store image), and the CI check that asserts both substrates reference the same digests.

## 2. DevOps — The unified platform on Docker Compose (open-core / single-host)
- [x] 2.1 Author `deploy/docker-compose.platform.yml`: agentd + Postgres + object store + queue + vector/graph
      stores + customer console, one `/readyz` truth, every secret in `${VAR:?}` refuse-to-start form.
- [x] 2.2 Author `deploy/.env.platform.example` carrying **no secrets** (mirror the `.env.console.example`
      posture: placeholders only, "THIS FILE CARRIES NO SECRETS AND NEVER WILL").
- [x] 2.3 Declare Postgres as a stateful component with a named volume; ship the **backup CronJob/service** and
      a restore procedure; make the dump **fail loud** on empty (delete the zero-byte file on every failure path).
- [ ] 2.4 Verify the whole stack reaches an **aggregated healthy `/readyz`** from a cold `up -d`, and that an
      unset required secret makes compose **refuse to start** with its message.
      *Not closed. §11 verified `docker compose config` parses both units, and verified the readiness
      aggregation itself against a real Postgres (green with it up; **HTTP 503, `status: degraded`,
      `degraded_components: ["postgres"]` with it stopped**). What is still unrun is the thing this task
      names: a cold `up -d` of the whole stack. A config that parses is not a stack that comes up.*

## 3. Backend — Readiness aggregation and the secret seam wiring
- [x] 3.1 Extend `/readyz` (`internal/api/server.go`) to aggregate **every** deployed component — the stores,
      the queue, both consoles, and the **secret source** — reporting `not_ready` and **naming** the degraded
      component, plus `secrets_source: {kind, detail}`.
- [x] 3.2 Wire the boot path (`internal/launch`) so the model-calling stages resolve credentials **only** through
      the `providergateway.Secrets` seam selected by `HEROS_SECRETS_SOURCE`, with **no bootstrap secret** and
      **fail-closed** on an unresolvable required credential (no env fallback from an external store).
- [x] 3.3 Confirm the fail-closed readiness signal measures **reachability**, not traffic freshness, and does
      not depend on the traffic it gates (no deadlock).
- [ ] 3.4 Ensure migrations run on upgrade **idempotently** and **edition-isolated**, and that user-state fields
      (tenant credentials, session secret, admin identity map) are preserved across the run.
      *Partly closed by 11.1/11.2: migrations now run at boot and a second boot applies none (proven live —
      19 applied to an empty database, then "already current"). Still open: **edition isolation** and the
      **byte-for-byte user-state assertion across a version boundary**, which is task 8.2's job and needs a
      real two-version upgrade. Left unchecked deliberately — "the migrations are idempotent" is not the
      same claim as "an upgrade preserved your data", and merging them would be the kind of half-evidence
      the audit behind §11 exists to stop.*

## 4. DevOps + System Designer — Kubernetes delivery (Kustomize)
- [x] 4.1 Author `deploy/k8s/base/`: a complete, applyable description — Deployments/StatefulSets for every
      component, each with liveness + readiness probes reading a health endpoint, resource requests/limits, and a
      bounded rolling-update policy.
- [x] 4.2 Add a **PodDisruptionBudget** for every workload with replicas > 1 (a node drain cannot remove the last
      replica of a control- or data-plane service).
- [x] 4.3 Author the **NetworkPolicy**: default-deny, allow only the seam flows (task 1.2), and make **egress an
      allowlist** — the model-call egress confined to the gateway's client; no bare unrestricted egress.
- [x] 4.4 Reference **all images by digest**; add the mutable-tag lint that fails an overlay pointing at a
      floating tag.
- [x] 4.5 Reference secrets via an **external-secret mechanism** (external-secrets operator / CSI / sealed
      reference); add the lint that fails on a committed plaintext `Secret`.
- [x] 4.6 Author `overlays/{dev,staging,prod,airgapped}`, each expressing **only its differences** (replica
      counts, resource sizes, the secret backend, the egress target for the air-gapped LLM seam).

## 5. Frontend + DevOps — Operator console deploy unit (second origin)
- [x] 5.1 Author `deploy/Dockerfile.admin-console` inheriting `Dockerfile.console` posture: digest-pinned
      `node:22`, `npm ci` from the lockfile, the build fences inside the image, `read_only` + `tmpfs` +
      `no-new-privileges`, `EXPOSE 4310`, a real `HEALTHCHECK`.
- [x] 5.2 Author `deploy/docker-compose.admin-console.yml` (ADR-006 two-container shape): admin BFF `:4310` +
      admin API `:4311`, its **own** credential (distinct from the customer BFF's), and the `configured` tenant
      identity seam (`dev` refuses production).
- [x] 5.3 Add the admin console to `deploy/k8s/base/` on its **own origin/service**; assert a **disjoint cookie
      jar** and that no admin capability is reachable from a customer-console route.
- [x] 5.4 Aggregate the admin console's health into `/readyz` (task 3.1), naming it when degraded.

## 6. AI Engineer + DevOps — Internal LLM access posture
- [x] 6.1 Document and wire the secret store paths: env (dev), AWS Secrets Manager (managed, per
      `secrets-baseline.md`), and an on-prem gateway target for air-gapped; provider→secret-ID mapping is
      configuration, not code.
- [x] 6.2 Assert **egress confinement**: only the gateway's client may reach a provider; a denied off-allowlist
      call is a test.
- [x] 6.3 Assert **no customer-path dependency**: no artifact wires the platform into a customer's production
      request path (ADR-002); the model path is internal-only.
- [x] 6.4 Air-gapped fail-static: with the on-prem gateway unreachable, model-dependent stages report
      **degraded-not-available** on `/readyz` and the rest of the platform stays fully functional.

## 7. DevOps — Air-gapped package, upgrade, rollback, doctor
- [x] 7.1 Define the **self-contained package**: platform binaries + `docker save` images + manifests +
      checksums; install works with **no public egress**; integrity is **verifiable before apply**; the package
      is reproducible from pinned inputs.
- [x] 7.2 Make deployment **declarative-idempotent** (apply twice → same state); **upgrade = apply a new package
      with the same command**; no bespoke teardown.
- [x] 7.3 **Rollback = re-apply the prior package**, non-destructive to upgrade-window data; **never** a
      same-version fallback.
- [x] 7.4 Ship a **`doctor` / preflight** check and **backup + restore** procedures an operator runs without us.

## 8. QA — The acceptance gate (three axes, machine assertions)
- [ ] 8.1 **Fresh install** on each form (compose, Kustomize on kind/k3d, air-gapped package) → aggregated
      healthy `/readyz` + one **live** end-to-end path (four-layer live-event assertion, not an HTTP 200).
- [ ] 8.2 **Sequential upgrade** across a version boundary → user-state fields survive **byte-for-byte**;
      migrations idempotent; image identity asserted by **digest**, not mtime.
- [ ] 8.3 **Rollback** round-trip → re-apply prior package; upgrade-window data non-destroyed.
- [x] 8.4 Health: a deliberately-broken component (stopped store, missing credential, dead console) turns
      `/readyz` red and **names** it; probes read the endpoint, not a UI.
- [x] 8.5 Security-as-tests: no secret in any bundle/manifest/log (build scan + apply lint); admin origin
      **unreachable** from customer origin; NetworkPolicy default-deny verified by a blocked probe; egress
      allowlist verified by a denied off-allowlist call.
- [x] 8.6 **Idempotency**: a second apply is a no-op; a re-install says "already present" exactly once and does
      not re-prompt for a master password.
- [ ] 8.7 **Self-service acceptance** (air-gapped): an engineer new to the system installs / operates / understands
      from the package + docs alone; any of the three failing means the delivery is not done.

## 9. Product Designer + Sales Operations — The runbook and the boundary
- [x] 9.1 Write the self-contained deploy runbook (`deploy/README.md`): each term defined before use; capacity as
      **ranges with configuration** (nodes *and* CPU/memory); lab baselines **labelled** as baselines, never
      production guarantees; single-Postgres SPOF and its backup precondition stated beside the benefit.
- [x] 9.2 Encode the **commercial boundary**: an open-core overlay standing up the self-hostable core (local
      execution layer + base control-plane modelling); managed/enterprise capabilities gated by entitlement.
- [x] 9.3 Honesty gates: **no price value** in any manifest/doc/bundle (plans by name); **"one deployment = one
      tenant boundary via deployment-level isolation, not software multi-tenancy"** stated plainly; the string
      "风险可控" appears nowhere; no internal profile/bundle/script name in an operator-facing message.

## 10. Documentation & fold-in
- [x] 10.1 Cross-link the PRD, this change, and the ADRs it inherits (002/004/006/008); add the P19 row to
      `docs/prd/README.md`.
- [ ] 10.2 On deploy, fold the four delta specs into `openspec/specs/` (drop the `## ADDED` headers).

## 11. Backend + DevOps — Capability carriage (added 2026-08-01, after P20–P26 landed)

The audit behind these tasks: the deployed process registered six routes, mounted no capability surface,
applied none of the platform's nineteen Postgres migrations, and opened no Postgres connection — while the
manifests started Postgres and named the SQLite ledger's ping after it. §§1–10 above composed the *artifacts*;
this section makes the artifacts and the process they run agree. Design: Decisions 9 and 10.

- [x] 11.1 New `internal/pgmigrate`: `go:embed` `db/migrations/postgres/*.up.sql` (via `db/migrations`, since
      go:embed cannot reach a parent directory), apply in **numeric** order, recording versions in a ledger
      that is **read** before each decision. Each file carries its own `BEGIN;…COMMIT;`, so it is executed
      whole. The DDL is bare `CREATE TABLE`, so idempotence comes from the ledger, never from the DDL
      (PRD FR25). *(Test: `internal/pgmigrate` — order, self-transactionality, every migration records
      itself, no `.down.sql` embedded. Live: 19 applied to an empty database, then `already current (19)`.)*
- [x] 11.2 Wire a Postgres pool into `internal/launch` from `DATABASE_URL` — the name `cmd/legalretention`
      already uses for the same database. Declared-and-unreachable **fails closed at boot**; the DSN is kept
      out of the error, because a boot failure is the line most likely to be pasted into a ticket. An unset
      DSN is a supported single-binary form, not an error.
- [x] 11.3 `/readyz` probes the Postgres it names; the local ledger is reported as `ledger`. The
      display-name-only `HEROS_DATASTORE_NAME` is gone (PRD FR26). *(Live: with Postgres stopped, `/readyz`
      → HTTP 503, `status: degraded`, `degraded_components: ["postgres"]`, detail `unreachable: driver: bad
      connection`. The renamed ledger ping it replaced could not go red at all.)*
- [x] 11.4 Register **every** capability surface in `internal/launch`. Sourced where a durable store exists —
      P2 read views (worktree/executor/variantspec), P10 + matrix (registry), P23 consent (legal PG store +
      the manifest origin **derived** from `CONSOLE_HEALTH_URL`); **nil source** everywhere else, so the
      answer is 503 not-mounted rather than 404 (PRD FR27). *(Tests: `internal/launch` — every route
      registered, unsourced ones 503, a source-level fence that every `Mount*`/`Register*` is called.
      Red-checked by removing one call. Live: `/api/p4/…` 503, `/api/nope` still 404.)*
- [x] 11.5 `SetBillingRollout` / `SetBillingCapability` — **NOT wired, and the reason is recorded**: billing
      has exactly one `Ledger` and one `account.Store` implementation and both are in-memory, so a wired
      billing capability would take a payment and forget it on restart. `MountBillingWebhook` stays
      unmounted for the same reason and because `internal/api/p21.go` rules that the one
      inbound-from-internet path is mounted only where a deployment exposes it. Left as PRD Q6 / the
      `noDurableStore` rows in the boot table, not papered over. *(Test:
      `TestBillingWebhookIsNotPublishedWithoutBilling` keeps the exception deliberate.)*
- [x] 11.6 Extend the environment contract on **both** substrates and in both `.env` examples: `DATABASE_URL`,
      `QDRANT_API_KEY`, `NEO4J_PASSWORD`, `HEROS_INBOX_SIGNING_KEY`, `HEROS_EDITION`/`HEROS_VERSION`,
      `CONSOLE_CONSENT_GATE`, `HEROS_CONTENT_ROOT`, `HEROS_SLUG_MANIFEST`, `HEROS_RELEASE_OFFLINE`, the
      `ADMIN_*` identity set, `CONSOLE_IDP_SECRET_MAP`/`CONSOLE_IDP_TIMEOUT_MS`, and
      `ADMIN_CONSOLE_HEALTH_URL` in Compose (only Kubernetes had it). 🔴 **P24's reporting switches are
      deliberately excluded** — `internal/deploy`'s P24 task 7.4 fence forbids naming them in any manifest,
      and it caught this change trying to (PRD FR30's stated exception).
- [x] 11.7 `cmd/legalretention` ships as a scheduled unit on both substrates: a `--profile jobs` one-shot on
      Compose, a weekly `CronJob` on Kubernetes, both `-window 0` (refuses, reports) with `-apply` absent.
      Built into the agentd image — one image set, second entrypoint. Its NetworkPolicy egress and
      Postgres's ingress were added with it: under default-deny a job with no rule fails quietly at 04:23
      on a Sunday (PRD FR31).
- [x] 11.8 Kubernetes `agentd`: `replicas: 1`, a PersistentVolumeClaim, and `Recreate` (a ReadWriteOnce claim
      cannot be held by two pods, so a rolling update would deadlock). The false "durable state is Postgres"
      comment is gone, the PDB is `minAvailable: 0` with the reason stated — a budget that can never be met
      blocks the drain forever rather than protecting anything. **The `prod`, `staging`, `airgapped` and
      `dev` overlays were re-patched too**: each pushed agentd back to 2–3 replicas, which would have
      re-opened the split ledger.
- [x] 11.9 New `scripts/deploy/check-env-parity.sh` — the environment-contract half of Decision 2 that
      `check-image-parity.sh` never covered — wired into `make deploy-lint` (PRD FR29). It found real
      pre-existing divergence on first run: `ADMIN_CONSOLE_HEALTH_URL`, seven customer-identity variables,
      the session TTL, the upstream timeout, `HEROS_SECRETS_DIR`, and both provider-credential names —
      the last of which meant the **airgapped overlay set `HEROS_SECRETS_SOURCE=env` against a base that
      declared no provider credentials at all**. Red-checked with an injected compose-only variable.
- [x] 11.10 `deploy/README.md` gains a "What a fresh install actually serves" table (served /
      registered-but-unsourced, with the reason for each), a "provisioned ahead of use" note on the four
      backing services no process dials yet, the one-replica/`Recreate` explanation, a Retention section,
      and the reminder that **agentd's ledger is a second thing to back up** and is not covered by the
      Postgres dump (PRD FR28, FR32).

## Verification record
- [ ] V1 M15 exit checklist (PRD §13) fully green on all three deployment forms.
- [x] V2 Deployment-form impact matrix attached to every P19 PR.
