# Verifying a `heros` CLI release (P11 supply chain)

The `heros` CLI runs **inside your CI with access to your repository**, so a compromised release is a
compromise of every build it runs in. This page is the documented verification step (P11 NFR8, task
6.2): it is runnable with **no account and no network** beyond the download, and it is exercised by an
automated test against a real built artifact (`internal/release`, task 6.4).

## What a release contains

- One **self-contained binary per target** — `heros-<version>-<os>-<arch>` — with no runtime dependency
  to install (task 6.1). Targets: `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`,
  `windows/amd64`.
- `SHA256SUMS` — the checksum manifest, one `sha256  name` line per binary, sorted.
- `SHA256SUMS.sig` — a detached **ed25519 signature** over `SHA256SUMS`, made with the release key.

## Step 1 — checksum (always runnable, no key)

```bash
# from the directory holding the downloaded binaries + SHA256SUMS
sha256sum -c SHA256SUMS        # Linux
shasum -a 256 -c SHA256SUMS    # macOS
```

Every line must say `OK`. A mismatch means the bytes you have are not the bytes we published — stop.

## Step 2 — signature (proves who published it)

The release **public key** is published in this repository at
[`docs/release/heros-release.pub`](heros-release.pub). Verify the manifest's signature with the CLI you
already have (the verifier is built into `herossign`, which ships beside `heros` and uses only the Go
standard library — no extra tool to trust):

```bash
herossign verify \
  --pub "$(cat docs/release/heros-release.pub)" \
  --in  SHA256SUMS \
  --sig SHA256SUMS.sig
# → OK: signature verifies and the release is authentic
```

If Step 1 passes but Step 2 fails, the checksums are internally consistent but were **not signed by the
release key** — treat the release as untrusted.

## Step 3 — reproduce it yourself (optional, strongest)

Builds are reproducible: the same source at the same tag, built on the **same platform** with the same
Go toolchain and C compiler and the flags in [`scripts/release-cli.sh`](../../scripts/release-cli.sh)
(`-trimpath -buildvcs=false -ldflags "-s -w"`), produces **byte-identical** binaries. (The CLI links
CGO tree-sitter frontends, so each target is built on its native runner; reproducibility is per
platform, not cross-platform.) So you can rebuild and compare:

```bash
go build -buildvcs=false -trimpath -ldflags "-s -w -X github.com/heros-foreal/agentd/internal/cli.ToolVersion=<version>" \
  -o heros ./cmd/heros
sha256sum heros   # compare against the matching line in SHA256SUMS
```

The automated test `TestReproducibleBuild` builds `heros` twice and asserts the two hashes match, so a
change that breaks reproducibility fails CI rather than a customer's audit.

## Version support window (task 6.3)

The CLI declares a **platform-contract version** (`runlink.ContractVersion`, currently `p11.link.v1`)
and reports it via `heros version`. The support policy:

- The platform supports CLI releases whose contract version is in its **current window**. At M14 the
  window is the single version `p11.link.v1`.
- A CLI whose contract version is **outside** the window does not silently compute something different:
  on any platform-facing command it **names the required version and refuses** (PRD FR6,
  `cli.CheckContract`; the ingest endpoint answers `426 Upgrade Required` with the required version).
- Local commands (`discover`, `apply`, `eval`, `status`, `version`) are **unaffected** by the window —
  they touch no platform contract and keep working on any release, which is what keeps the free,
  offline tier durable.
- When the contract version changes, the platform supports the **previous** version for a deprecation
  window announced in the release notes before the old version is refused, so a pinned CI does not break
  on our schedule.
