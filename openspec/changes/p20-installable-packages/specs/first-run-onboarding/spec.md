# First-Run Onboarding — Spec Delta (P20)

Product rationale: [`../../../../docs/prd/P20-installable-packages.md`](../../../../docs/prd/P20-installable-packages.md)
§6 (FR13–FR15) and §9 (Product + Backend + AI Engineer lenses). Inherits the P11 free-tier durability guarantee
(local commands work offline, no account).

Covers the install-to-first-success journey: a freshly-installed `heros` on a machine that has never seen the
platform must reach a first **real** eval without editing a config file — the interaction-simplicity-first rule
applied at minute zero.

> 減免用户输入: the user who cannot get to a first success in five minutes never gets to a second. Every failure
> names the next step; nothing fails silently.

## ADDED Requirements

### Requirement: `heros` with no arguments SHALL give a zero-config first-run greeting

`heros` invoked with **no arguments** on a fresh install SHALL print a zero-config greeting that names the first
command to run and requires **no config-file editing** to reach a first `discover`.

#### Scenario: A new user is told what to run first
- **WHEN** a user runs `heros` with no arguments right after install
- **THEN** it prints a short greeting naming the first command (e.g. `heros discover` / `heros init`)
- **AND** the user can reach a first `discover` without opening or editing a config file.

#### Scenario: The greeting does not hide existing commands
- **WHEN** the onboarding greeting and `heros --help` are compared
- **THEN** every existing subcommand remains listed and reachable
- **AND** onboarding additions do not remove or obscure prior functionality.

### Requirement: `heros init` SHALL write a starter config with safe defaults

`heros init` SHALL write a starter configuration with safe defaults, idempotently, without clobbering an
existing config without confirmation.

#### Scenario: init produces a working starter config
- **WHEN** a user runs `heros init` on a machine with no config
- **THEN** a starter config with safe defaults is written
- **AND** a subsequent local command works against it without further editing.

#### Scenario: init does not silently overwrite
- **WHEN** `heros init` runs where a config already exists
- **THEN** it does not overwrite it without explicit confirmation.

### Requirement: `heros doctor` SHALL check real prerequisites and name the next action on any gap

`heros doctor` SHALL check the local prerequisites — the toolchain for the target language, **provider-key
resolvability on the real path**, and write access — and, on any gap, name the **single next action**. It SHALL
NOT fail silently and SHALL NOT demand a prerequisite the command does not actually need.

#### Scenario: doctor confirms real readiness
- **WHEN** a user runs `heros doctor` with everything in place
- **THEN** it reports ready
- **AND** its provider-key check verifies the key is actually resolvable on the real eval path, not merely that
  a value is set (no plausible-but-meaningless "ready").

#### Scenario: doctor names the fix on a gap
- **WHEN** a prerequisite is missing (e.g. no provider key, missing language toolchain)
- **THEN** `heros doctor` names the single next action to fix it
- **AND** it does not fail silently or block on a prerequisite the intended command does not need.

### Requirement: Local commands SHALL remain fully functional offline with no account

The local commands (`discover`, `apply`, `eval`, `version`, `doctor`, `init`) SHALL work **offline with no
account**, preserving the P11 free-tier durability guarantee.

#### Scenario: First success is offline and account-free
- **WHEN** a new user runs the quickstart on a machine with no account and no network beyond the install download
- **THEN** discovery and a first eval complete against the user's own provider key
- **AND** no local command requires an account or a platform network call to function.
