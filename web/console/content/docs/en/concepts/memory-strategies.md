---
title: Memory strategies
tier: guide
summary: What a node carries between invocations, the five registered strategies, and exactly where a memory change reaches your source and where it does not.
platform_version: 0.20.0
boundary: This page explains the axis and states its boundary. It changes nothing. Your own node's current strategy, and the picker that changes it, are on the memory surface in the console.
order: 5
---

Memory is what a node keeps **across** invocations. It is not how one call assembles its message list —
that is the [context](/docs/concepts/context-policies) axis, and the two are confused often enough to be
worth separating on the first screen.

## Memory is not context

| | Context | Memory |
|---|---|---|
| **Scope** | one invocation | across invocations |
| **The question it answers** | what does this call read? | what does this node remember? |
| **Where it lives in your code** | how the message list is built at the call site | a store the node reads from and writes to |

A node can have both, neither, or one of each. They hash separately and are scored separately.

## The five strategies

Only registered strategies resolve. A name outside this set resolves to nothing, so offering free text
would be offering a choice that fails the moment it is sealed.

| Strategy | What it trades away | Parameters |
|---|---|---|
| `none` | The node carries nothing across invocations. Every call starts from the same blank state — the baseline the other four are measured against. | — |
| `entity-memory` | Structured facts about named entities, and nothing else. The narrowest strategy, and the only one whose loss is readable from the configuration: it carries exactly the keys it declares. | `entity_keys` (required) |
| `scratchpad` | Recent working notes, kept verbatim and bounded by count. The oldest note is dropped whole rather than summarized, so what survives is exact and what is lost is gone. | `max_entries` (required) |
| `summary-buffer` | Everything older than the retained tail is folded into a rolling summary. Cheapest in tokens and the most lossy: what the summary omits cannot be recovered from the configuration. | `max_tokens` (required), `keep_last_turns` |
| `vector-recall` | Prior turns are retrieved by embedding similarity rather than recency. Pays a retrieval step to avoid carrying everything, and is only as reproducible as the embedding it pins. | `top_k` (required), `embedding_ref` (required) |

`none` is the identity strategy. It changes nothing, so nothing is refused — and selecting it is
indistinguishable from clearing.

## What reaches your source

The memory runtime has landed. **Python and Go** call sites materialize it; a language without one is
waiting for that language's emitted memory module and the call-site rewriter that calls it, and is
refused by name rather than quietly applied.

Two preconditions hold even where it does apply, and you should meet them before choosing rather than
after:

- The call site must **write its message list** and **assign the call's result to a name**. Memory is a
  read *and* a write, and a call site that can carry only one is refused whole, naming which half.
- Go materializes a strict **subset** — `none` and `scratchpad` only — and the reason is permanent
  rather than owed. A Go message is your SDK's own type, so generated code cannot read its text without
  importing your SDK, which means the strategies that decide what to keep *by reading content* cannot
  run there. Python's messages are dicts, so all five work.

There is no plan, role or flag that changes any of this. The controls on the console stay usable
everywhere, because a disabled control would tell you none of the above.

### Why a refusal is the safe half

A memory override ends **applied**, **refused** or **equivalent**, and never in a fourth state — the
same three the other axes end in, for the same reason:
[a refused change is never a dropped one](/docs/concepts/refusals#a-refused-change-is-never-a-dropped-one).

## What an unapplied change is worth

Selecting a strategy resolves, hashes, seals a registry entry, records who authored it, and diffs
against the parent variant. That is a real configuration you can pin, compare and hand to a colleague —
and it materializes unchanged the day the rewriter lands.

Withholding all of that because a codemod is missing would confuse *"we cannot write this into your
source"* with *"you may not express this"*.

## Proposals on this axis

The platform can propose a memory change too. A proposal here carries **no result** and will not until
the rewriter for that language lands: a proposed strategy that cannot be materialized cannot be
evaluated, so it is offered as an intent to pin rather than as a measured improvement.

## Worked example

**The platform's own fixture**, not your repository. The demonstration node is called `recall` — a
Python call site that writes its message list and assigns the call's result, so both halves land.

Selecting `scratchpad` with `max_entries = 20` on that node produces a new `config_hash` distinct from
its parent. A different strategy, or a different parameter, is a different configuration and a different
hash: two variants that differ only in what a node remembers between turns are scored separately,
exactly like two that differ in their model.

Clearing removes the override entirely rather than setting it to a default. The key disappears from the
node, so the configuration returns to *exactly* the bytes it had before — same `config_hash`, no
residue.

Leaving a required parameter empty is refused before anything is stored:

```text
memory entry "scratchpad": params violate the strategy's schema: missing property 'max_entries' —
rejected before the entry is stored, so no version_id is minted for content that was never written
```

An id for content that was never written is an id a spec could reference forever without resolving.

## Where to go next

- [Context policies](/docs/concepts/context-policies) — what one call reads, which is the other axis
- [Authored changes](/docs/concepts/authored-changes) — the rules every axis shares
- [Refusals are outcomes, not errors](/docs/concepts/refusals) — and [why a refused change is never a dropped one](/docs/concepts/refusals#a-refused-change-is-never-a-dropped-one)
