# Verification — Spec Delta (P5.5)

Product rationale: [`../../../../docs/prd/P5.5-proposals-verification.md`](../../../../docs/prd/P5.5-proposals-verification.md) §6 (FR6–FR13).

Covers held-out auto-execution of the **transformed working copy** (the code the codemod would ship,
per ADR-001), the statistical significance gate, the regression check over other clusters and the
cost/latency budget, the verdict contents, the nothing-unverified-surfaces guarantee, the ranked
recommendation list + trend view, and the Advisory/Assisted apply expressed as opening a pull request
(draft for Advisory; one-click verified PR for Assisted; the human merges).

## ADDED Requirements

### Requirement: The gate SHALL auto-execute the transformed working copy on a held-out split where available

The verification gate SHALL **auto-execute the transformed working copy** — the candidate's source
diff applied to an isolated worktree/branch and built, i.e. the code that would actually ship (ADR-001)
— against the eval dataset through the P4 eval harness, on a **held-out split** (the cases the
proposal was *not* generated from) whenever such a split exists, so the measured gain is not overfit
to the generating cases. The surfaced delta SHALL be the held-out delta. When no held-out split
exists, the verdict SHALL be flagged **not held-out**.

#### Scenario: Verification runs the transformed code, not a shimmed run

- **WHEN** a candidate whose diff has built is verified
- **THEN** the harness executes the transformed working copy (the built codemod output) so the
  measured cost, latency, and quality reflect the code that would ship
- **AND** no runtime shim substitutes parameters at execution time

#### Scenario: An overfit proposal that wins only on its generating cases does not pass

- **WHEN** a prompt-rewrite proposal shows a large gain on the failing cases it was generated from but
  a delta whose CI overlaps the baseline on the held-out split
- **THEN** verification surfaces the **held-out** delta, not the generating-case delta
- **AND** because the held-out delta is a tie, the proposal does not pass the gate
- **AND** the proposal is not presented to the user as a recommendation

#### Scenario: Absence of a held-out split is disclosed

- **WHEN** a proposal is verified against an eval set for which no held-out split can be formed
- **THEN** the verdict is flagged **not held-out**
- **AND** the proposal is still subject to the significance and regression gates

### Requirement: The significance gate SHALL admit only statistically-real gains using the P4 primitive

Verification SHALL run **multi-seed** and admit a proposal only when its improvement over the baseline
variant is **statistically significant**, computed with the same P4 `Stats.Compare` primitive (mean +
confidence interval + significance test). An improvement whose confidence interval overlaps the
baseline's SHALL be treated as a **tie** and SHALL NOT pass the gate.

#### Scenario: A noise proposal is a tie and does not pass

- **WHEN** a proposal whose true delta is zero is verified over the configured seeds
- **THEN** its confidence interval overlaps the baseline's
- **AND** `Stats.Compare` returns a tie
- **AND** the proposal does not pass the significance gate and is not surfaced

#### Scenario: A real, significant gain passes the significance gate

- **WHEN** a proposal with a large, real quality gain is verified over the configured seeds
- **THEN** its confidence interval does not overlap the baseline's and the significance test fires
- **AND** the proposal passes the significance gate

### Requirement: The regression check SHALL catch degraded clusters and budget breaches as hard failures

Verification SHALL run a **regression check** that confirms the proposal did **not degrade any other
failure cluster** beyond a configured threshold and did **not breach the cost or latency budget**,
with the cost/latency budget enforced as a **hard gate**. A proposal that fixes its target cluster
but breaks another cluster, or that improves quality while breaching the cost or latency budget, SHALL
**fail** the regression check.

#### Scenario: Fixed accuracy, tripled cost fails the regression check

- **WHEN** a proposal raises task-success on its target cluster with a statistically significant gain
  but triples the cost per run, breaching the cost budget
- **THEN** the regression check fails on the cost budget
- **AND** the proposal's `gate_result` is `fail_regression`
- **AND** the verdict records the cost impact
- **AND** the proposal is not presented to the user as a recommendation

#### Scenario: Fixing one cluster while breaking another fails

- **WHEN** a proposal improves failure cluster A but degrades failure cluster B beyond the configured
  threshold
- **THEN** the regression check fails
- **AND** the verdict lists the **cases broken** in cluster B alongside the **cases fixed** in cluster A

### Requirement: Verification SHALL emit a verdict reporting the delta, cost/latency impact, and cases fixed and broken

Every verified proposal SHALL carry a **verdict** containing: the proposed change (the diff), the
**measured delta with confidence interval**, the **cost impact**, the **latency impact**, the list of
**cases fixed**, and the list of **cases broken**.

#### Scenario: A verdict reports both cases fixed and cases broken

- **WHEN** a proposal completes verification
- **THEN** its verdict contains the proposed diff, the measured delta with a confidence interval, the
  cost impact, and the latency impact
- **AND** the verdict lists the specific **cases fixed**
- **AND** the verdict lists the specific **cases broken** (empty if none)

### Requirement: Nothing unverified SHALL be presented to the user as a recommendation

A proposal that has not passed the verification gate — or that failed the significance gate, the
regression check, or a hard constraint — SHALL NOT be presented to the user as a recommendation. The
recommendation surface SHALL read only proposals whose `gate_result` is `pass`.

#### Scenario: A gate-failed proposal is withheld

- **WHEN** the engine generates a proposal that looks favorable on its generating cases but fails the
  held-out / significance / regression gate
- **THEN** the proposal's `gate_result` is not `pass`
- **AND** the proposal does not appear in the ranked recommendation list
- **AND** no apply control is offered for it

#### Scenario: Only gate-passing verdicts reach the recommendation surface

- **WHEN** a batch of proposals is verified and some pass while others fail the gate
- **THEN** the ranked recommendation list contains only the proposals whose `gate_result` is `pass`
- **AND** any human-readable synthesis narrates those gate-passing verdicts and never a withheld one

### Requirement: The system SHALL present a ranked recommendation list and a trend view over structured verdicts

The system SHALL present verified proposals as a **ranked recommendation list** where each entry =
diagnosis + evidence + proposed diff + **verified delta (with CI, cost/latency impact, cases
fixed/broken)**, and a **trend view across variants over time** showing whether the workflow's metrics
actually improved or the failure mass merely relocated across clusters. Any human-readable synthesis
SHALL be **narration over the structured verdict**, which remains the source of truth.

#### Scenario: Each recommendation carries diagnosis, evidence, diff, and verified delta

- **WHEN** the ranked recommendation list is rendered
- **THEN** each entry shows the originating diagnosis, the failing-case evidence, the proposed diff,
  and the verified delta with its confidence interval, cost/latency impact, and cases fixed/broken

#### Scenario: The trend view shows problems moving rather than a false improvement

- **WHEN** across three variant iterations failure cluster A shrinks while failure cluster B grows by
  a comparable amount
- **THEN** the trend view shows the workflow did not globally improve — the failure mass moved between
  clusters — rather than reporting an unqualified "improving"

### Requirement: Assisted one-click PR-open SHALL be offered only for a proposal that passed the verification gate

The system SHALL support **Advisory** (open a **draft pull request** / report the verified diff; the
human applies and merges) and **Assisted** (**one-click open the verified pull request** carrying the
codemod's source diff; the human reviews and merges). In both levels the change SHALL reach the user's
repository only as a **reviewable diff/PR**, SHALL NOT be merged to the default branch by the system,
and SHALL NOT mutate the working tree in place; rollback is `git revert` and the audit trail is git
history. Assisted one-click PR-open SHALL be offered **only** for a proposal whose `gate_result` is
`pass`; an unverified or gate-failing proposal SHALL NOT be one-click-openable as a PR.

#### Scenario: One-click PR-open is available only for a verified proposal

- **WHEN** a proposal has passed the verification gate (`gate_result = pass`) in Assisted mode
- **THEN** a one-click control is offered that opens a pull request carrying the candidate's source
  diff against the user's repository for the human to review and merge
- **AND** for a proposal whose `gate_result` is not `pass`, no one-click PR-open control is offered
- **AND** the system does not merge the pull request or mutate the working tree itself

#### Scenario: Advisory mode reports without opening a mergeable PR

- **WHEN** the workflow's automation level is Advisory and a proposal passes the gate
- **THEN** the verified proposal, its verdict, and its reviewable diff (optionally as a draft PR) are
  reported for the human to apply and merge
- **AND** no automatic merge occurs
