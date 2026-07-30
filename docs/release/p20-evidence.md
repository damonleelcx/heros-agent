# P20 evidence — what was actually run, and what was not

This document exists because of a specific, repeated failure in this repository: a task list that goes green
while two of its items were never built. The audit that found that one is why every claim below names the
command that produced it and, where something could not be run here, says so in the same sentence rather than
in a footnote.

Read this as the answer to "how do you know?" for each of P20's exit criteria. The task list
(`openspec/changes/p20-installable-packages/tasks.md`) carries the same facts per task; this is the one page.

Machine: macOS 14 on Apple silicon (arm64), Go 1.26.5, Docker 29.5.2, no `pwsh`, `/usr/bin/openssl` is
LibreSSL 3.3.6. Date: 2026-07-29/30.

---

## 1 · What ran, and what it proved

### The release spine — `make release-rehearse`

Runs the whole pipeline locally on a rehearsal tag: parse the tag → native build through `release-cli.sh` →
assert the binary reports the tag's `tool_version` → reproducibility gate → merge with per-runner cross-check →
sign (raw + sshsig) → attest → the fail-closed gate → release notes.

| Outcome | Evidence |
|---|---|
| ✅ Spine green end to end with a real trust-root key | `release gate — v0.20.0-rc.1 … ✅ matrix complete (5 targets) · manifest signed · reproducible build` |
| ✅ Gate goes RED on an incomplete matrix | `make release-rehearse-redcheck` → `red-check PASS: the gate refused an incomplete matrix` |
| ✅ Gate goes RED on an unsigned manifest | the same rehearsal without the key: `⛔ manifest-signed — channel "prerelease" requires a signed manifest` |
| ✅ Gate goes RED on an overclaiming document | planted `RELEASE_NOTES.md` → `⛔ honest-claims — …claims "macos-notarized", which this release did not deliver` |
| ✅ Re-running a tag reproduces the same signed bytes | `TestReleaseRerunReproducesTheSameArtifactSet` compares two independent runs' manifest AND signature |

**Not run here:** the `gh release create/edit/upload` path and the published-asset re-verify. They need a
repository token and a published Release. See §3.

### The install channels — `make install-smoke`

Two environments, the same cases. The clean-room one builds a **real native linux/arm64 binary inside
`golang:1.24`** (hermetic: host module cache mounted, `GOPROXY=off`) and installs it in a `debian:12` container
with no Go, no compiler and no prior heros.

| Case | macOS host | clean `debian:12` | What it proves |
|---|---|---|---|
| happy-path | ✅ | ✅ | verify-then-PATH works, and the installed binary reports the release version |
| tampered-binary | ✅ refused | ✅ refused | the checksum step catches substituted bytes |
| tampered-manifest | ✅ refused | ✅ refused | **only the signature** can catch a manifest rewritten to match |
| unsigned-release | ✅ refused | ✅ refused | no fallback to checksums-only |
| foreign-signing-key | ✅ refused | ✅ refused | the pinned key is the one that decides |
| doctor-reports-actionably | ✅ | ✅ | every gap names one next action; `doctor` exits 0 either way |
| first-discover | ✅ 1 node | ✅ 1 node | asserted to real work, not `--help` |
| first-eval | ✅ quality 0.4167 | ✅ quality 0.4167 | a **number**, read from `data.scores`, not just exit 0 |
| upgrade-replaces-and-verifies | ✅ | ✅ | `heros upgrade` N→N+1, signature verified, key id reported |
| upgrade-is-idempotent | ✅ | ✅ | a second run is `no-op-already-current` |
| pinned-prior-version-untouched | ✅ | ✅ | N's asset is byte-identical after the upgrade |
| upgrade-refuses-a-downgrade | ✅ | ✅ | an index offering an older tag does not roll a user back |
| rollback-by-pinned-install | ✅ | ✅ | `HEROS_VERSION=<prev>` is the documented rollback |
| uninstall | ✅ | ✅ | the channel's own idiom removes it |
| deb-channel-install-and-remove | — | ✅ | `dpkg -i` → reports the version → `dpkg -r` removes it |

**A false negative worth recording.** The first `dpkg -r` check reported `STILL PRESENT`. `dpkg -r` was
correct (exit 0, file deleted); the check used `command -v heros`, and **dash caches a resolved command path**
after the first invocation in the same shell. The harness now tests the path with `test -e`. The lesson is the
one this project keeps relearning: when a check fails, establish whether the check or the product is wrong
before doing anything else.

**A second one, in the opposite direction.** `doctor-ready` was first written to require `ready == true` and
failed in the clean container — correctly. The fixture repo has a `go.mod`, the container deliberately has no
Go, so `doctor` reported *"toolchain: go is missing → install go: …"* and `ready=false`, while `discover` and
`eval` both passed because only `apply`'s verification gate needs that toolchain. Demanding `ready=true` would
have made the case pass **only on a machine that is not clean**. The assertion now checks the property that
matters: exit 0, every gap actionable, no check omitted.

### The generated package manifests — `make packaging-proof DIST=<dist>`

Content tests assert what the generator emits; this hands each file to the tool that consumes it.

| Outcome | Evidence |
|---|---|
| ✅ Homebrew formula parses as Ruby | `ruby -c heros.rb` |
| ✅ Scoop manifest is valid JSON | parsed, `version` and `hash` checked against the signed manifest |
| ✅ winget's three files are valid YAML and agree on identifier+version | parsed; a disagreement is a rejected submission |
| ✅ A real `.deb` and `.rpm` build from the generated nfpm configs | `heros_0.20.0~rc.1_arm64.deb`, `heros-0.20.0~rc.1-1.aarch64.rpm` |
| ✅ The `~` in the .deb version is correct | Debian reserves `-`; `0.20.0~rc.1` sorts **before** `0.20.0`, so apt cannot treat an rc as newer than its GA |
| ✅ The container image builds **and runs**, reporting the release version | `docker build` then `docker run … version` |

**A real defect this caught.** The first run built the image around a linux binary that had been *renamed* to
the release's version. The image built fine and then reported a different `tool_version`. A filename is not
provenance — the proof now rebuilds the binary natively when the present one does not report the expected
version.

### The web console surface — browser-verified

`/app/install` and `/preview/install`, checked in a real browser at 1440×2400 and 1280×900:

- 3 tabs render; 7 platform rows (5 shipped, 2 disclosed limits, each with a reason **and** an answer);
- available channels render as cards **with** commands; the three unpublishable ones render as dashed rows
  **with no command at all** and their blocker in full;
- the Trust tab renders the no-release-published state and a delivered posture side by side, earned claims
  distinguished from unearned by icon and colour **and** by the wording of the sentence;
- no console errors; no horizontal page scroll.

Suites: `web/console` **355/355**; Go `make go` clean; `internal/distribution` and `internal/release` green.

### The distribution against a real third-party repository — `cmd/p20hermes`

The smoke matrix exercises the installed binary against *this* repository's fixture: one node, known shape,
written by us. That is the right fixture for testing an installer and the wrong one for the question a customer
asks. So the binary installed through the real `install.sh` — verification and all — was run against
**nousresearch/hermes-agent** at `8eb06e75b9db` (8,034 files; 3,653 Python, 1,275 TypeScript, 591 TSX).

```
=== P20 distribution — run for https://github.com/nousresearch/hermes-agent ===
binary     : .smoke/hermesrun/host/bin/heros        ← installed by scripts/install.sh, signature verified
platform   : macOS 12+ (Apple silicon) (darwin/arm64)

✅ installed version            tool_version="0.20.0-smoke.1"
✅ doctor reports actionably    7 checks, gaps=none
✅ discover                     26 nodes, 0 edges in 9.97s · workflow_id=workflow
✅ eval produces a number       quality=0.8109 via runtime "reference" in 9.96s
✅ coverage is answerable       7 registered languages
✅ works with no platform       re-ran discover with the endpoints closed: 26 nodes (same as before)
🔵 upgrade is honest offline    cannot reach the release index … only this one needs the network

  7 checks · 0 failed · 1 reported as refusals
  covered: darwin/arm64 only. The other four rows of the matrix need their own runners (D1).
```

This is the check the installer tests cannot make: the tree-sitter frontends are CGO, and a binary that was
built or packaged wrong links fine and then cannot parse. 26 nodes out of 8,000 files of somebody else's
multi-language code is the artifact working, not a download working.

The re-run with the platform endpoints pointed at a closed port returned **the same 26 nodes**, which is the
offline guarantee asserted rather than assumed.

**One harness bug, recorded because the first report was wrong.** The coverage check read `languages`; the field
is `registered_languages`. It reported *0 registered languages* for a command that had answered correctly with
all seven — a reader of that report would have gone hunting for a broken coverage table. The runner now reads
the field the payload actually has.

---

## 2 · What could NOT be run here, and why

Stated plainly, because a matrix that quietly shrinks to what it can run is the false green this document
exists to prevent.

| Not run | Why | Where it IS covered |
|---|---|---|
| `scripts/install.ps1`, executed | No native `pwsh` on this host; the amd64 PowerShell image is **killed (exit 137)** under emulation | `scripts/install_smoke.ps1` on a real `windows-2022` runner — `.github/workflows/install-smoke.yml` |
| A fresh **macOS** machine | This is the maintainer's machine. Every macOS result above is honestly labelled "NOT a clean OS" | the `macos-15` row of the install-smoke workflow (its LibreSSL-only `openssl` makes it the row that matters most). Recorded as `macos-14` when this evidence was taken; that image is deprecated and the row moved. |
| The four non-native build targets | D1 is five runners for five targets; this host is one of them | `release.yml`'s build matrix |
| `gh release` publish + the published-asset verify | Needs a repository token and a Release | `release.yml`'s publish and smoke jobs |
| Homebrew / Scoop / winget **installs** | The tap, bucket and upstream PR do not exist. The manifests are generated and attached; nothing a package manager reads points at them | reported as **undelivered** by the channel contract, on every surface, with the blocker named |
| macOS notarization / Windows Authenticode | No Apple identity, no code-signing certificate | the pipeline steps exist and are gated on the secrets; until then the attestation says **not signed**, and every surface renders that |

---

## 3 · Handed off — what the repository owner must do

### a. Install the release signing key (blocks every publish)

`gh secret list` returned **zero secrets** when P20 landed, so no release could be signed — and an unsigned
manifest is a gate failure on every channel but `dev`. A keypair was generated on 2026-07-29; its **public**
half is committed as trust-root key `heros-release-2026b` (`internal/release/trustroot.go` and
`docs/release/heros-release.pub`), and both install scripts pin it.

The private half was printed once, in the session that generated it, and is **not** in this repository. Install
it as a repository secret named `HEROS_RELEASE_PRIVATE_KEY`. Do that through GitHub's own UI or `gh secret set`
— it is a credential, and it should not pass through anything that keeps a history.

The P11 launch key (`heros-release-2026a`, `1f117664…`) was **removed** rather than kept as an accepted key: it
never had a private half configured and no tag was ever signed with it, so no binary in the field has ever
verified anything against it. Keeping it would have widened the trust root for compatibility with a release
that does not exist.

### b. Rehearse, then release

```sh
git push origin feat/p20-installable-packages       # then open the PR
# after the secret is installed:
git tag v0.20.0-rc.1 && git push origin v0.20.0-rc.1   # publishes a DRAFT Release; the rehearsal
git tag v0.20.0      && git push origin v0.20.0        # publishes GA, non-draft, marked latest
```

The rc tag is the rehearsal task 2.7 asks for and it exercises what this machine cannot: five native runners,
the `gh release` path, and the fresh-machine smoke matrix on five OS images. **Pushing a tag before the secret
exists fails at the signing step on purpose** — that is the fail-closed design, not a bug.

### c. Optional, and independent of each other

- **`HEROS_TAP_TOKEN` + a `heros-foreal/homebrew-tap` and `heros-foreal/scoop-bucket` repository** — flips
  Homebrew and Scoop from *generated* to *delivered*. Until then the `taps` job logs the blocker and the
  channel contract keeps reporting them as unavailable, so no surface promises them.
- **`APPLE_CERT_P12` / `APPLE_SIGNING_IDENTITY` / `APPLE_API_KEY_P8` / `APPLE_TEAM_ID`** and
  **`WINDOWS_CERT_PFX`** — delivers the ratified D3-(A) posture. The moment they exist, the attestation records
  it and the quarantine/SmartScreen instructions disappear from the installer output, the README and the
  console by themselves, because no surface holds a copy of them.

---

## 4 · One finding outside P20's scope — now FIXED

`internal/cli` — documented in its own header as never importing `net/http`, "the offline guarantee made
structural" — had transitively linked it since P13. P20 found it, corrected the false comment, and pinned the
chain as a baseline rather than laundering it. It has since been **fixed**, and the fix is not where the shape of
the problem suggested.

**Where the network actually entered.** `go list` over the whole graph showed `internal/providergateway` was the
*only* direct `net/http` importer among `internal/cli`'s ~90 transitive dependencies, and `internal/telemetry`
was its *only* entry point — one edge, five hops down:

```
internal/cli → authoring/authoringwire → proposal → attribution/diagnosis
             → evalharness/linkage → telemetry → providergateway → net/http
```

Telemetry's production code used exactly three symbols from providergateway: `CallInfo`, `ErrTimeout` and
`Observer` (everything else — `Gateway`, `New`, `StaticSecrets` — was test-only). It imported an HTTP client and
the AWS SDK **to name the struct its observer callback is handed.**

**Why the obvious fix was the wrong one.** Splitting `internal/authoring` (the shape the task suggested) would
not have worked: `authoring.Draft` legitimately needs `proposal`, which needs `attribution`, which needs
`telemetry`. It would also have fixed nothing for the other five packages on that chain. The root was one edge in
a shared package, so that is what was cut.

**The fix.** The observation vocabulary — `Observer`, `CallInfo`, `Usage`, `StopReason`, `ErrTimeout` — moved into
a new leaf package, `internal/providercall`, which has no transport and no in-repo dependencies. Everything that
*makes* a call (request types, adapters, retry loop, credentials) stayed in `providergateway`, because that is
what the network dependency is for. `providergateway` re-exports all five names as **type aliases**, so its API
did not change by one character: an alias is type identity, so an observer written against `providercall.Observer`
still satisfies `providergateway.Observer`, and every `providergateway.CallInfo{…}` call site still compiles.

| Verified | How |
|---|---|
| `internal/cli` links no network stack | `go list -deps ./internal/cli` — no `net/http`, over the whole transitive graph |
| The gate is a ban, not a baseline | `TestCLIPackageLinksNoNetworkStack`, tolerated-importer map **empty** |
| The gate can go red — transitively | re-adding telemetry's import → `internal/cli links net/http, via internal/authoring, internal/authoringwire` |
| The gate can go red — directly | a planted `_ "net/http"` in the package → `via a DIRECT import` |
| The aliases are identity, end to end | `telemetry/instrument_test.go` builds a real `providergateway.New(…WithObserver(inst))` with an `Instrument` whose `OnCall` takes a `providercall.CallInfo`, and passes |
| Nothing else broke | `make go` green; `providergateway`, `telemetry`, `attribution`, `diagnosis`, `proposal`, `authoring`, `authoringwire` all pass |
| The fix cannot be undone quietly | `TestProvidercallLinksNoTransport` fails if the leaf package ever grows a transport or an in-repo import |

The behavioural test (`TestLocalWorkflowRunsWithNetworkingDenied`, a deny-all dialer) still exists and still
passes. The two are not redundant: it stayed green for the entire three phases the structural claim was false,
which is precisely why the structural one had to exist. The `app.go` doc comment now makes the unqualified claim
again, and records how it broke — nobody added a network call; someone imported a package for a type name.
