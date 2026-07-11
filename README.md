> **⚠️ Project pivot in progress.** This repository is being repurposed into an
> **LLM Agentic Workflow Evaluation & Configuration System**. The comprehensive
> implementation timeline lives in
> [`docs/implementation-timeline/`](docs/implementation-timeline/README.md). The
> content below describes the prior *Heros OS-level agent* direction and is retained
> for reference until the migration completes.

---

# Heros — OS-level agent

This project targets an **operating-system-level agent** in the same **work posture** as **Claude Code**, **Codex**, and similar tools: it sits **next to real work**—IDE, terminal, pipelines—not as a separate “admin app.” **Admins and employees alike** are meant to **do their jobs through the agent** (planning, coding, asset workflows, ops, commerce tasks—whatever you wire in). The agent **accumulates** reusable **skills**, **memory**, and **tools** over time, then **evolves** that stack and **syncs** matured knowledge to an **organizational collective** so the whole company benefits.

**That is the product idea.** This repo’s **`agentd`** is the **local daemon** that holds that runtime: long-lived process, folder-first state, HTTP control plane, optional vectors/graph/bus. **`heros-mcp`** is the **sidecar** that lets MCP hosts (e.g. Cursor, other IDEs) talk to `agentd` the way a Codex-style client would—not as a toy, but as the **primary daily interface class** for knowledge workers.

**Human approval** is **not** “what the product is.” It is the **governance spine** for **self-modification**: when the agent (or an automation) wants to **change** the live skill library, tool registry, harness topology, or other durable config, that mutation is proposed as a **diff**, reviewed, then committed—so the agent can grow **without silent drift**. Day-to-day assistance does not need to feel like “opening a queue.”

**Collective sync** (skills + memory + progress **across** machines) is the **org-scale layer** this architecture is built for; today the repo ships **hooks** (HTTP ingest, NATS subjects) and **policy-ready** indexing—not a finished “Dropbox for agent state” product. See [`docs/TODO.md`](docs/TODO.md) for an honest gap list.

The authoritative design—including OS install posture and the four self-evolving layers—is in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## What you are building

A **self-improving OS agent** that people **work in**, not only **configure**: embedded in dev and business workflows, **memory- and skill-backed**, **tool-using**, and **safe when it rewrites itself** (propose → review → commit). Federated **collective** nodes are where **vetted** evolved artifacts and signals meet **organizational** memory and policy—target end state; partial wiring exists today.

## What it can do (in this repo)

- **Daily work surfaces**: **`heros`** — **one command** (clone: `go run ./cmd/heros` / `go install ./cmd/heros`; published: `go install …/cmd/heros@latest`) starts the **full stack in-process** (embedded HTTP daemon + `data_dir` + REPL). You do **not** need a second terminal running **`agentd`** for normal use. REPL: readline, streaming, skills/tools/memory/graph, **`heros_shell`** in **cwd**, optional **`heros_agent_shell`**, **`heros_submit_proposal`**, and **`/harness <goal>`** for bundled **multi-actor** (leader → specialists → critic). Optional **`heros-mcp`** for IDE hosts; standalone **`agentd`** is for headless HTTP-only setups.
- **Skills & prompts**: On-disk `SKILL.md` + `system/prompt.md`, indexed and tenant-aware; served to any client you build on top of `agentd`.
- **Tools**: `tools/*/tool.yaml` + `tool_registry` with sync rules; extensible for domain-specific actions (build, deploy, DCC hooks, etc.).
- **Memory**: Session episodic storage, consolidation/retrieve paths, optional **Qdrant** + **Neo4j** for org-grade retrieval and graph views.
- **Governance (secondary UX)**: Small static page at **`/`** lists **pending self-change proposals** so someone can approve/reject; use it when you don’t have another review surface—it is **not** the definition of “using the agent.”
- **Collective stubs**: **`collectived`** + **`collective_url`** + NATS events—starting points for org sync, not the full multi-tenant sync product yet.

For exact directory layout and catalog behavior, see [`docs/AGENT_LAYOUT.md`](docs/AGENT_LAYOUT.md).

## Architecture (summary)

Full detail and diagrams: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md). At a high level:

| Layer | Role |
|-------|------|
| **1 — Prompt / skills** | Versioned, indexed prompts and skills; continuous proposals as diffs; semantic + structural indexing after approval; org-wide skill graph at collective scale. |
| **2 — Context / memory** | Local episodic, semantic, and structural memory; importance and consolidation; session-aware context when budgets are tight; optional sync to a federated graph with controls. |
| **3 — Harness** | Leader–follower decomposition plus team + critic patterns; topology and threshold changes proposed as diffs and gated on approval. |
| **4 — Tooling** | NL → CLI with tiered risk; sandbox and egress constraints; new tools proposed, indexed, and published only after sign-off. |

**Cross-cutting principles**: **local node** where people work (IDE + MCP + APIs) vs **federated collective** where orgs merge **evolved** skills/memory/tools; **human approval** only on **mutations** to the durable agent stack, not on every chat turn; metrics and buses close the loop.

The **recommended stack sketch** in the architecture doc (Go daemon, LLM API, SQLite + vectors, graph DB, NATS, optional governance UI) matches this repo: **agentd** is the **local OS-node** daemon (embedded inside **`heros`** for normal use). A **first-party terminal UI** is **`heros`**; **MCP** (`heros-mcp`) is optional for IDE hosts—not to be confused with the small **`/`** proposal reviewer.

## How users interact (vision vs this repo)

| | **Target experience** (Claude Code / Codex–class) | **What this repo ships today** |
|---|-----------------------------------------------|--------------------------------|
| **Admin & employee** | Same agent runtime: everyone drives real work through terminal CLI, IDE, or internal apps wired to `agentd`. | **`heros`** (one binary: embedded agentd + REPL). Optional **`heros-mcp`** to `agentd`; optional **`heros-cli`** only if `agentd` already runs elsewhere. **Local shell** = process cwd; **server shell** = `-agent-shell`. |
| **Skills / memory / tools** | Grown from daily tasks; reused automatically; promoted org-wide after policy. | **On-disk + SQLite + optional Qdrant/Neo4j**; indexing and sync **rules** exist; **full collective merge/push** still a roadmap item ([`docs/TODO.md`](docs/TODO.md)). |
| **Self-evolution** | Agent proposes durable changes (new skill, tool, topology); human/org policy approves. | **Proposal + approve/reject** flow + `evolve` apply; **`/`** is one lightweight reviewer, not the main workspace. |
| **Collective** | Sync evolved skills, memory, progress across the company. | **Stubs + NATS**; not a finished org-wide state sync product. |

## Requirements

- **From source / `go install`:** [Go](https://go.dev/dl/) 1.22 or newer  
- **Prebuilt (no Go):** download **`heros-*-windows-*.zip`** or **`heros-*-x86_64.AppImage`** / **`.tar.gz`** from [GitHub Releases](https://github.com/heros-foreal/agentd/releases) (tags `v*`). Details: [`install/README.md`](install/README.md).

## How to run

### Install once — then only type **`heros`** (local or production)

Same workflow everywhere: put the **`heros`** binary on your **`PATH`**, then open any terminal and run:

```bash
heros
```

That starts **agentd + REPL** in one process. No second daemon, no `go run` after install.

**One-time install (pick one):**

| Situation | Command |
|-----------|---------|
| **Published module** (public repo, Go proxy can see it) | `go install github.com/heros-foreal/agentd/cmd/heros@latest` then **`heros -add-path`** (see below) |
| **You have this repo cloned** | `bash scripts/install-heros.sh` or **`pwsh scripts/install-heros.ps1`** — runs `go install` and updates your user **`PATH`**; or manually: `go install ./cmd/heros` then **`heros -add-path`** |
| **Double-click / GUI (clone)** | **Windows:** `install/Install-Heros-Windows.cmd`. **Linux:** `bash install/generate-linux-desktop.sh` then open the **Install Heros** desktop entry, or run `bash install/Install-Heros-Linux.sh` — see [`install/README.md`](install/README.md) |
| **Prebuilt binary (no Go)** | Download from [Releases](https://github.com/heros-foreal/agentd/releases): unzip and run **`heros.exe`**, or chmod +x the Linux binary / AppImage. Run **`heros -add-path`** once so the folder containing the binary is on your **user** PATH (Windows: registry; Linux/macOS: `~/.zshrc` or `~/.profile`). If you installed via **`go install`** and run **`heros` from `GOPATH/bin`**, the same flag adds that Go bin dir instead. |

`go install` itself **cannot** change environment variables (Go has no install hook). **`heros -add-path`** updates the **current user’s** PATH, then exits. **Open a new terminal** so PATH reloads. Check **`heros -version`** on release builds.

Alternatively, add Go’s bin dir to **`PATH`** by hand (once per machine):

- **Windows (PowerShell):** `[Environment]::SetEnvironmentVariable('Path', $env:Path + ';' + (go env GOPATH) + '\bin', 'User')`  
  Or put **`%USERPROFILE%\go\bin`** on your user PATH if that’s where `go install` writes.
- **macOS / Linux:** `export PATH="$(go env GOPATH)/bin:$PATH"` (add that line to `~/.bashrc` or `~/.zshrc`).

**First run:** if no API key is configured, **`heros`** prompts for one and saves it under **`%APPDATA%\heros\config.json`** (Windows) or the Unix equivalent — same file also stores your saved shell workspace **`cli_workdir`** after you use **`/cd`** in the REPL.

**Workspace:** Skills and memory always use **agentd `data_dir`** (default under your home). The **local shell** (`heros_shell`) uses **`cli_workdir`** if set, else the current directory. Run **`/cd D:\your\project`** once; after that you can start **`heros`** from anywhere and still land in the right folder.

**Optional overrides** (same binary, dev or prod): `-config`, `-workdir`, `-openai-base`, `-model`, `-no-stream`, `-agent-shell`, `-api-key` (when `auth_mode=required`). Details: [`docs/STEP-BY-STEP-RUN.md`](docs/STEP-BY-STEP-RUN.md).

**In the REPL:** chat in natural language; tools include **`heros_shell`**, **`heros_memory_*`**, **`heros_read_skill`**, **`heros_run_harness`** (multi-actor, same as **`/harness`**), **`heros_submit_proposal`**, **`heros_list_pending_proposals`**, **`heros_approve_proposal`**, **`heros_reject_proposal`** (so “approve that skill” works without typing slash commands), etc. Slash commands: **`/help`**, **`/harness`**, **`/pending`**, **`/approve`**, **`/reject`**, **`/cd`**, **`/refresh`**, **`/exit`**. The **`/`** page is optional. Some local LLM servers need **`-no-stream`**.

**Without installing** (contributors only): from repo root, `go run ./cmd/heros` — still one command, but slower startup; prefer **`go install ./cmd/heros`** for daily use.

### 2. Optional: `agentd` only (headless HTTP)

Only if you need the HTTP API **without** the terminal agent (automation, tests, or a remote box). Same config discovery as **`heros`** (`-config` or defaults).

```bash
go build -o agentd ./cmd/agentd && ./agentd
```

Sanity: `/` and `/health` on **`listen_addr`**.

### 3. Optional: `collectived` (collective ingest stub)

Use this only if you want to try **pushing proposals** to a second process. Set `collective_url` in `config.json` to match where `collectived` listens.

**Terminal A — collective (example server)**

```bash
go build -o collectived ./cmd/collectived
./collectived
# Default listen: :8790. Override with COLLECTIVE_LISTEN; optional NATS_URL for fan-out.
```

**`config.json`** (example)

```json
"collective_url": "http://127.0.0.1:8790"
```

**Terminal B — `heros`** or **`agentd`** with `collective_url` set. When clients call `POST /api/proposals`, `agentd` will **POST** the proposal to `collectived` (still a minimal reference implementation). After a proposal is **approved**, agentd also POSTs **`/v1/ingest/approved-mutation`** so the collective can broadcast **vetted** changes.

**Fleet nodes:** run **`fleet-skill-worker`** to subscribe to **`heros.fleet.proposals.approved`**, write **`skills/`** under each machine’s **`data_dir`**, and optionally **`git pull`** + **`POST /api/catalog/reindex`**. See [`docs/FLEET-SKILL-WORKER.md`](docs/FLEET-SKILL-WORKER.md).

### 4. Optional: Enterprise stack (Qdrant, Neo4j, NATS)

For vector search, graph mirror, and messaging, start Docker services and point the daemon at them:

```bash
# See docs/ENTERPRISE.md for env file and passwords
docker compose -f deploy/docker-compose.enterprise.yml --env-file deploy/.env.enterprise up -d
```

Then copy and edit **`config.enterprise.example.json`** and run **`heros`** (or **`agentd`**) with that file:

```bash
heros -config config.enterprise.example.json
```

### 5. Optional: `heros-mcp` (MCP → agentd)

For IDEs that only speak MCP, build the stdio bridge:

```bash
go build -o heros-mcp ./cmd/heros-mcp
./heros-mcp -agentd-url=http://127.0.0.1:8787
# If auth_mode is required: add -api-key=...
```

Configure your MCP client to execute that command. The MCP server expects **`agentd` HTTP** already listening—run **`agentd`** headlessly, or point it at the URL **`heros`** prints when it starts (same machine).

### 5b. Optional: `heros-desktop` (desktop GUI)

If you want a desktop chat window (instead of terminal REPL), install and run:

```bash
go install ./cmd/heros-desktop
heros-desktop -add-path   # one time
heros-desktop
```

`heros-desktop` starts `agentd` in-process (same pattern as `heros`), then opens a GUI where you can send prompts, view responses, and refresh catalog context.  
It uses the same config discovery and LLM settings (`OPENAI_API_KEY`, `openai_api_key`, `-config`, `-model`, `-workdir`, etc.).

Clickable installers from a local clone:

- **Windows:** `install/Install-Heros-Desktop-Windows.cmd`
- **Linux / Ubuntu:** `install/Install-Heros-Desktop-Linux.sh` or `bash install/generate-linux-desktop-heros-desktop.sh`
- **macOS:** `install/Install-Heros-Desktop-macOS.command`

**`heros-cli`** (source: `cmd/heros-cli`) exists only for the rare case where **`agentd` already runs on another host** and you want a REPL against that URL. Use **`heros -h`** / **`heros-cli -h`** for flags; you do not need **`heros-cli`** for normal local development or installed **`heros`**.

### 6. Where data lives

`data_dir` in config (e.g. **`.heros-data`** in `config.example.json`) is resolved **relative to the process working directory** when you start **`heros`** or **`agentd`**, unless you use an absolute path. Under it you will see:

- `agent.db` — SQLite ledger and indexes  
- `skills/`, `tools/`, `memory/`, `system/` — authoritative files (see [`docs/AGENT_LAYOUT.md`](docs/AGENT_LAYOUT.md))  

First boot **copies** bundled defaults from **`internal/promptlayer/embedded_defaults/`** (also in the binary via `go:embed`) into `data_dir` for any missing paths. List: [docs/DEFAULT-AGENT-FILES.md](docs/DEFAULT-AGENT-FILES.md).

### 7. Programs at a glance

| Binary | Role | Typical invocation |
|--------|------|--------------------|
| **heros** | **Default: daemon + terminal agent** | `heros` (from install) or `go run ./cmd/heros` |
| **agentd** | HTTP only (optional) | `go build -o agentd ./cmd/agentd && ./agentd` |
| **heros-desktop** | Desktop GUI (embedded daemon + chat window) | `go build -o heros-desktop ./cmd/heros-desktop && ./heros-desktop` |
| **heros-cli** | REPL → remote `agentd` only (optional) | `heros-cli -agentd-url=…` |
| **collectived** | Example collective HTTP ingest | `./collectived` |
| **fleet-skill-worker** | NATS → local `skills/` (+ optional `git pull`, reindex) | `go build -o fleet-skill-worker ./cmd/fleet-skill-worker` |
| **heros-mcp** | MCP stdio → `agentd` URL | `./heros-mcp -agentd-url=…` |

Build (only **`heros`** required for daily use):

```bash
go build -o heros ./cmd/heros
# optional: agentd, heros-desktop, heros-cli, collectived, fleet-skill-worker, heros-mcp
```

## Configuration

| File | Purpose |
|------|---------|
| `config.example.json` | Local / minimal defaults |
| `config.enterprise.example.json` | Neo4j, Qdrant, NATS, auth knobs |

Use `-config <path>` to load JSON. Omitting `-config` uses defaults plus any supported env overrides (see [`docs/AGENT_LAYOUT.md`](docs/AGENT_LAYOUT.md) for `tool_registry_sync` env vars).

## Documentation

- [Markdown / Obsidian vault as memory source of truth (design)](docs/MEMORY-VAULT.md)
- [Bundled default skills & tools (repo paths)](docs/DEFAULT-AGENT-FILES.md)
- [Run from a local clone (no global install)](docs/RUN-LOCAL-REPO.md)
- [Fleet skill worker (NATS approved proposals → `skills/`)](docs/FLEET-SKILL-WORKER.md)
- [External skills/tools bulk import](docs/SKILLS-TOOLS-IMPORT.md)
- [Step-by-step: run the project](docs/STEP-BY-STEP-RUN.md)
- [Agent framework architecture (authoritative design)](docs/ARCHITECTURE.md)
- [On-disk agent layout & catalog APIs](docs/AGENT_LAYOUT.md)
- [Enterprise stack (Docker, Qdrant, Neo4j, NATS)](docs/ENTERPRISE.md)
- Deployment samples: `deploy/`
- [Unified roadmap / gaps](docs/TODO.md)

## License

See repository metadata when published; this workspace may not yet include a `LICENSE` file.
