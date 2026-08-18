# PRD — P6: Autonomous optimizer

| Field | Value |
|---|---|
| Phase / Milestone | P6 / M9 |
| Target window | ~Weeks 33–40 |
| Lead role(s) | AI Engineer + DevOps (co-leads) |
| Supporting role(s) | Product Designer, System Designer |
| Status | Draft |
| OpenSpec change | `p6-autonomous-optimizer` |

## 1. Summary

P6 closes the loop. Every phase before it either measured (P4), localized (P4.5), or *proposed and
verified* a single change with a human in the seat (P5.5). P6 lets the system run the full cycle —
**analyze → propose → verify → apply** — on its own, under hard constraints, so a workflow improves
without a human clicking each step. Here **apply is a source-code change delivered as a pull
request** (ADR-001): a proposal is a candidate Variant Spec *and* the concrete source diff its
deterministic AST codemod produces, and **autonomous apply = the loop OPENS a pull request AND, under
the hard constraints with all gates green, MERGES it** — with no human clicking each step. This is
the new authority the Autonomous level grants; every level below it still requires a human to review
and merge. Two things make this safe rather than reckless. First, the search is **diagnosis-guided,
not blind**: the composite score (P4) is the objective it maximizes, the gates (P4) are its hard
constraints, and the P4.5 node+dimension attribution points the search at what to change — far more
sample-efficient than grid/Bayesian sweeps over the whole model×prompt×context space. Second, the
loop **merges nothing** until three operational prerequisites exist and are wired in: a **kill
switch**, a full **audit trail** (git history + an append-only change ledger), and **rollback** (git
revert of the merge commit to the exact prior state). Regression detection and budget alerts halt the
loop the moment any metric degrades past threshold or a budget is breached. Production failures feed
back as new eval cases and re-enter at P4 — the eval set is the system's living memory. Autonomous is
a distinct **trust contract**: the Product lens designs how a user grants that authority, watches it
live, sets its constraints, and stops it.

## 2. Problem & context

After P5.5 the engine emits a proposed diff with a *verified* delta (CI + cost/latency impact +
cases fixed/broken), but a human still opens and merges every pull request one at a time. That is
correct for Advisory (open a draft PR; human applies + merges) and Assisted (one-click open the
verified PR; human merges), and it is the ceiling of what those levels can do. It does not scale to a
workflow with a dozen nodes each carrying a fixable diagnosis, and it wastes the sample-efficiency
the diagnosis engine already bought — the machine knows *which node and which dimension* to change,
generates the codemod, verifies the transformed working copy, opens the PR, but a person still drives
every merge. Without P6:

- Optimization is manual and serial; a multi-node workflow with several diagnosed defects takes as
  many human sessions as there are fixes, and the search never runs overnight.
- The only automated search anyone would otherwise reach for is **blind** grid/Bayesian over the
  full configuration space — orders of magnitude more runs (and dollars) than a search pointed at
  the attributed node+dimension.
- There is no safe substrate for a machine to *merge* a change: no kill switch to stop a runaway
  loop, no audit trail (git history + change ledger) to reconstruct what it did and why, no rollback
  (git revert) to undo a regression. A loop that can merge source changes without these is an
  unbounded, irreversible production actor editing the user's own code — exactly the blast-radius the
  DevOps playbook exists to prevent.
- Production failures observed after a change never re-enter the eval set, so the system cannot
  learn from what broke in the wild.

**Upstream state assumed:** P4 (composite score + disqualifying gates + multi-seed/CI/tie harness —
the objective and hard constraints the search uses, and the verification substrate); the **source
transformation engine** (ADR-001) that applies a Variant Spec by generating a deterministic,
AST-level codemod against the user's source, delivered as a reviewable diff/PR; P4.5 (attribution to
node+dimension + typed diagnosis — what points the search); P5.5 (change operators driving the
codemod + held-out verification of the *transformed working copy* + regression check + the
Advisory/Assisted automation-level model — the propose/verify/open-PR half of the loop, run once per
proposal, now run repeatedly under automation); P5 (typed I/O contracts + dynamic tracing, so an
applied re-arrangement is validated, not silently broken); P2.5 (metrics substrate for
regression/budget signals); P2 (Runtime + run queue + idempotency for the run fan-out the loop
generates). P6 adds the search controller, the constraint/gate engine, the apply-path prerequisites
(kill switch; audit trail = git history + change ledger; rollback = git revert), the halt conditions,
the production-failure feedback intake, and the Autonomous-level governance UX that grants the loop
authority to open *and merge* pull requests.

## 3. Goals & non-goals

### Goals
- G1. **Diagnosis-guided search, not blind.** The search SHALL be pointed by the P4.5 node+dimension
  attribution — it enumerates and prioritizes candidate changes at the attributed node/dimension
  before considering any blind grid/Bayesian expansion, and SHALL record, for every candidate it
  evaluates, the diagnosis that motivated it.
- G2. **Composite score is the objective; gates are the hard constraints.** The search SHALL
  maximize the P4 composite score under the active weight profile and SHALL treat the P4 gates
  (budget ceiling, provider allowlist, min quality, latency SLA) as hard constraints — a candidate
  that violates any gate is never selected, regardless of its score.
- G3. **The loop may merge nothing without kill switch + audit trail + rollback.** Autonomous apply
  means the loop **opens a pull request AND merges it**; the merge step SHALL be disabled unless all
  three prerequisites are present and armed for the run — a **kill switch**, an **audit trail** (git
  history + the append-only change ledger), and **rollback** (git revert). This is a first-class
  precondition, not a configuration nicety; absent any one, the loop opens draft PRs but merges
  nothing.
- G4. **Enumerated hard-constraint gates as the loop's constraints:** budget ceiling (spend cap for
  the whole optimization run), provider allowlist, **min-improvement threshold** (a verified gain
  below it does not justify another iteration), and **max iterations**. These bound the loop in
  cost, providers, marginal value, and time.
- G5. **Kill switch — immediate, honored, auditable.** A user (or an automated halt) SHALL be able
  to stop the loop; after a stop no further pull request is merged, the in-flight iteration finishes
  or is abandoned safely, and the stop is recorded in the audit trail.
- G6. **Full audit trail = git history + change ledger.** Every decision the loop makes — candidate
  considered, motivating diagnosis, verification verdict, gate evaluation, PR open, merge, halt,
  rollback — SHALL be recorded in **git history** (the PR and merge commit) *and* an append-only
  **change ledger** (the optimization/audit records), keyed by the P0 tag set (`config_hash`,
  `variant_id`, `run_id`), sufficient to reconstruct *what changed, why, and with what measured
  effect*.
- G7. **Rollback via git revert.** Any applied change SHALL be reversible to the exact prior Variant
  Spec by **git revert** of the merge commit — restoring the byte-identical prior state, since every
  applied spec is content-addressed by `config_hash`; a rollback is itself audited.
- G8. **Regression & budget halt.** The loop SHALL halt automatically when regression detection finds
  any tracked metric degraded beyond its threshold versus the current best, or when a budget alert
  fires (run spend cap breached). A halt disarms the apply step until a human re-arms it.
- G9. **Feedback loop — production failures become eval cases.** Failures observed in production
  after an applied change SHALL be ingestible as new eval cases that re-enter at P4 (added to the
  eval set, coverage re-measured), so the eval set is the living memory and the next optimization
  run is measured against them.
- G10. **Verification-in-the-loop.** No candidate SHALL be merged on an unverified delta — every
  apply (i.e. merge) is gated by the P5.5 held-out verification of the *transformed working copy*
  (multi-seed, CI, significance, regression check); diagnosis proposes, verification decides, even
  when no human is in the seat. Concretely, autonomous apply opens a PR and merges it only when
  build (the codemod diff compiles) + eval (held-out verified gain) + regression check are all green.
- G11. **Loop-engineering discipline: it stops well.** The loop SHALL have explicit stopping
  conditions (min-improvement threshold, max iterations, budget), **stall/no-progress detection**
  (K iterations with no gate-passing verified gain), and safe recovery (a failed apply or a crashed
  iteration leaves the last-good Variant Spec live).
- G12. **Autonomous-level governance UX.** The Autonomous automation level SHALL be a distinct trust
  contract with an explicit **authority grant** (constraints set at grant time), a **live monitor**
  (current iteration, spend against ceiling, candidates applied, audit trail streaming), and an
  always-available **stop** control; the audit trail and rollback SHALL be visible throughout.

### Non-goals (explicitly deferred or owned elsewhere)
- **Advisory / Assisted automation levels** — **P5.5**. P6 adds the Autonomous level on top of the
  automation-level model P5.5 established; it does not redefine the lower levels.
- **The change operators themselves** (upgrade model, rewrite prompt, tune top-k, prune node, …) —
  **P5.5**. P6 *drives* the P5.5 operator catalog under search; it adds no new operators.
- **The verification gate mechanics** (held-out split, multi-seed, CI, significance, regression
  check) — **P4/P5.5**. P6 *invokes* the existing gate every iteration; it does not reimplement it.
- **The composite score, weight profiles, and the P4 gate definitions** — **P4**. P6 consumes them
  as objective + constraints unchanged.
- **Attribution + diagnosis** — **P4.5**. P6 consumes the node+dimension attribution to steer; it
  runs no new diagnosis method.
- **General-purpose production incident response / on-call for the target workflow** — out of scope.
  P6 owns halting *its own loop* safely, not operating the user's production system.
- **Multi-tenant scheduling / fair-share of the optimizer across many users** — deferred; P6 targets
  one optimization run over one workflow at a time with bounded fan-out.

## 4. Users & personas

- **Workflow owner (end user, primary)** — has a diagnosed, verified-improvable workflow and wants
  it optimized without babysitting each pull request. Grants Autonomous authority to open *and merge*
  PRs, sets the constraints (budget ceiling, provider allowlist, min-improvement, max iterations),
  watches the live monitor, and holds the stop control. Reviews the merged PRs after the fact and can
  **git revert** any applied change. Trust is the product: they must be able to see what the loop did
  (in git history + the change ledger) and undo it.
- **Platform / DevOps operator** — owns the operational guardrails: confirms the kill switch, the
  audit trail (git history + change ledger), and rollback (git revert) are armed before a loop may
  merge; owns the budget-alert and regression-halt thresholds; is paged if a loop halts abnormally.
  Cares about the blast radius of a machine editing and merging user code, and about reversibility,
  above throughput.
- **AI/ML engineer (power user)** — tunes the search policy (how aggressively diagnosis-guided vs.
  when to widen to blind search), curates the production-failure intake into eval cases, and audits
  whether the loop's applied changes actually held up.
- **Downstream subsystems** — the P4 leaderboard/score cache (the loop reads scores, writes new
  variants), the P5.5 verification gate (invoked per iteration), the eval set (re-seeded by the
  feedback loop), and the metrics substrate (regression/budget signals).

## 5. User stories / jobs-to-be-done

**Workflow owner**
- As a workflow owner, I want to grant the optimizer authority to run the full loop — opening *and
  merging* pull requests — under a budget ceiling, provider allowlist, min-improvement threshold, and
  max iterations, so that it improves my workflow overnight without me merging each PR.
- As a workflow owner, I want the search to go after the *diagnosed* node and dimension first rather
  than sweeping every combination, so that it converges in a handful of runs instead of thousands.
- As a workflow owner, I want a single, always-visible **stop** control that halts the loop
  immediately, so that I never feel it's running away from me.
- As a workflow owner, I want the loop to stop on its own once further iterations aren't buying a
  meaningful improvement, so that it doesn't burn budget chasing noise.
- As a workflow owner, I want to see, for any change the loop applied (merged), the diagnosis, the
  verified delta, the cost/latency impact, and a one-click **rollback** (git revert of the merge),
  so that I trust it and can undo any applied change.
- As a workflow owner, I want a failure I hit in production to become a new eval case, so that the
  next optimization run is measured against the thing that actually broke.

**Platform / DevOps operator**
- As an operator, I want the loop to refuse to merge anything unless the kill switch, the audit trail
  (git history + change ledger), and rollback (git revert) are all armed, so that no merged change is
  ever unattributable or irreversible.
- As an operator, I want the loop to halt automatically the moment any tracked metric regresses past
  threshold or the run breaches its budget, so that a bad iteration can't compound.
- As an operator, I want an applied change reconstructed from git history + the change ledger and
  reverted with **git revert**, so that recovery does not depend on anyone remembering what the loop
  did.

**AI/ML engineer**
- As an ML engineer, I want every candidate the loop evaluated recorded with its motivating
  diagnosis and verification verdict, so that I can audit whether diagnosis-guidance actually beat a
  blind sweep.
- As an ML engineer, I want a candidate merged only on a held-out verified gain from running the
  transformed working copy, so that the loop can't overfit to the cases that generated the proposal.

## 6. Functional requirements

These map 1:1 to the OpenSpec requirements under
`openspec/changes/archive/2026-07-23-p6-autonomous-optimizer/specs/autonomous-optimizer/`.

Autonomous **apply = open a pull request AND merge it** (ADR-001). A proposal is a candidate Variant
Spec *and* the concrete source diff its deterministic AST codemod produces; the merge is **gated by
build (the codemod diff compiles) + eval (held-out verified gain via the P5.5 gate) + regression
check**, and — for every automation level *below* Autonomous — by human review/approval. Autonomous
is the only level where the gates + hard constraints substitute for the human merge click, and even
then the merge requires all gates green.

**The optimizer — objective, constraints, diagnosis-guided search**
- FR1. The optimizer SHALL maximize the **P4 composite score** (under the active weight profile) as
  its objective function, and SHALL treat the **P4 gates** as hard constraints — a candidate
  Variant Spec that fails any gate SHALL never be merged, regardless of its composite score.
- FR2. The search SHALL be **diagnosis-guided**: it SHALL enumerate candidate changes at the
  node+dimension identified by the P4.5 attribution *before* expanding to blind grid/Bayesian search
  over the wider model×prompt×context space, and SHALL record the motivating diagnosis for every
  candidate it evaluates.
- FR3. The optimizer SHALL only apply (merge) a candidate whose improvement has passed the **P5.5
  held-out verification** run against the *transformed working copy* (multi-seed, CI, significance
  vs. current best, regression check) — diagnosis proposes, verification decides, with no human in
  the seat.

**Hard-constraint gates (the loop's operational constraints)**
- FR4. The loop SHALL enforce, as hard constraints for the whole optimization run: a **budget
  ceiling** (cumulative spend cap), a **provider allowlist**, a **min-improvement threshold**, and a
  **max-iterations** bound. Each is set at authority-grant time and is immutable for the run unless
  the run is stopped and re-granted.
- FR5. When the cumulative verified improvement from a further iteration would fall **below the
  min-improvement threshold**, the loop SHALL stop iterating (declare convergence) rather than
  continue.
- FR6. When the run reaches **max iterations**, the loop SHALL stop even if candidates remain.

**Apply-path prerequisites (kill switch + audit trail + rollback), in git terms**
- FR7. The loop SHALL **merge no** change unless a **kill switch**, an **audit trail** (git history +
  the append-only change ledger), and a **rollback** capability (git revert) are all present and
  armed for the run. Absent any one, the merge step is disabled and the loop may run only in a
  propose/verify (dry-run) mode that opens draft PRs and **merges nothing**.
- FR8. The **kill switch** SHALL stop the loop on demand; after it fires, no further pull request is
  merged, the in-flight iteration is finished or abandoned leaving the last-good Variant Spec live,
  and the stop is recorded in the audit trail (change ledger + git history).
- FR9. The **audit trail** SHALL record every loop decision — candidate considered, motivating
  diagnosis, verification verdict, gate evaluation, PR open, merge, halt, and rollback — as **git
  history** (the PR and merge commit) *and* an **append-only change ledger**, keyed by the P0 tag
  set, sufficient to reconstruct *what changed, why, and with what measured effect*. The apply SHALL
  be **write-ahead-audited**: the change-ledger event commits (and the PR/merge is recorded in git
  history) before/as the merge lands, so no merged change escapes the trail.
- FR10. Any **applied change SHALL be reversible** to the exact prior Variant Spec by **git revert**
  of the merge commit — restoring the byte-identical prior state (matching the prior `config_hash`);
  the rollback SHALL itself be recorded in the audit trail.

**Halt conditions (regression + budget)**
- FR11. The loop SHALL **halt automatically** when regression detection finds any tracked metric
  degraded beyond its configured threshold versus the current best, or when a budget alert fires
  (cumulative run spend breaches the budget ceiling). A halt SHALL disarm the merge step until a
  human explicitly re-arms it.
- FR12. The loop SHALL detect **stall / no-progress** — K consecutive iterations producing no
  gate-passing, verification-passing improvement — and stop rather than wander.

**Feedback loop — living memory**
- FR13. A **production failure** observed after an applied change SHALL be ingestible as a **new eval
  case** that re-enters at P4 (added to the eval set, coverage re-measured), so the next optimization
  run is measured against it. The eval set is the living memory of the system.

**Autonomous-level governance UX**
- FR14. **Autonomous** SHALL be a distinct automation level with an explicit **authority grant**: the
  user sets the hard constraints (budget ceiling, provider allowlist, min-improvement threshold, max
  iterations) at grant time, and the grant is recorded in the audit trail.
- FR15. While a loop runs, the UI SHALL present a **live monitor** — current iteration, cumulative
  spend against the ceiling, pull requests opened and merged, and the streaming audit trail — and an
  always-available **stop** control wired to the kill switch.
- FR16. The audit trail (git history + change ledger) and a **rollback** control (git revert) SHALL
  be visible for every applied change, so a user can see what the loop merged and undo any of it.

## 7. Non-functional requirements

- **Reversibility (first-class, load-bearing).** Every applied change is reversible by **git revert**
  of the merge commit to the exact prior Variant Spec (FR10). The one irreversible surface — a
  production side effect a change caused downstream of the workflow — is out of the loop's apply
  scope; the loop reverts the *merge* (and thus the Variant Spec), and the DevOps lens states plainly
  what the loop cannot un-happen. Tested by merging a change and git-reverting it to the
  byte-identical prior spec (`config_hash` match).
- **Bounded blast radius / cost.** A run's cumulative provider spend never exceeds the budget ceiling
  (FR4, FR11); the loop's run fan-out goes through the P2 queue with bounded concurrency and
  backpressure; a redelivered run does not double-charge (inherits P2 idempotency). The worst case is
  a run that spends up to the ceiling and merges zero changes — never an unbounded spend or an
  unattributable merge.
- **Auditability / reproducibility.** The audit trail is **git history + an append-only change
  ledger** keyed by `{config_hash, variant_id, run_id, timestamp}`; replaying the ledger against git
  history reconstructs the exact sequence of applied (merged) specs. Every applied Variant Spec is
  content-addressed so "what is live now" and "what was live at iteration k" are exact, not
  approximate.
- **Halt latency.** A kill-switch stop and an automated regression/budget halt SHALL take effect
  before the next merge — no candidate is merged after a stop/halt is raised. In-flight verification
  runs may finish, but their result is discarded rather than merged.
- **Safe degradation (System Designer lens).** If the search controller, the verification service,
  the queue, or the change ledger is unavailable, the loop **fails closed** — it stops merging and
  leaves the last-good Variant Spec live — rather than merging unverified or unaudited. No single
  point of failure on the **apply path**: the merge requires the change-ledger audit write (and the
  git record) to succeed first (write-ahead), so a merge that isn't audited cannot happen. The
  codemod **build gate** (the diff must compile) is likewise on the critical path — a transform that
  fails to build is never merged.
- **Least privilege / secrets.** The loop actuates only through the Runtime + registries; it holds no
  ambient provider credentials of its own, executes candidate runs only in the P3 sandbox, and never
  writes prompts/keys/PII into the audit trail inline (content-hashed blobs, hashes in the record).
- **Observability.** Loop state (iteration, spend, best score, halt reason), regression/budget
  signals, and every merge/rollback emit metrics + traces on the P2.5 substrate; the live monitor and
  the operator alerts read the same signals, never derived state that can drift.
- **Accessibility & performance (UI).** The live monitor updates without blocking; the audit trail is
  virtualized for long runs; the **stop** control is keyboard-reachable and always visible (never
  scrolled off); status color follows the **dataviz** skill for contrast and light/dark consistency;
  loading / running / halted / stopped / rolled-back are first-class, visually distinct states.

## 8. System design summary

**The closed loop.**

```mermaid
graph TB
  subgraph Grant[Authority grant Product/DevOps]
    C[Constraints: budget ceiling · provider allowlist<br/>min-improvement · max iterations]
    PRE{Kill switch + audit trail git history+ledger<br/>+ rollback git revert armed?}
  end
  C --> PRE
  PRE -->|no| DRY[Propose/verify + open draft PR only<br/>merges nothing]
  PRE -->|yes| LOOP
  subgraph LOOP[Optimization loop]
    DIAG[P4.5 attribution<br/>node + dimension] --> SEARCH[Diagnosis-guided search<br/>candidate Variant Spec + AST codemod diff]
    SEARCH --> VERIFY[P5.5 held-out verification of transformed copy<br/>build · multi-seed · CI · sig · regression]
    VERIFY -->|gate pass + real gain| GATES{P4 gates pass?<br/>score improved?}
    VERIFY -->|no gain / regression| HALTCHK
    GATES -->|yes| APPLY[Open PR → gates+constraints → merge PR<br/>write-ahead ledger + git history]
    GATES -->|no| HALTCHK
    APPLY --> AUDIT[(Audit trail<br/>git history + change ledger)]
    APPLY --> HALTCHK{Halt?<br/>regression · budget · stall<br/>min-improvement · max-iter · KILL}
    HALTCHK -->|continue| DIAG
    HALTCHK -->|stop| STOP[Disarm merge · last-good spec live]
  end
  AUDIT --> ROLL[Rollback control · git revert merge]
  ROLL --> AUDIT
  PROD[Production failure] --> INTAKE[New eval case] --> P4[(Eval set @ P4<br/>coverage re-measured)]
  P4 --> DIAG
  LOOP --> MON[Live monitor: iteration · spend/ceiling<br/>PRs opened+merged · streaming audit · STOP]
```

**Data model (System Designer lens).**
- **Postgres** — `optimization_run` (`run_id` PK, workflow, active weight profile, constraints
  snapshot, state ∈ {running, converged, max-iter, halted-regression, halted-budget, stalled,
  stopped}, armed-prereqs flags); `optimization_iteration` (`run_id` FK, iteration index, motivating
  `diagnosis_id`, candidate `config_hash`, verification verdict + delta + CI, gate status,
  merged bool); `audit_event` (append-only: `run_id`, seq, type ∈ {grant, consider, verify, open_pr,
  merge, halt, stop, rollback}, actor, `config_hash` before/after, pr_ref, merge_commit,
  payload_blob_hash, timestamp) — this is the **change ledger** that complements git history and the
  write-ahead record the apply path depends on; `applied_change` (`run_id`, from_config_hash →
  to_config_hash, merge_commit, reversible bool, reverted_by seq) — the from/to config_hash plus the
  merge commit are what a `git revert` reconstruction keys off. `optimization_run.constraints` is
  immutable per run.
- **Object store** — candidate Variant Specs, verification result blobs, rendered diffs (the codemod
  output), and the before/after specs for rollback — all content-hashed; the change-ledger record
  holds hashes only. Git history holds the authoritative merged source; the ledger holds the
  attribution and measured effect.
- **TSDB / span store (P2.5)** — loop metrics (iteration, cumulative spend, best score), regression
  and budget signals; the run fan-out's per-run traces.

**Key interfaces.**
- `Search.NextCandidates(diagnosis, current_spec, policy) → []VariantSpec` (diagnosis-guided first,
  blind expansion only after).
- `Verify(candidate, held_out, seeds) → {delta±ci, sig, regression, gate_status}` (the P5.5 gate,
  invoked per iteration).
- `Apply(candidate) → AppliedChange` — **opens a PR and merges it**; **precondition:** prerequisites
  armed (kill switch + audit trail + rollback) and build+eval+regression gates green; **write-ahead:**
  the change-ledger audit event commits (and the PR/merge is recorded in git history) before/as the
  merge lands; returns the reversible handle (merge commit).
- `Rollback(applied_change) → VariantSpec` (**git revert** of the merge commit to the byte-identical
  prior spec; audited).
- `Halt(reason)` / `Stop()` — disarm merge, leave last-good spec live, record the reason.
- `IngestProductionFailure(trace) → EvalCase` — re-enters at P4; coverage re-measured.
- `Grant(constraints) → Authority` — records the grant, arms the run subject to prerequisites.

## 9. Design by role lens

**AI Engineer (co-lead) — *evals before optimization; verification decides; the loop must stop well.***
This is where the playbook's phase 10 closes onto phase 3: production failures become new eval cases
and re-enter the harness — *the eval set is the living memory of the system*, and P6 is what wires
that loop shut. The discipline lands as:
- *The objective is the composite score; the constraints are the gates.* The search does not invent
  a new success signal — it maximizes the exact P4 composite under the active profile and treats the
  P4 gates as inviolable. This keeps the optimizer honest: it can't "win" by ranking noise or by
  topping a cost board with a broken variant, because those are already handled at the objective/gate
  layer it inherits.
- *Diagnosis-guided beats blind — and it's measured.* The search points at the P4.5 node+dimension
  attribution first; blind grid/Bayesian is the fallback, not the default. Every candidate records
  its motivating diagnosis so the sample-efficiency claim is auditable, not asserted. This is the
  whole reason the loop is affordable.
- *Verification-in-the-loop is non-negotiable.* Every apply is gated by the P5.5 held-out
  verification — multi-seed, CI, significance, regression — so *diagnosis proposes, verification
  decides* holds even with no human present. The loop cannot apply a candidate on the cases that
  generated it (held-out split), so it can't overfit its own recommendation.
- *Loop engineering — how it stops is the whole game.* Explicit stopping conditions
  (min-improvement threshold, max iterations, budget), **stall/no-progress detection** (K iterations
  with no gate-passing verified gain), verification separated from generation, and recovery that
  leaves the last-good spec live. An optimizer that reliably converges is distinguished from one that
  burns money wandering toward a plausible-but-wrong config precisely by these.
- *The feedback intake is a first-class input.* A production failure is not a log line — it is a new
  eval case that re-enters at P4, re-measures coverage, and becomes something the next run is scored
  against. This is the memory-engineering discipline applied to the whole system: write selectively
  (real failures), retrieve relevantly (into the eval set), and measure that it helps.

**DevOps (co-lead) — *blast radius before implementation; reversible or say it isn't; observable; least privilege.***
An autonomous loop that **edits the user's own source code and merges the pull request** is the
highest-blast-radius actor in the platform (ADR-001), so the guardrails come *before* the capability,
not after. Two things soften the radius by construction: every change is a **reviewable PR** a human
can read, and **nothing merges without the gates** (build + eval + regression) — Autonomous only
removes the human *merge click*, never the gates.
- *Prerequisites before power.* The loop **merges nothing** unless a kill switch, an audit trail (git
  history + change ledger), and rollback (git revert) are all armed (FR7). This is the direct
  expression of "reversible — or you must say it isn't": without git revert there is no reversibility,
  so there is no merge. Absent any prerequisite the loop is confined to a dry-run that proposes,
  verifies, and opens draft PRs but merges nothing.
- *Hard constraints as gates, not hopes.* Budget ceiling, provider allowlist, min-improvement
  threshold, and max iterations are set at grant time and immutable for the run. The worst case is
  bounded: spend up to the ceiling, merge zero changes — never unbounded spend, never an
  unattributable merge.
- *Halt on the first sign of harm.* Regression detection and budget alerts halt the loop the moment a
  metric degrades past threshold or spend breaches the ceiling, and a halt *disarms* the merge step
  until a human re-arms it — degradation cannot compound across iterations.
- *Write-ahead audit is the apply path.* The merge requires the change-ledger event to commit *first*
  (write-ahead), and the PR/merge is recorded in git history; a merge that wasn't audited cannot
  occur. This is what makes "no single point of failure on the apply path" concrete — the change
  ledger is on the critical path by construction, and if it's unavailable the loop fails closed. The
  codemod **build gate** (the diff must compile) sits on the same path.
- *Least privilege / secrets.* The loop actuates only through the Runtime + registries, holds no
  ambient credentials, runs candidates in the P3 sandbox, and never writes prompts/keys/PII inline
  into the change ledger. If it isn't observable it isn't done: loop state, halt reasons, and every
  merge/rollback emit metrics + traces on the P2.5 substrate that the monitor and the alerts both
  read.

**Product Designer (co-lead) — *automation levels are trust contracts; design the unhappy path; content is the interface; name the tradeoff.***
Autonomous is not "Assisted with the button pressed for you" — it is a different contract about how
much authority the user hands the machine, and the UX has to make that grant deliberate, legible, and
revocable.
- *Granting authority is an explicit, bounded act.* The user sets the hard constraints (budget
  ceiling, provider allowlist, min-improvement, max iterations) *at grant time*; the grant is
  recorded. The design names the tradeoff plainly — "you are letting the system apply changes on its
  own within these limits" — so no one grants power they didn't understand.
- *Monitoring live is the trust surface.* A live monitor shows the current iteration, spend against
  the ceiling, what's been applied, and the streaming audit trail — with an always-visible **stop**
  control wired to the kill switch. Content is the interface: "halted — quality regressed on cluster
  X, apply disarmed" must say exactly that, not "error."
- *Design the unhappy path first.* The states that matter are the bad ones: loop halted by
  regression, loop stopped by the user mid-iteration, budget exhausted with no gain, an applied change
  the user wants to undo. Each has a designed screen, and rollback is one control away from any
  applied change.
- *Each automation level is its own contract.* Advisory (report), Assisted (one-click apply), and
  Autonomous (the full loop) are visibly distinct; a user always knows which contract is active and
  how to step down a level or stop entirely.

**System Designer (support) — *numbers before boxes; the failure story; no single point of failure on the apply path.***
Owns how the loop **degrades safely** and the **queue semantics** for the run fan-out it generates.
The design principle is fail-closed: if the search controller, verification service, queue, or change
ledger is unavailable, the loop stops merging and leaves the last-good Variant Spec live — it never
merges unverified or unaudited. The apply path is made single-point-of-failure-free by construction:
the change-ledger write is *write-ahead* of the merge, so no merge escapes the trail, and the ledger
is the one component the apply path cannot proceed without. The run fan-out (candidate × held-out
slice × seeds, per iteration) goes through the P2 queue with bounded concurrency, backpressure, and
idempotent redelivery, so a redelivered verification run doesn't double-charge or double-merge.
Content-addressing every applied Variant Spec **plus git history** makes "what is live now" and "what
was live at iteration k" exact — and a **git revert** of the merge commit reconstructs the prior
state precisely, which the substrate rollback and audit reconstruction depend on.

## 10. Dependencies

- **Requires (upstream):** **P4** (composite score = objective; disqualifying gates = hard
  constraints; multi-seed/CI/tie harness = verification substrate; eval set the feedback loop
  re-seeds); **P4.5** (node+dimension attribution + typed diagnosis that steers the search); **P5.5**
  (change-operator catalog the search drives; held-out verification gate + regression check invoked
  per iteration; the Advisory/Assisted automation-level model P6 extends); **P5** (typed I/O
  contracts + dynamic tracing so an applied re-arrangement is validated, not silently broken); **P2.5**
  (metrics substrate for regression + budget signals, and loop observability); **P2** (Runtime +
  registries the loop actuates through; run queue + idempotency for fan-out); **P3** (sandbox for
  candidate execution); **P0** (tag set + `config_hash` the audit trail and reproducibility depend on).
- **Consumes:** a diagnosed, verified-improvable workflow; a user authority grant with constraints; a
  human-set regression threshold and budget ceiling; optional production-failure traces for intake.
- **Unblocks:** the closed-loop optimizer as a product capability — the M9 milestone and the end of
  the intelligence half. Nothing further in this timeline depends on it; it is the terminal phase.

## 11. Risks & mitigations

| Risk | Owner | Mitigation |
|---|---|---|
| Loop merges a change with no way to undo it | DevOps | Merge disabled unless kill switch + audit trail (git history + change ledger) + rollback (git revert) all armed (FR7); merge is write-ahead-audited (FR9); every applied change reversible via git revert of the merge commit to the exact prior spec (FR10) |
| Runaway spend — loop burns budget searching | DevOps / AI | Budget ceiling as a hard constraint; budget alert halts the loop mid-run and disarms merge (FR4, FR11); fan-out bounded on the queue |
| Loop chases noise, iterating on sub-threshold gains | AI | Min-improvement threshold stops iteration (FR5); stall/no-progress detection stops after K barren iterations (FR12); every gain is CI-verified vs. current best |
| A blind grid/Bayesian sweep is used by default (sample-inefficient, costly) | AI | Search is diagnosis-guided first — candidates at the attributed node+dimension before any blind expansion (FR2); motivating diagnosis recorded per candidate |
| An applied change regresses another case cluster or the cost budget | AI / DevOps | Per-iteration regression check in verification of the transformed copy (FR3); regression detection halts the loop and disarms merge (FR11) |
| Change merged on the cases that generated it (overfit) | AI | Held-out verification split every iteration (FR3) — diagnosis proposes, verification decides |
| Kill switch fires but a PR still merges | DevOps / System Designer | Stop takes effect before the next merge; in-flight verification result discarded not merged; stop recorded in audit (FR8) |
| Change ledger down → unattributable merge | System Designer | Write-ahead audit: merge cannot proceed without the change-ledger commit (and the git record); loop fails closed, last-good spec stays live (§7) |
| Production failures never re-enter the system | AI | Production-failure intake creates a new eval case that re-enters at P4; coverage re-measured (FR13) |
| User grants authority they didn't understand | Product | Explicit authority grant with constraints set at grant time and recorded; the tradeoff (the loop may open *and merge* PRs) named in the grant UX (FR14) |
| Codemod breaks the build or silently changes behavior | System Designer / AI | Build gate — the codemod diff must compile before a candidate is proposed; candidates validated against the P5 typed I/O contracts before verification; a failing transform is never merged |
| Loop keeps running after a halt because merge was only paused, not disarmed | DevOps | A halt *disarms* merge until a human re-arms; re-arming is an explicit, audited action (FR11) |

## 12. Rollout & test strategy

- **Fixtures.** A multi-node workflow with two independently diagnosable defects (a reasoning-heavy
  node on a weak model; a RAG node with low relevance), a P4 eval set with a held-out slice, a P4.5
  attribution pointing at each defect, and the P5.5 operator catalog + verification gate wired in. A
  budget ceiling small enough that a deliberately non-converging search *hits* it; a min-improvement
  threshold a noise-level candidate falls below; a seeded regression a candidate triggers.
- **Prerequisite-gating tests.**
  - With rollback (git revert), or audit trail (git history + change ledger), or kill switch
    **absent**, the loop runs in dry-run — opens draft PRs and merges **zero** changes; assert no
    merge (no Variant Spec swap) occurs. (FR7)
  - With all three armed, the loop opens and merges a verified, gate-passing candidate; assert the
    merge is preceded by a change-ledger event (write-ahead) and recorded in git history. (FR7, FR9)
- **Diagnosis-guided-search test.** Assert the first candidates evaluated are at the P4.5-attributed
  node+dimension, *before* any blind grid/Bayesian candidate; assert each candidate carries its
  motivating diagnosis. (FR2)
- **Objective/constraint test.** A candidate with a higher composite score that fails a P4 gate is
  **not** merged; a lower-scoring gate-passing candidate is preferred. (FR1)
- **Budget-halt-mid-run test.** Drive a search that would exceed the ceiling; assert the loop halts
  when cumulative spend breaches the budget, merges nothing further, disarms merge, and records the
  halt. (FR11)
- **Min-improvement-stop test.** After a candidate whose verified gain is below the threshold, assert
  the loop stops iterating (converged), not continues. (FR5)
- **Rollback-via-git-revert test.** Merge a change, then `git revert` the merge commit; assert the
  live Variant Spec is byte-identical to the prior one (`config_hash` match) and the rollback is
  recorded in the audit trail. (FR10)
- **Regression-halt test.** A candidate that fixes the target cluster but regresses another is caught
  by the per-iteration regression check and not merged; a seeded post-merge regression halts the loop
  and disarms merge. (FR3, FR11)
- **Kill-switch test.** Fire the stop mid-iteration; assert no PR merges after the stop, the
  last-good spec stays live, and the stop is audited. (FR8)
- **Stall-detection test.** A search with no gate-passing verified gain for K iterations stops with a
  `stalled` state, not an infinite loop. (FR12)
- **Feedback-loop test.** Ingest a production-failure trace; assert a new eval case is added to the
  P4 eval set, coverage is re-measured, and the next run is scored against it. (FR13)
- **Fail-closed test.** Kill the change ledger (then the verification service) mid-run; assert the
  loop stops merging and leaves the last-good spec live — never merges unaudited/unverified. (§7)
- **UI verification.** Drive the Autonomous grant → live monitor → stop → rollback flow against a
  live (stubbed-provider) loop; confirm the grant records constraints, the monitor streams iteration
  + spend/ceiling + PRs opened+merged + audit, the stop control is always visible and halts
  immediately, and every applied change exposes a working rollback (git revert); confirm halted /
  stopped / rolled-back states render.
- **Rollout.** Ships **dark** and dry-run-only by default (opens draft PRs, merges nothing) until the
  fixtures' M9 checklist is green; Autonomous authority is off until a user explicitly grants it with
  constraints; operators must confirm kill switch + audit trail (git history + change ledger) +
  rollback (git revert) armed before merge is enabled. Migrations expand-only (new
  optimization/change-ledger tables).

## 13. Success metrics & acceptance criteria (M9 exit checklist)

- [x] The system autonomously runs **analyze → propose → verify → apply**, where apply **opens a
      pull request AND merges it** under hard constraints, with every applied change **auditable (git
      history + change ledger) and reversible (git revert)**.
- [x] The search is **diagnosis-guided** — candidates at the P4.5-attributed node+dimension are
      evaluated before any blind grid/Bayesian expansion, and each candidate records its motivating
      diagnosis.
- [x] The **composite score is the objective** maximized and the **P4 gates are hard constraints** —
      a higher-scoring gate-failing candidate is never merged.
- [x] The loop **merges nothing** unless kill switch + audit trail (git history + change ledger) +
      rollback (git revert) are all armed; absent any one, it opens draft PRs in dry-run only.
- [x] The enumerated hard constraints — **budget ceiling, provider allowlist, min-improvement
      threshold, max iterations** — bound the run.
- [x] A **budget breach halts the loop mid-run**, merges nothing further, and disarms merge.
- [x] A verified gain **below the min-improvement threshold stops** further iterations.
- [x] An applied change is **rolled back via git revert** of the merge commit to the byte-identical
      prior spec.
- [x] **Regression detection halts** the loop and disarms merge; no candidate is merged on an
      unverified or regressing delta.
- [x] The **kill switch** stops the loop immediately; no PR merges after a stop; the stop is
      audited.
- [x] **Stall/no-progress detection** stops a search that isn't improving, rather than wandering.
- [x] A **production failure re-enters at P4** as a new eval case; coverage is re-measured.
- [x] The **Autonomous level** exposes an authority grant to open+merge PRs (constraints recorded), a
      live monitor (iteration + spend/ceiling + PRs opened+merged + streaming audit + stop), and a
      visible rollback (git revert) per applied change.

## 14. Open questions

- Q1. **Search policy hand-off.** What exactly triggers widening from diagnosis-guided candidates to
  blind grid/Bayesian expansion — diagnosis exhausted, no gate-passing gain after J targeted
  candidates, or an explicit budget slice? (Proposed: widen only after targeted candidates are
  exhausted *and* budget remains, capped by a separate blind-search sub-budget.)
- Q2. **Min-improvement semantics.** Is the threshold on the *marginal* gain of the next iteration or
  the *cumulative* gain since the run start, and is it on the composite score or per-metric?
  (Proposed: marginal gain on the composite CI-lower-bound, so noise can't clear it.)
- Q3. **Multi-node interaction.** When two nodes each have a diagnosed fix, does the loop apply them
  independently or verify the combination (fixes can interact — improving node 3 may shift node 5's
  inputs)? (Proposed: apply serially, re-attribute after each apply, since an applied change can
  invalidate a pending diagnosis.)
- Q4. **Re-arm authority.** After a regression/budget halt disarms merge, who may re-arm — the
  original granter only, or any operator — and does re-arming require re-stating the constraints?
- Q5. **Production-failure trust.** A production-failure intake case has no gold reference; how is it
  labeled (weak, per P4) and can a weak-labeled failure case gate an apply, or only widen coverage?
  (Proposed: it widens coverage and can *block* via regression, but a weak case alone cannot be the
  sole basis for an apply.)
- Q6. **Concurrent runs on one workflow.** Are two Autonomous runs on the same workflow forbidden
  (lock), or serialized? A second run applying against a spec the first is mid-optimizing is a
  write-write hazard. (Proposed: one active run per workflow, enforced by a lock keyed on the
  workflow.)
- Q7. **Git revert depth.** Can a user roll back several merges at once (git revert to iteration k),
  or only the most recent merge? (Proposed: revert-to-any-prior-`config_hash` via git history, since
  every applied spec is content-addressed and each merge is a distinct commit.)
