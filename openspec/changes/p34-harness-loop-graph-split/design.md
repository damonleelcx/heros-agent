# Design — P34: Harness / Loop / Graph

The arbitration lives in [ADR-014](../../../docs/adr/ADR-014-harness-loop-graph-axis-split.md). This document
is the mechanism and the compatibility argument.

## D1 — Expand only; the contract half is refused

**Decision.** Add `DimLoop` and `registry.KindLoop`. Do **not** remove the loop fields from `HarnessSpec`.
New authoring writes a loop entry; legacy loop-bearing harness entries stay resolvable indefinitely; a
spec setting both is refused at resolve, naming both.

**Why the removal is refused rather than scheduled.** `registry.Kind` is hashed into the `version_id`, and
`registry/harness_test.go` says why in its own failure message: *"the kind is hashed into every version_id
and into the DB trigger's argument, so it cannot drift."* So the chain is: remove a field → the entry's
content changes → its `version_id` changes → every spec referencing it hashes differently → every
measurement keyed by those hashes becomes unreachable. Not invalid — **unreachable**, from any spec anyone
can construct.

Scheduling the removal for "later" does not make that chain shorter; it makes it somebody else's, on a
day when the reasoning has been forgotten. Refusing it on the record means a future proposal to do it is
an amendment to an ADR rather than a cleanup ticket.

**The honest cost.** A permanent legacy read path, and a resolver that must recognise two shapes forever.
This is uncomfortable and it should be — the alternative trades level 2 (stability) for level 7
(maintenance), which the eight-level rule forbids outright.

## D2 — The ceiling is harness; the value is loop

**Decision.** `TurnCeiling` and the spend cap live on the envelope. `max_turns` lives on the loop and is
validated against the envelope's ceiling at resolve.

**Why.** `boundedCeiling` already makes the argument for turns: *"the ceiling is a policy about how much
autonomous tool-calling one node may do, and honouring a value the registry would not seal would make this
a second and looser gate."* A policy is imposed; a value is chosen. An operator raising a ceiling and an
engineer picking four instead of two are different acts by different people with different review
requirements, and putting them on one axis is what makes the current model unreviewable.

**Consequence worth stating.** Raising a ceiling must change **no** loop entry's content and no loop
entry's `version_id`. If it did, a policy change would silently re-hash every configuration under it —
which is the same orphaning failure as D1, arriving by a different door.

**Open.** PRD §14 Q1 asks whether *spend* follows turns. It is less obvious, because a spend cap is
consumed almost entirely by loop iterations even though it is imposed like a policy.

## D3 — Graph is spec-level, not a `Dimension`

**Decision.** Topology lives beside `order` and `edges` on the spec.

**Why.** Every member of `Dimensions()` is a property of one node. Topology is a property between nodes.
Making graph the first non-per-node `Dimension` breaks the invariant that lets the transform engine
iterate `Dimensions()` uniformly and lets the eval harness stay axis-agnostic — and once broken, every
future consumer has to ask which kind of dimension it is holding.

`Order` is already documented as identity-bearing — *"reordering changes config_hash (FR4), because the
wiring is part of a configuration"* — so topology is already hashed. This adds fields to a structure that
is already in the right place.

## D4 — Concurrency is declared **over** `Order`, never instead of it

**Decision.** `order` keeps every node in a linear sequence. A concurrent group declares which of its
members may overlap.

**Why.** The honest data model for a concurrent graph is a DAG, and replacing `order` with one would
change the serialization of every spec in existence — violating the byte-identical guarantee that D1 spent
its whole argument protecting. Declaring groups over the order is additive, `omitempty`, and preserves
that guarantee.

It also preserves something worth having on its own: **replay determinism**. A run that overlapped two
nodes still has a defined order to be replayed in, which matters for attribution, for diffing two runs,
and for anyone trying to reproduce a failure.

**Rejected.** *Replace `order` with a DAG and migrate.* Cleaner model, and it is the same orphaning chain
as D1 with a nicer justification.

## D5 — A predicate is an `expr` binding

**Decision.** A conditional edge's predicate is validated by ADR-004's `expr` rules: declared and validated
at spec-resolve time, never inferred, refused when a name is not in the program's lexical scope.

**Why.** This is the first place a customer-authored *expression* affects control flow, and the temptation
is to give it a small bespoke grammar because "a predicate is simpler than an expression". A second
grammar is a second scope-validation implementation, and the scope check is the thing standing between a
predicate and a name that does not exist at that call site. One grammar, one validator.

**If `expr` proves too permissive**, narrow it in one place — which is only possible because there is one
place.

## D6 — A fan-in without a merge is invalid, not defaulted

**Decision.** Refused at validate.

**Why.** The available defaults — first result wins, concatenate, last writer — are all semantic choices
about the author's program, and none of them is more obviously right than the others. A default here is
the platform deciding what the customer's code means. D6 also extends to failure semantics (PRD §14 Q3):
what happens when one member of a concurrent group fails is a choice, and it probably belongs in the merge
declaration rather than as a global rule.

## Compatibility argument, stated as the thing to test

```
pre-P34 spec, no loop_ref, no graph_groups
    → serialises byte-identically              (golden vectors, unchanged)
    → resolves to the same config_hash
    → its stored measurements remain reachable

pre-P34 spec, loop-bearing harness_ref
    → still resolves                            (legacy path, permanent)
    → same config_hash as before
    → loop fields honoured for that spec

post-P34 spec, loop_ref + envelope harness_ref
    → resolves
    → max_turns checked against the envelope ceiling
    → host service checked at resolve, not at run

any spec setting both
    → refused at resolve, naming both refs
```

## Data-model sketch

```go
// variantspec
const DimLoop Dimension = "loop"
func Dimensions() []Dimension {
    return []Dimension{DimModel, DimPrompt, DimSkills, DimContext, DimTools, DimMemory, DimHarness, DimLoop}
}

type VariantSpec struct {
    SourceRevision string                  `json:"source_revision"`
    Order          []string                `json:"order"`                    // unchanged: the deterministic walk
    Nodes          map[string]NodeOverride `json:"nodes"`
    Edges          []Edge                  `json:"edges"`                    // + predicate kind (D5)
    HarnessGroups  []HarnessGroup          `json:"harness_groups,omitempty"` // unchanged (P18 FR15)
    GraphGroups    []GraphGroup            `json:"graph_groups,omitempty"`   // NEW — D4, D6
}

type GraphGroup struct {
    Members     []string `json:"members"`               // must all appear in Order
    Concurrent  bool     `json:"concurrent,omitempty"`
    Merge       *Merge   `json:"merge,omitempty"`       // required on a fan-in (D6)
}
```

`GraphGroup` and `DimLoop`'s `loop_ref` are both `omitempty`, which is what makes the first line of the
compatibility argument hold.

## Risks this design accepts

- **A permanent legacy resolve path.** D1. It never expires, and the refusal is on the record so that
  removing it is an ADR amendment.
- **Attribution assumes a linear span sequence** in more than one place today, implicitly. Concurrency
  makes overlapping spans a real shape, and the ablation discipline applies: prove on a holdout that
  attribution does not degrade before this ships.
- **The `/app/harness` and `/app/wiring` re-cut can lose content.** Three surfaces where there were two;
  the standing failure mode of a UI revision is that something on the old page has no destination on any
  new one.
- **Two new proposal operators are two new search spaces**, and their honest measurement is per-axis pass
  rate through the P5.5 gate — never a mean across axes, which would hide an operator that is not working.
