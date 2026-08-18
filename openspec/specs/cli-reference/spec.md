# CLI Reference — Spec (folded from P23)

Product rationale: [`../../../docs/prd/P23-legal-and-developer-docs.md`](../../../docs/prd/P23-legal-and-developer-docs.md)
§6 (FR35–FR40) and §9.3 / §9.7 (Backend + QA lenses). Technical decisions:
[`../../changes/archive/2026-08-01-p23-legal-and-docs/design.md`](../../changes/archive/2026-08-01-p23-legal-and-docs/design.md) Decisions 6 and 12.

Covers the documented command surface of the `heros` binary — the product's free, offline, no-account entry
point, and the surface a customer's CI branches on.

> The command surface is already a **public contract**: `internal/cli/exit.go` says so about the exit codes,
> because "a CI step that fails for an unclear reason gets disabled." Documentation that lags that contract
> does not merely inconvenience a reader — it makes the contract unusable, since a contract nobody can look
> up is a contract nobody can rely on.

The registry at authoring time holds `help`, `version`, `discover`, `apply`, `eval`, `status`, `login` and
`link`; `login` and `link` are platform-facing and can be absent from a build. The reference is **generated**
from that registry (Decision 6), so this spec constrains the *coverage and content* of the generation, not a
hand-maintained page.

## Requirements

### Requirement: Every subcommand in the CLI registry SHALL appear in the reference

The reference SHALL cover the complete command surface. A subcommand present in the registry with no
reference entry SHALL fail the build.

#### Scenario: A new subcommand cannot ship undocumented
- **WHEN** a subcommand is added to the CLI registry and the build runs
- **THEN** the build fails if the generated reference has no entry for it
- **AND** the failure names the subcommand.

#### Scenario: Coverage is checked in both directions
- **WHEN** the CLI fences run
- **THEN** documentation naming a command that does not exist fails
- **AND** a command that exists with no documentation also fails.

### Requirement: The exit-code contract SHALL be documented as a contract, with each code's remedy

The reference SHALL document every exit code, its meaning and the **remedy it implies**, and SHALL match
`internal/cli`. Three distinct remedies SHALL NOT be documented as sharing a code.

#### Scenario: A CI author can branch without parsing prose
- **WHEN** a reader opens the exit-code reference
- **THEN** each code is listed with its meaning and the remedy it implies
- **AND** the distinction between a customer-configured gate failing and the tool breaking is stated
  explicitly, because the two have opposite remedies.

#### Scenario: A drifted code fails the build
- **WHEN** an exit code's meaning changes in `internal/cli` without the reference changing
- **THEN** the build fails naming the code and the disagreement.

### Requirement: Each command SHALL state whether it runs offline with no account

Every command's entry SHALL state whether it functions **offline with no account**, preserving the P11
free-tier durability guarantee, and platform-facing commands SHALL document that they may be **absent from a
build**.

#### Scenario: The free surface is legible as a boundary
- **WHEN** a reader opens the reference for `discover`, `apply` or `eval`
- **THEN** the entry states that the command runs offline against the reader's own provider key with no
  account.

#### Scenario: A platform command documents its absence
- **WHEN** a reader opens the reference for a platform-facing command such as `login` or `link`
- **THEN** the entry states that the command requires the platform
- **AND** it documents the "unavailable in this build" outcome rather than leaving it to be discovered at
  the terminal.

### Requirement: Every flag SHALL be documented with its default and its environment equivalent

Each flag SHALL carry its type, default and — where the CLI resolves one — the environment variable that
supplies it, including which wins when both are set.

#### Scenario: Precedence is documented, not inferred
- **WHEN** a flag also resolves from an environment variable
- **THEN** the reference names the variable and states which value wins when both are present.

#### Scenario: A removed flag fails the build
- **WHEN** a flag is removed from a subcommand while documentation still references it
- **THEN** the build fails naming the flag and the page.

### Requirement: Each command entry SHALL show a complete invocation and what success looks like

Every entry SHALL carry at least one invocation that runs as written, together with the observable outcome
and the exit code it returns on success.

#### Scenario: An example is runnable, not a sketch
- **WHEN** a reader copies a command example from the reference
- **THEN** it executes as written against the documented prerequisites
- **AND** the entry states what the reader should see and the exit code on success.

### Requirement: A deprecated command or flag SHALL be marked before it is removed

The reference SHALL mark deprecations with the replacement and the release in which removal is expected, and
a removal SHALL NOT be the reader's first notice.

#### Scenario: A deprecation is visible ahead of the removal
- **WHEN** a command or flag is deprecated
- **THEN** its reference entry is marked deprecated, names the replacement, and states when removal is
  expected
- **AND** the entry remains published until the removal ships.
