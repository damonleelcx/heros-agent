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
# The ldflags come from ONE place. In CI, `herosdist plan` computes them from the tag and passes them in
# through HEROS_LDFLAGS, so the version stamped into the binary and the version in every generated package
# manifest have a single source (P20 task 2.4). The fallback below is the same string for a local build;
# TestReleaseScriptStampsTheOneVersionVariable holds the two forms together.
LDFLAGS="${HEROS_LDFLAGS:--s -w -X github.com/heros-foreal/agentd/internal/cli.ToolVersion=${VERSION}}"

# The native target for this runner. GOOS/GOARCH default to the host; override to build a matched
# cross-target only when a CGO cross-toolchain is configured.
GOOS_T="$(go env GOOS)"; GOARCH_T="$(go env GOARCH)"
ext=""; [ "${GOOS_T}" = "windows" ] && ext=".exe"
name="heros-${VERSION}-${GOOS_T}-${GOARCH_T}${ext}"

# 🔴 The macOS floor is SET here, not inherited from whatever runner image happened to build the release.
# Without this, clang stamps LC_BUILD_VERSION minos with the BUILD HOST's OS version, so the binary refuses
# to launch on anything older than the runner — and the matrix's "macOS 12+" claim silently becomes
# "macOS <whatever GitHub last upgraded us to>+". That is not a hypothetical: it is why moving off the
# retired macos-13 image had to change more than a label. Target is stated once and asserted after the
# build; distribution.MacOSFloor is the same number on the Go side.
if [ "${GOOS_T}" = "darwin" ]; then
  export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-12.0}"
  echo "release-cli: macOS deployment target pinned to ${MACOSX_DEPLOYMENT_TARGET}"
fi

echo "release-cli: building heros ${VERSION} for native ${GOOS_T}/${GOARCH_T} → ${OUT}/${name}"
mkdir -p "${OUT}"
go build -buildvcs=false -trimpath -ldflags "${LDFLAGS}" -o "${OUT}/${name}" "${PKG}"

# Assert the floor we just claimed. `otool -l` reports what the linker actually recorded, which is the only
# copy of this number a user's machine will ever consult — an exported variable that clang ignored would
# otherwise pass every other check in this pipeline and fail on the customer's Mac.
if [ "${GOOS_T}" = "darwin" ]; then
  minos="$(otool -l "${OUT}/${name}" | awk '/LC_BUILD_VERSION/{f=1} f&&/^ *minos/{print $2; exit}')"
  if [ "${minos}" != "${MACOSX_DEPLOYMENT_TARGET}" ]; then
    echo "release-cli: FATAL: built binary declares minos ${minos:-<none>}, not ${MACOSX_DEPLOYMENT_TARGET}." >&2
    echo "release-cli: shipping it would claim a macOS floor the binary does not honour." >&2
    exit 1
  fi
  echo "release-cli: verified macOS floor: minos ${minos}"
fi

# The checksum manifest, sorted so it is itself reproducible.
( cd "${OUT}" && shasum -a 256 heros-* 2>/dev/null | sort -k2 > SHA256SUMS || sha256sum heros-* | sort -k2 > SHA256SUMS )
echo "release-cli: wrote ${OUT}/SHA256SUMS"

# Sign the manifest if a key is available. A real release sets HEROS_RELEASE_PRIVATE_KEY from a secret;
# without one, the manifest is still checksum-verifiable, just unsigned.
if [ -n "${HEROS_RELEASE_PRIVATE_KEY:-}" ]; then
  # Captured through a command substitution, NOT a `>` redirect: a redirect creates the .sig file before
  # the signer runs, so a failed signing would leave a zero-byte signature that every later step reads as
  # "a signature is present". With `set -e`, a failing substitution stops the script and no file appears.
  sig="$(go run ./cmd/herossign sign --in "${OUT}/SHA256SUMS")"
  printf '%s\n' "${sig}" > "${OUT}/SHA256SUMS.sig"
  # The SAME signature in OpenSSH sshsig form. It exists because the installer must verify before placing a
  # binary on PATH and cannot use the binary it just downloaded to do it — and stock macOS ships LibreSSL,
  # which cannot verify ed25519, while `ssh-keygen -Y verify` is present wherever openssh-client is.
  sshsig="$(go run ./cmd/herossign sign --ssh --in "${OUT}/SHA256SUMS")"
  printf '%s' "${sshsig}" > "${OUT}/SHA256SUMS.sshsig"
  go run ./cmd/herossign signers > "${OUT}/allowed_signers"
  echo "release-cli: signed ${OUT}/SHA256SUMS → SHA256SUMS.sig + SHA256SUMS.sshsig (+ allowed_signers)"
else
  echo "release-cli: HEROS_RELEASE_PRIVATE_KEY unset — manifest is checksum-verifiable but UNSIGNED"
fi

echo "release-cli: done. Verify with docs/release/cli-verification.md"
