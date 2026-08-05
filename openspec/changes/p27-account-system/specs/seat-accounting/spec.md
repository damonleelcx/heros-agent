# Seat Accounting — two quantities, two names, one of which is finally measured

Product rationale: [`docs/prd/P27-account-system.md`](../../../../../docs/prd/P27-account-system.md) §6
(FR20–FR23). Design: [`design.md`](../../design.md) §3.3, §5.3, §5.4.

`plancfg.LimitSeats` and `metering.MetricSeats` both exist;
[`entitlement.go:109`](../../../../../internal/entitlement/entitlement.go) gates the dashboard on the pair; the plan
fixtures price 1 / 5 / 25 / 500 seats. **No code path anywhere writes a `seats` usage record.** The gate
therefore compares a plan allowance against zero, forever, and passes. A plan that sells five seats admits five
hundred, and the invoice line for seats is derived from nothing.

The root cause is a category error, and the fix is to stop making it: **a seat count is a state, and it was
being accumulated as a flow.** A state nobody writes is a state that reads as zero. This capability separates
the two questions that were collapsed into one word.

## ADDED Requirements

### Requirement: The platform SHALL derive the current seat count from membership, not from the usage store

`seats_current` is the number of active memberships whose role can open the console. It is read directly.

#### Scenario: The count is membership, read directly
- **WHEN** the current seat count is required
- **THEN** it is computed from active memberships for that tenant
- **AND** the usage store is not consulted for it

#### Scenario: Reading it from the usage store is a test failure
- **WHEN** an implementation resolves the current seat count through a metering usage record
- **THEN** a unit test fails
- **AND** that test exists specifically because this substitution is what made the limit decorative

#### Scenario: The count moves when membership moves
- **WHEN** a membership is activated or removed
- **THEN** the current seat count reflects it immediately, with no projection lag and no scheduled job

### Requirement: The platform SHALL refuse a membership beyond the plan's seat allowance, naming both numbers

#### Scenario: The invitation past the allowance is refused
- **WHEN** a plan allows five seats, five memberships are active, and a sixth invitation is issued
- **THEN** the invitation is refused
- **AND** the refusal names the plan's allowance and the current count, both

#### Scenario: Pending invitations count when inviting
- **WHEN** the allowance is evaluated at invitation time
- **THEN** unexpired, unaccepted invitations count toward it
- **AND** the person who can fix the problem — the inviter — is the person who sees the error

#### Scenario: The allowance is re-checked at acceptance
- **WHEN** an invitation is accepted
- **THEN** the allowance is evaluated again against the state at that moment
- **AND** an acceptance beyond the allowance is refused with the same named error code

#### Scenario: A downgrade below the current count is refused
- **WHEN** a plan change would set an allowance below the current seat count
- **THEN** it is refused, naming both numbers, with removing members as the stated remedy

#### Scenario: An operator override replaces the plan allowance for that limit only
- **WHEN** an operator has set a seat quota override for a tenant
- **THEN** the override replaces the plan's seat allowance
- **AND** every other limit continues to resolve from plan configuration, unchanged from P7

### Requirement: The platform SHALL record a seat observation on every membership change and SHALL bill the period peak

`seats_billed` is the highest seat count held during the billing period.

#### Scenario: Every change is observed
- **WHEN** a membership is activated or removed
- **THEN** a seat usage observation for the current period is written in the same commit as the membership
  change

#### Scenario: The invoice cites the peak, not the closing count
- **WHEN** a tenant holds five seats for three weeks of a period and removes two on the last day
- **THEN** the period's billed seat quantity is five
- **AND** it is not three

#### Scenario: The peak is reconstructable from the observations
- **WHEN** the period's seat quantity is derived
- **THEN** it is derived at a named, idempotent reconciliation point over the recorded observations
- **AND** re-running that derivation over the same observations produces the same quantity

### Requirement: No surface SHALL render a seat figure without naming which quantity it is

#### Scenario: Every rendered seat number is labelled
- **WHEN** any console, operator or API surface presents a seat figure
- **THEN** it states whether the figure is the current count or the period's billed quantity
- **AND** a surface presenting an unlabelled "seats" figure is non-conformant

#### Scenario: The browser does not compute it
- **WHEN** a seat figure is shown in a console
- **THEN** it is rendered as received from the platform
- **AND** it is not derived, summed or adjusted client-side

### Requirement: The seat definition SHALL be settled before any seat quantity is quoted

Whether a member who holds a user-scoped credential but never opens the console occupies a seat is **undecided**
(PRD Open Question 3). The two available answers point in opposite commercial directions.

#### Scenario: An unsettled definition blocks the claim, not the code
- **WHEN** the seat definition has not been ratified
- **THEN** no customer-facing surface, price sheet or sales conversation states what a seat includes
- **AND** the enforcement mechanism may still ship, because it enforces whatever the ratified definition turns
  out to be
