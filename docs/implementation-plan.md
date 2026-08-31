# Implementation plan — phase DAG

**Status of this document:** live. It is the single source of truth for what is built and what is not.
Every phase must close on its own — it is not done until its own tests pass without the next phase.

## Legend

`[x]` done and tested · `[~]` partially done, gap named · `[ ]` not started

## Phase DAG

```
P0 scaffold ──┬─> P1 intent set ─────────┐
              │                          │
              └─> P2 durable kernel ──┬──┴─> P5 tier-B queries
                   (goal/task/store)  │
                                      ├─> P3 queue + leases ─> P4 worker loop ─┬─> P6 tier-A: assess
                                      │                                        ├─> P7 tier-C: effects
                                      └─> P8 bounds + refusals ────────────────┤   (+approval gates)
                                                                               ├─> P9 tier-A: evalset
                                                                               ├─> P10 tier-A: improve
                                                                               └─> P11 tier-A: compare
P12 timeline/observability ── cuts across P2..P11
P13 eval scenarios + recovery drills ── gates P14
P14 gradual autonomy rollout
```

## Phases

### [x] P0 · Scaffold
Module, README, architecture, this plan. Closes on: `make test` runs.

### [x] P1 · The closed intent set
Nineteen intents, three tiers, structural fences. Fifteen carried from the previous system plus four
resolved gaps: `skills` and `tools` were axes with no intent; `evalset` had a route but no intent; and
`prompt_model` conflated two axes into one intent.
Closes on: fences fail when an intent has no tier, no surface, or no question.

### [x] P2 · Durable kernel — goal, task DAG, store
`Goal` (objective, ceilings, completion criteria). `Task` DAG with typed dependency edges and a
`Ready()` computation. Store interface + in-memory implementation with checkpoints.
Closes on: a goal round-trips; a DAG reports the right ready set; a checkpoint restores.

### [x] P3 · Durable queue + leases
Lease-based claim so two workers cannot execute one task. Lease expiry returns the task to the queue.
Closes on: concurrent claims yield exactly one winner; an expired lease is reclaimable.

### [x] P8 · Bounds and typed refusals
Hard ceilings (iterations, tokens, tool calls, cost, wall-clock, spawn depth) and the closed refusal
set. Unbounded requests are refused, never defaulted.
Closes on: each ceiling trips; each refusal names a next action.

### [x] P2b · Postgres store
Baseline schema, embedded idempotent migrations, and a Postgres `Store`. The claim is ONE
`UPDATE … FOR UPDATE SKIP LOCKED` statement: a read-then-write claim has a window in which two workers
both see a task as free, and the window is small enough to reach production and rare enough to be
blamed on something else. Idempotency is enforced by a partial unique index rather than in application
code, which loses the race it exists to win.
Closes on: the conformance suite passes against a live Postgres, and the skip-is-not-a-pass fence
confirms the Postgres leg actually ran.

### [x] P4 · Worker loop + tool contracts
`RunOnce` performs exactly one bounded cycle: observe → plan → execute → verify → persist → continue.
The loop belongs to the CALLER, so "test recovery explicitly" is a matter of not calling RunOnce again —
which is what a crash is.

Tools are contracts, not functions: declared permissions, a required timeout, retry-safety, and a
SEPARATE verifier. A tool verifying itself asks the component that may have failed whether it failed.
An effect-bearing tool with no verifier is refused at registration.
Closes on: verification failure fails the task; an unconfirmable effect is never retried; the retry
ladder is bounded; cancellation releases the lease; the approval gate parks without holding one; a
crashed worker is recovered by another from persisted Postgres state.

**!!! Uses fakes at the tool boundary only.** No real model provider is wired yet, so the loop is proven
against a substituted external world. That is the correct seam to fake, but it means no token or cost
figure in this repo has yet come from a real call.

### [ ] P5 · Tier-B query surfaces
Eleven read-only intents over the store.

### [ ] P6 · Tier-A `assess`
Nine-axis assessment of a subject repository. Requires P4 + subject-repo discovery (not yet scoped).

### [~] P7 · Tier-C effects + approval gates
The GATE is built and tested (`GateEffectsOutsideThePlatform`, default-deny on anything touching the
customer's world). The four effect intents and the approval *surface* — where a human actually sees and
answers the request — are not.

### [ ] P9 · Tier-A `evalset`
Generated eval sets. Four generators: seed-from-real-traces, schema-driven, LLM-driven,
adversarial-perturbation.

### [x] P4b · Planner / executor split + replanning
One component owns the plan; workers own one task each and know nothing about the shape around them.
The plan exists as ROWS, so it can be shown to a person before it costs anything, rather than as control
flow that can only be read by running it.

`assess` fans out one task per axis and joins on a synthesis — so an unmeasurable axis is one blocked
row in a report that still has eight, rather than an assessment that did not happen. `improve` plans an
assessment and grows proposal chains from findings that actually exist. `evalset` gates quality between
generation and publication, because a generator scoring its own output marks its own homework.
`compare` waits for both runs.

Replanning is a DIFF, not a conversation: the plan changes because facts arrived, not because a model
was asked what it fancied next. Bounded three ways — task ceiling, spawn depth, and never re-adding an
existing id (which would reset attempt counters and let a failing task retry forever while every
individual round stayed inside its limits).
Closes on: every durable intent has a planner or the registry refuses to build; replanning is idempotent;
pull-request idempotency keys are stable across replans and clocks.

### [x] P6/P10 · `assess` and `improve` end to end
Proven against live Postgres, including a mid-run process death and resume.

**!!! The tool boundary is still substituted.** No real model provider, and subject-repository discovery
does not exist — the assessment tools return fixtures. What is proven is the ORCHESTRATION: the ordering,
the gates, the bounds, the persistence and the resume.

### [ ] P11 · Tier-A `compare` execution

### [~] P12 · Timeline / observability
Event model and recording exist and are written by the kernel. The *query* side — "what happened, why,
when, what next" as a rendered timeline — is not built.

### [ ] P13 · Eval scenarios + recovery drills
Kill workers mid-task, duplicate events, stale data, unavailable APIs; assert correct resume.

### [ ] P14 · Gradual autonomy rollout

## !!! Not started, and deliberately so

- **Subject-repository discovery / IR.** Every Tier-A goal depends on parsing the customer's agent
  chain. It is the largest single upstream dependency and is not yet scoped. `assess`, `evalset`,
  `improve` and `compare` cannot complete without it.
- **Console / HTTP surface.** No API and no UI in this tree.
