## Why

Prompt and model are the **only** dimensions the platform can both model *and* apply end to end today.
The codemod actually emits edits for them — `rewriteModel` and `rewritePrompt`
([`internal/transform/rewrite.go:55-56`](../../../internal/transform/rewrite.go)) — while `refuseSkills`
([`rewrite.go:388`](../../../internal/transform/rewrite.go)) and `refuseContext`
([`rewrite.go:417`](../../../internal/transform/rewrite.go)) still return `unsafeRewrite`. This is the
axis whose improvements are *shippable*, not merely *modelable*.

But it is also the axis that is thinnest in **what it proposes**. On prompt, the catalog carries a single
operator — `OpPromptRewrite` — and it handles exactly one diagnosis code, `CausePromptFormatDrift`
([`internal/proposal/catalog.go:108-149`](../../../internal/proposal/catalog.go)). A prompt that is
correct-but-bloated, correct-but-under-specified, or carrying dead few-shot exemplars produces **no
candidate**, because none of those is format drift — the transform can apply a better prompt, the engine
just never proposes one. On model, `OpModelDowngrade` fires on a `SignalCostBottleneck` and proposes
cheaper models ([`catalog.go:85-104`](../../../internal/proposal/catalog.go)) with **no quality
guardrail**: cheaper is the operator's goal, which makes "ship the cheaper model" the tempting failure,
and today nothing stops a cost win from quietly costing quality.

And on both halves the axis has exactly **one origin**: a catalog operator. There is no way for the
engineer who owns the workflow to say "use this model here" or "bind this prompt version" and have the
platform carry it — the only user-authoring surface that exists is P10's studio, which is prompt-only and
deliberately *not an evaluator*. That leaves two real gaps. An intent the catalog has no operator for is
inexpressible, and a proposal that is *nearly* right can only be accepted or rejected, never corrected.
The answer is **not** a second pipeline — it is a second **origin** on the same one.

This change **deepens the axis that ships** and changes nothing else. It builds directly on **P5.5**
(the operator/catalog/gain/verification architecture) and **P10** (immutable content-addressed prompt
versioning, bindings, apply-mode, impact analysis), and it obeys the platform's founding law:
*diagnosis proposes, verification decides*. Every new operator only *nominates* a candidate; the
multi-seed P4 harness and the P5.5 gate are the only things that can turn it into a claim. Because a
prompt/model change already lands in `ResolvedNode` fields (`PromptRef`, `ModelRef`, `ProviderParams`)
that already participate in the structural `config_hash`, the eval harness stays axis-agnostic and **no
eval, scoring, transform, registry, or schema change is required.**

## What Changes

- **New capability `prompt-rewrite`.** Deeper prompt operators — **instruction hardening**, **few-shot
  exemplar curation**, **prompt compression / token-reduction**, and **redundancy removal** — each added
  as a **catalog row** (its own `Handles()`/`HandlesSignal()`/`AdmissiblePatterns()`), never as a branch
  inside an existing operator. Each **declines when ungrounded** (emits no candidate, extending the
  existing `ErrUngrounded` discipline at [`catalog.go:132-137`](../../../internal/proposal/catalog.go)),
  so an "improve this prompt" that addresses nothing is never proposed. Each rewrite **publishes a new
  content-addressed prompt version** through P10's registry — the prior stays resolvable, nothing is
  edited in place — and a rewrite whose slot-set change would **un-apply a node** (a call-site value no
  longer binds) is **refused** at resolve/transform with the slot named, per P10 impact analysis:
  *resolved values ship in the same diff, or the transform is rejected*. Each candidate's only hashed
  effect is a changed `PromptRef`, and each is judged on the **same** `task_success` and cost metrics —
  a shorter prompt that does not hold quality within CI is **not** a win.
- **New capability `model-selection`.** A **model-downgrade quality guardrail**: a cheaper model is
  admissible **only** when its `task_success` confidence interval **overlaps** the incumbent's on
  **held-out** cases (the same CI-overlap predicate the tie rule already computes,
  [`internal/scoring/rank.go`](../../../internal/scoring/rank.go)), judged on cases the operator **did
  not select**, so a downgrade cannot be admitted by overfitting the cases that motivated it. An
  admitted downgrade is an **equal-quality-cheaper tie** — a shippable outcome reported as a cost win and
  a quality tie, never a quality win. **Parameter tuning** (temperature/max-tokens) is modeled and hashed
  via `ProviderParams`, materialized in **bound** mode ([ADR-004](../../../docs/adr/ADR-004-runtime-config-binding.md))
  and **refused** with a named cause on an inline node it cannot carry — never dropped. **Provider
  routing** states its boundary as a requirement: intra-provider swaps apply via `rewriteModel`, and a
  **cross-provider swap at a user call site is refused at transform** ([`rewrite.go:81-86`](../../../internal/transform/rewrite.go),
  [ADR-002](../../../docs/adr/ADR-002-provider-gateway-serves-platform-callers.md)) — modeled and
  hashable, not materialized. Model upgrade / extended-thinking keep their existing admissibility and
  ship only on a ranked win.
- **New capability `authored-change` (the cross-axis contract, defined once here).** A change may now be
  originated by a **user**, not only by an operator — and it travels the **same spine**: derived with
  `ParentVariantID`, resolved, hashed, gated, transformed, and (on request) scored by the same
  components. There is **no** authoring-only apply path. `Origin` (`operator` | `user`) plus actor and
  tenant are recorded on the candidate/transform/delivery records and are **never** hashed, so an
  authored configuration and an identical proposed one share one `config_hash`. **Every refusal that
  binds an operator binds a user identically** — cross-provider swap, un-apply, un-carryable inline
  param, unsupported-language materializer, typed-contract violation — with **no override flag on any
  plan or role**. Because a human is waiting, refusals are computed at **preflight**: a draft returns
  `admissible` / `refused(named cause, offending node)` / `not-yet-measurable` **before** submission,
  spending no eval budget and publishing nothing. An authored change **may be applied without a
  verdict** — it is the customer's repository — but it is stamped **`unverified`**, never enters the
  verified-delta ledger, never counts toward an aggregate improvement or savings figure, and **never
  auto-merges**. Editing a proposal forks it into an authored change that records the originating
  proposal and **does not credit the operator**. Drafts never mutate their parent (stale submission is a
  **named conflict**, not a lost update); every authored change has a **reversal that reproduces the
  parent `config_hash` byte-identically**; authoring is entitlement-gated and recorded **append-only**;
  the **CLI authors offline** with identical gates and identical cause text; and the path adds **no new
  egress**. The line the capability exists to hold: **a user may author the change; a user may not author
  the evidence** — case selection, held-out splits, and seeds stay platform-derived.
- **New capability `prompt-model-authoring`.** The axis-specific surface: a user sets a node's
  **`ModelRef`** (intra-provider swaps apply through the existing `rewriteModel`; **cross-provider models
  are not offered**, and are refused by name if submitted through any surface), its **`ProviderParams`**
  (materialized in `bound` mode, **refused at preflight** naming the node on an inline node with no
  applicable rewriter — never dropped), and its **`PromptRef`** (select a published version or publish a
  new **content-addressed** one; a slot-set change that would **un-apply** the node is refused at
  preflight **with the slot named**). An authored **downgrade** is applicable and `unverified`; if the
  user requests verification, the **same held-out CI-overlap guardrail** decides whether it is reported
  as an equal-quality-cheaper tie or a **quality regression** — authoring changes who picks the
  candidate, never who judges it. The studio gains model/param authoring and **still gains no evaluator**;
  no existing authoring capability is removed.
- **New capability `language-coverage` (the second cross-axis contract, defined once here).** Discovery
  registers seven languages and [`internal/transform/engines.go`](../../../internal/transform/engines.go)
  has a row for all seven — but what each can *apply* differs per axis (model/prompt in four languages,
  context and wiring in two, skill binding and tool pruning in one), and that spread lives in five tables
  across three packages with some cells having **no row at all**. An absent row renders on every surface as
  "not applicable" when it means "not built," so this capability makes coverage a **total function** over
  (axis × registered language × the form the axis binds against): every cell present, every gap carrying a
  named cause. A refusal must say **which of three different things** is missing —
  `not-expressible-at-a-call-site` (the value does not exist until run time), `call-site-cannot-carry-it`
  (unpacked arguments, a run-time-assembled list, no row locator, a binding the frontend never recorded),
  or `no-materializer-for-this-language` — distinguishable by a **stable identifier**, and the **most
  specific true one** is reported, with the **language asked last**. That ordering is the fix P16 already
  had to make on the context axis after shipping it the other way round. Materialization in **every**
  registered language is each axis's target state; a gap names the artifact that would close it (a form
  row, a list splitter, a statement resolver, a registry row, a frontend field), and a cell is never
  reached by guessing an SDK spelling, skipping a reparse assertion, or inferring a binding site nobody
  wrote. One coverage source is read by transform, preflight, console, CLI and every document, asserted in
  **both** directions; a row is admitted only on **executable evidence**; the same override means the same
  thing in every language; the CLI carries a **versioned** copy and names its version in a refusal; a
  polyglot workflow refuses by name; and a coverage gap is **identical on every plan** — never a tier, a
  flag, or a setting.
- **New capability `prompt-model-language-coverage`.** This axis's own cells. Model and prompt materialize
  in Go, Python, TypeScript and JavaScript; the other three registered languages refuse for reasons that
  look alike and are not. Kotlin *has* named arguments and a working span analyzer, and still refuses,
  because every Kotlin row declares no argument locator — those SDKs bind the model on a **builder** at
  construction. Java and Rust have no named-argument form at all and bind in a builder chain or a request
  struct. Both sentences describe the same missing concept from two directions, so the work is **not three
  rewriters**: the engine generalizes what it points at, from *the argument a call site wrote* to the
  **binding site the program wrote** — a named argument, a builder-chain call, or a request-value field.
  Registry rows gain an **additive** declaration of which form their SDK binds at and the locator that
  finds it; a row whose SDK binds nowhere locatable refuses **naming the SDK and its binding style**, never
  the language. After that Kotlin is a registry row and Java/Rust are a frontend extraction plus rows. The
  authoring surface states the boundary — language and binding form — **before** the picker, from the
  shared source, and every previously materializable call site's emitted change stays **byte-identical**
  with `config_hash` unchanged.
- **Not changed here.** No new `Dimension` (`internal/variantspec/spec.go:42` stays a four-member enum),
  no new registry `Kind`, no new database table, no new eval oracle, and no new scoring metric — the
  guardrail reads the existing `task_success` and its CI. The skills/context codemod refusals are **not**
  addressed; the P10 studio gains no score, rank, winner, or promotion path (*the studio is not an
  evaluator*); and cost stays `eval_cost_usd` and plan **names** only, never a price.
- **New capability `change-delivery` (the third cross-axis contract, defined once here).** Delivery
  today is one chain — a rewriter produces a diff, [P12](../p12-forge-delivery/) opens a pull request,
  a human merges — and it has no statement at all for what happens when the **first link refuses**,
  which the coverage tables say is the common case. A verified change on an axis with no materializer
  produces silence that is indistinguishable from a proposal nobody got to. So delivery becomes a
  **total function** over (axis × change × route): every cell names a route that delivers it or a
  typed cause that refused it, and a change no route can deliver is a **reported state**. On that
  footing a second route is added under [ADR-010](../../../docs/adr/ADR-010-runtime-gradual-rollout.md)
  — a **gradual rollout** that is a two-armed binding document resolved **inside the customer's own
  process** ([ADR-004](../../../docs/adr/ADR-004-runtime-config-binding.md)'s accessor, not our
  gateway): deterministic offline arm assignment, every invocation attributed to its **arm's own**
  `config_hash` (so ADR-002's comparability objection is answered rather than dodged), a bounded
  expiry that serves the parent, local guard-tripping revert with **human** resume, inert during
  measurement runs, and **never** a way to make a change permanent — that still costs a codemod, a
  pull request, and a merge. Referenced by [P14](../p14-skills-tools-optimization/),
  [P15](../p15-workflow-wiring-optimization/), [P16](../p16-context-strategy-optimization/),
  [P17](../p17-memory-strategy-optimization/) and [P18](../p18-harness-strategy-optimization/) rather
  than restated.
- **New capability `prompt-model-delivery`.** This axis's own cells — and it is the **only** axis whose
  runtime route is live, because model id, inference params and prompt version are precisely the fields
  [ADR-009](../../../docs/adr/ADR-009-binding-document-format.md) already fixed in the binding document.
  The cell that must not blur: a **provider-crossing** model change is `notRuntimeResolvable`, because
  swapping the provider rewrites the SDK call itself (ADR-002), and a user reading "model is
  rollout-eligible" must not conclude they can canary one vendor against another.

## Impact

- **Affected capabilities:** `prompt-rewrite` (new), `model-selection` (new), `authored-change` (new,
  cross-axis — referenced by [P14](../p14-skills-tools-optimization/), [P15](../p15-workflow-wiring-optimization/)
  and [P16](../p16-context-strategy-optimization/) rather than restated), `prompt-model-authoring` (new),
  `language-coverage` (new, cross-axis — referenced by the same three rather than restated),
  `prompt-model-language-coverage` (new), `change-delivery` (new, cross-axis — referenced by
  [P14](../p14-skills-tools-optimization/), [P15](../p15-workflow-wiring-optimization/),
  [P16](../p16-context-strategy-optimization/), [P17](../p17-memory-strategy-optimization/) and
  [P18](../p18-harness-strategy-optimization/) rather than restated), `prompt-model-delivery` (new).
  Consumed, not modified: `proposal-engine`/`verification` (P5.5),
  `prompt-authoring`/`runtime-config-binding` (P10), `eval-harness`/`scoring` (P4),
  `pattern-classifier` (P3.5), `diagnosis` (P4.5), `workflow-ir`/`config-hash` (P0),
  `entitlements` (P7), `cli`/`ci` (P11), `forge-delivery` (P12), `web-console` (P9).
- **Affected code/systems:** `internal/proposal` gains catalog rows in `DefaultCatalog()`
  ([`catalog.go:17`](../../../internal/proposal/catalog.go)) and priors in
  [`gain.go`](../../../internal/proposal/gain.go); the P5.5 verification gate gains a held-out guardrail
  predicate over metrics it already reads. **No** change to `internal/transform`, `internal/confighash`,
  `internal/registry`, `internal/evalharness`, or `internal/scoring` contracts. No new store.
  For authoring: a new `internal/authoring` package (draft lifecycle, `Origin`, preflight, conflict
  detection, reversal derivation) sitting **beside** the proposal engine and feeding the *same* resolve /
  gate / transform entry points; `internal/api` gains preflight + submit + revert routes; the P9 console
  gains authoring controls on its existing per-axis pages; the P11 CLI gains offline authoring verbs.
  The authored-change record is **append-only**, following the P12 delivery-record posture — a variant is
  produced once, an authored change has a lifecycle.
- **Dependencies:** requires **P0** (`config_hash` + `ResolvedNode` fields), **P2** (the model/prompt
  codemod), **P3.5** (pattern classifier), **P4** (harness + CI/tie), **P4.5** (taxonomy + signals),
  **P5.5** (operator/catalog/verification), **P10** (immutable versioning + impact analysis + ADR-004).
- **Unblocks:** **[P6](../p6-autonomous-optimizer/)** — a deeper, guardrail-tagged candidate stream on
  the one axis the loop can apply; and the **M16 Optimization Axis Expansion** program's template for
  deepening an *applied* axis (operators + admissibility) rather than expanding a *refused* one.
- **Breaking:** none. Every existing operator, its admissibility, and every hashed contract are
  preserved; the change is additive catalog rows plus one admissibility predicate, plus a second
  **origin** on the existing pipeline. `Origin` is not hashed, so every pre-existing `config_hash` and
  golden vector reproduces byte-identically, and a deployment with authoring disabled behaves exactly as
  before.
- **Sequencing:** **13a** (prompt-rewrite operators) is complete on its own. **13b** (model-selection
  guardrail + params + routing boundary) follows and is what P6's loop consumes. **13c**
  (`authored-change` + `prompt-model-authoring`) follows 13b and is independently revertible: removing it
  returns the axis to a single origin with no upstream change. P14/P15/P16's authoring waves depend on
  13c's shared contract landing first. **13d** (`language-coverage` + `prompt-model-language-coverage`)
  is independent of 13c and may run beside it; it is likewise independently revertible, returning the axis
  to its pre-13d cells with the refusals it already had. P14/P15/P16's coverage waves depend on 13d's
  shared contract landing first, and 13d's *totality* check is what forces each of them to publish an
  entry rather than an absence.
