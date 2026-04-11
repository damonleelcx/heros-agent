# Heros — OS-level agent

This project targets an **operating-system-level agent** in the same **work posture** as **Claude Code**, **Codex**, and similar tools: it sits **next to real work**—IDE, terminal, pipelines—not as a separate “admin app.” **Admins and employees alike** are meant to **do their jobs through the agent** (planning, coding, asset workflows, ops, commerce tasks—whatever you wire in). The agent **accumulates** reusable **skills**, **memory**, and **tools** over time, then **evolves** that stack and **syncs** matured knowledge to an **organizational collective** so the whole company benefits.

**That is the product idea.** This repo’s **`agentd`** is the **local daemon** that holds that runtime: long-lived process, folder-first state, HTTP control plane, optional vectors/graph/bus. **`heros-mcp`** is the **sidecar** that lets MCP hosts (e.g. Cursor, other IDEs) talk to `agentd` the way a Codex-style client would—not as a toy, but as the **primary daily interface class** for knowledge workers.

**Human approval** is **not** “what the product is.” It is the **governance spine** for **self-modification**: when the agent (or an automation) wants to **change** the live skill library, tool registry, harness topology, or other durable config, that mutation is proposed as a **diff**, reviewed, then committed—so the agent can grow **without silent drift**. Day-to-day assistance does not need to feel like “opening a queue.”

**Collective sync** (skills + memory + progress **across** machines) is the **org-scale layer** this architecture is built for; today the repo ships **hooks** (HTTP ingest, NATS subjects) and **policy-ready** indexing—not a finished “Dropbox for agent state” product. See [`TODO-BUSINESS.md`](TODO-BUSINESS.md) for an honest gap list.

The authoritative design—including OS install posture and the four self-evolving layers—is in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## What you are building

A **self-improving OS agent** that people **work in**, not only **configure**: embedded in dev and business workflows, **memory- and skill-backed**, **tool-using**, and **safe when it rewrites itself** (propose → review → commit). Federated **collective** nodes are where **vetted** evolved artifacts and signals meet **organizational** memory and policy—target end state; partial wiring exists today.

## What it can do (in this repo)

- **Daily work surfaces**: **`heros-cli`** — terminal REPL (optional **readline** history) with multi-turn LLM + tools: loads **folder skills** and **tool catalog** from `agentd`, **semantic memory** search, **Neo4j graph neighbors** (when configured), **SSE streaming** of assistant text to the terminal (when the API supports it), **`heros_shell` on the CLI machine** under **`-workdir`**, optional **`heros_agent_shell`** on the **agentd host** (policy-gated, off unless **`-agent-shell`**), **`heros_submit_proposal`** for self-evolution queues, optional episodic logging per session. Also: HTTP APIs for harness runs, memory, catalog, CLI; optional **`heros-mcp`** for MCP-only hosts.
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

The **recommended stack sketch** in the architecture doc (Go daemon, LLM API, SQLite + vectors, graph DB, NATS, optional governance UI) matches this repo: **agentd** is the **local OS-node** daemon; enterprise samples add graph, vectors, and messaging. A **first-party Claude Code–class UI** sits on **`heros-cli`** (terminal) plus the same HTTP APIs; **MCP** (`heros-mcp`) remains optional for IDE hosts that prefer it—not to be confused with the small **`/`** proposal reviewer.

## How users interact (vision vs this repo)

| | **Target experience** (Claude Code / Codex–class) | **What this repo ships today** |
|---|-----------------------------------------------|--------------------------------|
| **Admin & employee** | Same agent runtime: everyone drives real work through terminal CLI, IDE, or internal apps wired to `agentd`. | **`heros-cli`** (OpenAI-style tool loop → agentd: folder skills, tools, memory search, Neo4j neighbors; **local shell** under `-workdir` by default, optional **server shell** with `-agent-shell`). Optional **`heros-mcp`** for MCP-only IDEs; full HTTP API for custom UIs. |
| **Skills / memory / tools** | Grown from daily tasks; reused automatically; promoted org-wide after policy. | **On-disk + SQLite + optional Qdrant/Neo4j**; indexing and sync **rules** exist; **full collective merge/push** still a roadmap item ([`TODO-BUSINESS.md`](TODO-BUSINESS.md)). |
| **Self-evolution** | Agent proposes durable changes (new skill, tool, topology); human/org policy approves. | **Proposal + approve/reject** flow + `evolve` apply; **`/`** is one lightweight reviewer, not the main workspace. |
| **Collective** | Sync evolved skills, memory, progress across the company. | **Stubs + NATS**; not a finished org-wide state sync product. |

## Requirements

- [Go](https://go.dev/dl/) 1.22 or newer

## How to run

Run commands from the **repository root**. On Windows you can use Git Bash, PowerShell, or `cmd`; built binaries are often named `agentd.exe`, `collectived.exe`, etc.

### 1. Prerequisites

Install Go 1.22+, clone or copy this repo, and `cd` into it.

### 2. Main daemon: `agentd` (required)

This is the long-lived **local control plane**: HTTP API, approval UI, on-disk skills/tools/memory, SQLite, and optional enterprise backends.

```bash
cp config.example.json config.json
# Optional: edit config.json — set openai_api_key, data_dir, listen_addr, etc.

go build -o agentd ./cmd/agentd
./agentd -config config.json
```

- **Without `-config`**: built-in defaults apply (default data dir is under the user home, e.g. `~/.heros-agent`). Environment variables can still override pieces of config after load (e.g. `HEROS_TOOL_REGISTRY_SYNC_*` for tool registry sync — see [`docs/AGENT_LAYOUT.md`](docs/AGENT_LAYOUT.md)).
- After start, **`listen_addr`** defaults to `127.0.0.1:8787` unless changed in config.

**Sanity checks**

- Browser: `http://127.0.0.1:8787/` — static approval UI  
- `GET http://127.0.0.1:8787/health` — SQLite plus optional Qdrant / Neo4j / NATS status  

For day-to-day development, **running only `agentd`** and hitting `/` and `/health` is enough.

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

**Terminal B — `agentd`** (as above). When clients call `POST /api/proposals`, `agentd` will **POST** the proposal to `collectived` (still a minimal reference implementation).

### 4. Optional: Enterprise stack (Qdrant, Neo4j, NATS)

For vector search, graph mirror, and messaging, start Docker services and point `agentd` at them:

```bash
# See docs/ENTERPRISE.md for env file and passwords
docker compose -f deploy/docker-compose.enterprise.yml --env-file deploy/.env.enterprise up -d
```

Then copy and edit **`config.enterprise.example.json`** and run:

```bash
./agentd -config config.enterprise.example.json
```

### 5. Terminal agent: `heros-cli` (Codex / Claude Code–style)

**Requires `agentd` already running** in another terminal. The CLI calls **OpenAI-compatible** `POST …/chat/completions` with your key (same host as the terminal), and calls **agentd** for cataloged **folder skills**, **tools**, **memory** (`/api/memory/*`), **graph neighbors** (`/api/graph/neighbors` when Neo4j is enabled), **`POST /api/proposals`** when the model uses **`heros_submit_proposal`**, and—**only if you pass `-agent-shell`**—**`/api/cli/exec`** on the **agentd host** under server risk policy.

**Shell and workspace (important)**

- **`heros_shell`** runs on **the machine where `heros-cli` runs**, with working directory **`-workdir`** (default `.`, resolved to an absolute path). Use this for `git`, local builds, and inspecting the repo on your laptop.
- **`heros_agent_shell`** runs on the **agentd server**. It is **not** registered unless you pass **`-agent-shell`**, because the trust model is different (server policy + audit). Prefer local shell unless the task must execute on the daemon host.

**Streaming and TUI**

- By default the CLI uses **SSE streaming** (`stream: true`) so assistant text appears incrementally. Some OpenAI-compatible servers mishandle **streaming + tools**; if you see errors or empty turns, add **`-no-stream`**.
- By default the REPL uses **readline** (line editing + history in `~/.heros-cli.history`). Use **`-no-readline`** for a plain stdin loop (e.g. minimal environments).

**Local LLMs (e.g. Ollama)**

Point **`-openai-base`** at a compatible endpoint (must expose `…/v1/chat/completions`). Example:

```bash
./heros-cli -openai-base=http://127.0.0.1:11434/v1 -model=llama3.2
```

**Flags reference**

| Flag | Default | Meaning |
|------|---------|---------|
| `-agentd-url` | `http://127.0.0.1:8787` | Running agentd base URL |
| `-api-key` | (empty) | `X-API-Key` when `agentd` `auth_mode=required` |
| `-openai-base` | `https://api.openai.com/v1` | OpenAI-compatible API base (trailing `/v1` as required by the provider) |
| `-openai-api-key` | env `OPENAI_API_KEY` | Bearer token for the LLM |
| `-model` | `gpt-4o-mini` | Chat model id |
| `-session` | random UUID | Episodic memory session id |
| `-no-session-log` | off | Do not auto-append each user/assistant turn to episodic memory |
| **`-workdir`** | **`.`** | **Workspace root for `heros_shell` (local machine)** |
| **`-no-stream`** | off | Disable streaming; single JSON response per model step |
| **`-agent-shell`** | off | Register **`heros_agent_shell`** (server-side `/api/cli/exec`) |
| **`-no-readline`** | off | Disable readline; use simple stdin |
| **`-target-tenant`** | (empty) | Default `target_tenant` for **`heros_submit_proposal`** when the model omits it (admin keys only) |

```bash
go build -o heros-cli ./cmd/heros-cli
export OPENAI_API_KEY=sk-...
./heros-cli -agentd-url=http://127.0.0.1:8787 -workdir=/path/to/your/repo
# auth_mode=required:
#   ./heros-cli -api-key=YOUR_AGENTD_KEY ...
# Ollama (example):
#   ./heros-cli -openai-base=http://127.0.0.1:11434/v1 -model=llama3.2 -no-stream
```

Inside the REPL: type natural language; the model may call tools (`heros_shell`, `heros_memory_search`, `heros_read_skill`, `heros_graph_neighbors`, `heros_submit_proposal`, and optionally `heros_agent_shell`). Slash commands: `/help`, `/refresh` (re-fetch skill/tool catalog from agentd), `/exit`.

**`heros_submit_proposal`** sends **`POST /api/proposals`** to agentd (layer, title, rationale, diff, optional `target_tenant`). Approved proposals follow your existing evolve / governance flow (e.g. UI at `/`).

### 6. Optional: `heros-mcp` (MCP → agentd)

For IDEs that only speak MCP, build the stdio bridge:

```bash
go build -o heros-mcp ./cmd/heros-mcp
./heros-mcp -agentd-url=http://127.0.0.1:8787
# If auth_mode is required: add -api-key=...
```

Configure your MCP client to execute that command.

### 7. Where data lives

`data_dir` in config (e.g. **`.heros-data`** in `config.example.json`) is resolved **relative to the process working directory** when you start `agentd`, unless you use an absolute path. Under it you will see:

- `agent.db` — SQLite ledger and indexes  
- `skills/`, `tools/`, `memory/`, `system/` — authoritative files (see [`docs/AGENT_LAYOUT.md`](docs/AGENT_LAYOUT.md))  

First boot on an empty directory **seeds** default `system/prompt.md`, a sample skill, and a sample tool.

### 8. Programs at a glance

| Binary | Role | Typical invocation |
|--------|------|--------------------|
| **agentd** | Main HTTP service | `./agentd -config config.json` |
| **heros-cli** | Terminal agent (LLM + tools → agentd) | `OPENAI_API_KEY=... ./heros-cli` |
| **collectived** | Example collective HTTP ingest | `./collectived` |
| **heros-mcp** | Optional MCP stdio → agentd | `./heros-mcp -agentd-url=http://127.0.0.1:8787` |

Build:

```bash
go build -o agentd ./cmd/agentd
go build -o heros-cli ./cmd/heros-cli
go build -o collectived ./cmd/collectived
go build -o heros-mcp ./cmd/heros-mcp
```

## Configuration

| File | Purpose |
|------|---------|
| `config.example.json` | Local / minimal defaults |
| `config.enterprise.example.json` | Neo4j, Qdrant, NATS, auth knobs |

Use `-config <path>` to load JSON. Omitting `-config` uses defaults plus any supported env overrides (see [`docs/AGENT_LAYOUT.md`](docs/AGENT_LAYOUT.md) for `tool_registry_sync` env vars).

## Documentation

- [Step-by-step: run the project](docs/STEP-BY-STEP-RUN.md)
- [Agent framework architecture (authoritative design)](docs/ARCHITECTURE.md)
- [On-disk agent layout & catalog APIs](docs/AGENT_LAYOUT.md)
- [Enterprise stack (Docker, Qdrant, Neo4j, NATS)](docs/ENTERPRISE.md)
- Deployment samples: `deploy/`
- [Engineering roadmap / gaps](TODO.md) · [Business scenario vs collective sync](TODO-BUSINESS.md)

## License

See repository metadata when published; this workspace may not yet include a `LICENSE` file.
