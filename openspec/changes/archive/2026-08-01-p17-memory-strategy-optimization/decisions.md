# P17 — Recorded decisions (System Designer, §1)

Seven contracts that must be fixed **before any code ships**, because each is a one-way door. This is a
**greenfield** axis: memory has never been modeled, so these decisions define its shape for every future
reader. Three of them create or extend a frozen contract (a new registry `Kind` + table, a new closed
`Dimension`, an additive extension to the P0 `config_hash`); two define a behavioral boundary that cannot
be cleanly un-drawn (memory-vs-context, the interim refusal); one binds the operator to the platform's core
principle; and one (**D7**) binds the *user-originated* path to the same refusal, which is the decision
most exposed to product pressure. Each walks: **Problem → Decision → Why appropriate → Alternatives +
decision point (rejected on L-level) → Effect**, and carries its governing 八级法则 level.

---

## D1 — Memory strategies get a **new registry `Kind` `memory`** and a **new `memory_entry` table**

**Problem.** A memory strategy must be referenceable, content-addressed, pinnable to a version, and
resolvable back from a `config_hash` — exactly what the four existing registries provide for model, prompt,
skill, and context ([`internal/registry/registry.go:57`](../../../internal/registry/registry.go)). The
`Kind` is hashed into every `version_id` precisely so a ref pasted into the wrong dimension **fails closed**
instead of resolving ([`registry.go:51`](../../../internal/registry/registry.go)). Where should memory
strategies live — a new Kind and table, or a subtype inside the existing `context_entry`?

**Decision.** A **new `KindMemory Kind = "memory"`** and a **new `memory_entry` table**, with a `memory.go`
register/resolve path shaped exactly like `model.go`. A memory strategy's `version_id` is
`sha256(envelope)` as for every other kind, unique across all five registries.

**Why this is the appropriate design.** Content addressing's fail-closed guarantee depends on the Kind
being part of the identity: two registries sharing a table would share an id namespace, at which point a
context ref and a memory ref can collide and a cross-dimension paste can *resolve* — silently binding the
wrong thing — instead of failing closed. A distinct Kind and table preserve the guarantee the other four
kinds already rely on, at the cost of one table. Uniformity is the payoff: `memory_entry` is `model_entry`
with a different Kind, so there is no fifth storage pattern to invent, review, or keep correct.

**Alternatives + decision point.** A discriminator column on `context_entry` (`subtype ∈ {context,
memory}`) is less code and one fewer table. Rejected on **不可演进 (L5) + 不可扩展 (L6)**: overloading a
frozen storage shape with a second meaning is a one-way weld — once memory rows live in the context table
and customers' hashes reference them, the two dimensions cannot be separated without a migration that
rewrites ids. An L6 extensibility loss (a namespace that can no longer be split) and an L5 evolvability
loss (a frozen table forced to carry a second concept) traded for the L8 convenience of not writing a
`CREATE TABLE`. The eight-level law forbids buying a higher-level loss with a lower-level convenience.

**Effect.** Memory strategies have a first-class home; a memory `version_id` resolves **only** in the
memory registry; a memory ref pasted into another dimension, and a foreign ref pasted into memory, both
fail closed — asserted, not hoped (tasks 5.5, 9.4).

---

## D2 — Memory is a **new `DimMemory` Dimension, disjoint from `DimContext`**

**Problem.** Both memory and context concern "what the model effectively sees," so there is a real pull to
model memory as a mode of the existing `DimContext` rather than a fifth Dimension. But the two are not the
same concern, and the choice is a one-way door: once specs and `config_hash`es depend on memory being a
context sub-mode, the two cannot be cleanly separated.

**Decision.** A **new `DimMemory Dimension = "memory"`** in the closed enum
([`spec.go:42`](../../../internal/variantspec/spec.go)), disjoint from `DimContext` in override, resolution,
and transform dispatch. No field, ref, or operator lets a memory change be expressed as a context change or
vice versa.

**Why this is the appropriate design.** The distinction is not ours to invent — the codebase already draws
it. `MemoryManagement` is a `capability` pattern whose confirmation signal is *"memory read/write against a
store between turns"* ([`taxonomy.go:108`](../../../internal/patternclassifier/taxonomy.go)): the
**between-turns** clause is the definition of memory, and it is what separates memory (persists **across**
invocations and sessions) from context assembly (how a **single** call builds its message list — not an
argument at all, but how the surrounding code constructs the message list, which is exactly why
`refuseContext` exists, [`rewrite.go:417`](../../../internal/transform/rewrite.go)). Honoring an existing
boundary keeps one source of truth for "what is memory"; inventing a second, contradictory one would be a
禁止分裂 violation.

**Alternatives + decision point.** Model memory as a `DimContext` sub-mode — one Dimension, less surface.
Rejected on **不可演进 (L5) + strategy**: a merged Dimension lets a cross-session concern masquerade as a
within-call one, and it can never be cleanly un-merged once hashes depend on it. The classifier would then
say memory and context are different (distinct pattern, distinct metric set) while the optimizer said they
were the same — a split brain across two subsystems. An L5 evolvability loss (a permanently conflated axis)
traded for the L8 convenience of one fewer enum member.

**Effect.** Memory and context stay two axes, each with its own registry Kind, override field, resolve
block, and dispatch entry, matching the classifier's own split. [P16](../p16-context-strategy-optimization/)
owns context; P17 owns memory; neither can express the other (tasks 9.5, NFR8).

---

## D3 — The memory field is **additive `omitempty`**; **`none` hashes byte-identically to absent**

**Problem.** `config_hash` is P0's frozen contract with golden vectors the live producer must reproduce
bit-for-bit ([`resolved.go`](../../../internal/variantspec/resolved.go)), and every existing row is keyed
by it. Memory must join the resolved configuration so the hash "changes iff the memory strategy changes,"
**without** changing the hash of any configuration that carries no memory strategy — else every existing
spec becomes non-reproducible and every keyed row orphans. How is the field shaped?

**Decision.** Add an **additive, `omitempty`, nil/empty-when-unset** memory field to `ResolvedNode`
([`resolved.go:46`](../../../internal/variantspec/resolved.go)), and likewise to `NodeOverride`
([`spec.go:183`](../../../internal/variantspec/spec.go)) and `IRNode`
([`emit.go`](../../../internal/discovery/emit.go)). A node with no memory strategy emits **no memory key at
all**, so its canonical bytes are identical to a pre-P17 node. **`none` is the identity strategy**: a `none`
node resolves to the same empty representation and therefore hashes identically to a no-memory node.

**Why this is appropriate.** This is the expand-contract rule the registry and IR already live by — a new
*optional* field must leave old serializations byte-identical
([`registry.go`](../../../internal/registry/registry.go): "a spec struct that gains an optional field must
leave old envelopes byte-identical"), and it is exactly how P10 added `bindings` without touching a golden
vector (P10 decisions.md D-1.4). The sibling maps `ContextParams`/`ProviderParams` are always-present `{}`
because they **predate** the golden and their emptiness is part of the frozen bytes; a **new** field's
*absence* is what must stay byte-compatible, and absence is achieved by omission, not by an always-present
empty value. Because `config_hash` is purely structural, the field then auto-participates the moment it is
present, with **no change to the hashing code**.

**Alternatives + decision point.** Make the memory field always-present (a serialized `none`, like the
sibling `{}` maps) for visual uniformity. Rejected outright on **稳定 (L2) + 不可演进 (L5)**: it changes
the canonical bytes of **every** existing node in **every** existing config, so every golden vector breaks
and every keyed row orphans — a reproducibility break of a frozen contract, the L2 stability loss the whole
golden exists to prevent, traded for L8 cosmetic consistency. The chosen shape is the only one that
satisfies "changes iff the memory strategy changes" **and** "no-memory config unchanged" simultaneously.

**Effect.** Every config authored before P17 hashes to exactly the byte it did before and stays
reproducible; a `none` node is indistinguishable from a no-memory node in the hash. A config that adds,
removes, or changes a memory strategy or its params gets a new `config_hash`. Backward compatibility is a
**test**, not a hope (tasks 4.3, 9.2).

---

## D4 — Interim refusal: a `MemoryRef ≠ none` node is **refused at transform with a typed `unsafeRewrite`**

**Problem.** Binding a memory backend at a call site is not an argument swap — it means wiring a store the
surrounding code reads and writes **between** invocations, with lifecycle, eviction, and persistence
concerns, and there is no live memory runtime to bind to (the sweeper and platform wiring were removed at
the pivot, [`launch.go:6`](../../../internal/launch/launch.go)). So the transform **cannot** safely
materialize a memory change today. What happens to a node that carries one? This is a one-way door: the
contract for "what a not-yet-applicable override does" is depended on by every future reader.

**Decision.** A resolved node carrying a memory strategy other than `none` is **refused at transform** — a
`refuseMemory` returning a typed `unsafeRewrite` ([`edit.go:90`](../../../internal/transform/edit.go)) that
names the node, the `memory` dimension, and the reason — registered in the dispatch table for `DimMemory`
in **both** engines ([`rewrite.go:54`](../../../internal/transform/rewrite.go), `rewrite_span.go:59`). It is
**never** silently dropped and **never** produces a diff. A spec carrying a `MemoryRef` still **resolves**
and still produces a stable `config_hash`; only the transform refuses.

**Why this is appropriate.** This is the repo's established honest pattern for an axis whose rewrite is not
yet safe: `refuseSkills` and `refuseContext` ([`rewrite.go:388`,`:417`](../../../internal/transform/rewrite.go))
do exactly this — the override is modeled, resolvable, and hashable, and solely the call-site
materialization is deferred. The typed error is load-bearing: a caller (the Loader, the UI, P4) can tell
"we cannot yet safely do this" from `ErrUnknownNode` / `ErrInvalidSpec` ("you asked for something that does
not exist") — the first is a limit of the engine, the second is the author's mistake, and they need
different messages ([`spec.go:62`](../../../internal/variantspec/spec.go)). Above all, a typed refusal can
be made to **go red** in a test; a silent drop cannot, which is what makes it dangerous.

**Alternatives + decision point.** (a) **Silently drop** the un-applicable memory override and emit a diff
for the other dimensions. Rejected on **安全 (L1)**: the user believes a memory change applied that did not,
and lineage records a `config_hash` that corresponds to no realizable behavior — a correctness-and-trust
failure discovered late, by someone puzzling over a non-effect. (b) **Block the whole spec at resolve.**
Rejected on **稳定 (L2) + 不可演进 (L5)**: it discards the modeling, hashing, and proposal that *are* safe,
making the axis useless for the large fraction of its value that does not need the codemod. The chosen
refusal loses nothing safe and makes the boundary observable; (a) loses at L1 and (b) loses at L2/L5, both
above the L8 convenience they would buy.

**Effect.** A memory override is honestly refused, not lost: `apply` on a memory node returns a typed
`unsafeRewrite` naming node + dimension + reason and produces no diff, in both engines; the same spec still
resolves and hashes, so it participates in lineage and proposal. The refusal has a canary that must come
back refused (task 7.4) — it can go red, so the guarantee is real.

---

## D5 — A **closed, versioned builtin strategy set**, each declaring a **`ParamsSchema`**

**Problem.** "Use a rolling summary instead of raw scratchpad notes" must become a value the system can
reference, hash, and validate — not a free-form string. A stored strategy name must stay interpretable if
the vocabulary is later extended, and a strategy's params must be checkable. Open-ended or closed?

**Decision.** Ship exactly **five** builtin strategies — `none`, `scratchpad`, `summary-buffer`,
`vector-recall`, `entity-memory` — as a **closed, versioned** set, each declaring a **`ParamsSchema`**, a
title, and a description. A **cardinality assertion** (like `TaxonomySize`,
[`taxonomy.go:126`](../../../internal/patternclassifier/taxonomy.go)) fails on an unversioned sixth
strategy; a params violation is rejected **at seal**.

**Why this is appropriate.** A closed, versioned vocabulary is how this codebase already keeps stored
labels interpretable — `TaxonomyVersion` pins the pattern vocabulary so an added pattern without a version
bump fails loudly rather than silently changing what a stored label means
([`taxonomy.go:8`](../../../internal/patternclassifier/taxonomy.go)). Memory strategies need the same
guarantee: a `config_hash` from six months ago that references strategy `summary-buffer` must still mean the
same thing. The `ParamsSchema` moves validation to **seal time** — a malformed `vector-recall` (missing its
top-k, say) is rejected before it is stored, not discovered at run time. The five are a deliberate spread
across the design space (identity / ephemeral notes / rolling summary / embedding recall / structured
facts), so the operator has real, distinct hypotheses to propose between.

**Alternatives + decision point.** Free-form strategy strings with open params — maximally flexible, no
vocabulary to maintain. Rejected on **不可演进 (L5) + 稳定 (L2)**: an open set makes a stored strategy name
un-interpretable the moment the vocabulary drifts (the exact hazard `TaxonomyVersion` prevents), and open
params cannot be validated, so a bad entry surfaces as a run-time failure instead of a seal-time rejection.
An L5 evolvability loss (un-versioned, un-interpretable names) and an L2 stability loss (unvalidated params)
traded for the L8 convenience of skipping a schema.

**Effect.** The five strategies resolve; a name outside the set does not; a sixth added without a version
bump fails the cardinality assertion; a params-schema violation is rejected at seal (tasks 5.3, 5.4, 9.4).
A future strategy is a versioned addition, decided with its real target in hand.

---

## D6 — `OpMemoryPolicy` **proposes**; **verification decides**; **no win is claimed while the transform refuses**

**Problem.** The diagnosis engine should be able to *propose* a memory strategy swap against a memory
bottleneck (`stale_read` / `contradictory_memory`, [`metricset.go:101`](../../../internal/patternclassifier/metricset.go)).
But the transform refuses a memory rewrite (D4), so at M20 a memory proposal cannot be realized or scored.
What may the operator claim? This binds the operator to the platform's core principle and is depended on by
the dashboard and any commercial claim.

**Decision.** Add `OpMemoryPolicy` as a catalogued operator ([`operator.go:34`](../../../internal/proposal/operator.go),
a `DefaultCatalog` row, an `operatorPrior` + `verifyOrderHint`, [`gain.go:8,26`](../../../internal/proposal/gain.go)).
It **proposes** a strategy swap; its prior is a coarse ordering hint, **never** a result. While the
transform refuses, a memory proposal is surfaced as **refused-not-scored** — it resolves and hashes but is
refused at transform, so it yields **no** verified result and is never reported as a gain.

**Why this is appropriate.** It is the platform's core principle stated for this axis: *diagnosis proposes,
verification decides.* A proposal that cannot be verified cannot be a win, and reporting one would be
claiming an undelivered capability — a sales-honesty violation (only claim delivered, verifiable
capability) and, if a bill or dashboard were built on it, indefensible. The operator is catalogued but
**dormant**, exactly the posture of the reserved `OpMerge` ([`operator.go:46`](../../../internal/proposal/operator.go)):
present in the vocabulary, inert until its rewrite is safe. The improvement signal, when finally scorable,
is the classifier's **existing** metric set — `memory_hit_rate` up, `staleness` down, tokens down
([`metricset.go:98`](../../../internal/patternclassifier/metricset.go)) — not a new number invented to
flatter the axis.

**Alternatives + decision point.** Ship the operator as if a memory change were applicable and let it
report gains, so memory looks "done." Rejected on **安全/honesty (L1) + core principle**: a reported win
that never passed verification is a false claim, and the whole architecture exists to prevent exactly that
(a produced diff ≠ a scored win). The L1 honesty loss traded for the L8 appearance of completeness — the
worst trade in the ordering.

**Effect.** The diagnosis engine can propose a memory swap; the proposal is honestly surfaced as refused
until the rewrite lands; no scored memory win is reported anywhere at M20 (tasks 8.1–8.5, 9.6). When a
future memory-runtime phase lifts the refusal, the operator wakes with no change to its contract — its
worth was always going to be decided by the harness.

---

## D7 — A user may **author** a memory change; the platform **refuses to apply it**, but never silently, never late, and never disguised as success

**Problem.** Every other optimization axis lets a workflow owner originate a change themselves, on P13's
shared [`authored-change`](../p13-prompt-model-optimization/specs/authored-change/spec.md) spine: one
pipeline, two origins, `Origin` recorded and never hashed, *a user may author the change, a user may not
author the evidence.* Memory should be no different — an engineer who knows their agent re-reads a stale
fact every third turn should be able to pin `summary-buffer(max_tokens=2000)` on that node without waiting
for the catalog to nominate it. But **D4 refuses the transform**. The shared contract's central allowance —
*an authored change may be applied without a verification verdict, because it is the user's own
repository* — therefore **cannot fire on this axis at M20**. What is a user allowed to do, and what is the
platform allowed to show them? A one-way door: the states a surface can render and the records an apply
path can write are contracts the moment either exists.

**Decision.** Bind the shared contract, and split the refusal precisely: **modeling is not refused,
materialization is.** A user MAY select a node's strategy and params from the closed builtin set and MAY
clear it; the selection resolves, hashes, seals a registry entry, records `user` origin + actor + parent
pointer, and diffs in lineage — a real `config_hash`, re-materializable unchanged the day the rewriter
lands. A user MAY NOT get a diff, an apply, a delivery, or a number. Three rules make the refusal
survivable rather than a dead end:

1. **Stated before the choice.** The surface renders the M20 no-materializer boundary *before* a strategy
   is selected, read from **the same coverage fact the engine refuses from** — not a hand-written sentence
   beside it.
2. **Raised at preflight, with the transform's own typed cause.** Before any worktree, build, or eval
   spend, naming the node, the `memory` dimension, and the deferred call-site materialization.
3. **Never rendered as success.** `refused` is its own state, distinct from `failed` and from `pending`; no
   surface or record shows applied, delivered, or partially applied; nothing is scored.

**Why this is the appropriate design.** The alternative framings both fail on the axis's own terms. Refusing
to let the user *author* would confuse "we cannot write this into your source" with "you may not express
this," and would throw away the part that is completely safe and genuinely useful — a pinned, hashed,
comparable configuration that survives to the rewrite. Letting the user *apply* would require a second
apply path, which is a second place for every gate to be wrong (the exact thing the shared contract's "one
spine" clause exists to forbid). Stating the boundary **before** the choice rather than after is the
product half of the same honesty D4 enforces in the engine: a refusal a user discovers only after
composing a change is technically honest and practically a bait-and-switch, and an inert greyed-out
control is worse still — it tells the user nothing about *why*, and invites them to believe some other
strategy, language, or plan would unlock it. Sourcing that sentence from the coverage fact rather than
writing it twice is 禁止分裂 source-of-truth: two sentences are two things to keep true, and the copy in
the UI is the one that will drift.

**Alternatives + decision point.**

| Option | What the user gets | Failure mode | Level |
|---|---|---|---|
| No authoring on this axis until the rewriter lands | nothing | discards a safe, useful, fully-modeled capability; the axis is unusable for the large fraction of its value that needs no codemod | rejected **L5/L6** |
| Authoring with a **second** apply path that "applies" the record | an applied-looking change | a second place for every gate to be wrong; a lineage record claiming a configuration the source never had — D4's silent-drop failure, re-introduced one layer up | rejected **L1** |
| Authoring, refusal **discovered at apply** | a composed change, then a wall | technically honest, practically a bait-and-switch; the user spends effort against a boundary the platform knew about before they started | rejected **L1/L8** |
| Live control + **boundary stated up front**, refused at preflight, never scored (**chosen**) | a real hashed variant, an honest limit, no surprises | user cannot apply today — **stated plainly, before they choose** | — |

**Effect.** A workflow owner can select, parameterize, and clear a node's memory strategy and see the exact
`config_hash` it produces; the surface says up front that it cannot be written to source at M20 and why;
the apply path refuses at preflight with the transform's cause; nothing is ever shown as applied or scored.
Clearing reproduces the prior hash byte-identically and `none` is indistinguishable from cleared, so a user
can back out with no residue. When a future memory-runtime phase lifts the refusal, the authored variants
already stored materialize with no re-authoring and no change to this contract — which is the test of
whether the split between modeling and materialization was drawn in the right place.
