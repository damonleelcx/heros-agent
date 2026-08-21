# Spike — P31 intent routing, calibrated against a holdout

- **Date:** 2026-08-21
- **Owns:** [P31](../prd/P31-conversational-console.md) §6.7 (FR24), design.md D5 and D9, tasks 3.1–3.8
- **Reproduce:** `make intent-holdout`
- **Code:** [`internal/conversation/router.go`](../../internal/conversation/router.go),
  [`internal/conversation/holdout.go`](../../internal/conversation/holdout.go),
  [`internal/conversation/testdata/holdout.json`](../../internal/conversation/testdata/holdout.json)

> 🔴 **This document is the record task 3.5 requires, and it must be updated on every subsequent change
> to the router or the term table. There is no pure-refactor exemption.** A change that "cannot possibly
> alter behaviour" still has to produce the fourteen rows and show them unchanged — because the last time
> somebody was sure about that in this repository, `scoring_mirror`'s golden test had never actually run.

---

## What the spike was for

Deciding **how** the conversational console routes a sentence to one of fourteen intents, and **where the
abstention threshold sits**. Both had to be decided with evidence, because the failure this router can
have is invisible from the inside: a wrong route produces a well-formed, confident answer about something
else, and nothing on the screen says so.

## The question the metric had to answer

Not *"how accurate is it?"* — that is the number that hides the defect. The two questions that decide
whether this surface is safe to ship are:

1. **Is any single intent broken?** A mean over fourteen intents can sit at 93% while `coverage` — the
   intent that answers *"what did you not measure"* — is routed correctly one time in three.
2. **When it declines, is it right to decline?** An abstention is a visible failure; a misroute is an
   invisible one. They must not be traded off against each other in one number.

So the report has **fourteen rows plus two precision figures and no total.** `Report` has no `Accuracy`
field and offers no way to compute one; a caller who wants a mean has to write the arithmetic in the
open, where a reviewer can see it happen.

## Approach chosen: a deterministic lexical scorer

**Rejected: a model call.** Three reasons, in the order they decided it.

| | |
|---|---|
| **FR11 and the pin** | A repeated question must cost nothing. A model call in the router spends money *before* the pin is consulted, which puts the spend back on the path determinism exists to remove. |
| **Calibration needs a stable score** | A threshold means something only if the score means the same thing twice. A model's self-reported confidence is not that. |
| **Falsifiability** | Every decision here is a function of the sentence and one file, so a holdout run is reproducible — which is what makes "no pure-refactor exemption" enforceable rather than aspirational. |

**Scoring.** Per-intent weighted substring matches, then

```
absolute   = top / (top + 4)                 how much evidence there is at all
margin     = (top − runner_up) / top         how much better the winner is than the next
confidence = 0.6·absolute + 0.4·margin
```

Both halves are load-bearing. Absolute alone routes a sentence with one weak term hit. Margin alone
routes a sentence that matched two intents *equally badly* but one slightly less badly — which is exactly
the adjacent-intent failure a confident wrong route hides.

**Threshold: `AbstainThreshold = 0.34`,** a constant rather than configuration, for the same reason
`StepReEntryCeiling` is: a threshold an operator can lower gets lowered the first time somebody complains
the agent refuses too often, and lowering it does not make the router better — it makes its failures
invisible again.

## The holdout

76 questions. 62 labelled across the fourteen intents, 8 that must abstain, 7 that must be refused **by
name** with the surface that performs them (FR26), and **13 near-miss questions** — one per intent that
is *almost* another (`context` vs `memory`, `graph_order` vs `graph`, `assess` vs `improve`, …). The
near-miss subset is scored on its own denominator, because `context` can sit at 100% overall while the
one question that is almost `memory` is wrong, if the other four are easy.

## Result

```
intent          labelled  correct  misrouted  abstained   recall   near-miss
─────────────── ────────  ───────  ─────────  ─────────  ───────   ─────────
graph                  5        5          0          0   100.0%      1/1
run_history            5        5          0          0   100.0%      1/1
compare                5        5          0          0   100.0%      1/1
preview_change         4        4          0          0   100.0%      1/1
deliver                4        4          0          0   100.0%      1/1
prompt_model           5        5          0          0   100.0%      1/1
author                 3        3          0          0   100.0%        —
graph_order            5        5          0          0   100.0%      1/1
context                4        4          0          0   100.0%      1/1
memory                 4        4          0          0   100.0%      1/1
harness                4        4          0          0   100.0%        —
coverage               5        5          0          0   100.0%      1/1
assess                 4        4          0          0   100.0%      1/1
improve                4        4          0          0   100.0%      1/1

abstention precision    100.0%   (15 of 15 abstentions were correct)
redirection recall      100.0%   (7 of 7 out-of-scope questions named their surface)
```

## 🔴 What this result does NOT mean, stated because 100% invites the wrong reading

**The first run was not this.** It was:

```
prompt_model  80.0%   author  66.7%   context  75.0%       abstention precision 83.3%
```

Three questions abstained that should have routed, and **all three failures were abstentions rather than
misroutes** — the safe direction, and the direction D5's design predicts.

**Three term weights were then changed, after seeing those failures.** They are recorded here because a
reader is entitled to know the numbers above are optimistic:

| Change | Why it is a discrimination argument and not a fit to the fixture |
|---|---|
| `preview_change`: **removed `"diff"`** | A diff is rendered on `preview_change`, on `author`, and on a proposal card. It is the most *related* word in the product and one of the least *discriminating*. Weighting it tied "I want to edit this and see the diff" against `author` → abstain. |
| `author`: **removed `"i want to change"` and `"change something"`** | These are **phrasings, not subjects**. A person says "I want to change the instruction" about `prompt_model` and "I want to change the order" about `graph_order`. Weighting a phrasing put `author` in a tie with whatever the sentence was actually *about*, and a tie abstains — so the failure was a refusal on a perfectly clear question. |
| `memory`: **`"between calls"` 4.0 → 2.5** | It reads like a memory phrase because the PRD's memory question contains it, but *"what context does this node get between calls"* is an equally natural **context** question. It is a temporal qualifier the two axes **share**, and weighting a shared qualifier as a discriminator is precisely how adjacent intents collapse. |

**Consequences a future reader must hold on to:**

- Those three weights are **calibrated on** this set, so it is no longer a clean holdout for them. The
  fourteen rows are an upper bound, not an unbiased estimate.
- **Add questions before changing the router again.** The correct next move for anybody touching the term
  table is to extend `holdout.json` first, from questions real users actually typed, and only then tune.
- The set is **76 questions authored by one person**. It covers the phrasings that person imagined. The
  in-product signal that matters more is the `console.conversation.refused` rate — a rising abstention
  count is the router telling you the holdout is unrepresentative.

## What the fences catch, and what they cannot

| Fence | Catches | Cannot catch |
|---|---|---|
| `TestEveryIntentHasHeldOutQuestions` | an intent nobody measured (`Recall() == -1` skips every threshold, so this is what stops the rest passing vacuously) | whether the questions are representative |
| `TestEveryIntentClearsItsOwnRecallFloor` | one broken intent, individually | a router tuned to this set |
| `TestAMisrouteIsWorseThanAnAbstention` | a router that guesses instead of declining | — |
| `TestAdjacentIntentsAreRoutedCorrectly` | the near-miss subset, on its own denominator | pairs nobody thought to write |
| `TestTheThresholdActuallyBinds` | a threshold set to zero, under which every recall assertion still passes | — |
| `TestIntentSetEqualsTheWorkingSurfaceSet` (§6.15) | **structural** drift between intents and routes | that an intent routes *well* — two different failures, neither substituting for the other |

## Open

- **PRD §14 Q3 — clarification.** Ratified as *at most one clarification, from a closed set of
  disambiguations, never free-form*. The closed-set half is **not implemented**: the router abstains
  today rather than offering a disambiguation. That is the conservative direction (a refusal naming what
  the surface can do), and the missing capability is recorded here rather than in a comment nobody reads.
