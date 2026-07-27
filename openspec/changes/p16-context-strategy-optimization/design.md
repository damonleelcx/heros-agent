# Design — P16: Context Strategy Optimization

Product rationale: [`../../../docs/prd/P16-context-strategy-optimization.md`](../../../docs/prd/P16-context-strategy-optimization.md).
Upstream: [`../p3-context-skills-sandbox/`](../p3-context-skills-sandbox/) (the policies and the
interface), [`../p4-eval-harness/`](../p4-eval-harness/) (the axis-agnostic harness),
[`../p5.5-proposals-verification/`](../p5.5-proposals-verification/) (verification), and
[`../p10-prompt-model-studio/`](../p10-prompt-model-studio/) (apply-mode: context is code).

## Context

The context axis is the platform's honesty pattern at its most extreme. Left of the transform, it is the
most complete axis in the codebase:

- `DimContext` is a member of the closed dimension enum
  ([`internal/variantspec/spec.go:46`](../../../internal/variantspec/spec.go)).
- A node's `ContextPolicy` override resolves to a versioned registry entry and freezes into
  `ResolvedNode.ContextPolicy` + `ContextParams`
  ([`internal/variantspec/resolve.go:215-232`](../../../internal/variantspec/resolve.go),
  [`resolved.go:56-60`](../../../internal/variantspec/resolved.go)), so it participates in `config_hash`
  with no extra work — `config_hash` is purely structural.
- Seven policies are implemented behind one `Policy` interface: `full` and `full-history` (identity),
  `sliding-window`, `summarization`, `rag-retrieval`, `semantic-compaction`
  ([`internal/registry/context.go:66-74`](../../../internal/registry/context.go),
  [`context_policies.go`](../../../internal/registry/context_policies.go)).
- Loss telemetry is emitted (`AssembledContext.DropRatio` → `context_drop_ratio`,
  [`internal/telemetry/context_assembly.go:78`](../../../internal/telemetry/context_assembly.go)) and
  already consumed by attribution as a context-overflow signal
  ([`internal/attribution/signals.go:94`](../../../internal/attribution/signals.go)).
- Two operators propose context variants: `OpContextPolicy` and `OpRAGTune`
  ([`internal/proposal/catalog.go:153-237`](../../../internal/proposal/catalog.go),
  [`operator.go:39-41`](../../../internal/proposal/operator.go)), with priors in
  [`gain.go:13,17`](../../../internal/proposal/gain.go).

Then the codemod refuses. [`internal/transform/rewrite.go:409-424`](../../../internal/transform/rewrite.go)
`rewriteContext` calls `refuseContext`, which returns `unsafeRewrite`. The reason is not laziness:
context assembly is not an argument to swap, it is *how the surrounding code builds the message list*,
and the registry rows carry no context locator. Discovery records it as a description
(`inline_messages` / `inline_messages_with_tools`,
[`internal/discovery/extract.go:239-244`](../../../internal/discovery/extract.go)). So materializing a
policy is a **region** rewrite — genuine code generation, per language — which is why it was deferred to
step 7 of the "add an axis" checklist. The refusal's text even names P3 as the owner
([`rewrite.go:422`](../../../internal/transform/rewrite.go)); P3 shipped the policies and their
host-side `Assemble` but not the call-site rewrite. **The rewrite has no owner in code. P16 is it.**

The design problem is therefore narrow and deep: build one rewriter (step 7), and add the discipline
that makes shipping it safe — an interim refusal that is never a silent drop, a drop-tolerance gate that
keeps lossy variants off the board, and held-out verification that keeps overfit retrieval off the
ledger.

## Decision 1 — The interim refusal is a specified, tested behavior, per language

The Go engine materializes context; every other language keeps `refuseContext` in place — but as a
**specified requirement with its own passing test**, not an accident of partial coverage. A node
carrying an un-applicable `ContextPolicy` SHALL be refused at transform, naming the node, the policy,
and the reason, and SHALL NOT be a silent no-op.

**Alternative rejected — ship the Go rewriter and let other languages silently no-op the override.**
Less code, and "it just doesn't do anything on that language yet" feels harmless. Rejected on **L1 安全
+ L2 稳定**: a silently-dropped context override is resolved, hashed, and scored as the base
configuration *under the variant's hash* — a **false result**, the single worst thing an eval platform
can produce, because the number is wrong and looks right. A loud refusal is a correct, safe answer
("not applicable here yet"); a silent drop is an incorrect one. The refusal is a feature.

This is also why the interim refusal gets a test that asserts it is **loud**: a test that a differing
policy on an unbuilt language produces a typed error *and* does not transform as its base config. If
that test cannot be made to fail, the guarantee is decoration.

## Decision 2 — Context is materialized as code, inline, in the diff

A materialized policy rewrites the message-assembly region of the call site, and the resolved assembly
appears in the same diff a reviewer reads. No apply mode hides it behind an indirection.

**Alternative rejected — a bound-mode indirection that swaps a context handle without rendering the
assembly.** It would be a smaller diff and a tidier call site. Rejected on **L3 UX + L1**: context is
*how the code builds the message list*, and hiding that behind a handle means a reviewer approves a
change they cannot see — precisely the failure P10 already ruled out by classifying context as **code,
not data** ([`openspec/project.md`](../../project.md), "wiring, skills, context policy … are code").
P16 obeys the existing rule rather than inventing an exception for the axis that most needs review.

The consequence for the rewriter: it is a *region* rewrite (how the `[]Message` is constructed), not a
composite-literal field insertion like the model/prompt rewriters. That is what makes it the hard part —
and what makes the per-language refusal honest until each region rewriter is written and tested.

## Decision 3 — Drop-loss is a scored admissibility gate, not only a post-hoc diagnosis

Each node carries an optional **drop tolerance**. A proposal whose resolved policy would drive that
node's drop ratio past its tolerance is **inadmissible** — rejected at proposal admissibility, before
transform and before any eval spend. A materialized policy's *observed* drop is still recorded via the
existing `context_drop_ratio`, so scoring and diagnosis see it too.

**Alternative rejected — let lossy variants transform and run, and catch the quality drop in scoring.**
Scoring *would* eventually punish a `summarization` variant that dropped the answer — the multi-seed run
would show a `task_success` regression. Rejected on **L4 运维 + L8 实现**: it catches the problem after
spending the eval budget to prove an obviously-lossy change bad. A drop-tolerance gate rejects it before
the spend, with no loss of correctness — the gate is a cheaper, earlier expression of the same verdict.
`DropRatio` is already computed at assembly ([`context_policies.go:218,339`](../../../internal/registry/context_policies.go)),
so the gate reads a number the policy already produces.

Tolerance is modeled as a **per-node admissibility input**, additive and omit-when-absent, because
whether a given drop is acceptable is a property of the node's *job* (a Retrieval node tolerates
augmentation a Summarization node does not), not of the policy. Omit-when-absent — a pointer field, no
key emitted when unset — keeps a node that declares no tolerance byte-identical to a pre-P16 node,
the same additive discipline P10's `Bindings` field used
([`resolved.go:62-69`](../../../internal/variantspec/resolved.go)).

## Decision 4 — Retrieval is verified on a held-out eval set

An `OpRAGTune` change (top-k, chunk size, rerank, embedding) is verified on an eval set **disjoint** from
the one its parameters were selected on. A win measured only on the tuning set is not a verified delta.

**Alternative rejected — verify on the set the parameters were tuned against.** One eval set, simpler
wiring, a higher-looking win. Rejected on **L1 honesty**: a top-k tuned to its own eval set and
"verified" on the same set is overfit sold as a win — indefensible the first time it regresses on a
customer's real traffic, and "we tuned and verified on the same data" is not a defensible sentence.
Held-out separation is the only honest verdict, and it is enforced **by construction** (the split is a
read model with `tuning ∩ holdout = ∅`), not by convention — a test constructs an overlap and asserts
the verdict is refused.

## Decision 5 — Retrieval is pinned per measurement run

A retrieval measurement run pins the retriever, its parameters, and the seed, so re-running the same
`config_hash` at the same `source_revision` issues the **identical resolved retrieval request**,
including any rerank.

**Alternative rejected — let the retriever run free and average over seeds.** It models "real"
retrieval variance. Rejected on **L2 稳定 + reproducibility**: an unpinned retriever makes a
`config_hash` non-reproducible — two runs of the same configuration disagree, which breaks the one thing
the whole lineage design exists to guarantee (re-derive a result from its hash;
[`openspec/project.md`](../../project.md) "measurement runs are pinned"). P3 already set the
reproducibility ceiling for host-calling policies: determinism is at the **resolved-request** level, not
the provider-bytes level, and the `ResolvedRequest` is the handle
([`context_policies.go:57-65,259-269`](../../../internal/registry/context_policies.go)). P16 pins the
retriever so that handle is stable.

## Decision 6 — New policies are additions behind the existing interface

`hierarchical-summary`, `structured-extraction`, and any future policy are new implementations of the
`Policy` interface (`Name` / `ParamsSchema` / `Assemble`), registered via `Store.AddPolicy`
([`internal/registry/store.go:41`](../../../internal/registry/store.go)).

**Alternative rejected — add policy-specific fields to `ContextSpec` or a `Dimension` variant.** It
would let a policy carry a bespoke typed param surface. Rejected on **L5 不可演进 + L6 不可扩展**: a
schema change per policy is exactly the extensibility failure the `Policy` interface + `ParamsSchema`
(a JSON Schema carried by the policy, [`context.go:29-33`](../../../internal/registry/context.go)) were
built to prevent — a policy validates its own params at registration without the registry learning its
shape. A new policy costs a `Name`/`ParamsSchema`/`Assemble` implementation and a row; nothing else moves.

## Decision 7 — No new `Dimension`; retrieval reuses `DimContext`

Retrieval tuning is a `rag-retrieval` policy with params, not a new `DimRetrieval`.

**Alternative rejected — a `DimRetrieval` sub-axis for RAG.** It reads cleanly ("retrieval is its own
thing"). Rejected on **L5 不可演进**: retrieval *is* a context policy already
([`context_policies.go:231`](../../../internal/registry/context_policies.go)); a second dimension would
split one axis's identity across two enum members, double the `config_hash` surface, and require a
second resolveNode block and a second rewriter for behavior the `rag-retrieval` policy already models.
The closed enum stays closed.

## Interfaces sketch

```
transform (Go engine):
  rewriteContext(site, src, resolvedOverride)
     resolved.ContextPolicy == discovered assembly     → no edit (nothing to change)
     resolved.ContextPolicy is Go-materializable        → REGION rewrite of the message-assembly code
     resolved.ContextPolicy not yet materializable here → refuseContext(node, policy, reason)  // typed, loud
                                                            // NEVER return nil,nil (that would be a silent drop)

admissibility (proposal):
  admissible(proposal) := ... AND expected_drop(resolved_policy, node) <= node.context_drop_tolerance   // D3, FR8/FR13
     // node.context_drop_tolerance absent ⇒ pattern-derived default (PRD §14 Q1)

retrieval-tuning (OpRAGTune):
  propose:  {top_k, chunk_size, rerank∈{on,off}, embedding_model}  admissible only on RetrievalRAG nodes
  measure:  pin(retriever, params, seed) ⇒ identical ResolvedRequest  // D5
  verify:   holdout_set ∩ tuning_set == ∅  ⇒  verified delta, else refuse  // D4

data model:
  NodeOverride  += context_drop_tolerance? float64∈[0,1]   // additive, omit-when-absent
  ResolvedNode  += context_drop_tolerance? float64          // frozen when set; hashed additively
```

## Risks

| Risk | Mitigation |
|---|---|
| A context override is silently dropped and scored as the base config | Decision 1 — every differing policy yields an edit or a typed refusal; a test asserts the override is applied OR refused, never absent-under-the-variant-hash. |
| A partial language rewriter emits a subtly-wrong assembly that compiles and degrades quality | The interim refusal stays until a language's rewriter is tested; a language never emits a half-correct context edit — it refuses (the same reasoning as `refuseSkills`, [`rewrite.go:388`](../../../internal/transform/rewrite.go)). |
| A lossy policy burns eval budget proving itself bad | Decision 3 — the drop-tolerance gate rejects it before transform and before spend. |
| A retrieval change overfits its tuning set | Decision 4 — verified on a held-out set disjoint by construction; an overlap is refused. |
| An unpinned retriever makes `config_hash` non-reproducible | Decision 5 — pin retriever + params + seed; identical `ResolvedRequest` under a fixed seed. |
| A new policy forces a schema or `Dimension` change | Decision 6 — additions behind the `Policy` interface via `Store.AddPolicy`; no schema, no enum member. |
| The drop-tolerance field breaks frozen `config_hash` golden vectors | Additive, omit-when-absent — a node with no tolerance hashes byte-identically to pre-P16. |
| A summarizer/retriever credential leaks to a sandboxed node | Every model/retrieval call is host-side through `HostServices`; the policy never holds a secret. |
