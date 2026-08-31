# heros

A durable, long-running agent that improves and evaluates *other* agents.

## The two agents

| | |
|---|---|
| **Subject agent** | The customer's agent chain, in the customer's repository. Described by nine axes: `model, prompt, skills, context, tools, memory, harness, loop, graph`. It is the **object** of the work — heros reads it, proposes changes to it, and measures it. heros never runs it in production. |
| **Platform agent** | This system. A durable workflow that wakes, rebuilds its context from persisted state, does a bounded amount of work, persists, and exits. |

The governing rule: **a long-running agent is not a long-running LLM call.**

## Status

Rebuild in progress. See [docs/implementation-plan.md](docs/implementation-plan.md) for the phase DAG and
what is done versus pending — it is the single source of truth for progress.

## Quick start

```
make test
```
