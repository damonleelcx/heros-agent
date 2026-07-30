## Install

`heros` is a single self-contained binary. It needs no account and no network for `discover`, `apply`, `eval`, `doctor` or `init` — see the install runbook for the one-command install per OS, and `docs/release/install.md` for the offline verification steps.

## Trust posture

Trust posture — heros 0.20.0-rc.1

✅ The checksum manifest is signed with the heros release key (ed25519). Verify it offline, with no account.
⛔ The macOS binaries carry no Apple code signature.
⛔ The macOS binaries are NOT notarized. macOS quarantines internet downloads: clear the flag with the one command the installer prints, or install with Homebrew, which is not quarantined.
⛔ The Windows binary is NOT Authenticode-signed. SmartScreen warns on first run: More info → Run anyway. The publisher metadata on the package states who built it.

Manifest signed by release key heros-release-2026b.

### Verify this release yourself (offline, no account)

```sh
# 1. the download is intact
sha256sum -c SHA256SUMS        # or: shasum -a 256 -c SHA256SUMS
# 2. the manifest came from the holder of the heros release key
herossign verify --pub "$(cat docs/release/heros-release.pub)" \
  --in SHA256SUMS --sig SHA256SUMS.sig
```

Both steps run with no network beyond this download and no account. Every install channel performs them for you and refuses to place the binary on your PATH if either fails.

## Artifacts

- `heros-0.20.0-rc.1-darwin-amd64`
- `heros-0.20.0-rc.1-darwin-arm64`
- `heros-0.20.0-rc.1-linux-amd64`
- `heros-0.20.0-rc.1-linux-arm64`
- `heros-0.20.0-rc.1-windows-amd64.exe`

Container image:

- `ghcr.io/heros-foreal/heros:0.20.0-rc.1`

## Not in this release

These are stated because a missing row reads as *should work*:

- **Windows 11 (arm64)** — not built — no native windows/arm64 runner in the matrix, and the CGO tree-sitter frontends make a cross-build a different, less-tested artifact (D1). Instead: run the windows/amd64 build under Windows' x64 emulation, or ask for the row: adding it is a new runner, not a redesign.
- **Alpine / any musl Linux** — no native musl binary — the CLI links CGO tree-sitter frontends against glibc, and a glibc binary does not run on musl (D6). Instead: use the container image ghcr.io/heros-foreal/heros:<version>, which carries the same CLI in a glibc base.
