# Design — P6: Autonomous optimizer

Cross-reference: product rationale in [`../../../docs/prd/P6-autonomous-optimizer.md`](../../../docs/prd/P6-autonomous-optimizer.md).

## Context

P6 is where the intelligence half's loop closes: the AI Engineer playbook's phase 10 ("ship safely;
failures become new eval cases") folds back onto phase 3 ("build the eval harness FIRST"), and the
result is a machine that improves a workflow on its own. Three forces shape every decision. First,
an autonomous actor that can **apply** changes to a production workflow is the highest-blast-radius
component in the platform — so the DevOps guardrails (kill switch, audit trail, rollback, hard
constraints, halts) are *prerequisites*, not features layered on later. Second, blind search over
model×prompt×context is affordable only if it's rarely used — so the search is **diagnosis-guided**,
pointed by the P4.5 attribution, with blind expansion as a bounded fallback. Third, the loop must
**stop well** — an optimizer that wanders burns money reaching a plausible-but-wrong config — so
loop engineering (stopping conditions, stall detection, verification-in-the-loop, recovery) is
first-class. P6 adds almost no new *evaluation* machinery: the objective (composite score), the
constraints (gates), the verifier (held-out gate), and the operators (P5.5 catalog) all already
exist. P6 is the **controller** that drives them, plus the operational substrate that makes applying
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

## Decision 3 — The loop applies nothing without kill switch + audit trail + rollback

**Decision.** The apply step is **disabled** unless all three prerequisites are present and armed for
the run. Absent any one, the loop runs in a propose/verify **dry-run** that changes nothing. This is
a hard precondition on `Apply`, checked before every apply, not a config default.

**Why.** This is the direct expression of the DevOps directive *"every change must be reversible — or
you must say it isn't."* Without rollback there is no reversibility, so there is no apply. Without an
audit trail a change is unattributable. Without a kill switch a loop is unstoppable. A loop that could
apply without these is an unbounded, irreversible production actor — precisely the thing the blast-
radius directive exists to prevent. Making the prerequisites a *gate on apply* (rather than
documentation) means the unsafe configuration is unrepresentable.

**Alternative rejected.** Ship apply with rollback "coming soon" — the exact anti-pattern (apply
first, safety later) the playbook forbids.

## Decision 4 — Write-ahead audit: the audit store is on the apply path by construction

**Decision.** `Apply` commits the **audit event first**, then swaps the live Variant Spec. The spec
swap cannot proceed until the audit write succeeds. If the audit store is unavailable, the apply
fails closed and the last-good spec stays live.

**Why.** This is what makes "no single point of failure on the apply path" concrete rather than
aspirational. The naive design (apply, then log) can apply a change that never gets recorded — an
unattributable, unrollbackable mutation on exactly the failure it's supposed to survive. Putting the
audit write *ahead* of the swap makes an unaudited apply impossible: the audit store is deliberately
on the critical path, so its failure stops applies rather than producing silent ones.

**Alternative rejected.** Async/best-effort audit logging — fast, but admits applies with no trail;
unacceptable for the one component whose entire job is reversibility and attribution.

## Decision 5 — Halt disarms apply until a human re-arms

**Decision.** A regression halt or a budget-breach halt does not merely pause — it **disarms** the
apply step. The loop cannot resume applying until a human explicitly re-arms it (an audited action).
Stall/no-progress and min-improvement/max-iteration are *stops* (the run ends); regression/budget are
*halts* (apply disarmed, run may be inspected and re-armed).

**Why.** A regression or budget breach is evidence the loop is doing harm; auto-resuming would let the
harm compound across iterations. Requiring a human to re-arm forces a look at *why* it halted before
more changes land. Distinguishing stop (terminal) from halt (disarm-until-re-arm) keeps the semantics
legible in the audit trail and the UI.

**Open (Q4).** Who may re-arm (original granter vs. any operator) and whether re-arming re-states the
constraints.

## Decision 6 — Verification-in-the-loop on a held-out split every iteration

**Decision.** Every apply is gated by the P5.5 held-out verification — multi-seed, mean+CI,
significance vs. the current best, regression check — run on a **held-out** slice, not the cases that
generated the proposal. Diagnosis proposes; verification decides; no human in the seat changes that.

**Why.** The single most dangerous thing an autonomous loop can do is trust its own proposal.
Verifying on the generating cases overfits the recommendation; verifying on a held-out slice is the
only way "improvement" means a real, generalizing gain. Reusing the P5.5 gate (rather than a lighter
in-loop check) keeps the same statistical bar a human would have gotten at the Assisted level.

## Decision 7 — Fail-closed degradation; one active run per workflow

**Decision.** If the search controller, verification service, queue, or audit store is unavailable,
the loop **fails closed**: it stops applying and leaves the last-good Variant Spec live. At most one
Autonomous run is active per workflow, enforced by a lock keyed on the workflow.

**Why.** The safe failure of an optimizer is *doing nothing*, not *applying unverified/unaudited*. A
second concurrent run applying against a spec the first is mid-optimizing is a write-write hazard on
the live spec; a per-workflow lock removes it. This is the System Designer's failure story: every
degradation path ends with the last-good spec live and no unattributable change.

**Open (Q6).** Whether the second run is rejected outright or queued behind the lock.

## Data model sketch

```
optimization_run
  run_id PK · workflow_id · weight_profile · constraints_snapshot(jsonb, immutable)
  · state ∈ {running, converged, max_iter, halted_regression, halted_budget, stalled, stopped}
  · kill_switch_armed bool · audit_armed bool · rollback_armed bool
  · apply_enabled bool (⇔ all three armed AND not halted)
  · cumulative_spend · best_config_hash · lock(workflow_id) UNIQUE WHERE state='running'

optimization_iteration
  run_id FK · idx · diagnosis_id · candidate_config_hash
  · verify_delta · verify_ci · verify_sig bool · regression bool · gate_status
  · applied bool · source ∈ {diagnosis_guided, blind}

audit_event            -- append-only; the write-ahead record the apply path depends on
  run_id · seq PK · type ∈ {grant, consider, verify, apply, halt, stop, rollback}
  · actor · from_config_hash · to_config_hash · payload_blob_hash · ts
  -- tagged with the P0 set {config_hash, variant_id, run_id, timestamp}

applied_change
  run_id · from_config_hash → to_config_hash · reversible bool · reverted_by_seq NULL
```

Candidate Variant Specs, verification result blobs, rendered diffs, and before/after specs live in
the object store, content-hashed; `audit_event` and `applied_change` hold hashes only. Every applied
spec is content-addressed, so "what is live now" and "what was live at iteration k" are exact — the
substrate rollback (`Rollback` reconstructs `from_config_hash`) and audit replay depend on it.

## Interfaces

- `Grant(constraints) → Authority` — records the grant event; arms the run iff kill switch + audit +
  rollback present.
- `Search.NextCandidates(diagnosis, current_spec, policy) → []VariantSpec` — diagnosis-guided first;
  blind expansion only after, from a separate sub-budget.
- `Verify(candidate, held_out, seeds) → {delta±ci, sig, regression, gate_status}` — the P5.5 gate.
- `Apply(candidate) → AppliedChange` — precondition: `apply_enabled`; **write-ahead** audit before
  the spec swap.
- `Rollback(applied_change | to_config_hash) → VariantSpec` — reconstruct prior spec from the audit
  trail; audited.
- `Halt(reason)` — disarm apply, record reason (regression | budget). `Stop()` — terminal, last-good
  spec live, audited.
- `IngestProductionFailure(trace) → EvalCase` — re-enters at P4; coverage re-measured.

## Risks

- **Audit store becomes a throughput bottleneck** (it's on every apply path). Mitigation: audit
  writes are small append-only rows; the apply cadence is bounded by verification latency (many runs
  per apply), so audit is never the hot path.
- **Diagnosis is stale after an apply** — an earlier attribution may no longer hold once a node is
  changed. Mitigation: re-attribute after each apply; apply serially, not in a batch (design Q3).
- **A weak-labeled production-failure case drives an apply.** Mitigation: intake cases are weak-
  labeled (P4); a weak case can widen coverage and *block* via regression but cannot be the sole basis
  for an apply (design Q5).
- **Reversibility boundary.** The loop reverts the *Variant Spec*; a downstream production side effect
  a change caused is outside the loop's apply scope and is stated as not-un-happen-able, per the
  DevOps "say it isn't reversible" directive.

## Open questions

Carried to PRD §14: Q1 blind-expansion trigger + sub-budget; Q2 min-improvement semantics
(marginal vs. cumulative, composite CI-lower-bound); Q3 multi-node fix interaction (serial + re-
attribute); Q4 re-arm authority; Q5 production-failure case trust; Q6 concurrent-run lock semantics;
Q7 rollback depth (revert-to-any-prior-`config_hash`).
