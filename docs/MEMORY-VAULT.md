# Markdown vault as source of truth (Obsidian-style)

Today, **authoritative human-readable memory** under `data_dir` is mainly **`memory/<tenant>/sessions/.../turns.jsonl`** plus **SQLite** (`episodic_memory`, `semantic_chunks`) and optionally **Qdrant** for vectors. That is **not** the same as an **Obsidian vault** (a tree of `.md` files you edit, link with `[[wikilinks]]`, and browse in Obsidian).

This document describes how to evolve the architecture so **Markdown on disk (the vault) is the single source of truth**, while **agentd** keeps only **indexes and derived views** for fast retrieval—similar in spirit to how Obsidian’s “truth” is the files, and search/graph are derived.

---

## Target properties

| Property | Obsidian-like vault | Current Heros default |
|----------|---------------------|------------------------|
| Primary artifact | `.md` files in a folder | `turns.jsonl` + DB rows |
| Human edits | Direct in editor / Obsidian | Possible on `turns.jsonl`, uncommon |
| Agent read | Should read same files users trust | `heros_memory_search` → DB/Qdrant |
| Agent write | Append/update `.md` (or proposals) | `heros_memory_save` → episodic API |
| Links / graph | `[[notes]]` in files | **Implemented:** wikilinks → SQLite `graph_edges` (`WIKILINK`) + optional Neo4j when vault is indexed |

---

## Recommended vault layout (compatible with Obsidian)

Example (you choose one root or several):

```
~/Vault/
  00-Inbox/
  10-Projects/
  20-Areas/
  30-Resources/
  Daily/
    2026-04-11.md
  Agent/
    session-handoffs/
    long-running/
      project-foo.md
```

- Use **YAML frontmatter** for `tenant_id`, `tags`, `status` if you need multi-tenant or filtering.
- **Daily notes** map naturally to **session logs** or **end-of-day agent summaries**.
- **Evergreen pages** hold stable facts the business cares about; the agent should **retrieve** from these via search, not duplicate them only inside SQLite.

---

## Architecture: three layers

### 1. Source of truth — vault (Markdown + attachments)

- **Only** the vault directory (or a defined subtree) is “truth.”
- Git-sync, Obsidian, or any editor can change files.
- Attachments (PDF, images) stay as files; indexing policy decides whether to extract text elsewhere.

### 2. Index — derived, rebuildable

- **File watcher or periodic scan**: discover `*.md` (and optional `*.mdc`), resolve paths, `mtime`, `sha256`.
- **Chunking**: by heading (`##`), or fixed token/character windows with overlap, per file.
- **Embeddings**: same pipeline as today’s **`semantic_chunks` / Qdrant`** (`Embed` + upsert).
- **Payload** on each vector point should include at least: `source_path`, `vault_root`, `chunk_index`, `tenant_id` (if any), optional `heading`, `mtime`—so hits can cite **which file** to open in Obsidian.

SQLite table `semantic_chunks` uses `source` = `vault:relative/path.md#cN` (chunk index), `text`, `embedding_json`; Qdrant payload includes `source_kind: vault`, `vault_rel_path`, `vault_file_key`, etc.

### 3. Episodic “chat residue” (optional)

Two strategies:

**A. Vault-only writes (strict SoT)**  
- `heros_memory_save` **appends** to a dated note or a per-session note under `Agent/` using a small server-side template (or invokes a user-provided script).  
- **No** duplicate long-term row in `episodic_memory`, or episodic is only a **short-lived buffer** flushed into vault on timer/`/exit`.

**B. Dual-write during transition**  
- Keep episodic + jsonl for debugging; **nightly job** promotes high-importance rows into vault Markdown (mirrors today’s **promote / consolidation** idea).  
- Eventually turn off dual-write when vault coverage is trusted.

---

## Retrieval path (integration with current code)

**Implemented.** **`POST /api/memory/retrieve`** calls **`memorylayer.RetrieveSemantic`** → Qdrant (same collection, `source_kind: vault` in payload) or SQLite cosine over `semantic_chunks`.

1. Vault chunks live in the **same** Qdrant collection as other semantic points (filtered by `tenant_id`).
2. Rank merge is **shared top‑k** over one index (optional future: weighted blend).
3. Vault hits are prefixed **`[vault:relative/path.md]`** in the returned chunk text.

Touchpoints:

- `internal/api/server.go` — `handleRetrieve`, `POST /api/memory/vault/reindex`
- `internal/memorylayer/vector_infra.go` — `RetrieveSemantic`, `RunConsolidation`
- `internal/vaultindex/` — walk, chunk, hash, upsert, wikilink extraction → `graph_edges` / Neo4j

---

## Wikilinks and graph

**Implemented** (during `internal/vaultindex` indexing, when `knowledge_vaults` is configured):

- Parses `[[Note Title]]`, `[[path/to/note]]`, `[[target|alias]]`, `[[note#heading]]`; skips embeds `![[…]]`.
- Resolves targets against the vault file list (stem match, vault-root paths, `./` / `../` relative to the source note); unresolved targets become stub entities (`kind: vault_unresolved`).
- Writes **`rel: WIKILINK`** into SQLite **`graph_edges`** (and **`graph_entities`** for `vault_note` / `vault_unresolved`); mirrors the same to **Neo4j** when `neo4j_*` is configured.
- Deletes a note’s old outgoing wikilinks before re-inserting; removes the note entity when the file disappears from disk.

**Obsidian graph** remains the visual truth for humans; **Neo4j** / SQLite graph support agent queries (`heros_graph_neighbors`) where entity ids are `vn-…` / `vu-…`.

---

## Configuration

Example `config.json` shape (**supported**):

```json
{
  "knowledge_vaults": [
    {
      "path": "C:/Users/me/Obsidian/Vault",
      "tenant_id": "",
      "include_globs": ["**/*.md"],
      "exclude_globs": [".obsidian/**", "**/templates/**"],
      "poll_seconds": 60,
      "vault_append_enabled": true,
      "agent_notes_subdir": "Agent/heros-notes",
      "agent_notes_mode": "daily"
    }
  ]
}
```

- **`path`**: absolute recommended; can be **outside** `data_dir`.
- **`poll_seconds`**: `0` means no background poll (startup + manual reindex still run).
- **`vault_append_enabled`**: episodic `role: note` (e.g. `heros_memory_save`) also appends Markdown under `agent_notes_subdir`.
- **Security**: symlinks are not followed unless `follow_symlinks` is true.

---

## Phased rollout

1. **Read-only index** — **Done:** scan vault → chunks → Qdrant/SQLite; wikilinks → graph; retrieval sees vault + existing semantic memory.
2. **Write-through notes** — **Done (optional):** `vault_append_enabled` + `agent_notes_subdir` / `agent_notes_mode`; episodic mirror unchanged.
3. **Vault as sole long-term store** — Future: turn off episodic persistence or keep only last-N turns; consolidation writes Markdown, not only vectors.

---

## Relation to skills / `data_dir/skills`

- **Skills** (`skills/_global/.../SKILL.md`) are **procedural** catalog entries, not the same as a personal/business **knowledge vault**. They can stay under `data_dir` while the **Obsidian vault** holds **domain knowledge** (clients, specs, meeting notes).
- Optionally add a skill **“use the vault”** that tells the model to prefer `heros_memory_search` after reindex and to cite paths.

---

## Summary

- **Obsidian-style SoT** = **Markdown files** + optional attachments; everything else is index or ephemeral chat buffer.
- **Default Heros** = **session jsonl + DB + optional Qdrant**; with **`knowledge_vaults`** you add **vault-backed** chunks, **wikilink graph** rows, and optional **append-to-vault** for agent notes.
- **Implemented in repo:** `internal/vaultindex`, `knowledge_vaults` in config, `POST /api/memory/vault/reindex`, retrieval prefixes and Qdrant payloads as above.

Cross-links: [AGENT_LAYOUT.md](AGENT_LAYOUT.md), [ARCHITECTURE.md](ARCHITECTURE.md).
