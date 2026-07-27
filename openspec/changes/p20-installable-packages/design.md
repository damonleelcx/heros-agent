# Design — P20: Installable Packages & Self-Serve Distribution

Product rationale: [`../../../docs/prd/P20-installable-packages.md`](../../../docs/prd/P20-installable-packages.md).
Builds on the P11 supply-chain floor: [`scripts/release-cli.sh`](../../../scripts/release-cli.sh),
[`cmd/herossign`](../../../cmd/herossign/main.go), [`docs/release/cli-verification.md`](../../../docs/release/cli-verification.md),
the published release public key [`docs/release/heros-release.pub`](../../../docs/release/heros-release.pub), and
the reproducible-build test. Distinct from P19 (server/cluster deploy): P20 is the **individual-machine**
delivery.

Every decision below is arbitrated on the **八级法则** — the single trade-off law this project uses:

> **安全 > 稳定 > UX > 运维 > 可演进 > 可扩展 > 维护 > 实现**

with its three iron laws: (L1) a higher level's degradation is never traded for a lower level's convenience;
(L2) decide at the highest level that separates the options and do not fall back for a lower-level convenience;
(L3) 实现 (single-shot implementation cost) is the floor and never outranks anything above it.

## Context

P20 is downstream of P11 and P19 and adds no component and no statistic; it **wraps** the P11 binary in a
distribution and reaches the individual user. What already exists (and does not change): the self-contained
per-target binary, the sorted `SHA256SUMS`, the ed25519 signing/verify in `herossign`, the reproducible build,
the published public key, and the contract-version window. What is absent and P20-shaped: any release workflow,
any install channel, any OS-trust posture, any onboarding, and any safe update path.

Three properties from the rulebook are non-negotiable and shape every decision: **signature verification is
mandatory and fail-closed on every channel** (安全); **the release has no manual step** — no human uploads,
copies, or merges (运维, DevOps rule 2/3); and **version is a single source of truth** — the tag — that every
manifest derives from and never a second hand-written copy (维护, backend "展示=执行").

## Decision 1 — Native-runner build matrix, not cross-CGO

**Chosen:** `release.yml` builds each target on its **native GitHub-hosted runner** (macOS runner →
`darwin/{amd64,arm64}`; Ubuntu runner → `linux/{amd64,arm64}`; Windows runner → `windows/amd64`), each invoking
the existing `release-cli.sh`; a final **merge job** concatenates the per-runner `SHA256SUMS` into one sorted
manifest and signs it once.

**Why (L2 稳定 over L8 实现).** The CLI links **CGO tree-sitter** language frontends; a cross-compiled CGO
binary needs a cross C toolchain (zig/osxcross/mingw) whose output is a different, less-tested artifact than the
native build. `release-cli.sh`'s own header already commits to native-per-runner as "the standard, honest way
to ship a CGO binary." A cross-CGO matrix is marginally less YAML (L8) but risks a subtly broken tree-sitter
binary that passes CI and breaks on a user's repo — a 稳定 regression bought with an 实现 convenience, which L1
forbids. Native runners also preserve the **per-platform reproducibility** the P11 test asserts.

**Rejected — goreleaser with cross-CGO / zig.** Attractive for its built-in Homebrew/Scoop/nfpm generation, and
we adopt that *pattern* (D5), but its cross-CGO path is the exact stability risk above. If goreleaser is used at
all, it is driven to build **on each native runner** (its `builds` pinned to the host), never to cross-compile
CGO. The floor stays: `release-cli.sh` is the source of truth for how a binary is built.

## Decision 2 — ed25519 + published public key is the verification floor; cosign is opt-in extra

**Chosen:** every channel verifies with the **ed25519 signature over `SHA256SUMS`** using the in-repo
`heros-release.pub` and the `herossign`/Go-stdlib verifier that **already ships** — no new tool, no network, no
account. Sigstore/cosign keyless attestation MAY be published **in addition** but is never required to verify.

**Why (L1 安全 + L3 UX, protecting 运维).** The verification must be runnable **offline, with no account**
(NFR2 — the P11 free-tier guarantee) and must **add no new trust surface** (NFR7): a security reviewer at an
air-gapped org has to approve the tool without a network call. cosign's strongest mode depends on a
transparency log and OIDC — a network/identity dependency that *weakens* the offline promise (a 安全/运维
property) to gain a *different* attestation. L2 says decide at the highest separating level: offline-verifiable
wins, so ed25519 is the floor. cosign is welcome as an **extra** signal for orgs that want it, never as the
gate — layering it never removes the offline path.

**Rejected — cosign/Sigstore as the primary verifier.** Reopens only if a customer's supply-chain policy
*mandates* Sigstore; the answer then is "cosign **in addition to** ed25519", not "instead of."

## Decision 3 — OS trust: escalate the cost decision, never default it silently

**The one-way door.** macOS Gatekeeper quarantines an unsigned internet Mach-O; Windows SmartScreen flags an
unsigned `.exe`. The two honest resolutions have very different costs:

- **(A) Sign + notarize.** Apple **Developer ID** signature + notarization (Gatekeeper-clean) and a Windows
  **Authenticode** cert, ideally **EV** (SmartScreen-clean). Best UX (L3): the user sees no scare screen. Cost:
  recurring money **and an organizational identity** (an Apple account, a CA-issued cert), plus the signing
  steps in the pipeline and their secrets.
- **(B) Ship unsigned + document the one-command clear.** No cost, no identity. UX cost: the first-run OS
  warning, answered by a **documented single command** (`xattr -d com.apple.quarantine ./heros` on macOS;
  "More info → Run anyway" on Windows) surfaced in the install output and README.

**Chosen posture (process, not a silent pick):** this is a **cost-escalation-path** decision — the rulebook
forbids self-deciding a spend or an identity commitment. So the design **states both paths with their costs and
escalates the choice to the user** (PRD OQ1). The **default the pipeline ships without a decision** is (B) —
because (B) is always-available and never *claims* more than it delivers (安全/诚实), while (A) can be added
later purely additively (sign the same artifacts, notarize, re-attach) with **no change to the verification
floor** (D2). Crucially: **Homebrew and Scoop installs are not quarantined the way a double-clicked download is**
(the package manager fetches and places the binary), so investing in the `brew`/`scoop` channels (D5) already
removes the Gatekeeper/SmartScreen cliff for most users — which is why (A) is a UX *upgrade*, not a
prerequisite.

**Honesty gate (安全).** Whatever is chosen, the release notes and README claim **only** what is delivered — a
channel that is not notarized is never described as notarized (Sales lens; FR12).

**Key management.** The ed25519 **private key is only ever a CI secret** in `${VAR:?}` refuse-to-start form; it
never appears in the repo, a log, or an artifact. Rotation is a stated one-way-door cost (the public key is a
committed trust root): a rotation publishes a new `heros-release.pub`, signs the *next* release with the new
key, and documents the overlap — it is additive, but planned now rather than improvised later.

## Decision 4 — Installers verify BEFORE placing on PATH, and fail closed

**Chosen:** `install.sh` / `install.ps1` do, in order: detect OS+arch → download the matching binary **and**
`SHA256SUMS`(`.sig`) → **verify checksum, then verify signature against the pinned public key** → only on
success, place the binary on `PATH`. Any failure is a **hard stop** with a clear message and **no partial
install**.

**Why (L1 安全, backend "禁止静默回落").** The CLI runs inside CI with repo access; skipping verification is a
supply-chain hole. The single most likely real-world failure is a user (or a naïve script) that installs the
binary and *never runs the verify step* — so the installer must verify **for** the user, and it must **fail
closed**: an installer that falls back to "install anyway, unverified" on a signature failure has traded 安全
for the 实现 convenience of not handling the error path (L1 violation). The verify routine is the **same code**
`heros upgrade` uses (one source of truth), and the `curl | sh` script itself is published, pinned by tag, and
checksum-referenced from the README so the pipe target is auditable.

**Rejected — "download and chmod" as the documented default.** Kept as a *fallback* for the offline/air-gapped
reviewer (who runs the P11 verify steps by hand), never as the happy path.

## Decision 5 — Package-manager manifests are generated by the pipeline, never hand-edited

**Chosen:** the Homebrew formula, Scoop manifest, winget manifest, and nfpm `.deb`/`.rpm` configs are
**templated from the release** — version = the tag, URLs = the Release asset URLs, checksums = the emitted
`SHA256SUMS` — and written/PR'd by the release pipeline. The `ghcr.io/heros-foreal/heros` image is built and
pushed by the same pipeline, digest-pinned.

**Why (L7 维护, backend "单一真相源，展示=执行").** A hand-maintained formula carries a **second copy** of the
version and checksum that inevitably drifts from the binary — the exact single-source-of-truth failure the
rulebook names. Generating them from the one build makes drift structurally impossible: there is one version
(the tag) and one checksum set (the manifest), and every channel is a projection of them. It is also the DevOps
rule-3 requirement (repeatable, retryable, no manual copy).

**Rejected — a human bumps the tap on each release.** That is manual-copy work (DevOps rule 3) and a drift
source; forbidden.

## Decision 6 — Container image is the musl/Alpine answer, not a static-musl native build

**Chosen:** Alpine/musl and generic-CI users are served by the **published container image** (`ghcr.io/heros-foreal/heros`),
which carries the glibc CLI in a glibc base; native musl binaries are a **disclosed limit** (NFR5), not shipped
in the first cut.

**Why (L2 稳定 over L6 可扩展/L8 实现).** The CGO tree-sitter binary is glibc-linked and will **not** run on
musl; a static-musl build is a different, less-tested toolchain path (the same class of risk as D1's cross-CGO).
Shipping a half-tested musl binary that segfaults on a user's Alpine CI is a 稳定 regression; the **already-built
image** answers the same need with a tested artifact. Adding a native musl target later is additive (a new
runner + a new matrix row) once there is demand — disclosed as a limit rather than faked.

**Rejected — a static-musl native binary in the first cut.** Deferred to demand (PRD OQ4); the image covers it
meanwhile.

## Decision 7 — Self-update verifies like a fresh install, never auto-executes, never phones home on the hot path

**Chosen:** `heros upgrade` fetches the latest Release, runs the **same verify routine** as the installer
(checksum + signature), and replaces the binary in place only on success — **never executing the download
before verification**, a clean no-op when already current, and **deferring to the package manager** (printing
`brew upgrade …` / `scoop update …`) when the binary is manager-owned. Ordinary commands make **no
update-check network call**; a "newer version available" notice, if enabled, is computed only when a command
already contacted the platform (linked mode) or is explicitly requested, is **opt-out-able**, and never blocks
or slows the command. The CLI sends **no telemetry**.

**Why (L1 安全 + L3 UX + NFR2 offline).** The two naïve updaters each break a top-level property: auto-download-
and-execute re-introduces the supply-chain risk D4 removes (安全); a per-command remote version check breaks the
offline/no-account promise and leaks usage timing (安全/隐私 + UX). L2 decides at the highest separating level:
both are 安全 regressions, so both are refused, and the update path is designed to keep verification and
offline-by-default intact. Overwriting a package-manager-owned file would corrupt that manager's state (稳定) —
hence defer-to-manager.

**Rejected — background auto-update; per-invocation version ping.** Both are 安全/隐私 regressions bought for
UX convenience; refused.

## Alternatives considered (whole-phase)

- **A desktop GUI installer / app.** Out by the product's "not a desktop app" framing; the CLI's threat model
  wants direct, reproducible, signature-verifiable delivery, not a store sandbox or a bundled Electron shell.
- **Fold CLI distribution into P19's deploy artifacts.** Rejected: P19 is *fleet/server* delivery; a developer
  installing a CLI on a laptop is a different job with a different trust posture (individual download vs operator
  applying a manifest). Conflating them would drag the whole-platform image into a `brew install`.
- **A hosted apt/yum repo now.** Deferred (PRD OQ3): the first cut attaches `.deb`/`.rpm` to the Release, which
  is 运维-cheaper and enough until distro demand justifies a signed hosted repo.

## Risks

| Risk | Level it threatens | Mitigation |
|---|---|---|
| Manual release step creeps back in | 运维 / 稳定 | CI-only `release.yml`; no human upload; retryable merge (D1). |
| A channel skips verification | 安全 | Installers verify before PATH, fail closed (D4); one shared verify routine. |
| Gatekeeper/SmartScreen reads as malware | UX | Trust decision escalated + documented clear or notarize (D3); brew/scoop sidestep it (D5). |
| Cross-CGO / musl binary subtly broken | 稳定 | Native-runner matrix; image for musl (D1, D6). |
| Manifest version drifts from binary | 维护 | Generated from the tag, single source (D5). |
| Signing key leak | 安全 | Key only a CI secret; rotation story stated (D3). |
| Update auto-executes / phones home | 安全 / 隐私 | Verify-before-replace, no hot-path network, no telemetry (D7). |
