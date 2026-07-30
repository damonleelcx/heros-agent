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

## Ratification record (task 1)

The one-way doors below were decided **before** any pipeline, installer, or manifest existed, because each
is cheap now and expensive later. Where a decision has a code home, that home — not this document — is the
thing a build can check; the row names it so a reader can get from the decision to its gate in one step.

| Decision | State | Ratified | Enforced by |
|---|---|---|---|
| **D1** native-runner matrix, no cross-CGO | ✅ ratified | 2026-07-29 | `distribution.Shipped()` rows carry a `Runner`; `TestShippedRowsNameANativeRunner` fails on a runner whose OS/arch differs from the target, and `TestReleaseWorkflowMatchesTargetContract` holds `release.yml` to the same set |
| **D2** ed25519 floor, cosign opt-in | ✅ ratified | 2026-07-29 | `release.VerifyTrusted` is the only verifier any channel calls; `Attestation.Verified()` is the signed-manifest floor and is *not* satisfied by OS code signing |
| **D3** OS trust: sign+notarize vs documented clear | ✅ escalated · answered **(A)** 2026-07-29 · **REVERSED to (B) documented-clear 2026-07-30** | product owner, both times (PRD OQ1) | `distribution.ChosenPosture`, pinned by `TestChosenPostureIsTheRatifiedDecision`; `TestPostureBIsActuallyDelivered` asserts the workaround is surfaced, not merely documented |
| **D5** manifests generated, never hand-edited | ✅ ratified | 2026-07-29 | every channel manifest is emitted by `cmd/herosdist` from the tag + `SHA256SUMS`; `TestGeneratedManifestsCarryNoSecondVersion` fails on a hand-written version |

### D3 — what was escalated, what was answered, and what changed

The escalation was deliberate: (A) commits **recurring money** (Apple Developer Program, a code-signing
certificate) **and an organizational identity**, and the rulebook forbids an implementer from self-deciding
a spend or an identity commitment. Both paths were put with their costs.

**The decision log, both entries** — because a log showing only the current answer cannot tell a later reader
whether the question was ever asked:

| Date | Answer | Reason given |
|---|---|---|
| 2026-07-29 | **(A)** Developer-ID sign + notarize on macOS, Authenticode on Windows | best first-run UX; no scare screen |
| 2026-07-30 | **(B)** ship unsigned, surface the one-command clear — **RATIFIED** | no spend on signing |

What (B) obliges, and what it does not:

- Every macOS and Windows artifact ships **unsigned by the OS**, and every surface says so plainly.
- The workaround must be **in front of the user**, not merely in a document: the installer prints the
  pasteable `xattr -d com.apple.quarantine <the actual install path>` and the SmartScreen step, the README
  carries both, and `TestPostureBIsActuallyDelivered` fails if either goes missing. A posture whose answer
  lives only in a design document is indistinguishable from having no posture — which §5 of the PRD describes
  as the state that reads like *malware* rather than *unsigned*.
- Publisher metadata is still declared wherever a package can carry it (winget `Publisher`, nfpm
  `maintainer`/`vendor`). That a **bare `.exe` can carry none** — on Windows the Authenticode signature *is*
  the publisher declaration — is disclosed rather than glossed.
- **Homebrew and Scoop sidestep the cliff entirely**: a package manager fetches and places the binary, so it
  is not quarantined the way a double-clicked download is. Investing in those channels was always the larger
  half of the UX answer, which is why (B) is a smaller regression than it first looks — and why the tap and
  bucket repositories are now the highest-value thing left undone.
- The **verification floor is unchanged** (D2): the ed25519 signature over `SHA256SUMS` is what every channel
  checks, offline, with no account. OS code signing was always a UX upgrade layered on top, never a
  substitute — `Attestation.Verified()` deliberately ignores it, so the *security* story is identical under
  either answer. What (B) costs is a first-run warning, not a weaker guarantee.

**What the reversal cost in code: nothing user-facing.** Every claim was already rendered from `Attestation` —
what a release actually delivered — never from the ratified posture, so flipping the constant changed no
sentence anywhere in the product. This reversal is the event that proved the split was worth having: had the
claims been driven by the decision, (A) would have begun promising notarization the day it was given, and (B)
would have silently withdrawn the promise a day later.

The pipeline **keeps** its signing steps, gated and inert. They cost nothing to leave, they cannot make a claim
on their own (every attestation flag comes from a marker file a step actually wrote), and keeping them makes a
future decision to fund signing a **secrets change rather than a pipeline change**. Their log notices say
signing is *not part of the ratified posture* rather than *not yet provisioned* — the second would send a
maintainer looking for a budget that was deliberately declined.

### Signing-key management and rotation (task 1.4)

- The **private key is only ever a CI secret**, consumed by `release-cli.sh` in `${VAR:?}` refuse-to-start
  form. It is never in the repository, a log, or an artifact.
- The **trust root is compiled in** (`internal/release/trustroot.go`) and mirrored, for human use, in
  `docs/release/heros-release.pub`. A downloaded key would prove nothing: whoever can serve a binary can
  serve a key. The two copies are held identical by `TestTrustRootMatchesPublishedKey`.
- The trust root is a **key set with roles**, exactly one `active` (signs) and any number of `accepted`
  (verify only), because rotation must be planned before it is needed. A single-key verifier makes rotation
  a flag day: the moment a new key signs, every installed binary rejects every new release and the only
  repair is an unverified reinstall — the hole the signature exists to close.
- **Rotation is additive and staged:** publish the next public key as `accepted` → release once more with
  the old key (every binary in the field now trusts both) → flip the roles → after the overlap window (one
  minor version, stated in `docs/release/install.md`) delete the old entry. `TestRotationOverlapVerifiesBothKeys`
  rehearses all four steps with real signatures.
- **Compromise skips the overlap:** the leaked key is deleted in the same commit that adds its replacement,
  and the release notes say so. A deliberate, announced break is narrower than the alternative.

### Supported-target matrix, frozen as a contract (task 1.3)

A matrix on a README is a contract the moment it is published. So the matrix lives in
`internal/distribution` and the README's table is checked against it; adding or dropping a row is a
reviewed change, not a docs edit (`TestTargetMatrixIsFrozen`).

| OS | arch | Native binary | Native runner | Channels |
|---|---|---|---|---|
| macOS 12+ (Intel) | amd64 | ✅ | `macos-15-intel` | curl\|sh, Homebrew, container |
| macOS 12+ (Apple silicon) | arm64 | ✅ | `macos-15` | curl\|sh, Homebrew, container |
| Linux glibc 2.31+ | amd64 | ✅ | `ubuntu-22.04` | curl\|sh, Homebrew, `.deb`, `.rpm`, container |
| Linux glibc 2.31+ | arm64 | ✅ | `ubuntu-22.04-arm` | curl\|sh, Homebrew, `.deb`, `.rpm`, container |
| Windows 10/11 | amd64 | ✅ | `windows-2022` | PowerShell, Scoop, winget |
| Windows 11 | arm64 | ⛔ **not built** | — | run the amd64 build under x64 emulation |
| Alpine / any musl Linux | any | ⛔ **not built** (glibc CGO) | — | `ghcr.io/damonleelcx/heros:<version>` |

**Runner labels expire, and D1 makes that a contract problem.** The macOS rows first named `macos-13` and
`macos-14`; both images have since been retired or deprecated by GitHub (2025-12-04 and 2026-11-02), and a
retired label does not fail — the job simply queues until it times out, which reads as a slow release rather
than an impossible one. Two consequences are now built in rather than remembered:

1. **Runner arch is a reviewed table, not a pattern.** `distribution.runnerHosts` maps each label to its
   host OS/arch and `RunnerHost` refuses unknown labels. The label shapes are not systematic —
   `macos-15` is arm64 while `macos-15-intel` is x86_64, the reverse of the `-arm` suffix convention — so
   any inference rule confident enough to decide that pair decides it wrong, and a wrong answer here is
   precisely the cross-CGO build D1 exists to refuse.
2. **The macOS floor is pinned, not inherited.** With no deployment target set, clang stamps the *build
   host's* OS version into `LC_BUILD_VERSION`, so "macOS 12+" silently becomes "as new as whatever image
   GitHub gave us" — a claim the user's Mac enforces at launch. `distribution.MacOSFloor` states it,
   `release-cli.sh` exports it as `MACOSX_DEPLOYMENT_TARGET` and then reads the built binary back with
   `otool -l` to confirm the linker honoured it.

There is also a horizon worth naming now: GitHub retires x86_64 macOS entirely when the macOS 15 image goes,
in **Fall 2027**. `darwin/amd64` therefore becomes a ⛔ row on a known date. The matrix already has the shape
for saying so, so that will be a row edit, not a redesign.

The **⛔ rows are rows**, not absences. This is the P13 coverage lesson: a matrix listing only what works
forces the reader to infer everything else from a blank, and a blank reads as *should work — must be your
setup*. A user on `windows/arm64` who finds no row concludes the download is broken and opens a ticket. So
`TargetFor` returns limit rows too, and every refusal names one, with its reason and its answer.
`TestDisclosedLimitsAreStatedTotally` fails on a limit that carries either without the other.

The **glibc floor (2.31+) is part of the contract.** "Linux" with no version is the kind of claim that
becomes a support ticket the first time someone tries CentOS 7.

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
escalates the choice to the user** (PRD OQ1). Answered **(A) on 2026-07-29** and **reversed to (B) on
2026-07-30** by the same owner, on cost — see the decision log in the ratification record above.

**(B) is ratified: ship unsigned, with the one-command clear surfaced by the installer and the README.** The
claim is rendered from `distribution.Attestation` (a per-release fact) and never from the ratified posture,
which is why the reversal changed no user-visible sentence. If signing is ever funded, (A) layers on
additively — sign the same artifacts, notarize, re-attach — with **no change to the verification floor** (D2),
and the pipeline's inert signing steps mean that is a secrets change rather than a redesign. Crucially: **Homebrew and Scoop installs are not quarantined the way a double-clicked download is**
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
`SHA256SUMS` — and written/PR'd by the release pipeline. The `ghcr.io/damonleelcx/heros` image is built and
pushed by the same pipeline, digest-pinned.

**Why (L7 维护, backend "单一真相源，展示=执行").** A hand-maintained formula carries a **second copy** of the
version and checksum that inevitably drifts from the binary — the exact single-source-of-truth failure the
rulebook names. Generating them from the one build makes drift structurally impossible: there is one version
(the tag) and one checksum set (the manifest), and every channel is a projection of them. It is also the DevOps
rule-3 requirement (repeatable, retryable, no manual copy).

**Rejected — a human bumps the tap on each release.** That is manual-copy work (DevOps rule 3) and a drift
source; forbidden.

## Decision 6 — Container image is the musl/Alpine answer, not a static-musl native build

**Chosen:** Alpine/musl and generic-CI users are served by the **published container image** (`ghcr.io/damonleelcx/heros`),
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
