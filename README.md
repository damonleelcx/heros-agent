# LLM Agentic Workflow Optimization Platform

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

Point it at a codebase; it discovers the LLM call graph, and **opens verified pull requests that
optimize your prompts, models, context strategies, and node wiring** — with statistical proof the
change is better or cheaper before you merge.

Think **"Dependabot for LLM cost & quality."** You review a diff and merge; the platform never
touches production behavior without evidence.

> **Status: foundation + full design.** This repository currently contains the **complete design**
> (implementation timeline, per-phase PRDs, and OpenSpec change sets) plus a **minimal Go service
> foundation**. The subsystems below are specified and being built phase by phase (P0 → P12). It is
> being repurposed from a prior *Heros OS-level agent* project; see
> [`docs/reproposal-migration-checklist.md`](docs/reproposal-migration-checklist.md) for what was
> kept, adapted, and removed.

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
- **[Product Requirements Documents](docs/prd/README.md)** — one PRD per phase (P0 → P12).
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

## Repository layout (today)

```
cmd/agentd/            # service entrypoint — boots the HTTP server
internal/
  api  launch          # minimal HTTP server: /healthz, /readyz (auth-gated /api/*)
  auth  config  db     # API-key auth, config, SQLite ledger
  providergateway      # OpenAI-compatible provider client (seed of the LiteLLM-style gateway, P2)
  toolcontract         # tool JSON-schema + error taxonomy (Skill Registry contract seed, P2/P3)
  agentlayout  skillindex  toolindex   # registry foundations
  embeddings           # failure-clustering / RAG seed (P3/P4.5)
  approval  sqltime    # human-in-the-loop gate seed, helpers
docs/                  # implementation-timeline, prd, adr, migration checklist
openspec/              # spec-driven change sets (P0–P12)
```

The current `internal/` packages are the reusable foundation kept from the migration; the phase
subsystems (Discovery, Config Layer, Runtime, Eval, …) are built on top of them per the plan.

## Getting started

Requires **[Go](https://go.dev/dl/) 1.22+**.

```bash
# build and test everything
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
