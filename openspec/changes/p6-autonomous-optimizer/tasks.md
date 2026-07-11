# Tasks — P6: Autonomous optimizer

## 1. AI Engineer — Objective, constraints, diagnosis-guided search
- [ ] 1.1 Wire the **P4 composite score** (active weight profile) as the search objective and the
      **P4 gates** as hard constraints: a candidate that fails any gate is never selected/applied,
      regardless of composite score.
- [ ] 1.2 Implement `Search.NextCandidates(diagnosis, current_spec, policy)` — **diagnosis-guided
      first:** enumerate candidate changes at the P4.5-attributed node+dimension (via the P5.5
      operator catalog) *before* any blind grid/Bayesian expansion over model×prompt×context.
- [ ] 1.3 Record, for **every** candidate evaluated, the **motivating diagnosis** (`diagnosis_id`),
      so the diagnosis-guided sample-efficiency claim is auditable, not asserted.
- [ ] 1.4 Define the blind-expansion fallback and its trigger (targeted candidates exhausted *and*
      budget remains, capped by a separate blind-search sub-budget — see design Q1).
- [ ] 1.5 Test: first candidates are at the attributed node+dimension, before any blind candidate;
      each candidate carries its motivating diagnosis. A higher-scoring gate-failing candidate is
      **not** applied; a lower-scoring gate-passing one is preferred.

## 2. AI Engineer — Verification-in-the-loop
- [ ] 2.1 Invoke the **P5.5 held-out verification** per iteration: multi-seed, mean+CI, significance
      vs. current best, regression check. `Verify(candidate, held_out, seeds) → {delta±ci, sig,
      regression, gate_status}`.
- [ ] 2.2 Apply a candidate **only** on a held-out verified gain — never on the cases that generated
      the proposal (no overfitting the recommendation). Assert diagnosis proposes, verification
      decides, with no human present.
- [ ] 2.3 Validate every candidate against the **P5 typed I/O contracts** before verification;
      reject incoherent orderings/re-arrangements rather than applying them.

## 3. AI Engineer — Loop engineering (stopping, stall, recovery)
- [ ] 3.1 Implement explicit **stopping conditions**: min-improvement threshold (marginal gain on the
      composite CI-lower-bound — design Q2), max iterations, budget ceiling.
- [ ] 3.2 **Min-improvement stop:** after a candidate whose verified gain is below the threshold, stop
      iterating (state `converged`), do not continue. Test it.
- [ ] 3.3 **Stall/no-progress detection:** K consecutive iterations with no gate-passing,
      verification-passing gain → stop with state `stalled`. Test it (no infinite loop).
- [ ] 3.4 **Recovery:** a failed apply or crashed iteration leaves the last-good Variant Spec live;
      the loop never ends with a half-applied or unverified spec live.

## 4. DevOps — Apply-path prerequisites (kill switch + audit trail + rollback)
- [ ] 4.1 **Prerequisite gate:** the apply step is **disabled** unless a kill switch, an audit trail,
      and a rollback are all present and armed for the run; absent any one, the loop runs only in
      propose/verify **dry-run** (applies nothing). Test: remove each prerequisite in turn → zero
      Variant Spec swaps.
- [ ] 4.2 **Kill switch:** stop the loop on demand; after it fires no candidate applies, the in-flight
      iteration finishes or is abandoned leaving the last-good spec live, and the stop is recorded.
      Test: fire mid-iteration → no apply after stop.
- [ ] 4.3 **Audit trail:** append-only, attributable records keyed by the P0 tag set
      (`config_hash`, `variant_id`, `run_id`, timestamp) for every decision — grant, candidate
      considered (+ diagnosis), verification verdict, gate evaluation, apply, halt, stop, rollback.
- [ ] 4.4 **Write-ahead apply:** the audit event commits **before** the Variant Spec swaps, so an
      apply that isn't audited cannot occur (the apply path's single-point-of-failure guarantee).
      Test: assert every apply is preceded by its audit event.
- [ ] 4.5 **Rollback:** `Rollback(applied_change)` reconstructs the exact prior Variant Spec from the
      audit trail; the rollback is itself audited. Test: apply → roll back → live spec byte-identical
      to prior (`config_hash` match).

## 5. DevOps — Hard-constraint gates & halt conditions
- [ ] 5.1 **Constraint engine:** budget ceiling (cumulative spend cap), provider allowlist,
      min-improvement threshold, max iterations — set at grant time, **immutable** for the run.
- [ ] 5.2 **Budget halt:** when cumulative run spend breaches the ceiling, halt mid-run, apply nothing
      further, **disarm** apply, record the halt. Test: drive a run past the ceiling → halts, no
      further apply.
- [ ] 5.3 **Regression halt:** when regression detection finds any tracked metric degraded beyond
      threshold vs. the current best, halt and disarm apply. Test with a seeded post-apply regression.
- [ ] 5.4 **Disarm-until-re-arm:** a halt disarms the apply step until a human explicitly re-arms it;
      re-arming is an explicit, audited action (design Q4).
- [ ] 5.5 **Bounded fan-out / least privilege:** per-iteration verification fan-out (candidate ×
      held-out × seeds) goes through the P2 queue with bounded concurrency, backpressure, idempotent
      redelivery (no double-charge/double-apply); candidates run in the P3 sandbox; the loop holds no
      ambient credentials and writes no prompts/keys/PII inline into the audit trail (content-hashed).

## 6. AI Engineer + System Designer — Feedback loop & safe degradation
- [ ] 6.1 **Production-failure intake:** `IngestProductionFailure(trace) → EvalCase` adds a new case
      to the P4 eval set and **re-measures coverage**, so the next run is scored against it. Label the
      intake case per P4 (weak) — design Q5.
- [ ] 6.2 Test: ingest a production-failure trace → new eval case present in the P4 set, coverage
      re-measured, next run scores against it.
- [ ] 6.3 **Fail-closed degradation:** if the search controller, verification service, queue, or audit
      store is unavailable, the loop **stops applying** and leaves the last-good spec live — never
      applies unverified or unaudited. Test: kill the audit store (then verification) mid-run → loop
      stops applying, last-good spec live.
- [ ] 6.4 **Concurrency safety:** one active Autonomous run per workflow, enforced by a lock keyed on
      the workflow (no write-write hazard on the live spec) — design Q6.

## 7. Product + Frontend — Autonomous-level governance UX
- [ ] 7.1 Product: design the **Autonomous authority-grant** flow — the user sets the hard constraints
      (budget ceiling, provider allowlist, min-improvement, max iterations) at grant time; name the
      tradeoff ("you are letting the system apply changes on its own within these limits"); the grant
      is recorded in the audit trail.
- [ ] 7.2 Product: design the **unhappy paths first** — loop halted by regression, loop stopped
      mid-iteration by the user, budget exhausted with no gain, an applied change to undo. Content is
      the interface: "halted — quality regressed on cluster X, apply disarmed", not "error".
- [ ] 7.3 Frontend: **live monitor** — current iteration, cumulative spend against the ceiling,
      candidates applied, streaming audit trail; reads P2.5 signals, never derived state that drifts.
- [ ] 7.4 Frontend: an **always-visible stop control** wired to the kill switch — keyboard-reachable,
      never scrolled off; halts immediately.
- [ ] 7.5 Frontend: a **rollback control on every applied change**, showing the diagnosis, the
      verified delta, and the cost/latency impact; one click reverts via the audit trail.
- [ ] 7.6 Frontend: first-class states — running / converged / max-iter / halted-regression /
      halted-budget / stalled / stopped / rolled-back — each visually distinct; audit trail
      virtualized for long runs; status color via the **dataviz** skill for contrast + light/dark.
- [ ] 7.7 Frontend: make the three automation levels (Advisory / Assisted / Autonomous) visibly
      distinct — the user always knows which contract is active and how to step down or stop.

## 8. Testing & review
- [ ] 8.1 Fixtures: a multi-node workflow with two independently diagnosable defects (reasoning-heavy
      node on a weak model; low-relevance RAG node), a P4 eval set with a held-out slice, P4.5
      attribution per defect, the P5.5 operator catalog + verification gate; a small budget ceiling a
      non-converging search hits; a min-improvement threshold a noise-level candidate falls below; a
      seeded regression.
- [ ] 8.2 Prerequisite-gating: rollback/audit/kill-switch absent → dry-run, zero applies; all armed →
      apply preceded by its audit event (write-ahead).
- [ ] 8.3 Diagnosis-guided search: attributed-node candidates before blind candidates; each carries
      its diagnosis. Objective/constraint: gate-failing high-scorer not applied.
- [ ] 8.4 Budget-halt-mid-run; min-improvement-stop; stall-detection-stop — each stops with the
      correct state and applies nothing further.
- [ ] 8.5 Rollback-via-audit: applied change reverted to the byte-identical prior spec (`config_hash`
      match), rollback audited.
- [ ] 8.6 Regression-halt: per-iteration regression check blocks apply; seeded post-apply regression
      halts + disarms. Kill switch: no apply after stop, last-good live, stop audited.
- [ ] 8.7 Feedback loop: production-failure trace → new P4 eval case, coverage re-measured. Fail-
      closed: audit/verification store down → loop stops applying.
- [ ] 8.8 UI verification: drive grant → live monitor → stop → rollback against a live
      (stubbed-provider) loop; confirm constraints recorded, monitor streams iteration + spend/ceiling
      + audit, stop always visible + immediate, rollback works on every applied change, halted/stopped/
      rolled-back states render.
- [ ] 8.9 Confirm the M9 exit checklist (PRD §13) is green. Loop ships **dark, dry-run-only** until it
      is; Autonomous authority off until explicitly granted with constraints.
