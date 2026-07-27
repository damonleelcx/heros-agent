# Design — P17: Memory Strategy Optimization

Product rationale: [`../../../docs/prd/P17-memory-strategy-optimization.md`](../../../docs/prd/P17-memory-strategy-optimization.md).
Pre-code one-way-door contracts: [`decisions.md`](./decisions.md) (D1–D6).
Sibling axis kept disjoint: [`../p16-context-strategy-optimization/`](../p16-context-strategy-optimization/).

## Context

Memory is **absent** from the optimizer spine. The Dimension enum is closed and holds four members —
`DimModel`, `DimPrompt`, `DimSkills`, `DimContext` ([`internal/variantspec/spec.go:42`](../../../internal/variantspec/spec.go))
— and node ordering is a fifth structural axis. Memory is none of them. It survives in the codebase only as
a **detectable pattern** (`MemoryManagement`, pattern 8, [`taxonomy.go:29`](../../../internal/patternclassifier/taxonomy.go),
metrics at [`metricset.go:98`](../../../internal/patternclassifier/metricset.go)) and a **directory name**
(`MemoryDir`, [`agentlayout/layout.go:13`](../../../internal/agentlayout/layout.go)). The classifier can
measure how well a target agent's memory works; nothing can change it and re-measure. The old "memory
sweeper" was removed at the pivot ([`launch.go:6`](../../../internal/launch/launch.go)).

This is greenfield, so the design is not a feature negotiation — it is the **eight-step "add an axis"
checklist**, applied faithfully, with one honest twist. Two constraints shape everything:

- **Memory must be added the one canonical way.** The spine's value is its uniformity: an axis lands its
  effect in `ResolvedConfig` → `config_hash` and the axis-agnostic harness scores it with no change. P17's
  job is to be a *second worked example* of the checklist, not a bespoke subsystem.
- **The rewrite is not safe, and the repo already has an honest answer for that.** Only `model` and
  `prompt` emit edits; `skills` and `context` **refuse** with a typed `unsafeRewrite`
  ([`rewrite.go:388`,`:417`](../../../internal/transform/rewrite.go)). Memory is harder still — a stateful
  store read and written *between* invocations — so the honest interim is the same refusal, made
  first-class.

The resolution is not a compromise: model, resolve, hash, and propose memory end-to-end, and **refuse** at
transform until a memory runtime and a call-site rewriter exist. Modeling and materialization are
sequential, not opposed.

## Decision 1 — A new registry `Kind` `memory`, with its own `memory_entry` table

Memory strategies live in a **new** content-addressed registry Kind alongside the four existing ones
([`registry.go:57`](../../../internal/registry/registry.go)), backed by a new `memory_entry` table.

**Alternative rejected — store strategies in `context_entry` under a subtype discriminator.** Less code,
one fewer table, and it reuses a working seal/decode path. Rejected on **L5 不可演进 + L6 不可扩展**: a
shared table with a discriminator welds two dimensions into one namespace forever, so a context ref and a
memory ref can collide on one id and a ref pasted into the wrong dimension can *resolve* instead of failing
closed. The Kind is hashed into the `version_id` precisely so that cannot happen ([`registry.go:51`](../../../internal/registry/registry.go));
a distinct Kind preserves that guarantee. A new table is a one-way door, chosen deliberately rather than
stumbled into. (Full record: [decisions.md D1](./decisions.md).)

## Decision 2 — A new `DimMemory` Dimension, disjoint from `DimContext`

Memory is its own Dimension. It is **not** a context policy, a `DimContext` sub-mode, or a context param.

**Alternative rejected — model memory as a mode of `DimContext`.** Both concern "what the model sees," so
one Dimension feels economical. Rejected on **L5 不可演进 + strategy**: memory persists **across
invocations and sessions**; context assembly is how a **single** call builds its message list. The
codebase already draws this line — `MemoryManagement`'s confirmation signal is *"memory read/write against
a store between turns"* ([`taxonomy.go:108`](../../../internal/patternclassifier/taxonomy.go)), and the
*between-turns* clause is the boundary; context, by contrast, is not an argument at all but how the
surrounding code builds the message list ([`refuseContext`, rewrite.go:417](../../../internal/transform/rewrite.go)).
Collapsing them lets a cross-session concern masquerade as a within-call one, and a merged Dimension can
never be cleanly un-merged once specs and hashes depend on it.

| Axis | Scope | The question | Codebase locus |
|---|---|---|---|
| `DimContext` (P16) | within ONE call | how is *this* message list assembled? | call-site message construction |
| `DimMemory` (P17) | ACROSS invocations | what does the agent *carry* from prior turns? | a store read/written between turns |

(Full record: [decisions.md D2](./decisions.md).)

## Decision 3 — The memory field is additive `omitempty`; `none` is byte-identical to absent

`ResolvedNode`, `NodeOverride`, and `IRNode` each gain an additive, `omitempty`, nil/empty-when-unset
memory field. A node whose strategy is `none`, and a node with no memory field, canonicalize to **identical
bytes**.

**Alternative rejected — make the memory field always-present, like `ContextParams`'s `{}`.** It reads
more uniformly next to the always-present sibling maps ([`resolved.go:60`](../../../internal/variantspec/resolved.go)).
Rejected on **L2 稳定 + L5**: `config_hash` is P0's frozen contract with golden vectors that the live
producer must reproduce bit-for-bit, and every existing row is keyed by it. An always-present field changes
the canonical bytes of **every** existing node, so every golden vector breaks and every keyed row orphans.
The sibling maps are always-present because they *predate* the golden and their emptiness is part of the
frozen bytes; a **new** field's *absence* is what must stay byte-compatible, and absence is achieved by
`omitempty` with a nil-when-empty value — exactly the expand-contract rule that let P10 add `bindings`
without touching a vector (P10 decisions.md D-1.4). The hash then changes **iff** the memory strategy
changes.

| Field shape | Bytes of a no-memory node | Effect on the golden vectors |
|---|---|---|
| Always-present `none` | **change** | every vector breaks; every keyed row orphans |
| `omitempty`, nil-when-unset (**chosen**) | **unchanged** | vectors reproduce; hash changes iff strategy changes |

(Full record: [decisions.md D3](./decisions.md).)

## Decision 4 — Interim refusal: `MemoryRef ≠ none` is refused at transform with a typed `unsafeRewrite`

A resolved node carrying a memory strategy other than `none` is **refused** at transform — a typed
`unsafeRewrite` naming the node, the `memory` dimension, and the reason — in **both** the Go AST engine
([`rewrite.go:54`](../../../internal/transform/rewrite.go)) and the tree-sitter span engine
(`rewrite_span.go:59`). It is **never** silently dropped, and it **never** produces a diff. A spec carrying
a `MemoryRef` still **resolves** and still **hashes**; only the transform refuses.

**Alternative rejected (a) — silently drop an un-applicable memory override.** It would let a spec "succeed"
and produce a diff for its other dimensions. Rejected on **L1 安全**: silent-drop is the worst possible
outcome — the user believes a memory change applied that did not, and lineage records a `config_hash` that
does not correspond to any realizable behavior. The failure is discovered late, by someone puzzling over a
non-effect. **Alternative rejected (b) — block the whole spec at resolve.** Simpler to reason about, one
error path. Rejected on **L2 稳定 + L5**: it throws away the modeling, hashing, and proposal that *are*
safe and correct, and it makes the axis useless for the (large) fraction of its value that does not need
the codemod — comparison in lineage, proposal, and eventual scoring once the rewrite lands.

Refusing **only at transform, with a typed error**, is the repo's established honest pattern
([`refuseSkills`/`refuseContext`, rewrite.go:388,:417](../../../internal/transform/rewrite.go)): the axis
is modeled+resolvable+hashable, and solely the call-site materialization is deferred. The typed
`unsafeRewrite` ([`edit.go:90`](../../../internal/transform/edit.go)) is what makes the boundary
**observable and testable** — a caller can tell "we will not do this *yet*" from `ErrUnknownNode` /
`ErrInvalidSpec` ("you asked for something that does not exist"), and a test can make the refusal **go
red**. A refusal that cannot go red is decoration.

| Interim option | What "succeeds" | Failure mode | Level |
|---|---|---|---|
| Silent drop | the spec (minus memory) | user believes memory applied; lineage lies | rejected **L1** |
| Block at resolve | nothing | discards safe modeling/hash/propose | rejected **L2/L5** |
| **Typed refusal at transform (chosen)** | model + resolve + hash + propose | only materialization deferred; observable, testable | — |

(Full record: [decisions.md D4](./decisions.md).)

## Decision 5 — A closed, versioned builtin strategy set; each declares a `ParamsSchema`

The platform ships exactly five builtin strategies — `none`, `scratchpad`, `summary-buffer`,
`vector-recall`, `entity-memory` — as a **closed**, versioned set, each declaring a `ParamsSchema`. A sixth
strategy without a version bump fails a cardinality assertion (like `TaxonomySize`,
[`taxonomy.go:126`](../../../internal/patternclassifier/taxonomy.go)); a params violation is rejected at
seal.

**Alternative rejected — free-form strategy strings with open params.** Maximally flexible, no vocabulary
to maintain. Rejected on **L5 不可演进 + L2 稳定**: an open set makes a stored strategy name
un-interpretable the moment the vocabulary drifts (the exact problem `TaxonomyVersion` exists to prevent
for pattern labels), and open params cannot be validated, so a malformed strategy is discovered at run time
instead of at seal. A closed, versioned set with a `ParamsSchema` fails **loudly and early**: a cardinality
assertion catches an unversioned addition, and schema validation rejects a bad entry before it is stored.

The five are a deliberate spread across the design space, not an arbitrary list: `none` is the identity;
`scratchpad` is ephemeral working notes; `summary-buffer` trades fidelity for tokens via a rolling summary;
`vector-recall` is embedding-backed retrieval of prior turns; `entity-memory` is structured key facts. They
are *hypotheses* the harness will adjudicate, which is the whole reason memory becomes a Dimension.

(Full record: [decisions.md D5](./decisions.md).)

## Decision 6 — `OpMemoryPolicy` proposes; verification decides; no win is claimed while refused

A new operator `OpMemoryPolicy` ([`operator.go:34`](../../../internal/proposal/operator.go), catalog row,
prior + order hint at [`gain.go:8,26`](../../../internal/proposal/gain.go)) proposes a strategy swap against
a memory bottleneck (`stale_read` / `contradictory_memory`). Its prior is a coarse ordering hint, never a
result. While the transform refuses, a memory proposal is surfaced as **refused-not-scored**.

**Alternative rejected — ship the operator as if a memory change were applicable and let it report gains.**
It would make memory look "done." Rejected on **L1 honesty** and the platform's core principle —
*diagnosis proposes, verification decides.* While the transform refuses a memory rewrite, a memory proposal
**cannot be verified**, so it **cannot be a win**; reporting one would be claiming an undelivered
capability, and a bill or a dashboard built on it would be indefensible. The operator is catalogued but
**dormant** until the rewrite lands — the same posture as the reserved `OpMerge`
([`operator.go:46`](../../../internal/proposal/operator.go)). The improvement signal, when it is finally
scorable, is the classifier's **existing** metric set — `memory_hit_rate` up, `staleness` down, tokens
down ([`metricset.go:98`](../../../internal/patternclassifier/metricset.go)) — not a new number invented to
flatter the axis.

(Full record: [decisions.md D6](./decisions.md).)

## Interfaces sketch

```
Dimension enum (spec.go:42)          DimModel · DimPrompt · DimSkills · DimContext · DimMemory   ← +1

NodeOverride (spec.go:183)           { ModelRef, PromptRef, SkillRefs, ContextPolicy,
                                       MemoryRef string `json:"memory_ref,omitempty"` }          ← additive
ResolvedNode (resolved.go:46)        { …, memory `json:",omitempty"` (nil-when-unset) }          ← auto-hashed
IRNode (emit.go)                     { …, memory `json:",omitempty"` = "none" }                  ← discovered default

registry Kind (registry.go:57)       KindModel · KindPrompt · KindSkill · KindContext · KindMemory ← +1
memory_entry                         { version_id=sha256(envelope), name, strategy, params, title, desc }
builtin strategies (closed set)      none · scratchpad · summary-buffer · vector-recall · entity-memory
                                     each: ParamsSchema + title + description

transform dispatch (rewrite.go:54)   rewriters[DimMemory] = rewriteMemory  →  refuseMemory(...)  ← REFUSE
  refuseMemory → unsafeRewrite(node, "memory", "call-site materialization of a cross-invocation
                                store is deferred")                        (edit.go:90 *RewriteError)
  span engine (rewrite_span.go:59)   refuses identically                                          ← both engines

operator (operator.go:34)            OpMemoryPolicy = "memory_policy_switch"
  catalog.go:18                       row: {stale_read, contradictory_memory} → propose strategy swap
  gain.go:8,26                        operatorPrior[OpMemoryPolicy], verifyOrderHint[OpMemoryPolicy]

eval path                            axis-agnostic (config_hash + Trace) — READY, not exercised at M20
  improvement signal                  memory_hit_rate ↑ · staleness ↓ · eval_tokens_total ↓ (metricset.go:98)
```

## Risks

| Risk | Mitigation |
|---|---|
| A `MemoryRef` override is silently dropped rather than refused | Decision 4 — a typed `unsafeRewrite` in **both** engines; a canary node carrying a real strategy must come back refused; no path emits a memory edit. If that test cannot be made to fail, the guarantee is decoration. |
| `none` drifts from "absent" and breaks the P0 config-hash golden | Decision 3 — `none` canonicalizes to identical bytes as no-memory; the golden vectors are the guard and must reproduce. |
| Memory is conflated with context somewhere in override/resolve/transform | Decision 2 — disjoint Dimension, disjoint Kind, disjoint dispatch; a static check keeps `DimMemory` and `DimContext` from cross-expressing. |
| The operator reports a memory "win" that was never verified | Decision 6 — a proposal carries no authority; while the transform refuses, a memory proposal is surfaced as refused, never scored. |
| A sixth strategy is added without a version bump and reinterprets a stored name | Decision 5 — a cardinality assertion (like `TaxonomySize`) fails loudly; the builtin set is closed per strategy-set version. |
| A memory ref resolves in the wrong registry | Decision 1 — the Kind is hashed into the version_id, so a cross-dimension paste fails closed. |
| The phase is read as delivering scored memory optimization | The PRD, proposal, and decisions all state M20 is "modeled + refused," not "optimized"; no scored memory win is claimed anywhere. |
