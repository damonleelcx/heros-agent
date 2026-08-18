## Why

Context is, in model terms, the richest non-model axis the platform has — and the one axis that cannot
ship a single edit. The `Dimension` enum already contains `DimContext`
([`internal/variantspec/spec.go:46`](../../../internal/variantspec/spec.go)); a node already carries a
`ContextPolicy` override that resolves to a versioned registry entry and freezes into the
`ResolvedNode`, so it auto-participates in `config_hash`
([`internal/variantspec/resolve.go:215-232`](../../../internal/variantspec/resolve.go),
[`resolved.go:56-60`](../../../internal/variantspec/resolved.go)). Seven policies are implemented behind
one `Policy` interface ([`internal/registry/context.go:29-33`](../../../internal/registry/context.go),
[`context_policies.go`](../../../internal/registry/context_policies.go)); loss telemetry
(`DropRatio` → `context_drop_ratio`) is emitted and consumed by attribution
([`internal/telemetry/context_assembly.go:78`](../../../internal/telemetry/context_assembly.go),
[`internal/attribution/signals.go:94`](../../../internal/attribution/signals.go)); and two operators,
`OpContextPolicy` and `OpRAGTune`, already *propose* context variants
([`internal/proposal/catalog.go:153-237`](../../../internal/proposal/catalog.go)).

Everything is modeled, resolvable, hashable, proposable, and scoreable — and then the codemod
**refuses**. [`internal/transform/rewrite.go:417`](../../../internal/transform/rewrite.go)
`refuseContext` returns `unsafeRewrite` for every node carrying a context override, because *"context
assembly is not a call-site argument — it is how the surrounding code builds the message list."* The
reason is real: discovery records context as a *description* (`inline_messages`,
[`internal/discovery/extract.go:239-244`](../../../internal/discovery/extract.go)), not a locator, so
materializing a policy means rewriting the code region that assembles the messages — a genuine codemod,
per language. That is step 7 of the "add an axis" checklist, the hard part, and it was deferred. The
refusal even names **P3** as the owner ([`rewrite.go:422`](../../../internal/transform/rewrite.go)), but
P3 shipped the *policies* and their host-side `Assemble` and not the call-site rewrite — so the rewrite
has no owner in code.

This change makes context a first-class **applicable** axis. It replaces the refusal with a real
call-site context materialization codemod for the Go engine, keeps the refusal as a *specified, tested*
interim behavior per language until each language's rewriter lands (a node with an un-applicable
`ContextPolicy` SHALL be **refused at transform**, never silently dropped, because a dropped override
would be scored as the base configuration under a variant's hash — a false result), models context-loss
`DropRatio` as a scored **admissibility gate**, and closes the RAG loop by verifying `OpRAGTune`
proposals on **held-out** eval sets with the retriever **pinned** per measurement run.

## What Changes

- **New capability `context-policy`.** The transform's `refuseContext`
  ([`rewrite.go:409-424`](../../../internal/transform/rewrite.go)) is replaced, for the Go engine, with
  a real **call-site context materialization**: the message-assembly region of a call site is rewritten
  so the resolved policy governs how the message list is built, deterministically given policy + params
  + conversation + seed. Every other language keeps a **specified, tested interim refusal** — a node
  carrying an un-applicable `ContextPolicy` SHALL be refused with a typed `unsafeRewrite` naming the
  node, the policy, and the reason, and SHALL NOT be a silent no-op; the refusal's owner text is
  corrected to name this phase rather than a phase that does not build the rewrite. A materialized change
  is **code** (P10 apply-mode) and appears **in the diff**; no indirection hides it. New policies
  (`hierarchical-summary`, `structured-extraction`) are added behind the existing `Policy` interface via
  `Store.AddPolicy` ([`internal/registry/store.go:41`](../../../internal/registry/store.go)) — a new
  implementation and a registry row, **never** a schema change and **never** a new `Dimension`.
  `DropRatio` becomes a scored quality signal and a **drop-tolerance admissibility gate**: a proposal
  whose resolved policy would drive a node's drop ratio past that node's tolerance is **inadmissible**,
  rejected before transform and before any eval spend. The tolerance is an **additive, omit-when-absent**
  node attribute, so a node that declares none hashes byte-identically to a pre-P16 node.
- **New capability `retrieval-tuning`.** `OpRAGTune`
  ([`catalog.go:207-237`](../../../internal/proposal/catalog.go)) proposes retrieval-parameter variants
  — top-k, chunk size, rerank on/off, embedding model — admissible **only** on a node the classifier
  labels `RetrievalRAG`. A retrieval change is **verified on a held-out eval set** disjoint from the set
  its parameters were tuned on; a win measured only on the tuning set is **not** a verified delta. A
  retrieval measurement run is **deterministic**: the retriever, its parameters, and the seed are pinned
  so re-running the same `config_hash` issues the **identical resolved retrieval request**, including
  any rerank ([`context_policies.go:259-269`](../../../internal/registry/context_policies.go)). A
  retrieval change that would push a node past its drop tolerance is **inadmissible** (the drop gate
  applied to retrieval). Pure augmentation is recorded as retrieval, not loss: `DropRatio` 0 with a
  positive `RetrievedChunks` count.
- **New capability `context-authoring` (wave 16c).** The context axis gains a **second origin**: a user
  selects a node's context policy and parameters, declares or clears its **drop tolerance**, and tunes
  retrieval directly — instead of waiting for `OpContextPolicy` or `OpRAGTune` to fire. It consumes the
  shared [`authored-change`](../p13-prompt-model-optimization/specs/authored-change/spec.md) contract
  unchanged, and adds the three rules this axis needs, all of which are about **loss** rather than
  permission. First, **the drop-tolerance gate runs at preflight**, before any eval spend: a policy whose
  measured drop ratio exceeds the node's tolerance is refused naming the node, the tolerance, and the
  measured ratio — and where that ratio has **never been measured**, preflight returns the third verdict
  **`not-yet-measurable`** naming the missing measurement. 🔴 It never returns `admissible` (which would
  assert a safety check that did not run) and never returns `refused` (which would make the platform's
  measurement coverage the user's problem) — **the gate never refuses on ignorance**. Second, the
  **language boundary is stated before the user chooses**, naming the node, the policy, and the language,
  with the same typed cause the transform raises. Third, **retrieval parameters are offered only on a
  node the classifier labels `RetrievalRAG`, and no surface, flag, or role lets a user set that label** —
  the same rule as *a user may not author the evidence*. An authored retrieval change is **pinned** when
  measured and judged on a **platform-derived held-out set disjoint from the cases shown as motivation**.
  An authored context change applies while `unverified` with **no** token or cost saving attributed, and
  `DropRatio` is displayed as **information discarded**, never as a saving.
- **Not changed here.** No new `Dimension` — `DimContext` already exists and is reused for retrieval,
  which is a `rag-retrieval` policy with params, not a `DimRetrieval`. No registry-schema change and no
  `ContextSpec` change — new policies are rows behind the `Policy` interface. No new telemetry family —
  reduction is read from the existing `eval_tokens_total`
  ([`internal/evalharness/metricnames.go:27`](../../../internal/evalharness/metricnames.go)) and loss
  from the existing `context_drop_ratio`. No context-specific scorer — the axis-agnostic harness scores
  a context variant from `config_hash` + `Trace` unchanged. No node re-ordering (that is the `Order`
  axis and `OpReorder`, owned by P5). No skills materialization (the sibling `refuseSkills` is its own
  axis and phase).

- **New capability `context-language-coverage` (wave 16d).** `spanContextMaterializers`
  ([`contextmaterialize_span.go:70`](../../../internal/transform/contextmaterialize_span.go)) carries
  Python and says, in its own comment, that TypeScript and JavaScript are *"a row plus its splitter"* —
  a scope statement that ages into a product boundary the moment the data model has no way to say *we have
  not built this yet*. 16d makes context coverage a **total** table over **(language, policy)** pairs,
  each language gap naming the **list splitter** as its missing artifact, and adds the remaining
  splitters. The invariant that governs the growth is that **retention is not per language**: which turns
  a policy retains *is* the policy, so the shared selection code decides it in every language and a
  splitter answers exactly one question — what are the written elements of this list. The drop record is
  produced by the same shared path, so a newly covered language cannot delete turns without recording
  them and `context_drop_ratio` cannot become quietly incomplete as coverage grows. And the refusal order
  this axis already had to correct once is made a requirement: a policy whose content is produced at run
  time refuses **identically in every language**, a call site that wrote **no message list** is told
  **that** — before and after its splitter lands — and only then is the language the honest answer. The
  cross-axis rules come from **P13's `language-coverage`** and are referenced, never restated.
- **New capability `context-delivery` (wave 16e).** This axis's delivery cells, under P13's
  [`change-delivery`](../p13-prompt-model-optimization/specs/change-delivery/spec.md) contract and
  [ADR-010](../../../docs/adr/ADR-010-runtime-gradual-rollout.md). "Context strategy" is one name for
  two things that land in different columns: a **retrieval parameter is a number**, so the runtime
  route refuses it only as `noRolloutBinding` with a named missing field; a **selection policy is a
  deletion** of written turns, so it is `notRuntimeResolvable`, permanently. The requirement that
  matters most is neither — it is that the **drop record survives the second route**. This axis's
  central guarantee is that a context decision records what it discarded, unskippable by construction;
  a second path by which a context decision can take effect is precisely where an unskippable
  guarantee becomes skippable, so the rule is stated as a property of the decision rather than of the
  route: whatever chooses what the model sees records what it dropped, whichever arm chose it.

## Impact

- **Affected capabilities:** `context-policy` (new), `retrieval-tuning` (new), `context-language-coverage` (new, 16d), `context-authoring` (new,
  16c), `context-delivery` (new, 16e). Consumed, not modified:
  **`authored-change` (P13 — referenced, never restated)**,
  **`change-delivery` (P13 — referenced, never restated)**,
  `entitlements` (P7), `web-console` (P9), `cli`/`ci` (P11), and:
  `variant-spec`/`resolution` (P2), `context-strategies`/registry (P3), `pattern-classifier` (P3.5),
  `eval-harness`/`scoring` (P4), `attribution-diagnosis` (P4.5), `proposals-verification` (P5.5),
  `prompt-model-studio` apply-mode (P10).
- **Affected code/systems:** `internal/transform` gains the Go context-materialization rewriter and
  the corrected/specified refusal in `rewrite.go` + `rewrite_span.go`; `internal/registry` gains new
  policy implementations behind the existing interface; `internal/variantspec` gains the additive
  drop-tolerance attribute on `NodeOverride` / `ResolvedNode`; `internal/proposal` gains the
  drop-tolerance admissibility gate and the held-out-verification wiring for `OpRAGTune`. No new store,
  no new `Dimension`, no new registry `Kind`.
- **Dependencies:** requires **P0** (`config_hash` structural inclusion), **P2** (codemod engine +
  rewriter dispatch), **P3** (`Policy` interface, registered policies, `HostServices`, `Store.AddPolicy`),
  **P3.5** (`RetrievalRAG` gate), **P4** (axis-agnostic harness + held-out eval sets), **P4.5**
  (context-overflow attribution signal), **P5.5** (verification), **P10** (apply-mode: context is code).
- **Unblocks:** context as a **deliverable** optimization axis; token-cost wins on the P5.5 verified
  ledger; and isolation of the last modeled-but-unapplied axis (`refuseSkills`), whose phase reuses this
  phase's interim-refusal pattern.
- **Breaking:** none. The drop-tolerance attribute is additive and omit-when-absent, so pre-P16 specs
  and frozen `config_hash` golden vectors reproduce unchanged. `refuseContext` becoming a materialization
  is additive to callers: a context override that previously refused now either applies (Go) or refuses
  with a better-owned message (other languages).
- **Sequencing:** **16a** (context-policy materialization on the Go engine + the interim refusal + the
  drop gate) is a complete phase on its own. **16b** (retrieval tuning + held-out verification) follows.
  **16c** (`context-authoring`) follows 16b and depends on P13's shared `authored-change` contract landing
  first; it is independently revertible, returning the axis to a single origin with no upstream change.
  **16d** (`context-language-coverage`) depends on P13's shared `language-coverage` contract landing
  first, is independent of 16c, and is likewise independently revertible — removing it returns the axis
  to its 16a cells with the refusals it already had.
