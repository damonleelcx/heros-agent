## Why

The customer's request was "score the repo". Program ruling **R4** answers it with evidence-backed
per-surface findings and **no composite number**, and this change set is where that answer is built.

The reason is the platform's own founding principle. Every score in this system is comparative and
verified — variant against variant, multi-seed, ties declared when confidence intervals overlap — because
*diagnosis proposes, verification decides*. An absolute "your repository is 62 out of 100" is a model's
judgement rendered in a metric's typeface, and no held-out set exists that could make it true. Shipping it
would put an unverified opinion in the result position, which is the one thing thirty phases of this
codebase were built to prevent.

What does not exist today is any statement about a repository's *strategy*. `internal/discovery` extracts
call sites — a parts list. `internal/patternclassifier` labels patterns from topology, and P30 documented
how far that gets: seven of its eight detectors are topology predicates, so on a repository with no edges
*"0/22 can fire by construction"*. Nothing produces "this repository's memory strategy is a per-session
store that is never pruned."

There is also unrendered honesty already sitting in the tree. `evalboard.CoverageView` computes
`NIndecisive` — *"cases carrying an oracle that can never fail — the most misleading cases in the set"* —
and P30 recorded that *"none of it reaches a screen that shows which cases"*. A generated eval set whose
oracles cannot fail scores 1.0, and today nothing would say so.

## What Changes

- **ADDED** the assessment: nine axes (`model`, `prompt`, `skills`, `tools`, `context`, `memory`,
  `harness`, `loop`, `graph`), always all nine, each in exactly one of four states — `measured`,
  `observed`, `not_measured`, `refused`.
- **ADDED** the requirement that `not_measured` **names its missing input** and is never rendered as zero
  and never omitted. This is P29's `not reported` discipline extended from one field to a whole report.
- **ADDED** the `structural` / `inferred` origin split: deterministic extraction runs first, inference
  runs only on the residue, and an inferred finding is visibly an inference without hovering.
- **ADDED** decisiveness travelling with every score — `n_cases`, oracle coverage, and the count of cases
  whose oracle can never fail — plus **enumerable cases**, closing the gap P30 named.
- **ADDED** the rule that an empty graph is attributed to the **frontend**, naming the language, and never
  reported as the repository having a flat graph.
- **ADDED** pinned inference per `(source_revision, agent config_hash)` — P30's rule, unchanged — with
  explicit re-inference shown as a diff.
- **ADDED** spend caps per assessment, with budget exhaustion degrading to `not_measured` rather than to a
  partial report presented as complete.
- **NOT ADDED, deliberately:** any composite score, grade, maturity level, or cross-tenant ranking. A
  fence asserts no code path emits one.

## Impact

- **Affected capabilities:** `surface-assessment` (new), `eval-set-decisiveness` (new)
- **Affected code/systems:** `internal/discovery` (structural extractors per axis), `internal/herosagent`
  (inference over the residue), `internal/evalgen` + `evalharness` (used unchanged),
  `internal/evalboard` (`CoverageView` finally rendered), `internal/api`, `web/console`
- **Dependencies:** upstream — [P32](../../../docs/prd/P32-repo-intake.md) (source),
  [P31](../../../docs/prd/P31-conversational-console.md) (somewhere to report),
  [P34](../../../docs/prd/P34-harness-loop-graph-split.md) (**for the `loop` and `graph` axes only** — until
  it lands, those two report `refused`), P30 (pinning), P4 (eval), P3 (sandbox). Unblocks —
  [P35](../../../docs/prd/P35-autonomous-improvement-run.md), which has nothing to propose against without a
  finding.
- **Documents only in this program.** Every task is unchecked.
