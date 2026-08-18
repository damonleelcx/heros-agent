# CLI — Spec Delta (P11)

Product rationale: [`../../../../../docs/prd/P11-cli-ci-integration.md`](../../../../../docs/prd/P11-cli-ci-integration.md)
§6 (FR1–FR8) and §7. Design reasoning: [`../../design.md`](../../design.md) Decisions 1, 8, 9.

Covers the customer-installed binary: the command set the product advertises, the guarantee that the
local workflow needs **no account and no network**, provider-credential isolation, the machine/human
output split, exit codes as a discriminating contract, and explicit version compatibility.

> **Why offline-first is a requirement rather than a courtesy.** This is the only surface available on
> every plan including Free, and the license was chosen so the ingestion layer could be adopted freely.
> A tool that demands an account before it will read a repository does not become a default. The
> guarantee is also the shortest answer in a security review: it works with the network off.

## ADDED Requirements

### Requirement: The CLI SHALL provide discovery, apply, and eval as commands

The CLI SHALL provide commands that produce the Workflow IR from a repository, realize a Variant Spec
as a reviewable diff, and execute a scored multi-seed evaluation.

#### Scenario: The three advertised operations are available

- **WHEN** the CLI's command set is enumerated
- **THEN** discovery, apply, and eval are each available as commands
- **AND** each produces its documented artifact.

#### Scenario: Apply produces a reviewable diff, not an in-place mutation

- **WHEN** a Variant Spec is applied
- **THEN** the result is a reviewable diff produced against an isolated working copy
- **AND** the user's working tree is not modified in place.

### Requirement: Discovery, apply, and eval SHALL complete with no account and no network

Discovery, apply, and eval SHALL complete successfully without a platform account and without network
access to the platform. No command SHALL require a platform account to produce its primary output, and
no command SHALL fail because the platform is unreachable unless its purpose is to communicate with the
platform.

#### Scenario: The local workflow runs with networking denied

- **WHEN** discovery, apply, and eval are run in an environment where network access to the platform is
  denied
- **THEN** each completes successfully and produces its documented artifact
- **AND** no command reports a failure attributable to the absent network.

#### Scenario: No account is required

- **WHEN** a user who has never authenticated runs discovery, apply, or eval
- **THEN** each completes successfully
- **AND** the user is not prompted to create or supply an account.

#### Scenario: Only platform-facing commands require connectivity

- **WHEN** a command whose purpose is to communicate with the platform is run without connectivity
- **THEN** that command reports the connectivity failure
- **AND** the local workflow commands remain unaffected.

### Requirement: Provider credentials SHALL remain in the customer environment

Provider credentials SHALL be read from the customer's environment and used only for calls originating
on the customer's machine. The CLI SHALL NOT transmit a provider credential to the platform under any
configuration, flag, or verbosity level.

#### Scenario: No provider credential is transmitted

- **WHEN** any command that communicates with the platform executes
- **THEN** no provider credential appears in the transmitted data
- **AND** this holds under every configuration and verbosity level.

#### Scenario: No provider credential is written to output or artifacts

- **WHEN** a command produces output, logs, or artifacts
- **THEN** no provider credential appears in any of them
- **AND** this holds on the failure path as well as the success path.

### Requirement: Machine-consumable output SHALL be separated from human narration and SHALL be versioned

Machine-consumable output SHALL be written to the standard output stream in a stable, versioned
format. Human-facing narration and progress SHALL be written to the standard error stream.

#### Scenario: A consumer reads machine output without parsing prose

- **WHEN** a command runs with narration enabled
- **THEN** the standard output stream contains only the machine-consumable result
- **AND** all narration and progress appear on the standard error stream.

#### Scenario: The output format declares its version

- **WHEN** machine-consumable output is produced
- **THEN** it carries a format version
- **AND** a consumer can detect a format change rather than misparsing it.

### Requirement: Exit codes SHALL distinguish gate failure, operational error, and invalid configuration

The CLI SHALL exit with distinct, documented codes for success, a customer-configured gate failing, an
operational error, and an invalid configuration. These conditions SHALL NOT share an exit code.

#### Scenario: A configured gate failing is distinguishable from a crash

- **WHEN** a customer-configured quality gate fails and, separately, an operational error occurs
- **THEN** the two produce different exit codes
- **AND** a consumer can tell which remedy applies without parsing output.

#### Scenario: An invalid configuration has its own code

- **WHEN** the CLI is invoked with an invalid configuration
- **THEN** it exits with the invalid-configuration code
- **AND** not with the operational-error or gate-failure code.

#### Scenario: Success is unambiguous

- **WHEN** a command completes with no gate failure and no error
- **THEN** it exits with the success code.

### Requirement: A CLI outside the supported version window SHALL refuse to produce results

The CLI SHALL declare the platform-contract version it implements. When that version is outside the
platform's supported window, the CLI SHALL report the required version and SHALL NOT produce results
under mismatched semantics.

#### Scenario: An unsupported version names what is required

- **WHEN** a CLI whose contract version is outside the supported window contacts the platform
- **THEN** it reports the required version
- **AND** it does not proceed to produce results.

#### Scenario: A supported version proceeds

- **WHEN** the CLI's contract version is within the supported window
- **THEN** the command proceeds normally.

### Requirement: Configuration resolution SHALL be documented and inspectable

Configuration SHALL resolve in a documented precedence order across command-line flags, environment,
a project configuration file, and defaults. The CLI SHALL be able to report each effective value **and
the source it came from**.

#### Scenario: Effective configuration reports its provenance

- **WHEN** a user asks the CLI for its effective configuration
- **THEN** each value is reported together with the source that supplied it
- **AND** a value overridden at a higher precedence is shown as such.

#### Scenario: Precedence is deterministic

- **WHEN** the same setting is supplied by more than one source
- **THEN** the documented precedence decides which applies
- **AND** the outcome does not depend on ordering within a source.

### Requirement: The CLI SHALL provide a version command

The CLI SHALL provide a `version` command that reports the tool version and the contract versions it
implements — the run-linking payload contract and the machine-output format — together with the pinned
link endpoint. It SHALL require no configuration and SHALL contact no network.

#### Scenario: version reports the tool and contract versions

- **WHEN** the `version` command is run
- **THEN** the machine output carries the tool version, the payload-contract version, and the
  output-format version
- **AND** no network access is required to produce it.

#### Scenario: version is pinnable by a pipeline

- **WHEN** a pipeline reads the `version` machine output
- **THEN** it can pin or gate on the reported tool version
- **AND** the reported link endpoint is the pinned platform endpoint.

### Requirement: Every command SHALL be non-interactive by default

Every command SHALL complete without interactive input and SHALL NOT require a terminal.

#### Scenario: Commands run without a terminal

- **WHEN** a command is executed with no attached terminal, as in a CI runner
- **THEN** it completes without requiring interactive input
- **AND** it does not block waiting for a response.

#### Scenario: A missing required input fails rather than prompting

- **WHEN** a required input is absent
- **THEN** the command exits with the invalid-configuration code and names the missing input
- **AND** it does not prompt for it.
