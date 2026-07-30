# Tasks — P20: Installable Packages & Self-Serve Distribution

Ordered by workstream. P20 is downstream of P11 (the binary + supply-chain floor) and P19 (the platform
deploy the *linked* mode targets); these tasks wrap the existing artifacts in a distribution and add no product
feature. Each task is independently verifiable. Every PR carries a **channel impact matrix** (curl|sh /
Homebrew / Scoop-winget / deb-rpm / container) with every "not affected" row explaining *why*.

## 1. System Designer + DevOps — Decide the one-way doors first (blocks everything else)
- [x] 1.1 Ratify **D1 (native-runner matrix, not cross-CGO)**, **D2 (ed25519 floor; cosign opt-in)**, and
      **D5 (manifests generated, never hand-edited)** in `design.md` — cheap now, expensive later.
- [x] 1.2 **Escalate the OS-trust cost decision (D3 / PRD OQ1)** to the user: (A) sign+notarize vs (B) documented
      one-command clear. Record the choice; the pipeline ships (B) by default until (A) is funded. Do **not**
      self-decide the spend/identity commitment.
- [x] 1.3 Freeze the **supported-target matrix** and its **disclosed limits** (`windows/arm64`, native musl → image)
      as a contract before it appears on any README (System Designer: a matrix on a README is a contract).
- [x] 1.4 State the **signing-key management + rotation** story (key only a CI secret; public key is a committed
      trust root; rotation is additive and planned).

## 2. DevOps — The tag-triggered release pipeline (no human in the upload path)
- [x] 2.1 Author `.github/workflows/release.yml`: on a `v*` tag, a **matrix over native runners** (macOS / Ubuntu /
      Windows), each running `scripts/release-cli.sh <version-from-tag>`; **no cross-CGO**.
- [x] 2.2 Add the **merge job**: collect the per-runner binaries, produce **one sorted `SHA256SUMS`** over all
      targets, and **sign** it with the ed25519 key from a CI secret → `SHA256SUMS.sig`.
- [x] 2.3 **Publish a non-draft GitHub Release** with `GITHUB_TOKEN`: every binary, `SHA256SUMS`(`.sig`), the
      packaged installers, and the container image reference — **assert zero manual upload steps**.
- [x] 2.4 Stamp the **version from the tag** into `internal/cli.ToolVersion` (`-ldflags -X`) as the single source;
      fail the release if any manifest carries a hand-written version.
- [x] 2.5 Make the pipeline **fail-closed** on an **incomplete matrix**, an **unsigned** manifest on a non-dev
      channel, or a **reproducibility regression** (`TestReproducibleBuild`).
- [x] 2.6 Make the pipeline **idempotent/retryable**: a re-run of the same tag reproduces the same artifact set with
      no manual cleanup.
- [x] 2.7 Rehearse on a **pre-release tag** (`vX.Y.Z-rc.N`) to a **draft** Release before any GA tag.
      → Local rehearsal of the full spine is implemented and **green** (`make release-rehearse`, plus
      `make release-rehearse-redcheck` proving the completeness gate refuses). The **remote half is handed
      off**: `HEROS_RELEASE_PRIVATE_KEY` must be installed as a repo secret by the owner (key generated
      2026-07-29, public half committed as `heros-release-2026b`), then `git push origin v0.20.0-rc.1`
      publishes the draft. Recorded in `docs/release/p20-evidence.md`.

## 3. DevOps — Native install channels (each verifies before PATH)
- [x] 3.1 Author `scripts/install.sh` (macOS/Linux): detect OS+arch → download matching asset + `SHA256SUMS`(`.sig`)
      → **verify checksum, then signature against the pinned public key** → place on `PATH` — **fail closed**, no
      partial install. Support a pinned `HEROS_VERSION`.
- [x] 3.2 Author `scripts/install.ps1` (Windows, `irm … | iex`): the same detect → verify → install → fail-closed
      flow. → Authored and **statically gated** (`TestInstallScriptsFailClosed`/`PinTheTrustRoot`/`DiscloseTheLimits`
      assert strict mode, the hash→signature→copy order, the pinned key and the arm64 disclosure). **Not executed
      locally**: this host has no native `pwsh` and the amd64 PowerShell image is killed under emulation. Its
      live run is the `windows-2022` row of the CI smoke matrix (section 7).
- [x] 3.3 Generate the **Homebrew tap** formula from the release (version=tag, url + sha256 from the manifest);
      pipeline PRs/pushes it — **not hand-maintained**.
- [x] 3.4 Generate the **Scoop** manifest and **winget** manifest from the release, same way.
- [x] 3.5 Build `.deb`/`.rpm` with **nfpm**, attached to the Release; declare publisher metadata.
- [x] 3.6 Build + push the **container image** `ghcr.io/heros-foreal/heros:<version>` (+ `:latest` on GA),
      digest-pinned, as the musl/Alpine/CI answer.
- [x] 3.7 Publish the `install.sh` script itself with a **pinned, checksum-referenced** URL in the README so the
      `curl | sh` target is auditable.
- [x] 3.8 Ensure every channel is **uninstallable** by its own idiom and can install a **specific prior version**
      (the documented rollback).

## 4. DevOps + Product — OS-trust posture (Gatekeeper / SmartScreen)
- [x] 4.1 Implement the **chosen** macOS posture (D3): Developer-ID sign + notarize in the pipeline, **or** the
      documented `xattr -d com.apple.quarantine` step surfaced in installer output + README.
      → (A) chosen: the pipeline carries `codesign --timestamp --options runtime` + `notarytool submit --wait`
      on both macOS runners, in the BUILD job (signing mutates the binary, so it must precede the checksums),
      gated on the Apple secrets so an unprovisioned identity discloses rather than blocks. Until they exist the
      delivered posture is honestly (B) and `install.sh` prints the pasteable `xattr` line — driven by
      `Attestation.FirstRunNotice`, so it disappears by itself the day notarization lands.
      **Stapling is recorded separately from notarization**: `stapler` cannot attach a ticket to a bare
      executable, so a notarized `heros` needs Gatekeeper's online check on first run, and the claim says so.
- [x] 4.2 Implement the **chosen** Windows posture: Authenticode sign, **or** the documented "More info → Run
      anyway" step; declare publisher metadata on `.msi`/`.exe` either way.
      → `signtool sign /fd SHA256 /tr <rfc3161>` in the Windows build job, gated on `WINDOWS_CERT_PFX`.
      Publisher metadata is declared where a package can carry it (winget `Publisher`, nfpm
      `maintainer`/`vendor`); the **bare `.exe` carries none of its own and the release says so** — on Windows
      the Authenticode signature *is* the publisher declaration, so an unsigned `.exe` shows no publisher in its
      file properties. Disclosed rather than papered over.
- [x] 4.3 **Honesty gate:** release notes + README claim **only** the trust properties delivered — never
      "notarized"/"signed" for a channel that isn't (Sales lens).
      → `distribution.AuditClaims` inventories the claim vocabulary and refuses an unearned affirmative, while
      **allowing the disclosure** (a gate whose cheapest fix is deleting the honest sentence is worse than none).
      Wired into `herosdist gate` over README.md and RELEASE_NOTES.md; proven red end-to-end on a planted
      overclaim, and proven not to fire on the generated notes or on a release that earned the claims.

## 5. Backend — Onboarding & self-update subcommands (shared verify routine)
- [x] 5.1 Add the **no-arg `heros` greeting**: a zero-config message naming the first command; requires no
      config-file edit to reach a first `discover`.
- [x] 5.2 Add `heros init`: write a **starter config** with safe defaults (idempotent; never clobbers an existing
      config without confirmation).
- [x] 5.3 Add `heros doctor`: check toolchain-for-target-language, **provider-key resolvability** (the *real* path,
      not just "a value is set" — AI Engineer), and write access; on any gap **name the single next action**; never
      fail silently, never demand a prerequisite the command does not need.
- [x] 5.4 Add `heros upgrade`: fetch latest → **same verify routine** as the installer → replace in place; **no-op
      when current**; **defer to the package manager** (print its upgrade command) when manager-installed; **never
      execute a download before verification**.
- [x] 5.5 Extract **one shared verify routine** (checksum + ed25519 signature) used by both the installer helpers
      and `heros upgrade`, reusing `internal/release` — single source of truth for verification.
- [x] 5.6 Guarantee **no update-check network call on the hot path**; any "newer version available" notice is
      opt-out-able, non-blocking, and the CLI sends **no telemetry**.
      → There is no version notice at all: the release index is reached from exactly one call site, reachable
      only by typing `upgrade`. No background goroutine, no timer, nothing to opt out of. No telemetry — the
      only headers on the one outbound request are User-Agent and Accept, asserted by test.
      ⚠️ **Found while asserting this**: `internal/cli` — documented as never importing `net/http` — has
      transitively linked it since P13 (`author.go` → `internal/authoring` → `internal/providergateway`). No
      local command dials (the deny-all-dialer runtime test still passes, and that is the guarantee users
      have), but the *structural* claim was false. The comment in `app.go` is corrected, and
      `TestCLIPackageNetworkLinkageIsNotWidened` pins the known chain so it cannot get worse. Restoring the
      structural guarantee is a change inside P13's design and is filed separately, not silently absorbed.
- [x] 5.7 Confirm local commands (`discover`/`apply`/`eval`/`version`/`doctor`/`init`) work **offline with no
      account** (P11 free-tier durability preserved).

## 6. Frontend — Terminal UX of install + first run
- [x] 6.1 Design the **installer output** (progress, verification result, next step) and the **no-arg greeting** /
      `doctor` output so they read for a *new* user, not a flag reference.
      → Installer prints target → download → ✓ checksum → ✓ signature (naming which verifier proved it) →
      installed path → the first-run OS notice **only while the release still needs it** → PATH advice → the two
      commands to run next. Every refusal names what went wrong AND what to do, and says "nothing was installed".
      Greeting names ONE command and states that no config is required; `doctor` gives one next action per gap.
      **Also delivered: a new console surface** at `/app/install` (web/console) rendering the same contract —
      available channels, generated-but-unpublishable channels (dashed, *no command shown*), the total platform
      matrix with reasons + answers, and the trust posture. Browser-verified: 3 tabs, 7 matrix rows, both trust
      renderings, no console errors.
- [x] 6.2 Ensure onboarding additions **do not hide** existing subcommands in `heros --help` (no-lose-function rule).
- [x] 6.3 Verify every install script on a **real clean OS**, not just shellcheck/`vite`-style static pass.
      → `install.sh`: **executed** on a genuinely clean `debian:12` container (no Go, no compiler, no prior
      heros) against a real natively-built linux/arm64 binary — 7/7 cases including all four refusals — plus a
      full run on this macOS host. `install.ps1`: **not executable here** (no native `pwsh`; the amd64 image is
      killed under emulation), so its live run is `scripts/install_smoke.ps1` on a real `windows-2022` runner via
      the new `.github/workflows/install-smoke.yml`, which also adds a fresh `macos-14` row — that image is the
      one that matters most, since its `/usr/bin/openssl` is LibreSSL and cannot verify ed25519 at all. The four
      refusals run there with no secret; the happy path is conditional and **says when it did not run**.

## 7. QA — Fresh-machine smoke matrix + tamper red-check (the gate)
- [x] 7.1 Stand up a **genuinely clean** environment per OS (fresh runner / minimal container, **no** prior
      toolchain, quarantine intact) — not the maintainer's machine.
      → Linux: a `debian:12` container with no Go, no compiler and no prior heros, installing a **real natively
      built** linux/arm64 binary (built in `golang:1.24`, `GOPROXY=off` for hermeticity). macOS/Windows: fresh
      GitHub-hosted runners via `.github/workflows/install-smoke.yml` (+ the post-publish `smoke` job in
      release.yml over all five rows). Both assert "no heros on PATH" **before** testing — a pre-existing binary
      would make the no-verifier branch unreachable and could pass a happy path for the wrong reason.
      The macOS host results in `docs/release/p20-evidence.md` are labelled **NOT a clean OS**, because they
      are not.
- [x] 7.2 For **each channel × OS**: install → assert `heros version` = the tag → `heros doctor` ready → a first
      `discover` + `eval` on a fixture repo yields a **real** result (assert to first eval, not `--help`).
      → Asserted to `discover` (1 node) **and** `eval` (quality 0.4167, read from `data.scores`) — exit 0 with no
      measurement is not a result. Ran for the curl|sh channel on macOS + clean Debian, and for the **.deb
      channel through dpkg** (`dpkg -i` → reports the version → `dpkg -r` removes it). ⚠️ **`doctor ready` is
      NOT the assertion**: the clean container has no Go, so doctor honestly reports a gap and `ready=false`
      while discover/eval still pass. Demanding `ready=true` would only pass on a machine that is not clean, so
      the case asserts exit 0 + **every gap names one next action** + no check omitted.
- [x] 7.3 **Tamper red-check:** flip a byte in a downloaded asset; assert **every** installer and `heros upgrade`
      **refuse** (fail-closed). A verify step never shown to reject is treated as absent.
- [x] 7.4 **Upgrade simulation:** install `N`, `heros upgrade`/`brew upgrade` to `N+1`; assert replace + signature
      verified + a pinned-in-CI `N` untouched until asked.
      → N+1 is a **real build**, not a renamed copy of N (a renamed binary reports N's stamped version, so the
      upgrade would look successful and leave the user on the old version — the packaging proof caught exactly
      that mistake once). Asserts `action=replaced`, the signing key id is reported, a second run is
      `no-op-already-current`, and N's asset is **byte-identical** afterwards.
- [x] 7.5 **Rollback = install `@<prev>`:** assert every channel can install a specific prior version; no in-place
      downgrade magic.
- [x] 7.6 Keep `TestReproducibleBuild` a **required** gate.
      → Its own named step in `ci.yml` (not only one of ~900 tests inside `make go`), with `-count=1` so a cached
      PASS cannot stand in for a run, and an explicit **failure on SKIP** — a required gate that can silently
      opt out is the env-gated tripwire this project has been burned by. Also runs on every one of the five
      release runners, because reproducibility is per-platform.

## 8. Sales Operations + Product — Honest capability map & docs
- [x] 8.1 Write the README **install section**: the one-command install per OS, the offline verification steps, and
      the free/no-account framing — claiming **only ✅ 已交付** channels.
      → **Generated** from the channel contract (`make readme-install`) and drift-gated by
      `TestReadmeInstallSectionMatchesContract`, so the day a channel stops being installable the README stops
      showing its command without anyone editing prose. `TestReadmeClaimsOnlyDeliveredChannels` additionally
      proves no undeliverable channel appears above the disclosure line.
- [x] 8.2 Disclose the **⛔ limits** (`windows/arm64`, native musl → image; and, until D3-A ships, the unsigned +
      documented-clear posture) plainly.
- [x] 8.3 State the **free-vs-paid boundary** (CLI free forever; linked/hosted surfaces are the paid upgrade) so a
      customer's engineer can't catch a promise the product can't keep.
- [x] 8.4 Author `docs/release/install.md` — the install + trust runbook — as a **scenario story**, cross-linking
      the P11 `cli-verification.md`.
      → Four scenes: an engineer with twenty minutes, her security reviewer verifying offline, upgrading and
      rolling back six weeks later, and a platform that is not built. Plus the key-rotation story. Cross-linked
      both ways with `cli-verification.md`, and audited by `TestRepositoryDocsMakeNoUnearnedClaim`.

## 9. Exit — M16 installable
- [ ] 9.1 Full **fresh-machine smoke matrix green** across every channel × supported OS, asserted to a first real
      eval.
      → **PARTIAL, and deliberately not checked off.** Green here, each asserted to a real `discover` + `eval`:
      `curl|sh` × macOS host, `curl|sh` × clean `debian:12`, `.deb` × clean `debian:12` (through dpkg),
      container image × Linux. That is 4 channel×OS combinations of the delivered set.
      **Not run here:** PowerShell × Windows, `.rpm` × a yum distro, and the four non-native OS rows. The
      matrix that covers them exists (`.github/workflows/install-smoke.yml` and the post-publish `smoke` job in
      `release.yml`, over all five runners) and needs a runner and a published release. This box stays open until
      that run is green — a green box for a matrix that has not run is the failure mode this project audits for.
- [ ] 9.2 A **push-a-tag** release produces a published Release with **zero manual steps** and a reproducible re-run.
      → **BLOCKED on the owner, deliberately not checked off.** The pipeline is complete and its spine is green
      locally (`make release-rehearse`), the re-run-reproduces property is proven by test, and the publish path has
      zero manual steps by construction. It cannot be *executed* until `HEROS_RELEASE_PRIVATE_KEY` is installed as
      a repository secret (the key was generated 2026-07-29; the public half is committed as
      `heros-release-2026b`) and a tag is pushed. Steps in `docs/release/p20-evidence.md` §3.
- [x] 9.3 Tamper red-check red on tampering; reproducibility gate green.
      → Tamper refusals proven in **both** environments and in four shapes (tampered binary, rewritten manifest,
      missing signature, foreign key), plus `verify-release` and `heros upgrade` refusing independently, plus the
      release gate refusing an incomplete matrix / unsigned manifest / overclaiming document. Reproducibility is
      green and is a named required CI step that **fails on SKIP**.
- [x] 9.4 README/release-notes claims match delivered channels + trust posture exactly; limits disclosed.
      → The README install section and the release notes are both **generated** from the same contract and the
      same per-release attestation; `AuditClaims` is wired into the release gate over both documents and is proven
      red on a planted overclaim while proven NOT to fire on the honest disclosure. Both disclosed limits and all
      three unpublishable channels appear, each with its reason and its answer.
