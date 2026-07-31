---
title: Glossary
tier: guide
summary: The product's own nouns, defined once — so a word you meet in the console or in a pull-request body has somewhere to resolve.
platform_version: 0.20.0
boundary: These are definitions of terms this product uses in a particular way. It is not a glossary of LLM engineering in general, and where a word has an ordinary meaning too, the entry says which one is meant here.
order: 2
---

The console and the CLI use several words in a narrower sense than usual. Each one below appears on a
screen somewhere, and this page is where it resolves.

## Workflow IR

The structural model of a repository's LLM call sites: nodes, edges, and the metadata each carries. It is
produced by `heros discover`, its shape is a published JSON Schema, and everything downstream is computed
from it rather than from source.

Two things it is deliberately not. It is **not a call graph of your program** — only of the LLM call
sites and the connections between them. And it is **not a runtime trace**: an edge in the IR is one a
static reader proved, and edges that only appear at runtime arrive separately and stay distinguishable.

## Node

One LLM call site. It is the unit of everything: configuration is per node, measurement is per node,
attribution is per node, and a change applies to one node at a time.

If a single function calls a model three times, that is three nodes, because they can be configured and
measured independently.

## Variant

One named alternative to your current configuration — a different model on one node, a different prompt,
a different context policy. A variant is a **candidate**, not a change: it exists to be measured against
the baseline, and most of them lose.

## Variant Spec

The document that describes a variant precisely enough to realize it: which node, which dimension, which
value. It is data, not code, and it is what `heros apply` consumes.

Being data is the point. A variant you can diff, review and store is one you can reproduce a year later;
a variant that only exists as a series of edits somebody made is not.

## `config_hash`

The identity of a **resolved configuration** — a hash over the settings that actually determine
behaviour, canonicalized so that formatting changes do not change it and semantic changes always do.

It is the anchor that makes a result traceable. A score, an attribution, a proposal and a delivered pull
request all carry the `config_hash` of the configuration that produced them, so "which settings produced
this number" is a lookup rather than an investigation.

It is not a hash of your source. Two source revisions with the same resolved configuration share a
`config_hash`, and that is deliberate: it is what lets a result carry across a refactor that changed no
behaviour.

## Dimension

One axis along which a node can be changed — the model, the prompt, the bound skills, the tools, the
context policy, the memory policy, the harness. A change is always *a value on a dimension at a node*.

The word does real work in the product. Coverage is reported per dimension, refusals are per dimension,
and `heros coverage` prints a matrix of dimension against language — because whether a change can be
applied depends on both.

## Verified delta

A measured difference between a variant and its baseline, on a **held-out** set, reported **with its
confidence interval**.

Three qualifiers, each of which is load-bearing:

- **Measured**, not estimated. It comes from runs, not from a model's opinion about the change.
- **Held-out**, so the measurement is not on the data the change was chosen against.
- **With an interval**, so you can see when the answer is "no detectable difference" — which it often is.

A delta whose interval includes zero is a **tie**. The product renders it as a tie and will not rank it,
because a point estimate without its interval is how a random fluctuation gets shipped as an improvement.

## Refusal as `BuildStatus`

The transform pipeline's status field carries refusals **as first-class values**, alongside success and
failure — `rejected_transform` is a status, not an error string.

The distinction matters because of what consumers do with it. A refusal recorded as a failure looks like
something broke and gets retried. A refusal recorded as a status is a fact about the change: it was
considered, it was declined, and the reason is attached. See [refusals](/docs/concepts/refusals).

## Unclassified region

A part of a workflow graph that the pattern classifier did not label.

It means **no structural signature matched and no model returned anything in the taxonomy**. It does not
mean that region implements no patterns — and the console says so on the screen, rather than rendering a
gap that reads as an absence of behaviour.

Keeping unclassified regions visible is what makes the labelled ones trustworthy. A classifier that
labelled everything would have no way to tell you which labels it was confident about.

## Gate

A threshold **you** configure — a minimum quality, a maximum cost per run, a latency ceiling. A gate is
not our opinion about your workflow; it is your rule, checked.

A variant that fails a gate is **disqualified**, which means excluded from the ranked order rather than
ranked last. And a gate failure in the CLI exits `1`, distinct from `2` — your gate failing and our tool
breaking have opposite remedies. See the [exit codes](/docs/reference/cli#exit-codes).

## Coverage

What the eval set does **not** reach, kept in the denominator.

Dropping an uncovered obligation would raise the coverage percentage by deleting the evidence of a gap,
so the product does not do it. A coverage figure here is therefore lower than one computed the flattering
way, and it is the one you can act on.

## Automation level

How much a workflow is allowed to have done to it without a person: Advisory, Assisted, Autonomous.

It governs delivery. At Assisted, a verified proposal can be opened as a pull request for a human to
review and merge. Autonomous merging exists as a governed, budgeted and audited option — it is not what
happens by default, and the platform never merges on your behalf unless you have turned that on.

## Delivery record

The append-only history of a change reaching a repository: which configuration, which source revision,
which pull request, and what state it is in.

`merged` in that record is an **observed fact** from the forge, not an inference from a pull request
being closed. The difference matters when a change is billed against it.
