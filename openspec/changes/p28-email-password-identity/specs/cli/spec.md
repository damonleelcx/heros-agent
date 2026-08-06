# cli

## MODIFIED Requirements

### Requirement: Every command SHALL be non-interactive by default

Every command SHALL complete without requiring a terminal. A command MAY prompt **only** when a terminal is
attached, and only for a value that also has at least one non-interactive source. With no terminal, a command
SHALL refuse rather than prompt, naming every non-interactive way to supply the missing value.

#### Scenario: Commands run without a terminal

- **WHEN** a command is executed with no attached terminal, as in a CI runner, and every required value is
  supplied non-interactively
- **THEN** it completes without requiring interactive input
- **AND** it does not block waiting for a response.

#### Scenario: A missing required input fails rather than prompting

- **WHEN** a required input is absent and no terminal is attached
- **THEN** the command exits with the invalid-configuration code and names the missing input
- **AND** it names every non-interactive way to supply it
- **AND** it does not prompt for it, and does not block.

#### Scenario: A prompt is offered only where a person can see it

- **WHEN** a required value is absent and a terminal **is** attached
- **THEN** the command may prompt for it
- **AND** a secret value is read without echoing it to the terminal.

## ADDED Requirements

### Requirement: `heros login` SHALL authenticate with an email address and a password

#### Scenario: Signing in from a terminal
- **WHEN** `heros login` runs with a terminal attached and no credential supplied
- **THEN** it asks for an address and then for a password, reading the password without echo
- **AND** on success it stores a credential with file permissions readable only by its owner
- **AND** it reports who was authenticated and prints no secret

#### Scenario: Signing in without a terminal
- **WHEN** the address is supplied by flag or environment and the password by environment or standard input
- **THEN** it authenticates without prompting

#### Scenario: The password is never an argument
- **WHEN** the CLI's flags are enumerated
- **THEN** there is no flag that takes a password value
- **AND** a supplied password appears in no process argument, no emitted envelope and no log

### Requirement: `heros login` SHALL state which kind of credential it stored, and the kinds SHALL keep their existing meanings

#### Scenario: An email and password mint a personal credential
- **WHEN** `heros login` succeeds with an address and a password
- **THEN** the stored credential's kind is reported as personal
- **AND** removing that person from their organization causes the next request made with it to be refused, with
  no restart and no grace period

#### Scenario: The machine path is unchanged
- **WHEN** `heros login --token` is used
- **THEN** it behaves exactly as it did before this change and reports a machine credential

#### Scenario: The device flow remains available
- **WHEN** `heros login --device` is used
- **THEN** the device authorization flow runs as before and mints a personal credential
