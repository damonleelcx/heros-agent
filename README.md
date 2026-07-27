# LLM Agentic Workflow Optimization Platform

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

Point it at a codebase; it discovers the LLM call graph, and **opens verified pull requests that
optimize your prompts, models, context strategies, and node wiring** — with statistical proof the
change is better or cheaper before you merge.

Think **"Dependabot for LLM cost & quality."** You review a diff and merge; the platform never
touches production behavior without evidence.

> **Status: platform implemented and deployable (P0 → P12, P19); optimization-axis expansion
> (P13 → P18) and the commercial/distribution phases (P20 → P22) in design.** The four core
> subsystems and all three delivery surfaces are built:
> Discovery, the source-transformation Config Layer, the sandboxed Runtime, the Eval Harness, the
> attribution/diagnosis/verification engine, the autonomous optimizer, billing & metering, the
> admin and customer consoles, the offline `heros` CLI, and CI-mediated forge delivery all live under
> [`internal/`](internal/) and are demoable end-to-end through the `cmd/pNhermes` walkthroughs. The
> whole platform now **stands up from one digest-pinned image set** on Docker Compose or Kubernetes
> ([`deploy/`](deploy/), see [P19](docs/prd/P19-deployment-delivery.md)), and the pipeline has been
> run **live against a real 3,333-file repo** (a full end-to-end proof lands with the E2E-proof PR).
> The **complete design**
> (implementation timeline, per-phase PRDs, and OpenSpec change sets) is committed alongside. The
> current frontier is the **Optimization Axis Expansion** (P13 → P18) — turning each modeled dimension
> into an applicable optimization axis. The repo was repurposed from a prior *Heros OS-level agent*
> project; see [`docs/reproposal-migration-checklist.md`](docs/reproposal-migration-checklist.md) for
> what was kept, adapted, and removed.

## How it works

```
repo ─▶ Discovery ─▶ Workflow IR ─▶ Config Layer ─▶ Variant Spec
                         │                              │
                   Pattern Classifier            Runtime (sandboxed)
                                                        │
                    Eval Harness ◀── Traces + Metrics ──┘
                         │
             Analysis & Improvement Engine
                         │
            verified optimization  ─▶  Pull Request
```

1. **Discovery** — static (+ dynamic) analysis extracts every LLM call site into a canonical
   **Workflow IR** (a graph of nodes with precise source spans, models, prompts, tools, context).
2. **Configure** — each node's model / prompt / skill / context becomes an override; a full config
   is a **Variant Spec**.
3. **Apply by source transformation** — a Variant Spec is realized as a **deterministic AST codemod**
   (a reviewable diff), never a runtime shim. See
   [`docs/adr/ADR-001-source-transformation-apply-model.md`](docs/adr/ADR-001-source-transformation-apply-model.md).
4. **Run & measure** — the transformed working copy runs sandboxed, fully traced (OpenTelemetry).
5. **Evaluate** — variants run over generated + user eval sets, multi-seed, with confidence
   intervals; a leaderboard ranks them under a weighted objective.
6. **Diagnose & optimize** — attribution localizes failures, diagnosis explains them, and change
   operators propose fixes — **each verified on held-out data before it is surfaced.**
7. **Deliver** — the optimization ships as a **pull request**; git is the audit trail and
   `git revert` is rollback.

**Core principle:** *diagnosis proposes, verification decides.* No unverified LLM opinion drives an
automated change, and multi-seed statistics guard against ranking noise.

## Delivery surfaces (planned)

Not a desktop app — a cloud service reached through three thin surfaces:

| Surface | Role |
|---|---|
| **CLI + CI integration** — [P11](docs/prd/P11-cli-ci-integration.md) | Primary developer entry point; runs discovery / codemod / eval **in your own build environment** with **your own provider keys**. Free on every plan, works offline with no account; linking a run is explicit, opt-in, and sends metrics and structure — never source, prompts, or keys |
| **Git App / bot** (GitHub / GitLab / Bitbucket) — [P12](docs/prd/P12-forge-delivery.md) | Opens the optimization **PRs**, posts checks and comments. By default your **own CI** opens the PR with the token it already holds, so the platform never gets write access to your repo; a hosted Git App is the opt-in alternative |
| **Web dashboard** (hosted SaaS) — [P9](docs/prd/P9-web-console.md) | Graph view, leaderboard, diagnosis, trends, budgets, and automation-level governance. Scoped to one tenant; the platform credential stays server-side in a BFF, so no API key reaches the browser |

## The plan

The full engineering plan is committed and specified:

- **[Implementation timeline](docs/implementation-timeline/README.md)** — system overview, critical
  path, role-ownership matrix, Gantt, and milestones (M0 → M15).
- **[Product Requirements Documents](docs/prd/README.md)** — one PRD per phase (P0 → P22).
- **[OpenSpec change sets](openspec/)** — behavioral, testable specs (`SHALL` requirements with
  scenarios); see [`openspec/AGENTS.md`](openspec/AGENTS.md) for the format and
  [`openspec/project.md`](openspec/project.md) for conventions.

### Phases

| Phase | Delivers |
|---|---|
| **P0** | Foundations — Workflow IR + metric-event schema + storage/lineage |
| **P1** | Discovery MVP (multi-language static analysis — Go via `go/ast`, other languages via tree-sitter) |
| **P2** | Config Layer (source-transformation engine) + Runtime |
| **P2.5** | Metrics & Observability substrate (OpenTelemetry) |
| **P3** | Context strategies + Skill Registry + Sandbox |
| **P3.5** | Pattern Classifier |
| **P4** | Eval Harness + eval-set generation + scoring |
| **P4.5** | Attribution + Diagnosis |
| **P5** | Typed I/O contracts + Re-arrangement + Dynamic tracing |
| **P5.5** | Proposal operators + Verification gate |
| **P6** | Autonomous optimizer |
| **P7** | Billing, Metering & Entitlements |
| **P8** | Admin & Operations Console (internal operator surface — RBAC, tenant/billing admin, fleet controls, audit log; a separate Next.js app on its own origin) |
| **P9** | Web Console (the customer-facing dashboard — Next.js + BFF, one design system, no API key in the browser) |
| **P10** | Prompt & Model Studio (prompt authoring + versioning, variable bindings, per-node model/prompt selection, runtime config binding) |
| **P11** | CLI & CI Integration (offline-first CLI on every plan; opt-in run linking that gives SUM metering its input) |
| **P12** | Forge Delivery (the optimization PR — CI-mediated by default, hosted Git App opt-in; the gainshare input) |
| **P13** | Prompt & Model Optimization (deepening the one axis that already ships) |
| **P14** | Skills & Tools Optimization (making the skill axis apply, and splitting tools from skills) |
| **P15** | Workflow / Node-Wiring Optimization (turning the graph's shape into an optimization axis) |
| **P16** | Context Strategy Optimization (making the richest modeled axis actually applicable) |
| **P17** | Memory Strategy Optimization (what an agent remembers becomes a tunable dimension) |
| **P18** | Harness Strategy Optimization (the scaffold around a node becomes a tunable dimension) |
| **P19** | Deployment & Delivery (the platform as a thing you can stand up) |
| **P20** | Installable Packages & Self-Serve Distribution (GitHub-Release pipeline; native installers; signature-verified before `PATH`; onboarding + self-update) |
| **P21** | Stripe Payments (real Stripe behind the P7 `billing.Provider` interface — checkout, metered usage, idempotent signature-verified webhooks, entitlement sync) |
| **P22** | SSO & Identity (customer OIDC/SAML behind the ADR-008 seam; operator SSO + MFA made real) |

Phases **P13 → P18** form the **Optimization Axis Expansion**: each takes a dimension the IR already
models and makes it a *verified, applicable* optimization axis under the same "diagnosis proposes,
verification decides" gate.

## Repository layout (today)

```
cmd/
  agentd               # service entrypoint — boots the HTTP server
  heros                # the offline-first customer CLI (P11)
  pNhermes / pNdemo    # per-phase end-to-end walkthroughs against the hermes demo repo
internal/
  discovery  irwriteback                 # LLM call-graph extraction → Workflow IR (P1)
  transform  variantspec  config*        # source-transformation codemod + Variant Spec (P2)
  executor  nodeexec  sandbox  runqueue  # sandboxed, traced Runtime (P2/P3)
  providergateway  toolcontract  skillindex  toolindex  registry   # gateway + Skill Registry
  telemetry  metricevent                 # OpenTelemetry substrate (P2.5)
  patternclassifier                      # agentic-pattern labelling (P3.5)
  evalgen  evalharness  evalrun  evalstats  scoring  scorecard  evalboard   # Eval Harness (P4)
  attribution  attrengine  diagnosis  proposal  verification   # analysis & improvement (P4.5/P5.5)
  arrangements  typedcontract  dynamictracing   # re-arrangement + typed I/O + dynamic tracing (P5)
  optimizer                              # autonomous closed-loop optimizer (P6)
  billing  metering  entitlement  account   # billing, metering & entitlements (P7)
  admin*  adminaudit  adminrbac  adminops    # admin & operations console backend (P8)
  linkage  linkingest  runlink  clilink  cli # CLI + opt-in run linking (P11)
  forgedelivery  deliveryrecord  submit  reconcile   # CI-mediated forge delivery (P12)
  studio  broker  approval  auth  db  launch  api    # studio, HITL gate, service core
docs/                  # implementation-timeline, prd (P0–P22), adr, decisions, migration checklist
openspec/              # spec-driven change sets (P0–P22)
web/console            # the customer-facing Next.js dashboard + BFF (P9)
```

The four core subsystems (Discovery, Config Layer, Runtime, Eval Harness), the cross-cutting
engines (metrics, analysis/improvement, pattern classifier, billing), and all three surfaces are
implemented; `GOWORK=off go test ./...` runs green across the tree.

## Getting started

Requires **[Go](https://go.dev/dl/) 1.24+**.

```bash
# build and test everything
# (prefix GOWORK=off if a parent go.work workspace is present)
go build ./...
go test ./...

# run the service (defaults to 127.0.0.1:8787)
go run ./cmd/agentd

# in another terminal
curl -s http://127.0.0.1:8787/healthz   # {"status":"ok"}
curl -s http://127.0.0.1:8787/readyz    # {"status":"ready"}
```

Configuration is optional (sensible defaults are used). Pass `-config <path>` to point at a JSON
config; the SQLite ledger and state live under the configured `data_dir`.

### The CLI (`heros`) — free on every plan, offline, no account

`cmd/heros` is the customer-installed CLI (P11). It runs discovery, the codemod, and eval **in your own
environment with your own provider keys** — with **no account and no network**:

```bash
go build -o heros ./cmd/heros

heros discover --repo /path/to/repo         # → Workflow IR + discovery report
heros apply    --repo /path/to/repo --spec variant.json   # → reviewable diff (worktree-isolated)
heros eval     --repo /path/to/repo --seeds 5 --min-quality 0.7   # → scored, multi-seed, gate-aware
heros status                                # effective config + where each value came from
heros version                               # tool + contract versions
```

Machine output goes to **stdout** in a stable, versioned JSON envelope; human narration goes to
**stderr**. Exit codes are a contract: `0` ok · `1` a configured gate failed · `2` operational error ·
`3` invalid config.

**Linking is opt-in and explicit.** `heros link` transmits a run's **allowlisted** metrics + structure
(never source, prompts, or provider keys) to **`https://heros-agent.space`** and nowhere else. See
exactly what would be sent, without sending it:

```bash
heros login --token "$HEROS_PLATFORM_TOKEN"
heros link --run <run-id> --dry-run    # prints the exact payload; transmits nothing
heros link --run <run-id>              # transmits it; prints a dashboard URL
```

The egress allowlist, output format, and exit codes are referenceable contracts in
[`docs/decisions/p11-contracts.md`](docs/decisions/p11-contracts.md); release verification is in
[`docs/release/cli-verification.md`](docs/release/cli-verification.md).

### The web console

The console is a **separate component** — a Next.js app with its own backend-for-frontend — rather
than a page the Go service embeds. It requires **Node 22+**, and it runs beside `agentd`:

```bash
cd web/console
npm ci

PLATFORM_API_BASE=http://127.0.0.1:8787 CONSOLE_PLATFORM_CREDENTIAL=<the platform API key> CONSOLE_TENANT_IDENTITY=dev   npm run dev            # http://127.0.0.1:4320
```

🔴 **The browser never receives `CONSOLE_PLATFORM_CREDENTIAL`.** It is read by the BFF process and
nothing else: the browser gets an `HttpOnly` session cookie it cannot read, every upstream call is made
server-side, and the request's scope is derived from the session's tenant rather than from anything the
client sends. `npm run build` fails if credential material reaches the shipped bundle — that is a
build gate, not a review habit.

`CONSOLE_TENANT_IDENTITY=dev` is a **development-only** identity provider and refuses to start under
`NODE_ENV=production`, so it cannot be shipped by accident.

To see it against a real repository with no provider account, point it at the hermes demo:

```bash
go run ./cmd/p9hermes -repo /path/to/hermes-agent      # serves the platform API on :4321
```

The console's own checks run with `npm test` (214 cases) and `npm run build`, which runs the token,
string, markup, claim and bundle scans around `next build`.

## Deploying (P19)

The whole platform stands up as **one deployment unit** from **one digest-pinned image set**
([`deploy/images.env`](deploy/images.env)), on either of two substrates that reference the *same*
digests and the *same* environment contract:

- **Docker Compose** — a single host; the open-core / evaluation path.
- **Kubernetes (Kustomize)** — a cluster; the managed and enterprise path, with `dev` / `staging` /
  `prod` / `airgapped` overlays and external-secret references (values never live in the repo).

```bash
cp deploy/images.env            deploy/.env.images     # the digest-pinned image set
cp deploy/.env.platform.example deploy/.env.platform   # then fill from YOUR secret store
docker compose --env-file deploy/.env.images --env-file deploy/.env.platform \
  -f deploy/docker-compose.platform.yml up -d
```

> **One deployment = one tenant boundary.** Isolation between customers is deployment-level, not
> software multi-tenancy — a hosted concern, not a knob inside a shared install.

`make deploy-lint` fails the build if the Compose and Kubernetes topologies ever diverge or if a
plaintext secret or unpinned image slips in. The full runbook — capacity ranges, the control-plane /
data-plane split, and the single point of failure named beside its backup precondition — is
[`deploy/README.md`](deploy/README.md). A **live end-to-end proof** of the pipeline run against the
real `nousresearch/hermes-agent` repo — 40 discovered nodes, honest codemod refusals, gate-enforced
eval, both consoles — is captured under `docs/release/p19-e2e-hermes/`, which lands with the
E2E-proof PR.

## Contributing

Work proceeds phase by phase against the OpenSpec change sets. Before implementing a phase, read its
PRD (`docs/prd/`) and its OpenSpec change (`openspec/changes/`); every requirement is behavioral and
carries at least one testable scenario. Keep `go build ./...`, `go vet ./...`, and `go test ./...`
green.

## License

Licensed under the **Apache License, Version 2.0** — see [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).

Apache-2.0 is a permissive license with an explicit patent grant, chosen so the discovery/CLI layer
can be adopted freely and become the ecosystem's ingestion standard. You may use, modify, and
distribute the code — including commercially — provided you retain the license and attribution
notices and state any changes.

Unless you explicitly note otherwise, contributions you submit are licensed under the same Apache-2.0
terms.
