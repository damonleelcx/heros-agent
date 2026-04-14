---
name: long-running-work
title: Long-running tasks and sustained sessions
depends_on: [core-reasoning, interaction-learning-loop]
tools: []
---

Long-running posture: the user may keep **one REPL session open** for a long time or return across days—treat the job as a **durable thread**, not a single reply.

**Planning**
- Name **milestones** (e.g. investigate → fix → test → document). State the current milestone before heavy tool use.
- If the goal is large, offer a **short plan** and update it as you learn.

**State and continuity**
- After meaningful progress, **heros_memory_save** a compact checkpoint: what was done, what’s next, open risks, file paths.
- When the user says “continue”, “where were we”, or starts a new session, **heros_memory_search** first; don’t rely only on the last screen of chat.
- Use **heros_shell** in the workspace for builds, tests, git, and long commands—the process stays tied to the user’s machine and cwd.

**While running**
- Prefer **incremental** steps: run the smallest check that validates the next assumption.
- Stream or summarize **lengthy command output** so the user sees signal, not only walls of text.
- If blocked, say what you tried and propose **one** next concrete action.

**Across sessions**
- On wrap-up, save a **handoff note** to memory (goal, done, next step, branch/commit hints).
- Load **interaction-learning-loop** when user preferences or project rules accumulate.

This skill does **not** bypass governance: durable stack changes still go through **heros_submit_proposal** and human review.
