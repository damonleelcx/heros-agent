# Graph Topology — Spec (P34)

Topology is **spec-level**, beside `order` and `edges`, not a `Dimension` — every `Dimension` is a
property of one node, and topology is a property between nodes.

Today the spec can express a linear ordering and typed edges. This capability adds concurrency,
conditional routing and merge, additively, so a spec declaring none serialises byte-identically to its
pre-P34 form.

## ADDED Requirements

### Requirement: The system SHALL allow a spec to declare that members of a group may run concurrently

#### Scenario: Concurrency is declared over the ordering
- **WHEN** a spec declares a concurrent group
- **THEN** `order` still contains every node in a defined sequence
- **AND** a replay visits nodes in that sequence even when the live run overlapped them

#### Scenario: A group member outside the ordering
- **WHEN** a concurrent group names a node the ordering does not contain
- **THEN** the spec is refused at validate

#### Scenario: No declaration hashes as before
- **WHEN** a spec declares no concurrent group and no merge
- **THEN** it serialises byte-identically to its pre-P34 form
- **AND** the golden vectors reproduce

### Requirement: The system SHALL refuse a fan-in that declares no merge

#### Scenario: Undeclared merge
- **WHEN** two or more concurrent members converge on one downstream node and no merge is declared
- **THEN** the spec is refused at validate
- **AND** no default combination is applied

#### Scenario: Declared merge
- **WHEN** a fan-in declares how its inputs are combined
- **THEN** the merge is validated against the downstream node's typed input contract

### Requirement: The system SHALL support a conditional edge whose predicate follows the existing expression binding rules

#### Scenario: Predicate validated at resolve
- **WHEN** an edge declares a predicate
- **THEN** the predicate is validated at spec-resolve time under the same rules that govern an `expr` binding
- **AND** it is never inferred from the call site

#### Scenario: Out-of-scope predicate
- **WHEN** a predicate names a symbol that is not in the program's lexical scope at that call site
- **THEN** it is refused, naming the symbol

#### Scenario: Edge kinds remain a closed set
- **WHEN** an edge declares a kind
- **THEN** it is one of the closed set, and an unknown kind is refused

### Requirement: The system SHALL validate every topology form through the typed-contract gate before generating a codemod

#### Scenario: Contract violation
- **WHEN** a declared concurrency, conditional edge or merge violates a typed I/O contract
- **THEN** it is rejected before any codemod is generated
- **AND** the mismatch is anchored to the offending edge

#### Scenario: Adaptable mismatch
- **WHEN** a mismatch is adaptable
- **THEN** the preview shows the adapter and the source diff it would generate
- **AND** the adapter appears as an explicit node in the spec, not as a hidden runtime coercion

### Requirement: The system SHALL refuse an unsafe topology rewrite by name rather than dropping it

#### Scenario: Language cannot carry a concurrent group
- **WHEN** a spec declares a concurrent group for a call site in a language whose transform cannot safely materialise it
- **THEN** the transform refuses with a typed unsafe-rewrite error naming the node and the axis
- **AND** the override is NOT silently dropped

#### Scenario: Coverage names what is missing
- **WHEN** the graph axis is unavailable for a language
- **THEN** the coverage report names which of the frontend, the analysis, or the language support is missing
- **AND** it does not report a generic unsupported state

### Requirement: The system SHALL keep the eval harness unaware that these axes exist

#### Scenario: No bespoke oracle
- **WHEN** a variant differing only in loop, harness envelope or topology is scored
- **THEN** it is scored by the existing harness with no axis-specific scorer, oracle or metric
- **AND** any reduction appears in the existing token, cost and task-success metric family
