---
title: The execution envelope
tier: guide
summary: Where a node may reach, the most it may spend, the most turns any loop inside it may take — the nine fields that bound a node's blast radius, and the two places each one is enforced.
platform_version: 0.20.0
boundary: The envelope is written into your source in no language, permanently — and that is not the same as unenforced. This page explains the axis; your own node's envelope, and its refusals, are on the harness surface in the console.
order: 6
---

An **execution envelope** is what a node is allowed to do, and inside what walls: where it may reach on
the network, the most it may spend, the most turns any loop inside it may take, how many of its steps
may overlap, and which guardrail it answers to.

It **imposes**. The loop **chooses** within it.

## Imposed versus chosen

| Axis | Who decides | | The sentence it answers |
|---|---|---|---|
| Loop | the engineer authoring the change | CHOOSES | *"stop after four turns; reflect between them"* |
| Harness | the operator who owns the deployment | IMPOSES | *"never spend more than a dollar and never reach the network"* |

### Why the loop lives elsewhere

The harness surface described a node's control loop until the axis was split. Two different people were
editing one axis and producing the same class of change with nothing to tell them apart: an operator
tightening a spend ceiling, and an engineer changing a reflection prompt.

The control loop now has its own surface. Nothing was dropped in the re-cut — every item has a named
destination.

### Why the two numbers cannot live on one axis

A loop's `max_turns` is checked against the envelope's `turn_ceiling` when the configuration resolves.
If it is higher, the configuration is **refused** — naming both numbers, never quietly clamped, because
clamping would run a different configuration than the one recorded.

Both numbers are named because they are two different requests. Lowering `max_turns` is yours; raising
`turn_ceiling` is a question for whoever owns the envelope. A refusal that said only *"too many turns"*
would leave you unable to tell which.

Raising a ceiling changes **no** loop configuration. The two live in separate sealed entries, so a
policy change cannot re-hash the configurations underneath it — which is what stops every measurement
taken under the old ceiling from becoming unreachable.

## The nine fields

Three are required, and each of the three is a **blast-radius statement**. The registry refuses an
envelope that omits one. There is no default for those, deliberately: an omitted ceiling reads as
"unbounded" to a person and has to be read as *some* number by the code, and those two readings
differing is how a policy stops being a policy.

The params schema the registry declares, with where each field bites:

```text
sandbox_posture      required   the sandbox, at execution
turn_ceiling         required   resolve — before any diff, worktree, build or provider call exists
spend_ceiling_usd    required   the runtime, before each provider call
host_services        optional   resolve
concurrency_limit    optional   BOTH — resolve, and capped again by the sandbox at execution
retry_budget         optional   the runtime
timeout_seconds      optional   the sandbox, at execution
guardrail_ref        optional   the runtime
approval_gate_ref    optional   the runtime
```

`enforced at` is a first-class fact rather than prose, because the answer differs per field. A reader who
assumes *"refused at resolve"* for all nine will believe the concurrency limit can be bypassed by not
resolving — which is exactly the assumption the sandbox's own limit exists to defeat.

What each one bounds:

| Field | What it bounds |
|---|---|
| Where it may reach | One of `no-network`, `provider-egress-only`, `unrestricted-egress`. There is no safe default for what a node is allowed to talk to, so the registry refuses an envelope that does not say. |
| The most turns any loop may take | 1–16. A policy, imposed. The loop chooses a value at or below it; a loop asking for more is refused when the configuration resolves, naming BOTH numbers. |
| The most it may spend in one run | Checked BEFORE each provider call, not after — checking afterwards enforces a ceiling by having already exceeded it. Exhaustion is a named stopping condition, not an error: the run produced a real, partial answer under a known configuration. |
| Which second actors it grants | Any of `critic`, `planner`, `tool-executor`. A loop needing one the envelope does not grant is refused at resolve, and is never degraded to a strategy that needs none. |
| How many of its steps may overlap | 1–32. Concurrency multiplies a run's PEAK resource use by the group's width, which is what makes this a blast-radius bound rather than a scheduling hint. |
| How many failed turns may be retried | 0–8. It is here rather than on the loop because retries multiply turns, so an unbounded budget would defeat the turn ceiling from the side. |
| The wall-clock bound on one run | 1–3600 seconds. |
| The guardrail it answers to | A reference. Which guardrail applied is part of what makes a result reproducible. |
| The approval gate it answers to | A reference. |

## Refused everywhere is not unenforced

This is the one axis whose answer is *"this is never written into your source, and it is enforced
anyway."* Model, prompt, context, memory, loop and graph all end in a diff or in a refusal that names a
missing rewriter. This one ends in neither.

An envelope is a property of how a node is **deployed** — where it may reach, what it may spend, how
long it may run, how many of its steps may overlap, which guardrail and approval gate it answers to.
None of that is written at a call site in any language, so there is no rewriter that could ever emit it.
Here, "no materializer" is a permanent fact rather than work the platform owes you.

**Refused is not ignored.** Read the listing above: the turn ceiling and the host
services are checked when your configuration *resolves*, before any diff or worktree or provider call
exists. The spend ceiling is checked before each call. The concurrency limit is checked twice.

A reader who took "refused at every call site" to mean "ignored" would be wrong about their own blast
radius, which is the one misreading this axis cannot afford.

## Why the concurrency limit is checked twice

The first check refuses a concurrent group wider than the envelope allows when the configuration
resolves. That gate is early and legible, and it is the one you see.

It is also the one that is **bypassed** by every path that reaches an executor without resolving a spec.
So the sandbox enforces its own limit and does not trust the number it is handed: at most **8**
overlapping steps per group, whatever the spec declared.

When the sandbox has to narrow something, it says so on its health endpoint rather than in a log line —
because *"the early gate is not running"* is invisible in every aggregate: nothing errors, nothing
retries, and the work simply runs narrower than it asked to.

## When a loop needs a second actor

Three control loops need one: `react-loop` needs a tool executor, `plan-execute` a planner, and
`critic-loop` a critic. If the envelope grants none of them, binding that loop is refused — naming the
loop **and** the missing service.

It is never degraded to a loop that needs no second actor. A `critic-loop` without a critic *is*
`reflexion`, and running it under critic-loop's configuration hash would report one strategy's result as
another's.

The refusal is a preflight answer. It used to arrive when a run reached the node — after the change was
already generated and applied.

## Where to go next

- [Refusals are outcomes, not errors](/docs/concepts/refusals)
- [Authored changes](/docs/concepts/authored-changes) — the rules every axis shares
- [Glossary](/docs/concepts/glossary) — `axis`, `gate`, `coverage`
