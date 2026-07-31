---
title: Refusals are outcomes, not errors
tier: guide
summary: A refusal is a designed result with a name and a reason. Meeting one for the first time in production reads as a bug; meeting one here reads as the design.
platform_version: 0.20.0
boundary: This page explains what a refusal means and how to act on one. It is not a troubleshooting list — a refusal names its own cause, and a page that restated all of them would drift from the ones the binary actually emits.
order: 1
---

Most tools have two outcomes: it worked, or something broke. This one has three, and the third is the
one worth understanding before you meet it.

| Outcome | What it means |
|---|---|
| A result | The thing was done, and here is the evidence. |
| An error | Something broke. Retrying might help. |
| **A refusal** | The thing was **not** done, on purpose, and here is which of a small set of named reasons applies. |

A refusal is not a failure to compute. It is a computed answer whose content is *"I will not tell you
that, and here is exactly why"*.

## Why this design exists

Because the alternative is worse in a specific and expensive way.

Every refusal in this product replaces a plausible answer that would have been **wrong in a way you could
not see**. An unlabelled region of a workflow graph could have been labelled with the closest-matching
pattern. Two overlapping confidence intervals could have been ranked. A call site whose source cannot
carry a configuration value could have been rewritten with a guess.

Each of those would have produced a result that looks exactly like a good one. The refusal is the product
declining to spend your trust on something it did not establish.

## The shape of every refusal

Three parts, always:

1. **What was not done.**
2. **Which named reason applies** — from a closed set, so the same situation always produces the same
   word rather than the same paragraph rewritten.
3. **What would change the answer** — or an honest statement that nothing you can do will, because the
   limit is in the world rather than in your configuration.

The third part is the one that makes a refusal actionable. A refusal that only says no is a wall; one
that says which of three things is missing is a next step.

## Seeing them all at once

The clearest way to meet refusals is to ask for the whole map before you need it:

```bash
heros coverage
```

This prints what the build can apply, per axis and per language. Every registered language appears on
every axis — **a gap is a cell, not an absence** — and each cell that refuses says which of three things
is missing.

```
coverage cov-c19cf0c4 — what this build can apply, per axis and language
🔴 a gap is a CELL, not an absence: every registered language appears on every axis, and a
   refusal names which of three things is missing —
     not-expressible-at-a-call-site     the value does not exist in source in ANY language
     call-site-cannot-carry-it          this call site's own source cannot express it
     no-materializer-for-this-language  the platform has not landed the artifact (named below)
── model ──
  go          openai.chat.completions.new        applies
  java        java.bedrock.converse              refuses: call-site-cannot-carry-it
  javascript  js.vercel.generatetext             refuses: call-site-cannot-carry-it
```

*Captured from `heros` 0.20.0 on 2026-07-31. Truncated: the full matrix reported 128 cells that apply
and 123 that refuse by name, across Go, Java, JavaScript, Kotlin, Python, Rust and TypeScript.*

### Why the three causes are kept apart

They have completely different remedies, and collapsing them into "unsupported" destroys the only useful
information in the message.

| Cause | What it means | What you can do |
|---|---|---|
| `not-expressible-at-a-call-site` | The value does not exist in source **in any language**. It is not a gap in our coverage — there is nothing at a call site to change. | Nothing. This will not arrive in a later release. |
| `call-site-cannot-carry-it` | **Your** call site's source cannot express it — the value is computed, passed through a wrapper, or hidden behind `**kwargs`. | Change the call site so the value is expressible, and it applies. |
| `no-materializer-for-this-language` | We have not built it yet for this language. | Nothing today. This one **is** a roadmap item, and it says so rather than implying your code is at fault. |

The middle row is the important one. Told "unsupported", you would go and wait for a release that was
never going to fix it. Told `call-site-cannot-carry-it`, you know the change is in reach and it is yours
to make.

## Refusals you will meet elsewhere

**A change that is refused rather than applied.** `heros author` answers with one of three verdicts —
admissible, refused by name, or not yet measurable. The third is not a softer refusal: it means the
change might be fine and nothing has measured it, which is a different statement from "this is wrong".

**A tie, where you expected a winner.** When two confidence intervals overlap, the result is a tie and
the product renders it as a tie. It will not tell you which variant is better when the measurement cannot
say. This frustrates people exactly once, usually while they are about to ship the wrong one.

**A variant that is disqualified rather than ranked last.** A variant that fails a gate you configured is
**excluded from the ranked order**, not placed at the bottom of it. Those are different claims: last
means measured and worst; excluded means it did not qualify to be compared.

**An unlabelled region of a graph.** It means no structural signature matched and no model returned
anything in the taxonomy. It does **not** mean the workflow implements no patterns there, and the
product says so on the screen rather than leaving a blank that reads as "nothing here".

## How to act on one

1. **Read the name, not the sentence.** The name is from a closed set and is the same every time. The
   sentence around it is there to be readable.
2. **Check which of the three shapes it is** — your code, our coverage, or the world.
3. **Do not retry.** A refusal is deterministic. The same input produces the same refusal, and a retry
   loop around one is a loop that never exits.

That last point is worth its own line, because it is the mistake automation makes. An error is worth
retrying. A refusal never is.

## What this page does not do

It does not list every refusal the product can emit. That list would be a copy of the truth, and it would
be wrong the first time a new one shipped — so each refusal carries its own name and reason, and
`heros coverage` prints the whole current map on demand. What this page gives you is how to read one.
