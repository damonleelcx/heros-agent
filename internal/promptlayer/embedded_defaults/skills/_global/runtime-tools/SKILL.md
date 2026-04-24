---
name: runtime-tools
title: Catalog extension tools (Go)
depends_on: [core-reasoning]
tools: []
---

Many skills under `skills/_global/` describe workflows that used to rely on external script runtimes. In Heros, **executable** access is unified through Go-backed surfaces:

- **Workspace shell:** `heros_shell` (local cwd = workspace).
- **First-party file APIs:** `heros_list_files`, `heros_read_file`, `heros_write_file`, `heros_delete_path`, `heros_make_dir`.
- **Catalog extension tools:** call **`heros_extension_tool`** with:
  - `tool_id` — matches `tools/_global/<id>/tool.yaml` (`id` field / directory name).
  - `arguments` — JSON object; common patterns:
    - **terminal-tool:** `{ "command": "..." }`
    - **file-operations:** `{ "action": "list|read|write|delete|mkdir", ... }` (same fields as the `heros_*_file` tools)
    - **memory-tool:** `{ "action": "search|save", "query"?: "...", "note"?: "..." }`
    - **web-tools:** `{ "url": "https://..." }` (GET only, size-capped)
    - **skills-tool** (and related): `{ "action": "list_skills|list_tools|read_skill", "name"?: "..." }`

Tools that require browsers, remote sandboxes, voice, or third-party daemons return JSON `{ "status": "not_implemented", ... }` with hints—use MCP, `heros-mcp`, or `heros_shell` to integrate those externally.
