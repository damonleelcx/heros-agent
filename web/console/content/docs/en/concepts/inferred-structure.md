---
title: Inferred structure
tier: glossary
summary: Some edges on your graph were proposed by a model rather than read out of your source — here is how to tell which, who ran the analysis, and how to switch it off.
platform_version: 0.20.0
boundary: An inferred edge is a hypothesis about your workflow, not a reading of it. Nothing here claims the graph is complete or correct, and no inferred fact ever overrides one a parser established.
order: 3
---

The job on this page: **know which parts of your graph were measured, which were guessed, whose machine
did the guessing, and how to turn it off.**

## The short version

- Your graph is built by **parsers** that read your source. For some languages they cannot follow a
  value from one statement to the next, so they find your call sites and no dependencies between them.
- Where that leaves a gap, an **analysis agent** can propose the missing edges. Every fact it proposes
  is marked `inferred` and carries the exact agent version that produced it.
- 🔴 **An inferred edge never overrides a measured one.** The agent is not shown the pairs your parsers
  already answered, and an answer naming one is recorded and discarded.
- It is **off by default** for every organization. If your graph has no inferred facts, nothing has been
  guessed.

## Four states, and why they are four

Everything on the graph and the composition panel is one of these. They are kept distinct on purpose:

| State | What it means |
|---|---|
| **measured** | A language parser or a rule detector established this by reading your source. The strongest thing this platform says. |
| **inferred** | The agent proposed it and it cleared a confidence floor. A lead, not a finding. |
| **not analysed** | Nothing has looked. On most organizations this is everything, because analysis is off by default. |
| **unavailable** | Something looked and could not answer — a fault or a limit on our side. |

The last two look identical on a page — both are an absence — and they are opposite facts. One is work
outstanding; the other is a problem at our end. The console says which, so you know whether to wait or
to ask us.

## Reading the graph

An inferred edge is drawn in its own colour **and** with its own dash and arrowhead, so the distinction
survives a greyscale print or a projector. The legend names it. The edge table below the drawing carries
a *How we know* column with the same information, so nothing is lost if you read the table instead.

Where a count mixes the two, both numbers are shown: *"19 edges — 4 of them inferred, not measured"*.
🔴 A single undifferentiated number would read as measured, and that arithmetic is how a hypothesis
quietly becomes a fact.

## The composition panel

Above the pattern cards, *"What this workflow is made of"* lists every pattern present, how many nodes
it covers, how it is known, and what established it. Below the table is the remainder — the nodes no
pattern covers at all, stated rather than left as a subtraction.

A pattern reads **inferred** only when *every* label contributing to it came from the agent. One rule
detector among three agent proposals still means something read your source and found the pattern.

## The assessed paragraph

When the agent writes prose about your workflow it appears marked **assessed**, in its own treatment.
It is an assessment, not a measurement, and nothing on this console dispatches off it.

🚫 If the agent wrote nothing, **nothing appears**. There is no generated summary standing in for one —
prose assembled from your counts would look assessed while having been written by a template.

## Whose machine ran it

The panel names the placement, and the two mean different things about your source:

- **`platform`** — we ran the analysis, on our infrastructure, under our own provider credential. That
  means we read your source to do it.
- **`customer`** — you ran it, on your machine, with your own provider key, and submitted the result.
  Your source never reached us, and we never hold your key.

If your source may not leave your network, `customer` is the supported answer. Run:

```
heros analyse --ir ir.json
```

against the IR `heros discover` wrote. The command fetches the platform's own agent definition, runs it
locally under your key, and submits what it found through the same opt-in channel `heros link --with-ir`
uses.

## Turning it off

Ask us to set your placement to `disabled`. Then:

- No analysis runs for you, anywhere.
- Every surface returns to the facts your parsers established.
- 🔴 **Your existing inferred facts are kept and marked stale**, not deleted. They were true when they
  were written, nothing is maintaining them any more, and the graph says so. Deleting them would mean
  paying twice if you switch back on, and would make "which of these edges did the model write" an
  unanswerable question during an incident.

Switching back on clears the mark. It does **not** re-run anything: the stored facts are still the ones
written before the gap.

## What it does not do

- **It does not learn across analyses.** Its memory lasts one analysis and is discarded with it, so a
  repository analysed twice starts cold both times. That is a deliberate scope: memory carried between
  analyses would make your graph depend on what we happened to analyse first, and the same revision
  would stop reliably showing you the same graph.
- **It is not shown your whole repository.** It sees the gap — the node ids, the pairs no parser
  connected, and which parsers ran — and nothing else. Not your prompts, not your source.
- **It cannot invent a node.** Its answers are validated against the ids already in your graph, so the
  worst a hostile instruction hidden in your source achieves is a wrong edge, which the confidence floor
  and the marking already cover.
