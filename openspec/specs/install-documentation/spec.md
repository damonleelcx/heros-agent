# Installation & Release-Package Documentation — Spec (folded from P23)

Product rationale: [`../../../docs/prd/P23-legal-and-developer-docs.md`](../../../docs/prd/P23-legal-and-developer-docs.md)
§6 (FR41–FR48) and §9.6 / §9.8 (DevOps + Sales Operations lenses). Technical decisions:
[`../../changes/p23-legal-and-docs/design.md`](../../changes/p23-legal-and-docs/design.md) Decisions 6, 12 and 13. Documents — and only documents — the channels
[P20](../../../docs/prd/P20-installable-packages.md) delivers.

Covers the page a stranger lands on before they have anything installed: how to get the `heros` CLI from a
GitHub Release onto their machine, on macOS, Linux and Windows, and how to know that what they got is what
was published.

> 🔴 The rule this capability exists to enforce: **the verification step is not an appendix.** The CLI "runs
> inside your CI with access to your repository, so a compromised release is a compromise of every build it
> runs in." An install page whose copy-paste path places a binary on `PATH` without checking the checksum and
> the signature has silently removed the step the threat model depends on — and it will be the path everyone
> uses, because it is the one that fits on one line.

> **Honest status at authoring time.** `.github/workflows/` holds only `ci.yml` and `heros-eval.yml`: there
> is **no release pipeline and therefore no published GitHub Release yet**. What exists is the P11
> supply-chain floor — [`scripts/release-cli.sh`](../../../scripts/release-cli.sh) (reproducible build
> + sorted `SHA256SUMS` + ed25519 signature via `cmd/herossign`) and the verification runbook
> [`docs/release/cli-verification.md`](../../../docs/release/cli-verification.md). So this capability
> **documents what exists** and refuses to describe channels that do not — which is the claims fence applied
> where a fabricated install command would 404 in front of a first-time reader.

## ADDED Requirements

### Requirement: Installation documentation SHALL describe only channels that actually exist

An install channel SHALL be documented only when it is published and reachable. Describing an unshipped
channel SHALL fail the build, under the same rule that gates capability claims.

#### Scenario: An unshipped channel cannot be documented
- **WHEN** the install page describes a channel that the release pipeline does not publish
- **THEN** the build fails naming the channel
- **AND** no install command that would 404 reaches a reader.

#### Scenario: Before the release pipeline exists, the page documents what does
- **WHEN** no GitHub Release has been published
- **THEN** the install page documents the paths that exist — building from source and the reproducible-build
  plus checksum-and-signature verification runbook
- **AND** it states plainly that packaged channels are not yet available, rather than omitting the question
  or implying they are.

### Requirement: The release-asset table SHALL be generated from the published release, never hand-maintained

Asset filenames, target platforms, version strings and checksums SHALL be generated from the release
artifacts. A hand-typed checksum, filename or version SHALL fail the build.

#### Scenario: A new release updates the page without an edit
- **WHEN** a release is published and the documentation is built
- **THEN** the asset table reflects that release's assets, versions and checksums
- **AND** no hand edit to documentation was required.

#### Scenario: A hand-typed checksum is rejected
- **WHEN** content contains a literal checksum or asset filename that is not generated from the release
- **THEN** the build fails naming the value.

### Requirement: Every documented install path SHALL verify the download before the binary reaches PATH

Each install path SHALL present checksum **and** signature verification as part of the install, not as an
optional follow-up. A documented path that places the binary on `PATH` before verification SHALL fail
review and SHALL NOT be published.

#### Scenario: The copy-paste path is the verified path
- **WHEN** a reader follows the shortest install path on the page
- **THEN** that path verifies the checksum against the published manifest and the manifest's signature
  against the pinned public key before the binary is placed on `PATH`.

#### Scenario: Verification is not framed as optional
- **WHEN** the install page presents verification
- **THEN** it is a step of the install rather than an appendix, an aside, or a "recommended" extra
- **AND** the consequence of skipping it is stated in one sentence.

#### Scenario: Manual verification is runnable with no account and no network beyond the download
- **WHEN** a reader verifies a downloaded asset by hand
- **THEN** the documented commands run with no account
- **AND** they require no network access beyond having fetched the asset and the public key.

### Requirement: The OS-trust posture SHALL be stated honestly per platform

The documentation SHALL state, per platform, whether artifacts are signed and notarized. It SHALL NOT claim
signing or notarization for a channel that does not have it, and where an artifact is unsigned it SHALL
document the exact command that clears the OS quarantine and what that command means.

#### Scenario: An unsigned artifact is described as unsigned
- **WHEN** a platform's artifacts are not signed or notarized
- **THEN** the page says so
- **AND** it documents the exact quarantine-clear or trust step, and what the reader is accepting by running
  it.

#### Scenario: No trust claim outruns the pipeline
- **WHEN** the documentation states that an artifact is signed or notarized
- **THEN** that claim resolves to a signing step the release pipeline actually performs.

#### Scenario: The first-run warning is anticipated
- **WHEN** a reader on macOS or Windows will meet an OS warning on first run
- **THEN** the page tells them so before they meet it, and tells them what to do.

### Requirement: Installing a pinned version SHALL be documented for every channel

Every documented channel SHALL show how to install a **specific** version, not only the latest.

#### Scenario: A CI author pins a version
- **WHEN** a reader wants a specific version for a reproducible CI image
- **THEN** each documented channel shows the pinned-version invocation
- **AND** the pinned install verifies exactly as the latest install does.

### Requirement: Upgrade and uninstall SHALL be documented in each channel's own idiom

Each documented channel SHALL state how to upgrade and how to remove the tool by that channel's own
mechanism, including deferring to the package manager where the install was manager-mediated.

#### Scenario: Removal is documented, not left to guesswork
- **WHEN** a reader wants to remove the CLI
- **THEN** the page states the removal step for the channel they installed from
- **AND** names anything left behind (configuration or cache) and where it lives.

#### Scenario: A manager-installed binary is not upgraded behind the manager's back
- **WHEN** the CLI was installed through a package manager
- **THEN** the documented upgrade path defers to that manager rather than replacing the binary underneath it.

### Requirement: An offline or air-gapped install SHALL be documented

The documentation SHALL describe installing from a previously downloaded asset on a machine with no network
access, including how verification is performed there.

#### Scenario: An air-gapped machine can be brought up
- **WHEN** a reader transfers the asset, the checksum manifest, its signature and the public key to a machine
  with no network
- **THEN** the documented steps complete the install and the verification on that machine
- **AND** no step requires the machine to reach the internet or an account.

### Requirement: The install page SHALL lead directly into a first real result

The install documentation SHALL end by naming the next command, and that command SHALL be the quickstart's
first step — with no configuration-file edit between installing and a first result.

#### Scenario: Install does not dead-end
- **WHEN** a reader finishes installing
- **THEN** the page names the exact next command to run
- **AND** following it reaches a first discovery graph without opening a configuration file.

#### Scenario: The platform is not required to have installed successfully
- **WHEN** a reader completes the install with no account and no platform reachable
- **THEN** the documented verification of a successful install succeeds
- **AND** it does not depend on signing in or on the platform being up.
