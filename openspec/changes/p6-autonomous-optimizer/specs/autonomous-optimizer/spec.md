# Autonomous Optimizer — Spec Delta (P6)

Product rationale: [`../../../../docs/prd/P6-autonomous-optimizer.md`](../../../../docs/prd/P6-autonomous-optimizer.md) §6 (FR1–FR16).

Covers the closed analyze → propose → verify → apply loop: a diagnosis-guided search whose objective
is the P4 composite score and whose hard constraints are the P4 gates; the enumerated hard-constraint
gates that bound the run; kill switch + audit trail + rollback as mandatory prerequisites before any
apply; regression and budget halts; stall/stop discipline; the feedback loop that re-seeds the eval
set from production failures; and the Autonomous-level authority-grant, live-monitor, and stop UX.

## ADDED Requirements

### Requirement: The optimizer SHALL maximize the composite score as its objective and treat the gates as hard constraints

The autonomous optimizer SHALL use the P4 composite score (under the active weight profile) as the
objective function its search maximizes, and SHALL treat the P4 gates (budget ceiling, provider
allowlist, min quality, latency SLA) as hard constraints. A candidate Variant Spec that fails any
gate SHALL NOT be applied, regardless of how high its composite score is.

#### Scenario: A higher-scoring but gate-failing candidate is never applied

- **WHEN** the search evaluates a candidate whose composite score exceeds the current best but which
  violates a P4 gate (e.g. it exceeds the provider allowlist or the latency SLA)
- **THEN** the optimizer does not apply that candidate
- **AND** it prefers a lower-scoring candidate that passes every gate over the gate-failing one

#### Scenario: The objective is the composite score, not a bespoke reward

- **WHEN** the optimizer ranks two gate-passing candidates
- **THEN** it selects the one with the higher P4 composite score under the active weight profile
- **AND** it uses no success signal other than the composite score

### Requirement: The search SHALL be diagnosis-guided before any blind search

The search SHALL enumerate candidate changes at the node and dimension identified by the P4.5
attribution — via the P5.5 change-operator catalog — before expanding to any blind grid/Bayesian
search over the wider model×prompt×context space. Every candidate the search evaluates SHALL record
the motivating diagnosis that produced it.

#### Scenario: Attributed-node candidates are evaluated before blind candidates

- **WHEN** the P4.5 attribution localizes a failure to node 3's model dimension and the loop begins
  a run
- **THEN** the first candidates the search evaluates are changes at node 3's model dimension
- **AND** no blind grid/Bayesian candidate over the full configuration space is evaluated until the
  diagnosis-guided candidates are exhausted and budget remains

#### Scenario: Every candidate records its motivating diagnosis

- **WHEN** the search evaluates any candidate
- **THEN** the candidate's record references the `diagnosis_id` that motivated it
- **AND** an auditor can reconstruct which diagnosis each candidate was intended to address

### Requirement: The optimizer SHALL apply a candidate only on a held-out verified gain

The optimizer SHALL apply a candidate only after the P5.5 held-out verification (multi-seed,
mean + confidence interval, significance test versus the current best, and regression check) confirms
a statistically real improvement on a held-out slice. Diagnosis proposes; verification decides — even
with no human in the seat. A candidate SHALL NOT be applied on an unverified delta or on the cases
that generated it.

#### Scenario: An unverified candidate is not applied

- **WHEN** the search produces a candidate whose measured gain over the held-out slice is within the
  confidence interval of the current best (not significant)
- **THEN** the optimizer does not apply it
- **AND** it records the verification verdict (delta, CI, non-significant) in the audit trail

#### Scenario: Verification runs on held-out cases, not the generating cases

- **WHEN** a candidate is verified before apply
- **THEN** the verification is computed over a held-out slice distinct from the cases that produced
  the proposal
- **AND** a candidate that improves only the generating cases but not the held-out slice is not
  applied

### Requirement: The loop SHALL enforce budget ceiling, provider allowlist, min-improvement threshold, and max iterations as hard constraints

The loop SHALL enforce, as hard constraints for the whole optimization run, a budget ceiling
(cumulative spend cap), a provider allowlist, a min-improvement threshold, and a max-iterations bound.
These SHALL be set at authority-grant time and SHALL be immutable for the duration of the run unless
the run is stopped and re-granted.

#### Scenario: Constraints are fixed at grant time for the run

- **WHEN** a user grants Autonomous authority with a budget ceiling, a provider allowlist, a
  min-improvement threshold, and a max-iterations bound
- **THEN** those four constraints govern the entire run
- **AND** they cannot be changed mid-run without stopping and re-granting the authority

### Requirement: A verified gain below the min-improvement threshold SHALL stop further iterations

When the verified improvement a further iteration would deliver falls below the min-improvement
threshold, the loop SHALL stop iterating and declare convergence rather than continue searching.

#### Scenario: A sub-threshold gain stops the loop

- **WHEN** the best remaining candidate's held-out verified gain over the current best is below the
  min-improvement threshold
- **THEN** the loop stops iterating with state `converged`
- **AND** it does not run further iterations chasing a smaller gain

### Requirement: Reaching max iterations SHALL stop the loop

When the run reaches its max-iterations bound, the loop SHALL stop even if unevaluated candidates
remain.

#### Scenario: The loop stops at the iteration bound

- **WHEN** the run has completed its configured maximum number of iterations
- **THEN** the loop stops with state `max_iter`
- **AND** no further candidate is evaluated or applied

### Requirement: The loop SHALL apply no change unless a kill switch, an audit trail, and a rollback capability are all armed

The loop SHALL NOT apply any change unless a kill switch, an audit trail, and a rollback capability
are all present and armed for the run. If any one is absent, the apply step SHALL be disabled and the
loop MAY run only in a propose/verify dry-run mode that applies nothing.

#### Scenario: A missing prerequisite disables apply entirely

- **WHEN** the loop is started for a run in which the rollback capability is not armed (or the audit
  trail, or the kill switch is absent)
- **THEN** the apply step is disabled
- **AND** the loop runs only in propose/verify dry-run mode and applies zero changes
- **AND** no Variant Spec swap occurs for the run

#### Scenario: Apply proceeds only with all three prerequisites armed

- **WHEN** the kill switch, audit trail, and rollback are all armed and a verified, gate-passing
  candidate is selected
- **THEN** the loop is permitted to apply the candidate
- **AND** the apply is recorded in the audit trail before the Variant Spec is swapped

### Requirement: The audit trail SHALL record every loop decision as an append-only, attributable record

The audit trail SHALL record every decision the loop makes — authority grant, candidate considered
(with its motivating diagnosis), verification verdict, gate evaluation, apply, halt, stop, and
rollback — as an append-only record keyed by the P0 tag set (`config_hash`, `variant_id`, `run_id`,
timestamp), sufficient to reconstruct what changed, why, and with what measured effect. The apply
SHALL be write-ahead-audited: the audit event for an apply SHALL be committed before the Variant Spec
is swapped, so no applied change can escape the trail.

#### Scenario: An apply is written to the audit trail before it takes effect

- **WHEN** the loop applies a candidate
- **THEN** the audit event for that apply is committed before the live Variant Spec is swapped
- **AND** the record includes the from/to `config_hash`, the motivating diagnosis, and the verified
  delta

#### Scenario: The audit store being unavailable prevents the apply

- **WHEN** the audit store is unavailable at the moment an apply would occur
- **THEN** the apply does not proceed (it fails closed)
- **AND** the last-good Variant Spec remains live and no unaudited change is applied

### Requirement: Any applied change SHALL be reversible to the exact prior Variant Spec via the audit trail

Any change the loop applies SHALL be reversible to the exact prior Variant Spec using the audit trail,
and the rollback SHALL itself be recorded in the audit trail.

#### Scenario: An applied change is rolled back to the byte-identical prior spec

- **WHEN** a user (or an operator) rolls back a change the loop applied
- **THEN** the live Variant Spec is restored to the exact prior spec, matching the prior `config_hash`
- **AND** the rollback is recorded in the audit trail as its own event

### Requirement: Regression detection and budget alerts SHALL halt the loop and disarm apply

The loop SHALL halt automatically when regression detection finds any tracked metric degraded beyond
its configured threshold versus the current best, or when a budget alert fires because cumulative run
spend breaches the budget ceiling. A halt SHALL disarm the apply step until a human explicitly re-arms
it; no candidate SHALL be applied after a halt until re-armed.

#### Scenario: A budget breach halts the loop mid-run

- **WHEN** the cumulative provider spend of the run breaches the budget ceiling during a search
- **THEN** the loop halts with state `halted_budget`
- **AND** no further candidate is applied
- **AND** the apply step is disarmed until a human re-arms it
- **AND** the halt is recorded in the audit trail

#### Scenario: A regression halts the loop and disarms apply

- **WHEN** regression detection finds a tracked metric degraded beyond its threshold versus the
  current best after an iteration
- **THEN** the loop halts with state `halted_regression`
- **AND** the apply step is disarmed until a human explicitly re-arms it

### Requirement: The kill switch SHALL stop the loop immediately with no further apply

A user or an automated halt SHALL be able to stop the loop on demand via the kill switch. After the
kill switch fires, no further candidate SHALL be applied, the in-flight iteration SHALL finish or be
abandoned leaving the last-good Variant Spec live, and the stop SHALL be recorded in the audit trail.

#### Scenario: Firing the kill switch mid-iteration applies nothing further

- **WHEN** a user fires the kill switch while an iteration is verifying a candidate
- **THEN** no candidate is applied after the stop
- **AND** the last-good Variant Spec remains live
- **AND** any in-flight verification result is discarded rather than applied
- **AND** the stop is recorded in the audit trail

### Requirement: The loop SHALL detect stall/no-progress and stop rather than wander

The loop SHALL detect a stall — K consecutive iterations producing no gate-passing,
verification-passing improvement — and stop, rather than continue consuming budget on a search that
is not converging.

#### Scenario: A non-improving search stops instead of looping forever

- **WHEN** K consecutive iterations produce no candidate that both passes every gate and shows a
  significant held-out gain
- **THEN** the loop stops with state `stalled`
- **AND** it does not continue iterating indefinitely

### Requirement: A production failure SHALL be ingestible as a new eval case that re-enters at P4

A failure observed in production after an applied change SHALL be ingestible as a new eval case that
re-enters at P4 — added to the eval set with coverage re-measured — so that the next optimization run
is measured against it. The eval set is the living memory of the system.

#### Scenario: A production failure becomes an eval case measured by the next run

- **WHEN** a production-failure trace is ingested after a change was applied
- **THEN** a new eval case is added to the P4 eval set and coverage is re-measured
- **AND** the next optimization run scores candidates against that new case

### Requirement: Autonomous SHALL be a distinct automation level with an explicit, recorded authority grant

Autonomous SHALL be a distinct automation level (above Advisory and Assisted) in which the user
explicitly grants the loop authority to apply changes, setting the hard constraints (budget ceiling,
provider allowlist, min-improvement threshold, max iterations) at grant time. The grant SHALL be
recorded in the audit trail.

#### Scenario: Granting Autonomous authority records the constraints

- **WHEN** a user grants Autonomous authority and sets the budget ceiling, provider allowlist,
  min-improvement threshold, and max iterations
- **THEN** those constraints govern the run and the grant is recorded in the audit trail
- **AND** the user is shown that the loop may apply changes on its own within those limits before the
  grant is confirmed

### Requirement: The Autonomous UI SHALL provide a live monitor and an always-available stop control

While a loop runs, the UI SHALL present a live monitor showing the current iteration, cumulative spend
against the budget ceiling, candidates applied, and the streaming audit trail, together with an
always-available stop control wired to the kill switch, and a rollback control for every applied
change.

#### Scenario: The user monitors a running loop and stops it

- **WHEN** an Autonomous loop is running
- **THEN** the live monitor shows the current iteration, cumulative spend against the ceiling, the
  changes applied so far, and the streaming audit trail
- **AND** a stop control wired to the kill switch is visible and reachable at all times
- **AND** activating it halts the loop immediately with no further apply

#### Scenario: Every applied change exposes a rollback control

- **WHEN** the loop has applied one or more changes
- **THEN** each applied change is shown with its motivating diagnosis, its verified delta, and its
  cost/latency impact
- **AND** each exposes a rollback control that reverts the change via the audit trail
