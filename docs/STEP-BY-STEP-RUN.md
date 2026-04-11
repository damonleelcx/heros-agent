# Step-by-step: run this project

**Recommended path:** a single binary **`heros`** starts the local agent daemon **and** the terminal REPL in one process—suitable for **long-running work** (memory, skills, proposals stay in `data_dir` until you exit). For layout and APIs, see [ARCHITECTURE.md](ARCHITECTURE.md) and [AGENT_LAYOUT.md](AGENT_LAYOUT.md). Overview: [README](../README.md).

---

## 0. Requirements

1. **[Go 1.22+](https://go.dev/dl/)**
2. Shell at the **repository root** (where `go.mod` lives)
3. On Windows: **PowerShell**, **cmd**, or **Git Bash** (`.\heros.exe` after build)

---

## 1. One command: `heros` (daemon + agent REPL)

This is the default way to run: **no separate `agentd` terminal**, **no `-config` or `-workdir` required** for normal use.

### Step 1 — Install globally (recommended)

From anywhere, after [Go](https://go.dev/dl/) is installed:

```bash
go install github.com/heros-foreal/agentd/cmd/heros@latest
```

Ensure **`GOBIN`** or **`GOPATH/bin`** is on your **`PATH`** (default `go install` puts the binary in `$(go env GOPATH)/bin` — e.g. `%USERPROFILE%\go\bin` on Windows).

From a **clone** of the repo:

```bash
go install ./cmd/heros
```

Then run **`heros`** from any directory.

### Step 2 — Config (automatic)

If you pass **`-config /path/to/config.json`**, that file wins. Otherwise **`heros`** searches, in order:

1. **`HEROS_CONFIG`** or **`HEROS_CONFIG_PATH`** (full path to `config.json`)
2. **`config.json`** starting in the **current working directory**, then each parent folder up to the filesystem root
3. **`%APPDATA%\heros\config.json`** on Windows (`os.UserConfigDir()/heros/config.json`)
4. **`~/.heros/config.json`**
5. **`~/.heros-agent/config.json`**
6. Built-in defaults (data dir usually **`~/.heros-agent`**)

So a project can ship **`config.json`** next to your code; a user machine can use a **single global file** under AppData or home.

### Step 3 — API key & model (automatic)

Resolution order for the **LLM bearer token**:

1. **`-openai-api-key`**
2. Environment **`OPENAI_API_KEY`**
3. **`openai_api_key`** in the loaded `config.json`

For **base URL** and **model** (when you do not pass flags):

- **Base:** **`OPENAI_BASE_URL`**, then **`openai_base_url`** in config, then default OpenAI-compatible URL.
- **Model:** **`OPENAI_MODEL`** or **`HEROS_MODEL`**, then **`openai_model`** in config, then default model.

You can rely on **only** a global `config.json` with `openai_api_key` set and run plain **`heros`** with no env vars.

### Step 4 — Workspace (current directory)

**`heros_shell`** uses the **current working directory** as the workspace root. **`cd`** into your repo (or any folder) and run **`heros`** — no **`-workdir`**. Use **`-workdir`** only to override.

### Step 5 — Run

```bash
cd /path/to/your/project
heros
```

Or from a clone without `go install`:

```bash
go run ./cmd/heros
```

**What happens**

1. **agentd** stack starts **in-process**: SQLite, seeded `skills/` / `tools/` if empty, optional Qdrant/Neo4j/NATS from config, HTTP API on **`listen_addr`**.
2. The process waits until **`GET /health`** succeeds.
3. The **same process** opens the **terminal agent REPL** (readline, streaming, tools).

**Long-running tasks**

- Leave the REPL open; use **`heros_memory_save`** / **`heros_memory_search`**, **`heros_shell`** under **`-workdir`**, and **`heros_submit_proposal`** as usual.
- State persists under **`data_dir`** across runs.
- Open [http://127.0.0.1:8787/](http://127.0.0.1:8787/) anytime for the **proposal approval UI** (same port as `listen_addr`).

**Stop cleanly**

- Type **`/exit`** or **Ctrl+D** in the REPL → HTTP server and database shut down and the process exits.

### Useful overrides (optional)

| Flag / env | Role |
|------------|------|
| `-config` | Force a specific `config.json` |
| `-workdir` | Override workspace (default: **cwd**) |
| `-openai-base`, `-model`, `-openai-api-key` | Override discovered LLM settings |
| `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL` | Env overrides |
| `-api-key` | `X-API-Key` if `auth_mode=required` |
| `-no-stream`, `-agent-shell`, `-session`, … | Same as **heros-cli** |

**Ollama example** (env or flags)

```bash
set OPENAI_BASE_URL=http://127.0.0.1:11434/v1
set OPENAI_MODEL=llama3.2
heros -no-stream
```

---

## 2. Split mode (advanced): `agentd` + `heros-cli`

Use this when the daemon should run **on another host** or **outlive** the REPL.

1. Start **`agentd`** (see below).
2. Build **`heros-cli`** and pass **`-agentd-url=http://…`** explicitly.

```bash
go build -o agentd ./cmd/agentd
./agentd -config config.json

# other terminal
go build -o heros-cli ./cmd/heros-cli
export OPENAI_API_KEY=sk-...
./heros-cli -agentd-url=http://127.0.0.1:8787 -workdir=/path/to/repo
```

---

## 3. Daemon only: `agentd`

Headless HTTP service (no REPL):

```bash
go build -o agentd ./cmd/agentd
./agentd -config config.json
```

Stop with **Ctrl+C**. Sanity checks: `/` and `/health` on **`listen_addr`**.

---

## 4. (Optional) Collective stub — `collectived`

Forward proposals to a second process: run **`collectived`**, set **`collective_url`** in config, restart **`agentd`** (or restart **`heros`** with that config).

---

## 5. (Optional) Enterprise stack

Docker + **`config.enterprise.example.json`** — see [ENTERPRISE.md](ENTERPRISE.md). Run **`heros -config config.enterprise.example.json ...`** or **`agentd`** with the same file.

---

## 6. (Optional) `heros-mcp`

```bash
go build -o heros-mcp ./cmd/heros-mcp
./heros-mcp -agentd-url=http://127.0.0.1:8787
```

(Requires a running **`agentd`** — e.g. started separately, or use **`heros`** only for the all-in-one terminal path.)

---

## 7. Build all binaries

```bash
go build -o heros ./cmd/heros
go build -o agentd ./cmd/agentd
go build -o heros-cli ./cmd/heros-cli
go build -o collectived ./cmd/collectived
go build -o heros-mcp ./cmd/heros-mcp
```

---

## 8. Troubleshooting

| Symptom | What to check |
|--------|----------------|
| `heros` exits immediately on “not ready” | Port **`listen_addr`** in use; change port or stop the other process. |
| Missing LLM key | `OPENAI_API_KEY` or **`-openai-api-key`**. |
| Ollama weirdness | **`-no-stream`**. |
| `auth_mode=required` | **`-api-key`** matching a tenant key in config. |
| Stale catalog after disk edits | **`POST /api/catalog/reindex`** while **`heros`** / **`agentd`** is running. |

---

## 9. Read next

| Topic | Doc |
|--------|-----|
| Folders, SKILL.md, tool.yaml | [AGENT_LAYOUT.md](AGENT_LAYOUT.md) |
| Enterprise Docker stack | [ENTERPRISE.md](ENTERPRISE.md) |
| Architecture | [ARCHITECTURE.md](ARCHITECTURE.md) |
| Roadmap | [TODO.md](../TODO.md), [TODO-BUSINESS.md](../TODO-BUSINESS.md) |
