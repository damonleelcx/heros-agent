---
name: loop-engineering
title: Closed-loop task execution
depends_on: [core-reasoning, interaction-learning-loop, long-running-work]
tools: [heros_memory_search, heros_memory_save, heros_shell, heros_agent_shell, heros_run_harness, write_todos]
---

Use this skill when a task needs a **repeatable execution loop** instead of a one-shot answer.

## Core loop
1. **Frame the goal** in one sentence and write down the success criteria.
2. **Ground the current state** with tools before acting: inspect files, search memory, or run the smallest useful command.
3. **Plan the next action** as a tiny step that can be verified.
4. **Execute** the step.
5. **Verify** the result with tests, diffs, file reads, or a second pass.
6. **Decide** whether to stop, iterate, or escalate.

## When to use the loop
- The user asked for implementation, repair, or investigation across multiple steps.
- A result can regress unless it is checked.
- The task depends on repository state, prior decisions, or remembered preferences.
- The work should continue until validation passes or a hard blocker is proven.

## Operating rules
- Start with `write_todos` when there is more than one meaningful step.
- Use `heros_memory_search` before long work or when a prior decision may matter.
- Save a short checkpoint with `heros_memory_save` after meaningful progress.
- Prefer the smallest command or edit that can prove the next assumption.
- If a check fails, do not narrate around it; inspect the failure, fix the root cause, and rerun.
- If the same fix/issue appears repeatedly, treat that as signal to evolve the skill or workflow rather than just repeating the loop forever.

## Stop conditions
- The requested artifact is complete and verified.
- The next step requires missing external input.
- A hard blocker is proven after reasonable retry and inspection.

## Good outputs
- State the current loop phase plainly.
- Report what changed, what was verified, and what remains.
- Keep the user oriented with a short checkpoint instead of a long recap.
