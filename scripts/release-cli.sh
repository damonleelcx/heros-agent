#!/usr/bin/env bash
# release-cli.sh builds the `heros` CLI reproducibly for every target, writes a SHA256SUMS manifest, and
# (when a signing key is present) signs it. It is the P11 supply-chain pipeline (tasks 6.1–6.2).
#
# The CLI links CGO tree-sitter language frontends (Python, etc.), so it is built WITH CGO on each
# target's NATIVE runner — not cross-compiled CGO-free. This script builds the ONE native target for the
# machine it runs on; the release CI matrix runs it once per OS/arch runner and merges the artifacts
# into one SHA256SUMS. Building native per runner is the standard, honest way to ship a CGO binary.
#
# Why each flag matters for REPRODUCIBILITY (NFR8), on a fixed toolchain + C compiler:
#   -trimpath       strips local filesystem paths from the binary, so two machines produce the same bytes
#   -buildvcs=false keeps VCS stamping (commit, dirty flag) out of the binary — otherwise every build differs
#   -ldflags -s -w  strips the symbol table and DWARF — smaller, and removes a source of build variance
#   -X ...ToolVersion  stamps the release version without a code edit
# The binary is self-contained (task 6.1): no heros runtime dependency to install; it links only the
# platform's own libc, which is always present on its target.
#
# A customer verifies with docs/release/cli-verification.md: sha256sum -c SHA256SUMS, then
# `herossign verify`. Both steps are runnable with no account and no network.
set -euo pipefail

VERSION="${1:-0.11.0-dev}"
OUT="${OUT:-dist}"
PKG="./cmd/heros"
LDFLAGS="-s -w -X github.com/heros-foreal/agentd/internal/cli.ToolVersion=${VERSION}"

# The native target for this runner. GOOS/GOARCH default to the host; override to build a matched
# cross-target only when a CGO cross-toolchain is configured.
GOOS_T="$(go env GOOS)"; GOARCH_T="$(go env GOARCH)"
ext=""; [ "${GOOS_T}" = "windows" ] && ext=".exe"
name="heros-${VERSION}-${GOOS_T}-${GOARCH_T}${ext}"

echo "release-cli: building heros ${VERSION} for native ${GOOS_T}/${GOARCH_T} → ${OUT}/${name}"
mkdir -p "${OUT}"
go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o "${OUT}/${name}" "${PKG}"

# The checksum manifest, sorted so it is itself reproducible.
( cd "${OUT}" && shasum -a 256 heros-* 2>/dev/null | sort -k2 > SHA256SUMS || sha256sum heros-* | sort -k2 > SHA256SUMS )
echo "release-cli: wrote ${OUT}/SHA256SUMS"

# Sign the manifest if a key is available. A real release sets HEROS_RELEASE_PRIVATE_KEY from a secret;
# without one, the manifest is still checksum-verifiable, just unsigned.
if [ -n "${HEROS_RELEASE_PRIVATE_KEY:-}" ]; then
  go run ./cmd/herossign sign --in "${OUT}/SHA256SUMS" > "${OUT}/SHA256SUMS.sig"
  echo "release-cli: signed ${OUT}/SHA256SUMS → ${OUT}/SHA256SUMS.sig"
else
  echo "release-cli: HEROS_RELEASE_PRIVATE_KEY unset — manifest is checksum-verifiable but UNSIGNED"
fi

echo "release-cli: done. Verify with docs/release/cli-verification.md"
