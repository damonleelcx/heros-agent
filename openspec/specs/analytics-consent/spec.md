# Analytics Consent — Spec (folded from P24)

Product rationale: [`../../../docs/prd/P24-analytics-and-error-monitoring.md`](../../../docs/prd/P24-analytics-and-error-monitoring.md)
§6 (FR6–FR13), §9.1 (Product Designer lens). Technical decisions:
[`../../changes/archive/2026-08-01-p24-analytics-error-monitoring/design.md`](../../changes/archive/2026-08-01-p24-analytics-error-monitoring/design.md) D7.

Covers the only new thing this phase asks a human to do, and the storage it must **not** use.

> Two asymmetries carry it. **Default-denied, and a refusal is a stored fact** — a refusal recorded as
> "not yet asked" re-prompts forever, which is the same defect class as asking a user to re-type an
> identifier the system already holds. And **the lifecycle is the opposite of a statutory acceptance** —
> a revocable, per-visitor, often pre-session preference must not be written into a ledger that is
> append-only, keyed to an immutable document hash, and survives identity erasure.

## Requirements

### Requirement: Consent SHALL be recorded per category, from a closed set

The categories SHALL be `essential`, `product_analytics`, `session_replay` and `error_diagnostics`. Each
non-essential category SHALL be independently grantable and independently withdrawable. An accept-all
control MAY be offered as a convenience but SHALL NOT be the only granularity available.

#### Scenario: Accepting usage counting does not enable recording
- **WHEN** a visitor grants `product_analytics` and nothing else
- **THEN** the analytics integration loads
- **AND** no session-replay script loads and no replay data is collected.

#### Scenario: Each category can be withdrawn on its own
- **WHEN** a visitor who granted two categories withdraws one
- **THEN** only that category's collection stops
- **AND** the other remains granted.

### Requirement: Every non-essential category SHALL default to denied

No script SHALL load, no beacon SHALL fire and no non-essential cookie or storage entry SHALL be written
before an explicit grant for that category.

#### Scenario: A visitor is not tracked before they consent to anything
- **WHEN** an anonymous visitor loads a surface carrying consent-gated integrations and takes no action
- **THEN** no analytics, tag manager, session-replay or third-party beacon is loaded
- **AND** no non-essential cookie or storage entry exists
- **AND** no request carries the visitor's address to a party other than the surface's own origin.

#### Scenario: Nothing is pre-granted
- **WHEN** the consent interface is presented
- **THEN** no category is pre-selected as granted
- **AND** no category is granted by dismissing, scrolling past, or navigating away from the interface.

### Requirement: A refusal SHALL be stored as a refusal and SHALL NOT be re-prompted

A denial SHALL be distinguishable from "not yet asked" and SHALL persist across navigations and
sessions until the consent policy version changes materially.

#### Scenario: Declining is remembered
- **WHEN** a visitor declines every category and then navigates to three further pages and returns in a
  new session
- **THEN** the consent interface is not presented again
- **AND** the stored state reads as denied, not as absent.

#### Scenario: Not-asked and denied are different states
- **WHEN** the stored consent state is inspected
- **THEN** `not-asked`, `granted` and `denied` are three distinguishable values per category
- **AND** no code path collapses them to a boolean.

### Requirement: Withdrawal SHALL be reachable from every gated page and effective on the next navigation

Withdrawal SHALL be reachable from every page that carries a consent-gated integration, SHALL take
effect on the next navigation without requiring a sign-out or a session reset, and SHALL stop the
corresponding collection.

#### Scenario: Withdrawal stops collection
- **WHEN** a visitor withdraws a granted category and then navigates
- **THEN** that category's script is not loaded on the new page
- **AND** no further request to that category's origin is made.

#### Scenario: Withdrawal is not buried
- **WHEN** any page carrying a gated integration is rendered
- **THEN** a control to review and change consent is reachable from that page
- **AND** it is reachable by keyboard with visible focus.

### Requirement: Declining SHALL leave every function of the surface intact

No content, control, route or capability SHALL be conditioned on a consent grant, and declining SHALL
carry no functional penalty.

#### Scenario: A declining visitor loses nothing
- **WHEN** a visitor declines every category and exercises every control on the surface
- **THEN** every control behaves identically to the granted case
- **AND** no content is withheld and no route is blocked.

#### Scenario: Decline is not visually disadvantaged
- **WHEN** the consent interface is rendered
- **THEN** the decline control carries the same visual weight as the accept control
- **AND** both resolve to the shared token set.

### Requirement: A consent grant SHALL carry the policy version it was given against

A grant SHALL record the consent policy version. Publishing a version declared material SHALL reset the
affected non-essential categories to `not-asked` and re-ask. Publishing a version declared non-material
SHALL ask nobody.

#### Scenario: A material change re-asks
- **WHEN** a consent policy version declared material is published
- **THEN** the affected categories return to `not-asked` for every visitor
- **AND** collection for those categories stops until a new grant is given.

#### Scenario: A wording fix asks nobody
- **WHEN** a version declared non-material is published
- **THEN** no visitor is re-asked
- **AND** every existing grant remains in force.

### Requirement: Analytics consent SHALL NOT be written into the statutory consent-records ledger

Analytics consent SHALL be stored separately from the P23 acceptance ledger. The ledger holds statutory
acceptances of immutable documents, is append-only, and survives identity erasure; analytics consent is
revocable, per-visitor, frequently pre-session, and SHALL NOT acquire those properties.

#### Scenario: A cookie preference does not outlive a deletion request
- **WHEN** an identity erasure is executed
- **THEN** no analytics-consent decision survives as a statutory record attributable to that subject
- **AND** the statutory acceptance records are unaffected.

#### Scenario: The two stores are distinguishable
- **WHEN** the acceptance ledger is inspected
- **THEN** it contains no analytics-consent category decision
- **AND** an analytics-consent decision cannot be created through the acceptance path.

### Requirement: The operator console SHALL NOT present a consent interface, and the exception SHALL be stated

The operator console is a staff surface whose only integration is error reporting, whose payload carries
no personal data by construction. Its exemption SHALL be stated in the internal acceptable-use notice
rather than inferred from the absence of an interface.

#### Scenario: No banner on the operator surface
- **WHEN** an operator loads any operator-console route
- **THEN** no consent interface is presented
- **AND** no analytics or session-replay integration is loaded on that route.

#### Scenario: The exemption is written down
- **WHEN** the internal acceptable-use notice is read
- **THEN** it states that operator-surface error reporting is collected, what it contains, and why no
  consent interface is presented
- **AND** the statement names the surfaces it applies to.
