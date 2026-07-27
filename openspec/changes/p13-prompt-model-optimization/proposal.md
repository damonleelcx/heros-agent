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
- **Not changed here.** No new `Dimension` (`internal/variantspec/spec.go:42` stays a four-member enum),
  no new registry `Kind`, no new database table, no new eval oracle, and no new scoring metric — the
  guardrail reads the existing `task_success` and its CI. The skills/context codemod refusals are **not**
  addressed; the P10 studio gains no score, rank, winner, or promotion path (*the studio is not an
  evaluator*); and cost stays `eval_cost_usd` and plan **names** only, never a price.

## Impact

- **Affected capabilities:** `prompt-rewrite` (new), `model-selection` (new). Consumed, not modified:
  `proposal-engine`/`verification` (P5.5), `prompt-authoring`/`runtime-config-binding` (P10),
  `eval-harness`/`scoring` (P4), `pattern-classifier` (P3.5), `diagnosis` (P4.5), `workflow-ir`/`config-hash` (P0).
- **Affected code/systems:** `internal/proposal` gains catalog rows in `DefaultCatalog()`
  ([`catalog.go:17`](../../../internal/proposal/catalog.go)) and priors in
  [`gain.go`](../../../internal/proposal/gain.go); the P5.5 verification gate gains a held-out guardrail
  predicate over metrics it already reads. **No** change to `internal/transform`, `internal/confighash`,
  `internal/registry`, `internal/evalharness`, or `internal/scoring` contracts. No new store.
- **Dependencies:** requires **P0** (`config_hash` + `ResolvedNode` fields), **P2** (the model/prompt
  codemod), **P3.5** (pattern classifier), **P4** (harness + CI/tie), **P4.5** (taxonomy + signals),
  **P5.5** (operator/catalog/verification), **P10** (immutable versioning + impact analysis + ADR-004).
- **Unblocks:** **[P6](../p6-autonomous-optimizer/)** — a deeper, guardrail-tagged candidate stream on
  the one axis the loop can apply; and the **M16 Optimization Axis Expansion** program's template for
  deepening an *applied* axis (operators + admissibility) rather than expanding a *refused* one.
- **Breaking:** none. Every existing operator, its admissibility, and every hashed contract are
  preserved; the change is additive catalog rows plus one admissibility predicate.
- **Sequencing:** **13a** (prompt-rewrite operators) is complete on its own. **13b** (model-selection
  guardrail + params + routing boundary) follows and is what P6's loop consumes.
