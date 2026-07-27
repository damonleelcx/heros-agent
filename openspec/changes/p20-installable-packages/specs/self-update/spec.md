# Self-Update — Spec Delta (P20)

Product rationale: [`../../../../docs/prd/P20-installable-packages.md`](../../../../docs/prd/P20-installable-packages.md)
§6 (FR16–FR18), §14 (OQ5), and §9 (Backend + Product lenses). Architecture: [`../../design.md`](../../design.md)
Decision 7. Reuses the same verification code path as `install-channels` (one source of truth for verify).

Covers a **safe** update path. The naïve updaters each break a top-level property: auto-download-and-execute
re-introduces the supply-chain risk verification removes (安全); a per-command remote version check breaks the
offline/no-account promise and leaks usage (安全/隐私 + UX). This capability keeps verification and
offline-by-default intact.

## ADDED Requirements

### Requirement: `heros upgrade` SHALL verify like a fresh install before replacing the binary

`heros upgrade` SHALL fetch the latest release, verify the checksum and ed25519 signature **exactly as a fresh
install**, and replace the binary in place **only on success**. It SHALL NOT execute a downloaded artifact before
verification, and SHALL be a clear no-op when already current.

#### Scenario: Upgrade verifies before replacing
- **WHEN** a user runs `heros upgrade` with a newer release available
- **THEN** the new binary is downloaded, its checksum and signature verified against the pinned public key, and
  only then does it replace the current binary
- **AND** no downloaded artifact is executed before verification.

#### Scenario: A tampered update is refused
- **WHEN** the fetched update fails checksum or signature verification
- **THEN** `heros upgrade` refuses and leaves the current binary in place
- **AND** it does not fall back to installing the unverified artifact.

#### Scenario: Upgrade is a no-op when current
- **WHEN** `heros upgrade` runs and the installed version is already the latest
- **THEN** it makes no change and says so clearly.

### Requirement: `heros upgrade` SHALL defer to the package manager when manager-installed

When `heros` was installed via a package manager (Homebrew/Scoop/apt/…), `heros upgrade` SHALL defer to that
manager by printing the manager's upgrade command rather than overwriting a manager-owned file.

#### Scenario: Manager-owned binaries are not clobbered
- **WHEN** `heros upgrade` runs on a manager-installed binary
- **THEN** it prints the correct manager upgrade command (e.g. `brew upgrade …`) and does not overwrite the file
- **AND** the package manager's state is left consistent.

### Requirement: The CLI SHALL NOT check for updates on the hot path and SHALL send no telemetry

Ordinary commands SHALL make **no update-check network call** on the hot path. Any "newer version available"
notice SHALL be opt-out-able and non-blocking, and the CLI SHALL send **no telemetry**.

#### Scenario: Ordinary commands do not phone home
- **WHEN** a user runs a local command (`discover`/`apply`/`eval`/`version`)
- **THEN** the command performs no update-check network request and no telemetry send
- **AND** it runs at full speed offline.

#### Scenario: The update notice is passive and opt-out-able
- **WHEN** a "newer version available" notice is enabled and a version gap is known (e.g. from an already-made
  linked-mode call, or an explicit check)
- **THEN** the notice is informational, does not block or slow the command, and can be turned off
- **AND** it never triggers an automatic download or execution.
