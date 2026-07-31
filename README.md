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
- **[Product Requirements Documents](docs/prd/README.md)** — one PRD per phase, grouped by program, with
  each phase's lead roles and the rationale for its band. **This is the per-phase source of truth**; the
  table below is a map, not a second copy of it.
- **[OpenSpec change sets](openspec/)** — behavioral, testable specs (`SHALL` requirements with
  scenarios); see [`openspec/AGENTS.md`](openspec/AGENTS.md) for the format and
  [`openspec/project.md`](openspec/project.md) for conventions.

### Phases

Thirty phases in nine programs. Each row links to the program's phases; the
[PRD index](docs/prd/README.md) carries the per-phase detail.

| Program | Phases | Delivers |
|---|---|---|
| **Foundations & Discovery** | [P0](docs/prd/P0-foundations.md) · [P1](docs/prd/P1-discovery-mvp.md) · [P2](docs/prd/P2-config-runtime.md) · [P2.5](docs/prd/P2.5-metrics-observability.md) · [P3](docs/prd/P3-context-skills-sandbox.md) · [P3.5](docs/prd/P3.5-pattern-classifier.md) | Workflow IR, metric-event schema and lineage; multi-language static analysis (Go via `go/ast`, the rest via tree-sitter); the source-transformation engine and runtime; the OpenTelemetry substrate everything else measures against; context strategies, the Skill Registry, the sandbox, and the pattern classifier |
| **Evaluation & Improvement** | [P4](docs/prd/P4-eval-harness.md) · [P4.5](docs/prd/P4.5-attribution-diagnosis.md) · [P5](docs/prd/P5-contracts-rearrange-tracing.md) · [P5.5](docs/prd/P5.5-proposals-verification.md) · [P6](docs/prd/P6-autonomous-optimizer.md) | The eval harness, eval-set generation and scoring; attribution and diagnosis; typed I/O contracts, re-arrangement and dynamic tracing; the proposal operators and the **verification gate** that decides; and the autonomous optimizer that runs the loop |
| **Commerce & Consoles** | [P7](docs/prd/P7-billing-metering.md) · [P8](docs/prd/P8-admin-console.md) · [P9](docs/prd/P9-web-console.md) · [P10](docs/prd/P10-prompt-model-studio.md) | Billing, metering and entitlements; the internal **operator** console (RBAC, tenant/billing admin, fleet controls, audit log); the customer-facing **web** console (Next.js + BFF, no API key in the browser); and the Prompt & Model Studio |
| **Distribution Surfaces** | [P11](docs/prd/P11-cli-ci-integration.md) · [P12](docs/prd/P12-forge-delivery.md) | The offline-first CLI, free on every plan, with opt-in run linking that gives SUM metering its input; and forge delivery — the optimization PR, CI-mediated by default |
| **Optimization Axis Expansion** | [P13](docs/prd/P13-prompt-model-optimization.md) · [P14](docs/prd/P14-skills-tools-optimization.md) · [P15](docs/prd/P15-workflow-wiring-optimization.md) · [P16](docs/prd/P16-context-strategy-optimization.md) · [P17](docs/prd/P17-memory-strategy-optimization.md) · [P18](docs/prd/P18-harness-strategy-optimization.md) | Six axes: prompt & model, skills & tools, node wiring, context strategy, memory, harness. Each takes a dimension the IR already models and makes it a *verified, applicable* optimization axis — scored by the **axis-agnostic** harness, under the same "diagnosis proposes, verification decides" gate |
| **Deployment & Packaging** | [P19](docs/prd/P19-deployment-delivery.md) · [P20](docs/prd/P20-installable-packages.md) | The platform as something you can stand up (Docker Compose, Kubernetes, air-gapped); and the `heros` CLI as installable packages — GitHub-Release pipeline, native install channels, verification before the binary reaches `PATH`, onboarding and self-update |
| **Identity & Payments** | [P21](docs/prd/P21-stripe-payments.md) · [P22](docs/prd/P22-sso-identity.md) | Real Stripe behind the P7 `billing.Provider` interface — checkout, metered usage, idempotent signature-verified webhooks, entitlement sync; and SSO — customer OIDC/SAML behind the ADR-008 seam, plus operator SSO + MFA made real |
| **Published Word** | [P23](docs/prd/P23-legal-and-developer-docs.md) | The two read-not-computed surfaces: Terms and Privacy Notice as versioned artifacts with append-only consent records, and three-tier developer documentation — both held honest by build-time accuracy fences |
| **Seeing the System** | [P24](docs/prd/P24-analytics-and-error-monitoring.md) · [P26](docs/prd/P26-operator-console-refresh.md) | Product analytics and error monitoring, installed under a per-prefix origin fence that keeps every tenant surface at `default-src 'self'`; and the operator-console refresh, whose product is a **build fence** that makes oversight drift fail rather than accumulate |

There is no **P25** — the token `p25` already denotes P2.5 in this repository (`/p25/monitor`, the Gantt
id, `internal/api/monitor.go`), so reusing it would make it ambiguous exactly where someone greps during
an incident.

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

<!-- BEGIN GENERATED INSTALL SECTION — `make readme-install` regenerates it -->

### Install

`heros` is one self-contained binary. `discover`, `apply`, `eval`, `doctor` and `init` work **offline with no account** — there is nothing to sign up for to get a first result.

**curl | sh** — darwin, linux

```sh
curl -fsSL https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.20.0/scripts/install.sh | sh
```

**PowerShell (irm | iex)** — windows

```sh
irm https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.20.0/scripts/install.ps1 | iex
```

**.deb package** — linux

```sh
curl -fsSLO https://github.com/damonleelcx/heros-agent/releases/download/v0.20.0/heros_0.20.0_amd64.deb && sudo dpkg -i heros_0.20.0_amd64.deb
```

**.rpm package** — linux

```sh
sudo rpm -i https://github.com/damonleelcx/heros-agent/releases/download/v0.20.0/heros-0.20.0-1.x86_64.rpm
```

**container image** — darwin, linux, windows

```sh
docker run --rm -v "$PWD:/repo" ghcr.io/damonleelcx/heros:0.20.0 discover --repo /repo
```

#### Auditing the install script before you pipe it

The URL above is pinned to the `v0.20.0` tag, so it cannot change under you, and the script is covered by the same signed manifest as the binaries. To read it before running it:

```sh
curl -fsSLO https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.20.0/scripts/install.sh
# then compare its sha256 against the install.sh line in that release's SHA256SUMS
less install.sh && sh install.sh
```

#### Verifying a release yourself (offline, no account)

Every channel above performs these two steps for you and **refuses to place the binary on your PATH** if either fails. To run them by hand:

```sh
sha256sum -c SHA256SUMS          # or: shasum -a 256 -c SHA256SUMS
ssh-keygen -Y verify -f allowed_signers -I heros-release \
  -n file -s SHA256SUMS.sshsig < SHA256SUMS
```

`allowed_signers` ships with the release; the same key is published as [`docs/release/heros-release.pub`](docs/release/heros-release.pub) for the raw-ed25519 path. Neither step needs a network or an account. `ssh-keygen` rather than `openssl` because stock macOS ships LibreSSL, which cannot verify ed25519 at all — the same signature is published in both encodings.

The full story — installing, upgrading, rolling back, what the first-run OS warning means, and how the release key rotates — is [`docs/release/install.md`](docs/release/install.md).

#### Not supported — stated, because a blank reads as *should work*

- **Windows 11 (arm64)** — not built — no native windows/arm64 runner in the matrix, and the CGO tree-sitter frontends make a cross-build a different, less-tested artifact (D1). Instead: run the windows/amd64 build under Windows' x64 emulation, or ask for the row: adding it is a new runner, not a redesign.
- **Alpine / any musl Linux** — no native musl binary — the CLI links CGO tree-sitter frontends against glibc, and a glibc binary does not run on musl (D6). Instead: use the container image ghcr.io/damonleelcx/heros:<version>, which carries the same CLI in a glibc base.

Also generated but **not yet installable**, and listed so nobody plans around them:

- **Homebrew** — the formula is generated from the signed manifest and attached to every Release, but the tap repository heros-foreal/homebrew-tap does not exist yet and pushing to it needs a token secret. Until then `brew install heros-foreal/tap/heros` would fail.
- **Scoop** — the manifest is generated and attached to every Release, but the bucket repository heros-foreal/scoop-bucket does not exist yet and pushing to it needs a token secret.
- **winget** — the three-file winget manifest is generated and attached to every Release, but publication is a pull request into microsoft/winget-pkgs whose review and merge are not ours to schedule.

#### What is free, and what is not

The **CLI is free, with no account, forever**: `discover`, `apply`, `eval`, `coverage`, `doctor`, `init`, `version` and `upgrade` all run locally and send no telemetry.

The **paid upgrade is the hosted platform**: `heros login` and `heros link` push run results to a tenant, which is what buys the console — leaderboards across runs, attribution scorecards, autonomous proposals and pull requests, and team-wide history. Nothing in the free path is degraded to sell it, and no local command starts requiring an account later.

<!-- END GENERATED INSTALL SECTION -->

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
go run ./cmd/proof/customerconsole -repo /path/to/hermes-agent      # serves the platform API on :4321
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
