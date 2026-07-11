# Re-proposal Migration Checklist — Heros → LLM Agentic Workflow Eval & Config System

This is the keep/adapt/remove decision for every part of the existing repository, mapped against
the new purpose (see [`implementation-timeline/README.md`](implementation-timeline/README.md) and
[`prd/`](prd/README.md)). Work top to bottom; the execution order is in §7.

> **Execution status (branch `reproposal/cleanup`).** §7 steps 1–7 are DONE and verified —
> `go build/vet/test ./...` all green, and `agentd` boots to serve `/healthz`+`/readyz`. See
> [§8 Execution log](#8-execution-log) for exactly what happened, including three reclassifications
> made during execution. Remaining: land P0 (net-new).

## Legend

| Verdict | Meaning |
|---|---|
| 🟢 **KEEP** | Reusable roughly as-is; small edits only |
| 🟡 **ADAPT** | Valuable foundation, but must be repurposed/rewritten toward a new subsystem |
| 🔵 **SALVAGE** | Extract a specific useful piece; discard the rest of its package |
| 🔴 **REMOVE** | Old-product-specific; delete |
| ⚪ **ADD** | Net-new; does not exist yet (tracked in the PRDs/OpenSpec) |

**Scale of the change:** the existing tree is an *OS-level personal/organizational agent* (interactive
CLI + Fyne desktop GUI + MCP sidecar + memory vault + org "collective" sync + skill evolution). The
new system is a *server-side platform* that discovers, configures, runs, scores, and optimizes other
codebases' LLM workflows. Most agent-runtime and desktop code goes; the HTTP/registry/provider/
embedding plumbing is worth keeping.

---

## 1. 🟢🟡🔵 Keep / Adapt / Salvage — the reusable foundation

| Path | LOC | Verdict | New role → phase | Action |
|---|---:|:---:|---|---|
| `cmd/agentd/main.go` | 43 | 🟡 ADAPT | Backend daemon entrypoint → **P2** | Convert to the Gin server bootstrap; drop enterprise-runtime wiring |
| `internal/api/` | 1261 | 🟡 ADAPT | HTTP control plane → **P2+** | Reuse router/middleware/auth/static-serving wiring; **strip** agent/memory/collective/harness routes; add discovery/config/runtime/eval endpoints |
| `internal/auth/` | 155 | 🟢 KEEP | AuthN for the API → **P2** | Reuse HMAC/token auth as-is |
| `internal/config/` | 673 | 🟡 ADAPT | Service config → **P0/P2** | Keep the load/merge/env pattern; delete agent/desktop/enterprise fields; **remove `heros_desktop.go` + test** |
| `internal/db/` | 300 | 🟡 ADAPT | Data access → **P0** | SQLite ledger pattern is fine for dev; plan targets **Postgres** — introduce `pgx` and port schema, or keep SQLite for local only |
| `internal/sqltime/` | 22 | 🟢 KEEP | Time helper | Trivial util, reuse |
| `internal/approval/` | 220 | 🟡 ADAPT | Human-in-the-loop gates → **P6** | Repurpose into the Advisory/Assisted/**Autonomous** approval model |
| `internal/scheduler/` | 108 | 🟡 ADAPT | Run/job scheduling → **P4/P6** | Seed for the run-fan-out **queue**; likely superseded by a real queue |
| `internal/observability/` | 124 | 🔵 SALVAGE | Telemetry → **P2.5** | Concept only; **replace** with OpenTelemetry (GenAI semantic conventions) + the 7-tag event contract |
| `internal/cliagent/openai.go` + `stream.go` | ~2 files | 🔵 SALVAGE | **Provider gateway** → **P2** | Extract the OpenAI-compatible chat/stream client as the seed of the LiteLLM-style unified provider gateway. **Discard the rest of `cliagent/`.** |
| `internal/toolcontract/` | 83 | 🟢 KEEP | Skill-Registry contract → **P2/P3** | Tool JSON-schema + error taxonomy maps directly to the Skill Registry contract, tool-response contract, and typed I/O envelope |
| `internal/toolindex/` | 552 | 🟡 ADAPT | **Skill/Tool Registry** → **P2/P3** | `registry/scan/policy/scope` become the versioned Skill Registry with pre-execution schema validation |
| `internal/skillindex/` | 374 | 🟡 ADAPT | **Skill Registry** → **P2/P3** | Fold into the registry; keep the scan/index logic |
| `internal/promptlayer/store.go` (+ seed) | ~2 files | 🔵 SALVAGE | **Prompt Registry** → **P2** | Keep the prompt store/versioning; **delete `embedded_defaults/` (295 markdown skill files — old product content)** |
| `internal/embeddings/` | 153 | 🟢 KEEP | Failure clustering + RAG → **P3/P4.5** | Naive + OpenAI embedders reused for the RAG context strategy and failure-cluster embedding |
| `internal/harness/` | 1898 | 🟡 ADAPT (heavy) | **Runtime executor** → **P2/P5** | Salvage the graph-execution + critic-loop patterns, but it is hard-wired to a leader/follower/critic topology — **rebuild** for arbitrary Variant-Spec DAG execution over the transformed working copy in a sandbox |
| `internal/platform/` | 106 | 🔴 REMOVE* | — | It only wires the old enterprise deps (nats/neo4j/qdrant/memorylayer); rewrite as thin dependency wiring for the new stack |

\* keep the ~10-line "hold shared deps in a struct" shape if convenient; the contents go.

---

## 2. 🔴 Remove — old-product-specific code

These implement the interactive agent, desktop app, org-collective, and memory-vault concepts that
the new purpose does not have.

### Commands / entrypoints
- 🔴 `cmd/heros-desktop/` (+ `prefs*.go`, `theme*.go`, 1453 LOC) — Fyne GUI; new UI is React web
- 🔴 `cmd/heros/` and `cmd/heros-cli/` — interactive CLI agent
- 🔴 `cmd/heros-mcp/` — MCP host sidecar (old daily-driver interface)
- 🔴 `cmd/collectived/` — org "collective" daemon
- 🔴 `cmd/fleet-skill-worker/` — fleet skill worker

### Internal packages
- 🔴 `internal/cliagent/` (26 files, 4907 LOC) — REPL, filetools, localshell, intent, session, tools_runtime, contracts — **except** the salvaged `openai.go`/`stream.go` (§1)
- 🔴 `internal/collective/`, `internal/syncsnapshot/`, `internal/indexsync/` — org knowledge sync
- 🔴 `internal/fleetworker/` — fleet worker
- 🔴 `internal/memorylayer/`, `internal/memoryfs/`, `internal/memorytree/`, `internal/vaultindex/` (~2288 LOC) — agent memory vault
- 🔴 `internal/inbox/` — agent inbox
- 🔴 `internal/agentlayout/` — agent file layout
- 🔴 `internal/launch/` — daemon/desktop launch config
- 🔴 `internal/installpath/` — install-path resolution for the old binaries
- 🔴 `internal/evolve/` — skill self-evolution (the new **P6 optimizer** is a different, verification-gated design — do not carry this forward)
- 🔴 `internal/config/heros_desktop.go` (+ test) — desktop config

### Infra (replace with the new stack)
- 🔴 `internal/infra/natsbus/` — replace with the run **queue** chosen in P4/P6
- 🔴 `internal/infra/neo4jstore/` — the Workflow IR graph lives in **Postgres** per the plan (Neo4j optional, not default)
- ⚪/🔴 `internal/infra/qdrant/` — **optional**: could back the embedding store for failure clustering/RAG. Keep only if you adopt Qdrant; otherwise remove and use pgvector

---

## 3. 🔴 Remove — build artifacts, junk, and old packaging

### On disk (already gitignored — just delete the files)
- 🔴 `agentd`, `collectived`, `heros-mcp`, `heros.exe`, `heros-desktop.exe` — prebuilt binaries (~140 MB)
- 🔴 `.gocache/` — Go build cache
- 🔴 `internal/cliagent/.heros/` — local runtime state

### Tracked in git (need `git rm`)
- 🔴 `heros-cli` — committed binary (should never have been tracked)
- 🔴 `bash.exe.stackdump` — crash dump junk

### Packaging / install / deploy (desktop + daemon oriented)
- 🔴 `install/` (all `Install-Heros-*` + desktop generators)
- 🔴 `packaging/appimage/`
- 🔴 `deploy/agentd.service`, `deploy/heros.service`, `deploy/heros-desktop.service`, `deploy/com.heros.agentd.plist`
- 🟡 `deploy/docker-compose.enterprise.yml`, `deploy/.env.enterprise.example` — ADAPT into a dev `docker-compose` (Postgres + object store + OTel collector + span store + TSDB + queue)
- 🔴 `scripts/install-heros*.{ps1,sh}`, `scripts/*-heros-service.ps1`, `scripts/rollback-*` — old service installers
- 🔴 `scripts/*.py` (tool-contract generators, asset import, standardize/regenerate tools) — old tool-authoring tooling
- 🔴 `config.example.json`, `config.enterprise.example.json` — replace with the new service config example

### CI
- 🔴 `.github/workflows/release-desktop.yml` — desktop release
- 🟡 `.github/workflows/release.yml` — REWRITE for a server build (Docker image publish, not multi-OS binary/desktop release)

---

## 4. 🟡 Docs — archive or rewrite

| Doc | Verdict | Note |
|---|:---:|---|
| `docs/ARCHITECTURE.md` | 🟡 REWRITE | Replace with the new four-subsystem architecture |
| `docs/TOOL-RESPONSE-CONTRACT.md` | 🔵 KEEP-as-reference | Informs the Skill Registry contract / typed I/O envelope |
| `docs/AGENT_LAYOUT.md`, `MEMORY-VAULT.md`, `FLEET-SKILL-WORKER.md`, `DEFAULT-AGENT-FILES.md`, `SKILLS-TOOLS-IMPORT.md`, `RUN-LOCAL-REPO.md`, `STEP-BY-STEP-RUN.md` | 🔴 REMOVE | Describe the old product |
| `docs/ENTERPRISE.md`, `docs/ENTERPRISE_CONTRACTS.md` | 🔴 REMOVE | Old enterprise/collective model |
| `docs/TODO.md` | 🔴 REMOVE | Stale |
| `docs/implementation-timeline/`, `docs/prd/`, `openspec/` | 🟢 KEEP | The new plan (already added) |
| `README.md` | 🟡 REWRITE | Currently carries the pivot banner; do a full rewrite once code migration starts |
| `docs/media/image1.png` | 🔴 REMOVE | Old-product diagram |

---

## 5. 🟡 Dependency pruning (`go.mod`)

Remove after the code that uses them is deleted (run `go mod tidy` last):

- 🔴 `fyne.io/fyne/v2`, `fyne.io/systray`, `github.com/go-gl/*`, `github.com/fyne-io/*`, `github.com/go-text/*`, `github.com/srwiley/*`, `golang.org/x/image`, `github.com/nfnt/resize`, `github.com/jsummers/gobmp` — **desktop GUI**
- 🔴 `github.com/neo4j/neo4j-go-driver/v5` — if dropping Neo4j
- 🔴 `github.com/nats-io/nats.go` (+ nkeys/nuid) — if replacing the bus with a new queue
- 🔴 `github.com/chzyer/readline` — old CLI REPL
- 🟢 Keep: `github.com/google/uuid`, `gopkg.in/yaml.v3`, `github.com/bmatcuk/doublestar/v4`, `github.com/stretchr/testify`
- 🟡 `modernc.org/sqlite` — keep for local dev, or drop in favor of Postgres (`pgx`)
- ⚪ Add later: `gin-gonic/gin`, `go.opentelemetry.io/otel*`, `jackc/pgx/v5`, object-store SDK, a queue client, `go/ast` (stdlib) / tree-sitter bindings

---

## 6. ⚪ Add — net-new subsystems (already specified)

These do not exist in the current tree; each is fully specified in a PRD + OpenSpec change. Nothing
to salvage — build fresh:

| Subsystem | Phase | Spec |
|---|:---:|---|
| Workflow IR + metric event schema + lineage | P0 | `openspec/changes/p0-foundations/` |
| Discovery Engine (Go `go/ast`) | P1 | `openspec/changes/p1-discovery-mvp/` |
| Config Layer + source-transformation engine + Variant Spec + Runtime executor | P2 | `openspec/changes/p2-config-runtime/` |
| Metrics/OTel substrate + 3 stores | P2.5 | `openspec/changes/p2.5-metrics-observability/` |
| Context strategies + Skill Registry + **Sandbox** | P3 | `openspec/changes/p3-context-skills-sandbox/` |
| Pattern Classifier | P3.5 | `openspec/changes/p3.5-pattern-classifier/` |
| Eval Harness + eval-set gen + scoring | P4 | `openspec/changes/p4-eval-harness/` |
| Attribution + Diagnosis | P4.5 | `openspec/changes/p4.5-attribution-diagnosis/` |
| Typed contracts + Re-arrangement + Dynamic tracing | P5 | `openspec/changes/p5-contracts-rearrange-tracing/` |
| Proposals + Verification | P5.5 | `openspec/changes/p5.5-proposals-verification/` |
| Autonomous optimizer | P6 | `openspec/changes/p6-autonomous-optimizer/` |
| React web UI (graph editor, leaderboard, dashboards) | P2+ | across the UI-bearing phases |

---

## 7. Suggested execution order

Do the deletions and salvage on a branch, in this order, so the tree stays buildable at each step:

1. **Purge artifacts & junk** (§3 on-disk + `git rm heros-cli bash.exe.stackdump`), tighten `.gitignore`.
2. **Salvage first, then delete** — copy `cliagent/openai.go`+`stream.go` into a new `internal/providergateway/`, and `promptlayer/store.go` into a new `internal/promptregistry/`, **before** deleting their old packages, so nothing you want is lost.
3. **Remove old commands** (`cmd/heros*`, `cmd/collectived`, `cmd/fleet-skill-worker`) and the desktop config.
4. **Remove old internal packages** (§2) — expect a cascade of compile errors in `api/` and `platform/`; resolve by stripping their old routes/wiring.
5. **Prune infra** (nats/neo4j; decide qdrant vs pgvector).
6. **Trim `go.mod`** and run `go mod tidy`; confirm `go build ./...` is green on the reduced tree.
7. **Archive/rewrite docs** (§4) and the CI workflow (§3).
8. **Land P0** (`openspec/changes/p0-foundations`) as the first net-new work on the cleaned foundation.

**Net effect:** you keep the HTTP server, auth, config, DB access, the OpenAI-compatible provider
client, the tool/skill/prompt registry plumbing, tool contracts, and embeddings (~8 salvageable
areas). You remove roughly the entire agent runtime, desktop app, MCP sidecar, memory vault, org
collective, and fleet worker (~12k+ LOC plus ~140 MB of binaries). Everything net-new is already
specified in the PRDs and OpenSpec changes.

---

## 8. Execution log

Executed on branch `reproposal/cleanup` in four commits (planning baseline → artifact purge →
provider-gateway salvage → package/ancillary removal). The tree stayed buildable at each step.

### What remains (the minimal, green foundation)

```
cmd/agentd                     # daemon entrypoint (boots launch → api)
internal/
  api          launch          # minimal authed HTTP server: /healthz, /readyz
  auth  config  db  sqltime    # core: API-key auth, config, SQLite ledger, time util
  providergateway              # SALVAGED OpenAI-compatible client (P2 gateway seed)
  toolcontract agentlayout     # registry seeds (clean island) …
  skillindex   toolindex       #   … Skill/Tool Registry foundations (P2/P3)
  approval                     # human-in-the-loop gate seed (P6)
  embeddings                   # failure-clustering / RAG seed (P3/P4.5)
docs/{implementation-timeline,prd}  openspec/   # the new plan
docs/TOOL-RESPONSE-CONTRACT.md      # kept as reference for the Skill Registry contract
deploy/docker-compose.enterprise.yml # kept to ADAPT into the dev compose (P2.5)
```

**Verification:** `go build ./...`, `go vet ./...`, `go test ./...` all exit 0; `go.mod` reduced
from ~40 dependencies to `yaml.v3` + `modernc.org/sqlite` (+ sqlite's transitive indirects). A
live `agentd` run returned `{"status":"ok"}` on `/healthz` and `{"status":"ready"}` on `/readyz`.

### Removed (628 files)

All old commands (`heros`, `heros-cli`, `heros-desktop`, `heros-mcp`, `collectived`,
`fleet-skill-worker`); the agent-runtime/desktop/collective/memory-vault packages
(`cliagent`, `collective`, `syncsnapshot`, `indexsync`, `fleetworker`, `memorylayer`, `memoryfs`,
`memorytree`, `vaultindex`, `inbox`, `installpath`, `evolve`); the old infra
(`infra/natsbus`, `infra/neo4jstore`, `infra/qdrant`); desktop config; installers/packaging;
old service units; install/tool-gen scripts; both release workflows; old docs; and ~140 MB of
committed/on-disk binaries.

### Three reclassifications made during execution (differ from §1–2)

1. **`agentlayout` → KEEP** (was 🔴). It has zero internal deps and is a foundation of the
   registry packages we keep (`skillindex`/`toolindex`/`promptlayer` all imported it). Removing it
   would have broken the registry island.
2. **`promptlayer` → REMOVE now, rebuild in P2** (was 🔵 salvage-in-place). Its `store.go` is
   entangled with the embedded-seed (`SeedIfEmpty` → `seedEmbeddedDefaults`) and 295 old-product
   skill markdown files. Cleaner to cut it whole and re-salvage `store.go` from git history when
   building the Prompt Registry, than to carry the embedded content forward.
3. **`harness`, `scheduler`, `platform`, `observability`, `tooling` → REMOVE now** (were 🟡 adapt).
   Each was coupled to removed infra (nats/neo4j/qdrant) or the old topology and is not needed for
   the P0-ready foundation. Their reusable *patterns* are recoverable from git history when the
   Runtime executor (P2/P5), run queue (P4), and OTel substrate (P2.5) are built.

**Salvage-from-git note:** `internal/harness`, `internal/scheduler`, and `internal/promptlayer`
(store.go) are preserved in history at commit `6f381e8`'s parent and earlier — retrieve with
`git show <rev>:internal/<pkg>/<file>.go` when their adaptation phase begins.
