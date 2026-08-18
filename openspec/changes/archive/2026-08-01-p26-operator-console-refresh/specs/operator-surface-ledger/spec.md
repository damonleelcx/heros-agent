# Operator Surface Ledger — Spec Delta (P26)

Product rationale: [`../../../../../docs/prd/P26-operator-console-refresh.md`](../../../../../docs/prd/P26-operator-console-refresh.md)
§6 (FR42–FR47), §8.2 D1. Technical decisions: [`../../design.md`](../../design.md) D1, D7, D8.

Covers this phase's product, as distinct from its output. The four new pages close today's gaps; this
capability is what stops the next fourteen phases reopening them.

> The asymmetry: **drift became a build failure.** Fourteen phases of operator-console drift happened
> without anything failing, because the operator console was nobody's acceptance criterion. A refresh that
> closes nine gaps and leaves that property intact buys eighteen months. A fence buys the property.
>
> And the second asymmetry, which is what keeps the fence honest: **a gap with a named cause is not a
> gap.** A read that is wanted but not derivable is recorded with the collection that would make it
> readable — the typed-refusal shape applied to oversight — so it becomes a specified task rather than a
> vague wish or a table built to hold guesses.

## ADDED Requirements

### Requirement: Every built capability SHALL resolve in the ledger to exactly one of three states

A checked-in ledger SHALL carry a row for every capability in the specs directory. Each row SHALL resolve
to `surface` (naming the operator destination that exercises it), `no-operator-surface` (carrying a reason
and the deciding phase), or `not-yet-readable` (naming the collection that would make the read possible).
There SHALL be no fourth state and no unresolved capability.

#### Scenario: Every capability is accounted for
- **WHEN** the ledger is read against the specs directory
- **THEN** every capability appears in exactly one row
- **AND** that row resolves to one of the three states.

#### Scenario: A deliberate absence is recorded and attributable
- **WHEN** a capability needs no operator surface
- **THEN** the row carries a reason and the phase that decided it
- **AND** the decision is reviewable rather than indistinguishable from an oversight.

### Requirement: A capability with no ledger row SHALL fail the build

The fence SHALL fail when a capability exists in the specs directory and appears in no row, naming the
capability. Operator-oversight drift SHALL be a build failure, not a review finding.

#### Scenario: Adding a capability without deciding its operator story fails
- **WHEN** a new capability is added to the specs directory with no ledger row
- **THEN** the build fails, naming the capability
- **AND** the failure states that a surface, a reasoned absence, or a named missing collection is required.

#### Scenario: The fence has been demonstrated red
- **WHEN** the fence is validated
- **THEN** a deliberately unresolved capability produces a build failure
- **AND** the fence is not accepted on the basis of a passing run alone.

### Requirement: The ledger SHALL be asserted against the surface registry in both directions

Every row naming a surface SHALL resolve to a destination present in the single surface registry, and
every destination in that registry SHALL be named by at least one row.

#### Scenario: A row cannot name a surface that does not exist
- **WHEN** a row names a destination absent from the surface registry
- **THEN** the assertion fails, naming the row and the destination.

#### Scenario: A surface cannot exist unjustified
- **WHEN** a destination is present in the surface registry and named by no row
- **THEN** the assertion fails, naming the destination
- **AND** a surface nobody can justify is treated as a defect rather than as extra coverage.

### Requirement: A not-yet-readable row SHALL name the collection that would make the read possible

A row in the `not-yet-readable` state SHALL name the missing collection, signal or store. It SHALL NOT be
satisfied by an empty detail, an estimate, an extrapolation, or a surface that renders an empty state as
though it were a zero.

#### Scenario: An unnamed missing input fails
- **WHEN** a `not-yet-readable` row carries no named collection
- **THEN** the assertion fails, naming the row.

#### Scenario: The gap is actionable by the next phase
- **WHEN** a later phase reads a `not-yet-readable` row
- **THEN** it finds a specified missing input rather than a wish
- **AND** implementing that input is sufficient to move the row to `surface`.

#### Scenario: An absent read is never filled with an estimate
- **WHEN** a wanted read is not derivable from an existing store
- **THEN no** figure is rendered for it
- **AND** the surface states the value is unknown rather than inferring one.

### Requirement: A new operator surface SHALL require a granted capability, and new capabilities SHALL partition rather than widen

Every operator surface SHALL be reachable only with a granted capability. A capability added for a new
surface SHALL partition existing responsibility rather than widening an existing role, and the
deny-by-default permission matrix test SHALL continue to iterate every capability.

#### Scenario: No surface is reachable without a capability
- **WHEN** an operator whose role grants no relevant capability signs in
- **THEN** the surface is absent from the navigation and absent from the command palette
- **AND** it is never offered and then refused.

#### Scenario: A capability added without a considered grant fails a test
- **WHEN** a capability is added to the capability set without a grant decision per role
- **THEN** the permission matrix test fails.

#### Scenario: No existing role widens
- **WHEN** the roles are compared before and after this change
- **THEN** no existing role holds a capability it did not hold before
- **AND** any new capability is held only where it was deliberately granted.

### Requirement: The surface registry SHALL remain the single map read by the navigation, the palette and the ledger

There SHALL be exactly one registry of operator destinations. The navigation, the command palette and the
ledger SHALL read it, so no two can disagree about what is reachable.

#### Scenario: Nav and palette cannot disagree
- **WHEN** a destination is added to the registry and granted
- **THEN** it appears in both the navigation (if marked as such) and the palette
- **AND** neither can present a destination the other lacks.

#### Scenario: A page added without its registry entry is caught
- **WHEN** a route is added with no registry entry
- **THEN** the ledger's reverse assertion or the fence fails
- **AND** the page does not ship as a destination reachable from one surface and missing from another.

### Requirement: The ledger SHALL cover the operator console only, and SHALL state that boundary

The ledger's scope SHALL be operator oversight. It SHALL NOT be read as a statement about
customer-console coverage, and its scope SHALL be stated in the ledger itself.

#### Scenario: The scope is explicit
- **WHEN** the ledger is read
- **THEN** it states that it governs operator surfaces
- **AND** a reader cannot mistake a `no-operator-surface` row for a claim that no surface of any kind
  exists for that capability.
