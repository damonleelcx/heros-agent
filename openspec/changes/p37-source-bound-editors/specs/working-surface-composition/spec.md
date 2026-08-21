# Working Surface Composition — Spec (P37)

Product rationale: [`../../../../../docs/prd/P37-source-bound-editors.md`](../../../../../docs/prd/P37-source-bound-editors.md) §6.3, §6.4.
Design reasoning: [`../../design.md`](../../design.md) D2, D3, D4.

Covers where a sentence lives, what may never leave a working surface, and the fence that keeps a
working route from growing back into a document.

## ADDED Requirements

### Requirement: Static text SHALL live on the working surface only when its content varies with the reader's data

The test is mechanical and is applied per block, not per page: a block whose content is identical for
every reader is documentation and belongs on the reading surface; a block whose content changes with the
reader's data is the product and stays.

#### Scenario: Reader-invariant text is on the reading surface
- **WHEN** a block of static text is identical for every reader
- **THEN** it is rendered on the reading surface
- **AND** the working surface links to it

#### Scenario: Reader-varying text stays on the working surface
- **WHEN** a block's content is produced from the reader's own data
- **THEN** it is rendered on the working surface
- **AND** it is not replaced by a link

### Requirement: A working route SHALL stay within its static-prose budget

#### Scenario: Budget enforced at build
- **WHEN** a working route's static prose exceeds the budget
- **THEN** the build fails, naming the route and the excess
- **AND** the failure is not waivable by a comment or an environment variable

#### Scenario: The budget's blind spot is not treated as coverage
- **WHEN** the budget passes
- **THEN** it is evidence about volume only
- **AND** it is not cited as evidence that content was moved rather than rearranged

### Requirement: Moved text SHALL land on the reading surface and SHALL NOT be relocated into a tooltip, an accordion or a modal

#### Scenario: Destination is a document section
- **WHEN** a block is moved off a working surface
- **THEN** it lands in a named reading-surface section reachable by URL
- **AND** it is not placed in a tooltip, a disclosure widget or a modal

#### Scenario: Every destination exists before the move
- **WHEN** a working surface links to a reading-surface section
- **THEN** that section exists and the link resolves
- **AND** a broken destination link fails the build

#### Scenario: Every moved block is accounted for
- **WHEN** a change removes static text from a working surface
- **THEN** the change enumerates each removed block and the destination it landed in
- **AND** a block with no destination is not removed

### Requirement: A refusal's cause text SHALL remain on the working surface, verbatim

#### Scenario: The engine's sentence is rendered unmodified
- **WHEN** a lower layer refuses with a typed cause naming an axis and a node
- **THEN** the working surface renders that cause text unchanged
- **AND** it is not paraphrased, summarised, shortened or moved to the reading surface

### Requirement: A not-measured state and its named missing input SHALL remain on the working surface

#### Scenario: Absence is drawn, not omitted
- **WHEN** a surface cannot report a measurement
- **THEN** it renders `not_measured` and names the input that would resolve it
- **AND** it renders neither zero nor an empty region in its place

### Requirement: A stated boundary SHALL be rendered above the control it bounds

#### Scenario: The wall is stated before the work
- **WHEN** an axis can be authored but cannot yet be written into the reader's source
- **THEN** the surface states that boundary above the control, naming the missing artifact
- **AND** the reader meets it before composing a change, not after submitting one

#### Scenario: The control is live, not disabled
- **WHEN** a boundary prevents a change from reaching source
- **THEN** the control remains usable and the change remains authorable and pinnable
- **AND** the reason is stated rather than expressed as a greyed-out control
