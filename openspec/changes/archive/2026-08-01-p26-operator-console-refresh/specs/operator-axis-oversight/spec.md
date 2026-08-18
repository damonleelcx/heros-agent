# Operator Axis Oversight — Spec Delta (P26)

Product rationale: [`../../../../../docs/prd/P26-operator-console-refresh.md`](../../../../../docs/prd/P26-operator-console-refresh.md)
§6 (FR59–FR64), §9.5 (AI Engineer lens). Technical decisions:
[`../../design.md`](../../design.md) D3, D4, D9.

Covers the surface that lets the platform's own backlog be ordered by evidence. Six axes now resolve into
`config_hash` and the console shows none of them, so the question *which materializer would unblock the most
refused nodes across the fleet* has no data path — and the last several such decisions were made without
one.

> Two asymmetries carry this capability. **The console reads the one coverage source and computes nothing** —
> a console that filtered or rolled up coverage would become a second opinion about coverage, and coverage is
> a claim about a customer's code. And **an absent row renders as unknown, never as *not applicable*** —
> because *not applicable* says "your call site cannot carry this", while the truth may be "we have not built
> the materializer". That substitution converts our backlog into the customer's problem, invisibly, and the
> customer has no way to discover it.

## ADDED Requirements

### Requirement: The console SHALL show each axis's declared status and its fleet-wide adoption

Per optimization axis, the surface SHALL show the axis's own declared `EXISTS` / `PARTIAL` / `ABSENT`
status, and how many tenants and nodes carry an override on that axis.

#### Scenario: An axis's honest status is rendered as declared
- **WHEN** the axis surface is opened
- **THEN** each axis shows the status the axis itself declares
- **AND** the console does not compute, adjust or reinterpret that status.

#### Scenario: Adoption is visible per axis
- **WHEN** an axis is examined
- **THEN** the number of tenants and nodes carrying an override on it is shown
- **AND** the count offers the drill-down to the nodes behind it.

### Requirement: The console SHALL show refusal counts by stable typed cause and by language

Refusal counts SHALL be keyed by the stable cause identifier, per axis and per language. The three causes —
not-expressible-at-a-call-site, call-site-cannot-carry-it, and no-materializer-for-this-language — SHALL
remain distinguishable, because they are answered by three different parties.

#### Scenario: The three causes are not conflated
- **WHEN** refusals are rendered
- **THEN** each cause is counted separately under its stable identifier
- **AND** no view presents a single combined refusal total as the only figure.

#### Scenario: A cause is identified, not described
- **WHEN** a refusal cause is rendered
- **THEN** it derives from a stable identifier rather than from prose
- **AND** the same cause renders identically on every surface that shows it.

### Requirement: The console SHALL rank which artefact would close the most refusals

The surface SHALL rank the artefacts that would close refusals — a form row, a list splitter, a statement
resolver, a registry row, a frontend field — by the number of refusals each would close.

#### Scenario: The backlog is orderable from evidence
- **WHEN** an axis owner opens the ranking
- **THEN** each candidate artefact is shown with the number of refusals it would close
- **AND** the ranking is drillable to the refused nodes it counts.

#### Scenario: The ranking is a count, not a score
- **WHEN** the ranking is rendered
- **THEN** it is presented as counts
- **AND** it does not use the visual grammar of a ranked evaluation result, because only the eval harness
  ranks and only a verified delta is a claim.

### Requirement: The coverage matrix SHALL be read from the one coverage source, and parity SHALL be asserted in both directions

The console SHALL read the same coverage source the transform's refusal, preflight, the CLI and the
customer console read, and SHALL render it as received. It SHALL NOT compute, cache, merge, re-rank or
reformat coverage. The surface SHALL NOT offer a cell the engine refuses, and SHALL NOT omit a cell the
engine materializes.

#### Scenario: Operator and customer see the same coverage
- **WHEN** the same node's coverage is read on the operator console and on the customer console
- **THEN** both show the same answer
- **AND** a disagreement is a test failure rather than a support conversation.

#### Scenario: Parity is asserted against the engine, not a fixture
- **WHEN** the parity assertion runs
- **THEN** it drives the real coverage source
- **AND** it fails if the surface offers a cell the engine refuses, or omits a cell the engine
  materializes.

#### Scenario: No caching introduces a stale refusal
- **WHEN** the engine stops refusing a cell
- **THEN** the surface stops offering it as refused on the next read
- **AND** no cached copy can present the superseded answer.

### Requirement: An absent coverage row SHALL render as unknown and SHALL NOT render as not applicable

An absent row SHALL render as unknown, naming what is missing. *Not applicable* SHALL be rendered only from
a present row whose named cause states it. A blank, a dash or a suppressed row SHALL NOT be used for an
absent row.

#### Scenario: A missing row does not blame the customer's code
- **WHEN** a coverage row is absent
- **THEN** the cell renders as unknown and names the missing input
- **AND** it does not render as *not applicable*, which is a claim about the customer's code.

#### Scenario: A row is never suppressed
- **WHEN** a row has no data
- **THEN** the row is still rendered, in the unknown state
- **AND** its existence is not hidden from the reader.

#### Scenario: Not applicable comes only from a stated cause
- **WHEN** a cell renders as *not applicable*
- **THEN** a present row carries a named cause that states it
- **AND** that cause is a stable identifier.

### Requirement: A coverage gap SHALL NOT be presented as a plan boundary

The surface SHALL NOT imply that a plan, tier, role, entitlement or setting would materialize a cell the
engine refuses. A gap is *not yet applied by the platform*, identical on every plan.

#### Scenario: No tier is implied to unlock a refused cell
- **WHEN** a refused cell is rendered
- **THEN** no plan, tier or entitlement is named as a way to change it
- **AND** the cause names the artefact that would close it instead.

#### Scenario: A permanent fact is not dressed as a delay
- **WHEN** a call site is refused for its own shape rather than for a missing materializer
- **THEN** the surface does not present it as *not yet*
- **AND** the cause distinguishes a fact about the source from a pending platform artefact.

### Requirement: Every aggregate on this surface SHALL offer its drill-down

A fleet-level count SHALL be traceable to the individual records behind it.

#### Scenario: A fleet count does not hide a single tenant
- **WHEN** a refusal count is examined
- **THEN** the individual refused nodes behind it are reachable
- **AND** a single tenant's pathological repository cannot be mistaken for a fleet-wide pattern.
