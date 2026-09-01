# Architecture

## Why this document exists

The rebuild replaces a system that worked but had grown 2,261 Go files across fourteen phases. The
value being preserved is not the code; it is four hard-won design results (§6) and a closed intent set.
Everything else is being rebuilt against one principle:

> A long-running agent should not be a long-running LLM call. It is a durable workflow system in which
> the model repeatedly wakes up, reconstructs its state, performs a **bounded** amount of work,
> persists the result, and safely continues later.

## 1. The two agents

Conflating these is the most expensive mistake available in this codebase, so it is stated first.

**Subject agent** — the customer's agent chain, living in the customer's repository. Described by nine
axes. It is the *object* of the work. heros parses it, proposes changes to it, and measures it.

**Platform agent** — this system. The durable worker that does the assessing, improving and evaluating.
Every principle in this document is about the platform agent.

The product loop:

```
user goal → plan → durable task DAG → queue → worker (leased) → tools → verify
   → checkpoint → [approval gate] → next task → … → verified proposals + eval set → PR
```

## 2. Intents are tiered — not every intent is a goal

"Define the long-running goal" is a **filter**, not a blanket. Building all nineteen intents as durable
workflows would put a queue, a lease and a checkpoint behind "what happened in that run?", which is a
database read.

| Tier | Intents | Machinery |
|---|---|---|
| **A · Durable goal** | `assess`, `improve`, `evalset`, `compare` | Full stack: planner/executor split, task DAG, checkpoints, leases, approval gates, replanning, ceilings |
| **B · Query** | `graph`, `run_history`, `coverage`, `context`, `memory`, `harness`, `loop`, `graph_order`, `preview_change`, `skills`, `tools` | Single turn, reads the store a Tier-A run wrote. No queue, no checkpoint. |
| **C · Bounded effect** | `author`, `prompt`, `model`, `deliver` | Idempotency key + approval gate. Effect-bearing but not long-running. |

Tier B is the *payoff* of "persist all state": because Tier A writes everything down, answering a
question is a `SELECT` rather than an agent run. That is what makes the console fast and the agent cheap.

## 3. Subsystems, and which principle each discharges

| Subsystem | Principles | Shape |
|---|---|---|
| `goal` | 1 | The long-running goal: objective, ceilings, completion criteria, milestones |
| `task` | 2, 12 | Durable task DAG. Typed edges; a blocked task waits on prerequisites |
| `store` | 5, 6, 7 | Postgres is the only memory. Checkpoint after every meaningful step |
| `memory` | 20, 21 | Four **separate** classes: task state, episodic history, reusable knowledge, user preferences |
| `queue` | 9, 10, 22 | Durable queue; workers hold **leases**, not locks; pause/resume is a state, not a signal |
| `wake` | 11 | Timers, webhooks, queue messages, external state change |
| `planner` / `executor` | 3, 4, 18 | One component owns the plan, workers own single tasks; replanning diffs state against goal |
| `toolcontract` | 14, 8 | Typed in/out, permissions, timeout, retry policy, **idempotency key per side effect** |
| `verify` | 15 | Never trust a tool's exit code. Confirm the real-world result independently |
| `recovery` | 16, 17 | Retry ladder, backoff, alternate model, explicit failure states, partial-failure isolation |
| `bounds` | 19 | Hard ceilings: iterations, retries, tokens, tool calls, cost, wall-clock, recursive spawn |
| `approval` | 23, 24 | Human gate before irreversible/expensive/sensitive acts; cancellation leaves no half-written state |
| `timeline` | 25, 26 | Answers *what happened, why, when, and what next* |
| `evalscenario` | 27, 28, 29, 30 | Multi-day workflows, crashes, duplicate events, stale data; failures become regression tests |

## 4. The agent loop

```
observe  → read durable state; reconstruct context (never trust the model's context window)
plan     → what is the next bounded unit of work?
execute  → run one task through a typed tool contract
verify   → confirm the real-world result actually occurred
persist  → checkpoint; write the timeline entry
continue → release lease; enqueue successors, or wake later
```

Each cycle is **bounded**. A worker that cannot finish a task within its lease renews or yields; it
never holds a model call open across the boundary.

## 5. Fail-closed defaults

Three rules inherited deliberately from the previous system, each of which cost a real incident:

1. **A question that cannot be bounded is refused, not run with defaults.** Defaults are how an agent
   spends someone's money on a search they did not ask for. The failure is silent and is discovered on
   an invoice. Every refusal names a next action.
2. **An effect-bearing step must produce an artifact.** If a step can cause an effect and cannot name
   what it produced, it did not happen, and the run says so.
3. **A ref that lands in the wrong dimension fails closed.** Identity is content-addressed, and the
   dimension is part of the hash, so a mismatched reference cannot silently resolve to the wrong thing.

## 6. What was carried forward from the previous system, and why

These are designs, not code. Each was expensive to learn and invisible until it regressed.

- **Closed intent set with a structural fence** — an intent with no surface, or a surface with no
  intent, fails the build. Without it a surface ships, nobody adds its intent, and the agent answers
  with a refusal that is indistinguishable from the feature not existing.
- **Typed refusal causes** — "I cannot do that" teaches a person nothing. Each cause leads to a
  different next action.
- **Effect-bearing kinds require an artifact** — one table a reviewer can check, rather than three
  constructors a reviewer must trust.
- **Content-addressed identity** — the kind is hashed into the id, so cross-dimension mistakes fail
  closed rather than resolving to the wrong entry.
