---
title: Wire it into CI
tier: guide
summary: Run discovery and evaluation on every pull request, post a check, and keep the build green when the platform is not.
platform_version: 0.20.0
boundary: CI runs the local workflow and posts a check. It does not merge, does not push, and does not fail your build because our platform is unavailable — only a gate you configured can fail it.
order: 3
---

The job on this page: **make the evidence arrive on the pull request, without making your build depend on
us.**

## The one rule this integration is built around

**Platform unavailability must never fail a customer's build.** A gate *you* configured failing is a
legitimate red build. Our service being down is not — and a CI step that goes red for reasons the team
cannot act on is a CI step that gets `|| true` appended to it within a fortnight, after which real
failures are invisible too.

Everything below follows from that.

## Use the published workflow

Reference it by version from your own pipeline:

```yaml
jobs:
  heros:
    uses: heros-foreal/agentd/.github/workflows/heros-eval.yml@v0
    with:
      eval-args: "--min-quality 0.7 --seeds 5"
    secrets:
      platform-token: ${{ secrets.HEROS_PLATFORM_TOKEN }}
```

Referencing by version is deliberate. A snippet copied into two hundred pipelines cannot be fixed; a
workflow referenced by a tag can — a fix reaches every consumer when the tag moves.

### Or the composite action, if you need the steps

```yaml
- uses: heros-foreal/agentd/.github/actions/heros@v0
  with:
    repo: "."
    eval-args: "--min-quality 0.7 --seeds 5"
    platform-token: ${{ secrets.HEROS_PLATFORM_TOKEN }}
```

## Running with no account at all

**Omit the token.** The whole local workflow — discover, eval, gates, the check, the artifacts — runs
with no platform token and publishes nothing:

```yaml
    with:
      eval-args: "--min-quality 0.7"
    # no secrets block
```

This is worth doing first. It is the honest way to evaluate whether the signal is useful to your team
before anyone signs anything, and everything except linking works identically.

## Permissions

The workflow grants exactly two:

| Permission | Why |
|---|---|
| `contents: read` | To check out and read the repository. |
| `checks: write` | To post the check with the outcome. |

It does not ask for `contents: write` and does not ask for `pull-requests: write`. It cannot push a
branch or open a pull request, and that is a property of the grant rather than a promise about behaviour.
Delivery is a [separate, deliberate step](/docs/guides/take-delivery).

## What arrives on the pull request

- **A check**, with the outcome and which gate (if any) bit.
- **Artifacts**: the IR and the run report, uploaded so a reviewer can open the evidence rather than
  trust a summary.

Credentials come from the CI secret mechanism and are never echoed to logs, to the check, or into an
artifact.

## Choosing gates that will not be disabled

Set gates that describe a **regression you would genuinely block a merge over**. Two failure modes, both
common:

- **Gates too tight.** Everything is red, the team stops reading the check, and the one real regression
  arrives in a sea of noise.
- **Gates absent.** Everything is green, the check becomes decoration, and it is eventually deleted for
  being slow.

`--min-quality` is the one most teams want first. Add `--max-cost-per-run` when somebody asks what this
costs, which they will.

Remember the exit codes: a gate you configured exits `1`; the tool breaking exits `2`. If your pipeline
branches on them, `1` should fail the build and `2` should page whoever owns the integration — see the
[exit-code contract](/docs/reference/cli#exit-codes).

## Timing and cost, honestly

Each run calls models. Seeds × cases × nodes multiplies quickly, and on a busy repository this is the
part of the bill people do not predict. Two things worth doing before you turn it on for everyone:

- Run it on one branch for a week and look at the total.
- Set `eval-args` explicitly rather than taking defaults, so the number is a decision.

## What it will not do

- **It will not merge anything.** Nothing in this integration has write access to your code.
- **It will not fail on our account.** Platform unavailability is non-fatal by construction; linking is
  optional and its failure does not change the local result.
- **It will not publish anything without a token.** With no token there is nothing to publish to.
