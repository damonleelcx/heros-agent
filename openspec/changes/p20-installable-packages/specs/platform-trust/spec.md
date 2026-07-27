# Platform Trust (Gatekeeper / SmartScreen) — Spec Delta (P20)

Product rationale: [`../../../../docs/prd/P20-installable-packages.md`](../../../../docs/prd/P20-installable-packages.md)
§6 (FR10–FR12), §14 (OQ1), and §9 (DevOps + Product + Sales lenses). Architecture:
[`../../design.md`](../../design.md) Decision 3.

Covers the OS-trust posture: macOS **Gatekeeper** quarantines an unsigned internet Mach-O, and Windows
**SmartScreen** flags an unsigned `.exe`. Without a posture, the first thing a new user sees reads as *malware*.
This capability requires the posture be **decided, delivered, and stated honestly** — never defaulted silently
and never over-claimed.

> Cost-escalation-path: signing/notarization is a recurring spend **and** an organizational identity commitment;
> the rulebook forbids self-deciding it. The decision is escalated to the user; the default is the
> always-available documented-clear path until signing is funded.

## ADDED Requirements

### Requirement: The macOS trust posture SHALL be one of two explicitly chosen paths

macOS artifacts SHALL be delivered under one of two postures, **recorded as a decision**: (a) Developer-ID-signed
**and notarized** (Gatekeeper-clean), or (b) unsigned with a **documented one-command** quarantine-clear step
surfaced in both the installer output and the README. The choice SHALL NOT be defaulted silently.

#### Scenario: The chosen macOS posture is delivered and documented
- **WHEN** a macOS user obtains `heros` via a channel subject to Gatekeeper
- **THEN** either the artifact is notarized and runs without a warning, or the first-run warning has a documented
  one-command answer (`xattr -d com.apple.quarantine ./heros`) shown in the installer output and README
- **AND** which posture is in effect is recorded as an explicit decision, not left implicit.

#### Scenario: Package-manager installs are not quarantined
- **WHEN** a user installs via Homebrew
- **THEN** the binary is not subject to the double-clicked-download quarantine
- **AND** the docs note that `brew`/`scoop` sidestep the Gatekeeper/SmartScreen cliff for most users.

### Requirement: The Windows trust posture SHALL be one of two explicitly chosen paths

Windows artifacts SHALL be delivered under the analogous choice: **Authenticode-signed** (ideally EV,
SmartScreen-clean) or a **documented "More info → Run anyway"** path; the `.msi`/`.exe` SHALL declare publisher
metadata either way.

#### Scenario: The chosen Windows posture is delivered and documented
- **WHEN** a Windows user runs the downloaded installer
- **THEN** either it is Authenticode-signed and passes SmartScreen, or the SmartScreen prompt has a documented
  "More info → Run anyway" answer in the README/installer output
- **AND** the `.msi`/`.exe` declares publisher metadata regardless of which posture is in effect.

### Requirement: The trust posture SHALL be stated honestly and never over-claimed

The release notes and README SHALL claim **only** the trust properties actually delivered. The platform SHALL
NOT describe a channel as "notarized" or "signed" when it is not.

#### Scenario: Claims track delivery
- **WHEN** artifacts ship under the documented-clear (unsigned) posture
- **THEN** the release notes and README do not claim notarization/Authenticode signing
- **AND** the honest posture (unsigned + documented clear) is stated plainly.

#### Scenario: The signing key/cert is a CI secret only
- **WHEN** signing or notarization is enabled
- **THEN** the signing key/certificate exists only as a CI secret in `${VAR:?}` refuse-to-start form
- **AND** it appears in no log, artifact, or repository file, and its rotation story is documented.
