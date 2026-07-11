## Why

After P5.5 the engine emits a proposed **source diff** with a *verified* delta (CI + cost/latency
impact + cases fixed/broken) and opens a pull request, but a human still reviews and **merges** every
change, one at a time. That is right for the Advisory and Assisted levels and is their ceiling. It
does not scale to a multi-node workflow with several diagnosed defects, and it wastes the
sample-efficiency the diagnosis engine already bought: the machine knows *which node and which
dimension* to change, yet a person drives every iteration.

P6 closes the loop — **analyze → propose → verify → apply** — and runs it under hard constraints
without a human clicking each step. Per **ADR-001**, applying a change means transforming source and
delivering it as a pull request; **autonomous "apply" therefore means the loop opens and — under the
hard constraints, with every gate green — MERGES a pull request**, where every lower automation level
still requires a human to merge. Two things make that safe. First, the search is **diagnosis-guided,
not blind**: the P4 **composite score is the objective** it maximizes, the P4 **gates are its hard
constraints**, and the P4.5 node+dimension attribution points the search at what to change — far more
sample-efficient than a grid/Bayesian sweep over the whole model×prompt×context space. Second, the
loop may merge **nothing** until three operational prerequisites are armed — a **kill switch**, a full
**audit trail** (**git history + a change ledger**), and **rollback** (**`git revert`**) — and every
merge is gated by **build + eval + regression** (the codemod diff compiles, the P5.5 held-out
verification shows a real gain, and the regression check is clean). Regression detection and budget
alerts **halt** the loop the moment any metric degrades past threshold or a budget is breached.
Production failures re-enter at P4 as new eval cases — the eval set is the system's living memory.
Autonomous is a distinct **trust contract**: the user grants the authority to merge within limits,
sets the constraints, watches it live, and stops it.

Depends on **P4** (composite score = objective, disqualifying gates = hard constraints, multi-seed/
CI harness = verification substrate, eval set the feedback loop re-seeds), **P4.5** (node+dimension
attribution + typed diagnosis that steers the search), **P5.5** (the change-operator catalog + its
deterministic AST codemod the search drives, the build gate + held-out verification gate + regression
check invoked every iteration, and the Advisory/Assisted PR-based automation-level model P6 extends),
the **source transformation engine** (ADR-001 — the codemod + isolated worktree the loop applies
through), **P5** (typed I/O contracts + dynamic tracing
so an applied re-arrangement is validated, not silently broken), **P2.5** (regression/budget signals
+ loop observability), and **P2** (Runtime + registries the loop actuates through; run queue +
idempotency for fan-out). This is the terminal phase of the intelligence half — **Milestone M9**.

## What Changes

- **New capability `autonomous-optimizer`.** A closed-loop controller that maximizes the **P4
  composite score** (active weight profile) as its objective and treats the **P4 gates** as hard
  constraints — a candidate that fails any gate is never applied regardless of score. The search is
  **diagnosis-guided**: it enumerates candidate changes at the **P4.5-attributed node+dimension**
  *before* any blind grid/Bayesian expansion over the wider model×prompt×context space, and records
  the motivating diagnosis for every candidate. **Autonomous apply = the loop opens a pull request
  and, with every gate green, MERGES it** (no human clicking merge; every level below Autonomous still
  requires human review + merge). Every apply/merge is gated by **build + eval + regression**: the
  codemod diff compiles, the **P5.5 held-out verification** (multi-seed, CI, significance vs. current
  best, regression check) shows a real gain, and the P4 gates pass — *diagnosis proposes, verification
  decides*, with no human in the seat.
- **Hard-constraint gates for the loop.** Set at authority-grant time and immutable for the run:
  **budget ceiling** (cumulative spend cap), **provider allowlist**, **min-improvement threshold**
  (a verified gain below it stops iteration), and **max iterations**.
- **Apply-path prerequisites (breaking gate on any merge).** The loop merges **nothing** unless a
  **kill switch**, an **audit trail** (**git history + a change ledger**), and a **rollback**
  (**`git revert`**) are all present and armed; absent any one, the loop runs only in a propose/verify
  **dry-run** that may open draft PRs but **merges nothing**. The merge is **write-ahead-audited** —
  the change-ledger event commits (and the merge is recorded in git history) before the merge lands,
  so no applied change escapes the trail.
- **Halt conditions.** **Regression detection** (any tracked metric degraded beyond threshold vs. the
  current best) and **budget alerts** (cumulative spend breaches the ceiling) **halt** the loop
  mid-run and **disarm** the merge step until a human re-arms it. **Stall/no-progress detection**
  (K iterations with no gate-passing verified gain) stops a wandering search. Budget-breach,
  min-improvement, and max-iteration halts remain first-class.
- **Kill switch + rollback.** A user or an automated halt can **stop** the loop immediately — no
  candidate merges after a stop, the last-good (currently-merged) Variant Spec stays live, and the
  stop is recorded in the change ledger. Any **applied change is reversible** to the exact prior
  Variant Spec via **`git revert`** of the merge (git history + the change ledger are the audit
  trail); the revert is itself audited.
- **Feedback loop — living memory.** A **production failure** observed after an applied change is
  ingestible as a **new eval case** that re-enters at P4 (added to the eval set, coverage
  re-measured), so the next optimization run is measured against what actually broke.
- **Autonomous-level governance UX.** A new automation level (on top of P5.5's Advisory/Assisted)
  with an explicit **authority grant** to **open and merge PRs** within limits (constraints set +
  recorded at grant time), a **live monitor** (current iteration, cumulative spend against the ceiling,
  PRs merged, streaming audit trail = change ledger + git history), an always-visible **stop** control
  wired to the kill switch, and a **`git revert` rollback** control on every merged change.
- **Deferred / owned elsewhere:** the change operators + their AST codemod, the build gate, and the
  verification gate mechanics (**P5.5** / ADR-001 source transformation engine);
  the composite score, weight profiles, and gate definitions (**P4**); attribution + diagnosis
  (**P4.5**); the Advisory/Assisted levels (**P5.5**); general production incident response for the
  target workflow (out of scope — P6 owns halting *its own loop* safely, not operating the user's
  system).

## Impact

- **Affected capabilities:** `autonomous-optimizer` (new). Consumes `scoring` (composite score +
  gates, P4), `eval-harness` (verification substrate + eval set, P4), the attribution/diagnosis
  outputs (P4.5), the change-operator catalog + verification gate + automation-level model (P5.5), the
  typed I/O contracts (P5), the metrics substrate (P2.5), and the Runtime + run queue + registries
  (P2).
- **Affected code/systems:** new search controller (diagnosis-guided candidate enumeration + blind-
  expansion fallback), constraint/gate engine (budget ceiling, provider allowlist, min-improvement,
  max iterations), apply path (write-ahead change-ledger event → **open + merge PR**, recorded in git
  history), audit trail (**git history + an append-only change ledger**, tagged), rollback service
  (**`git revert`** of the merge), halt engine (regression + budget + stall), production-failure
  intake into the P4 eval set, Postgres schema (optimization runs/iterations, change-ledger events,
  applied changes with merge-commit refs), object store (candidate specs, source diffs, verification
  blobs, before/after specs — content-hashed), run-queue fan-out for per-iteration verification, and a
  React Autonomous-level grant + live-monitor + `git revert` rollback UI.
- **Dependencies:** requires **P4**, **P4.5**, **P5**, **P5.5**, **P2.5**, **P2**, **P3**, **P0**.
  Unblocks nothing further — it is the terminal phase (**M9**) of the timeline, the closed-loop
  optimizer as a product capability.
