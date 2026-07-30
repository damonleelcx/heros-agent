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

## Decision 7 — Materialization is a transposition of two adjacent SIBLING STATEMENTS, or a refusal

Wave 15c replaces Decision 5's blanket refusal for exactly one shape and leaves it in force for every
other. The engine materializes a wiring change iff:

1. the spec's `Order` differs from the discovered order by **one adjacent transposition**, `Edges` equal;
2. both call sites are in the **same file**, at the **same indentation**, **consecutive** (nothing but
   blank lines between), each spanning **whole lines**;
3. neither statement is control flow (`return` / `raise` / `break` / `continue` / `yield` / `defer` / `go`);
4. the two statements are **independent**: no name bound by one is read by the other;
5. the workflow's language has a **statement materializer** (Go, Python).

**Alternative rejected — a general move-a-call-anywhere rewriter.** It would materialize merge and prune
too. Rejected on **L1 安全 + L2 稳定 over L8 reach** (decisions.md D-3): an arbitrary move has no
checkable invariant, while a transposition of whole-line blocks is a *permutation of the file's lines* —
provable in one comparison. The wrong-but-compiling version of a control-flow edit is the failure mode
with no downstream net.

## Decision 8 — The swap is a NEW edit class with a stronger gate, not a relaxed old one

`gateMinimal`'s "no rewrite changes the line count" stays exactly as it is for value rewrites. The swap
class asserts: same line count **and** same multiset of lines **and** every changed line inside one of
the two swapped blocks. See decisions.md D-4 — relaxing the shared rule would remove the check from every
rewriter forever to serve one caller.

## Decision 9 — Go and Python first; every other language refuses BY NAME

Independence needs a parse. Go has `go/ast`; Python's statements are whitespace-delimited and the
syntactic frontend already supplies spans and a parse check. Java/Kotlin/Rust/TypeScript/JavaScript
refuse with the language named — the same shape P14 D-14.3 uses for skills, and for the same reason: a
textual move for a language whose structure we did not parse is a guess that compiles.

> **Scope note (Decision 11).** "Go and Python first" is an **ordering** decision, not a terminal state.
> Decision 11 and wave 15e make the wiring coverage table total over the registered language set and
> define what each remaining language actually needs — a resolver and a row, never a new gate.

## Decision 10 — The graph editor tells the truth about every gesture, and refuses to fake a scoreable variant

A user may draft a reorder, a parallelization, a merge, or a prune. The coherence gate runs at
**preflight**; an ordering that breaks a producer→consumer contract is refused **naming the consumer, the
producer, and the field**; a reconciling adapter is shown as an **explicit inserted node in the preview
before submission**; and a shape the transform cannot materialize is refused with the **shape named** and
is **not presented as a scoreable variant**. Everything shared comes from
[`authored-change`](../p13-prompt-model-optimization/specs/authored-change/spec.md).

**Alternative rejected — let the editor accept any rewiring and surface the refusal at apply time.** It is
what a graph editor naturally does, and the refusal machinery already exists downstream. Rejected on
**L3 user-facing complexity** and, more seriously, on **L2 stability**. The UX half is bad enough: dragging a node is a
two-second gesture, and this axis materializes exactly *one* shape, so an editor that accepts everything
is a machine for generating rearrangements that can never be applied. The stability half is worse. An
unmaterializable wiring change is not merely un-appliable, it is **unscoreable** — evaluating a
wiring-changed `config_hash` against unchanged source scores the base configuration under a variant's
hash, which is a false result, and it is *precisely* the failure Decision 5's interim refusal exists to
prevent. An editor that shows a refused rewiring sitting in a variant list "awaiting evaluation" has
already told the user something untrue. So the harder line here is deliberate: refused shapes may be
retained as a **recorded intent**, explicitly not a variant, explicitly not scoreable.

**Alternative rejected — insert reconciling adapters silently, since the gate already proves they drop
nothing required.** The gate's guarantee is real, and hiding the adapter keeps the diff smaller. Rejected
on **L1 safety (honesty) + L5**: an adapter is generated source that will run in the customer's program, and
*an indirection never hides a value from review*. A user who reorders two nodes and receives a diff
containing a component they never saw proposed cannot meaningfully review it. Showing the adapter at
preflight also makes the trade visible at the moment of choice — this reorder is only legal because we
would insert this thing — which is the difference between a tool that reconciles and a tool that
improvises.

**Why the coherence gate is the right thing to move left.** Unlike the other three axes, this one can
decide the hard safety question *statically and cheaply*: `GateReorder` → `ValidateOrdering` needs no eval
spend, no model call, and no repository build. That makes it the strongest pre-submission safety net in
the platform, and leaving it at compile time — after a human has rearranged a graph — wastes the one
property that could have made a graph editor safe.

## Decision 11 — Every registered language gets a resolver and a row; the shape question is asked first

Decision 9 chose Go and Python as the *first* resolvers. It is worth being explicit that this was an
ordering decision, because the sentence "Java/Kotlin/Rust/TypeScript/JavaScript refuse with the language
named" is stable enough to read as a terminal state, and it is not one. Nothing about those languages makes
an adjacent transposition unsound — the parse that Decision 9 requires is exactly what tree-sitter already
supplies for them, and the frontends already locate their call sites. So an absent row in
`statementResolvers` describes **our backlog**, while rendering on every surface as *the move does not
apply to your code*. Wave 15e makes the wiring table total over the registered language set, each gap
naming the resolver as its missing artifact (P13 Decision 13's contract applied here).

Two properties have to survive that growth, and they are the whole of this decision.

**The resolver stays the only per-language part.** Everything that makes a wiring move safe is neutral
already: `planWiringSwap`'s literal one-adjacent-pair check, the edge-set comparison, the coherence gate,
and the line-permutation invariant that catches an emission which is not a permutation. What differs is
one question — where does the statement enclosing this line begin and end. Adding a language must add a
resolver and a row, never a gate. The moment a language needs its own invariant, this axis has five
dialects of one safety property, and the subtly weaker one will not be the one anyone audits. *Rejected:
a per-language "fast path" that skips the invariant where a resolver is confident.* Confidence is not
evidence, and this is the gate that stops a textual move from being a guess that compiles.

**The shape question is asked before the language question.** On any real repository the dominant refusal
here is not "your language has no resolver" — it is *this workflow offers no adjacent transposable pair*,
or *a merge is not a transposition*, and both are already true in Go. Reporting those as a missing rewriter
sends an engineer to wait for work that would not have helped them. So the order is: the requested shape,
then the coherence gate, then the source's statement structure, then the language. And a resolved statement
the resolver can locate but does not model is a `call-site-cannot-carry-it` cause naming the construct —
not a language gap — the same distinction the context axis draws for an unpacked argument mapping.

## Decision 12 — Wiring is permanently outside the runtime route, and the gate has no second door

**Context.** P13's `change-delivery` adds a second delivery route beside a gate whose entire purpose is
to produce **nothing** for an incoherent ordering. That adjacency is the hazard.

**Decision.** Every wiring cell is `notRuntimeResolvable`, **permanently**. Order and concurrency are
compiled program structure; a document that could reorder statements in a built binary would be an
interpreter, and shipping an interpreter into a customer's process to rearrange their own code is a larger
change to their system than any optimization justifies. The cell carries no artifact and no date, and a
later table that moves it to "pending" is claiming an ability that cannot exist.

**And the rule the second route exists to be constrained by.** A change the coherence gate rejected yields
no runnable spec, so it is **not authorable as a rollout candidate**, not deliverable as a pull request,
and reaches a customer's process by no path. Without this stated explicitly, "the rewriter refused, so
roll it out instead" is an available and plausible reading, and it would turn the strongest safety gate in
the system into a speed bump.

**Why say it rather than imply it.** The refusal here degrades slowly and quietly — first into a roadmap
item, then into an exception. NFR15 and NFR16 make both halves executable: no path admits a gate-rejected
ordering, and no wiring cell can acquire a date.

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


// P15 15c — materialization of ONE wiring shape (new: internal/transform/wiringswap.go):
planWiringSwap(discovered, desired) (*swapPlan, bool)   // exactly one adjacent transposition, edges equal
materializeSwap(plan, site, src, language) ([]edit, error)
    same file · same indent · consecutive · whole lines · not control flow · independent → two block edits
    otherwise → refuseWiringMaterialize(node, reason)     // names WHICH condition failed
gateMinimal(swap edits) asserts: line count equal ∧ line multiset equal ∧ changed lines ⊆ the two blocks

// identity (all already true):
config_hash includes Order + Edges (+ InsertedAdapter)   → wiring change ⇒ new hash
adapterNodeID(from,to,kind) deterministic                → same reorder ⇒ same adapters ⇒ same hash

// P15 15d — wiring-authoring: a second ORIGIN, and the gate moves LEFT (see p13 authored-change)
authoring.Draft{ Edits: { Order?, Edges?, Merge?, Prune?, Parallelize? } }
preflight(draft) =
    GateReorder(ir, draft, catalog)                       // the SAME gate, run before submission
      incoherent → refused{ consumer, producer, field }   // all THREE names, never "invalid ordering"
      adapted    → adapted{ inserted:[adapterNode], edges: producer→adapter→consumer }
                   //  ↑ shown in the PREVIEW before submit; never a hidden runtime coercion
      coherent   → planWiringSwap(discovered, desired)
                     ok    → admissible
                     !ok   → refused{ shape: merge|prune|edge|non-adjacent|multi-swap|
                                             unprovable-independence|no-materializer(language) }
scoreable(draft) ⇔ admissible                             // 🔴 a refused shape is NOT a variant:
                                                          //    no eval run, no hash submitted for scoring,
                                                          //    retained only as an explicit "recorded intent"
```

## Risks

| Risk | Mitigation |
|---|---|
| A merge changes behavior the gate admits but a reviewer cannot follow | Decision 1 — adjacent-only, survivor-subsumes; the merged `Edges` are a mechanical rewire and the diff is local. |
| The graph editor accepts a rewiring nothing can apply, and the user finds out at apply time | Decision 10 — the coherence gate and the materializability probe both run at **preflight**; the refused shape is named. |
| A refused wiring draft sits in a variant list "awaiting evaluation" and is silently scored against unchanged source | Decision 10 — an unmaterializable draft is **not a scoreable variant**: no eval run is enqueued and no hash is submitted; it is retained only as an explicit recorded intent. |
| A reconciling adapter appears in the diff the user never saw proposed | Decision 10 — the adapter is an **explicit inserted node in the preview before submission**; *an indirection never hides a value from review*. |
| "Invalid ordering" is all the user is told | Decision 10 — the refusal names the **consumer, the producer, and the field**; a graph error with no names is unactionable. |
| An authored parallelization races on a dependency the analysis could not prove | The parallelize draft is admissible only on **provable** data-independence; unprovable is a refusal naming the dependency, matching Decision 7's conservative posture. |
| An incoherent ordering reaches the transform engine | Decision 3 — `GateReorder` returns `(nil, verdict)`; a caller physically cannot pass a rejected ordering onward. Test asserts no runnable spec. |
| A bridging adapter silently drops a consumer-required field | `FindAdapter` re-validates both schemas and refuses a non-satisfying adapter ([`adapter.go:73-82`](../../../internal/typedcontract/adapter.go)); the ordering is rejected with it. |
| A coercion hides between two nodes | Decision 4 — the adapter is an explicit `InsertedAdapter` node in the diff; a test asserts presence in the spec **and** the diff. |
| A wiring `config_hash` is scored against unchanged source | Decision 5 — interim refusal at transform naming the axis; no silent no-op path exists. |
| A merge that reads redundant scores worse (the second call was correcting the first) | Verification-gated surfacing on held-out data — the produced candidate is exploratory until a P5.5 verified delta confirms it. |
| `OpMerge`'s stored semantics change after proposal rows reference it | Fixed as a one-way door in [`decisions.md`](decisions.md) before any `mergeOp` ships. |
| A language is absent from the wiring table and reads as "the move does not apply here" | Decision 11 — a **total** table over the registered language set, generated; a missing cell goes red. |
| A user whose workflow has no transposable pair is told their language has no rewriter | Decision 11 — shape → gate → statement structure → **language last**; the ordering test goes red when reversed. |
| Adding languages multiplies the gate into per-language dialects | Decision 11 — per-language knowledge is confined to statement boundaries; the invariant, the edge check and the coherence gate stay one neutral path. |
