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

## Decision 9 — One spine, two origins: an authored change reuses the proposal pipeline entirely

A user-originated change is derived from a parent variant, resolved, hashed, gated, transformed and
(optionally) scored by **the same components** that process an operator candidate. `Origin`
(`operator` | `user`) plus actor and tenant ride on the candidate/transform/delivery records. There is no
authoring-only resolve, transform, or gate.

**Alternative rejected — a direct "edit and apply" path that writes the node override straight to a
diff.** It is dramatically less code: skip derivation, skip the gate, emit the rewrite. Rejected on
**L2 stability + L5 evolvability**, with L8 explicitly refused as a justification. Every safety property this
platform has is a property of that one pipeline — un-apply refusal at resolve, cross-provider refusal at
transform, `GateReorder` before codemod, drop-tolerance before eval spend. A second path does not
*inherit* those; it **re-implements or omits** them, and the omissions are invisible until a user hits
one. The specific failure is concrete and known: the same class of bug as a silently-dropped override,
where a config hashes one thing and the emitted code does another. Two origins on one spine costs a
`Origin` field and a preflight entry point; two spines costs every future gate being added twice, forever.
That is the definition of an unevolvable design, and L3 (the lowest rank can never outrank anything above it) forbids buying it with
implementation convenience.

**Corollary — `Origin` is recorded, never hashed.** `config_hash` is purely structural, so a user-authored
configuration and a byte-identical operator-proposed one hash the same and are scored once. Putting origin
in the hash would fork identity by authorship, break P0's golden vectors for every pre-existing config,
and make "did we already measure this?" unanswerable.

## Decision 10 — Refusals are origin-blind, and there is no override flag

Every refusal — cross-provider swap, un-apply, un-carryable inline param, unsupported-language
materializer, typed-contract violation, drop-tolerance exceedance — is raised for a user under exactly the
same conditions and with the same typed cause as for an operator. No plan, role, entitlement, or request
parameter suppresses one.

**Alternative rejected — a "force" / "I know what I'm doing" override for authenticated owners.** It is
the single most requested affordance in tools like this, and it feels respectful of the expert user.
Rejected on **L1 safety + L2 stability**. The refusals are not paternalism about *taste*; each one exists
because the emitted artifact would be **wrong in a way the user cannot see at the moment of choosing**. A
cross-provider swap does not become correct because a human asked for it — the SDK client, params type,
and response shape still differ, and the diff still compiles and then calls the wrong provider in
production. A dropped inline param still means the run's `config_hash` describes a configuration that did
not execute. An override converts a loud, typed, pre-submit refusal into a silent production defect, and
the person who authored it is the one least able to detect it. The honest affordance is not a bypass but a
**better refusal**: name the cause, name the node, name the field, and — where a legitimate path exists —
name it (switch the node to `bound` mode; change the provider at the gateway, not the call site).

## Decision 11 — Refusal moves left: preflight is the authoring path's one genuinely new mechanism

A draft is evaluated *before* submission and returns `admissible` / `refused(named cause, offending node
or field)` / `not-yet-measurable`. Preflight publishes nothing, writes no diff, and spends no eval budget.

**Alternative rejected — reuse the existing refusal points and let the user discover the outcome after
submitting.** No new endpoint, no new state. Rejected on **L3 user-facing complexity**, which outranks the
implementation saving by five levels. An operator is a program: discovering a refusal after generating a
candidate costs it nothing. A human is not: they will have written a prompt, chosen a model, and formed an
intention before the platform says "this node's language has no materializer." Worse, the two most common
authoring refusals are *structural properties of the node* — its apply mode, its provider, its language —
which are knowable **before the user types anything**. Withholding a fact the system already knows until
after the user has done the work is the exact UX failure the interaction-simplicity rule names. Preflight
also enforces the third verdict: where an admissibility input has never been measured, it returns
`not-yet-measurable` rather than guessing — a gate that **never refuses on ignorance**, matching the
posture P16's drop-tolerance gate already ships.

## Decision 12 — Authoring may apply without a verdict, but may never produce a claim

An authored change may be applied and produce a diff with no verification run. It is stamped `unverified`,
is excluded from the verified-delta ledger and every aggregate improvement or savings figure, and never
auto-merges. Verification, when requested, is judged by the unchanged harness — including the held-out
guardrail — and the user does not choose the cases, the split, or the seeds.

**Alternative rejected (a) — block the apply until verification passes.** The strictest reading of
*verification decides*, and it keeps one rule for everything. Rejected on **L3**, and on a boundary error:
verification decides what the platform may **claim**, not what the customer may **do with their own
repository**. Blocking would also break P11's offline-first contract outright — a CLI that cannot apply an
edit without a network round trip and an eval budget is not offline-first — and would make the cheapest,
most obvious edits (fix a typo in a prompt, pin a model the team already standardized on) the most
expensive operations in the product.

**Alternative rejected (b) — let an authored change carry a verdict of "presumed fine" or inherit the
parent's.** Convenient for dashboards, and it keeps the ledger dense. Rejected on **L1 safety (honesty)** —
the same rule that forbids an ungrounded rewrite from being ranked. A configuration the harness never ran
has no verdict, and inventing one destroys the only property that makes the ledger worth reading. So the
label is structural, not cosmetic: `unverified` is a state the ledger filters on, not a badge in a UI that
a future refactor can drop.

**The line, stated once:** *a user may author the change; a user may not author the evidence.* Case
selection, held-out splits, and seeds stay platform-derived, because an authored downgrade verified on
cases its author picked is precisely the overfitting Decision 4 exists to prevent — and a human has the
same incentive as `OpModelDowngrade` and better tools for satisfying it.

## Decision 13 — Coverage is a total table over cells, and the engine points at a binding site

**What was rejected:** (a) closing the language gaps by adding a Java rewriter, a Kotlin rewriter and a
Rust rewriter as three pieces of work; (b) continuing to list only the cells that work, letting absence
mean "no".

Two failures hide inside "the platform is Go-only", and neither is the one the phrase describes.

**The first is that absence is not a value.** Today coverage lives in five tables across three packages —
`argumentForms`, `toolValueForms`, `spanContextMaterializers`, `contextForms`, `statementResolvers` — and
each states what *is* there. A reader wanting to know what happens to Rust finds nothing, and *nothing*
renders identically to "not applicable" on every surface that consumes it. So the one state the platform
most needs to express — **we have not built this yet, and here is the artifact that would close it** — is
the state the data model cannot hold. Making coverage a **total function** over (axis × registered
language × form) is therefore not documentation work; it is the difference between a gap that has an owner
and a gap that has a shrug. And it forces a second, harder honesty: three genuinely different things are
being confused. *No language can carry this change* (a summarized context does not exist in source), *this
call site cannot carry it* (arguments unpacked from a mapping; a tool list assembled at run time), and
*this language has no materializer yet* are answered by the platform's designer, the customer's engineer,
and the platform's backlog respectively. Collapsing them sends the reader to the wrong one — and the most
common real refusal, on the most common real repository, is the middle one. Hence the ordering rule:
**the change, then the row, then the source, then the language** — the language question asked last,
which is exactly the correction P16 had to make after shipping the context refusal the other way round.

**The second is that three languages do not need three rewriters.** They need one concept the engine does
not have. `rewrite.go`'s founding rule is *replace an expression the call site already wrote*, and the
locator that finds it assumes the expression is a **named argument**. Kotlin disproves the "unsupported
language" reading on its own: Kotlin has named arguments, `kotlinAnalyzer` produces real spans and a real
insert point, and the rewriter would work — but every Kotlin row declares no `arg_map`, because
langchain4j, Spring AI and Bedrock bind the model on a **builder** at construction, so `generate(...)`
has no model argument to point at. Java and Rust arrive at the same place from the other side: no named
argument form at all, and a model bound in a builder chain or a request struct assembled before the call.

So the generalization is: the engine locates a **binding site**, of which a named argument is one form, a
**builder-chain call** is a second, and a **request-value field** is a third. Registry rows declare which
form their SDK binds at, additively, so an existing row is untouched and every hash it participates in is
unchanged. After that, Kotlin is a **registry row**, Java and Rust are a **frontend extraction plus rows**,
and the next SDK that binds somewhere new costs a row rather than a project — the L6 extensibility test
(*do not exhaust cases with if/else where a configuration table belongs*), applied to a missing concept
rather than to a missing branch.

**What this decision explicitly does not license.** Coverage is never reached by weakening a gate: no
guessed SDK spelling, no skipped reparse assertion, no inferred binding site. A row is admitted on
**executable evidence** — the language's reparse assertion, plus the build gate wherever the change
constructs source — because a row is a claim that this spelling is well-formed, and the only thing that
can prove it is a run. Trading L1/L2 for reach would produce exactly the failure the skill materializer's
own comment names: *a change that compiles and then degrades quality invisibly*. Reach is worth nothing if
what arrives is wrong.

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

Authoring (13c — a second ORIGIN on the pipeline above, never a second pipeline):

  Draft {                                   // in-memory / draft store; NOT a variant until submitted
    ParentVariantID   : id                  // immutable parent; never mutated (AC-7)
    Edits             : node → {ModelRef?, ProviderParams?, PromptRef?}
    Actor, Tenant     : identity            // recorded, never hashed
    ForkedFromProposal: candidate_id?       // set when a proposal was edited (operator NOT credited)
    ConcurrencyToken  : opaque              // stale submit ⇒ named conflict, never a lost update
  }

  preflight(Draft) → admissible
                   | refused{cause, node, field}      // same typed causes as the operator path
                   | not-yet-measurable{missing}      // never refuse on ignorance
      = resolve(derive(parent, edits)) → gates → materializability probe
        …publishes nothing, writes no diff, spends no eval budget

  submit(Draft) → Variant{ParentVariantID, Origin: user, Actor, Tenant}
                → the SAME resolve → config_hash → transform → (optional) eval → verdict

  verification_state ∈ { unverified, verified{verdict} }
      unverified ⇒ ∉ verified-delta ledger, ∉ any aggregate figure, ∄ auto-merge
      the user requests a run; the PLATFORM derives cases, held-out split, seeds

  revert(authored) → new Variant derived from recorded parent
                     ⇒ config_hash == pre-edit config_hash, byte-identical   // never in-place restore

  Record (append-only, P12 delivery-record posture):
    {actor, tenant, ts, parent, axis, config_hash, diff_ref, origin, forked_from?}
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
| Authoring grows a second apply path whose gates drift from the first | Decision 9 — one spine, two origins; a structural test asserts a single transform entry point and that no authored change reaches a diff bypassing a gate an operator must pass. |
| An "expert override" is added later and silently disables a refusal | Decision 10 — refusals are origin-blind by construction and a test asserts no flag, role, plan, or parameter suppresses one; the refusal set is enumerated, not sampled. |
| An unverified authored change is counted as an improvement | Decision 12 — `unverified` is a ledger-filtering state, not a UI badge; a test asserts unverified changes contribute zero to every aggregate figure. |
| A user verifies their own change on cases they picked | Decision 12 — case selection, held-out split and seeds are platform-derived; the disjointness assertion from Decision 4 covers the authored path too. |
| A lost update silently discards a colleague's edit | Decision 9 — drafts carry a concurrency token against an immutable parent; a stale submit is a **named conflict**, and two edits from one parent yield two variants. |
| An operator's win-rate is inflated by humans fixing its proposals | A forked proposal records its origin and the operator is **not** credited with the authored outcome. |
| Authoring leaks prompt text or source across the boundary | The P11 allowlist is unchanged and covers preflight; a test asserts the preflight payload carries no prompt text, source, diff, env value, or credential. |
| A language is absent from a coverage table and reads as "not applicable" | Decision 13 — coverage is a **total** function over (axis × registered language × form); a generated test over the registered language set fails on any missing cell. |
| A user is told their language is unsupported when their own call site is the problem | Decision 13 — three typed causes with stable identifiers, reported most-specific-first with the language asked **last**; the ordering test goes red when reversed. |
| Coverage is extended by guessing an SDK spelling | Decision 13 — a row is admitted only on executable evidence (reparse, plus the build gate where source is constructed), and no gate is relaxed to reach a language. |
| Generalizing the locator changes an existing diff or hash | Decision 13 — the binding-form declaration is additive; previously materializable call sites emit byte-identical changes and P0 golden vectors reproduce. |
| Two languages implement the same override differently | Decision 13 — semantic parity asserted over a shared fixture; a divergence is a defect, never a per-language behavior. |
