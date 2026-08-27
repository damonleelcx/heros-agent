---
title: Context policies
tier: guide
summary: What each node is given to read — which turns survive, what is summarised, what is retrieved — and which of those the platform can write into your source.
platform_version: 0.20.0
boundary: This page explains the axis. It does not change anything. Your own node's current policy, and the control that changes it, are on the context surface in the console — this page is what that surface links to when it needs a paragraph rather than a value.
order: 4
---

## What a context policy decides

Context is the set of messages a node is given. A policy decides which of them survive, whether
anything is summarised, and whether anything is retrieved and added.

A policy is part of a configuration's identity: two variants that differ only in their policy have
different hashes and are scored separately, exactly like two that differ in their model.

## Why applying one is a code rewrite

What makes this axis different from the others is that a policy is **not an argument you can swap**. It
is *how the surrounding code builds the message list*, so applying one means rewriting that code.

The platform will do that rewrite for some policies and declines to do it for others, and the boundary
is not arbitrary — it follows from what actually exists in your source at the moment the rewrite would
happen.

## Two different reasons a policy is declined

These read alike on a screen and have different owners and different lifespans. Collapsing them into one
greyed-out control is especially costly on this axis, because the policies readers most want —
summarization, retrieval — are exactly the ones in the first category.

### Not in source

A summary, a tiered digest, or a retrieved chunk set does not exist until a model or a retriever
produces it at run time. There is nothing in your source to select, **in any language**.

This is not a wait. No rewriter will ever apply it, and the policy still runs where it belongs — host
side, where the credential lives.

### Not yet — and it is ours

A policy that *selects among the turns you wrote* is a deletion, and it needs that language's list
splitter. Where the splitter has not landed, the platform says so and names it. This one is work the
platform owes you.

A third case belongs to neither: a call site that unpacks its arguments has written no message list at
all, so a selection has nothing to select among — before or after any splitter lands. That is a fact
about the call site, not about language support.

## What each policy does at a call site

This table is a fact about each **policy**: whether its assembly exists in your source at all. Applying
one is then a fact about the **language** — the selection rewriter has landed for `go` and `python`, and
every other language declines a selection policy by name until its rewriter lands.

| Policy | At a Go call site | What happens, and why |
|---|---|---|
| `full` | identity | Passes the whole conversation through. A call site that writes its turns out is already doing exactly this, so there is nothing to change — and no diff is the correct answer, not a missing one. |
| `full-history` | identity | The same behaviour under its spec name. Both names resolve, so a pinned spec keeps working. |
| `sliding-window` | applied | Keeps the most recent N turns. Applied by DELETING the older ones from the list you wrote — a deletion of your own code, which constructs nothing and cannot invent a shape. |
| `semantic-compaction` | applied | Keeps the most recent turns that fit a token budget. Applied the same way, selecting by size instead of by count. |
| `summarization` | declined | Replaces the history with a summary a model writes at run time. There is no summary in your source to select, and writing one in would freeze a model's answer into your repository. |
| `hierarchical-summary` | declined | Keeps the recent turns verbatim and summarises the older ones — one model call, at run time. Its summarised tier is model output, not source you wrote. |
| `rag-retrieval` | declined | Retrieves passages for the live question at run time. What comes back depends on the conversation, not on your source, so there is nothing at the call site to materialise it from. |
| `structured-extraction` | declined | Rewrites message CONTENT into a declared field set. That means constructing new messages rather than selecting among yours, and a constructed message whose shape was guessed is the failure with no downstream net. |

Partial coverage is stated rather than smoothed over: a decline is a correct answer — *"not applicable
here yet"* — and a silent no-op would be an incorrect one.

## What reaches your source

Every context override ends in one of three states, and never in a fourth:

| State | What it means |
|---|---|
| **applied** | the change is in the diff |
| **declined** | nothing was persisted, and the reason names the policy |
| **equivalent** | the policy and your call site already do the same thing — a window wider than the conversation, for instance — so no diff is the correct diff |

If a policy could not be written into your source and the platform applied it as a no-op, the run would
use your original message list while reporting the variant's configuration hash. The number would be
wrong and would look exactly like a number that is right.

## Why context is applied inline

Context is **code**, so it is applied inline and shows up in the diff you review — even when the variant
asks for the indirection mode that carries a model or a prompt as data.

Hiding an assembly change behind a handle would mean approving a change you cannot read, and the message
list is the last place that is acceptable.

## Choosing a policy

Only registered policies are offered. A policy outside the registered set is not a lesser choice —
nothing resolves it, so offering free text would be offering a choice that fails the moment it is
sealed.

The picker on the context surface binds to that registered set at the set's recorded version, so a
configuration hash you pin today still means the same thing months later.

## What it costs to lose context

A policy that summarises or compacts throws information away. How much it threw away is recorded on
every run, per node, as `context_drop_ratio` — a real number from the assembly, not an estimate. It is
computed in `internal/telemetry/context_assembly.go:EmitContextAssembly` and emitted as a ratio.

A policy that **cannot** lose anything records nothing at all, deliberately: `context_drop_ratio` is
emitted only by a lossy policy. A zero from a summariser means *"this run happened to drop nothing"*; a
zero from a window would mean *"this policy never drops"*. Publishing both as `0` would make the two
unreadable, so only the first is published.

### A proposal that would lose too much is never run

Each node carries a **tolerance**: the fraction of its context its job can afford to lose. A proposed
policy that would push a node past it is *inadmissible* — rejected when it is proposed, so it never
becomes a diff and never consumes an evaluation run.

Scoring would eventually punish the same change: a summariser that removed the answer shows up as a drop
in task success. The gate reaches the same verdict earlier and for free. It is not a second opinion
about quality — it is the same one, arriving before the bill.

### What a node tolerates by default

| The node's job | Default tolerance | Why |
|---|---|---|
| Retrieval (RAG) | 60% | It assembles from a corpus by design; reshaping what it carries is its normal behaviour, not a failure. |
| Memory management | 75% | Compaction is the job. A tight limit here would reject the node's whole purpose. |
| Reflection, planning, reasoning, guardrails | 20% | These reason OVER the conversation, so what a lossy policy removes is the material they reason about. |
| Everything else | 40% | The middle, chosen so the gate bites on obvious loss without blocking ordinary tuning. |

A node may declare its own tolerance, and an explicit value always wins — including an explicit zero,
which means *"this node tolerates no loss at all"*. A node that declares nothing is unchanged in every
other way: its configuration hash is byte-identical to what it was before this existed.

### What the gate will not do

It never rejects a policy simply because nothing has measured it yet. A change with no measurement and
no estimate is admitted and goes to verification, because *"we have no data"* must not come to mean
*"no"* — that would freeze the board on whatever happened to be measured first. When a measurement for
that node does exist, it wins over any estimate.

## A drop ratio is not a saving

A lossier policy shows fewer tokens immediately. That number is reported as **information discarded**,
never as a saving: until a multi-seed evaluation runs, task success is unmeasured, and a policy that
discarded the answer looks exactly like one that discarded filler.

Pure augmentation is recorded as retrieval rather than loss — a drop ratio of zero with a positive
retrieved-chunk count. Recording augmentation as loss would make the tolerance gate reject retrieval,
this axis's single best operator, for doing exactly what it is for.

## Retrieval tuning

Four knobs decide what a retriever returns: **top-k**, **chunk size**, **rerank** and the **embedding
model**. Each proposal names the knob it moved and what it moved it from, so a verified win is
attributable to one change rather than to "something about retrieval".

### Offered only where retrieval happens

They are proposed only on a node the classifier labels as retrieval — on any other node they are
meaningless. Where they are not offered, the reason is stated: a control that were simply absent would
read as a missing feature.

The label is **not** settable from the console. A node relabelled to unlock the parameters would let a
result be attributed to parameters that did nothing; a misclassification is a classifier defect to fix,
not an override to hand out.

### Verified on cases the tuning never saw

Retrieval knobs are searchable, and a search against an evaluation set will find whatever scores best on
that set. Reporting that number would be overfitting sold as a result.

So a retrieval change is verified on a **held-out** set, disjoint from the one its parameters were
selected on. The split is derived so the two halves cannot share a case; if a split is supplied whose
halves *do* intersect, verification is **refused** rather than computed — there is no honest number to
report from it. An overlap of the intervals on held-out cases is a *tie*, and a tie is not a win.

### The same configuration asks the same question twice

A measurement run pins the retriever, its parameters, and the seed, so re-running the same configuration
at the same commit issues the identical retrieval request — the rerank included. A run that does not pin
all three is not accepted as a measurement at all, because nothing could re-derive its number later.

The promise is about the **request**, not about the provider's bytes. What a search index returns next
week is outside anything this platform controls, and claiming otherwise would be a promise that fails
silently.

## Worked examples

Everything below is **the platform's own fixture** — a demonstration node in the engine's test corpus,
not your repository. Your own node, its current policy and its own diff are on the context surface in
the console.

### An applied window

The source assembles `turnOne, turnTwo, turnThree, turnFour`; a `sliding-window` policy keeping the two
most recent turns assembles `turnThree, turnFour`. The platform can apply this one, because keeping a
subset of the turns you wrote is a deletion of your own code — it constructs nothing, needs no knowledge
of your SDK, and cannot produce a value whose type you did not already write.

The engine's own unified diff, over the Go fixture's four-turn call site:

```diff
--- a/pipeline.go
+++ b/pipeline.go
@@ -68,7 +68,7 @@

 // history writes a four-turn conversation on ONE line, the shape a window materializes into cleanly.
 func history(client *anthropic.Client) {
-	client.Messages.New(nil, anthropic.MessageNewParams{Messages: []anthropic.MessageParam{turnOne, turnTwo, turnThree, turnFour}})
+	client.Messages.New(nil, anthropic.MessageNewParams{Messages: []anthropic.MessageParam{turnThree, turnFour}})
 }
```

**Only turns were removed, and no line was added.** Nothing was constructed and no import was added, so
this change cannot have introduced a value the code did not already contain — it can only have made the
node read less. That is checked before the diff is offered.

### Four declines, in the order the engine considers them

Ordering them any other way produces a true-but-useless answer: a `**kwargs` call site told to wait for
a rewriter that would have refused it too.

#### A run-time policy — permanent, and correct

A summarization policy on a Go node. The platform understood it, resolved it, and hashed it — and then
declined to write it into the source, because the summary does not exist until a model produces it at
run time. The policy still runs host-side, where the credential lives.

The engine's own sentence, verbatim:

```text
context policy "summarization" assembles by CALLING a summarizer model, host-side through HostServices,
at run time; there is no summary in the source to select, and writing one in would freeze a model's
output into the diff and claim a provider call this engine never made. It is REFUSED at the call site
rather than dropped; the policy still runs host-side where it belongs, and the policies this engine
materializes into source are [full full-history semantic-compaction sliding-window]
```

#### A call that unpacks its arguments — permanent for this call site

A window on a Python call site that passes `**api_kwargs`. The request is assembled somewhere else in
the program, so there are no turns written here to select among.

```text
this call site passes **api_kwargs, so its message list is assembled somewhere else in the program and
is not written here — there are no turns at this call site for policy "sliding-window" to select among.
This is a property of the call site, NOT of Python support: a context rewriter for this language would
refuse it for exactly this reason too. Apply the policy where **api_kwargs is built, or write the
messages at the call site as a list this engine can select from
```

#### A language without a rewriter — the one decline that is a promise

The same window, on a TypeScript node that *does* write its turns out. Go and Python can perform the
selection; TypeScript's splitter has not landed.

```text
context assembly is not a call-site argument — it is how the surrounding code builds the message list —
so materializing policy "sliding-window" is a REGION rewrite of that code, per language. P16 owns that
rewrite and has landed it for Go; the TypeScript materializer is still being built, so this override is
REFUSED rather than dropped — applying it as the base configuration would score a configuration that
never ran
```

#### A run-time message list — yours to change

A window over a call site that builds its messages with a function call instead of writing them out.

```text
this call site assembles its messages at runtime (a call to buildHistory), not as a written list, so
policy "sliding-window" has no declared messages to select among; materializing it would mean guessing
what the runtime assembly contains, and a guess that compiles is the failure mode with no downstream net
```

### The three verdicts a preflight returns

Every draft is checked before submission. The check publishes nothing, writes no diff and spends no
evaluation budget — and it answers with one of three verdicts, never two.

| Verdict | Fixture | What it means |
|---|---|---|
| **admissible** | `config_hash b41c7e09aa25`, node `summarize` | measured, and within the tolerance this node declares |
| **refused** | node `answer` declares a tolerance of 0.20; `ctx-summarization` was measured to discard 0.62 of its context — refused before any evaluation spend | measured, and over it, with **both** numbers shown |
| **not yet measurable** | node `classify`, missing `context_drop_ratio` for `ctx-hierarchical-summary` | nobody has measured this pair, and we say so |

The third is the one that gets lost. Drawn as a refusal it points the reader at their own configuration
to find a fault that is not there; drawn as admissible it claims a safety check that never ran. It is
neither, and it renders as neither.

Both numbers are shown on a refusal because either could be the thing to change: relax the tolerance if
this node can afford the loss, or pick a policy that discards less. A refusal that said only *"exceeds
tolerance"* would give you neither.

## Where to go next

- [Refusals are outcomes, not errors](/docs/concepts/refusals) — what a refusal is, and [why a refused change is never a dropped one](/docs/concepts/refusals#a-refused-change-is-never-a-dropped-one)
- [Memory strategies](/docs/concepts/memory-strategies) — what a node carries *between* invocations, which is a different axis
- [Glossary](/docs/concepts/glossary) — `axis`, `policy`, `variant`, `config_hash`
