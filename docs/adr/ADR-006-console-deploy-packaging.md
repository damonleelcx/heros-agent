# ADR-006 — The console ships as its own container in the same deployment unit, not as a second process under a supervisor

- **Status:** Accepted (2026-07-24)
- **Deciders:** System Design + DevOps (proposed) + User (ratified)
- **Resolves:** [`openspec/changes/p9-web-console/tasks.md`](../../openspec/changes/p9-web-console/tasks.md)
  §2.1, and PRD [P9](../prd/P9-web-console.md) §14 Q2, which fixed the *requirements* of
  `design.md` Decision 6 (declared, supervised, health-checked, readiness-aggregated, pinned runtime,
  lockfile-reproducible deps) and deliberately left the packaging to implementation time.
- **Relates to:** `design.md` Decision 1 — this ADR is the **payment schedule** for the operational
  cost that decision explicitly accepted, not a re-litigation of it.
- **Owns:** phase **P9 — Web Console**, and by precedent the P8 operator console, which has the same
  shape and the same question.

## Context

Decision 1 put a Node process in the request path in order to hold the platform credential
server-side. It arbitrated **L1 安全 over L4 运维** and said so plainly: *"the 运维 cost is accepted and
priced, not waved away."* Decision 6 then wrote the price — one declared, supervised, health-checked
component whose health the platform's readiness signal aggregates — and stopped short of saying how it
is packaged, because that is a question about the deployment substrate rather than about the
architecture.

There are two candidate packagings, and they are not equivalent:

1. **One container, two processes under a supervisor** (`s6`, `supervisord`, `tini` + a shell
   wrapper). One image, one log stream, one thing to deploy.
2. **Two containers in one deployment unit** — one compose service / pod, two containers, each with
   its own image, its own probes and its own restart policy.

Today the repository ships stacks as compose files (`deploy/docker-compose.enterprise.yml`,
`…sandbox.yml`, `…discovery.yml`), each service with its own image and its own pinned tag. Neither
console currently has a deployment artifact at all.

## Decision

**The console ships as its own container, in the same deployment unit as the platform service.** One
compose service (or one pod) contains two containers: `agentd` and `console`. Each is independently
built, independently probed, independently restarted, and independently versioned. Neither runs a
supervisor.

Concretely:

- **Declared.** The console is a named service in the deployment manifest. It is not started by a
  script, an entrypoint side effect, or another process.
- **Supervised.** By the orchestrator — the same mechanism that already restarts `agentd`. The
  console's restart policy, backoff and resource limits are the orchestrator's, not a supervisor's.
- **Health-checked.** The console container exposes its own liveness/readiness endpoint, probed
  directly by the orchestrator.
- **Readiness-aggregated.** Separately and additionally, the platform's `/readyz` reads the console's
  health and reports **not ready** when it is unreachable, naming the degraded component
  (`console-bff` spec; `internal/api/server.go`). Note that this is **not** a consequence of the
  packaging — it works identically under either option — so packaging must be decided on other
  grounds. It is stated here only so a future reader does not conclude it was the reason.
- **Pinned runtime.** The base image is a digest-pinned Node **22 LTS** (Next.js 15 requires ≥ 18.18;
  22 is the LTS line with the longest remaining support at the time of this decision). A floating
  `node:22` tag is not acceptable — a tag that moves makes the build irreproducible, which is the
  property NFR9 exists to buy.
- **Lockfile-reproducible.** `npm ci` against the checked-in `package-lock.json`, never `npm install`,
  which is permitted to resolve differently.

## Why — the arbitration

**L2 稳定 decides it, and the reason is a health signal, not convenience.**

A supervisor inside one container makes the container's liveness a **lie about its contents**. If the
Node process dies and the supervisor keeps the container alive — or restarts it in a tight loop — the
orchestrator sees a healthy container, the restart policy never fires, the rollout never fails, and the
only symptom is that the surface users reach is down. That is precisely the failure mode 🔴
`health-signal-surface` names: *a readiness endpoint that reports ready while the surface users
actually reach is dead is a lying health signal.* Two containers make a dead process a **dead
container**, which every layer above already knows how to see and act on.

**L4 运维 agrees, which makes the decision unanimous rather than a trade.** A supervisor is a
second, hand-rolled implementation of what the orchestrator already does — restart policy, backoff,
health probing, signal forwarding, PID-1 zombie reaping, log multiplexing — and each of those is a
known source of subtle production failure (a `SIGTERM` that never reaches the child, so every deploy
takes the full termination grace period; interleaved unstructured logs from two processes on one
stream). The customer's DevOps team can operate two containers with knowledge they already have; they
cannot operate our supervisor configuration without learning it.

**L8 实现 is where the one-container option wins**, and L3 铁律 is explicit that the lowest level never
outranks anything above it. One image is marginally simpler to build and to publish. That is not a
reason.

## Alternatives rejected

**One container with a supervisor.** Rejected above on L2. It is worth naming the one case that would
reopen it: a deployment target that genuinely cannot run two containers in one unit (some
single-container PaaS offerings). If that target becomes a requirement, the answer is not a supervisor
by default — it is a supervisor **for that target only**, with the health signal explicitly re-derived,
because the failure mode above does not go away by being necessary.

**Two containers in two separate deployment units.** More isolation, and it is how the P8 operator
console is deployed (deliberately, on a separate origin — P8 Decision 11). Rejected here on **L4**:
the customer console's BFF only ever talks to the platform service, and separating the units means
their versions can skew. The generated type contract (ADR-007) makes a skew a **runtime** mismatch
rather than a build failure, and co-locating the units removes the class.

**Embedding the console into the Go binary with `go:embed`.** Already rejected as `design.md`
Decision 1 — a static bundle cannot hold a secret. Recorded here only so this ADR is not read as
reopening it.

## Consequences

- The deploy artifact gains one service definition and one Dockerfile. `deploy/` grows a compose file
  for the console pair; nothing about the existing stacks changes.
- Two images are built and published per release instead of one, and their versions must be released
  together. This is the cost, and it is bounded: they are in the same repository, built by the same
  pipeline, and pinned in the same manifest.
- A future third console-side process (there is no requirement for one) inherits this decision rather
  than reopening it.
