# Design — P13: Prompt & Model Optimization

Product rationale: [`../../../docs/prd/P13-prompt-model-optimization.md`](../../../docs/prd/P13-prompt-model-optimization.md).
Extends: [`../p5.5-proposals-verification/`](../p5.5-proposals-verification/) and
[`../p10-prompt-model-studio/`](../p10-prompt-model-studio/). Boundaries:
[ADR-002](../../../docs/adr/ADR-002-provider-gateway-serves-platform-callers.md) (provider gateway),
[ADR-004](../../../docs/adr/ADR-004-runtime-config-binding.md) (apply mode).

## Context

Prompt and model are the one axis the platform can both model **and** apply: `rewriteModel` and
`rewritePrompt` emit real byte edits ([`internal/transform/rewrite.go:55-56`](../../../internal/transform/rewrite.go)),
while skills and context still `refuse…` at the call site. So this axis is where a proposal becomes a
*shippable* change rather than a *modeled-but-deferred* one. The gap is not the plumbing — it is the
**catalog**: one prompt operator that handles one diagnosis code
([`catalog.go:108-149`](../../../internal/proposal/catalog.go)), and a cost-driven downgrade operator
with **no quality guardrail** ([`catalog.go:85-104`](../../../internal/proposal/catalog.go)).

Two forces shape the design, and they do not conflict — they compound:

- **The axis is applied, so a bad proposal is a real risk.** Because the codemod actually ships prompt
  and model edits, an unverified or behavior-changing rewrite is not a harmless suggestion; it is a diff
  that reaches a branch. The discipline has to be *in the proposal*, not left to a reviewer.
- **The contracts are already sufficient.** A prompt change is a new `PromptRef`; a model change is a new
  `ModelRef`/`ProviderParams` — all three already fields on `ResolvedNode`, all three already hashed by a
  purely structural `config_hash` ([`internal/confighash`](../../../internal/confighash/)). So the axis
  can be deepened by **adding catalog rows and one admissibility predicate**, touching no schema, no
  hash, no eval, no scoring.

The resolution is to spend the whole phase on operator design and one guardrail, and deliberately spend
nothing on new contracts.

## Decision 1 — Each prompt strategy is its own catalog operator, not a mode flag

Instruction hardening, few-shot curation, prompt compression, and redundancy removal are four catalog
rows, each with its own `Handles()`/`HandlesSignal()`/`AdmissiblePatterns()`/`Propose()`.

**Alternative rejected — one `improve_prompt` operator with a strategy enum.** Fewer structs. Rejected on
**L6 不可扩展** and L8: the catalog is a dispatch table precisely so that "adding an operator is adding a
row here — never editing a switch" ([`catalog.go:16`](../../../internal/proposal/catalog.go)). A strategy
enum rebuilds the switch the architecture removed and welds four different admissibility rules into one
function, so the pattern-classifier gate (each operator's `AdmissiblePatterns()`) can no longer discern
which strategy is valid where.

## Decision 2 — Every new prompt operator declines when ungrounded

An operator that cannot ground its rewrite in the cases it addresses emits **no candidate** (not an
error), extending the `ErrUngrounded` decline already in `OpPromptRewrite`
([`catalog.go:132-137`](../../../internal/proposal/catalog.go)).

**Alternative rejected — emit a best-effort rewrite and let verification filter it.** *Diagnosis proposes,
verification decides*, so "let the gate sort it out" sounds consistent. Rejected on **L1 安全** and
strategy: an ungrounded "improve this prompt" returns a longer, differently-worded prompt that addresses
nothing, and one such candidate in a sweep will occasionally tie or win **by chance** — at which point
the platform ships noise dressed as a result. Grounding is what makes verification's verdict about the
*change*, not about luck. The decline is the cheaper honesty: an operator with nothing grounded to say
says nothing.

## Decision 3 — Rewrites publish new immutable versions; compression routes through P10 impact analysis

Every rewrite publishes a **new content-addressed prompt version**; the prior stays resolvable. A rewrite
whose slot-set change would **un-apply a node** (a call-site value no longer binds) is **refused** at
resolve/transform with the slot named.

**Alternative rejected — let a compression/redundancy operator trim a prompt version directly.** It would
be simpler to mutate the body in place. Rejected on **L1/L5**: P10's contract is that edits publish a new
version ("no interface expresses mutation — the DB trigger is the last line, not the first",
[`project.md`](../../project.md)), and that *an indirection never hides a value from review — resolved
values ship in the same diff, or the transformation is rejected*. Compression is the operator most likely
to drop a live `{{slot}}`, which is precisely the un-apply case P10's impact analysis already refuses
in-editor. Routing around it produces a codemod that either does not build or binds the wrong value,
discovered after submit — the exact trap the platform exists to remove.

| Rewrite outcome | What ships |
|---|---|
| Slot set unchanged, body improved | New `PromptRef`; the diff carries the resolved body; verification decides. |
| Slot **removed** but call site still supplies it | **Refused** at resolve, slot named. No diff. |
| Slot **added** not supplied at the call site | **Refused** (un-applied node), per P10 impact analysis. No diff. |

## Decision 4 — Model downgrade is guardrailed on held-out CI overlap

A cheaper model is admissible **only** when its `task_success` confidence interval **overlaps** the
incumbent's on **held-out** cases — the same CI-overlap predicate `evalstats.Compare` uses for ties
([`internal/scoring/rank.go`](../../../internal/scoring/rank.go)) — judged on cases the operator **did
not** select.

**Alternative rejected — ship the cheaper model whenever cost drops and quality "looks fine".** It is the
operator's own goal, met fastest. Rejected on **L1 安全 (honesty)**: `OpModelDowngrade` exists to reduce
cost, so "looks fine" is a judgment made by the party that wants the cheap answer, on the cases it chose
to motivate the proposal. Held-out CI overlap is a predicate the operator cannot game: the platform must
be **unable to tell the two models apart** on data it did not pick. A cost win that silently costs quality
is a stability degradation bought with cost convenience, which the eight-level ordering forbids. This is
the one genuinely new contract in P13 — and it is deliberately assembled from an existing predicate
(overlap) over an existing metric (`task_success`) so it cannot fork the definition of quality.

## Decision 5 — The guardrail reads existing metrics; no new eval or metric

The guardrail is computed from `task_success` and its confidence interval, already produced by the P4
harness ([`metricnames.go`](../../../internal/evalharness/metricnames.go)). P13 registers **no** bespoke
metric and does **not** use the optional `registry.go:86` hook.

**Alternative rejected — a dedicated "downgrade-safety" quality metric.** It would name the concept
directly. Rejected on **L5 不可演进** and L7: a second quality metric is a second definition of "did the
task succeed," and a second place for the harness and the guardrail to drift apart. The eval stays
axis-agnostic — it consumes only `config_hash` + `Trace`
([`evaluator.go`](../../../internal/evalharness/evaluator.go)) — and a downgrade candidate is scored by
the *same* harness as any other, which is what makes its verdict comparable.

## Decision 6 — Cross-provider routing is refused at transform, as a first-class requirement

An intra-provider model swap applies via `rewriteModel`; a **cross-provider** swap at a user call site is
**refused** with a named cause and produces no diff.

**Alternative rejected — attempt the string swap and hope the SDK lines up.** "Model selection" reads as
if it should. Rejected on **L1/L2**: `rewriteModel` already refuses this — "an anthropic SDK call site
does not become an OpenAI call by changing its model string; the client, the params type, and the
response shape are all different" ([`rewrite.go:72-86`](../../../internal/transform/rewrite.go), ADR-002).
A diff that compiles and then talks to the wrong provider is the worst failure mode: silent, and in
production. The provider gateway is the call path for models the **platform** invokes; the customer's
transformed program keeps its own SDK, so a cross-provider swap is a real SDK codemod, deliberately out of
scope. P13 states the refusal as a requirement so nobody sells past it.

## Decision 7 — Parameter tuning is refused where the apply mode cannot carry it

Temperature/max-tokens tuning is modeled and hashed via `ProviderParams`, materialized in **bound** mode
([`internal/transform/boundmode.go:109`](../../../internal/transform/boundmode.go), ADR-004), and
**refused** with a named cause on an inline node with no applicable parameter rewrite — never dropped.

**Alternative rejected — emit the param override and silently drop the part inline mode cannot apply.**
Rejected on **L1/L4**: a dropped param is a config that **hashed one thing and ran another**, which the
P10 reconciliation rule turns into a failed run rather than a scored one ("a mismatched run **fails**
rather than being scored", [`project.md`](../../project.md)). Refusing loud, or carrying the param as data
in bound mode, are the only two honest outcomes; a silent drop is neither.

## Decision 8 — Effects land only in existing `ResolvedNode` fields; this change ships no `decisions.md`

Every P13 candidate's effect is a changed `PromptRef`, `ModelRef`, or `ProviderParams` — all existing
fields. No new hashed field, `Dimension`, registry `Kind`, or database table is introduced.

**Alternative rejected — add a P13 field to `ResolvedNode` to carry richer prompt/model intent.** It would
let a candidate describe *why* it changed something in the hash. Rejected on **L5**: `config_hash` is
purely structural, so a new field would change the golden bytes of **every** config that predates it and
break P0's bit-for-bit contract — the same expand-contract trap P10 navigated for bindings. Intent belongs
on the in-memory `Candidate` (its `Rationale`/`Grounding`/guardrail verdict), which is **not** hashed.
Because P13 opens **no** one-way door — no new `Dimension` (the enum at
[`spec.go:42`](../../../internal/variantspec/spec.go) stays four members), no new `Kind`, no new table —
there is **no** pre-code contract that warrants a `decisions.md`, and this change deliberately ships none.

## Interfaces sketch

```
Catalog (P13 adds rows; DefaultCatalog() at catalog.go:17):
  instructionHardenOp   Handles(under-specification)          grounded-or-silent → new PromptRef
  fewShotCurateOp       Handles(dead/misleading exemplars)    grounded-or-silent → new PromptRef
  promptCompressOp      Handles(bloat / token-reduction)      grounded-or-silent → new PromptRef
  redundancyRemoveOp    Handles(redundant instruction)        grounded-or-silent → new PromptRef
  modelDowngradeOp      Signal(cost bottleneck) + GUARDRAIL   → new ModelRef (admissible iff held-out CI overlap)
  paramTuneOp           params sweep                          → ProviderParams (bound-mode) | REFUSE (inline)

Guardrail (verification-time, in-memory, NOT hashed, NOT stored):
  split(cases, config_hash) → { motivating[], held_out[] }        // disjoint by construction (NFR5)
  admit(downgrade) ⇔ CI_overlap(task_success | held_out,          // reuses evalstats.Compare
                                cheaper, incumbent)
                    ∧ |held_out| ≥ floor                          // else inadmissible-insufficient-data
  verdict = { held_out_overlap: bool, cost_delta_sign }            // tie ⇒ cost win, quality tie (never quality win)

Refusals (typed, loud, no diff):
  cross-provider swap       → unsafeRewrite (rewrite.go:81, ADR-002)
  inline param, no rewriter → ErrUnsafeRewrite (named cause)
  slot-set change un-applies → refused at resolve, slot named (P10 impact analysis)

Hash participation: PromptRef | ModelRef | ProviderParams already in ResolvedNode → config_hash automatic.
```

## Risks

| Risk | Mitigation |
|---|---|
| A prompt rewrite ships because it *looked* better | Decision 2 — grounded-or-silent; Decision 3 — new immutable version; the P5.5 gate is the only path to a change. A test asserts a raw candidate is never applied. |
| A compression silently changes behavior | Decision 3 — a slot-set change that un-applies a node is refused at resolve with the slot named; never an in-place edit. |
| A downgrade is admitted by overfitting its motivating cases | Decision 4 — held-out CI overlap on cases disjoint from the motivating set; a test asserts disjointness. |
| "Model selection" is read as provider routing | Decision 6 — cross-provider swap refused at transform (ADR-002), stated as a requirement and a claim boundary. |
| A tuned param hashed one thing, ran another | Decision 7 — bound-mode materialization or a loud refusal; the reconciliation rule fails a mismatched run. |
| The phase grows a contract by accident | Decision 8 — effects land only in existing fields; P0 golden vectors still reproduce; no new `Dimension`/`Kind`/table/metric. |
| Insufficient held-out data yields a false tie | An explicit `inadmissible-insufficient-data` verdict below a declared floor, not a silent pass (open question 2). |
| A single-seed result decides a change | Multi-seed CIs and the tie rule — the same P4 machinery, no second implementation. |
