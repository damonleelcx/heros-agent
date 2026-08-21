# Operator Surface Ledger — Delta (P38)

Extends [`../../../../specs/operator-surface-ledger/spec.md`](../../../../specs/operator-surface-ledger/spec.md),
folded from P26.

**Why this capability is extended.** The ledger's fence asserts that a capability's row names a
destination that exists in the surface registry. It does not assert that the destination *implements* the
capability. The `operator-agent-authoring` row —
`| operator-agent-authoring | surface | /agent#publish, /agent |` — is true, and the destination it names
serves every axis as a free-text input, which the capability's own folded spec forbids. Presence passed;
conformance was never asked.

This delta adds a conformance assertion for **one row**. The general hole is real, covers every other row,
and is a large change to a fence fourteen phases depend on — so it is stated here and assigned, not
closed by this change. A narrow fix presented as a general one retires the concern without fixing it.

## ADDED Requirements

### Requirement: A ledger row MAY carry a conformance assertion, and where it does, a destination SHALL satisfy it

A row's destination existing is the minimum. A row carrying a conformance assertion is satisfied only
when its destination also demonstrates the behaviour named in the assertion.

#### Scenario: A conforming destination satisfies the row
- **WHEN** a row carries a conformance assertion and its destination demonstrates the named behaviour
- **THEN** the ledger fence passes for that row

#### Scenario: A destination that exists but does not conform
- **WHEN** a row carries a conformance assertion and its destination exists but does not demonstrate the named behaviour
- **THEN** the fence fails, naming the row, the destination and the assertion
- **AND** the failure is not satisfiable by adding another destination

#### Scenario: Rows without an assertion are unchanged
- **WHEN** a row carries no conformance assertion
- **THEN** it is judged on destination existence exactly as before
- **AND** no existing row's state changes as a result of this capability

### Requirement: The operator agent authoring row SHALL assert that every axis is bound to its vocabulary

#### Scenario: An axis served by a free-text input
- **WHEN** the destination for `operator-agent-authoring` renders any axis as a free-text input
- **THEN** the fence fails, naming that axis

#### Scenario: Every axis bound
- **WHEN** the destination renders every axis as a control bound to that axis's vocabulary
- **THEN** the assertion passes

#### Scenario: The general gap is recorded, not closed
- **WHEN** this assertion passes
- **THEN** it is evidence about one row only
- **AND** it is not cited as evidence that other rows' destinations implement their capabilities
