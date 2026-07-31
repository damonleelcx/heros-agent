#!/usr/bin/env bash
# release-rehearse.sh runs the release pipeline's whole spine on this machine, against a tag that does not
# have to exist, with a throwaway signing key (P20 tasks 2.6, 2.7).
#
# # Why a local rehearsal exists at all
#
# The pipeline's gates only fire on a `v*` tag pushed to the default branch. Without this script, every
# mistake in them is debugged one public tag at a time — and a release you push to find out whether the
# release works is the definition of a manual step. So the spine runs here:
#
#   plan → build (this host's native target) → per-runner bundles → merge → sign → attest → gate → notes
#
# What it faithfully rehearses: tag parsing and refusal, the native build and its version stamp, the
# per-runner bundle shape, the merge's recompute-and-cross-check, ed25519 signing, the attestation, every
# fail-closed gate, and the generated release notes.
#
# What it CANNOT rehearse, and does not pretend to: the four targets this machine is not native for (that is
# D1 — five runners, five hosts), and the `gh release` calls, which need a repository and a token. Those
# rows are stubbed with placeholder bytes so the merge and the gate see a complete matrix; the stub bytes are
# clearly named as such in the output, and `--real-only` skips them so you can watch the completeness gate
# go red instead.
#
#   scripts/release-rehearse.sh                 # full spine, stubs for the non-native targets
#   scripts/release-rehearse.sh v1.2.3-rc.4     # a specific rehearsal tag
#   scripts/release-rehearse.sh v1.2.3 --real-only   # only this host's target: the gate MUST go red
set -euo pipefail

cd "$(dirname "$0")/.."

TAG="${1:-v0.20.0-rc.1}"
REAL_ONLY=0
for arg in "$@"; do [ "$arg" = "--real-only" ] && REAL_ONLY=1; done

WORK="${WORK:-$(mktemp -d)}"
ART="${WORK}/artifacts"
DIST="${WORK}/dist"
mkdir -p "${ART}" "${DIST}"
echo "release-rehearse: tag=${TAG} work=${WORK}"

export GOWORK=off

# ── plan ────────────────────────────────────────────────────────────────────────────────────────────────
echo
echo "── plan ─────────────────────────────────────────────────────────────"
PLAN="${WORK}/plan.env"
go run ./cmd/herosdist plan --tag "${TAG}" | tee "${PLAN}"
VERSION="$(grep '^version=' "${PLAN}" | cut -d= -f2-)"
CHANNEL="$(grep '^channel=' "${PLAN}" | cut -d= -f2-)"
LDFLAGS="$(grep '^ldflags=' "${PLAN}" | cut -d= -f2-)"
IMAGE_TAGS="$(grep '^image_tags=' "${PLAN}" | cut -d= -f2-)"

# ── build: this host's native target, through the same script CI runs ────────────────────────────────────
echo
echo "── build (native) ───────────────────────────────────────────────────"
HOST_OS="$(go env GOOS)"; HOST_ARCH="$(go env GOARCH)"
NATIVE_BUNDLE="${ART}/build-${HOST_OS}-${HOST_ARCH}"
mkdir -p "${NATIVE_BUNDLE}"
OUT="${NATIVE_BUNDLE}" HEROS_LDFLAGS="${LDFLAGS}" bash scripts/release-cli.sh "${VERSION}"

# The version stamp assertion the workflow makes on each runner. A misspelled -X symbol path is silently
# ignored by the Go linker, so the only way to know the stamp landed is to ask the binary.
ext=""; [ "${HOST_OS}" = "windows" ] && ext=".exe"
BIN="${NATIVE_BUNDLE}/heros-${VERSION}-${HOST_OS}-${HOST_ARCH}${ext}"
# Asserted against the MACHINE-READABLE tool_version on stdout, not the narration on stderr. That field is
# the one stamped into linked run metadata, so it is the copy of the version a customer's report is filed
# against — and it is the only one worth asserting.
REPORTED="$("${BIN}" version 2>/dev/null)"
if printf '%s' "${REPORTED}" | grep -qF "\"tool_version\": \"${VERSION}\""; then
  echo "✅ the version stamp landed: tool_version=${VERSION}"
else
  echo "⛔ the binary does not report tool_version=${VERSION}:"; printf '%s\n' "${REPORTED}"; exit 1
fi

# The reproducibility gate, and its marker. Written only after the test passes — the same rule as CI.
echo
echo "── reproducibility gate ─────────────────────────────────────────────"
go test -count=1 -run 'TestReproducibleBuild' ./internal/release/
echo "reproducible on $(uname -s)-$(uname -m)" > "${NATIVE_BUNDLE}/REPRODUCIBLE-${HOST_OS}-${HOST_ARCH}"

# ── the four targets this host cannot build natively ────────────────────────────────────────────────────
if [ "${REAL_ONLY}" = "1" ]; then
  echo
  echo "── --real-only: the other four targets are ABSENT ───────────────────"
  echo "   the completeness gate must now go red. That is the point of this mode."
else
  echo
  echo "── stubs for the non-native targets (D1: they need their own runners) ─"
  while IFS=/ read -r goos goarch; do
    [ "${goos}" = "${HOST_OS}" ] && [ "${goarch}" = "${HOST_ARCH}" ] && continue
    b="${ART}/build-${goos}-${goarch}"
    mkdir -p "${b}"
    ext=""; [ "${goos}" = "windows" ] && ext=".exe"
    name="heros-${VERSION}-${goos}-${goarch}${ext}"
    # Clearly-labelled placeholder bytes. They are NOT a heros binary and the label says so, so nobody can
    # mistake a rehearsal artifact for a release one.
    printf 'REHEARSAL PLACEHOLDER — not a heros binary — %s\n' "${name}" > "${b}/${name}"
    ( cd "${b}" && { shasum -a 256 "${name}" 2>/dev/null || sha256sum "${name}"; } > SHA256SUMS )
    echo "reproducible (rehearsal stub)" > "${b}/REPRODUCIBLE-${goos}-${goarch}"
    echo "   stub ${goos}/${goarch}"
  done < <(go run ./cmd/herosdist plan --tag "${TAG}" | grep '^expected_assets=' | cut -d= -f2- \
           | tr ',' '\n' | sed -E "s/^heros-${VERSION}-//; s/\.exe$//" | tr '-' '/')
fi

# ── merge ───────────────────────────────────────────────────────────────────────────────────────────────
echo
echo "── merge (recompute + cross-check + sort) ───────────────────────────"
# --include mirrors the workflow: the install scripts join the signed manifest so a user who piped
# `curl … | sh` can verify what they ran against the same signature as the binaries (task 3.7).
go run ./cmd/herosdist merge --in "${ART}" --out "${DIST}" \
  --include scripts/install.sh,scripts/install.ps1

# ── sign, with a throwaway key ───────────────────────────────────────────────────────────────────────────
# A rehearsal must not need the real release key: a script that asks a developer for the production signing
# secret to try things out is how that secret ends up in a shell history. So the rehearsal generates a key,
# signs with it, and then states plainly that the signature will NOT verify against the published trust
# root — which is the honest outcome, and is exactly what the attestation records.
echo
echo "── sign (throwaway rehearsal key) ───────────────────────────────────"
if [ -n "${HEROS_RELEASE_PRIVATE_KEY:-}" ]; then
  echo "using HEROS_RELEASE_PRIVATE_KEY from the environment (a real release key)"
else
  eval "$(go run ./cmd/herossign keygen | sed 's/^/export /')"
  echo "generated a rehearsal keypair; public key ${HEROS_RELEASE_PUBLIC_KEY:0:16}…"
fi
sig="$(go run ./cmd/herossign sign --in "${DIST}/SHA256SUMS")"
printf '%s\n' "${sig}" > "${DIST}/SHA256SUMS.sig"

# ── attest ──────────────────────────────────────────────────────────────────────────────────────────────
echo
echo "── attest ───────────────────────────────────────────────────────────"
# attest VERIFIES the signature against the published trust root before recording the release as signed. A
# rehearsal key is not in that root, so this is expected to refuse — and the refusal is the proof that
# `SignedManifest: true` is a claim about bytes rather than about a file existing.
if go run ./cmd/herosdist attest --tag "${TAG}" --dir "${DIST}"; then
  echo "✅ attested as signed by a trust-root key"
  SIGNED=1
else
  echo "↑ expected with a rehearsal key: the signature does not verify against docs/release/heros-release.pub."
  echo "  Recording this rehearsal as UNSIGNED, which is what the gate then refuses to publish."
  rm -f "${DIST}/SHA256SUMS.sig"
  go run ./cmd/herosdist attest --tag "${TAG}" --dir "${DIST}"
  SIGNED=0
fi

# ── gate ────────────────────────────────────────────────────────────────────────────────────────────────
echo
echo "── gate (fail-closed) ───────────────────────────────────────────────"
set +e
go run ./cmd/herosdist gate --tag "${TAG}" --dir "${DIST}" --markers "${ART}"
GATE=$?
set -e

echo
echo "── notes ────────────────────────────────────────────────────────────"
go run ./cmd/herosdist notes --dir "${DIST}" --image-tags "${IMAGE_TAGS}" | head -30
echo "   … (full notes at ${DIST}/RELEASE_NOTES.md)"
go run ./cmd/herosdist notes --dir "${DIST}" --image-tags "${IMAGE_TAGS}" > "${DIST}/RELEASE_NOTES.md"

echo
echo "════════════════════════════════════════════════════════════════════"
echo "rehearsal summary — tag ${TAG} (channel ${CHANNEL})"
echo "  native target built + version-stamped + reproducible : ✅ ${HOST_OS}/${HOST_ARCH}"
if [ "${SIGNED}" = "1" ]; then
  echo "  manifest signed by a trust-root key                  : ✅"
else
  echo "  manifest signed by a trust-root key                  : ⛔ rehearsal key (expected)"
fi
if [ "${GATE}" = "0" ]; then
  echo "  release gate                                         : ✅ would publish"
else
  echo "  release gate                                         : ⛔ refused — nothing would publish"
fi
echo
echo "Not rehearsed here, and needing a repository + secrets:"
echo "  · the four non-native targets (D1 — one runner each)"
echo "  · the gh release create/edit/upload path and the published-asset re-verify"
echo "  · pushing an rc tag to publish a DRAFT Release (task 2.7's remote half)"
echo "════════════════════════════════════════════════════════════════════"
echo "artifacts: ${DIST}"
