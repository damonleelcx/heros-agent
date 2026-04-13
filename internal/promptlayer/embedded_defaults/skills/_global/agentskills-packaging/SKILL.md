---
name: agentskills-packaging
title: Packaging skills (agentskills-style)
depends_on: [core-reasoning]
tools: []
---

Align new skills with small, composable **procedural memory** (similar in spirit to [agentskills.io](https://agentskills.io)):

- **One skill = one job** (e.g. “how we run migrations here”, “how we format API errors”).
- **Frontmatter** on disk: `name`, `title`, `depends_on`, `tools` — keep `depends_on` minimal and real.
- Prefer **updating** an existing skill via **### SKILL:existing-slug** proposals over duplicating overlapping skills.
- Include **triggers**: when to apply this skill (keywords, repo areas, user roles).
- After approval, remind the user they can **reindex** if their deployment requires **POST /api/catalog/reindex**.

Proposal path for *new* on-disk skills still goes through **prompt_engineering** diffs as above, not by writing files from the chat model directly.
