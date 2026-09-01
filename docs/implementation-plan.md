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

### [x] P15 · A real model provider
DeepSeek, called for real. `internal/provider` is the boundary; `internal/provider/deepseek` is the
client; `internal/tools` is the first tool that actually calls a model.

Measured on 2026-08-31, `deepseek-v4-flash`, nine axes over a real Postgres: **8 model calls, 4,397
tokens, $0.0016, 24.4s.** The `graph` axis failed because topology cannot be read without executing the
customer's code, and the synthesis blocked behind it — both as designed.

Spend is a **micro-cent** ledger against a **cent** ceiling. A real call proved why: 300 tokens cost
0.0127 cents, and rounding up to the cent — the correct instinct, since under-reporting is the dangerous
direction — overstated it 79x.

DeepSeek prices **double during peak hours** (01:00–04:00 and 06:00–10:00 UTC, Mon–Fri), so the price is
selected per call from the call's own clock, defaulting to peak when uncertain.

### [x] P16 · Repository intake and discovery
The fixture is gone. `internal/intake` resolves what a person types — a local path or a GitHub link — to
a PINNED revision, and refuses when it cannot: without a revision, "did my change help?" compares against
something that has moved. A dirty working tree is recorded and surfaced rather than silently pinned.

`internal/discovery` reads the repository within four bounds (containment, skipped trees, file size, file
count) and extracts per-axis EVIDENCE: real spans of the customer's code with file and line. It finds
evidence and stops; a model judges it. The two halves fail differently — extraction fails the same way
every time and can be fixed, judgement fails by being wrong in a plausible sentence — so keeping them
apart means a false claim can be traced to which half produced it.

Verified against two real repositories, which corrected two things reasoning had not:
- **Test files dominate.** Nearly every call site and axis span landed in test code, because tests
  instantiate models and set temperatures more explicitly than production code does. Excluded and
  counted.
- **Alphabetical sampling is a biased sample presented as evidence.** Spans are now ranked by proximity
  to a call site before truncation, and the sample discloses how it was ranked.

`Corpus.LooksLikeAnAgent()` separates "this repository has nine weaknesses" from "this is not an agent".

### [x] P17 · The console, wired to the engine
`internal/router` turns a sentence into one of the nineteen intents, a named redirection, or an
abstention — deterministically, because a component that decides whether to spend money should not
itself cost money on every keystroke, and because it must be testable against a fixed holdout that a
model's answers would move under. 70 held-out questions, ≥80% recall per intent, 100% abstention
precision.

`internal/api` serves subject intake, routing, and a live SSE stream of run progress.
`cmd/herosd` serves the console and drives goals with `context.Background()` — a durable goal's lifetime
is not the browser request's, or a refresh would cancel an hour of work.

Three bugs the wiring exposed:
- **"remember" contains "member"**, so a memory question redirected to the members page. Redirect
  matching is word-boundary now, not substring.
- **Nine axes rescanned the corpus nine times** — 26 seconds to load a 2,541-file repository.
  `discovery.Index` scans once, and the reuse is a TYPE rather than a hidden cache, because a hidden
  cache on a value type is a lie about aliasing.
- **The scope in a sentence was thrown away.** "How to improve prompt?" planned a nine-axis run. The
  principle was already written in `goal.Axes` and was a comment rather than a code path.

### [x] P18 · Discovery performance
A 2,541-file repository took **17.2 seconds** to index and now takes **764ms** — 22.6x — with evidence
proven byte-identical before and after. End to end over HTTP: 26s at the start of this work, **1.1s** now.

The route there was three wrong guesses and one profile:
1. A gate regex made of the union of all thirty axis patterns: **no change at all.** A union of complex
   expressions costs what its parts cost — it is the same automaton.
2. A bitmask index so a gated line only runs patterns whose hints it contains: **no change.** The
   per-pattern work was never the bottleneck.
3. The profile said the *gate itself* was 3.2s of `regexp.(*machine)` plus 1.9s of GC pressure from its
   allocations. Go's regexp is a general automaton and pays general-automaton costs even when every
   branch is a constant string.

The fix is a two-byte prefix index over the hint literals (`internal/discovery/literals.go`): one pass,
no automaton, no allocation. Applied to the axis patterns and then to the call-site scan.

**The optimisation is fenced, not trusted.** A hint that is too narrow does not fail loudly — it silently
drops findings, and this package reports absence AS a finding, so a dropped signal becomes a confident
claim that a customer's agent has no memory strategy. `TestTheLiteralGateChangesNothing` and
`TestTheCallSiteGateChangesNothing` run every pattern with the gate on and off over a real
2,541-file repository and assert the results are identical.

### [x] P19 · Truncation, and the default nobody chose
Three axes truncated on every real repository and none on fixtures. It was never a budgeting problem.

The provider enables chain-of-thought **by default, at `high` effort**. Reasoning tokens are billed as
output and consume `MaxTokens` before the answer begins — measured on a real excerpt: **243 of 296
output tokens were reasoning, leaving 53 for the answer.** Raising the ceiling would have bought more
thinking, not more answer.

Thinking mode also silently disables `temperature`: the provider documents that it "will not trigger an
error but will also have no effect". Every call had been setting temperature 0 for determinism and
getting neither the determinism nor an error.

`provider.Reasoning` now has **no usable zero value** — `ValidateRequest` refuses an unset one, the same
way it refuses an unset ceiling. A forgotten field must not silently buy the expensive option.

Per-axis budgets are measured (50–120 output tokens for the fixed reply shape, budgeted at ~4x) and
**escalate with the attempt number**, because a retry that repeats an identical request is not a retry.
Two escalations, then a failure that says the budget was raised and still ran out — a different report
from "it truncated", and the one that says the prompt is wrong rather than the number.

Same repository, before and after: 12 model calls → **9**; 22,750 tokens → **9,244**; $0.0085 →
**$0.0045**; 2m8s → **15.3s**; 6 of 9 axes assessed → **9 of 9**.

### [x] P20 · The synthesis
The join that answers "what is weak here?". First fully green run: **10 of 10 tasks**, 9 of 9 axes,
7 actionable, $0.0015, 14.4s.

**The ordering, the counts and the unmeasured list are COMPUTED; only the connective sentence comes from
a model.** The two halves fail differently and only one is survivable: a miscomputed ranking is wrong the
same way every time and can be fixed, while a generated summary is wrong by being *plausible* — it will
state that an agent has no memory strategy when the memory axis was never assessed, in a well-formed
sentence, next to eight true ones.

So the model may connect the findings it was given and may not add one. `validate` rejects a synthesis
naming any axis that produced no finding, and the unmeasured axes are never shown to it in the first
place. Refusing costs one retry; publishing costs a customer acting on a weakness never observed in
their code.

A join needs its edges, so `toolcontract.Call` gained `Inputs` — the results of *declared* dependencies
only. A tool that could read any result would make the DAG's edges decorative: the graph would say what
waits for what while the data flowed wherever a tool reached.

## !!! Not started, and deliberately so

### [ ] P11 · Tier-A `compare` execution

### [~] P12 · Timeline / observability
Event model and recording exist and are written by the kernel. The *query* side — "what happened, why,
when, what next" as a rendered timeline — is not built.

### [ ] P13 · Eval scenarios + recovery drills
Kill workers mid-task, duplicate events, stale data, unavailable APIs; assert correct resume.

### [ ] P14 · Gradual autonomy rollout

### [x] P17 · The console, wired to the engine
`internal/router` turns a sentence into one of the nineteen intents, a named redirection, or an
abstention — deterministically, because a component that decides whether to spend money should not
itself cost money on every keystroke, and because it must be testable against a fixed holdout that a
model's answers would move under. 70 held-out questions, ≥80% recall per intent, 100% abstention
precision.

`internal/api` serves subject intake, routing, and a live SSE stream of run progress.
`cmd/herosd` serves the console and drives goals with `context.Background()` — a durable goal's lifetime
is not the browser request's, or a refresh would cancel an hour of work.

Three bugs the wiring exposed:
- **"remember" contains "member"**, so a memory question redirected to the members page. Redirect
  matching is word-boundary now, not substring.
- **Nine axes rescanned the corpus nine times** — 26 seconds to load a 2,541-file repository.
  `discovery.Index` scans once, and the reuse is a TYPE rather than a hidden cache, because a hidden
  cache on a value type is a lie about aliasing.
- **The scope in a sentence was thrown away.** "How to improve prompt?" planned a nine-axis run. The
  principle was already written in `goal.Axes` and was a comment rather than a code path.

### [x] P18 · Discovery performance
A 2,541-file repository took **17.2 seconds** to index and now takes **764ms** — 22.6x — with evidence
proven byte-identical before and after. End to end over HTTP: 26s at the start of this work, **1.1s** now.

The route there was three wrong guesses and one profile:
1. A gate regex made of the union of all thirty axis patterns: **no change at all.** A union of complex
   expressions costs what its parts cost — it is the same automaton.
2. A bitmask index so a gated line only runs patterns whose hints it contains: **no change.** The
   per-pattern work was never the bottleneck.
3. The profile said the *gate itself* was 3.2s of `regexp.(*machine)` plus 1.9s of GC pressure from its
   allocations. Go's regexp is a general automaton and pays general-automaton costs even when every
   branch is a constant string.

The fix is a two-byte prefix index over the hint literals (`internal/discovery/literals.go`): one pass,
no automaton, no allocation. Applied to the axis patterns and then to the call-site scan.

**The optimisation is fenced, not trusted.** A hint that is too narrow does not fail loudly — it silently
drops findings, and this package reports absence AS a finding, so a dropped signal becomes a confident
claim that a customer's agent has no memory strategy. `TestTheLiteralGateChangesNothing` and
`TestTheCallSiteGateChangesNothing` run every pattern with the gate on and off over a real
2,541-file repository and assert the results are identical.

### [x] P19 · Truncation, and the default nobody chose
Three axes truncated on every real repository and none on fixtures. It was never a budgeting problem.

The provider enables chain-of-thought **by default, at `high` effort**. Reasoning tokens are billed as
output and consume `MaxTokens` before the answer begins — measured on a real excerpt: **243 of 296
output tokens were reasoning, leaving 53 for the answer.** Raising the ceiling would have bought more
thinking, not more answer.

Thinking mode also silently disables `temperature`: the provider documents that it "will not trigger an
error but will also have no effect". Every call had been setting temperature 0 for determinism and
getting neither the determinism nor an error.

`provider.Reasoning` now has **no usable zero value** — `ValidateRequest` refuses an unset one, the same
way it refuses an unset ceiling. A forgotten field must not silently buy the expensive option.

Per-axis budgets are measured (50–120 output tokens for the fixed reply shape, budgeted at ~4x) and
**escalate with the attempt number**, because a retry that repeats an identical request is not a retry.
Two escalations, then a failure that says the budget was raised and still ran out — a different report
from "it truncated", and the one that says the prompt is wrong rather than the number.

Same repository, before and after: 12 model calls → **9**; 22,750 tokens → **9,244**; $0.0085 →
**$0.0045**; 2m8s → **15.3s**; 6 of 9 axes assessed → **9 of 9**.

### [x] P20 · The synthesis
The join that answers "what is weak here?". First fully green run: **10 of 10 tasks**, 9 of 9 axes,
7 actionable, $0.0015, 14.4s.

**The ordering, the counts and the unmeasured list are COMPUTED; only the connective sentence comes from
a model.** The two halves fail differently and only one is survivable: a miscomputed ranking is wrong the
same way every time and can be fixed, while a generated summary is wrong by being *plausible* — it will
state that an agent has no memory strategy when the memory axis was never assessed, in a well-formed
sentence, next to eight true ones.

So the model may connect the findings it was given and may not add one. `validate` rejects a synthesis
naming any axis that produced no finding, and the unmeasured axes are never shown to it in the first
place. Refusing costs one retry; publishing costs a customer acting on a weakness never observed in
their code.

A join needs its edges, so `toolcontract.Call` gained `Inputs` — the results of *declared* dependencies
only. A tool that could read any result would make the DAG's edges decorative: the graph would say what
waits for what while the data flowed wherever a tool reached.

## !!! Not started, and deliberately so

- **Discovery is heuristic, not a parser.** It finds candidate evidence by pattern, so a false positive
  is cheap (the model discards it) and a false negative is expensive (the axis reports absence). Python,
  TypeScript, JavaScript and Go only. Every report states that it shows what was found, not everything
  that exists.
- **Tier-C effect intents are routed but not built.** `author`, `prompt`, `model` and `deliver` return a
  refusal naming why, rather than a generic failure.
- **`internal/memory` is written and untested.** The four classes and the promotion rules compile; there
  is no store implementation and no test. It is not wired into anything.
- **Console / HTTP surface.** No API and no UI in this tree.
