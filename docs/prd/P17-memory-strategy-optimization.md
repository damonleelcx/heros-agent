# PRD — P17: Memory Strategy Optimization (making what an agent remembers a tunable dimension)

| Field | Value |
|---|---|
| Phase / Milestone | P17 / M20 |
| Target window | Two waves: 20a memory-store + the policy modeling and its interim refusal, then 20b the operator and the metric wiring |
| Lead role(s) | System Designer + AI Engineer (co-leads) |
| Supporting role(s) | Backend, QA Engineer, Product Designer |
| Status | Draft |
| OpenSpec change | `p17-memory-strategy-optimization` |
| Related | [P11 — CLI & CI Integration](P11-cli-ci-integration.md) · [P16 — Context Strategy Optimization](P16-context-strategy-optimization.md) · [P3 — Context, Skills, Sandbox](P3-context-skills-sandbox.md) |

> **Commercial position.** Memory optimization is a **Business/Enterprise** capability in the same way
> every other optimization axis is: the modeling, the registry, and the config-hash participation ship
> on every plan because they are part of the shared substrate, and the *operator* that proposes and
> verifies a memory change is part of the diagnosis engine that gates behind the paid tiers. Nothing in
> this document claims a delivered, scored memory win — see §2. What ships here is honest modeling plus
> a first-class refusal; the sales claim is exactly that and no more.

> **Money-in-git rule.** No dollar amounts, percentages, or price bands appear in this document. Plans
> are referred to by **name only** — Free / Team / Business / Enterprise.

> **Status update — superseded in part by [P18](P18-memory-runtime.md).** This PRD specifies memory as
> *modeled and refused*, and its refusal named what was missing: a memory runtime plus the call-site
> rewriter that reads and writes it. P18 shipped both. Memory now **materializes** at a Python call site
> that writes its message list and assigns its result, and still refuses — with a typed cause — everywhere
> else. Read FR11/FR12's "refused at transform" as *refused where the cell has no materializer*; the
> no-silent-drop guarantee behind them is unchanged and re-asserted in P18.

## 1. Summary

Every optimization axis on this platform is a **Dimension**: a node's model, prompt, skills, or context
can each be overridden, resolved to an exact value, hashed into a `config_hash`, and — for the two axes
that are call-site-safe — realized as a source codemod and scored. The canonical enum is closed and
tiny ([`internal/variantspec/spec.go:42`](../../internal/variantspec/spec.go)): `DimModel`, `DimPrompt`,
`DimSkills`, `DimContext`. **Memory is not one of them.** An agent's memory — what it carries *across*
invocations and sessions — is today neither modelable nor optimizable. It exists in the codebase only as
two disconnected fragments: a **detectable behavioral pattern** (`MemoryManagement`, pattern 8 of the
frozen taxonomy, [`internal/patternclassifier/taxonomy.go:29`](../../internal/patternclassifier/taxonomy.go),
with metrics `memory_hit_rate` / `recall_precision` / `staleness` / `write_amplification` at
[`internal/patternclassifier/metricset.go:98`](../../internal/patternclassifier/metricset.go)), and a
**filesystem directory concept** (`MemoryDir = "memory"`,
[`internal/agentlayout/layout.go:13`](../../internal/agentlayout/layout.go)). The classifier can *notice*
that a target agent manages memory and can *score how well it does*; it cannot *propose a different
strategy and prove the change is better*. The old runtime "memory sweeper" that once touched this area was
removed in the pivot ([`internal/launch/launch.go:6`](../../internal/launch/launch.go)) and left nothing
optimizable behind.

P17 introduces memory as a **new first-class Dimension**, following the repository's canonical eight-step
"add an axis" checklist (§8.3) exactly as `DimModel` and `DimPrompt` did. It delivers two capabilities. A
**memory-store**: a new content-addressed registry `Kind` (`memory`) holding versioned **memory
strategies** — `none`, `scratchpad`, `summary-buffer`, `vector-recall`, `entity-memory` — each declaring a
`ParamsSchema`, backed by a new `memory_entry` table. And a **memory-policy**: a new `DimMemory` Dimension,
a `NodeOverride.MemoryRef`, a `ResolvedNode` field that auto-participates in `config_hash` because the hash
is purely structural, an additive/`omitempty` IR field, and an operator `OpMemoryPolicy` that proposes
strategy swaps whose worth is **decided by verification**, not by the proposal.

The load-bearing honesty of this phase is its **interim refusal**. Binding a memory backend at a call site
is not an argument swap — it is wiring a store the surrounding code reads and writes *between* turns, which
is not yet safe to generate. So P17 does **not** ship a memory codemod. Instead it specifies, as a
first-class requirement, that a node carrying a `MemoryRef` **SHALL be refused at transform with a typed
`unsafeRewrite`, never silently dropped** — mirroring exactly how skills and context are handled today
([`internal/transform/rewrite.go:388`](../../internal/transform/rewrite.go) `refuseSkills`, `:417`
`refuseContext`). The Dimension is modeled, resolvable, and hashable end-to-end; the call-site
materialization is deferred to a named future owner. Milestone **M20 — memory is a modeled, refused
dimension** means the platform can *represent* a memory change, *hash* it into lineage, *propose* it, and
*refuse* to realize it honestly, which is the correct and complete state for an axis whose rewrite is not
yet safe.

## 2. Problem & context

The platform's premise is that any lever affecting an LLM workflow's quality or cost should be
expressible as a sparse override, resolvable to a hash, and — when safe — provable via a scored diff.
Memory is such a lever and is entirely missing from the optimizer spine. Five problems block it, and each
maps to a design commitment below.

- **Memory is observable but not actionable — the classifier is one-way.** The pattern classifier already
  isolates `MemoryManagement` as a distinct behavioral pattern with its own metric set
  (`memory_hit_rate` primary, `staleness`, `recall_precision`, `write_amplification`;
  [`metricset.go:98`](../../internal/patternclassifier/metricset.go)) and its own failure modes
  (`contradictory_memory`, `stale_read`). We can therefore *measure* whether a target agent's memory is
  working. What we cannot do is *change it and re-measure*: there is no Dimension, no override field, no
  registry entry, and no operator. Diagnosis without an actionable axis is a thermometer with no dial.
- **Memory and context are different things, and the codebase already says so.** Memory persists **across
  invocations and sessions**; context assembly ([P16](P16-context-strategy-optimization.md), `DimContext`)
  is how a single call builds its message list. The classifier encodes exactly this split: `MemoryManagement`
  is a `capability` pattern whose confirmation signal is *"memory read/write against a store between turns"*
  ([`taxonomy.go:108`](../../internal/patternclassifier/taxonomy.go)) — the *between-turns* clause is the
  boundary. Folding memory into `DimContext` would erase a distinction the taxonomy is built on and let a
  cross-session concern masquerade as a within-call one. This is a one-way door (§8, decisions.md D2).
- **There is no vocabulary of memory strategies.** "Use a rolling summary instead of raw scratchpad notes"
  is a sentence, not a value the system can reference, hash, or diff. Without a content-addressed registry
  of named strategies, a memory change cannot enter a `config_hash`, cannot be reproduced from one, and
  cannot be compared. The four existing registries ([`internal/registry/registry.go:57`](../../internal/registry/registry.go)
  `KindModel`/`KindPrompt`/`KindSkill`/`KindContext`) are the model to follow; memory needs its own `Kind`.
- **Realizing a memory change at a call site is not yet safe, and pretending otherwise would ship a diff
  nobody can trust.** Only `model` and `prompt` actually emit edits today; skills and context *refuse*
  with a typed `unsafeRewrite` ([`rewrite.go:388`,`:417`](../../internal/transform/rewrite.go)) because
  generating them correctly is code generation, not an argument swap, and a subtly-wrong version compiles
  and degrades quality invisibly. Memory is strictly harder: it is a *stateful* store the surrounding code
  must read and write between invocations, with lifecycle, eviction, and persistence concerns. The honest
  interim is the same one the repo already uses — refuse until safe — made first-class rather than implicit.
- **The runtime that would host memory was deliberately removed.** The pivot stripped the "memory sweeper"
  and the platform-runtime wiring ([`launch.go:6`](../../internal/launch/launch.go)), with subsystems
  "reintroduced per phase." So there is no live memory runtime to bind to even if the transform were ready.
  This reinforces the refusal: the modeling can and should land now; the rewrite waits for a runtime owner.

**Upstream state assumed.** **P1** (discovery and the Workflow IR into which a memory default is emitted —
[`internal/discovery/emit.go`](../../internal/discovery/emit.go) `IRNode`). **P2** (Variant Spec
resolution, the `config_hash` contract that any new `ResolvedNode` field joins automatically, and the
transform engine whose per-dimension dispatch table this phase extends —
[`rewrite.go:54`](../../internal/transform/rewrite.go)). **P4** (the axis-agnostic eval harness and
scoring, which consumes only `config_hash` + `Trace` and therefore scores a memory variant with no change,
plus the bespoke-metric registry a memory quality metric can register through). **P4.5** (the diagnosis
taxonomy and the proposal catalog / operator priors this phase adds a row to —
[`internal/proposal/operator.go:34`](../../internal/proposal/operator.go),
[`gain.go:8`](../../internal/proposal/gain.go)). **P16** (context assembly, the sibling within-call axis
from which memory is deliberately kept separate).

## 3. Goals & non-goals

### Goals

- **G1. Memory is a first-class Dimension.** A new `DimMemory` SHALL join the closed Dimension enum, and a
  node's memory strategy SHALL be overridable exactly as its model and prompt are, through the same
  sparse-override → resolve → hash pipeline, with no special-casing in eval or scoring.
- **G2. Memory strategies are versioned, content-addressed registry entries.** A new registry `Kind`
  (`memory`) SHALL hold named strategies, each content-addressed and each declaring a `ParamsSchema`, so a
  strategy can be referenced, pinned to a version, and resolved back from a `config_hash`.
- **G3. The builtin strategy vocabulary is enumerated and closed for this version.** The platform SHALL
  ship exactly `none`, `scratchpad`, `summary-buffer`, `vector-recall`, and `entity-memory`, each with a
  declared meaning and `ParamsSchema`; adding a strategy is a versioned change, not an ad-hoc string.
- **G4. `none` is the identity strategy.** A node whose memory strategy is `none` SHALL resolve and hash
  **byte-identically to a node that carries no memory field at all**, so every existing config remains
  reproducible and no golden vector changes.
- **G5. Memory participates in `config_hash` automatically.** The new `ResolvedNode` memory field SHALL be
  additive and `omitempty`, so it joins the purely-structural hash the moment it is present and is absent
  from the canonical bytes when it is not — the hash SHALL change iff the memory strategy changes.
- **G6. Memory is distinct from context and is never conflated with it.** The memory Dimension SHALL model
  only cross-invocation persistence; within-call context assembly remains [P16](P16-context-strategy-optimization.md)'s
  `DimContext`. No field, ref, or operator SHALL let a memory change be expressed as a context change or
  vice versa.
- **G7. A node carrying a memory override SHALL be refused at transform, never silently dropped.** Until a
  memory codemod is safe, a `MemoryRef` on a node SHALL cause the transform to return a typed
  `unsafeRewrite` naming the node, the dimension, and the reason. It SHALL NOT be ignored, and it SHALL NOT
  produce a diff.
- **G8. The refusal is resolvable and hashable, not a rejection of the spec.** A spec carrying a
  `MemoryRef` SHALL still resolve and still produce a stable `config_hash`; only the *transform* refuses.
  Modeling, lineage, and proposal are complete; solely the call-site materialization is deferred.
- **G9. An operator proposes memory strategy swaps, and verification decides.** A new operator
  `OpMemoryPolicy` SHALL be catalogued with a prior and a verification-order hint, so the diagnosis engine
  can *propose* a strategy change against a memory bottleneck. The proposal SHALL carry no authority; its
  worth is decided by the harness, and while the transform refuses, a memory proposal SHALL NOT be
  reported as a verified win.
- **G10. The memory quality signal is the classifier's existing metric set.** Improvement, once a memory
  codemod is safe to score, SHALL be read as a higher `memory_hit_rate`, a lower `staleness`, and fewer
  eval tokens — the metrics the pattern classifier already defines — not a new bespoke number invented here.
- **G11. Discovery emits a memory default for every node.** The IR SHALL carry a memory field per node,
  defaulting to `none`, so the resolver always has a base to override against and a node's absence of a
  memory strategy is an explicit fact rather than a missing field.

### Non-goals (explicitly deferred or owned elsewhere)

- **A memory codemod / call-site rewrite** — **deferred to a named future memory-runtime owner** (§14 Q1).
  P17 ships the refusal, not the rewrite. No scored memory win is claimed at M20.
- **A live memory runtime / store** — the platform-runtime wiring was removed at the pivot
  ([`launch.go:6`](../../internal/launch/launch.go)) and is reintroduced per phase; P17 does not
  reintroduce it. `vector-recall`'s embedding store and `entity-memory`'s structured store are strategy
  *descriptions*, not running services.
- **Within-call context assembly** — **[P16](P16-context-strategy-optimization.md).** Message-list
  construction, summarization within one call, and lost-in-middle reordering are `DimContext`.
- **Changing the pattern taxonomy or its metrics** — the `MemoryManagement` pattern, its metric set, and
  `TaxonomyVersion` are frozen contracts ([`taxonomy.go:8`](../../internal/patternclassifier/taxonomy.go));
  P17 *consumes* them and adds no pattern and no metric.
- **A second cost model or a bespoke scoring path** — eval is axis-agnostic; memory is scored through the
  existing harness on `config_hash` + `Trace` with no scoring change.
- **Prompt-level memory instructions** — telling a model to "remember X" inside a prompt body is a
  `DimPrompt` concern, not a memory strategy.

## 4. Users & personas

| Persona | What P17 is for them | What breaks without it |
|---|---|---|
| **AI engineer optimizing a stateful agent** (primary) | Express "try a rolling summary instead of raw scratchpad" as a referenceable, hashable strategy and let the harness decide — once the rewrite is safe, a scored answer; today, a modeled, proposable one. | Memory changes stay untracked one-offs that never enter lineage and can never be A/B'd. |
| **System designer extending the optimizer** (primary) | A worked example of the eight-step "add an axis" checklist applied end-to-end, including the honest interim refusal for an axis whose rewrite is not yet safe. | The next axis re-derives the checklist and re-litigates the memory-vs-context boundary from scratch. |
| **Backend engineer** | A new registry `Kind`, a `memory_entry` table, and a resolve/register path shaped exactly like the four existing ones — no new patterns to invent. | Memory strategies have nowhere to live and no content-addressed identity. |
| **QA engineer** | A refusal that can be made to **go red**: a node with a `MemoryRef` must return a typed `unsafeRewrite`, and a `none` node must hash byte-identically to a no-memory node — both machine-checkable. | The interim refusal degrades silently into a dropped override, the worst failure mode. |
| **Product designer** | A memory strategy surfaced as a named, explained choice with visible params, kept as three separate layers (interface text ↔ strategy entity ↔ code name). | Users see an opaque enum they cannot reason about. |

Non-personas: **the end users of the target agent** (they never see the platform), and **platform
operators** (P8) — no runtime is introduced here to operate.

## 5. User stories / jobs-to-be-done

**AI engineer**
- As an AI engineer, I want to name a memory strategy and its params as a versioned entry, so that a
  memory change is a referenceable thing and not a code edit I have to remember I made.
- As an AI engineer, I want a memory change to alter the `config_hash`, so that two runs that differ only
  in memory strategy are distinguishable in lineage and comparable in the dashboard.
- As an AI engineer, I want the platform to *propose* a memory strategy against a `stale_read` bottleneck,
  and I want that proposal to mean nothing until it is verified.

**System designer**
- As a system designer, I want memory added through the exact same eight steps as every other axis, so
  that the spine stays uniform and the next axis has a second worked example.
- As a system designer, I want the memory-vs-context boundary decided once, in writing, tagged with the
  level it was decided on, so it is never re-litigated.

**Backend engineer**
- As a backend engineer, I want the memory registry to be structurally identical to the model registry, so
  I am not inventing a fifth storage pattern.
- As a backend engineer, I want a node with no memory strategy to serialize exactly as it did before the
  field existed, so no existing row or golden vector breaks.

**QA engineer**
- As a QA engineer, I want a test that a `MemoryRef` node is **refused** at transform, and a test that a
  `none` node hashes identically to a no-memory node — because a refusal that cannot go red is decoration.

**Product designer**
- As a product designer, I want each strategy to have a human title and a readable param surface, distinct
  from its wire name, so a user chooses a memory strategy from understanding rather than from a raw enum.

## 6. Functional requirements

Numbered FRs; each maps 1:1 to an OpenSpec requirement under
`openspec/changes/p17-memory-strategy-optimization/specs/`.

### The memory registry (capability `memory-store`)

- **FR1.** A new registry `Kind` `memory` SHALL exist alongside `model`, `prompt`, `skill`, and `context`,
  content-addressed identically (a `version_id` derived from the sealed envelope, unique across all
  registries), backed by a new `memory_entry` table.
- **FR2.** The platform SHALL ship exactly five builtin strategies — `none`, `scratchpad`,
  `summary-buffer`, `vector-recall`, `entity-memory` — as a closed, versioned set. A strategy name outside
  this set SHALL NOT resolve.
- **FR3.** Each strategy SHALL declare a `ParamsSchema` describing its tunable parameters (for example a
  token budget for `summary-buffer`, a top-k and embedding reference for `vector-recall`, an entity-key
  set for `entity-memory`). A memory entry SHALL be rejected if its params violate the strategy's schema.
- **FR4.** `none` SHALL be the identity strategy: a resolved node whose strategy is `none` SHALL be
  byte-identical, under canonicalization, to a resolved node that carries no memory field, so no existing
  `config_hash` changes.
- **FR5.** A memory entry SHALL be inline-definition-free: a spec SHALL reference a memory strategy by
  version_id, never inline its params, so it is always resolvable back from a `config_hash` (mirroring the
  registry's existing inline-definition rejection).
- **FR6.** A strategy SHALL carry a stable human title and a description distinct from its wire name, so
  the interface layer, the strategy entity, and the code name remain three separate layers.

### The memory Dimension and its refusal (capability `memory-policy`)

- **FR7.** `DimMemory` SHALL be added to the closed `Dimension` enum, and `ResolvedOverride.Dimensions()`
  SHALL report it when and only when a memory override is set, so the transform engine iterates it exactly
  as it does the other four.
- **FR8.** `NodeOverride` SHALL gain a `MemoryRef` field (a memory-registry version_id), additive and
  `omitempty`, participating in `isEmpty`, `Refs`, and `Validate` exactly as the sibling refs do.
- **FR9.** `ResolvedNode` SHALL gain a memory field, additive and `omitempty` with a nil/empty-when-unset
  representation, so a node with no memory strategy emits no memory key and hashes byte-identically to a
  pre-P17 node; when present it participates in `config_hash` structurally, with no change to the hashing
  code.
- **FR10.** The IR node SHALL carry a memory field, additive and `omitempty`, defaulting to `none`, emitted
  by a discovery frontend, so the resolver always resolves against a concrete base.
- **FR11.** A resolved node carrying a memory strategy other than `none` SHALL be **refused at transform**
  with a typed `unsafeRewrite` that names the node, the `memory` dimension, and the reason (call-site
  materialization of a cross-invocation store is deferred). It SHALL NOT be silently dropped and SHALL NOT
  produce a diff.
- **FR12.** The refusal SHALL occur in **both** transform engines — the Go AST rewriter dispatch
  ([`rewrite.go:54`](../../internal/transform/rewrite.go)) and the tree-sitter span rewriter
  ([`rewrite_span.go:59`](../../internal/transform/rewrite_span.go)) — so no target language silently
  applies a memory change through the other path.
- **FR13.** A spec carrying a `MemoryRef` SHALL still **resolve** and still produce a stable, reproducible
  `config_hash`; the refusal SHALL be a property of the transform only, not of resolution or hashing.
- **FR14.** A new operator `OpMemoryPolicy` SHALL be catalogued (an `OperatorKind`, a `DefaultCatalog` row,
  an `operatorPrior`, and a `verifyOrderHint`) so the diagnosis engine can propose a memory strategy swap
  against a memory bottleneck signal.
- **FR15.** An `OpMemoryPolicy` proposal SHALL carry no authority: its worth SHALL be decided by
  verification, and while the transform refuses a memory rewrite, a memory proposal SHALL NOT be reported
  as a verified win. The proposal path SHALL surface the refusal honestly rather than as a scored result.
- **FR16.** The memory improvement signal SHALL be the classifier's existing metric set — `memory_hit_rate`
  (primary), `staleness`, `recall_precision`, `write_amplification`
  ([`metricset.go:98`](../../internal/patternclassifier/metricset.go)) — plus eval token totals; P17 SHALL
  add no new metric and SHALL NOT alter the taxonomy.

### User-initiated change on this axis (capability `memory-authoring`)

The cross-axis rules are **FR21–FR33 of [P13](P13-prompt-model-optimization.md)** (capability
[`authored-change`](../../openspec/changes/p13-prompt-model-optimization/specs/authored-change/spec.md))
and apply here in full without restatement: one spine, two origins; origin recorded and never hashed; *a
user may author the change, a user may not author the evidence.*

What P17 adds is governed by the one fact that separates this axis from
[P13](P13-prompt-model-optimization.md)/[P14](P14-skills-tools-optimization.md)/[P15](P15-workflow-wiring-optimization.md)/[P16](P16-context-strategy-optimization.md):
**at M20 the transform refuses every memory change.** On the other four axes an authored change reaches
the user's source as a diff and is merely *unscored*. Here it does not reach the source at all. That makes
the authoring surface's job the opposite of the usual one — it must sell the user *less* than they will
expect, up front and in specifics, and it must still be worth using.

It is worth using because the refusal is narrow. Selecting a strategy **resolves**, **hashes**,
**versions**, **records**, and **compares** — everything except the codemod. A workflow owner can pin
`summary-buffer(max_tokens=2000)` on a node, see the exact `config_hash` it produces, diff it against the
parent variant, hand the id to a colleague, and have it survive to the day the rewriter lands, at which
point that same id materializes. What they cannot do is get a diff today, or a number ever, until
verification runs.

> **The sentence this capability adds to the shared contract: the platform may refuse to apply an
> authored change, but it may never refuse *silently*, discover the refusal *late*, or dress the refusal
> up as the change having *worked*.**

- **FR17.** A user SHALL be able to **select a node's memory strategy and set its parameters** from the
  closed builtin set, expressed solely through the existing `MemoryRef` override so it resolves, freezes,
  and participates in `config_hash` through the existing field. Only **registered** strategies SHALL be
  offered; free text SHALL NOT be a selection path, and a params value violating the strategy's
  `ParamsSchema` SHALL be rejected before the entry is sealed.
- **FR18.** A user SHALL be able to **clear** a node's memory strategy. Clearing SHALL reproduce the
  pre-selection `config_hash` **byte-identically**, and selecting `none` SHALL be indistinguishable from
  clearing — the same `config_hash`, the same canonical bytes (FR4 applied to authoring). A surface SHALL
  NOT present `none` and *cleared* as two states that differ in effect.
- **FR19.** 🔴 An authored memory change SHALL be refused at **preflight** — before any transform, worktree,
  build, or eval spend — with the **same typed cause the transform raises**, naming the node, the `memory`
  dimension, and the deferred call-site materialization. The user SHALL learn this from the refusal path,
  never from an empty diff.
- **FR20.** 🔴 **Before** a user selects a strategy, the authoring surface SHALL state — from the same
  coverage source the transform refuses from — that a memory change **cannot be applied to source at
  M20**, and it SHALL state this as a **fact about the platform's missing artifact**, not as a fact about
  the user's call site, their language, or their strategy choice. The control SHALL be **live and the
  boundary stated**, never silently disabled: a disabled control tells the user nothing about *why*, and
  invites the belief that some other strategy, language, or plan would enable it.
- **FR21.** 🚫 An authored memory change SHALL NOT be **applied**, delivered, or merged at M20, and the
  refusal SHALL NOT be presentable as success. No surface, report, or record SHALL show an applied,
  delivered, or partially-applied state for a memory change while the transform refuses it.
- **FR22.** An authored memory change SHALL nonetheless be **derivable, resolvable, hashable, storable and
  comparable**: it SHALL produce a candidate variant with a real `config_hash`, a recorded `user` origin
  and actor, and a parent-variant pointer, so it is diffable in lineage and re-materializable unchanged
  once the rewriter lands. Refusing to apply SHALL NOT mean refusing to model.
- **FR23.** An authored memory change SHALL be stamped `unverified` and SHALL claim **nothing** — no
  memory-hit-rate gain, no staleness reduction, no token or cost saving, no quality effect — until the
  harness has run. While the transform refuses, it SHALL be surfaced as **refused-not-scored** (FR15
  applied to the user-originated path), and `refused` SHALL be rendered as its own state, distinct from
  both `failed` and `pending`.
- **FR24.** The two origins SHALL be **indistinguishable downstream**: a memory configuration a user
  authors and one `OpMemoryPolicy` proposes SHALL resolve to the same `config_hash`, be refused by the
  same rewriter with the same typed cause, and pass the same gates. There SHALL be no authoring-only
  resolve path, transform path, or gate.

## 7. Non-functional requirements

| # | Requirement | Target |
|---|---|---|
| **NFR1** | **Hash back-compatibility** | A `none` node and a no-memory node canonicalize to identical bytes; the P0 golden config-hash vectors reproduce unchanged. Machine-asserted, not reviewed. |
| **NFR2** | **Config-hash participation** | Two specs differing only in memory strategy (or memory params) produce different `config_hash`es; two differing only in memory *authoring order that is not identity-bearing* produce the same one. |
| **NFR3** | **Refusal totality** | Every path from a resolved `MemoryRef ≠ none` to a transform output ends in a typed `unsafeRewrite`; there is no code path that drops the override or emits a memory edit. Asserted in both engines. |
| **NFR4** | **Refusal typing** | The refusal is the repo's `unsafeRewrite` error type, distinguishable by callers from `ErrUnknownNode` / `ErrUnresolvedRef` / `ErrInvalidSpec`, so "we will not do this yet" is never confused with "you asked for something that does not exist." |
| **NFR5** | **Registry uniformity** | The memory registry's seal/decode/version_id derivation is the same construction as the four existing kinds; a memory ref pasted into another dimension fails closed, and a foreign ref pasted into memory fails closed. |
| **NFR6** | **Determinism** | The same target at the same revision emits the same IR memory defaults; the same spec resolves to the same memory field bytes and the same `config_hash` every time. |
| **NFR7** | **Strategy closure** | The builtin strategy set is closed for the shipped strategy-set version; a sixth strategy without a version bump fails a cardinality assertion rather than silently changing what a stored strategy name means (mirroring `TaxonomySize`). |
| **NFR8** | **Boundary containment** | No memory field, ref, or operator is expressible as a context construct or vice versa; a static check keeps `DimMemory` and `DimContext` disjoint in override, resolution, and transform dispatch. |
| **NFR9** | **One spine, two origins** | Exactly one resolve path, one transform entry point, and one gate serve both the authored and the proposed memory change; asserted by enumerating the apply path, not by review. A second path would be a second place for the refusal to be wrong. |
| **NFR10** | **The refusal is stated before the choice, not after it** | The authoring surface renders the M20 no-materializer boundary *before* a strategy is selected, sourced from the same coverage fact the transform refuses from — never a second hand-written sentence that can drift from the engine's behaviour. Asserted in the console tests. |
| **NFR11** | **A refusal is never rendered as success** | No surface presents a refused memory change as applied, delivered, partially applied, or improved, and `refused` renders as its own state, distinct from `failed` and `pending`. This is the one presentation the axis exists to keep honest: at M20 the refusal *is* the outcome, so a UI that softens it is the whole lie. |
| **NFR12** | **Clearing is byte-exact** | Selecting a strategy and then clearing it reproduces the prior `config_hash` byte-identically, and `none` hashes identically to cleared — so a user can back out of an authored memory change with no residue in the hash. |

## 8. System design summary

### 8.1 Where memory sits on the optimizer spine

```mermaid
graph LR
  subgraph Model["Modeled end-to-end (ships at M20)"]
    IR[IRNode.memory = none default<br/>emit.go] --> OV[NodeOverride.MemoryRef<br/>spec.go:183]
    OV --> RS[resolveNode + Dimensions()<br/>resolve.go:67,154]
    RS --> RN[ResolvedNode.memory omitempty<br/>resolved.go:46]
    RN --> CH[(config_hash<br/>structural, auto-participates)]
    REG[(memory_entry<br/>Kind=memory · registry.go:57)] -.version_id.-> OV
    OP[OpMemoryPolicy<br/>operator.go:34 · catalog · prior] -.proposes.-> OV
  end
  CH --> TF{Transform dispatch<br/>rewrite.go:54 / rewrite_span.go:59}
  TF -->|MemoryRef != none| REFUSE[[unsafeRewrite<br/>refuseMemory — first-class]]
  TF -. deferred .-> REWRITE[memory codemod<br/>future runtime owner]
  CH --> EVAL[P4 harness<br/>axis-agnostic: config_hash + Trace]
  EVAL --> METRICS[memory_hit_rate ↑ · staleness ↓ · tokens ↓<br/>metricset.go:98]
```

The diagram's asymmetry is the phase: everything left of the transform **ships**; the transform
**refuses**; the rewrite is **deferred**. The eval/metrics path is drawn because it is *ready* — the
harness is axis-agnostic, so the moment a memory codemod is safe, a memory variant is scorable with zero
eval change — but no memory diff reaches it at M20.

### 8.2 Memory is not context — the boundary the whole phase rests on

```
CONTEXT (P16 · DimContext)            MEMORY (P17 · DimMemory)
─────────────────────────            ────────────────────────
scope:   within ONE call             scope:   ACROSS invocations / sessions
question:how is THIS message          question:what does the agent CARRY
         list assembled?                       from prior turns?
codebase:call-site message build     codebase:a store read/written BETWEEN turns
signal:  (P16 context metrics)       signal:  memory_hit_rate / staleness (metricset.go:98)
classifier: —                        classifier: MemoryManagement pattern 8, confirm signal
                                                 "memory read/write against a store between turns"
                                                 (taxonomy.go:108) — the "between turns" clause IS the line
```

The pattern classifier already draws this line; P17 does not invent it, it *honors* it. Folding memory
into `DimContext` would let a cross-session concern be expressed as a within-call one, which is exactly the
confusion the taxonomy's `capability` vs within-call split exists to prevent (decisions.md D2).

### 8.3 The eight-step "add an axis" checklist, applied

| Step | Spine anchor | What P17 adds |
|---|---|---|
| 1. Dimension const | [`spec.go:42`](../../internal/variantspec/spec.go) | `DimMemory Dimension = "memory"` |
| 2. NodeOverride field | [`spec.go:183`](../../internal/variantspec/spec.go) | `MemoryRef string omitempty` (+ `isEmpty`/`Refs`/`Validate`) |
| 3. Resolve + Dimensions() | [`resolve.go:67,154`](../../internal/variantspec/resolve.go) | a `resolveNode` memory block + a `DimMemory` case |
| 4. ResolvedNode field | [`resolved.go:46`](../../internal/variantspec/resolved.go) | additive `omitempty` memory field, auto-hashed by `confighash` |
| 5. Registry Kind | [`registry.go:57`](../../internal/registry/registry.go) | `KindMemory` + `memory.go` (register/resolve) + `memory_entry` table |
| 6. IR field + frontend | [`emit.go`](../../internal/discovery/emit.go) + `extract.go` | `IRNode.memory` default `none` + a discovery frontend |
| 7. Per-dimension rewriter | [`rewrite.go:54`](../../internal/transform/rewrite.go) + `rewrite_span.go:59` | `refuseMemory` → `unsafeRewrite` in **both** engines (the hard part — here, a refusal) |
| 8. Operator | [`operator.go:34`](../../internal/proposal/operator.go) + `catalog.go` + `gain.go:8,26` | `OpMemoryPolicy` kind, catalog row, prior + order hint |

Step 7 is "the hard part" everywhere; for P17 it is a *refusal*, which is the honest version of the hard
part when the rewrite is not yet safe (§8.4 D4).

### 8.4 Decisions, with what was rejected

| # | Decision | Rejected alternative | Why (八级法则) |
|---|---|---|---|
| **D1** | **A new registry `Kind` `memory` + `memory_entry` table** | Store strategies in the existing `context_entry` table under a subtype flag | **L5 不可演进 + L6.** A shared table with a discriminator overloads a frozen storage shape and lets a context ref and a memory ref collide in one namespace; a distinct Kind makes a cross-dimension paste fail closed. A new table is a one-way door chosen deliberately. |
| **D2** | **A new `DimMemory` Dimension, disjoint from `DimContext`** | Model memory as a context policy / a `DimContext` sub-mode | **L5 不可演进 + strategy.** Memory persists across invocations; context is within one call. The classifier already separates them (`MemoryManagement` confirm signal *"between turns"*, `taxonomy.go:108`). Collapsing them erases a distinction the taxonomy is built on and can never be cleanly un-collapsed. |
| **D3** | **Memory field is additive `omitempty`; `none` ≡ absent** | Make the memory field always-present like `ContextParams`'s `{}` | **L2 稳定 + L5.** An always-present field changes the canonical bytes of every existing node and breaks the P0 golden `config_hash` vectors. `omitempty` with nil-when-empty is the expand-contract rule the registry already lives by (cf. P10 D-1.4): absence stays byte-compatible; presence changes the hash iff the strategy changes. |
| **D4** | **Interim refusal: `MemoryRef ≠ none` → typed `unsafeRewrite`, first-class** | Silently drop an un-applicable memory override, or block the whole spec at resolve | **L1 安全 + L2 稳定.** Silent-drop is the worst outcome — the user believes a change applied that did not, and lineage lies. Blocking at resolve throws away the modeling and hashing that *are* safe. Refusing only at transform, with a typed error, keeps model/hash/propose working and makes the boundary observable and testable (it can go red). Mirrors `refuseSkills`/`refuseContext` ([`rewrite.go:388`,`:417`](../../internal/transform/rewrite.go)). |
| **D5** | **Closed, versioned builtin strategy set; each declares a `ParamsSchema`** | Free-form strategy strings with open params | **L5 不可演进 + L2.** An open set makes a stored strategy name un-interpretable if the vocabulary drifts and makes params unvalidatable. A closed set with a cardinality assertion (like `TaxonomySize`) fails loudly on an unversioned sixth strategy; a `ParamsSchema` rejects a malformed entry at seal time rather than at run time. |
| **D6** | **`OpMemoryPolicy` proposes; verification decides; no win claimed while refused** | Ship the operator as if a memory change were applicable and let it report gains | **L1 honesty + core principle.** *Diagnosis proposes, verification decides.* While the transform refuses, a memory proposal cannot be verified, so it cannot be a win. Reporting one would be claiming an undelivered capability. The operator is catalogued but dormant until the rewrite lands — like `OpMerge` is reserved. |

### 8.5 Data-model additions

```
memory_entry            = registry row, Kind=memory: { version_id = sha256(envelope), name,
                          strategy ∈ {none,scratchpad,summary-buffer,vector-recall,entity-memory},
                          params (schema-validated), title, description, created_at }
NodeOverride.MemoryRef  = memory version_id, omitempty                          // spec.go:183
ResolvedNode.<memory>   = resolved (strategy, params), additive omitempty        // resolved.go:46
IRNode.<memory>         = discovered default, omitempty, = none                   // emit.go
DimMemory               = "memory"                                               // spec.go:42
OpMemoryPolicy          = "memory_policy_switch" OperatorKind + catalog + prior   // operator.go:34
```

No live store, no runtime, no second cost model. `memory_entry` is the only new table; everything else is
an additive field on an existing structure, which is why `config_hash` absorbs it for free.

## 9. Design by role lens

**System Designer (co-lead) — *one axis, added the one canonical way, with the boundary decided once.***
The value of P17 to the platform is not the memory feature; it is a *second* worked example of the
eight-step checklist (§8.3), this time for an axis whose rewrite is not safe, so the next axis author has a
template for the honest-refusal case as well as the model/prompt success case. Two one-way doors are the
substance. The **new Kind and table** (D1): a discriminator on `context_entry` would have been less code
and is exactly the trap — it welds two dimensions into one namespace forever. The **memory-vs-context
boundary** (D2) is the load-bearing call, and it is not ours to invent: the classifier already encodes it
in the `MemoryManagement` confirm signal *"between turns"* ([`taxonomy.go:108`](../../internal/patternclassifier/taxonomy.go)).
Writing it down, tagged L5, is what stops it being re-litigated when P16 and P17 sit next to each other in
the console. Everything else follows the spine mechanically, and mechanically is the point.

**AI Engineer (co-lead) — *the strategies are hypotheses; the harness is the judge.***
The five builtin strategies are a deliberate spread across the real design space: `none` (identity),
`scratchpad` (ephemeral working notes), `summary-buffer` (a rolling summary that trades fidelity for
tokens), `vector-recall` (embedding-backed retrieval of prior turns), `entity-memory` (structured key
facts). Each is a *hypothesis* about how a stateful agent should carry state, and the whole point of making
memory a Dimension is that we stop arguing about them and let `memory_hit_rate` / `staleness` / token count
decide — the metrics the classifier already defines ([`metricset.go:98`](../../internal/patternclassifier/metricset.go)),
not a new number I invent to flatter a strategy I like. The operator `OpMemoryPolicy` proposes a swap
against a `stale_read` or `contradictory_memory` bottleneck, and its prior is a coarse ordering hint, never
a result. The honesty I must hold: at M20 a memory proposal **cannot** be a verified win, because the
transform refuses. I would rather ship a proposable-but-unverifiable axis and say so than ship a number I
cannot stand behind.

**Backend (support) — *the fifth registry looks exactly like the first four.***
`memory_entry` is `model_entry` with a different Kind: the same content-addressed `version_id =
sha256(envelope)`, the same seal/decode, the same fail-closed on a cross-dimension paste
([`registry.go:51`](../../internal/registry/registry.go) — the Kind is hashed into the id precisely so a
ref cannot resolve in the wrong registry). `MemoryRef` on `NodeOverride` and the memory field on
`ResolvedNode` are `omitempty` and nil-when-empty, so a node that binds no memory serializes byte-for-byte
as it did before the field existed — the same expand-contract discipline that let P10 add `bindings`
without touching a golden vector. The one thing I must not do is make the field always-present: that would
rewrite every existing node's canonical bytes and break P0's `config_hash` contract (D3, NFR1).

**QA Engineer (support) — *the two guarantees that matter are both machine-decidable, and both can go red.***
First, the **refusal**: a resolved node with `MemoryRef ≠ none` must return a typed `unsafeRewrite` from
*both* the AST and the span engines (FR11, FR12) — I assert the error type and that no diff is produced,
and I assert it is not `ErrUnknownNode` or `ErrInvalidSpec`, because "we will not yet" must not read as
"you asked for nonsense." A refusal that cannot be made to fail is decoration; the canary is a node
constructed to carry a real memory strategy that must come back refused. Second, **`none` ≡ absent**: a
`none` node and a no-memory node must canonicalize to identical bytes and the P0 golden vectors must
reproduce (FR4, NFR1) — a single byte of drift there orphans every keyed row. Both are pass/fail, neither
is a matter of reading the code.

**Product Designer (support) — *a memory strategy is a choice a person makes, so it must be legible.***
Five wire names (`none` … `entity-memory`) are the *code* layer; each strategy also carries a human title
and a description (FR6), and its params are a readable surface driven by the `ParamsSchema`, not a raw blob
— three separate layers kept separate, the same discipline applied everywhere the interface meets a data
entity. When the console eventually offers a memory strategy, the user should be choosing between "a rolling
summary" and "structured key facts" with their trade-offs visible, not between two opaque enum values. And
because the transform refuses today, the one interaction that must be honest now is the *proposal* surface:
a proposed memory change must be shown as proposed-and-not-yet-applicable, never as a result — the UI must
not let the refusal read as a silent success.

## 10. Dependencies

**Requires**
- **P1** — discovery and the Workflow IR, into which the memory default is emitted (`emit.go` `IRNode`).
- **P2** — Variant Spec resolution, the `config_hash` contract a new `ResolvedNode` field auto-joins, and
  the transform engine whose dispatch this phase extends with a refusal.
- **P4** — the axis-agnostic eval harness and scoring (ready to score a memory variant with no change; not
  exercised for memory at M20 because the transform refuses).
- **P4.5** — the diagnosis taxonomy, proposal catalog, and operator priors this phase adds a row to.
- **P16** — context assembly, the sibling within-call axis memory is kept disjoint from.

**Unblocks**
- **A future memory-runtime phase** — the modeling, registry, hash participation, and proposal are in
  place; that phase owns only the live store and the call-site codemod that lifts the refusal (§14 Q1).
- **Memory-aware diagnosis** — once a memory variant can be scored, the classifier's memory metrics become
  an actionable dial rather than a read-only gauge.

## 11. Risks & mitigations

| Risk | Owner | Mitigation |
|---|---|---|
| A `MemoryRef` override is silently dropped instead of refused | Backend + QA | FR11/FR12 + NFR3: a typed `unsafeRewrite` in both engines, asserted by a canary node that must come back refused; no code path emits a memory edit. |
| `none` drifts from "absent" and breaks the config-hash golden | Backend + QA | FR4/NFR1: `none` canonicalizes to identical bytes as no-memory; the P0 golden vectors are the guard and must reproduce. |
| Memory is conflated with context in override, resolve, or transform | System Designer | D2/NFR8: disjoint Dimension, disjoint Kind, disjoint dispatch; a static check keeps `DimMemory` and `DimContext` from cross-expressing. |
| The operator reports a memory "win" that was never verified | AI Engineer | FR15/D6: a proposal carries no authority; while the transform refuses, a memory proposal is surfaced as refused, never scored. |
| A sixth strategy is added without a version bump and silently reinterprets a stored name | Backend | NFR7: a cardinality assertion (like `TaxonomySize`) fails loudly; the builtin set is closed per strategy-set version. |
| A memory ref resolves in the wrong registry | Backend | NFR5: the Kind is hashed into the version_id, so a cross-dimension paste fails closed, as it does for the four existing kinds. |
| The phase is read as delivering scored memory optimization | Sales-adjacent / System Designer | §1/§2/G-line honesty: M20 is "modeled + refused," not "optimized"; no scored memory win is claimed anywhere. |

## 12. Rollout & test strategy

**Wave 20a — the store and the modeled, refused Dimension.** The `memory` registry Kind, the
`memory_entry` table, the five builtin strategies with their `ParamsSchema`, `DimMemory`, the
`NodeOverride.MemoryRef`, the resolve block and `Dimensions()` case, the additive `ResolvedNode` and IR
fields, and the first-class `refuseMemory` in both engines. Ends when a spec can carry a memory strategy,
resolve, hash reproducibly, and be **refused** at transform with a typed error — and a `none` node hashes
byte-identically to a no-memory node.

**Wave 20b — the operator and the metric wiring.** `OpMemoryPolicy` as an `OperatorKind`, its catalog row,
its prior and verify-order hint, and the mapping from a memory bottleneck signal (`stale_read` /
`contradictory_memory`) to a proposed strategy swap. Ends when the diagnosis engine can propose a memory
change against a memory bottleneck, and that proposal is honestly surfaced as refused-not-scored.

**How correctness is proven.**
1. **Refusal totality** — a resolved `MemoryRef ≠ none` node returns a typed `unsafeRewrite` in the AST
   engine and in the span engine; no diff is produced; the error is distinct from `ErrUnknownNode` /
   `ErrInvalidSpec`.
2. **Identity of `none`** — a `none` node and a no-memory node canonicalize byte-identically; the P0
   golden `config_hash` vectors reproduce unchanged.
3. **Hash participation** — two specs differing only in memory strategy (or params) hash differently; one
   differing only in non-identity-bearing authoring order hashes the same.
4. **Registry uniformity** — a memory version_id resolves only in the memory registry; a memory ref pasted
   into another dimension, and a foreign ref pasted into memory, both fail closed.
5. **Strategy closure** — a name outside the five builtins does not resolve; a cardinality assertion fails
   on an unversioned sixth strategy; a params-schema violation is rejected at seal.
6. **Dimension iteration** — `Dimensions()` reports `DimMemory` iff a memory override is set, and the
   transform iterates it exactly as the other four.
7. **Operator dormancy** — `OpMemoryPolicy` is catalogued with a prior; a proposal it generates resolves
   and hashes but is refused at transform, so it produces no verified result at M20.
8. **Boundary disjointness** — no memory construct is expressible as a context construct or vice versa.

## 13. Success metrics & acceptance criteria (M20 exit checklist)

- [ ] **A1.** `DimMemory` is a member of the closed `Dimension` enum and `Dimensions()` reports it iff a
      memory override is set (G1, FR7).
- [ ] **A2.** A `memory` registry Kind and a `memory_entry` table exist, content-addressed identically to
      the four existing kinds (G2, FR1, NFR5).
- [ ] **A3.** Exactly five builtin strategies (`none`, `scratchpad`, `summary-buffer`, `vector-recall`,
      `entity-memory`) resolve; a name outside the set does not; a sixth without a version bump fails a
      cardinality assertion (G3, FR2, NFR7).
- [ ] **A4.** Each strategy declares a `ParamsSchema`; a params violation is rejected at seal (FR3).
- [ ] **A5.** A `none` node canonicalizes byte-identically to a no-memory node, and the P0 golden
      `config_hash` vectors reproduce unchanged (G4, FR4, NFR1).
- [ ] **A6.** Two specs differing only in memory strategy or params produce different `config_hash`es
      (G5, FR9, NFR2).
- [ ] **A7.** `NodeOverride.MemoryRef` is additive/`omitempty` and participates in `isEmpty`/`Refs`/
      `Validate`; a no-memory node serializes byte-identically to a pre-P17 node (FR8).
- [ ] **A8.** The IR carries a per-node memory field defaulting to `none`, emitted by a discovery frontend
      (G11, FR10, NFR6).
- [ ] **A9.** A resolved node with `MemoryRef ≠ none` is **refused** at transform with a typed
      `unsafeRewrite` naming node + dimension + reason, and produces **no** diff (G7, FR11, NFR3, NFR4).
- [ ] **A10.** The refusal occurs in **both** the AST and the tree-sitter span engines (FR12, NFR3).
- [ ] **A11.** A spec carrying a `MemoryRef` still resolves and still produces a stable, reproducible
      `config_hash` — only the transform refuses (G8, FR13).
- [ ] **A12.** Memory is never expressible as a context construct or vice versa (G6, NFR8).
- [ ] **A13.** `OpMemoryPolicy` is catalogued with a prior and a verify-order hint (G9, FR14).
- [ ] **A14.** An `OpMemoryPolicy` proposal is decided by verification, is surfaced as refused-not-scored
      at M20, and no memory win is reported anywhere (G9, FR15, D6).
- [ ] **A15.** The memory improvement signal is the classifier's existing metric set with no new metric and
      no taxonomy change (G10, FR16).

## 14. Open questions

1. **Who owns the memory codemod and the live store that lift the refusal.** The rewrite is deferred and
   unowned in this document. It requires a reintroduced memory runtime (the sweeper and platform wiring
   were removed at the pivot, [`launch.go:6`](../../internal/launch/launch.go)) and a call-site rewriter
   that can wire a store read/written between invocations. Named as a **future memory-runtime phase**;
   P17 deliberately ships the modeling and the refusal only. Analogous to how [P3](P3-context-skills-sandbox.md)
   owns the context rewrite that `refuseContext` defers to.
2. **Whether `vector-recall`'s embedding reference is a memory param or a cross-reference to the model
   registry.** An embedding model is a registered thing; `vector-recall` could carry an embedding
   `version_id` in its params rather than an inline name. Leaning toward a cross-reference for the same
   reason bindings reference version_ids, but deferred until the strategy's params schema is exercised
   against a real target.
3. **Whether `entity-memory`'s entity-key set is authored or discovered.** A structured-facts strategy
   needs to know which entities to track; whether that is an author-supplied param or something a discovery
   frontend can infer from the target is open, and interacts with Q1's runtime owner.
