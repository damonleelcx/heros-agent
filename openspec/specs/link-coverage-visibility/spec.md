# Link Coverage Visibility — Spec (folded from P29)

Product rationale: [`../../../docs/prd/P29-linked-run-fanout.md`](../../../docs/prd/P29-linked-run-fanout.md)
§6 (FR56–FR62). Design reasoning: [`../../changes/archive/2026-08-07-p29-linked-run-fanout/design.md`](../../changes/archive/2026-08-07-p29-linked-run-fanout/design.md) D8.

Metering counts only what it observed, and it has always reported how complete that observation is. The
defect is that the report was only readable *inside* a billing view that requires a billing account — so
an organization without one could link runs and see nothing, including the one number the link certainly
produced.

## Requirements

### Requirement: An organization SHALL hold a billing account from its first authenticated act

An organization SHALL be given an account on the plan that charges nothing at its first authenticated
act, if it does not already hold one.

#### Scenario: A first link provisions an account

- **WHEN** an organization with no account links a run
- **THEN** an account exists for it afterwards
- **AND** it is on a plan that charges nothing.

#### Scenario: An existing account is never corrected

- **WHEN** an organization already holds an account
- **THEN** provisioning leaves its plan, its provider handle and its consent state unchanged
- **AND** repeated authenticated acts do not alter it.

#### Scenario: No plan catalogue is a stated condition, not a silent skip

- **WHEN** the deployment publishes no plan catalogue
- **THEN** provisioning does not occur
- **AND** the surface states that condition and names what would change it.

### Requirement: Link coverage SHALL be readable independently of a billing account

Link coverage SHALL be exposed as its own read model, answerable for an organization that holds no
account, no plan and no invoice.

#### Scenario: Coverage answers without an account

- **WHEN** an organization with no account requests its link coverage
- **THEN** the number of runs it linked and the number it reported observing are returned.

#### Scenario: Coverage does not depend on a charging plan

- **WHEN** an organization is on a plan that charges nothing
- **THEN** its coverage is answered exactly as for any other plan.

### Requirement: Link coverage SHALL be three-state, and unknown SHALL never render as zero or complete

Coverage SHALL be complete, partial with its denominator, or unknown. Unknown SHALL be rendered
distinctly from the other two.

#### Scenario: A read failure is unknown, not zero

- **WHEN** the coverage read fails
- **THEN** the result is unknown
- **AND** it is not zero and not complete.

#### Scenario: Partial coverage shows its denominator

- **WHEN** coverage is partial
- **THEN** both the observed count and the reported count are shown.

#### Scenario: Unknown is visually distinct

- **WHEN** coverage is unknown
- **THEN** it renders differently from complete coverage
- **AND** a reader cannot mistake one for the other.

### Requirement: Every derived spend figure SHALL display its link coverage

Wherever a figure derived from linked runs is shown, the coverage of the runs it derives from SHALL be
shown beside it.

#### Scenario: Spend carries its coverage

- **WHEN** spend under management is displayed
- **THEN** the link coverage it derives from is displayed with it.

#### Scenario: Unlinked spend is never inferred

- **WHEN** an organization's coverage is partial
- **THEN** the platform reports only the spend it observed
- **AND** it does not extrapolate to the unobserved runs.

### Requirement: A linked run SHALL be visible in the metering read model in the period it reports

A linked run's cost, latency and token events SHALL land in the period its own timestamp names.

#### Scenario: A run is countable immediately

- **WHEN** a run is linked into an open period
- **THEN** the metering read model for that period includes it.

#### Scenario: A closed period is refused distinctly

- **WHEN** a run reports a timestamp inside a closed period
- **THEN** the link is refused with a cause naming the closed period
- **AND** the refusal is distinguishable from a duplicate link and from a rejected payload.

### Requirement: The metering surface SHALL distinguish no data from no account from not served

The billing and metering surface SHALL render three different outcomes for three different causes.

#### Scenario: Three causes, three messages

- **WHEN** the surface is opened by an organization that has linked nothing
- **THEN** it says so
- **AND** it differs from what it says when the organization holds no account
- **AND** both differ from what it says when the capability is not served in this deployment.
