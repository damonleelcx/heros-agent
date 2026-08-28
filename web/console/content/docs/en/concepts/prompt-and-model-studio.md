---
title: Prompt and model
tier: guide
summary: The node × model matrix, the prompt library, bound and inline apply modes, and why the model list offered for a call site is shorter than the catalogue.
platform_version: 0.20.0
boundary: Nothing on this axis is ranked. There is no score, no winner, no confidence interval and no promotion path — a bound cell is selected and unverified, never proven best. This page explains the axis; your own nodes and the matrix that binds them are on the studio surface in the console.
order: 10
---

Two things are chosen per node on this axis: **which model runs there**, and **which prompt version it
uses**. The studio surface is where both are chosen, over your own call sites.

## Nothing here is ranked

There is no score, no winner, no confidence interval, and no button that promotes a configuration. A
comparison shows two outputs side by side and says nothing about which is better.

Every result the studio produces is **exploratory**. A bound cell is *in force, unverified* — never
*proven best*. Proving a configuration is what a multi-seed evaluation does, and it is a different act
with a different cost.

Prompt bodies are **customer content**. They are rendered as text, never as markup, and never logged.

## Bound and inline

`apply_mode` is a structural property of a call site, known before you choose anything.

| Mode | What it means | What can change under it |
|---|---|---|
| `inline` | the values are written at the call site, as code | a model string, by a codemod and a pull request |
| `bound` | the values are carried as data and resolved at run time | the model, its parameters and the prompt, without touching source |

Two distinctions here are load-bearing and must never collapse:

- *"someone selected this"* (**unverified**) and *"this was proven better"* (**verified**) render
  differently. Collapsing them destroys the only distinction that makes a selection safe to make.
- A bound node shows its **resolved values**, never the pointer. A reviewer approves a configuration,
  not `agentcfg.Node("n").Model()`.

Call-site *structure* — an expression binding, an input binding — is shown as **needing a new change**,
never as data you can edit here.

### When the resolver is degraded

If a configuration source cannot be adopted, the **last known-good configuration stays in force** and the
surface says which source failed and why. The resolver does not fall back to an empty or default
configuration: a default silently in force is a configuration nobody chose being reported as one somebody
did.

## Why the model list is short

A call site written against one SDK does not become another by changing a model string — the client, the
parameter types and the response shape all differ. Offering a model from another provider would produce
a diff that compiles and then calls the wrong provider in production: silent, and in production, which
is the worst failure this axis could cause.

But a list that is silently short reads as an incomplete catalogue, and the reader's next move is to look
for the missing entries or file a bug. So the boundary is **stated at the point of choice**, and an
option this deployment cannot supply is rendered **disabled, naming the service it needs**, rather than
hidden.

## Authoring a model

Picking a model for a node directly — instead of waiting for a diagnosis to propose one — goes through
the same gates a proposed change does, produces a reviewable diff, and is recorded **unverified** until a
multi-seed evaluation has run. There is no separate path for a hand-made change, which means there is no
gate a hand-made change can skip.

What a node can carry is a structural property of its call site, known *before* you choose, so it is said
before you choose rather than discovered at submit.

## Provider parameters

Provider parameters follow the apply mode. On a `bound` node they are data and can be set. On an
`inline` node they are code, and there is no applicable parameter rewriter — so the override is refused
rather than dropped from the diff:

```text
node "summarize", dim model: this call site applies inline, where provider parameters are code rather
than data; there is no applicable parameter rewriter, so the override is refused rather than dropped
from the diff
```

## The prompt library

A prompt is versioned, and a version is immutable. The library shows each version's slots, its creation
time, and a diff against any other version — body changes and slot changes separately, because a slot
that stops binding is a different failure from a paragraph that was reworded.

### Slots and impact

A prompt's `{{slots}}` are its interface. Changing them changes what the nodes using that prompt must
supply, so the library reports **impact** before a version is adopted:

- **blocked** nodes — a node that cannot supply a proposed slot, named with the reason;
- **unanalyzed** nodes — a node whose bindings could not be determined, named with why.

An unanalyzed node is not a passing node. Rendering it as one would claim a check that never ran.

## Authoring is not an evaluator

Authoring changes **who chooses** a model. It changes nothing about **who judges** whether the choice was
good.

## Worked example

**The platform's own fixture**, not your repository. Three nodes on an `anthropic` call site:

| Node | Provider | Model | Apply mode |
|---|---|---|---|
| `classify` | anthropic | `claude-haiku-4-5` | bound |
| `summarize` | anthropic | `claude-sonnet-4-5` | inline |
| `answer` | anthropic | `claude-opus-4-1` | bound |

The models offered for these call sites are `claude-haiku-4-5`, `claude-sonnet-4-5` and
`claude-opus-4-1` — every one of them anthropic, for the reason above.

## Where to go next

- [Authored changes](/docs/concepts/authored-changes) — the rules every axis shares
- [Use the studio](/docs/guides/use-the-studio) — the walkthrough
- [Glossary](/docs/concepts/glossary)
