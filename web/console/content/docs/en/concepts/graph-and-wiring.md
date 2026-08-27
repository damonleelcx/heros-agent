---
title: Graph and wiring
tier: guide
summary: The shape of a workflow — the four wiring operations that rearrange it, the three topology forms that declare how it runs, and the one shape that actually reaches your source.
platform_version: 0.20.0
boundary: Exactly one shape is written into your source today — a transposition of two adjacent, independent statements. Everything else is refused by name, and this page says which and why. Your own workflow's shape, and the editor that rearranges it, are on the graph surface in the console.
order: 7
---

## The shape of a workflow

Two axes share this surface because both are the **shape** of a workflow, and they share vocabulary
without sharing meaning.

**Wiring** rearranges the graph: which node runs where, which edges exist. **Topology** declares how the
graph runs: what may overlap, which edges are conditional, how a fan-in combines.

## Wiring and topology are not synonyms

They are opposite operations sharing a word, and this is stated here rather than left to be discovered
from a refusal.

| Word | On the wiring axis | On the graph axis |
|---|---|---|
| **parallelize / concurrent** | *drops a sequencing edge* between two nodes that share no data, so nothing orders them any more | *declares that nodes may overlap*, over an ordering that still lists both |
| **merge** | *fuses two nodes into one* — the node set shrinks, and the claim is that one of the calls was unnecessary | says *how a fan-in's results combine* — every call still happens |

## The four wiring operations

| Operation | What it does | Its bound |
|---|---|---|
| **Merge** | Fuses an adjacent pair into one call: one survives, the other's edges are rewired through it. | Adjacent pairs only, never across a gap and never three at once. |
| **Reorder** | Exchanges two neighbours that exchange no data. | A data dependency is a fact about the code, never an ordering choice. |
| **Parallelize** | Drops a sequencing edge between two nodes that share no data, so they may run at once. | Expressed as the absence of an edge — there is no separate flag to disagree with it. |
| **Prune** | Removes a node nothing downstream reads and rewires its neighbours to each other. | Proposed, never applied on the strength of looking redundant. |

Each is a **proposal**. A change that removes a call is not a win because it removed a call — a
candidate reaches you only after it scores better or cheaper on held-out cases.

## The five gestures

The graph editor offers five, and each one gets its verdict *as you make it* rather than after
submission. An editor that accepts the drag and refuses afterwards manufactures rearrangements that can
never ship.

| Gesture | What it is | Scoreable |
|---|---|---|
| Swap two adjacent nodes | exchange two adjacent, independent statements in the same function | yes |
| Swap, reconciled by an adapter | reorder two nodes whose schemas do not line up; the adapter is shown, with its edge and its kind, **before** submission | yes |
| Move a consumer before its producer | refused — the field the consumer reads would be undefined | no |
| Merge two nodes into one | modelled and hashed, refused at the source | no |
| Move a node across the graph | not an adjacent transposition, so refused by shape | no |

An incoherence refusal names **the consumer, the producer and the field**. A refusal that named only the
node would leave the reader with a graph to re-read by eye.

## What reaches your source

Exactly one shape: a **transposition of two adjacent, independent statements**. Everything else is
declined by name, because materialising it would mean moving, fusing or deleting a call.

### Before you move anything

Two facts decide whether a reorder can ship. They have different owners and different next steps, and
collapsing them into *"reordering is unavailable here"* sends the second reader to wait for the first
reader's fix.

**Not yet — and it is ours.** A reorder is materialised by exchanging two adjacent statements, which
needs that language's statement boundaries. Where the resolver has not landed, the platform says so and
names it. Nothing in your code unlocks it.

**Your workflow.** Even where the resolver exists, this axis materialises exactly one shape: a single
exchange of two *adjacent sibling statements*. A workflow whose nodes are not adjacent — or a merge, a
prune, an edge change, a non-adjacent move — is refused by shape, in every language. That is a fact
about the source, and it has no *"when"*.

Both answers are identical on every plan. A refused shape is also never scored: evaluating a wiring hash
against source that was never rearranged would be a false measurement, not a partial one.

### A refusal is not a queue

A refused rearrangement is kept as a **recorded intent**, not queued for anything. It is not a variant,
it is not pending, and nothing will retry it. What it is, is a thing you meant, written down where you
can find it when the shape becomes materialisable.

## The three topology forms

You can declare all three today. **None of them is written into your source yet — in any language.**

A topology declaration *resolves*, is *validated against your typed I/O contracts*, and is part of the
configuration's hash. What does not exist is the codemod: no language has a rewriter that writes a
concurrent group, a conditional edge or a merge into source. So it is **refused by name** rather than
applied as a no-op, and the refusal names the axis, the node and the form.

| Form | What it declares | What is missing |
|---|---|---|
| `concurrent group` | Two or more nodes may overlap. The ordering still lists every one of them, so a replay visits them in a defined sequence even when the live run overlapped them. | a topology rewriter for your language, or — where the frontend is syntactic — a typed analysis that emits edges at all |
| `conditional edge` | An edge taken only when its predicate holds. The predicate is an expression binding and follows the same rule a prompt slot's does: it must name a value already in scope at the producing call site, validated when the configuration resolves, never inferred. | a topology rewriter for your language |
| `merge` | How a fan-in's results combine, and what happens when one of its nodes fails. Both are required and neither has a default. | a topology rewriter for your language |

### Why a fan-in declares its merge

When two nodes converge on a third, something has to say how their results combine. The available
defaults — first result wins, concatenate, last writer — are all semantic choices about **your**
program, and none of them is more obviously right than the others. A default here is the platform
deciding what your code means.

The same applies to failure: `fail-fast` and `collect-partial` are different products, so
`on_node_failure` is required too.

And `collect-partial` carries a consequence that is checked rather than documented: the merge may
deliver fewer inputs than the group has nodes, so a downstream contract that *requires* a field only one
node produces is refused. Without that check, the mode would be a promise the type system does not keep.

## Worked examples

**The platform's own fixtures**, from the engine's test corpus — not your repository.

### An applied transposition

Two calls that sit next to each other in one function, share no name, and could legitimately run in
either order. The source wires `first → second`; the spec asks for `second → first`.

```diff
--- a/wiring.go
+++ b/wiring.go
@@ -7,8 +7,8 @@
 // first is a measurable choice — exactly what the wiring axis proposes and what 15c can now apply.
 func twoCalls(client *anthropic.Client) {
 	prepare(client)
-	first := client.Messages.New(nil, anthropic.MessageNewParams{Model: anthropic.ModelClaudeSonnet4})
+	second := client.Messages.New(nil, anthropic.MessageNewParams{Model: anthropic.ModelClaudeOpus4_6})
-	second := client.Messages.New(nil, anthropic.MessageNewParams{Model: anthropic.ModelClaudeOpus4_6})
+	first := client.Messages.New(nil, anthropic.MessageNewParams{Model: anthropic.ModelClaudeSonnet4})
 	report(first, second)
 }
```

**The file's lines were reordered and nothing else changed.** Same line count, same lines — so this
change cannot have altered what any line says, only when it runs. That is checked before the diff is
offered.

### A declined reorder

The same shape as the applied case, but these two calls are not adjacent statements in one block:
something else sits between them. A transposition exchanges neighbours; anything else is a move.

```text
this spec asks for a wiring change (node order differs at position 1: the source runs "draft" there, the
spec asks for "summarize" (source [plan draft summarize], spec [plan summarize draft])), but no
call-site rewriter materializes a node rearrangement as source — moving, fusing, or deleting a call is
control-flow surgery, not the value replacement this engine performs. It is REFUSED rather than applied
as a no-op: a no-op would let this spec's config_hash, which already records the new graph, be scored
against source that was never rewired — a false measurement, not a missing feature
```

### A merge

An adjacent pair fused into one call: one node survives and absorbs the other, whose edges are rewired
through the survivor.

```text
this spec asks for a wiring change (the source wires 3 node(s) [plan draft summarize] but the spec
orders 2 [plan draft]), but no call-site rewriter materializes a node rearrangement as source — moving,
fusing, or deleting a call is control-flow surgery, not the value replacement this engine performs.
```

### A rewired edge

Same nodes, same order, one edge added. A graph is its order **and** its edges, so this is a rewire too
— and it produces a different configuration with a different hash.

```text
this spec asks for a wiring change (the spec adds the edge plan -data-> summarize, which the source does
not wire), but no call-site rewriter materializes a node rearrangement as source
```

### A refusal on an axis this console carries no note for

It still renders, and the platform's own sentence carries it. Swallowing an unrecognised refusal would
leave the reader with a submission that vanished.

```text
this dimension has no call-site rewriter for the matched registry row, so the override is refused rather
than dropped from the diff
```

## Where to go next

- [Refusals are outcomes, not errors](/docs/concepts/refusals) — and [why a refused change is never a dropped one](/docs/concepts/refusals#a-refused-change-is-never-a-dropped-one)
- [The execution envelope](/docs/concepts/execution-envelope) — the ceilings a graph runs inside
- [Glossary](/docs/concepts/glossary)
