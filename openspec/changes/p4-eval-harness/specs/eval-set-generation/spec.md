# Eval-Set Generation — Spec Delta (P4)

Product rationale: [`../../../../docs/prd/P4-eval-harness.md`](../../../../docs/prd/P4-eval-harness.md) §6 (FR8–FR12).

Covers measured path/node/edge coverage, the layered generators, gold-vs-weak reference labeling,
the coverage report + gap-filling loop, and eval-set difficulty/diversity as a metric.

## ADDED Requirements

### Requirement: The generator SHALL measure achieved path, node, and edge-case coverage and emit a coverage report

The generator SHALL measure, against the Workflow IR, achieved **path coverage** (every IR edge,
every branch/router outcome, and loops driven to min, typical, and max iterations), **node
coverage** (each node reached across its input schema), and **edge-case coverage** (empty/malformed
input, tool-returns-nothing, retrieval-miss, context-overflow, adversarial/injection, boundary
values), and SHALL emit a coverage report of achieved vs. target for each.

#### Scenario: Coverage report enumerates uncovered paths

- **WHEN** an eval set is measured against a Workflow IR with two branch outcomes and a loop node
- **THEN** the coverage report states, per IR edge / branch outcome / loop iteration bound, whether
  it is covered
- **AND** an unexercised branch outcome is listed explicitly as an uncovered path
- **AND** the loop node's min, typical, and max iteration cases are each tracked separately

### Requirement: The generator SHALL run layered generators, with LLM-driven synthesis targeting currently-uncovered paths

The generator SHALL run generators behind one interface in layers: seed-from-real-traces (interface
present; active once P5 dynamic tracing exists), schema-driven synthesis from typed I/O contracts
(property/fuzz style producing valid + boundary + invalid inputs), LLM-driven synthesis that
**targets the specific paths the coverage report reports as uncovered** plus a fixed failure
taxonomy, and adversarial perturbation of existing cases.

#### Scenario: LLM generator is pointed at the residual gap, not the whole space

- **WHEN** after schema-driven synthesis the coverage report shows one branch outcome still uncovered
- **THEN** the LLM-driven generator is invoked with that uncovered branch as its explicit target
- **AND** it produces an input that forces execution down that branch

#### Scenario: Schema-driven generator produces boundary and invalid inputs

- **WHEN** the schema-driven generator runs against a node's typed I/O contract
- **THEN** it produces valid, boundary, and invalid inputs derived from that contract

### Requirement: The generator SHALL iterate a gap-filling loop until coverage thresholds are met or report the residual gap

The generator SHALL run a loop — measure coverage, target the gaps, regenerate — iterating until the
configured path/node/edge coverage thresholds are met or a maximum-iteration bound is reached, in
which case it SHALL report the residual uncovered gap rather than claim full coverage.

#### Scenario: Loop iterates until path coverage threshold is met

- **WHEN** the path-coverage threshold is 100% of IR edges and the initial set covers 70%
- **THEN** the generator iterates the measure-target-regenerate loop
- **AND** it stops once measured path coverage reaches the threshold
- **AND** the final coverage report shows the threshold met

#### Scenario: Unreachable path terminates at the max-iteration bound with a reported residual

- **WHEN** an IR edge is unreachable for any generated input and the max-iteration bound is reached
- **THEN** the generator terminates
- **AND** it reports the residual uncovered path rather than a false 100% coverage

### Requirement: Each case's reference SHALL be labeled gold or weak, and weak references SHALL NOT silently drive scoring

The generator SHALL label each case's reference output **gold** where an oracle exists (exact-match,
JSON-schema, deterministic tool result, or human-reviewed) or **weak** where it is LLM-generated and
unreviewed. A weak-labeled reference SHALL NOT drive a scoring or gating decision without being
surfaced as weak.

#### Scenario: Oracle-derived reference is gold; unreviewed LLM reference is weak

- **WHEN** one case has a deterministic exact-match oracle and another has only an LLM-generated
  reference that no human has reviewed
- **THEN** the first case is labeled `gold` and the second is labeled `weak`

#### Scenario: Weak-labeled reference cannot silently gate

- **WHEN** a scoring or gating decision is computed over a set containing weak-labeled cases
- **THEN** the weak-labeled cases are surfaced as weak in the result
- **AND** a weak-labeled reference does not silently drive a disqualification

### Requirement: The generator SHALL track eval-set difficulty and diversity and dedupe near-identical cases

The generator SHALL compute and report an eval-set **difficulty** metric and a **diversity** metric,
and SHALL dedupe near-identical cases. An eval set below the configured difficulty/diversity floor
SHALL be surfaced as low-confidence so a passing score on a weak set is not mistaken for a real one.

#### Scenario: A trivially-easy, low-diversity set is surfaced as low-confidence

- **WHEN** a generated eval set consists largely of near-duplicate, trivially-easy cases
- **THEN** the generator dedupes the near-identical cases
- **AND** it reports difficulty and diversity below the configured floor
- **AND** any score computed over that set is surfaced as low-confidence
