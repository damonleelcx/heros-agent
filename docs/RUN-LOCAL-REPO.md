# Run from a local clone (no global `go install`)

Use this when you have the **repository cloned** and want to run **`heros`** without installing to `GOPATH/bin`. The binary is the same; only how you invoke it changes.

---

## Quickest

From the **repository root** (where `go.mod` lives):

```bash
go run ./cmd/heros
```

---

## Which directory to `cd` into

**`heros_shell`** uses your process **current working directory** as the workspace. So:

- To work on a specific project, **`cd` into that project’s folder first**, then start `heros` so **`heros_shell`** uses that folder as the workspace.

**Why not `go run C:\…\heros-foreal\cmd\heros` from another folder?**  
Go finds the **module from your shell’s current directory**, not from the path after `go run`. A folder without `go.mod` produces *cannot find main module*. Use **`-C`** to set the clone as the module root, or build once and run the `.exe`.

**Windows (example paths):**

```bash
cd C:\path\to\your\actual\project
C:\Users\damon\Downloads\heros-foreal\heros.exe
```

If you have not built `heros.exe` yet, stay in the project directory and either **set `HEROS_WORKDIR`** (recommended on Windows **cmd**) or pass **`-workdir`**:

**cmd.exe (project = Plutux-board, clone = heros-foreal):**

```bat
cd /d C:\path\to\your\actual\project
set HEROS_WORKDIR=%CD%
go run -C C:\Users\damon\Downloads\heros-foreal ./cmd/heros
```

**One-liner with `-workdir` (any shell):**

```bash
go run -C C:\Users\damon\Downloads\heros-foreal ./cmd/heros -workdir C:\path\to\your\actual\project
```

Replace paths with your real clone and project locations.

**Alternative:** run from the clone and pass **`-workdir`**:

```bash
cd C:\Users\damon\Downloads\heros-foreal
go run ./cmd/heros -workdir C:\path\to\your\actual\project
```

**macOS / Linux:**

```bash
cd /path/to/your/actual/project
go run -C /path/to/heros-foreal ./cmd/heros
```

---

## Build once, run from anywhere

```bash
cd /path/to/heros-foreal
go build -o heros ./cmd/heros
# Windows: heros.exe
```

Then from your project:

```bash
cd /path/to/your/project
/path/to/heros-foreal/heros
```

---

## Config and API key

- You can run with **no config file**: defaults apply (data dir is usually under your home directory, e.g. `~/.heros-agent`).
- For custom settings: copy **`config.example.json`** to **`config.json`** in a directory that **config discovery** will find (see [STEP-BY-STEP-RUN.md](STEP-BY-STEP-RUN.md)), or use **`%APPDATA%\heros\config.json`** on Windows / **`UserConfigDir/heros/config.json`** on Unix.
- If no key is configured, **`heros`** **prompts** for an API key on first run (hidden on a real TTY) and can **save** it under **`%APPDATA%\heros\config.json`** (or the Unix equivalent).

---

## Default workspace (any install path)

Heros stores **`cli_workdir`** in **`%APPDATA%\heros\config.json`** (same file as `openai_api_key`). On startup, **`-workdir` → `HEROS_WORKDIR` / `INIT_CWD` → saved `cli_workdir` → cwd** (if cwd is the clone root, it falls back to your home until you **`/cd`** once).

Inside the REPL: **`/cd D:\your\project`** updates the shell workspace and saves it; **`/exit`** saves again. Skills and SQLite/Qdrant always use **agentd `data_dir`** (not the shell cwd).

---

## Summary

| Goal | What to do |
|------|------------|
| Try from clone | Repo root: `go run ./cmd/heros` |
| Work on project X | `cd` to X, then `go run -C /path/to/clone ./cmd/heros`, or run built `heros` from X |
| Override workspace without `cd` | From clone: `go run ./cmd/heros -workdir /path/to/project` (or `heros -workdir …` after install) |
| Approve skills/tools | REPL: `/pending`, then `/approve <id>` (no browser required) |

Full install / discovery details: [STEP-BY-STEP-RUN.md](STEP-BY-STEP-RUN.md) · Overview: [README](../README.md).
