# Installing `heros`, and deciding whether to trust it

This is the install and trust runbook, written as the story of one afternoon rather than as a reference. If you
want the reference, [`cli-verification.md`](cli-verification.md) is the three verification steps on their own,
and the README's install section is the one command per channel.

Everything here is free and needs no account.

---

## Scene 1 · An engineer wants to try it, and has twenty minutes

Priya has a Python agent repository and a colleague who mentioned `heros`. She wants a number on screen before
she commits to reading anything.

```sh
curl -fsSL https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.21.0/scripts/install.sh | sh
```

What she sees — captured verbatim from this command on darwin/arm64, 2026-08-03, against the published
v0.21.0 release, and not edited afterwards:

```
heros install: target darwin/arm64
heros install: latest version is 0.21.0
heros install: downloading heros-0.21.0-darwin-arm64
heros install: ✓ checksum matches the release manifest
heros install: ✓ signature verified against the pinned heros release key (ssh-keygen)
heros install: ✓ installed /Users/damon/.local/bin/heros

heros install: next: cd into a repository and run
    heros doctor      # check this machine is ready
    heros discover    # find the agent workflow in your code
```

This capture is from **v0.21.0**, and it is left exactly as it was captured. The release after it draws the
Heros mark between the signature line and the install line; that is not spliced in here, because a transcript
labelled *verbatim* stops being worth anything the first time it shows output no published version produced.
It is re-captured when the next release publishes.

That last path is worth reading rather than skipping. The installer places the binary in the first
**writable** entry of `/usr/local/bin`, then `~/.local/bin` — and on the machine this was captured on,
`/usr/local/bin` is `root:wheel` and not writable, so it chose the home directory and said so. It did not
ask for `sudo` and it did not fail. If `~/.local/bin` is not on your PATH, that line is the one that tells
you, which is why it prints the absolute path it used instead of a reassuring "installed successfully".

Two things happened that she did not ask for. The download was checked against the release's checksum manifest,
and that manifest was checked against a public key **pinned inside the script she just piped**. She did not run
a verify step, and she did not skip one either. If either check had failed, nothing would have been placed on
her PATH and the script would have said which check failed and what to do about it.

Then:

```sh
cd ~/work/my-agent
heros doctor
heros discover
heros eval --seeds 5
```

`discover` reads her source and prints what it found. `eval` scores it. No key, no account, no network: the
evaluation runs a deterministic reference runtime, which `doctor` states plainly — the scores compare variants
against each other, and they are **not** a measurement of a live model. That distinction is on screen rather
than in a footnote, because the alternative is Priya believing a number that does not mean what she thinks.

### If `doctor` is not happy

It reports; it never blocks. Each gap names one action:

```
  ⛔ toolchain: go          go is missing or unusable: exec: "go": executable file not found in $PATH
     → install go: brew install go
```

That check runs the real verification toolchain and asks it to answer for itself — it does not merely look for
a file on PATH. On stock macOS `/usr/bin/javac` exists, is executable, and prints "Unable to locate a Java
Runtime": a PATH check passes and the later verification fails, and the failure would then be reported as *your
code does not compile*. Asking the tool to speak converts that whole class of broken install into the operations
problem it always was.

---

## Scene 2 · Her security reviewer wants to know what he is approving

Sam does not install things because a script said it verified them. He wants to do it himself, offline.

```sh
mkdir heros-review && cd heros-review
gh release download v0.21.0            # or download the assets by hand

# 1. the downloads are intact
sha256sum -c SHA256SUMS                # or: shasum -a 256 -c SHA256SUMS

# 2. the manifest came from the holder of the heros release key
ssh-keygen -Y verify -f allowed_signers -I heros-release \
  -n file -s SHA256SUMS.sshsig < SHA256SUMS
```

Neither command needs a network or an account. `allowed_signers` ships with the release; the same key is
published as [`heros-release.pub`](heros-release.pub) in raw hex for the `herossign verify` path, and the two
are held identical by a test — a drift between them would mean the documented check verifies against a key the
binary rejects.

**Why `ssh-keygen` and not `openssl`.** Stock macOS ships LibreSSL 3.3.6, which cannot verify ed25519 at all,
and a minimal `ubuntu:22.04` image has no `openssl` binary. An installer that required OpenSSL 3 would silently
have no verifier on the primary developer platform. So the same ed25519 signature over the same bytes is
published in two encodings: the raw `.sig` for `herossign` and `heros upgrade`, and the `.sshsig` for the tool
every machine already has. If you have OpenSSL 3, the installer will use it and say so.

If Sam already has a `heros` he trusts, one command does both checks with the routine the installer itself uses:

```sh
heros verify-release --manifest SHA256SUMS
# → heros verify-release: ✅ verified 7 of 7 listed artifacts (heros-0.21.0-darwin-amd64,
#   heros-0.21.0-darwin-arm64, heros-0.21.0-linux-amd64, heros-0.21.0-linux-arm64,
#   heros-0.21.0-windows-amd64.exe, install.ps1, install.sh) — manifest signed by release
#   key heros-release-2026c
```

Seven, not five: the manifest covers the two install scripts as well as the five binaries, which is what
makes the `curl … | sh` line auditable against the same signature as the thing it downloads. This line
previously read "5 of 5" — an example written from the shape of the release rather than from a run of it.
The `ssh-keygen` path above was executed against the same download and answers
`Good "file" signature for heros-release with ED25519 key SHA256:…`. The fingerprint it prints is not
reproduced here on purpose: it is public, but it is high-entropy base64, and pasting it into a document
is how a `generic-api-key` finding gets allowlisted — after which the allowlist is what hides the next
real one. Compare it against your own `ssh-keygen -lf` of [`heros-release.pub`](heros-release.pub).

He can go further and rebuild it: see
[Step 3 — reproduce it yourself](cli-verification.md#step-3--reproduce-it-yourself-optional-strongest). Builds
are reproducible per platform on a fixed toolchain, so a third party can confirm the bytes rather than trust
them. One honest caveat: a release whose binaries carry an **OS code signature** is not byte-reproducible,
because a signature embeds a trusted timestamp. The reproducibility claim covers the pre-signing build, which is
what `TestReproducibleBuild` checks, and the release notes say so rather than implying more.

### What Sam will ask about next: the first-run warning

Read the release notes' **Trust posture** block. It is generated from the release's own attestation, so it
states exactly what that build carries and — just as explicitly — what it does not. A release that is not
notarized says so, and prints the one command to clear the quarantine flag. A release that *is* notarized says
so, and the installer stops printing the workaround. Nothing in the notes is written by hand, which is why they
cannot describe last release's posture.

---

## Scene 3 · Six weeks later: upgrading, and going back

```sh
heros upgrade
```

It fetches the latest release, verifies it with the same routine as the installer, and replaces the binary only
if that passes. Four things it will refuse to do:

1. **Execute anything before verifying it.** The download is verified as bytes; it is never run to ask its
   version.
2. **Reinstall the version you already have.** `Already current. Nothing downloaded, nothing changed.`
3. **Overwrite a file your package manager owns.** If you installed with `brew` or `dpkg`, it prints that
   manager's upgrade command and stops — replacing the file would corrupt the manager's state, and its next
   upgrade would silently revert you. It decides this from the install path *before* touching the network.
4. **Walk backwards.** An older release is a legitimately signed artifact, so a signature cannot tell an upgrade
   from someone serving you the version with the bug. If the index offers an older tag, `upgrade` refuses and
   names the explicit path instead.

**Rolling back is installing a specific version**, not an in-place downgrade:

```sh
curl -fsSL .../install.sh | HEROS_VERSION=0.19.4 sh   # curl | sh
brew install heros-foreal/tap/heros@0.19.4            # Homebrew
scoop install heros@0.19.4                            # Scoop
docker run --rm ghcr.io/heros-foreal/heros:0.19.4      # container (or @sha256:… to pin a digest)
```

Every channel can also uninstall itself by its own idiom — `HEROS_UNINSTALL=1`, `brew uninstall heros`,
`dpkg -r heros`, `scoop uninstall heros`. The console's install surface lists all four commands per channel, and
so does `CHANNELS.md` in each release.

---

## Scene 4 · The platform you are on is not built

Two rows are deliberately not built, and they are **rows**, not blanks:

| Platform | Why not | What to do |
|---|---|---|
| Windows 11 arm64 | No native `windows/arm64` runner in the release matrix, and the CGO tree-sitter frontends make a cross-build a different, less-tested artifact | Run the `windows/amd64` build under Windows' x64 emulation. `install.ps1` does this for you and says so |
| Alpine / any musl Linux | The CLI links CGO tree-sitter frontends against glibc, and a glibc binary does not run on musl | `docker run --rm -v "$PWD:/repo" ghcr.io/heros-foreal/heros:<version> discover --repo /repo` |

`install.sh` detects musl and refuses with that answer rather than installing a binary that would die on first
use with a loader error. The full matrix — every platform, the native runner that builds each one, and the
channels that serve it — is on the console's install surface and in the README, generated from one table so the
two cannot disagree.

---

## The release key, and what happens when it changes

The signing key exists only as a CI secret. It is never in this repository, in a log, or in an artifact.

The **public** key is a committed trust root, in two places held identical by a test: compiled into the binary
(`internal/release/trustroot.go`) and readable at [`heros-release.pub`](heros-release.pub). A downloaded key
would prove nothing — whoever can serve you a binary can serve you a key — which is why the installers pin
theirs rather than fetching it.

The trust root is a **set** of keys with roles, exactly one `active` (signs) and any number of `accepted`
(verify only). That is what makes rotation safe rather than a flag day:

1. the next public key is published as `accepted`;
2. one more release is signed with the **old** key — every binary in the field now trusts both;
3. the roles flip: the new key signs, the old one keeps verifying;
4. after an overlap of one minor version, the old entry is deleted.

Only step 4 breaks anything, and only for a binary older than step 2. A **compromise** skips the overlap: the
leaked key is deleted in the same commit that adds its replacement, and the release notes say so — a deliberate,
announced break, which is narrower than the alternative.

Current trust root: `heros-release-2026c`, active since 2026-07-30.

It replaced `heros-release-2026b` under the **compromise** path described just above, and the details are stated
rather than glossed: 2026b's private half was found in cleartext in a local tool transcript, because
`herossign keygen` prints both halves to stdout and that output had been captured to a log. No overlap window was
offered, and none was needed — 2026b signed the `v0.20.0-rc.4` rehearsal and nothing else. That release was a
draft, whose assets are reachable only through the authenticated API, so no installed binary has ever verified
anything against 2026b. Deleting it in the same commit that added 2026c cost nobody a working install.

The P11 launch key `heros-release-2026a` was removed on the same reasoning: it never signed a published release
either. In both cases keeping the old key as accepted would have widened the set of keys that can produce a
trusted release, in exchange for compatibility with a release that does not exist.

---

## What is free, and what is not

The CLI is free with no account, forever: `discover`, `apply`, `eval`, `coverage`, `doctor`, `init`, `version`
and `upgrade` all run locally and send no telemetry. There is no update check on any ordinary command — the
release index is contacted only when you type `upgrade`, so there is nothing to opt out of.

The paid upgrade is the hosted platform: `heros login` and `heros link` push a run's allowlisted metrics and
structure — never source, prompts, or provider keys — to a tenant, which is what buys the console. Nothing in
the free path is degraded to sell it, and no local command starts requiring an account later.

---

## Related

- [`cli-verification.md`](cli-verification.md) — the three verification steps as a reference, including the
  reproduce-it-yourself path and the contract-version support window.
- [`p20-evidence.md`](p20-evidence.md) — what was actually executed to justify the claims on this page, and what
  was not.
- `openspec/changes/p20-installable-packages/design.md` — why each of these decisions is the way it is, and what
  the alternatives cost.
