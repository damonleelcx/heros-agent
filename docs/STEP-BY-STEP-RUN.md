# Step-by-step: run this project

You only need the **`heros`** command: one binary starts the local agent daemon **and** the terminal REPL (long-running memory, skills, proposals in `data_dir`). Same flow for **clone + `go run`** or **`go install`** — no second terminal, no `agentd` + `heros-cli` split unless you choose it.

Layout and APIs: [ARCHITECTURE.md](ARCHITECTURE.md), [AGENT_LAYOUT.md](AGENT_LAYOUT.md). Overview: [README](../README.md).

---

## 0. Requirements

1. **[Go 1.22+](https://go.dev/dl/)** (for `go install` / `go run`), or a prebuilt **`heros`**
2. Shell with **`heros`** on **`PATH`** (after install), or repo root for `go run ./cmd/heros`
3. Windows: **PowerShell**, **cmd**, or **Git Bash**

---

## 1. Install and run **`heros`**

### Install

**Published module (global):**

```bash
go install github.com/heros-foreal/agentd/cmd/heros@latest
```

Add **`$(go env GOPATH)/bin`** to **`PATH`** (Windows: often **`%USERPROFILE%\go\bin`**).

**From a clone:**

```bash
go install ./cmd/heros
# or without installing:
go run ./cmd/heros
```

More detail (paths, `cd` vs project folder, `go build`): [RUN-LOCAL-REPO.md](RUN-LOCAL-REPO.md).

### Config (automatic)

Unless you pass **`-config`**, **`heros`** discovers `config.json` in **cwd → parents**, then **`%APPDATA%\heros\config.json`**, **`~/.heros/`**, **`~/.heros-agent/`**, else defaults. See **`HEROS_CONFIG`** / **`HEROS_CONFIG_PATH`** to force a path.

### API key (automatic + first-run prompt)

Order: **`-openai-api-key`** → **`OPENAI_API_KEY`** → **`openai_api_key`** in config → interactive prompt (hidden on TTY). After prompt, key is **merged** into **`%APPDATA%\heros\config.json`** (or Unix `UserConfigDir/heros/config.json`).

### Workspace

**`heros_shell`** uses the **current working directory**. **`cd`** into your repo, then **`heros`**. Optional **`-workdir`** to override.

### Run

```bash
cd /path/to/your/project
heros
```

**Stop:** **`/exit`** or **Ctrl+D** (stops HTTP + SQLite).

**Approval UI:** **`http://127.0.0.1:8787/`** (or your `listen_addr`).

### Overrides (optional)

| Flag / env | Role |
|------------|------|
| `-config` | Force a `config.json` path |
| `-workdir` | Override workspace |
| `-openai-base`, `-model`, `-openai-api-key` | LLM overrides |
| `OPENAI_BASE_URL`, `OPENAI_MODEL`, `HEROS_MODEL` | Env |
| `-api-key` | `X-API-Key` if `auth_mode=required` |
| `-no-stream`, `-agent-shell`, `-session`, … | Advanced |

**Ollama example:**

```bash
set OPENAI_BASE_URL=http://127.0.0.1:11434/v1
set OPENAI_MODEL=llama3.2
heros -no-stream
```

---

## 2. Optional: `agentd` only (no REPL)

Headless HTTP (tests, automation, remote server). Same config discovery.

```bash
go build -o agentd ./cmd/agentd && ./agentd
```

---

## 3. Optional: `collectived`

Set **`collective_url`** in config; run **`heros`** or **`agentd`** with that config. See [README](../README.md) §4.

---

## 4. Optional: Enterprise stack

Docker + **`config.enterprise.example.json`** — [ENTERPRISE.md](ENTERPRISE.md). Prefer:

```bash
heros -config config.enterprise.example.json
```

---

## 5. Optional: `heros-mcp`

Needs **`agentd` HTTP** reachable (headless **`agentd`**, or the URL from a running **`heros`**).

```bash
go build -o heros-mcp ./cmd/heros-mcp
./heros-mcp -agentd-url=http://127.0.0.1:8787
```

---

## 6. Optional: `heros-cli` (remote `agentd` only)

Only if **`agentd` already runs elsewhere** (another host or long-lived process). Not part of the default workflow. Flags: **`heros-cli -h`**.

---

## 7. Troubleshooting

| Symptom | What to check |
|--------|----------------|
| “not ready” | Port **`listen_addr`** in use |
| No LLM key | Env, config, or first-run prompt |
| Ollama issues | **`-no-stream`** |
| `auth_mode=required` | **`-api-key`** |
| Stale catalog | **`POST /api/catalog/reindex`** |

---

## 8. Read next

| Topic | Doc |
|--------|-----|
| Folders, SKILL.md | [AGENT_LAYOUT.md](AGENT_LAYOUT.md) |
| Enterprise | [ENTERPRISE.md](ENTERPRISE.md) |
| Architecture | [ARCHITECTURE.md](ARCHITECTURE.md) |
| Roadmap | [TODO.md](../TODO.md), [TODO-BUSINESS.md](../TODO-BUSINESS.md) |
