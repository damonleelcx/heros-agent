---
title: Install the CLI
tier: quickstart
summary: Get the heros binary onto macOS, Linux or Windows — with the checksum and the signature checked before it reaches your PATH.
platform_version: 0.20.0
boundary: This page gets the binary onto your machine and proves it is ours. It does not create an account, and it does not send anything anywhere — the CLI runs offline with no account.
generated: true
order: 1
---

This page is **generated** from the published release and the install-channel contract on every build. No filename, version or checksum on it was typed by a person.

## The one command

Version **0.20.0**. The install script downloads the binary, checks its SHA-256 against the release manifest, checks the manifest's signature against a key pinned inside the script, and only then puts anything on your `PATH`. **Any failure is a hard stop** — there is no "continue anyway".

:::tabs
```bash label="macOS and Linux"
curl -fsSL https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.20.0/scripts/install.sh | sh
```
```powershell label="Windows"
irm https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.20.0/scripts/install.ps1 | iex
```
:::

There is deliberately **no shorter unverified variant** on this page. A one-liner that installs first and verifies later is the line everyone copies, and it removes the only control that makes running our binary inside your CI defensible.

Confirm it landed:

```bash
heros version
```

## What your operating system will say

### macOS

The macOS binaries are **not code-signed and not notarized**. This is a deliberate posture, not an oversight, and you will meet it as a dialog — so here it is before you do.

Gatekeeper will refuse to run the binary the first time, with a message about an unidentified developer. Clearing the quarantine attribute is one command:

```bash
xattr -d com.apple.quarantine $(command -v heros)
```

**What accepting this means:** macOS is telling you it cannot confirm who built this binary, and you are telling it to proceed anyway. That is a real statement, and the honest reason to be comfortable with it is not the dialog — it is that you verified the SHA-256 against a signed manifest, which is a stronger check than an Apple certificate proving a paid developer account exists.

### Windows

The Windows binaries are **not code-signed and not notarized**. This is a deliberate posture, not an oversight, and you will meet it as a dialog — so here it is before you do.

SmartScreen will show a blue "Windows protected your PC" panel the first time. **More info → Run anyway** proceeds.

**What accepting this means:** Windows cannot attribute the binary to a certificate holder. As on macOS, the check that actually establishes provenance here is the signed manifest the install script verified before the file reached your `PATH`.

In both cases the release **manifest is signed** with key `heros-release-2026c`, and that signature is what the install script and `heros verify-release` check. OS code signing and manifest signing answer different questions; this release answers the second one.

## Verify a download yourself

Every file below is listed in `SHA256SUMS`, the manifest the release signs. These checksums are read from that file at build time — they are not transcribed.

| File | For | Size | SHA-256 |
|---|---|---|---|
| `heros-0.20.0-darwin-amd64` | macOS · Intel | 17.2 MB | `9e0ff26a1c4f5394242cce0bd920a0ed8b200b6dd637f63d1cff4439aebab47f` |
| `heros-0.20.0-darwin-arm64` | macOS · Apple silicon | 16.7 MB | `e7bac31c6d4585bdf1309c8520f08c6131776ca90971e88b8ee5ea9b32d17034` |
| `heros-0.20.0-linux-amd64` | Linux · x86-64 | 17.2 MB | `b7ff4cec5e31d6ac1c556600a44aa4f42fe44411d6f45ef8aee760bc733fc49a` |
| `heros-0.20.0-linux-arm64` | Linux · arm64 | 16.5 MB | `6a3377edcabd36507025a22af2d7216ea11b5eb13abf2fce0d9b7d079872f5b8` |
| `heros-0.20.0-windows-amd64.exe` | Windows · x86-64 | 17.4 MB | `4fa08f0bf40e55f4ae96586964540f05a8bb7479d687188e80ec45c47ec818c1` |
| `install.ps1` | install script | 0.0 MB | `b0b2b1d0bb0b5aefefdc55b3e1da32674f18077ed8a49dfb27598e38ce186464` |
| `install.sh` | install script | 0.0 MB | `1a55a6334aa891410593841007f65a9ca985f5bb8538fe33dbaab65ea86aa3d7` |

Check one by hand:

```bash
curl -fsSLO https://github.com/${OWNER}/${REPO}/releases/download/v0.20.0/SHA256SUMS
shasum -a 256 -c SHA256SUMS --ignore-missing
```

The manifest's own signature ships as `SHA256SUMS.sig` and `SHA256SUMS.sshsig`. `heros verify-release` checks the checksums **and then** the signature, against a key compiled into the binary — so verification needs no network and no account:

```bash
heros verify-release --manifest SHA256SUMS --sig SHA256SUMS.sig
```

### Files this release publishes but does not cover

These are attached to release v0.20.0 but are **not listed in `SHA256SUMS`**, so the signed manifest does not cover them and neither this page nor `heros verify-release` can establish that they are ours:

| File | For |
|---|---|
| `heros_0.20.0_amd64.deb` | Debian/Ubuntu package · x86-64 |
| `heros_0.20.0_arm64.deb` | Debian/Ubuntu package · arm64 |
| `heros-0.20.0-1.aarch64.rpm` | RPM package · aarch64 |
| `heros-0.20.0-1.x86_64.rpm` | RPM package · x86-64 |
| `heros-packaging-0.20.0.tar.gz` | — |

They are listed rather than hidden, and they are kept out of the table above rather than mixed into it: a download with no checksum in the signed manifest sitting next to ones that have them would imply a verification it cannot offer. **If you need a verified artifact, use one from the table above.**

## Channels, pinning, upgrading and removing

An install you cannot pin is an install you cannot reproduce, so every channel states how to install an exact version. Upgrade and uninstall are given in **each channel's own idiom** — where a package manager owns the file, `heros upgrade` defers to it and prints that manager's command rather than overwriting a file the manager is tracking.

### curl \| sh

macOS, Linux. Installed directly; nothing else owns the file.

```bash
# install
curl -fsSL https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.20.0/scripts/install.sh | sh
# pin an exact version
curl -fsSL .../install.sh | HEROS_VERSION=0.20.0 sh
# upgrade
heros upgrade
# remove
curl -fsSL https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.20.0/scripts/install.sh | HEROS_UNINSTALL=1 sh
```

**How this channel establishes the bytes are ours:** the script verifies the sha256 against the release manifest and the manifest's ed25519 signature against a key pinned in the script, before placing anything on PATH; any failure is a hard stop.

### PowerShell (irm \| iex)

Windows. Installed directly; nothing else owns the file.

```bash
# install
irm https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.20.0/scripts/install.ps1 | iex
# pin an exact version
$env:HEROS_VERSION='0.20.0'; irm .../install.ps1 | iex
# upgrade
heros upgrade
# remove
$env:HEROS_UNINSTALL=1; irm .../install.ps1 | iex
```

**How this channel establishes the bytes are ours:** the script verifies the sha256 and then the ed25519 signature against a pinned key, before adding anything to PATH; any failure is a hard stop.

### .deb package

Linux. A package manager owns the installed file.

```bash
# install
curl -fsSLO https://github.com/damonleelcx/heros-agent/releases/download/v0.20.0/heros_0.20.0_amd64.deb && sudo dpkg -i heros_0.20.0_amd64.deb
# pin an exact version
download heros_0.20.0_amd64.deb for the exact version from that release and dpkg -i it
# upgrade
sudo dpkg -i heros_0.20.0_amd64.deb   (from the newer release)
# remove
sudo dpkg -r heros
```

**How this channel establishes the bytes are ours:** the package's own sha256 is NOT in the signed release manifest (the manifest is signed before packaging runs). What the manifest covers is the binary INSIDE it, byte for byte: verify the heros-VERSION-linux-ARCH asset against SHA256SUMS, then confirm the installed /usr/bin/heros matches it.

For release v0.20.0 those uncovered files are `heros_0.20.0_amd64.deb`, `heros_0.20.0_arm64.deb`.

### .rpm package

Linux. A package manager owns the installed file.

```bash
# install
sudo rpm -i https://github.com/damonleelcx/heros-agent/releases/download/v0.20.0/heros-0.20.0-1.x86_64.rpm
# pin an exact version
rpm -i the exact version's package URL, which ends in heros-0.20.0-1.x86_64.rpm
# upgrade
sudo rpm -U heros-0.20.0-1.x86_64.rpm   (from the newer release)
# remove
sudo rpm -e heros
```

**How this channel establishes the bytes are ours:** the package's own sha256 is NOT in the signed release manifest (the manifest is signed before packaging runs). What the manifest covers is the binary INSIDE it, byte for byte: verify the heros-VERSION-linux-ARCH asset against SHA256SUMS, then confirm the installed /usr/bin/heros matches it.

For release v0.20.0 those uncovered files are `heros-0.20.0-1.aarch64.rpm`, `heros-0.20.0-1.x86_64.rpm`.

### container image

macOS, Linux, Windows. A package manager owns the installed file.

```bash
# install
docker run --rm -v "$PWD:/repo" ghcr.io/damonleelcx/heros:0.20.0 discover --repo /repo
# pin an exact version
docker run --rm ghcr.io/damonleelcx/heros:0.20.0   (or @sha256:… for a digest pin)
# upgrade
docker pull ghcr.io/damonleelcx/heros:<newer>
# remove
docker rmi ghcr.io/damonleelcx/heros:0.20.0
```

**How this channel establishes the bytes are ours:** the image is digest-pinnable and is built in the same run from the same verified binary; pull by digest to pin exactly what you audited.

### What removal leaves behind

Uninstalling removes the binary. It does **not** remove a `.heros.json` in a repository you configured, or an `llm-eval.yaml` you wrote — those are your files, in your repositories, and a package manager deleting them would be a surprise nobody wants. If you signed in, a stored platform token remains in your user configuration directory until you delete it.

## Channels that are not available yet

Each of these has its manifest generated and attached to every release. None of them is installable today, and each says exactly what is missing — a channel listed as unavailable with no reason is indistinguishable from one nobody thought about.

| Channel | For | What is missing |
|---|---|---|
| Homebrew | macOS, Linux | the formula is generated from the signed manifest and attached to every Release, but the tap repository heros-foreal/homebrew-tap does not exist yet and pushing to it needs a token secret. Until then `that channel's install command` would fail |
| Scoop | Windows | the manifest is generated and attached to every Release, but the bucket repository heros-foreal/scoop-bucket does not exist yet and pushing to it needs a token secret |
| winget | Windows | the three-file winget manifest is generated and attached to every Release, but publication is a pull request into microsoft/winget-pkgs whose review and merge are not ours to schedule |

Their commands are deliberately not printed here. A command that does not work yet is worse than an absence.

## Installing on a disconnected machine

Verification happens **on the disconnected machine**, not on the one that had the network. No step below needs the internet or an account.

On a connected machine, fetch four things: the binary for the target platform, the manifest, its signature, and the `heros` binary you already trust.

```bash
curl -fsSLO https://github.com/${OWNER}/${REPO}/releases/download/v0.20.0/heros-0.20.0-linux-amd64
curl -fsSLO https://github.com/${OWNER}/${REPO}/releases/download/v0.20.0/SHA256SUMS
curl -fsSLO https://github.com/${OWNER}/${REPO}/releases/download/v0.20.0/SHA256SUMS.sig
```

Transfer all of them. Then, on the disconnected machine — before the binary goes anywhere near `PATH`:

```bash
shasum -a 256 -c SHA256SUMS --ignore-missing
heros verify-release --manifest SHA256SUMS --sig SHA256SUMS.sig
install -m 0755 heros-0.20.0-linux-amd64 /usr/local/bin/heros
```

The release key is **compiled into the `heros` binary**, which is what makes the signature check work with no network and no keyserver. That is also why the ordering matters: you verify with a binary you already trust, then install the new one.

## Next: your first discovery graph

You have the binary and you have proved it is ours. There is **no config file to edit** between here and a result — the next command runs against a repository you already have:

```bash
heros discover --repo . --out ir.json --report discovery.json
```

The [quickstart](/docs/start/quickstart) walks through what it produces and how to read it.

