# Install channels — heros 0.20.0-rc.1

## ✅ Available now

### curl | sh (darwin, linux)

```sh
curl -fsSL https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.20.0-rc.1/scripts/install.sh | sh
```

- verification: the script verifies the sha256 against the release manifest and the manifest's ed25519 signature against a key pinned in the script, before placing anything on PATH; any failure is a hard stop
- upgrade: `heros upgrade`
- uninstall: `curl -fsSL https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.20.0-rc.1/scripts/install.sh | HEROS_UNINSTALL=1 sh`
- install a specific version: `curl -fsSL .../install.sh | HEROS_VERSION=0.20.0-rc.1 sh`

### PowerShell (irm | iex) (windows)

```sh
irm https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.20.0-rc.1/scripts/install.ps1 | iex
```

- verification: the script verifies the sha256 and then the ed25519 signature against a pinned key, before adding anything to PATH; any failure is a hard stop
- upgrade: `heros upgrade`
- uninstall: `$env:HEROS_UNINSTALL=1; irm .../install.ps1 | iex`
- install a specific version: `$env:HEROS_VERSION='0.20.0-rc.1'; irm .../install.ps1 | iex`

### .deb package (linux)

```sh
curl -fsSLO https://github.com/damonleelcx/heros-agent/releases/download/v0.20.0-rc.1/heros_0.20.0-rc.1_amd64.deb && sudo dpkg -i heros_0.20.0-rc.1_amd64.deb
```

- verification: the package's sha256 is listed in the signed release manifest; verify it with the documented two steps before installing, since dpkg has no signature to check without a hosted repo
- upgrade: `sudo dpkg -i heros_<newer>_amd64.deb`
- uninstall: `sudo dpkg -r heros`
- install a specific version: `download the .deb for the exact version from that release and dpkg -i it`

### .rpm package (linux)

```sh
sudo rpm -i https://github.com/damonleelcx/heros-agent/releases/download/v0.20.0-rc.1/heros-0.20.0-rc.1.x86_64.rpm
```

- verification: the package's sha256 is listed in the signed release manifest; verify it with the documented two steps before installing, since rpm has no signature to check without a hosted repo
- upgrade: `sudo rpm -U heros-<newer>.x86_64.rpm`
- uninstall: `sudo rpm -e heros`
- install a specific version: `rpm -i the exact version's package URL`

### container image (darwin, linux, windows)

```sh
docker run --rm -v "$PWD:/repo" ghcr.io/heros-foreal/heros:0.20.0-rc.1 discover --repo /repo
```

- verification: the image is digest-pinnable and is built in the same run from the same verified binary; pull by digest to pin exactly what you audited
- upgrade: `docker pull ghcr.io/heros-foreal/heros:<newer>`
- uninstall: `docker rmi ghcr.io/heros-foreal/heros:0.20.0-rc.1`
- install a specific version: `docker run --rm ghcr.io/heros-foreal/heros:0.20.0-rc.1   (or @sha256:… for a digest pin)`

## ⛔ Generated but not yet publishable

The manifests below are generated from this release's signed manifest and attached to it, so they are correct — but nothing a package manager reads points at them yet. They are listed rather than omitted because an absent channel reads as *not supported*, and that is not what is true here.

- **Homebrew** — the formula is generated from the signed manifest and attached to every Release, but the tap repository heros-foreal/homebrew-tap does not exist yet and pushing to it needs a token secret. Until then `brew install heros-foreal/tap/heros` would fail
- **Scoop** — the manifest is generated and attached to every Release, but the bucket repository heros-foreal/scoop-bucket does not exist yet and pushing to it needs a token secret
- **winget** — the three-file winget manifest is generated and attached to every Release, but publication is a pull request into microsoft/winget-pkgs whose review and merge are not ours to schedule

## Trust posture

Trust posture — heros 0.20.0-rc.1

✅ The checksum manifest is signed with the heros release key (ed25519). Verify it offline, with no account.
⛔ The macOS binaries carry no Apple code signature.
⛔ The macOS binaries are NOT notarized. macOS quarantines internet downloads: clear the flag with the one command the installer prints, or install with Homebrew, which is not quarantined.
⛔ The Windows binary is NOT Authenticode-signed. SmartScreen warns on first run: More info → Run anyway. Publisher metadata is declared where a package can carry it — the winget manifest and the .deb/.rpm metadata name Heros Foreal — but the bare .exe carries none of its own, because on Windows the Authenticode signature IS the publisher declaration. Its file properties will show no publisher.

Manifest signed by release key heros-release-2026b.

