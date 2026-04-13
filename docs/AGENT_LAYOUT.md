# On-disk agent layout (source of truth)

Skills, tools, and episodic memory **live in folders** under `data_dir`. SQLite holds **indexes** (`skill_fs_index`, `tool_fs_index`, episodic rows with pointers) and `**tool_registry`** (approval + script metadata). Scanning `tools/*/tool.yaml` **upserts** `tool_registry` while preserving `approved`. Approving a tooling proposal **writes** `tool.yaml` from the registry. Neo4j mirrors **the same graph** for queries (`HerosSkill`, `HerosTool`, `DEPENDS_ON`, `USES_TOOL`); skill node `id` is `tenant_scope/name`, tool node `id` is `tenant_scope/tool_id`.

**Repo copy of bundled defaults:** the same tree is versioned at `**internal/promptlayer/embedded_defaults/`** and copied into `data_dir` on first run (missing files only). See [DEFAULT-AGENT-FILES.md](DEFAULT-AGENT-FILES.md).

**Obsidian-style vault:** optional; configure `knowledge_vaults` in `config.json` to index Markdown into `semantic_chunks` / Qdrant, mirror wikilinks into `graph_edges` (and Neo4j if configured), and optionally append agent notes under the vault. See [MEMORY-VAULT.md](MEMORY-VAULT.md).

```
<data_dir>/
  system/
    prompt.md                 # global system prompt (authoritative)
  skills/
    _global/                  # shared skills (recommended default)
      <skill-slug>/
        SKILL.md
    <tenant>/                 # tenant-specific skills (same layout)
      <skill-slug>/
        SKILL.md
    # Legacy (still indexed as tenant _global): skills/<skill-slug>/SKILL.md
  tools/
    _global/                  # shared tools (seed uses this path)
      <tool-slug>/
        tool.yaml
    <tenant>/
      <tool-slug>/
        tool.yaml
    # Legacy: tools/<tool-slug>/tool.yaml → indexed as tenant _global
  memory/
    <tenant>/
      sessions/
        <session-id>/
          meta.json
          turns.jsonl         # one JSON object per line (episodic mirror)
```

## SKILL.md frontmatter

```yaml
---
name: my-skill
title: Human title
depends_on: [core-reasoning]
tools: [echo-safe]
---

Markdown instructions for the model…
```

## tool.yaml

```yaml
id: echo-safe
risk_tier: low
description: What this tool does
script_path: optional/path/or/command
skills:
  - core-reasoning
```

Linking is **bidirectional in meaning**: skills list `tools:` and tools list `skills:`; the graph includes edges from both. After editing files, call `POST /api/catalog/reindex` (and Neo4j syncs if configured). That rescan **merges** filesystem tools into `tool_registry` (keyed by `tenant_id` + `name`) without clearing `approved`. To push registry rows back to disk, use `POST /api/catalog/tools/registry-to-disk` (scope controlled by config below).

## `tool_registry` sync rules (config)

In `config.json`, optional block `tool_registry_sync`:

```json
"tool_registry_sync": {
  "disk_to_db": "all",
  "conflict": "yaml",
  "push_to_disk": "all"
}
```


| Field          | Values           | Meaning                                                                                                                           |
| -------------- | ---------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| `disk_to_db`   | `all` (default)  | Every rescan may refresh metadata from yaml into existing registry rows (subject to `conflict`).                                  |
|                | `approved_only`  | **Existing** rows are updated from disk only when `approved=1`; new tools on disk still **INSERT** with `approved=0`.             |
| `conflict`     | `yaml` (default) | On update from disk: description/tier follow yaml (with defaults); `script_path` updated only if yaml supplies a non-empty value. |
|                | `db`             | Never **UPDATE** existing rows from disk (only **INSERT** missing tools).                                                         |
|                | `yaml_nonblank`  | Per-field: keep DB value when yaml leaves a field empty.                                                                          |
| `push_to_disk` | `all` (default)  | Registry flush writes every row.                                                                                                  |
|                | `approved_only`  | Only rows with `approved=1` are written to yaml.                                                                                  |


Tooling proposals register under the proposal’s `**tenant_id`** (sanitized; empty → `_global`).

**Environment overrides** (optional; set in production without editing JSON). Non-empty values replace the corresponding `tool_registry_sync` field after `config.json` is loaded (`config.Load`, used by `agentd`):


| Variable                                | Same as JSON field |
| --------------------------------------- | ------------------ |
| `HEROS_TOOL_REGISTRY_SYNC_DISK_TO_DB`   | `disk_to_db`       |
| `HEROS_TOOL_REGISTRY_SYNC_CONFLICT`     | `conflict`         |
| `HEROS_TOOL_REGISTRY_SYNC_PUSH_TO_DISK` | `push_to_disk`     |


Example: `HEROS_TOOL_REGISTRY_SYNC_DISK_TO_DB=approved_only` and `HEROS_TOOL_REGISTRY_SYNC_PUSH_TO_DISK=approved_only`.

## Tenant visibility (catalog)

- `GET /api/catalog/skills`: non-admin principals see **their tenant + `_global`**. Admins see all skills; optional `?tenant=<slug>` filters one subtree.
- `GET /api/catalog/skills/body?name=`: resolves **tenant first**, then `_global`.
- `GET /api/catalog/tools`: same visibility as skills (tenant + `_global`, or admin + optional `?tenant=`).

## APIs


| Endpoint                                   | Purpose                                                                                        |
| ------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| `GET /api/catalog/skills`                  | Indexed list from disk                                                                         |
| `GET /api/catalog/skills/body?name=`       | Skill body from file                                                                           |
| `GET /api/catalog/tools`                   | Indexed tools                                                                                  |
| `GET /api/skills/graph`                    | Merged JSON graph (skills + tools)                                                             |
| `GET /api/memory/sessions`                 | Session folders for current tenant                                                             |
| `POST /api/catalog/reindex`                | Rescan + sync `tool_registry` from yaml + optional Neo4j sync                                  |
| `POST /api/catalog/tools/registry-to-disk` | Write `tool_registry` rows to `tools/<tenant>/<id>/tool.yaml` (per `push_to_disk`) and reindex |


## Legacy table `skill_versions`

Older builds stored skill bodies in SQLite. New path is **filesystem + `skill_fs_index` only** for reads. `skill_versions` may still receive rows from old migrations but is **not** used for `LoadSkillMarkdown`.