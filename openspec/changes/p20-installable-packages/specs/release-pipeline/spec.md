# Release Pipeline — Spec Delta (P20)

Product rationale: [`../../../../docs/prd/P20-installable-packages.md`](../../../../docs/prd/P20-installable-packages.md)
§6 (FR1–FR5) and §9 (DevOps lens). Architecture: [`../../design.md`](../../design.md) Decisions 1, 2, 5. Builds on
the P11 supply-chain floor: [`scripts/release-cli.sh`](../../../../scripts/release-cli.sh),
[`cmd/herossign`](../../../../cmd/herossign/main.go), and the reproducible-build test.

Covers the tag-triggered GitHub Actions workflow that turns a tag into a **published, signed GitHub Release**
with **no human in the upload path** — the release surface P11 documented but never automated (there is no
`release.yml` today; `.github/workflows/` holds only `ci.yml` and `heros-eval.yml`).

> DevOps rulebook, non-negotiable: (1) the only path is CI/CD — if the pipeline fails, the release fails;
> (2) **物理上禁止任何人手工上传文件到 release**; (3) no manual copy/merge — the per-runner merge is a
> repeatable, retryable job.

## ADDED Requirements

### Requirement: A release tag SHALL build every target on its native runner

A tag matching the release pattern (`v*`) SHALL trigger a GitHub Actions workflow that builds the `heros` CLI
on the **native runner** for each supported target (macOS runner for `darwin/{amd64,arm64}`, Ubuntu runner for
`linux/{amd64,arm64}`, Windows runner for `windows/amd64`) by invoking `scripts/release-cli.sh`. The workflow
SHALL NOT cross-compile the CGO binary.

#### Scenario: A tag builds the full native matrix
- **WHEN** a `v*` tag is pushed
- **THEN** the workflow runs one build job per supported target on that target's native OS runner
- **AND** each job produces a self-contained `heros-<version>-<os>-<arch>` binary via `release-cli.sh`.

#### Scenario: Cross-CGO is refused
- **WHEN** the build matrix is defined
- **THEN** no target is produced by cross-compiling CGO from a foreign host
- **AND** each CGO tree-sitter binary is built on its own platform, preserving per-platform reproducibility.

### Requirement: The pipeline SHALL merge and sign one checksum manifest

The workflow SHALL collect the per-runner binaries into a single **sorted `SHA256SUMS`** covering every target
and SHALL sign it with the ed25519 release key (sourced from a CI secret) via `herossign`, producing
`SHA256SUMS.sig`.

#### Scenario: One signed manifest over all targets
- **WHEN** all build jobs complete
- **THEN** a merge job writes one `SHA256SUMS` with a line per released binary, sorted, reproducible
- **AND** it emits `SHA256SUMS.sig`, a detached ed25519 signature over that manifest.

#### Scenario: The signing key never leaves CI
- **WHEN** the manifest is signed
- **THEN** the private key is read only from a CI secret in `${VAR:?}` refuse-to-start form
- **AND** it appears in no log, no artifact, and no repository file.

### Requirement: The pipeline SHALL publish a GitHub Release with no manual upload

The workflow SHALL publish a **non-draft GitHub Release** whose assets include every target binary,
`SHA256SUMS`, `SHA256SUMS.sig`, the packaged installers, and the container-image reference — performed by the
CI job's token. **No human SHALL upload, copy, or merge any artifact.**

#### Scenario: The release is published by CI, not a person
- **WHEN** the merge-and-sign job succeeds
- **THEN** the workflow publishes a non-draft Release carrying all assets using `GITHUB_TOKEN`
- **AND** there is no step in which a person uploads or hand-assembles an asset.

#### Scenario: The release is idempotent and retryable
- **WHEN** the workflow is re-run for the same tag after a transient failure
- **THEN** it reproduces the same artifact set with no manual cleanup
- **AND** it does not require a person to delete or re-upload anything.

### Requirement: The release version SHALL be a single source of truth stamped from the tag

The release version SHALL be taken from the **tag** and stamped into `internal/cli.ToolVersion` via
`-ldflags -X`. No package manifest, formula, or doc SHALL carry a hand-written version that can drift from it.

#### Scenario: Version derives from the tag
- **WHEN** `vX.Y.Z` is released
- **THEN** `heros version` prints `X.Y.Z` from the stamped `ToolVersion`
- **AND** every generated manifest's version equals the tag, never a separately edited value.

### Requirement: The pipeline SHALL fail closed on an incomplete or unverifiable release

The workflow SHALL **block the release** if the built matrix is incomplete (a supported target missing), if the
merged `SHA256SUMS` is unsigned on a non-dev channel, or if the reproducible-build assertion regresses.

#### Scenario: A missing target blocks the release
- **WHEN** any supported target fails to build
- **THEN** the workflow fails and publishes nothing
- **AND** no partial Release is left for users to install from.

#### Scenario: An unsigned manifest blocks a real release
- **WHEN** a non-dev release is assembled without a valid `SHA256SUMS.sig`
- **THEN** the workflow fails before publishing
- **AND** the failure names the missing signature.

#### Scenario: A reproducibility regression blocks the release
- **WHEN** `TestReproducibleBuild` fails on the release commit
- **THEN** the release pipeline fails
- **AND** the regression is surfaced in CI, not in a customer's audit.
