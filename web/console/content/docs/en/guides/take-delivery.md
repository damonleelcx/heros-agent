---
title: Take delivery as a pull request
tier: guide
summary: Get a verified change into your repository as a pull request a person reviews — opened by your own CI, with no standing write access on our side.
platform_version: 0.20.0
boundary: The platform opens the pull request. It does not merge it. Autonomous merging exists as a governed, budgeted and audited option; it is off by default and nothing here holds a token to your repository until you turn it on.
claims: pull-request, ci-mediated
order: 4
---

The job on this page: **get a change that has evidence behind it into your repository, through the review
process you already have.**

## The credential posture, first

The default path is **CI-mediated**: your own CI opens the pull request, using the token your CI already
has. The platform never holds a write credential to your repository.

This is the part worth understanding before the mechanics, because it is what makes the rest
uninteresting from a security review's point of view:

| | Who holds a repository token | What they can do with it |
|---|---|---|
| **CI-mediated** (default) | Your CI, as it already does | Whatever you already granted it |
| Hosted Git App (opt-in, **off**) | The platform | Open pull requests on repositories you connect |

If you never turn the second one on, there is nothing on our side that can write to your code — not
because of a policy, but because no such credential exists.

## What has to be true before a change is delivered

A proposal is opened as a pull request only when **both** hold:

1. **The verdict passed.** There is a [verified delta](/docs/concepts/glossary) — a measured difference on
   a held-out set, with its confidence interval. A proposal that fails its gate is withheld and labelled,
   not quietly dropped and not shown as a recommendation.
2. **The workflow is at Assisted automation.** At Advisory, nothing is delivered; you read the evidence
   and act on it yourself.

Both are refusals in the useful sense: they say what is missing rather than doing something plausible.

## What the pull request contains

The body is a **published contract**, so automation that greps it keeps working. Its first line is a
machine-readable marker, and its sections are fixed and ordered:

1. **Summary** — the proposal and its automation level.
2. **Verified delta** — the held-out metric delta with its 95% confidence interval, rendered **as
   computed**. An interval whose low bound sits at or below the baseline reads as a **tie**, never as an
   improvement.
3. **Held-out status** — `held-out` or `not held-out`, verbatim from the verdict.
4. **Eval evidence** — cases fixed and broken, cost and latency deltas.
5. **Lineage** — the `config_hash` and the `source_revision`, so the change is traceable to the exact
   configuration and commit that produced it.
6. **Evidence in the console** — one canonical link that opens the full evidence, and resolves from
   anywhere it is pasted.

Point 2 is where a delivery pipeline usually oversells. A change with a real point estimate and an
interval crossing zero is reported here as a tie, in the pull request, in the sentence a reviewer reads
first.

The body never contains a credential — on the success path or the failure path.

## Reviewing one

Read the verified delta before the diff. The diff tells you what changed; the delta tells you whether
anybody has reason to believe it helps, and on what evidence.

Then check three things:

- **Held-out or not.** A delta measured on the same data the change was selected against is not evidence
  of generalization, and the body says which it is.
- **Cases broken.** A change that fixes six cases and breaks two is a trade, not an improvement, and the
  counts are there so you can make that call.
- **The lineage.** If the `source_revision` is behind your branch, the measurement describes code that
  has moved.

## Merging

**You merge it.** The platform opens the pull request and stops.

Merge is also the moment that matters commercially: gainshare is billed against **merged** pull requests
only, and `merged` in the delivery record is an **observed fact** read back from the forge, not inferred
from a pull request being closed. Closing one without merging bills nothing.

## Idempotency

The same change, for the same target, produces the same delivery — the record is keyed on the
configuration, the source revision and the target, so a retried pipeline does not open a second pull
request. Every delivery and every state change is a new row in an append-only record, so the history can
be reconstructed in order.

## What this does not do

- **It does not merge.** Autonomous merging is a separate, governed, budgeted, audited option that is off
  by default.
- **It does not push to your default branch.** Delivery is a branch and a pull request.
- **It does not deliver an unverified change.** Both conditions above are checked at the point of
  delivery, and failing either is a refusal with a name.
