# Design — P6: Autonomous optimizer

Cross-reference: product rationale in [`../../../docs/prd/P6-autonomous-optimizer.md`](../../../docs/prd/P6-autonomous-optimizer.md).

## Context

P6 is where the intelligence half's loop closes: the AI Engineer playbook's phase 10 ("ship safely;
failures become new eval cases") folds back onto phase 3 ("build the eval harness FIRST"), and the
result is a machine that improves a workflow on its own. Per **ADR-001**, applying a change means
transforming source and delivering it as a pull request; so **autonomous "apply" here means the loop
opens a PR and — with every gate green, under the hard constraints — MERGES it**, and the operational
substrate is expressed in git terms: **audit trail = git history + a change ledger**, **rollback =
`git revert`**. Three forces shape every decision. First, an autonomous actor that can **merge**
changes into a production workflow's source is the highest-blast-radius component in the platform — so
the DevOps guardrails (kill switch, audit trail, rollback, hard constraints, halts) are
*prerequisites*, not features layered on later. Second, blind search over
model×prompt×context is affordable only if it's rarely used — so the search is **diagnosis-guided**,
pointed by the P4.5 attribution, with blind expansion as a bounded fallback. Third, the loop must
**stop well** — an optimizer that wanders burns money reaching a plausible-but-wrong config — so
loop engineering (stopping conditions, stall detection, verification-in-the-loop, recovery) is
first-class. P6 adds almost no new *evaluation* machinery: the objective (composite score), the
constraints (gates), the verifier (held-out gate), the operators + their codemod and build gate (P5.5
catalog / ADR-001 source transformation engine) all already exist. P6 is the **controller** that
drives them and turns Assisted's "open a PR" into "open **and merge** a PR," plus the operational
substrate (git-history + change-ledger audit, `git revert` rollback, kill switch) that makes merging
safe.

## Decision 1 — The objective and constraints are inherited, not reinvented

**Decision.** The search objective **is** the P4 composite score under the active weight profile; the
search's hard constraints **are** the P4 gates (budget ceiling, provider allowlist, min quality,
latency SLA). A candidate that fails any gate is never selected or applied, regardless of its
composite score. P6 adds no new success signal.

**Why.** The honesty properties that P4 fought for — normalization, gates-disqualify-not-penalize,
CIs, tie-on-overlap — are exactly what stop a search from "winning" by ranking noise or topping a
cost board with a broken variant. Inheriting the objective/constraint layer means the optimizer can't
bypass them. Reinventing an objective inside the search would re-open every honesty hole P4 closed.

**Alternative rejected.** A bespoke search reward (e.g. raw accuracy) — decouples the loop from the
leaderboard users trust and re-admits the cheap-but-broken failure mode.

## Decision 2 — Diagnosis-guided search first, blind expansion as a bounded fallback

**Decision.** `Search.NextCandidates` enumerates candidate changes at the **P4.5-attributed
node+dimension** (through the P5.5 operator catalog) *before* any grid/Bayesian expansion over the
wider space. Blind expansion triggers only when targeted candidates are exhausted *and* budget
remains, and it draws from a **separate blind-search sub-budget** so it can never consume the whole
ceiling. Every candidate records its motivating `diagnosis_id`.

**Why.** The diagnosis engine already localized the failure to a node and a dimension; sweeping the
whole configuration space blindly throws that information away and costs orders of magnitude more
runs. Recording the motivating diagnosis per candidate makes the sample-efficiency claim *auditable*
— an ML engineer can check that guidance actually beat a sweep, rather than trust the assertion.

**Alternative rejected.** Pure grid/Bayesian from the start — sample-inefficient and expensive; the
whole point of building attribution (P4.5) was to steer the search. Diagnosis-only with no blind
fallback — leaves gains on the table when the diagnosis is incomplete.

**Open (Q1).** The exact widen trigger and the blind sub-budget size.

## Decision 3 — The loop merges nothing without kill switch + audit trail (git history + ledger) + rollback (git revert)

**Decision.** The merge step is **disabled** unless all three prerequisites are present and armed for
the run: a **kill switch**, an **audit trail** (**git history + the change ledger**), and **rollback**
(**`git revert`**). Absent any one, the loop runs in a propose/verify **dry-run** that may open draft
PRs but **merges nothing**. This is a hard precondition on `Apply` (which now opens+merges a PR),
checked before every merge, not a config default. Every merge is additionally gated by **build + eval
+ regression**.

**Why.** This is the direct expression of the DevOps directive *"every change must be reversible — or
you must say it isn't."* Without `git revert` there is no reversibility, so there is no merge. Without
the git-history + change-ledger trail a merged change is unattributable. Without a kill switch a loop
is unstoppable. A loop that could merge without these is an unbounded, irreversible production actor —
precisely the thing the blast-radius directive exists to prevent. Making the prerequisites a *gate on
merge* (rather than documentation) means the unsafe configuration is unrepresentable. Editing user
source and merging it is exactly why the delivery is a reviewable PR at every level and why below
Autonomous a human must approve the merge.

**Alternative rejected.** Ship merge with rollback "coming soon" — the exact anti-pattern (apply
first, safety later) the playbook forbids.

## Decision 4 — Write-ahead audit: the change ledger is on the merge path by construction

**Decision.** `Apply` commits the **change-ledger event first**, then merges the PR (recording the
merge in git history). The merge cannot proceed until the ledger write succeeds; the merge commit ref
is written back to the ledger. If the change-ledger store is unavailable, the merge fails closed and
the last-good (currently-merged) spec stays live.

**Why.** This is what makes "no single point of failure on the merge path" concrete rather than
aspirational. Git history alone records *that* a merge happened but not the loop's decision context
(diagnosis, verdict, gate evaluation); the change ledger records *why*, and the two together are the
audit trail. The naive design (merge, then log) can merge a change that never gets recorded in the
ledger — an unattributable mutation on exactly the failure it's supposed to survive. Putting the
ledger write *ahead* of the merge makes an unaudited merge impossible: the ledger is deliberately on
the critical path, so its failure stops merges rather than producing silent ones.

**Alternative rejected.** Async/best-effort ledger logging — fast, but admits merges with no decision
trail; unacceptable for the one component whose entire job is reversibility and attribution.

## Decision 5 — Halt disarms apply until a human re-arms

**Decision.** A regression halt or a budget-breach halt does not merely pause — it **disarms** the
merge step. The loop cannot resume merging until a human explicitly re-arms it (an audited action).
Stall/no-progress and min-improvement/max-iteration are *stops* (the run ends); regression/budget are
*halts* (merge disarmed, run may be inspected and re-armed).

**Why.** A regression or budget breach is evidence the loop is doing harm; auto-resuming would let the
harm compound across merges. Requiring a human to re-arm forces a look at *why* it halted before more
changes land. Distinguishing stop (terminal) from halt (disarm-until-re-arm) keeps the semantics
legible in the audit trail and the UI.

**Open (Q4).** Who may re-arm (original granter vs. any operator) and whether re-arming re-states the
constraints.

## Decision 6 — Verification-in-the-loop on a held-out split every iteration

**Decision.** Every apply (merge) is gated by **build + eval + regression**: the candidate's codemod
diff must compile (the P5.5 build gate), then the P5.5 held-out verification — multi-seed, mean+CI,
significance vs. the current best, regression check — run on a **held-out** slice (not the cases that
generated the proposal) must pass, and the P4 gates must hold. Diagnosis proposes; verification
decides; no human in the seat changes that.

**Why.** The single most dangerous thing an autonomous loop can do is trust its own proposal.
Verifying on the generating cases overfits the recommendation; verifying on a held-out slice is the
only way "improvement" means a real, generalizing gain. Reusing the P5.5 gate (rather than a lighter
in-loop check) keeps the same statistical bar a human would have gotten at the Assisted level.

## Decision 7 — Fail-closed degradation; one active run per workflow

**Decision.** If the search controller, verification service, queue, or change-ledger store is
unavailable, the loop **fails closed**: it stops merging and leaves the last-good (currently-merged)
Variant Spec live. At most one Autonomous run is active per workflow, enforced by a lock keyed on the
workflow.

**Why.** The safe failure of an optimizer is *doing nothing*, not *merging unverified/unaudited*. A
second concurrent run merging against a branch the first is mid-optimizing is a write-write hazard on
the repo; a per-workflow lock removes it. This is the System Designer's failure story: every
degradation path ends with the last-good spec live and no unattributable merge.

**Open (Q6).** Whether the second run is rejected outright or queued behind the lock.

## Data model sketch

```
optimization_run
  run_id PK · workflow_id · weight_profile · constraints_snapshot(jsonb, immutable)
  · state ∈ {running, converged, max_iter, halted_regression, halted_budget, stalled, stopped}
  · kill_switch_armed bool · audit_armed bool · rollback_armed bool
  · merge_enabled bool (⇔ all three armed AND not halted)
  · cumulative_spend · best_config_hash · lock(workflow_id) UNIQUE WHERE state='running'

optimization_iteration
  run_id FK · idx · diagnosis_id · candidate_config_hash · source_diff_blob_hash · builds bool
  · verify_delta · verify_ci · verify_sig bool · regression bool · gate_status
  · merged bool · pr_ref · merge_commit · source ∈ {diagnosis_guided, blind}

change_ledger_event    -- append-only; the write-ahead record the merge path depends on (complements git history)
  run_id · seq PK · type ∈ {grant, consider, verify, apply(open+merge PR), halt, stop, revert}
  · actor · from_config_hash · to_config_hash · pr_ref · merge_commit · payload_blob_hash · ts
  -- tagged with the P0 set {config_hash, variant_id, run_id, timestamp}

applied_change
  run_id · from_config_hash → to_config_hash · merge_commit · reversible bool · reverted_by_seq NULL
```

Candidate Variant Specs, source diffs, verification result blobs, rendered diffs, and before/after
specs live in the object store, content-hashed; `change_ledger_event` and `applied_change` hold hashes
+ git refs only. Every applied spec is content-addressed and every merge is a commit in git history,
so "what is live now" and "what was live at iteration k" are exact — **rollback is `git revert` of the
merge commit** (reconstructing `from_config_hash`) and audit replay = git history + the change ledger.

## Interfaces

- `Grant(constraints) → Authority` — records the grant event; arms the run iff kill switch + audit
  (git history + change ledger) + rollback (`git revert`) present.
- `Search.NextCandidates(diagnosis, current_spec, policy) → []VariantSpec` — diagnosis-guided first;
  blind expansion only after, from a separate sub-budget.
- `Verify(candidate, held_out, seeds) → {builds, delta±ci, sig, regression, gate_status}` — the P5.5
  build + held-out verification gate on the transformed working copy.
- `Apply(candidate) → AppliedChange` — precondition: `merge_enabled` AND build+eval+regression green;
  **opens a PR and merges it**, with the **write-ahead** change-ledger event committed before the
  merge (which is recorded in git history).
- `Rollback(applied_change | to_config_hash) → VariantSpec` — **`git revert`** of the merge commit
  reconstructs the prior spec; audited in the change ledger.
- `Halt(reason)` — disarm merge, record reason (regression | budget). `Stop()` — terminal, last-good
  spec live, audited.
- `IngestProductionFailure(trace) → EvalCase` — re-enters at P4; coverage re-measured.

## Risks

- **Change-ledger store becomes a throughput bottleneck** (it's on every merge path). Mitigation:
  ledger writes are small append-only rows; the merge cadence is bounded by verification latency (many
  runs per merge), so the ledger is never the hot path.
- **Diagnosis is stale after an apply** — an earlier attribution may no longer hold once a node is
  changed. Mitigation: re-attribute after each apply; apply serially, not in a batch (design Q3).
- **A weak-labeled production-failure case drives an apply.** Mitigation: intake cases are weak-
  labeled (P4); a weak case can widen coverage and *block* via regression but cannot be the sole basis
  for an apply (design Q5).
- **Reversibility boundary.** The loop reverts the *merged source change* via `git revert`; a
  downstream production side effect a change caused is outside the loop's apply scope and is stated as
  not-un-happen-able, per the DevOps "say it isn't reversible" directive.
- **A bad codemod is merged.** Mitigation: the P5.5 build gate (the diff must compile) plus held-out
  eval + regression gate every merge; a non-building or regressing candidate is never merged. Editing
  user source is why every merge below Autonomous requires human review, and why Autonomous merges
  only with all gates green.

## Open questions

Carried to PRD §14: Q1 blind-expansion trigger + sub-budget; Q2 min-improvement semantics
(marginal vs. cumulative, composite CI-lower-bound); Q3 multi-node fix interaction (serial + re-
attribute); Q4 re-arm authority; Q5 production-failure case trust; Q6 concurrent-run lock semantics;
Q7 rollback depth (`git revert` a single merge vs. revert-to-any-prior-`config_hash` across merges).
