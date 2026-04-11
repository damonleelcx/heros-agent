# Step-by-step: run this project

This guide walks you from zero to a working **agentd** plus optional **heros-cli**. For architecture and on-disk layout, see [ARCHITECTURE.md](ARCHITECTURE.md) and [AGENT_LAYOUT.md](AGENT_LAYOUT.md). The [README](../README.md) is the overview; this file is the ordered checklist.

---

## 0. Requirements

1. Install **[Go 1.22+](https://go.dev/dl/)**.
2. Clone the repository and open a terminal at the **repository root** (where `go.mod` lives).
3. On Windows, use **Git Bash**, **PowerShell**, or **cmd** (examples below use Unix-style paths; on Windows you may use `.\agentd.exe` after build).

---

## 1. Create config (recommended)

1. Copy the example config:

   ```bash
   cp config.example.json config.json
   ```

2. Open `config.json` and adjust if needed:
   - **`data_dir`** — where SQLite, `skills/`, `tools/`, `memory/`, `system/` live. Relative paths are resolved from the **current working directory** when you start `agentd` (e.g. `.heros-data`).
   - **`listen_addr`** — default `127.0.0.1:8787`.
   - **`openai_api_key`** — optional for `agentd` itself; **heros-cli** uses its own OpenAI key / `-openai-api-key` unless you wire harness features that call the API from the daemon.

3. Save the file.

---

## 2. Build the main daemon

1. From the repo root:

   ```bash
   go build -o agentd ./cmd/agentd
   ```

2. Start **agentd** with your config:

   ```bash
   ./agentd -config config.json
   ```

   **Alternative:** omit `-config` to use built-in defaults (data dir often under the user home, e.g. `~/.heros-agent`).

3. Leave this terminal running.

---

## 3. Verify agentd is up

1. Open a browser: [http://127.0.0.1:8787/](http://127.0.0.1:8787/) — approval UI for self-change proposals.
2. Check health (browser or `curl`):

   ```bash
   curl -s http://127.0.0.1:8787/health
   ```

3. **First boot:** an empty `data_dir` is **seeded** with default `system/prompt.md`, sample skills under `skills/_global/`, and sample tools under `tools/_global/`. See [AGENT_LAYOUT.md](AGENT_LAYOUT.md) for the folder contract.

---

## 4. (Optional) Collective stub — `collectived`

Skip this unless you want agentd to **forward proposals** to a second HTTP process.

1. In a **second** terminal, from the repo root:

   ```bash
   go build -o collectived ./cmd/collectived
   ./collectived
   ```

   Default listen is often **`:8790`** (see env `COLLECTIVE_LISTEN` if documented in your tree).

2. In `config.json`, set:

   ```json
   "collective_url": "http://127.0.0.1:8790"
   ```

3. **Restart `agentd`** so it loads the new `collective_url`.

---

## 5. (Optional) Enterprise stack — Qdrant, Neo4j, NATS

Skip this for a minimal dev setup.

1. Read [ENTERPRISE.md](ENTERPRISE.md) for Docker env files and passwords.
2. Start services, for example:

   ```bash
   docker compose -f deploy/docker-compose.enterprise.yml --env-file deploy/.env.enterprise up -d
   ```

3. Copy **`config.enterprise.example.json`**, edit URLs and secrets, then run:

   ```bash
   ./agentd -config config.enterprise.example.json
   ```

---

## 6. Terminal agent — `heros-cli`

**Prerequisite:** `agentd` from step 2 must still be running.

1. Build:

   ```bash
   go build -o heros-cli ./cmd/heros-cli
   ```

2. Set an LLM API key (OpenAI-compatible):

   ```bash
   export OPENAI_API_KEY=sk-...
   ```

   On Windows (PowerShell): `$env:OPENAI_API_KEY="sk-..."`

3. Run (point at your repo for local shell):

   ```bash
   ./heros-cli -agentd-url=http://127.0.0.1:8787 -workdir=/absolute/path/to/your/project
   ```

4. **If `agentd` uses `auth_mode=required`**, add:

   ```bash
   ./heros-cli -api-key=YOUR_AGENTD_X_API_KEY
   ```

5. **Ollama (or other OpenAI-compatible local server):**

   ```bash
   ./heros-cli -openai-base=http://127.0.0.1:11434/v1 -model=llama3.2 -no-stream
   ```

   Use **`-no-stream`** if you see errors with streaming + tools.

6. **REPL tips:**
   - `/help`, `/refresh`, `/exit`
   - **`heros_shell`** runs on **your machine** under **`-workdir`**.
   - **`heros_agent_shell`** only appears if you pass **`-agent-shell`** (runs on the **agentd** host under server policy).

---

## 7. (Optional) MCP bridge — `heros-mcp`

For IDEs that use MCP instead of `heros-cli`:

```bash
go build -o heros-mcp ./cmd/heros-mcp
./heros-mcp -agentd-url=http://127.0.0.1:8787
```

Add `-api-key=...` when agentd requires auth. Point your MCP client at this command.

---

## 8. Build everything at once (optional)

From the repo root:

```bash
go build -o agentd ./cmd/agentd
go build -o heros-cli ./cmd/heros-cli
go build -o collectived ./cmd/collectived
go build -o heros-mcp ./cmd/heros-mcp
```

---

## 9. Troubleshooting (short)

| Symptom | Things to check |
|--------|------------------|
| `agentd` won’t start | `auth_mode=required` needs tenant keys in config; paths in `config.json` valid. |
| `heros-cli` says missing API key | Set `OPENAI_API_KEY` or `-openai-api-key`. |
| Empty or broken model turns with Ollama | Try **`-no-stream`**. |
| Skills/tools not updating after disk edits | `POST /api/catalog/reindex` on agentd (or restart + ensure layout under `data_dir`). |
| Wrong shell machine | Default local **`heros_shell`** uses **`-workdir`**; server shell needs **`-agent-shell`**. |

---

## 10. Where to read next

| Topic | Doc |
|--------|-----|
| Folders, SKILL.md, tool.yaml | [AGENT_LAYOUT.md](AGENT_LAYOUT.md) |
| Docker enterprise stack | [ENTERPRISE.md](ENTERPRISE.md) |
| Product / architecture intent | [ARCHITECTURE.md](ARCHITECTURE.md) |
| Roadmap / gaps | [TODO.md](../TODO.md), [TODO-BUSINESS.md](../TODO-BUSINESS.md) |
