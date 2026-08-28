---
title: Delivery routes
tier: guide
summary: The two ways a change reaches a running agent, the six route states, and why most changes are refused by both routes with a stated reason rather than sitting in a queue.
platform_version: 0.20.0
boundary: A human merges. The platform never merges below the Autonomous level, and a rollout is never a merge. This page explains the routes; your own deliveries and your own undeliverable count are on the delivery surface in the console.
order: 9
---

Two questions live on this axis, and the second is the one most people arrive with:

- *What reached my repository?* — the pull requests, their outcome, the route condition.
- *How does a change reach it at all?* — the route ledger.

The second matters because the honest answer is usually **it does not**. Read against the coverage
tables, most cells have no materializer. When the rewriter refuses there is no diff, so there is no pull
request, so nothing appears in the delivery list — and an empty list is exactly what *"nobody has gotten
to it yet"* looks like. The ledger turns that silence into a stated reason with an owner.

## The two routes

Every change shows **both routes side by side, always**. A change both routes refuse reads as
`undeliverable` — a word with no hopeful synonym.

| Route | What it carries | What it costs |
|---|---|---|
| **Runtime** | a bound node's values, swapped under real load without touching source | the node must already apply in `bound` mode |
| **Source** | a pull request against your repository | a codemod for that language and call-site form |

Delivery used to render as one word per change: *delivered*, or *pending*. That word had no room for the
thing that is true most of the time — the rewriter refused, so there is no diff, so nothing is going to
happen. On screen that state was indistinguishable from *"queued behind other work"*, which is how a dead
end came to look like a promise.

## A rollout is not a delivery

A gradual rollout runs inside your own process, assigns each unit of work to an arm deterministically,
expires to the parent arm, and reverts itself if a guard trips — **without reaching the platform**.

It never merges anything. Permanence still costs a codemod, a pull request, and a human.

A rollout is evidence. It is not delivery.

## Whose move a refusal is

A refusal's next action differs completely depending on whose move it is, and **only one of the three is
a wait**.

| Whose move | What it means | What you do |
|---|---|---|
| nobody's | program structure, not data. There is no *"when"*. | stop asking — this one cannot be built |
| yours | the node applies inline; a bound migration would unlock it | migrate the node to `bound` |
| ours | the document has no field yet, and the missing piece is **named** | wait, and the name tells you for what |

The permanent one may never acquire an artifact or a date. A boundary that starts rendering like a
backlog item is how *"we will never do this"* becomes *"we have not done this yet"*, and the product ends
up promising something that cannot be built. `permanent` is a boolean on the wire, and it — never a
sentence — selects the treatment.

## Delivery states

A closed set, with no *"pending"*:

| State | What it means |
|---|---|
| `delivers` | this route carries this change today |
| `varies` | the source route can carry any diff, but whether a diff **exists** is decided per language and per call-site form |
| `boundary` | permanent — nobody's move |
| `contingent` | waiting on a runtime component that could exist |
| `migration` | the node is not bound; migrating it would unlock this route |
| `gap` | ours, and the missing artifact is named |

`contingent` is checked **before** `boundary`. Memory and wiring both refuse as *"not data"* and carry
the same cause, but wiring is a property of compiled code while memory waits on a runtime component that
could exist. A reader who cannot tell them apart draws the wrong conclusion from one of them: either they
stop asking about something merely unbuilt, or they keep waiting on something that cannot be built.

### Why the hazard palette is not used here

`warn` and `danger` are reserved for hazard. A refusal is an **answer**, not a hazard, and spending the
hazard colour here would make it stop meaning anything where it matters. The states are separated by
border weight and style, so they stay distinguishable without colour.

## Where the source actually comes from

The source route's answer depends on what the platform can read, and there are three ways it can:

- a **pushed bundle** — you sent a snapshot;
- a **connected repository** — the platform clones at a named revision under a narrow, revocable grant;
- a **local machine** — the source never leaves it, and the verdicts are computed there.

Each answers the pull-request question differently, and the ledger says which one this workflow is on.

## The one number this axis exists to produce

**`undeliverable`** — how many of *your* reported nodes cannot receive a change by **either** route.

A node the platform was not told about is **not** counted as undeliverable. That would be a claim about
your code drawn entirely from our own ignorance, and it is exactly the number somebody would act on — so
the denominator is the number of **reported** cells, and it is printed beside the count.

## Where to go next

- [Refusals are outcomes, not errors](/docs/concepts/refusals)
- [Authored changes](/docs/concepts/authored-changes)
- [Take delivery](/docs/guides/take-delivery) — the guide to actually receiving one
