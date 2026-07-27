# Design — P15: Workflow / Node-Wiring Optimization

Product rationale: [`../../../docs/prd/P15-workflow-wiring-optimization.md`](../../../docs/prd/P15-workflow-wiring-optimization.md).
One-way-door contracts: [`decisions.md`](decisions.md) (OpMerge semantics, adapter-insertion posture).

## Context

The structural axis is not a new dimension to be added; it is a **half-operated axis to be completed**.
Three facts fix the shape of this design:

- **The spec already carries wiring, and it is already hashed.** `VariantSpec.Order` and
  `VariantSpec.Edges` are identity-bearing in `config_hash`
  ([`spec.go:253-258`](../../../internal/variantspec/spec.go)); `InsertedAdapter` is a first-class,
  inspectable node on the spec ([`spec.go:213-229`](../../../internal/variantspec/spec.go)). So a wiring
  change needs **no** `config_hash` work and **no** eval work — landing its effect in `Order`/`Edges` is
  sufficient for the axis-agnostic harness to score it.
- **The gate already exists and is fail-closed.** `GateReorder` validates a candidate ordering through
  `ValidateOrdering` and returns a runnable spec **only** on a coherent or adapted verdict; on rejection
  it returns `(nil, verdict)`, so a caller physically cannot hand a rejected ordering to the transform
  engine ([`rearrange.go:52-61`](../../../internal/variantspec/rearrange.go)). The adapter catalog
  re-validates every synthesized adapter against both producer and consumer
  ([`adapter.go:61-84`](../../../internal/typedcontract/adapter.go)).
- **Two operators are real; one is a reserved promise.** `OpReorder` and `OpPrune` are catalogued
  operators with `Propose` implementations ([`catalog.go:175,326`](../../../internal/proposal/catalog.go)).
  `OpMerge` is a constant with a gain prior ([`operator.go:46`](../../../internal/proposal/operator.go),
  [`gain.go:20,29`](../../../internal/proposal/gain.go)) and **no** `mergeOp` — it is absent from
  `DefaultCatalog()`.

The design work is therefore narrow: implement one operator, route it through the one existing gate, and
carry an honest interim refusal for the source materialization that does not yet exist.

## Decision 1 — `OpMerge` fuses only two *adjacent* nodes, and only where the survivor subsumes both

`mergeOp.Propose` selects the adjacent pair the redundancy signal names and derives a candidate that
**drops the absorbed node from `Order`** and **rewires its edges through the survivor** — the absorbed
node's inbound edges retarget the survivor, its outbound edges re-source from the survivor. All other
per-node overrides are inherited unchanged.

**Alternative rejected — merge any node subset the diagnosis flags, across gaps in the order.** More
reach: a single operator could collapse three scattered redundant calls at once. Rejected on **L2 稳定 +
L1 安全**: a non-adjacent or many-to-one merge changes data flow in ways the coherence gate can *admit*
(it only checks I/O contracts) but a reviewer cannot *follow*, and the resulting `Edges` become a re-plan
rather than a mechanical rewire. Adjacency keeps the merge a local, checkable edit whose diff a reviewer
can read in one screen. The reach of the operator (L8) is not worth the loss of graph legibility (L2).
A chain of three merges pairwise across proposal iterations, which is sufficient (see `decisions.md`).

## Decision 2 — Every wiring candidate is derived with lineage; the parent is never mutated

A merge/reorder/prune candidate is produced by deriving a new `VariantSpec` from the parent with
`ParentVariantID` set, exactly as `Reorder` already does
([`rearrange.go:18-31`](../../../internal/variantspec/rearrange.go)). The parent spec is untouched.

**Alternative rejected — mutate `Order`/`Edges` on the base spec in place.** Fewer allocations, simpler
call sites. Rejected on **L5 不可演进**: in-place mutation destroys the parent lineage the compare view
and rollback depend on, and it is incompatible with the whole re-arrangement design, which is built on
derived candidates carrying `ParentVariantID`. Lineage is *"a property of how a spec was authored"*
([`spec.go:236-239`](../../../internal/variantspec/spec.go)) and must survive.

## Decision 3 — Incoherent orderings are rejected at compile — no runnable spec, ever

Every candidate — merge, reorder, or prune — is validated by `GateReorder` **before any codemod is
generated**. On an incoherent verdict the gate returns **no runnable spec**, so no diff, codemod, or pull
request is generated from it. This is the exact behavior [`AGENTS.md:87`](../../AGENTS.md) cites as its
canonical example.

**Alternative rejected — emit the diff and let the downstream build fail.** Simpler control flow; the
compiler would catch the break eventually. Rejected on **L1 安全**: a wiring change that does not
type-check must be caught **before** a codemod, diff, or PR exists, not discovered after one is produced
and possibly reviewed. `GateReorder` returning `(nil, verdict)` makes it *physically impossible* to hand
a rejected ordering to the transform engine ([`rearrange.go:52-56`](../../../internal/variantspec/rearrange.go)) —
a structural guarantee, not a review convention. Catching the break late trades a safety guarantee for
the convenience of not gating early, which the ordering forbids.

| Ordering verdict | Outcome |
|---|---|
| **Coherent** | Runnable candidate returned; `config_hash` computed; harness scores it. |
| **Adapted** | Adapter recorded on the spec, edges rewired through it; runnable candidate returned. |
| **Incoherent** | `(nil, verdict)` — **no runnable spec, no diff, no PR**. |

## Decision 4 — A bridging adapter is explicit generated source in the same diff

When `FindAdapter` can bridge a producer→consumer mismatch, the ordering is admitted `adapted`, and
`withAdapters` records each adapter as an explicit `InsertedAdapter` node carrying its own `InSchema`/
`OutSchema` `io_contract`, then rewires the edge producer→adapter→consumer
([`rearrange.go:66-89`](../../../internal/variantspec/rearrange.go)). The adapter is materialized as
generated source in the same reviewable diff.

**Alternative rejected — insert a runtime coercion shim that reconciles the mismatch invisibly.** Less
diff noise, no synthetic node in the graph. Rejected on **L1 安全 + L3 UX**: the platform's core rule is
*a deterministic source codemod, never a runtime shim*, and *an indirection never hides a value from
review* ([`project.md`](../../project.md) P10 conventions). An adapter that is not in the diff is a data
transformation hidden from the reviewer — precisely the failure the explicit-`InsertedAdapter` shape
exists to prevent.

## Decision 5 — Un-materializable wiring is refused at transform, naming the axis

Until a source-level wiring rewriter exists, a resolved spec whose `Order`/`Edges` differ from the
discovered wiring is **refused at transform** with an `unsafeRewrite`-class error naming the wiring axis —
the honest analogue of `refuseSkills`/`refuseContext`
([`rewrite.go:388,417`](../../../internal/transform/rewrite.go)).

**Alternative rejected — silently no-op the wiring and rewrite only the node content.** The transform
would "succeed" and the eval would run. Rejected on **L1 安全 + L2 稳定**: a silent no-op would let a spec
whose `config_hash` claims a reordered graph be scored against source that was never reordered — a **false
measurement**, the worst possible outcome for a system whose principle is *verification decides*. Refusal
is the repo's honest *refuse-until-safe* pattern: modeled, resolvable, hashable, but call-site
materialization deferred and **stated**, not hidden. When the rewriter lands, it replaces the refusal and
nothing upstream changes.

## Decision 6 — No new eval metric; wiring is scored by the axis-agnostic harness

A wiring-changed `config_hash` is scored by the existing harness; P15 registers no bespoke metric and adds
no Dimension-label branch.

**Alternative rejected — a wiring-specific quality metric (e.g. "graph depth reduced").** It would give
the console a wiring-flavored number. Rejected on **L6 不可扩展 + single source of truth**: the harness
consumes `config_hash` + `Trace` only ([`evaluator.go`](../../../internal/evalharness/evaluator.go)), and
a bespoke metric would be a second definition of "better" for one axis. "Fewer nodes" is not a goal; a
better score at equal or lower cost is, and the existing `task_success`/`eval_cost_usd`/`eval_latency_ms`
metrics already express it. Landing the effect in `config_hash` is sufficient.

## Interfaces sketch

```
proposal.mergeOp  (new — one row in DefaultCatalog, never a switch edit)
  Kind()          → OpMerge
  HandlesSignal() → SignalRedundantNode           // same signal OpPrune consumes
  Propose(in) → []Candidate:
      pick adjacent (survivor, absorbed) the signal names
      spec := deriveMerge(in.Base, survivor, absorbed)     // ParentVariantID set; parent untouched
          Order:  absorbed removed
          Edges:  absorbed.in  → survivor ;  survivor → absorbed.out   (mechanical rewire)
      newCandidate(OpMerge, in, survivor, []string{"order"}, spec, "fuse adjacent redundant calls")

// unchanged, reused as-is:
variantspec.Reorder(parent, parentID, order, edges) *VariantSpec          // derive, lineage
variantspec.GateReorder(ir, candidate, catalog) (*VariantSpec, Verdict)   // validate BEFORE transform
    coherent → candidate ;  adapted → withAdapters(candidate, verdict.Adapters) ;  incoherent → (nil, verdict)
typedcontract.FindAdapter(producerOut, consumerIn) (*AdapterMatch, bool)  // admissible iff both schemas satisfied

// transform interim refusal (new — analogue of refuseSkills/refuseContext):
if resolved.Order/Edges differ from discovered wiring:
    return nil, unsafeRewrite(nodeID, "wiring", "wiring materialization is deferred; refusing rather than scoring a hash against unchanged source")

// identity (all already true):
config_hash includes Order + Edges (+ InsertedAdapter)   → wiring change ⇒ new hash
adapterNodeID(from,to,kind) deterministic                → same reorder ⇒ same adapters ⇒ same hash
```

## Risks

| Risk | Mitigation |
|---|---|
| A merge changes behavior the gate admits but a reviewer cannot follow | Decision 1 — adjacent-only, survivor-subsumes; the merged `Edges` are a mechanical rewire and the diff is local. |
| An incoherent ordering reaches the transform engine | Decision 3 — `GateReorder` returns `(nil, verdict)`; a caller physically cannot pass a rejected ordering onward. Test asserts no runnable spec. |
| A bridging adapter silently drops a consumer-required field | `FindAdapter` re-validates both schemas and refuses a non-satisfying adapter ([`adapter.go:73-82`](../../../internal/typedcontract/adapter.go)); the ordering is rejected with it. |
| A coercion hides between two nodes | Decision 4 — the adapter is an explicit `InsertedAdapter` node in the diff; a test asserts presence in the spec **and** the diff. |
| A wiring `config_hash` is scored against unchanged source | Decision 5 — interim refusal at transform naming the axis; no silent no-op path exists. |
| A merge that reads redundant scores worse (the second call was correcting the first) | Verification-gated surfacing on held-out data — the produced candidate is exploratory until a P5.5 verified delta confirms it. |
| `OpMerge`'s stored semantics change after proposal rows reference it | Fixed as a one-way door in [`decisions.md`](decisions.md) before any `mergeOp` ships. |
