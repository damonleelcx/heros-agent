## Why

After P5.5 the engine emits a proposed diff with a *verified* delta (CI + cost/latency impact +
cases fixed/broken), but a human still applies every change, one at a time. That is right for the
Advisory and Assisted levels and is their ceiling. It does not scale to a multi-node workflow with
several diagnosed defects, and it wastes the sample-efficiency the diagnosis engine already bought:
the machine knows *which node and which dimension* to change, yet a person drives every iteration.

P6 closes the loop — **analyze → propose → verify → apply** — and runs it under hard constraints
without a human clicking each step. Two things make that safe. First, the search is **diagnosis-
guided, not blind**: the P4 **composite score is the objective** it maximizes, the P4 **gates are its
hard constraints**, and the P4.5 node+dimension attribution points the search at what to change —
far more sample-efficient than a grid/Bayesian sweep over the whole model×prompt×context space.
Second, the loop may apply **nothing** until three operational prerequisites are armed: a **kill
switch**, a full **audit trail**, and **rollback**. Regression detection and budget alerts **halt**
the loop the moment any metric degrades past threshold or a budget is breached. Production failures
re-enter at P4 as new eval cases — the eval set is the system's living memory. Autonomous is a
distinct **trust contract**: the user grants the authority, sets the constraints, watches it live,
and stops it.

Depends on **P4** (composite score = objective, disqualifying gates = hard constraints, multi-seed/
CI harness = verification substrate, eval set the feedback loop re-seeds), **P4.5** (node+dimension
attribution + typed diagnosis that steers the search), **P5.5** (the change-operator catalog the
search drives, the held-out verification gate + regression check invoked every iteration, and the
Advisory/Assisted automation-level model P6 extends), **P5** (typed I/O contracts + dynamic tracing
so an applied re-arrangement is validated, not silently broken), **P2.5** (regression/budget signals
+ loop observability), and **P2** (Runtime + registries the loop actuates through; run queue +
idempotency for fan-out). This is the terminal phase of the intelligence half — **Milestone M9**.

## What Changes

- **New capability `autonomous-optimizer`.** A closed-loop controller that maximizes the **P4
  composite score** (active weight profile) as its objective and treats the **P4 gates** as hard
  constraints — a candidate that fails any gate is never applied regardless of score. The search is
  **diagnosis-guided**: it enumerates candidate changes at the **P4.5-attributed node+dimension**
  *before* any blind grid/Bayesian expansion over the wider model×prompt×context space, and records
  the motivating diagnosis for every candidate. Every apply is gated by the **P5.5 held-out
  verification** (multi-seed, CI, significance vs. current best, regression check) — *diagnosis
  proposes, verification decides*, with no human in the seat.
- **Hard-constraint gates for the loop.** Set at authority-grant time and immutable for the run:
  **budget ceiling** (cumulative spend cap), **provider allowlist**, **min-improvement threshold**
  (a verified gain below it stops iteration), and **max iterations**.
- **Apply-path prerequisites (breaking gate on any apply).** The loop applies **nothing** unless a
  **kill switch**, an **audit trail**, and a **rollback** are all present and armed; absent any one,
  the loop runs only in a propose/verify **dry-run** that changes nothing. Apply is **write-ahead-
  audited** — the audit event commits before the Variant Spec swaps, so no apply escapes the trail.
- **Halt conditions.** **Regression detection** (any tracked metric degraded beyond threshold vs. the
  current best) and **budget alerts** (cumulative spend breaches the ceiling) **halt** the loop
  mid-run and **disarm** the apply step until a human re-arms it. **Stall/no-progress detection**
  (K iterations with no gate-passing verified gain) stops a wandering search.
- **Kill switch + rollback.** A user or an automated halt can **stop** the loop immediately — no
  candidate applies after a stop, the last-good Variant Spec stays live, and the stop is audited. Any
  **applied change is reversible** to the exact prior Variant Spec via the audit trail; the rollback
  is itself audited.
- **Feedback loop — living memory.** A **production failure** observed after an applied change is
  ingestible as a **new eval case** that re-enters at P4 (added to the eval set, coverage
  re-measured), so the next optimization run is measured against what actually broke.
- **Autonomous-level governance UX.** A new automation level (on top of P5.5's Advisory/Assisted)
  with an explicit **authority grant** (constraints set + recorded at grant time), a **live monitor**
  (current iteration, cumulative spend against the ceiling, candidates applied, streaming audit
  trail), an always-visible **stop** control wired to the kill switch, and a **rollback** control on
  every applied change.
- **Deferred / owned elsewhere:** the change operators and the verification gate mechanics (**P5.5**);
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
  max iterations), apply path (write-ahead audit → Variant Spec swap), audit-trail store (append-only,
  tagged), rollback service, halt engine (regression + budget + stall), production-failure intake into
  the P4 eval set, Postgres schema (optimization runs/iterations, audit events, applied changes),
  object store (candidate specs, verification blobs, before/after specs — content-hashed), run-queue
  fan-out for per-iteration verification, and a React Autonomous-level grant + live-monitor + rollback
  UI.
- **Dependencies:** requires **P4**, **P4.5**, **P5**, **P5.5**, **P2.5**, **P2**, **P3**, **P0**.
  Unblocks nothing further — it is the terminal phase (**M9**) of the timeline, the closed-loop
  optimizer as a product capability.
