# Operator Metering Honesty — Spec Delta (P26)

Product rationale: [`../../../../../docs/prd/P26-operator-console-refresh.md`](../../../../../docs/prd/P26-operator-console-refresh.md)
§6 (FR65–FR69). Technical decisions: [`../../design.md`](../../design.md) D5, D9.

Covers three wrong numbers on shipped operator pages. Not missing numbers — **wrong ones**, which is the
worse case, because a missing number prompts a question and a wrong number gets acted on.

The project context states the rule once, plainly: metering counts only what it observed, SUM derives from
*linked* runs, the platform never infers or extrapolates unlinked spend, and **link coverage is displayed
wherever a derived figure is shown.** The customer console honours it. The operator billing surface, which
predates run linking, does not — verified: no coverage field exists in the operator billing read model or
its page.

> The asymmetry that makes this urgent: a billing operator issuing a credit against a SUM figure with 31%
> link coverage is acting on a number that is wrong by an unknown factor, in a direction nobody can
> quantify. The correction is not a footnote — a footnote beside a figure reads as reassurance. The
> correction is **the coverage beside the figure, changing the decision.**

## ADDED Requirements

### Requirement: Link coverage SHALL be displayed beside every SUM-derived figure on the operator console

The coverage SHALL appear in the same view as the figure — not behind a link, not in a footnote, not on a
detail page.

#### Scenario: Coverage changes a decision
- **WHEN** an operator opens a SUM-derived figure with partial link coverage
- **THEN** the coverage percentage is shown beside the figure in the same view
- **AND** an operator about to act on the figure sees the coverage without navigating.

#### Scenario: Every derived figure carries it
- **WHEN** every operator surface showing a SUM-derived figure is audited
- **THEN** each such figure displays its link coverage
- **AND** the assertion establishing this fails if a figure is added without one.

### Requirement: A figure whose coverage is unknown SHALL NOT be rendered

Unknown coverage SHALL render as unknown, and the figure it would have qualified SHALL NOT be displayed.
The platform SHALL NOT infer or extrapolate unlinked spend.

#### Scenario: An unqualifiable figure is withheld
- **WHEN** link coverage for a period cannot be determined
- **THEN** the surface states that coverage is unknown
- **AND** no SUM-derived figure for that period is displayed.

#### Scenario: No extrapolation fills the gap
- **WHEN** some runs in a period are unlinked
- **THEN** the figure reflects only linked runs
- **AND** no estimate, projection or scaling factor is applied to account for the unlinked ones.

#### Scenario: The pairing is structural
- **WHEN** a derived figure's read model is constructed
- **THEN** the figure and its coverage travel together, such that a figure cannot be rendered without a
  coverage value
- **AND** an absent coverage value results in the figure being withheld rather than shown bare.

### Requirement: Every aggregate improvement, savings or quality figure SHALL exclude unverified authored changes, and SHALL state that it does

The exclusion SHALL be enforced at the query and SHALL be asserted by test. The surface SHALL state that
unverified authored changes are excluded.

#### Scenario: A seeded unverified change contributes zero
- **WHEN** an authored change is created and left unverified, and every aggregate improvement, savings and
  quality figure is then read
- **THEN** the change contributes exactly zero to each of them
- **AND** the assertion is made against the figures, not against the presence of a filter in the query.

#### Scenario: The exclusion is stated where the figure appears
- **WHEN** an aggregate improvement or savings figure is rendered
- **THEN** the surface states that unverified authored changes are excluded from it.

#### Scenario: A refactor cannot silently drop the state
- **WHEN** the exclusion is removed
- **THEN** the seeded-change assertion fails, naming the requirement it defends.

### Requirement: A gainshare or verified-savings figure SHALL name the verified-delta ledger it drew on

Such figures SHALL draw exclusively on the verified-delta ledger, and the surface SHALL name that
provenance where the figure appears.

#### Scenario: Provenance is on the surface, not in a document
- **WHEN** a gainshare or verified-savings figure is rendered
- **THEN** the surface names the verified-delta ledger as its source
- **AND** an operator can tell it apart from an unverified estimate without leaving the page.

#### Scenario: No unverified saving reaches a billable figure
- **WHEN** an unverified saving exists
- **THEN** it appears in no gainshare or verified-savings figure
- **AND** it is not billable.

### Requirement: Plans SHALL be named and prices SHALL remain references, never values

The operator console SHALL name plans and SHALL contain no price value, percentage, or other business
number committed to the repository. The existing build fence on priced literals SHALL continue to hold.

#### Scenario: No priced literal ships
- **WHEN** the operator console is built
- **THEN** the build fails on any priced literal in the shipped client bundle
- **AND** this phase introduces none.

#### Scenario: Plans are referenced by name
- **WHEN** a plan is rendered
- **THEN** it is identified by name
- **AND** its price is resolved from configuration rather than from a committed value.

### Requirement: Every aggregate SHALL offer the drill-down to its samples

A fleet-level or tenant-level aggregate SHALL be traceable to the records behind it.

#### Scenario: An aggregate does not hide a single-sample defect
- **WHEN** an operator examines an aggregate figure
- **THEN** the individual records behind it are reachable
- **AND** an aggregate with no path to its samples is treated as incomplete.
