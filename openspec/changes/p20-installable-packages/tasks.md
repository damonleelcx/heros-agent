# Tasks — P20: Installable Packages & Self-Serve Distribution

Ordered by workstream. P20 is downstream of P11 (the binary + supply-chain floor) and P19 (the platform
deploy the *linked* mode targets); these tasks wrap the existing artifacts in a distribution and add no product
feature. Each task is independently verifiable. Every PR carries a **channel impact matrix** (curl|sh /
Homebrew / Scoop-winget / deb-rpm / container) with every "not affected" row explaining *why*.

## 1. System Designer + DevOps — Decide the one-way doors first (blocks everything else)
- [ ] 1.1 Ratify **D1 (native-runner matrix, not cross-CGO)**, **D2 (ed25519 floor; cosign opt-in)**, and
      **D5 (manifests generated, never hand-edited)** in `design.md` — cheap now, expensive later.
- [ ] 1.2 **Escalate the OS-trust cost decision (D3 / PRD OQ1)** to the user: (A) sign+notarize vs (B) documented
      one-command clear. Record the choice; the pipeline ships (B) by default until (A) is funded. Do **not**
      self-decide the spend/identity commitment.
- [ ] 1.3 Freeze the **supported-target matrix** and its **disclosed limits** (`windows/arm64`, native musl → image)
      as a contract before it appears on any README (System Designer: a matrix on a README is a contract).
- [ ] 1.4 State the **signing-key management + rotation** story (key only a CI secret; public key is a committed
      trust root; rotation is additive and planned).

## 2. DevOps — The tag-triggered release pipeline (no human in the upload path)
- [ ] 2.1 Author `.github/workflows/release.yml`: on a `v*` tag, a **matrix over native runners** (macOS / Ubuntu /
      Windows), each running `scripts/release-cli.sh <version-from-tag>`; **no cross-CGO**.
- [ ] 2.2 Add the **merge job**: collect the per-runner binaries, produce **one sorted `SHA256SUMS`** over all
      targets, and **sign** it with the ed25519 key from a CI secret → `SHA256SUMS.sig`.
- [ ] 2.3 **Publish a non-draft GitHub Release** with `GITHUB_TOKEN`: every binary, `SHA256SUMS`(`.sig`), the
      packaged installers, and the container image reference — **assert zero manual upload steps**.
- [ ] 2.4 Stamp the **version from the tag** into `internal/cli.ToolVersion` (`-ldflags -X`) as the single source;
      fail the release if any manifest carries a hand-written version.
- [ ] 2.5 Make the pipeline **fail-closed** on an **incomplete matrix**, an **unsigned** manifest on a non-dev
      channel, or a **reproducibility regression** (`TestReproducibleBuild`).
- [ ] 2.6 Make the pipeline **idempotent/retryable**: a re-run of the same tag reproduces the same artifact set with
      no manual cleanup.
- [ ] 2.7 Rehearse on a **pre-release tag** (`vX.Y.Z-rc.N`) to a **draft** Release before any GA tag.

## 3. DevOps — Native install channels (each verifies before PATH)
- [ ] 3.1 Author `scripts/install.sh` (macOS/Linux): detect OS+arch → download matching asset + `SHA256SUMS`(`.sig`)
      → **verify checksum, then signature against the pinned public key** → place on `PATH` — **fail closed**, no
      partial install. Support a pinned `HEROS_VERSION`.
- [ ] 3.2 Author `scripts/install.ps1` (Windows, `irm … | iex`): the same detect → verify → install → fail-closed
      flow.
- [ ] 3.3 Generate the **Homebrew tap** formula from the release (version=tag, url + sha256 from the manifest);
      pipeline PRs/pushes it — **not hand-maintained**.
- [ ] 3.4 Generate the **Scoop** manifest and **winget** manifest from the release, same way.
- [ ] 3.5 Build `.deb`/`.rpm` with **nfpm**, attached to the Release; declare publisher metadata.
- [ ] 3.6 Build + push the **container image** `ghcr.io/heros-foreal/heros:<version>` (+ `:latest` on GA),
      digest-pinned, as the musl/Alpine/CI answer.
- [ ] 3.7 Publish the `install.sh` script itself with a **pinned, checksum-referenced** URL in the README so the
      `curl | sh` target is auditable.
- [ ] 3.8 Ensure every channel is **uninstallable** by its own idiom and can install a **specific prior version**
      (the documented rollback).

## 4. DevOps + Product — OS-trust posture (Gatekeeper / SmartScreen)
- [ ] 4.1 Implement the **chosen** macOS posture (D3): Developer-ID sign + notarize in the pipeline, **or** the
      documented `xattr -d com.apple.quarantine` step surfaced in installer output + README.
- [ ] 4.2 Implement the **chosen** Windows posture: Authenticode sign, **or** the documented "More info → Run
      anyway" step; declare publisher metadata on `.msi`/`.exe` either way.
- [ ] 4.3 **Honesty gate:** release notes + README claim **only** the trust properties delivered — never
      "notarized"/"signed" for a channel that isn't (Sales lens).

## 5. Backend — Onboarding & self-update subcommands (shared verify routine)
- [ ] 5.1 Add the **no-arg `heros` greeting**: a zero-config message naming the first command; requires no
      config-file edit to reach a first `discover`.
- [ ] 5.2 Add `heros init`: write a **starter config** with safe defaults (idempotent; never clobbers an existing
      config without confirmation).
- [ ] 5.3 Add `heros doctor`: check toolchain-for-target-language, **provider-key resolvability** (the *real* path,
      not just "a value is set" — AI Engineer), and write access; on any gap **name the single next action**; never
      fail silently, never demand a prerequisite the command does not need.
- [ ] 5.4 Add `heros upgrade`: fetch latest → **same verify routine** as the installer → replace in place; **no-op
      when current**; **defer to the package manager** (print its upgrade command) when manager-installed; **never
      execute a download before verification**.
- [ ] 5.5 Extract **one shared verify routine** (checksum + ed25519 signature) used by both the installer helpers
      and `heros upgrade`, reusing `internal/release` — single source of truth for verification.
- [ ] 5.6 Guarantee **no update-check network call on the hot path**; any "newer version available" notice is
      opt-out-able, non-blocking, and the CLI sends **no telemetry**.
- [ ] 5.7 Confirm local commands (`discover`/`apply`/`eval`/`version`/`doctor`/`init`) work **offline with no
      account** (P11 free-tier durability preserved).

## 6. Frontend — Terminal UX of install + first run
- [ ] 6.1 Design the **installer output** (progress, verification result, next step) and the **no-arg greeting** /
      `doctor` output so they read for a *new* user, not a flag reference.
- [ ] 6.2 Ensure onboarding additions **do not hide** existing subcommands in `heros --help` (no-lose-function rule).
- [ ] 6.3 Verify every install script on a **real clean OS**, not just shellcheck/`vite`-style static pass.

## 7. QA — Fresh-machine smoke matrix + tamper red-check (the gate)
- [ ] 7.1 Stand up a **genuinely clean** environment per OS (fresh runner / minimal container, **no** prior
      toolchain, quarantine intact) — not the maintainer's machine.
- [ ] 7.2 For **each channel × OS**: install → assert `heros version` = the tag → `heros doctor` ready → a first
      `discover` + `eval` on a fixture repo yields a **real** result (assert to first eval, not `--help`).
- [ ] 7.3 **Tamper red-check:** flip a byte in a downloaded asset; assert **every** installer and `heros upgrade`
      **refuse** (fail-closed). A verify step never shown to reject is treated as absent.
- [ ] 7.4 **Upgrade simulation:** install `N`, `heros upgrade`/`brew upgrade` to `N+1`; assert replace + signature
      verified + a pinned-in-CI `N` untouched until asked.
- [ ] 7.5 **Rollback = install `@<prev>`:** assert every channel can install a specific prior version; no in-place
      downgrade magic.
- [ ] 7.6 Keep `TestReproducibleBuild` a **required** gate.

## 8. Sales Operations + Product — Honest capability map & docs
- [ ] 8.1 Write the README **install section**: the one-command install per OS, the offline verification steps, and
      the free/no-account framing — claiming **only ✅ 已交付** channels.
- [ ] 8.2 Disclose the **⛔ limits** (`windows/arm64`, native musl → image; and, until D3-A ships, the unsigned +
      documented-clear posture) plainly.
- [ ] 8.3 State the **free-vs-paid boundary** (CLI free forever; linked/hosted surfaces are the paid upgrade) so a
      customer's engineer can't catch a promise the product can't keep.
- [ ] 8.4 Author `docs/release/install.md` — the install + trust runbook — as a **scenario story**, cross-linking
      the P11 `cli-verification.md`.

## 9. Exit — M16 installable
- [ ] 9.1 Full **fresh-machine smoke matrix green** across every channel × supported OS, asserted to a first real
      eval.
- [ ] 9.2 A **push-a-tag** release produces a published Release with **zero manual steps** and a reproducible re-run.
- [ ] 9.3 Tamper red-check red on tampering; reproducibility gate green.
- [ ] 9.4 README/release-notes claims match delivered channels + trust posture exactly; limits disclosed.
