# Product — the eval-run journey (P4)

Task 7.1. Versioned alongside the spec deltas. This document designs the **unhappy path first**,
because on this surface the unhappy paths are not edge cases — they are the *common* outcome, and a
design that treats them as exceptions will present them as failures of the tool rather than as the
findings they are.

## 0. The one job

> *Is variant B actually better than variant A, and by how much?*

Everything on these screens exists to answer that honestly, including — especially — when the honest
answer is **"we can't tell yet."**

## 1. The unhappy paths, designed first

### 1.1 The coverage loop cannot reach the threshold

**What happened.** The generator ran its rounds and one IR edge is still uncovered. Often it is
genuinely unreachable: a branch no input satisfies, dead code discovery still sees.

**What must NOT happen.** A progress bar that reaches 100% by dropping the obligation from its
denominator. That is the single most damaging thing this product could render, because every number
downstream would then be computed over an eval set that never exercises the failing path.

**The design.** The coverage screen's headline is `83% (5 of 6 edges)` — never "complete". The
residual is named, listed, and given a reason:

```
Residual — 1 obligation the generator could not discharge

  router->branch_dead    edge    unreachable after 3 rounds
  Why: no generator produced an input that selects this branch.
  What to do: check whether the branch is dead code, or supply a hand-authored case.
```

The user's next action is offered because "unreachable" is a *finding about their workflow*, not an
error in ours. The state is **actionable-incomplete**, not **failed**.

### 1.2 The judge fails calibration

**What happened.** An LLM-judge metric's agreement against the human-labeled subset is below the
floor, or nobody supplied a subset at all.

**What must NOT happen.** The judge quietly gating variants, or its score rendering identically to a
deterministic oracle's.

**The design.** Two distinct treatments, because they are two distinct facts:

- *Below floor* — the score still renders, always with `κ 0.41 · n=40 · below floor 0.60` beside it,
  and any gate configured on it is shown **refused** at board level:
  `min_quality gate refused — judge is below its agreement floor; it disqualified no variant.`
- *Uncalibrated* — `uncalibrated · n=0`. Same refusal, different sentence.

The refusal is a **board-level note**, not a per-row badge, because it changes what the *whole board*
means: the user is looking at a ranking with one fewer hard constraint than they configured.

### 1.3 Every variant ties

**What happened.** Five variants, overlapping composite CIs, no winner.

**What must NOT happen.** Rank 1 rendered as a victory. The all-tie board is where a well-meaning UI
does the most damage: the user reads a number-one row and ships it.

**The design.** The board renders with an explicit banner:

```
No winner. All 5 variants' confidence intervals overlap on the balanced profile.
Ranks below are ordering, not evidence.
```

Rows keep their ranks (something must be printed in order) but a tied row's rank number is rendered
in muted ink with a `tie` chip, and the tie group is drawn as a shared bracket. The next action
offered is *raise N seeds* or *strengthen the eval set* — the two things that actually resolve a tie.

### 1.4 Fan-out in progress

The board is a **partial** state, not a loading state: results aggregate incrementally, so real rows
exist while the sweep runs. Rendering a spinner over readable data would be a lie in the other
direction. The design shows the rows with `142 / 250 runs complete`, and marks intervals computed
from fewer than the configured seed floor as **provisional** — they are wide on purpose and must not
be read as a ranking yet.

## 2. The happy path

```
choose variants  →  generate / select eval set  →  is it good enough?  →  run  →  compare
                                                   ▲                              │
                                                   └─── coverage loop ────────────┘
```

Step 3 — **"is this eval set good enough?"** — is a first-class moment, not a settings pane. It sits
between selecting the set and spending money running it, because that is the only point at which the
answer is still cheap to act on. It is the screen that turns "enough cases" from a vibe into a
checkable predicate, and a user who skips it is told what they skipped:

> `Running on an eval set with 83% path coverage and unmeasured difficulty. Results will be marked
> low-confidence.`

## 3. States, and why each is visually distinct

| State | What it means | Why it cannot share a treatment |
|---|---|---|
| **loading** | nothing has arrived yet | distinct from *empty* — "no data yet" ≠ "no variants configured" |
| **empty** | no variants on this board | the action is "add a variant", not "wait" |
| **error** | the board could not be computed | must name what failed; never renders a partial board as if whole |
| **partial** | fan-out in progress | real rows, provisional intervals — readable, not blocked |
| **tie** | composite CIs overlap | the rank is ordering, not evidence |
| **disqualified** | a hard gate was violated | a **separate section**, never rank N+1 — a disqualified variant is not "last", it is *out* |
| **weak-labeled** | the score rests on unreviewed references | the number is real, its evidence is not gold |
| **low-confidence** | the eval set is below a difficulty/diversity floor | the whole board is suspect, so it is a board-level banner |

The **disqualified** treatment is the one with teeth. A variant that violates the min-quality gate is
listed *below the ranked table under its own heading*, with the failed gate named. Ranking it last
would let a user scroll to it and read it as "the worst passing option"; it is not an option at all.

## 4. Terminal status is read, never derived

The board reads its aggregate status from the persisted results (`units_completed / units_planned`,
the stored coverage items, the stored calibration row). It does not maintain a client-side notion of
"probably done by now". Derived state drifts; the drift shows up as a board that says *complete*
while runs are still landing.

## 5. What the leaderboard row must carry

Every gate-passing row shows, in this order:

1. **rank** (muted when tied)
2. **variant label** + `config_hash` prefix — the lineage that makes a win attributable
3. **score ± CI** — never a bare point value
4. **component breakdown** — quality / cost / latency / reliability, raw units beside normalized
5. **gate: pass**
6. **flags** — `tie`, `weak-labeled`, `provisional`

Raw units sit beside the normalized values because a user acts on `$0.021 / 900 ms`, and understands
the ranking through `0.72 / 0.81`. Showing only one of the two makes the board either unactionable or
unexplainable.

## 6. Accessibility and scale

- Large variant lists are **virtualized** — a 500-variant board renders the visible window only.
- Rows are **keyboard-operable**: `↑/↓` move, `Enter` expands the breakdown, `Tab` reaches every
  control. Nothing is hover-only.
- Identity is never carried by color alone: every state has a text chip beside its color, and the
  Pareto frontier marks non-dominated points with a distinct shape as well as a distinct hue.
- Chart color comes from the **dataviz** palette, validated against this product's dark surface
  (`#1a2332`) rather than assumed — see `internal/api/static/p4board.html`.
