# P15 — Recorded decisions (System Designer, §1)

Two contracts that must be fixed **before any `mergeOp` ships or any adapter reaches a customer diff**,
because each is a one-way door. `OpMerge` is a wire value stored on a proposal row
([`operator.go:30-32`](../../../internal/proposal/operator.go): *"the wire value stored on a proposal
row, so it is a stable string"*), so its **semantics** become a contract the moment the first row names
it. An inserted adapter becomes generated source in a shipped diff, so its **posture** becomes a contract
the moment the first diff carries one. This file records both.

The neither-here decisions — that the axis lives in `Order`/`Edges` with no new hashed field, and that no
new eval metric is added — are **not** one-way doors (they add nothing that must later be retracted) and
live in [`design.md`](design.md) Decisions 2 and 6.

---

## D-1 — `OpMerge` fuses **only two adjacent nodes**, survivor subsumes, edges rewired mechanically

**Problem.** `OpMerge` exists as a reserved constant with a gain prior and no implementation
([`operator.go:46`](../../../internal/proposal/operator.go),
[`gain.go:20,29`](../../../internal/proposal/gain.go)); it is absent from `DefaultCatalog()`. P15
implements it. Its semantics are a one-way door: a proposal row storing `OpMerge` today constrains what
every future reader — the compare view, the verified-delta ledger, a re-run months later — believes a
merge *did*. We must choose the **scope** of a merge: (A) fuse any subset of nodes the redundancy signal
flags, across gaps in the order, or (B) fuse only an **adjacent pair**, with one node designated the
survivor and the other's edges rewired through it.

**Decision.** **(B): adjacent-pair only.** `mergeOp.Propose` picks the adjacent `(survivor, absorbed)`
pair the `SignalRedundantNode` names, derives a candidate that **drops `absorbed` from `Order`** and
**rewires its edges through `survivor`** (inbound edges retarget the survivor, outbound edges re-source
from it), and inherits every other per-node override unchanged. A chain of three redundant calls merges
**pairwise across proposal iterations**, never in one n-ary step.

**Why this is the appropriate design.** A merge is scored, then possibly shipped as a diff a human
reviews. The two failure modes are a **non-building or behavior-changing fusion** the coherence gate
admitted but nobody could follow, and a **merge whose diff a reviewer cannot read in one screen**.
Adjacency closes both: the resulting `Edges` are a **mechanical rewire** (retarget in, re-source out),
not a re-plan, so the candidate is a local edit whose diff is legible and whose data-flow change the
typed-contract gate checks exactly. The gate ([`rearrange.go:52`](../../../internal/variantspec/rearrange.go))
validates *any* ordering, including a wild one — so the gate alone does not bound reviewability; the
operator's scope must.

**Alternatives + decision point.** (A), arbitrary-subset merge, has more reach: one operator collapses
three scattered calls at once. Rejected on **stability (L2) + safety (L1) over operator reach (L8)**: a
non-adjacent or many-to-one merge changes data flow in ways the gate can admit but a reviewer cannot
trace, and its `Edges` become an authored re-plan rather than a derivation — an L2/L1 cost (a graph whose
correctness a human cannot verify) bought for the L8 convenience of a wider operator. Under the
eight-level law that trade is forbidden. Pairwise-across-iterations reaches the same three-node outcome
by a sequence of individually-reviewable, individually-gated, individually-scored steps — which is also
the honest one, because each intermediate merge is *separately verified* rather than bundled into one
unfalsifiable claim.

**Effect.** A stored `OpMerge` row always means "these two adjacent nodes were fused, one survived, its
neighbour's edges moved to it" — a fixed, legible meaning. If a native n-ary merge is ever wanted, it is
a *new* operator kind decided with its reviewability cost in hand, never a silent widening of `OpMerge`'s
meaning under rows that already assumed the narrow one.

---

## D-2 — A reconciling adapter is an **explicit source node in the same diff**, never a runtime shim

**Problem.** When a reorder or merge produces a producer→consumer mismatch the typed-contract catalog can
bridge, the verdict is `adapted` and an adapter reconciles it
([`adapter.go:61-84`](../../../internal/typedcontract/adapter.go)). *How* the adapter exists is a one-way
door: the moment the first `adapted` diff ships to a customer, the posture — is the adapter **visible
generated source** or an **invisible runtime coercion**? — is a contract every future review depends on.
The spec already carries an `InsertedAdapter` node type ([`spec.go:213-229`](../../../internal/variantspec/spec.go))
and `withAdapters` already records and rewires through it ([`rearrange.go:66-89`](../../../internal/variantspec/rearrange.go)),
so both postures are *reachable*; P15 must fix which one is the contract.

**Decision.** **Explicit source, in the same reviewable diff.** An admitted adapter is recorded as an
`InsertedAdapter` node carrying its own `InSchema`/`OutSchema` `io_contract`, its edge rewired
producer→adapter→consumer, and it is **materialized as generated source** that appears in the spec's node
list and its diff against the parent. There is **no** runtime coercion path.

**Why this is appropriate.** The platform's core principle is *a deterministic source codemod, never a
runtime shim* ([`README`/`project.md`](../../project.md)), and P10 fixed the companion rule: *an
indirection never hides a value from review — the resolved values ship in the same diff, or the
transformation is rejected.* An adapter is precisely an indirection that reconciles two nodes' data; a
coercion applied at runtime would be a data transformation a reviewer approving the reorder never saw.
Recording it as an explicit node with its own contract makes the bridge auditable and keeps the codemod
the single source of truth for what the transformed program does.

**Alternatives + decision point.** A runtime coercion shim is less diff noise and adds no synthetic node
to the graph. Rejected on **safety (L1) over UX/convenience (L3)**: a hidden coercion is a value change
outside review, which is the exact failure the explicit-`InsertedAdapter` shape exists to prevent, and
"less diff noise" is an L3 convenience that cannot buy down an L1 review guarantee. The admissibility rule
travels with this: an adapter is admitted **only** if its `InSchema` is satisfied by the producer and its
`OutSchema` satisfies the consumer ([`adapter.go:73-82`](../../../internal/typedcontract/adapter.go)), so
the visible adapter also provably drops nothing required — a hidden shim could silently drop a field and
nobody would see it in the diff.

**Effect.** Every `adapted` verdict yields a diff a reviewer can read end to end: the reorder, plus the
bridging adapter as generated source with its declared contract. A reorder that *needs* a bridge the
catalog cannot supply is rejected with no runnable spec (D-1's sibling in [`design.md`](design.md)
Decision 3), never quietly reconciled. Determinism is part of the contract: the adapter node id is
derived from `(from, to, kind)` and the catalog match order is fixed
([`rearrange.go:91-93`](../../../internal/variantspec/rearrange.go),
[`adapter.go:61-71`](../../../internal/typedcontract/adapter.go)), so the same reorder yields the same
adapter and the same `config_hash` on every evaluation.
