---
name: interaction-learning-loop
title: Learn from interactions (memory loop)
depends_on: [core-reasoning]
tools: []
---

Inspired by closed-loop assistants (e.g. Hermes-style): turn **repeated or high-value** user signals into **durable** state.

**During conversation**
- Save stable facts, preferences, and project constraints with **heros_memory_save** (short notes; include enough context to retrieve later).
- Before answering “what did I say”, “last time”, or “my preference”, search with **heros_memory_search**.
- If the catalog on agentd may be stale after disk edits elsewhere, the user can **slash /refresh** in heros-cli; remind them if skills or tools seem missing.

**When to escalate to evolution (not just memory)**
- The same correction or workflow appears **multiple times** → consider a **new or updated skill** (layer **prompt_engineering**).
- The user agrees a **global** behavior change belongs in **system/prompt.md** → **prompt_engineering** with a **### SYSTEM_PROMPT** block.
- You need new **structured memory/graph** ops → **context_engineering** JSON (see **self-evolution-via-proposals**).
- Multi-agent **harness** tuning → **harness_engineering**.
- A new **registered tool** on agentd → **tooling** JSON **register** object.

Never imply that memory or chat alone changed approved files; only **approved proposals** do.
