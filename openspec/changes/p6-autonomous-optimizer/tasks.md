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
- [ ] 2.1 Invoke the **P5.5 build gate + held-out verification** per iteration on the **transformed
      working copy** (the built codemod output): the diff must compile, then multi-seed, mean+CI,
      significance vs. current best, regression check. `Verify(candidate, held_out, seeds) → {builds,
      delta±ci, sig, regression, gate_status}`.
- [ ] 2.2 **Apply = open a PR and merge it**, **only** on a build-passing, held-out verified gain with
      the P4 gates green — never on the cases that generated the proposal (no overfitting the
      recommendation). Assert diagnosis proposes, verification decides, with no human present (every
      level below Autonomous still requires human review + merge).
- [ ] 2.3 Validate every candidate against the **P5 typed I/O contracts** before verification;
      reject incoherent orderings/re-arrangements rather than applying them.

## 3. AI Engineer — Loop engineering (stopping, stall, recovery)
- [ ] 3.1 Implement explicit **stopping conditions**: min-improvement threshold (marginal gain on the
      composite CI-lower-bound — design Q2), max iterations, budget ceiling.
- [ ] 3.2 **Min-improvement stop:** after a candidate whose verified gain is below the threshold, stop
      iterating (state `converged`), do not continue. Test it.
- [ ] 3.3 **Stall/no-progress detection:** K consecutive iterations with no gate-passing,
      verification-passing gain → stop with state `stalled`. Test it (no infinite loop).
- [ ] 3.4 **Recovery:** a failed merge or crashed iteration leaves the last-good (currently-merged)
      Variant Spec live; the loop never ends with a half-merged or unverified spec live.

## 4. DevOps — Apply-path prerequisites (kill switch + audit trail = git history + ledger + rollback = git revert)
- [ ] 4.1 **Prerequisite gate:** the merge step is **disabled** unless a kill switch, an audit trail
      (**git history + a change ledger**), and a rollback (**`git revert`**) are all present and armed
      for the run; absent any one, the loop runs only in propose/verify **dry-run** (may open draft
      PRs, **merges nothing**). Test: remove each prerequisite in turn → zero merges.
- [ ] 4.2 **Kill switch:** stop the loop on demand; after it fires no candidate merges, the in-flight
      iteration finishes or is abandoned leaving the last-good (currently-merged) spec live, and the
      stop is recorded. Test: fire mid-iteration → no merge after stop.
- [ ] 4.3 **Audit trail = git history + change ledger:** git records each merge commit; the append-only
      change ledger records every decision — grant, candidate considered (+ diagnosis), verification
      verdict, gate evaluation, apply(open+merge PR), halt, stop, revert — keyed by the P0 tag set
      (`config_hash`, `variant_id`, `run_id`, timestamp) with the PR ref + merge commit.
- [ ] 4.4 **Write-ahead merge:** the change-ledger event commits **before** the PR is merged (the merge
      is then recorded in git history), so a merge that isn't audited cannot occur (the merge path's
      single-point-of-failure guarantee). Test: assert every merge is preceded by its ledger event.
- [ ] 4.5 **Rollback = `git revert`:** `Rollback(applied_change)` reverts the merge commit to
      reconstruct the exact prior Variant Spec; the revert is itself audited in the ledger. Test:
      merge → `git revert` → live spec byte-identical to prior (`config_hash` match).

## 5. DevOps — Hard-constraint gates & halt conditions
- [ ] 5.1 **Constraint engine:** budget ceiling (cumulative spend cap), provider allowlist,
      min-improvement threshold, max iterations — set at grant time, **immutable** for the run.
- [ ] 5.2 **Budget halt:** when cumulative run spend breaches the ceiling, halt mid-run, merge nothing
      further, **disarm** merge, record the halt. Test: drive a run past the ceiling → halts, no
      further merge.
- [ ] 5.3 **Regression halt:** when regression detection finds any tracked metric degraded beyond
      threshold vs. the current best, halt and disarm merge. Test with a seeded post-merge regression.
- [ ] 5.4 **Disarm-until-re-arm:** a halt disarms the merge step until a human explicitly re-arms it;
      re-arming is an explicit, audited action (design Q4).
- [ ] 5.5 **Bounded fan-out / least privilege:** per-iteration verification fan-out (candidate ×
      held-out × seeds) goes through the P2 queue with bounded concurrency, backpressure, idempotent
      redelivery (no double-charge/double-merge); candidates build + run in the P3 sandbox on isolated
      worktrees; the loop holds no ambient credentials and writes no prompts/keys/PII inline into the
      change ledger (content-hashed).

## 6. AI Engineer + System Designer — Feedback loop & safe degradation
- [ ] 6.1 **Production-failure intake:** `IngestProductionFailure(trace) → EvalCase` adds a new case
      to the P4 eval set and **re-measures coverage**, so the next run is scored against it. Label the
      intake case per P4 (weak) — design Q5.
- [ ] 6.2 Test: ingest a production-failure trace → new eval case present in the P4 set, coverage
      re-measured, next run scores against it.
- [ ] 6.3 **Fail-closed degradation:** if the search controller, verification service, queue, or
      change-ledger store is unavailable, the loop **stops merging** and leaves the last-good spec live
      — never merges unverified or unaudited. Test: kill the change-ledger store (then verification)
      mid-run → loop stops merging, last-good spec live.
- [ ] 6.4 **Concurrency safety:** one active Autonomous run per workflow, enforced by a lock keyed on
      the workflow (no write-write hazard on the repo/live spec) — design Q6.

## 7. Product + Frontend — Autonomous-level governance UX
- [ ] 7.1 Product: design the **Autonomous authority-grant** flow — the user sets the hard constraints
      (budget ceiling, provider allowlist, min-improvement, max iterations) at grant time; name the
      tradeoff ("you are letting the system **open and merge pull requests** on its own within these
      limits"); the grant is recorded in the audit trail (change ledger).
- [ ] 7.2 Product: design the **unhappy paths first** — loop halted by regression, loop stopped
      mid-iteration by the user, budget exhausted with no gain, an applied change to undo. Content is
      the interface: "halted — quality regressed on cluster X, apply disarmed", not "error".
- [ ] 7.3 Frontend: **live monitor** — current iteration, cumulative spend against the ceiling,
      **PRs merged**, streaming audit trail (change ledger + git history); reads P2.5 signals, never
      derived state that drifts.
- [ ] 7.4 Frontend: an **always-visible stop control** wired to the kill switch — keyboard-reachable,
      never scrolled off; halts immediately.
- [ ] 7.5 Frontend: a **`git revert` rollback control on every merged change**, showing the diagnosis,
      the verified delta, and the cost/latency impact; one click reverts the merge commit.
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
- [ ] 8.2 Prerequisite-gating: rollback (`git revert`) / audit (git history + ledger) / kill-switch
      absent → dry-run, **zero merges**; all armed → merge preceded by its ledger event (write-ahead).
- [ ] 8.3 Diagnosis-guided search: attributed-node candidates before blind candidates; each carries
      its diagnosis. Objective/constraint: gate-failing high-scorer not merged.
- [ ] 8.4 Budget-halt-mid-run; min-improvement-stop; stall-detection-stop — each stops with the
      correct state and merges nothing further.
- [ ] 8.5 Rollback-via-`git revert`: merged change reverted to the byte-identical prior spec
      (`config_hash` match), revert audited in the ledger.
- [ ] 8.6 Regression-halt: per-iteration build+eval+regression check blocks merge; seeded post-merge
      regression halts + disarms. Kill switch: no merge after stop, last-good live, stop audited.
- [ ] 8.7 Feedback loop: production-failure trace → new P4 eval case, coverage re-measured. Fail-
      closed: change-ledger/verification store down → loop stops merging.
- [ ] 8.8 UI verification: drive grant → live monitor → stop → `git revert` rollback against a live
      (stubbed-provider) loop; confirm constraints recorded, monitor streams iteration + spend/ceiling
      + PRs merged + audit, stop always visible + immediate, rollback works on every merged change,
      halted/stopped/rolled-back states render.
- [ ] 8.9 Confirm the M9 exit checklist (PRD §13) is green. Loop ships **dark, dry-run-only** (opens
      draft PRs, merges nothing) until it is; Autonomous authority off until explicitly granted with
      constraints.
