# UI verification record — P4 eval board (task 8.7)

Driven in Chrome against `make p4-board-demo`, which is a **live fan-out**: the eval set comes from
the gap-filling loop, the runs go through the bounded worker pool, the evaluators score real
telemetry spans, the results land in the tagged store, and the board is the computed view over the
score cache. The only stub is the provider.

Reproduce:

```
make p4-board-demo                          # 4 variants
go run ./cmd/p4boarddemo -extra-variants 80 # 84 variants, for the virtualization check

# The board moved to the console in the P9 cutover; the demo serves the read model, the console
# serves the screen.
cd web/console && PLATFORM_API_BASE=http://127.0.0.1:8085 \
  CONSOLE_PLATFORM_CREDENTIAL=demo CONSOLE_TENANT_IDENTITY=dev npm run dev
open http://127.0.0.1:4320/app/workflows/wf-router/board
```

> **Note (2026-07-24).** This record was written against `static/p4board.html`, which the P9 cutover
> removed together with its handler. The observations below stand as the record of what the live
> fan-out produced; the *route* changed, and the commands above are the current way to reproduce it.
> Every behaviour the record checked is now a case in
> [`web/console/tests/inventory.test.mjs`](../../../web/console/tests/inventory.test.mjs) (**P4-1…44**).

## What the live fan-out produced

```
eval set 904da8f15190: 17 cases · path 100%/100% node 100%/100% edge 100%/100% (1 iteration, 0 residual)
placement: 8 of 17 cases routed to the P3 sandbox
fan-out: 340/340 units · 6300 result rows · spend $6.3694
```

## States confirmed in the browser

| State | How it was produced | What rendered |
|---|---|---|
| **complete** | full fan-out | leaderboard, Pareto, coverage, spend |
| **partial** | budget cap fired at 2219/7140 units | `Fan-out in progress` banner + `58 of 84 variants could not be scored`, rows still readable |
| **error** | `?workflow=no-such-workflow` → 404 | error banner alone; **every board section hidden** |
| **empty** | no variants | "No variants on this board yet" |
| **tie** | 3 statistically indistinguishable variants | `No winner` banner + muted ranks + `tie` chip per row |
| **disqualified** | min-quality gate on `haiku + no context` | separate section, rank `—`, `gate: fail — min_quality` |
| **weak-labeled** | 8 of 17 references LLM-generated | `weak-labeled` chip on every affected row |

## Claims verified live

- **Profile switch re-ranks with zero new runs.** Switching balanced → cost-optimized changed every
  score and the order; `0 new runs` on the board; the board is a GET, so it structurally cannot
  enqueue. **The disqualified variant stayed disqualified** even though its raw 0.776 would have
  topped the cost board — the gate/weight separation, demonstrated rather than asserted.
- **Keyboard operation.** `Enter` toggles `aria-expanded` and reveals the breakdown; `↑/↓` move focus
  between rows; focus ring visible. Nothing is hover-only.
- **Virtualization.** 83-variant board keeps 7–34 rows in the DOM depending on the visible window,
  repainting on scroll, with spacer rows preserving scroll height.
- **Component breakdown** shows raw AND normalized values, each penalty term, the tie explanation,
  and the full `config_hash` with the interval's method.
- **Coverage screen** shows achieved/target per dimension, cases, rounds, difficulty, diversity,
  oracle coverage (`76% — 13 of 17 cases can decide success`), and the gold/weak/none split.
- **No console errors** on any state.

## Defects the live run found and fixed

Each of these passed its unit tests and still failed in front of a browser. They are listed because
the fix is now fenced by a regression test, and because "the tests were green" is exactly what made
them worth recording.

1. **Schema-derived cases stored the SCHEMA as the reference.** Exact-match then compared every
   output against a JSON Schema document and scored 0 for every variant. Root cause was
   `Case.Validate` demanding a *reference* for a gold label; an oracle is a reference **or** a schema
   **or** a regex. → `Case.HasOracle`, and the generator no longer stuffs the schema into
   `Reference`.
2. **A weak eval set produced a confident wrong ranking.** 12 of 17 cases carried no oracle, so
   `task_success` rested on five cases and the broken variant topped the board at quality 1.000 —
   while difficulty and diversity both passed their floors. → added the **oracle-coverage floor**;
   difficulty and diversity describe the *inputs*, and neither says whether the set can answer the
   question it exists to answer.
3. **Penalties bypassed the confidence interval.** A penalty derived from a measured quantity was
   subtracted as an exact per-variant constant, so on a board where every metric was statistically
   degenerate three indistinguishable variants were ranked 1-2-3 on a difference of 1e-5, with no tie
   flag. → measurement-derived penalties now enter the bootstrap per replicate; count-derived ones
   stay constant.
4. **A budget-capped fan-out crashed the board.** `scoring.Build` errored on the first variant with
   no observations. → unmeasured variants are **excluded and named**, the board goes partial, and the
   58 excluded variants are one summary note plus a collapsed list rather than 58 banners burying the
   one that explains them.
5. **The error state rendered a hollow board** — "0 gate-passing under undefined" beside the error
   banner, which reads as "fine, just empty". → on error the board scaffold is hidden entirely.
6. **Pareto marks were clipped to the axes.** No domain padding, so a three-point frontier read as
   two dots in opposite corners. → domain padded by a fraction of the observed range.
7. **The all-tie message rendered twice** (styled banner + plain note). → the banner is the single
   renderer; `AllTie` is a flag, not a duplicated sentence.
8. **A vacuous 100%.** Running P1 discovery over a real repository (nousresearch/hermes-agent,
   commit `e57918a`) emitted 40 LLM call sites and **zero edges** — inter-node flow is P5's dynamic
   tracing, not P1's static pass. With no edges there are no path obligations, and an empty-set
   covered-fraction of 1.0 reported "path coverage 100%" for a workflow whose control flow had never
   been observed. → empty dimensions are `Vacuous`: achieved 0, never `Met`, rendered "not
   measurable".
9. **The coverage-derived low-confidence flag was clobbered** by the set-quality pass, which
   *assigned* over both the flag and the reasons instead of OR-ing and appending. → fixed; fenced.
10. **A latent bug in the P2.5 down-migration proof.** Its `information_schema` check was not
   schema-qualified, so it went green only while no other package created an `eval_result` table —
   and turned red the moment P4's proof did. → filtered by `current_schema()`, matching the filter
   `worktree`'s equivalent check already carried.

## Scope limit — what has NOT been scored

Every board in this record was produced by `cmd/p4boarddemo`, whose **prober and run handler are
hardcoded to the demo's fixture topology** (`router` → `branch_a`/`branch_b` → `reflect`). They ignore
the IR they are handed.

The `-ir` flag therefore does only two things honestly: it makes **coverage enumerate the real
workflow's obligations**, and it runs **real P3.5 classification** over the real IR. It does NOT make
the fan-out execute that workflow — the traces, the scores and the leaderboard still come from the
fixture's stubbed runtime.

Concretely, for the hermes-agent run: the discovery output and the 0-label classification result are
real findings about that repository. The `node coverage 0 of 40` and the 45 "unreachable" residual
obligations are **not** — they are the arithmetic consequence of a fixture prober being handed a
foreign IR, and would look identical for any repository.

Scoring a real workflow needs, in order: P5 dynamic tracing (for edges, without which P3.5 labels
nothing), and a runtime that can execute that workflow's nodes. Neither exists yet.

## Chart colour

Series and status colours come from the **dataviz** palette's dark steps, validated against **this
page's** chart surface (`#1a2332`) rather than the skill's default surface — contrast and CVD results
are only meaningful against the surface the chart actually renders on:

```
Palette (dark, surface #1a2332, categorical): 4 slots
  [PASS] Lightness band         all 4 inside L 0.48–0.67
  [PASS] Chroma floor           all 4 >= 0.1
  [PASS] CVD separation         worst adjacent ΔE 13.0 (deutan) · tritan 8.7
  [PASS] Normal-vision floor    worst adjacent ΔE 19.3
  [PASS] Contrast vs surface    all 4 >= 3:1
```

Identity is never colour-alone: every state ships a text chip, and the Pareto frontier marks
non-dominated points with a **diamond** against the dominated **circle**, plus a table view.
