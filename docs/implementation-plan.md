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

### [x] P21 · Tier-C effects — the first writes to somebody's code
`author`, `prompt`, `model`, `deliver`. They run IN-TURN rather than as durable goals, because the
tiering says so and the tiering is right: a bounded change is one model call and a diff, and a queue
would add every failure mode of a distributed run to something that finishes in two seconds.

`internal/edit` is built to REFUSE. A refused edit costs one exchange; a wrong edit costs a corrupted
file discovered later and attributed to us. So every ambiguity resolves to a refusal: text that occurs
more than once, a replacement that re-indents (in Python indentation IS block structure — a statement
moved one level out is valid, parseable, and in a different scope), a path leaving the repository, a
no-op, or a file that changed between proposing and approving.

Line numbers are never identity — the anchor is the text. An unrelated edit above moves every line
below it, and an edit applied by line number rewrites whatever now sits there.

**Nothing is pushed.** Approval writes the change on a new branch and commits it, then hands back the
exact `git push` command. Pushing would need a credential with write access to somebody's repository,
held by this process, used while they are not present — the standing grant that repository connection is
deliberately out of scope for. One more step for them, one fewer credential for us.

Proven against a disposable repository: proposal → minimal diff (1 file, 1 insertion, 1 deletion) →
branch → commit; decline leaves the file byte-identical and HEAD unmoved; a second decision on the same
change is refused.

`deliver`'s QUESTION was corrected, not its tier. It read "how does an approved change reach my
repository?" — explanatory wording on an effect-bearing intent, which would eventually have been
implemented as whichever half the reader noticed.

### [x] P22 · Memory, wired
Four classes, four tables, because they differ in lifetime, in who may write, and in what a row must
carry to be trustworthy. One table forces one shape and one write path onto all four, and the concrete
failure is knowledge: an agent able to INSERT there launders its own speculation into fact, and the next
goal reads it as if somebody had established it.

- **Knowledge cannot be written, only PROMOTED**, citing episodes, and only observations or effects — a
  decision is the agent's own reasoning, and promoting one is the laundering step. The schema requires
  evidence; it is not a convention.
- **Preferences require a human author.** The Go type refuses system identities, the column refuses an
  empty one. An agent that infers "they seem to prefer aggressive refactors" has invented a mandate.
- **Compression never folds a failure or an effect**, and never deletes its source. What broke and what
  changed in the world are the two things a reader most needs from an old run.

The payoff is that **`run_history` is now a real answer** rather than a placeholder: "what happened in
that run?" is a SELECT over what a durable run wrote down, and it costs nothing. That is what persisting
everything was for.

Two bugs the wiring exposed:
- `MAX(seq)+1` inside an INSERT is **not** atomic — 16 concurrent writers produced 6 sequences and 10
  errors. Now a per-goal advisory lock, released on rollback or a dropped connection so a dying worker
  cannot wedge a goal's log.
- Run history answered about **the lexically-last goal**, not the newest — ids carry the prefix of
  whatever created them (`g-`, `live-`, `e2e-`), so a leftover test goal sorted last and the real run
  reported "no episodes" while holding nine. `store.LatestGoal` now asks the question that was meant.

### [x] P25 · Authentication and multi-tenancy
**Isolation is structural, not remembered.** Twelve of the fourteen store methods take a goal id and
nothing else, so before this a goal id was sufficient to read, mutate, claim or approve any customer's
work. `store.Root.For(tenant)` closes over the tenant and every query carries it; a handler is given a
scoped store and never holds the root, so it cannot construct a query for another tenant — not because
it is careful, but because it has nothing to be careless with.

`TestATenantCannotReachAnotherTenantsData` exercises **every** method against a goal owned by somebody
else, on both implementations. A sample would prove only that the sampled methods were fixed, and the
one that was missed is the one that gets used.

A cross-tenant row is **invisible**, not forbidden: returning "forbidden" would confirm the id exists and
turn a guessable identifier into an enumeration of everybody's data.

**The loaded repository was a single global field** — one customer's question was answered about
whichever repository another customer had opened last, with real file:line references, about code they
have never seen. A cross-tenant leak wearing the shape of a cache. It is per-tenant now.

Authentication is argon2id (RFC 9106 parameters, encoded per hash so the cost can be raised later),
sessions stored as SHA-256 so a leaked dump yields nothing usable, HttpOnly + SameSite=Lax cookies, and
a **default-deny** mux: adding a route makes it protected, exposing one takes an edit to a list called
`public`. A missing user and a wrong password are indistinguishable, in message and in timing.

Bootstrap **refuses to start** when no user exists and no credentials are given. A built-in default
password is a published credential.

## !!! Not started, and deliberately so

### [x] P23 · `evalset` and `compare`
**heros never executes the customer's code** — stated in `internal/tools/boundary.go` because it was
implicit until it forced a decision. Running a customer's agent means arbitrary code, with their
credentials, against their providers, on our infrastructure, while they are absent.

That costs the product something real and it is written down rather than hidden: `evalset` GENERATES a
set and hands it over (`heros-evalset.json`, in their repo, so they own it); `compare` cannot run an
eval set twice and diff the scores, so it diffs ASSESSMENTS — an honestly weaker claim, and the report
says so in the claim itself.

The `compare` plan used to be `run-baseline` / `run-candidate` of kind `run_eval_set`. That kind is gone,
and `TestNoPlanAsksToRunTheCustomersAgent` keeps it gone — a plan encoding a capability the system has
decided not to have gets implemented by whoever reaches it first.

Proven end to end on a disposable repository: **15 cases from 3 generators, `missing:
[seed_from_real_traces]` recorded in the artefact**, parked for approval, approved, published, goal
succeeded.

Four bugs the run exposed:
- **A failed generator killed the whole set.** Each strategy is blind to what the others find, so one
  failing must degrade rather than destroy. `task.Contributes` — dependencies that must be TERMINAL, not
  SUCCESSFUL — is the distinction the graph could not express.
- **`Claim` did not return the new column**, so the gate was handed zero results while three generators'
  cases sat in the database. A column added to a table has to be added to all three queries that build
  the struct.
- **Approval did not stick.** The policy ran on every claim, so an approved task parked again forever
  and each re-approval reported success. The policy answers "does this KIND need a person"; whether one
  has answered is a different fact and was nowhere.
- **Every Tier-A goal inherited an assess-shaped criterion.** An eval-set run has no axes, so it
  published the artefact and reported FAILED. A criterion borrowed from another intent is not a weaker
  measure, it is a measure of something else.

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

### [x] P21 · Tier-C effects — the first writes to somebody's code
`author`, `prompt`, `model`, `deliver`. They run IN-TURN rather than as durable goals, because the
tiering says so and the tiering is right: a bounded change is one model call and a diff, and a queue
would add every failure mode of a distributed run to something that finishes in two seconds.

`internal/edit` is built to REFUSE. A refused edit costs one exchange; a wrong edit costs a corrupted
file discovered later and attributed to us. So every ambiguity resolves to a refusal: text that occurs
more than once, a replacement that re-indents (in Python indentation IS block structure — a statement
moved one level out is valid, parseable, and in a different scope), a path leaving the repository, a
no-op, or a file that changed between proposing and approving.

Line numbers are never identity — the anchor is the text. An unrelated edit above moves every line
below it, and an edit applied by line number rewrites whatever now sits there.

**Nothing is pushed.** Approval writes the change on a new branch and commits it, then hands back the
exact `git push` command. Pushing would need a credential with write access to somebody's repository,
held by this process, used while they are not present — the standing grant that repository connection is
deliberately out of scope for. One more step for them, one fewer credential for us.

Proven against a disposable repository: proposal → minimal diff (1 file, 1 insertion, 1 deletion) →
branch → commit; decline leaves the file byte-identical and HEAD unmoved; a second decision on the same
change is refused.

`deliver`'s QUESTION was corrected, not its tier. It read "how does an approved change reach my
repository?" — explanatory wording on an effect-bearing intent, which would eventually have been
implemented as whichever half the reader noticed.

### [x] P22 · Memory, wired
Four classes, four tables, because they differ in lifetime, in who may write, and in what a row must
carry to be trustworthy. One table forces one shape and one write path onto all four, and the concrete
failure is knowledge: an agent able to INSERT there launders its own speculation into fact, and the next
goal reads it as if somebody had established it.

- **Knowledge cannot be written, only PROMOTED**, citing episodes, and only observations or effects — a
  decision is the agent's own reasoning, and promoting one is the laundering step. The schema requires
  evidence; it is not a convention.
- **Preferences require a human author.** The Go type refuses system identities, the column refuses an
  empty one. An agent that infers "they seem to prefer aggressive refactors" has invented a mandate.
- **Compression never folds a failure or an effect**, and never deletes its source. What broke and what
  changed in the world are the two things a reader most needs from an old run.

The payoff is that **`run_history` is now a real answer** rather than a placeholder: "what happened in
that run?" is a SELECT over what a durable run wrote down, and it costs nothing. That is what persisting
everything was for.

Two bugs the wiring exposed:
- `MAX(seq)+1` inside an INSERT is **not** atomic — 16 concurrent writers produced 6 sequences and 10
  errors. Now a per-goal advisory lock, released on rollback or a dropped connection so a dying worker
  cannot wedge a goal's log.
- Run history answered about **the lexically-last goal**, not the newest — ids carry the prefix of
  whatever created them (`g-`, `live-`, `e2e-`), so a leftover test goal sorted last and the real run
  reported "no episodes" while holding nine. `store.LatestGoal` now asks the question that was meant.

### [x] P25 · Authentication and multi-tenancy
**Isolation is structural, not remembered.** Twelve of the fourteen store methods take a goal id and
nothing else, so before this a goal id was sufficient to read, mutate, claim or approve any customer's
work. `store.Root.For(tenant)` closes over the tenant and every query carries it; a handler is given a
scoped store and never holds the root, so it cannot construct a query for another tenant — not because
it is careful, but because it has nothing to be careless with.

`TestATenantCannotReachAnotherTenantsData` exercises **every** method against a goal owned by somebody
else, on both implementations. A sample would prove only that the sampled methods were fixed, and the
one that was missed is the one that gets used.

A cross-tenant row is **invisible**, not forbidden: returning "forbidden" would confirm the id exists and
turn a guessable identifier into an enumeration of everybody's data.

**The loaded repository was a single global field** — one customer's question was answered about
whichever repository another customer had opened last, with real file:line references, about code they
have never seen. A cross-tenant leak wearing the shape of a cache. It is per-tenant now.

Authentication is argon2id (RFC 9106 parameters, encoded per hash so the cost can be raised later),
sessions stored as SHA-256 so a leaked dump yields nothing usable, HttpOnly + SameSite=Lax cookies, and
a **default-deny** mux: adding a route makes it protected, exposing one takes an edit to a list called
`public`. A missing user and a wrong password are indistinguishable, in message and in timing.

Bootstrap **refuses to start** when no user exists and no credentials are given. A built-in default
password is a published credential.

## !!! Not started, and deliberately so

- **Name resolution is exact for Python, conservative for JS/TS, absent for Go.** Python uses its own
  AST. JavaScript and TypeScript use a hand-written scanner measured at **0 false positives over 3,504
  simulated edits on 1,438 real files** — the differential rule is what makes that possible. Go would
  need a compile, which needs the customer's dependencies; the verdict says "no name check for this
  language" rather than implying one ran.
- **Discovery is heuristic, not a parser.** It finds candidate evidence by pattern, so a false positive
  is cheap (the model discards it) and a false negative is expensive (the axis reports absence). Python,
  TypeScript, JavaScript and Go only. Every report states that it shows what was found, not everything
  that exists.
- **Console / HTTP surface.** No API and no UI in this tree.
