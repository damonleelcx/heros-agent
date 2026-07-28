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

---

## D-3 — The wiring rewriter's admitted operation is a **transposition of two adjacent sibling statements**

**Problem.** 15a/15b left source materialization ABSENT and every wiring change refused. Closing that
gap means writing a rewriter that changes CONTROL FLOW rather than a value — the one thing this engine
has never done, and the thing ADR-001 names as its top risk. The scope of that rewriter is a one-way
door: a stored transform row that says a reorder was applied constrains what every future reader — a
reviewer, a rollback, a re-run — believes happened to the file. We must choose: (A) a general "move a
call to a new position" rewriter, which also enables merge and prune materialization, or (B) a
**transposition of two ADJACENT statements** and nothing else.

**Decision.** **(B): adjacent transposition only.** The engine materializes a wiring change if and only
if the spec's order differs from the discovered order by exactly one adjacent swap, the edge set is
unchanged, and the two call sites are consecutive sibling statements that bind nothing the other reads.
Merge and prune remain refused. Non-adjacent moves remain refused. Two swaps at once remain refused.

**Why this is the appropriate design.** A transposition of two whole-line blocks has an invariant no
other source edit in this repository has: **the output is the input's lines, reordered**. Same count,
same multiset. That is checkable in one comparison, it cannot be subtly wrong, and it means the review
question collapses from "did the codemod rewrite this correctly" to "should these two statements run in
the other order" — which is exactly the question the typed-contract gate and verification already
answer. A general move has no such invariant: it must reconstruct indentation, re-anchor comments,
decide what travels with the statement, and prove the destination scope binds the same names. Each of
those is a place to be wrong in a way that still compiles.

**Alternatives + decision point.** (A) has real reach — it would materialize merge and prune too, making
the whole axis applicable rather than one slice of it. Rejected on **safety (L1) + stability (L2) over
operator reach (L8)**: an arbitrary move is the ADR-001 top risk with no cheap invariant behind it, and
the wrong-but-compiling version of a control-flow edit degrades behaviour invisibly — the failure mode
with no downstream net. Under the eight-level law, reach never buys down a safety guarantee. The narrow
slice also fails HONESTLY: a pair that is not a clean transposition is refused by name, so a user learns
which condition blocked it instead of receiving a diff nobody can vouch for.

**Effect.** A stored transform that materialized wiring always means "these two adjacent statements
changed places, nothing else in the file moved". If a general mover is ever wanted, it is a NEW
capability decided with its own risk in hand, never a silent widening of this one.

---

## D-4 — The permutation invariant is a **new edit class**, never a loosening of the minimality gate

**Problem.** `gateMinimal` enforces that no rewrite changes a file's line count
([`engine.go`](../../../internal/transform/engine.go)): a rewriter may not emit a newline, and may not
replace a multi-line expression with a single-line one. That rule is what makes "only the targeted lines
changed" checkable at all, and it is what keeps `TouchedDimension.Line` valid in both the original and
the transformed file. A statement swap moves lines by construction, so it does not fit through the gate
as written. Two ways out: (A) relax the rule so a block move is allowed, or (B) introduce a **separate
edit class** with its own, stricter invariant.

**Decision.** **(B): a separate class.** A wiring swap is a distinct edit kind. Value rewrites keep the
old invariant unchanged. The swap class asserts something STRONGER: the file's line count is unchanged
**and** the multiset of its lines is unchanged (a permutation) **and** every changed line lies inside
one of the two swapped blocks.

**Why this is appropriate.** Relaxing the shared rule would remove the check from every rewriter that
exists today and every one added later, to serve one new caller — the definition of an越级 trade (an L2
guarantee spent for an L8 convenience). Keeping the classes separate means a future value rewriter that
tries to emit a newline is still caught by the original rule, and a future wiring rewriter that tries to
edit a line while moving it is caught by the new one. Two rules, each exactly as strict as its class
allows, is also what makes the failure messages honest: a violation names which invariant broke.

**Alternatives + decision point.** (A) is fewer lines of code and one rule instead of two. Rejected on
**stability (L2) over implementation cost (L8)**: a safety check that is weakened for one case protects
nothing afterwards, and the weakening is invisible — nothing fails, the gate simply stops catching the
class of bug it was written for.

**Effect.** The permutation assertion is the thing a reviewer can trust without reading the codemod: if
it holds, the change cannot have altered a single character of the file's content.

