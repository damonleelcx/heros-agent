#!/usr/bin/env bash
# packaging_proof.sh builds every generated package manifest for real (P20 tasks 3.3–3.6).
#
# # Why this exists
#
# `herosdist manifests` emits a Homebrew formula, a Scoop manifest, three winget files, two nfpm configs and a
# Dockerfile. Go tests assert their CONTENT — the version is the tag's, the checksums came from the signed
# manifest. What no Go test can tell you is whether the tools that consume them accept them: a formula with a
# Ruby syntax error, an nfpm config with a field nfpm renamed, or a Dockerfile that builds an image which then
# cannot run. Every one of those passes a content test and fails on the user's machine.
#
# So this script hands each generated file to the tool that reads it, and — for the two artifacts a user
# actually executes — runs the result and asserts the version it reports.
#
#   scripts/packaging_proof.sh <dist-dir>
#
# <dist-dir> must be a merged release directory (SHA256SUMS + trust.json + the binaries), such as the one
# `scripts/release-rehearse.sh` leaves behind. A real linux binary must be present for the container and
# package builds to be meaningful; the script says so rather than quietly building an image around a stub.
set -euo pipefail
cd "$(dirname "$0")/.."
export GOWORK=off

DIST="${1:?usage: packaging_proof.sh <dist-dir>}"
[ -f "${DIST}/SHA256SUMS" ] || { echo "⛔ ${DIST}/SHA256SUMS not found — that is not a merged release directory"; exit 1; }
[ -f "${DIST}/trust.json" ] || { echo "⛔ ${DIST}/trust.json not found — run \`herosdist attest\` first"; exit 1; }

TAG="v$(python3 -c "import json,sys;print(json.load(open('${DIST}/trust.json'))['version'])")"
VERSION="${TAG#v}"
echo "packaging proof — ${TAG}"

PASS=0; FAIL=0
ok()   { echo "  ✅ $*"; PASS=$((PASS+1)); }
bad()  { echo "  ⛔ $*"; FAIL=$((FAIL+1)); }

echo
echo "── generate ─────────────────────────────────────────────────────────"
go run ./cmd/herosdist manifests --tag "${TAG}" --dir "${DIST}" --out "${DIST}/packaging"
PKG="${DIST}/packaging"

echo
echo "── Homebrew formula: does Ruby accept it? ───────────────────────────"
# `ruby -c` is a syntax check only, but a formula with a syntax error is the failure mode a content test cannot
# see: brew reports it as "invalid formula" to every user of the tap at once.
if command -v ruby >/dev/null 2>&1; then
  if ruby -c "${PKG}/homebrew/heros.rb" >/dev/null 2>&1; then ok "heros.rb parses as Ruby"; else bad "heros.rb is not valid Ruby"; fi
else
  bad "ruby not available — the formula's syntax was NOT checked (stated, not skipped silently)"
fi

echo
echo "── Scoop / winget manifests: valid JSON and YAML? ───────────────────"
if python3 -c "import json;json.load(open('${PKG}/scoop/heros.json'))" 2>/dev/null; then
  ok "scoop/heros.json is valid JSON"
else
  bad "scoop/heros.json is not valid JSON — scoop would reject it"
fi
if python3 - "$PKG" <<'PY'
import sys, glob, pathlib
try:
    import yaml
except ImportError:
    sys.exit(3)
files = glob.glob(str(pathlib.Path(sys.argv[1]) / "winget" / "**" / "*.yaml"), recursive=True)
if not files:
    sys.exit(1)
ids = set()
for f in files:
    d = yaml.safe_load(open(f))
    ids.add((d["PackageIdentifier"], str(d["PackageVersion"])))
# winget requires the identifier and version to agree across all three files.
sys.exit(0 if len(ids) == 1 else 2)
PY
then ok "the three winget files are valid YAML and agree on identifier+version"
else
  case $? in
    3) bad "pyyaml not available — the winget manifests were NOT parsed (stated, not skipped silently)" ;;
    2) bad "the winget files disagree about PackageIdentifier/PackageVersion — winget rejects the submission" ;;
    *) bad "the winget manifests are not valid YAML" ;;
  esac
fi

echo
echo "── nfpm: build a real .deb and .rpm ─────────────────────────────────"
LINUX_ARCH="$(go env GOARCH)"
LINUX_BIN="heros-${VERSION}-linux-${LINUX_ARCH}"

# The linux asset must be a REAL binary that reports THIS version — not merely a file with the right name.
# The first run of this proof caught exactly that: a binary from another build, renamed, builds a perfectly
# good-looking image that then reports someone else's version. A filename is not provenance.
ensure_linux_binary() {
  if [ -f "${DIST}/${LINUX_BIN}" ] && command -v docker >/dev/null 2>&1; then
    if docker run --rm -v "${DIST}:/d:ro" debian:12-slim /d/"${LINUX_BIN}" version 2>/dev/null \
         | grep -qF "\"tool_version\": \"${VERSION}\""; then
      ok "${LINUX_BIN} is a real linux binary reporting ${VERSION}"
      return 0
    fi
    echo "  ${LINUX_BIN} is present but does not report ${VERSION} — rebuilding it natively"
  fi
  command -v docker >/dev/null 2>&1 || { bad "docker unavailable — cannot build or check the linux binary"; return 1; }
  echo "  building linux/${LINUX_ARCH} natively inside golang:1.24 (GOPROXY=off: hermetic, host module cache)"
  docker run --rm \
    -v "${PWD}:/src" -v "${DIST}:/out" -v "$(go env GOMODCACHE):/go/pkg/mod:ro" \
    -w /src -e GOWORK=off -e CGO_ENABLED=1 -e "GOFLAGS=-buildvcs=false -mod=mod" \
    -e GOPROXY=off -e GOCACHE=/tmp/gocache -e OUT=/out \
    golang:1.24 bash scripts/release-cli.sh "${VERSION}" >/dev/null 2>&1 \
    || { bad "the native linux build failed"; return 1; }
  ok "built ${LINUX_BIN} natively"
}

if ! ensure_linux_binary; then
  bad "skipping the package and image builds: no trustworthy linux binary"
else
  if ! command -v nfpm >/dev/null 2>&1; then
    echo "  installing nfpm (pinned)…"
    go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.43.1 >/dev/null 2>&1 || true
    export PATH="$(go env GOPATH)/bin:${PATH}"
  fi
  if command -v nfpm >/dev/null 2>&1; then
    for packager in deb rpm; do
      if ( cd "${DIST}" && nfpm package --config "packaging/nfpm/nfpm-${LINUX_ARCH}.yaml" --packager "${packager}" --target . ) >/dev/null 2>&1; then
        built="$(ls -1 "${DIST}"/*."${packager}" 2>/dev/null | head -n1)"
        ok "nfpm built $(basename "${built}")"
        # The version inside the package metadata must be the tag's. A package whose filename says one version
        # and whose metadata says another is the drift D5 exists to prevent, one layer down.
        # Debian's version grammar reserves "-" for the package revision, so nfpm maps a semver prerelease
        # "0.20.0-rc.1" to "0.20.0~rc.1". The "~" is CORRECT and load-bearing: it sorts BEFORE 0.20.0, so
        # apt will not treat a release candidate as newer than its own GA. The expected value is derived
        # rather than hard-coded so this check does not have to know whether the tag was a prerelease.
        want_deb="${VERSION//-/~}"
        if [ "${packager}" = "deb" ] && command -v dpkg-deb >/dev/null 2>&1; then
          got="$(dpkg-deb -f "${built}" Version)"
          [ "${got}" = "${want_deb}" ] && ok "the .deb's metadata version is ${got}" || bad ".deb metadata version is ${got}, want ${want_deb}"
        elif [ "${packager}" = "deb" ]; then
          case "$(basename "${built}")" in
            *"${want_deb}"*) ok "the .deb filename carries ${want_deb} (dpkg-deb absent, so metadata was not read)" ;;
            *) bad "the .deb filename does not carry ${want_deb}" ;;
          esac
        fi
      else
        bad "nfpm could not build the ${packager} from the generated config"
      fi
    done
  else
    bad "nfpm could not be installed — the .deb/.rpm build was NOT proved"
  fi
fi

echo
echo "── container image: build it, then RUN it ───────────────────────────"
if ! command -v docker >/dev/null 2>&1; then
  bad "docker not available — the image was NOT built or run"
elif [ ! -f "${DIST}/heros-${VERSION}-linux-amd64" ] && [ ! -f "${DIST}/${LINUX_BIN}" ]; then
  bad "no linux binary to copy into the image"
else
  # The generated Dockerfile copies the amd64 asset (that is what the release publishes). On an arm64 host the
  # amd64 asset may be a rehearsal stub, so the proof builds against THIS host's real binary and says so — an
  # image built around a stub would pass a build and fail every `docker run`.
  img="heros-packaging-proof:${VERSION}"
  sed "s|COPY heros-${VERSION}-linux-amd64|COPY ${LINUX_BIN}|" \
    "${PKG}/container/Dockerfile" > "${DIST}/Dockerfile.proof"
  if docker build -q -f "${DIST}/Dockerfile.proof" -t "${img}" "${DIST}" >/dev/null 2>&1; then
    ok "the generated Dockerfile builds"
    if docker run --rm "${img}" version 2>/dev/null | grep -qF "\"tool_version\": \"${VERSION}\""; then
      ok "the image runs and reports tool_version=${VERSION}"
    else
      bad "the image built but does not report tool_version=${VERSION} — a pushed image nobody ran"
    fi
    # Asserted to a first REAL result, not to `version` (P20 task 7.2). An image that answers `version` and then
    # dies on the first discover is the shape of a broken packaging job: a missing shared library, a wrong base,
    # an unwritable workdir. None of them surface until the tool does real work.
    fixture="${PWD}/internal/discovery/testdata/samplerepo"
    out="$(docker run --rm -v "${fixture}:/src:ro" --entrypoint sh "${img}" -c \
      'cp -r /src /tmp/repo && cd /tmp/repo && /usr/local/bin/heros discover --out ir.json --report r.json 2>/dev/null && /usr/local/bin/heros eval --seeds 3 --cases 4 2>/dev/null' || true)"
    if printf '%s' "${out}" | grep -q '"nodes": [1-9]' && printf '%s' "${out}" | grep -q '"metric": "quality"'; then
      quality="$(printf '%s' "${out}" | grep -A1 '"metric": "quality"' | grep '"value"' | head -1 | tr -d ' ,' | cut -d: -f2)"
      ok "the image runs a real discover + eval in the container channel (quality=${quality})"
    else
      bad "the image cannot complete a first discover + eval — it answers \`version\` and does no work"
    fi
    docker rmi -f "${img}" >/dev/null 2>&1 || true
  else
    bad "the generated Dockerfile does not build"
  fi
fi

echo
echo "════════════════════════════════════════════════════════════════════"
echo "packaging proof: ${PASS} passed, ${FAIL} failed"
[ "${FAIL}" -eq 0 ] || exit 1
