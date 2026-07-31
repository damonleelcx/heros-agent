---
title: Run an eval and read the scorecard
tier: guide
summary: Score a variant against its baseline over multiple seeds, and read the result without over-reading it.
platform_version: 0.20.0
boundary: An eval measures what your eval set covers. It reports what it did not cover rather than dropping it, and it will not tell you which variant is better when the intervals overlap.
claims: evaluate
order: 2
---

The job on this page: **turn a change you can see into a change you have a reason for.**

## Run it

```bash
heros eval --repo . --seeds 5 --cases 8
```

This calls models **with your own provider keys**, on your machine. It costs real money — see [bring your
own provider keys](/docs/guides/bring-your-own-keys) before the first run if that matters to your budget.

```
eval: discovering .…
eval: 26 nodes × 8 cases × 5 seeds via runtime "reference"…
```

*Shape captured from `heros` 0.20.0. The counts are yours.*

## Why five seeds and not one

Because one seed is not a measurement, and the tool says so out loud:

```
eval: WARNING single-seed run — reported as provisional, not a hosted-grade result
```

A single-seed result has no interval, so it cannot distinguish a real improvement from the variance you
would get by running the same configuration twice. It is useful while you are iterating and it is not
evidence. The warning exists because a provisional number, once written into a slide, stops being
labelled provisional.

## Reading the scorecard

Every metric arrives as **a value and an interval**, never a bare number. Read them in this order.

### 1 · Is there a difference at all?

If the intervals overlap, the answer is **no detectable difference** — and the product renders it as a
tie rather than picking a winner. This is the single most common thing people over-read.

A tie is a real result. It means: on this eval set, at this sample size, the change did not move the
metric enough to be distinguishable from noise. You can respond by accepting the tie, by enlarging the
eval set, or by running more seeds — but not by choosing the higher point estimate, because the point
estimate is the part the interval is telling you not to trust.

### 2 · Did anything you configured fail?

A gate you set — `--min-quality`, `--max-cost-per-run`, `--latency-sla-ms` — that fails **disqualifies**
the variant. Disqualified is not the same as ranked last:

```
eval: gate "min-quality" FAILED: …
```

A disqualified variant is excluded from the ranked order. Ranking it last would mean "measured and
worst"; excluding it means "did not qualify to be compared". The board keeps those apart because they
support different decisions.

The exit code follows: a failed gate exits `1`, not `2`. See the [exit-code
contract](/docs/reference/cli#exit-codes) — `1` is your gate biting, `2` is our tool breaking, and they
have opposite remedies.

### 3 · What was not covered?

The scorecard reports what the eval set does **not** reach, and keeps it in the denominator. A coverage
figure here is lower than one computed by dropping uncovered obligations — which is the flattering way,
and the way that deletes the evidence of the gap.

Read the uncovered list before you read the score. A 0.9 over a set that misses your hardest cases is a
worse result than 0.7 over one that does not.

### 4 · Who judged it?

Where a metric comes from a model judge, the scorecard reports that judge's **agreement with human
labels**, on every metric it produced. An uncalibrated judge's number is shown, and shown as an opinion.

It is not silently excluded and it is not silently trusted. Both of those would be easier to render and
both would hide the thing you need in order to weigh the number.

## Where the run goes

```
eval: run run-7 stored under ~/.heros/runs (link it with `heros link run-7`)
```

The run is stored **locally**. Nothing has been transmitted. `heros link` is a separate, explicit,
authenticated act — and `heros link --run your-run-id --dry-run` prints the exact payload without sending it, so
you can read what would leave your machine before any of it does.

## Cost, before you are surprised by it

`--seeds` × `--cases` × nodes is the number of model calls. The defaults (5 seeds, 8 cases) are chosen to
give an interval worth reading, not to be cheap. Halve the cases before you halve the seeds: fewer cases
narrows what you measured, fewer seeds widens the interval on everything.

The per-call figures the platform records are documented in the [metric
reference](/docs/reference/metrics), including exactly what each one measures and where it is computed —
`cost_usd`, for instance, is priced from the provider's published rate in
`internal/telemetry/pricing.go`, and a model with no pricing entry emits **no cost at all** rather than a
zero.

## Common surprises

**Every variant tied.** The usual cause is an eval set too small to separate them. More cases before more
seeds, if the cases are the thing that is thin.

**The score went up and the gate failed.** Both can be true: gates are absolute thresholds, not
comparisons. A variant can be better than the baseline and still not good enough.

**The run cost more than expected.** Node count multiplies. Twenty-six nodes at 8 cases and 5 seeds is a
thousand calls before you have read anything.

## Next

- [Wire this into CI](/docs/guides/wire-ci) so it runs on every pull request.
- [Take delivery as a pull request](/docs/guides/take-delivery) once a change has a verified delta.
