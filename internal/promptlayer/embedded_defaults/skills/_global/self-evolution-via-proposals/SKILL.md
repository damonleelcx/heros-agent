---
name: self-evolution-via-proposals
title: Self-evolution via proposals
depends_on: [core-reasoning, interaction-learning-loop]
tools: []
---

Use **heros_submit_proposal** to queue changes. Every mutation is reviewed; treat the **diff** as the contract.

**Tooling approvals fail if `diff` is not machine-valid JSON.** For **layer `tooling`** (and **context_engineering** / **harness_engineering**), the server runs `json.Unmarshal` on the diff string. Natural-language plans, bullet lists, or Markdown code fences **inside the diff field** will not apply—submit **only** a single JSON object as plain text (do not wrap the diff value in Markdown code fences).

## Agent-initiated proposals (default posture)

When you realize you are **missing** a skill, tool, memory rule, or harness setting for the user’s recurring work, **you** submit the proposal—do not ask the human to draft it from scratch. Give a clear **title** and **rationale** so they can approve or reject quickly (browser **/** or CLI **/pending**). After approval, the change applies locally and (if the node has **collective_url**) is pushed to the org collective for fleet-wide downstream sync.

## Layer: prompt_engineering (skills + system prompt on disk)

Use a **single text document** with one or more blocks:

```
### SKILL:my-skill-slug
Markdown body only here (no frontmatter in the diff; the server adds YAML on apply).

### SYSTEM_PROMPT
Full replacement text for system/prompt.md (only if you intend to change global behavior).
```

At least one **### SKILL:name** or **### SYSTEM_PROMPT** section is required. Slugs should be kebab-case.

## Layer: context_engineering

**diff** must be **JSON**:

```json
{
  "promote": [{"session_id": "<uuid-or-session-id>", "threshold": 0.35}],
  "links": [
    {"entity_id": "user:acme", "name": "Acme", "kind": "org", "props": {}},
    {"edge_id": "e1", "src": "user:acme", "dst": "project:foo", "rel": "OWNS", "props": {}}
  ]
}
```

Arrays may be empty. Use **promote** to consolidate episodic session content into longer-lived memory when policy allows.

## Layer: harness_engineering

**diff** is **JSON** partial **Topology** (only non-empty fields override):

```json
{
  "specialists": ["researcher", "coder", "writer"],
  "critic_threshold": 0.55,
  "max_critic_retries": 2,
  "leader_model": "gpt-4o-mini"
}
```

## Layer: tooling

**GO-ONLY CONSTRAINT (non-negotiable):**

- New tools must be implemented in **Go source code** under `internal/cliagent/` (or other in-repo Go packages).
- Do **not** introduce Python/Node/bash runtime dependencies as primary tool implementations.
- Do **not** rely on `script_path` wrappers for production behavior.
- Include/extend Go tests for any new tool behavior.

**diff** must be **valid JSON** (strict): one top-level object with a **`register`** key. Prose explanations belong in **`rationale`** / chat—not in **`diff`**.

- **Required shape**: `{"register":{...}}`. Omitting `register` or using non-JSON text causes apply to error after approval.
- **Do not** put Markdown, headings, or “here is the plan…” text in the diff payload.
- **Example** of the exact JSON object the **`diff`** field must contain (serialize this object as UTF-8 text; the fences below are documentation only):

```json
{
  "register": {
    "name": "my-tool-id",
    "description": "What it does; linked skills for catalog graph",
    "risk_tier": "low",
    "skills": ["core-reasoning"]
  }
}
```

**rationale** should name risk, rollback mindset, and who benefits. **title** is a one-line summary for the reviewer UI.
