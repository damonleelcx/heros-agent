#!/bin/sh
# Air-gapped delivery packager (P19 §7.1, Decision 7, deployment-topology: "An air-gapped package SHALL
# install with no public egress and be integrity-verifiable before apply").
#
# WHY THIS EXISTS. A private-deploy / 2B customer runs the platform in a machine room we will never
# touch, often with NO public egress at all. That customer cannot `docker pull`, cannot reach ghcr.io,
# cannot `go build`, and cannot run our CI. So the delivery unit has to be a SINGLE self-contained
# tarball that carries everything the install needs — the digest-pinned images already `docker save`d,
# every compose/Kustomize manifest, the operator scripts, and a checksum manifest — such that the only
# thing the operator supplies on the far side is their own SECRETS (which, by construction, this package
# never carries — see below).
#
# WHY REPRODUCIBLE-FROM-PINNED-INPUTS. The inputs to this package are the digests in deploy/images.env
# (D2: one image set, pinned by digest, not a floating tag). Building from a digest means the image that
# gets `docker save`d is THE artifact, not "whatever :latest resolved to today". Two builds from the same
# images.env load back the same image IDs on the far side, so "it works on my build host but not the
# customer's" is a topology question, never an image question. We refuse to package an image we cannot
# prove is the pinned one (fail loud, below), rather than silently packaging a drifted local copy.
#
# WHY A CHECKSUM MANIFEST IS PART OF THE PACKAGE, NOT A SEPARATE DOWNLOAD. Integrity has to be
# "verifiable BEFORE apply" — the operator must be able to prove the bytes on the USB stick are the bytes
# we cut, before any `docker load` runs, without contacting us. SHA256SUMS travels INSIDE the tarball and
# covers every other artifact; verify-package.sh checks it. (The tarball's own outer checksum is what you
# publish out-of-band for the first-hop trust; this manifest is the inner, per-artifact gate.)
#
# 🔴 THIS PACKAGE CARRIES NO SECRETS AND NEVER WILL. It bundles the `.env.*.example` files (NAMES and
# shapes, no values) exactly as the repo posture demands — a real credential committed or shipped in a
# bundle outlives the moment it was valid. The operator fills their own secret env from their own secret
# store at install time; install-airgapped.sh reads it from a path OUTSIDE the package. If you are ever
# tempted to bundle a filled `.env`, that is the one-way door 安全>* forbids.
#
# 八级法则 (安全 > 稳定 > UX > 运维 > 可演进 > 可扩展 > 维护 > 实现): this script is 运维/可演进 machinery.
# Every failure path is LOUD and non-zero — a packager that half-builds a tarball is worse than one that
# refuses, because a truncated package `docker load`s a partial image set and fails on the far side,
# somewhere less obvious, in an air-gapped room with no one to call.
#
# USAGE:
#   deploy/scripts/package-airgapped.sh --version v1.4.0            # digests already pulled locally
#   HEROS_ALLOW_PULL=1 deploy/scripts/package-airgapped.sh --version v1.4.0   # pull pinned digests first
#   deploy/scripts/package-airgapped.sh --version v1.4.0 --out /path/to/dist
#
# OUTPUT: <out>/heros-platform-<version>.tar.gz  (+ a staging dir alongside it, kept for inspection).
set -eu

# ── LOUD failure helper. Never a silent exit; always stderr + non-zero. ───────────────────────────────
die() { echo "package-airgapped: FATAL: $*" >&2; exit 1; }
log() { echo "package-airgapped: $*"; }

# ── Locate the repo's deploy/ tree from this script's own location, so the packager works regardless of
#    the caller's CWD (an operator may run it from anywhere). ───────────────────────────────────────────
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || die "cannot resolve script directory"
DEPLOY_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd) || die "cannot resolve deploy/ directory"
REPO_ROOT=$(CDPATH= cd -- "$DEPLOY_DIR/.." && pwd) || die "cannot resolve repo root"
IMAGES_ENV="$DEPLOY_DIR/images.env"

# ── Inputs. VERSION is a required, pinned input (reproducibility): a package's identity is chosen, not a
#    wall-clock timestamp that would make two builds of the same inputs differ. ─────────────────────────
VERSION="${HEROS_PACKAGE_VERSION:-}"
DIST_DIR="${DIST_DIR:-$REPO_ROOT/dist}"   # /dist/ is gitignored — build artifacts never enter the tree.
while [ $# -gt 0 ]; do
  case "$1" in
    --version) shift; [ $# -gt 0 ] || die "--version needs a value"; VERSION="$1" ;;
    --out)     shift; [ $# -gt 0 ] || die "--out needs a value"; DIST_DIR="$1" ;;
    -h|--help) sed -n '1,40p' "$0"; exit 0 ;;
    *) die "unknown argument: $1 (see --help)" ;;
  esac
  shift
done
if [ -z "$VERSION" ]; then
  # Fall back to git describe only as a convenience; still records a concrete, inspectable value.
  VERSION=$(cd "$REPO_ROOT" && git describe --tags --always --dirty 2>/dev/null || true)
  [ -n "$VERSION" ] || die "no version given: pass --version <v> or set HEROS_PACKAGE_VERSION (git describe unavailable)"
  log "no --version given; derived VERSION=$VERSION from git describe"
fi

# ── Dependency-light preflight: docker (for save), tar, gzip, and a sha256 tool. Fail loud on absence. ─
command -v docker >/dev/null 2>&1 || die "docker not found — needed to 'docker save' the pinned image set"
command -v tar    >/dev/null 2>&1 || die "tar not found"
command -v gzip   >/dev/null 2>&1 || die "gzip not found"
if command -v sha256sum >/dev/null 2>&1; then SHA256="sha256sum";
elif command -v shasum   >/dev/null 2>&1; then SHA256="shasum -a 256";
else die "no sha256 tool (sha256sum or shasum) — cannot write the integrity manifest"; fi
[ -f "$IMAGES_ENV" ] || die "missing $IMAGES_ENV — the single pinned image set is the packager's input (D2)"

# ── Read the pinned image references out of images.env. We PARSE rather than `source`, so a stray line in
#    the env file can never execute; we take the RHS of every *_IMAGE= line and strip the inline #pin
#    comment. The values are digest-pinned refs (…@sha256:…). ───────────────────────────────────────────
image_refs() {
  grep -E '^[A-Z0-9_]+_IMAGE=' "$IMAGES_ENV" \
    | sed -e 's/#.*$//' -e 's/^[^=]*=//' -e 's/[[:space:]]*$//' \
    | grep -v '^$'
}
REFS=$(image_refs) || die "failed to parse image refs from $IMAGES_ENV"
[ -n "$REFS" ] || die "no *_IMAGE entries found in $IMAGES_ENV — nothing to package"

# ── Staging layout. We build into a staging dir, checksum it, then tar it, so the tarball's top-level dir
#    is the package name (a clean extract, never a tar-bomb into CWD). ──────────────────────────────────
PKG_NAME="heros-platform-$VERSION"
STAGE="$DIST_DIR/$PKG_NAME"
TARBALL="$DIST_DIR/$PKG_NAME.tar.gz"
mkdir -p "$DIST_DIR" || die "cannot create dist dir $DIST_DIR"
rm -rf "$STAGE" || die "cannot clean stale staging dir $STAGE"
mkdir -p "$STAGE/images" "$STAGE/deploy" || die "cannot create staging layout"

# ── 1) Save every pinned image. Require the exact digest to be present locally; only pull when the
#    operator explicitly allows it (HEROS_ALLOW_PULL=1). A packager that silently packages whatever local
#    tag happens to match the repo name would defeat the whole digest-pinning contract. ─────────────────
log "packaging $PKG_NAME"
log "image set from $IMAGES_ENV:"; echo "$REFS" | sed 's/^/    /'
for ref in $REFS; do
  case "$ref" in
    *@sha256:0000000000000000000000000000000000000000000000000000000000000000)
      die "refusing to package placeholder digest for '$ref' — the release pipeline must replace it with a real digest before an air-gapped cut" ;;
  esac
  if ! docker image inspect "$ref" >/dev/null 2>&1; then
    if [ "${HEROS_ALLOW_PULL:-0}" = "1" ]; then
      log "pulling pinned digest: $ref"
      docker pull "$ref" || die "docker pull failed for $ref (build host has no route to the registry?)"
    else
      die "image not present locally: $ref
      → pull the pinned digest on a connected build host first, or re-run with HEROS_ALLOW_PULL=1.
      → we refuse to guess a substitute; the packaged image must be the pinned one, byte-for-byte."
    fi
  fi
  # Deterministic per-image filename: a saved tar per image keeps SHA256SUMS granular, so verify names
  # the exact image that was tampered with, not just "the blob".
  fname=$(printf '%s' "$ref" | tr '/:@' '___').tar
  out="$STAGE/images/$fname"
  log "docker save -> images/$fname"
  # Save to a .partial first; the final name only ever names a COMPLETE save (same discipline as
  # pg-backup.sh: a truncated artifact must never masquerade as a finished one).
  if ! docker save "$ref" -o "$out.partial"; then
    rm -f "$out.partial"
    die "docker save failed for $ref"
  fi
  [ -s "$out.partial" ] || { rm -f "$out.partial"; die "docker save produced an empty file for $ref — refusing to ship a zero-byte image"; }
  mv "$out.partial" "$out" || die "cannot finalize $out"
done

# ── 2) Copy the manifests: every compose file, the pinned image set, the env EXAMPLES (no secrets), the
#    operator scripts, and the whole Kustomize tree. This is "manifests + platform/operator binaries". ──
log "copying manifests (compose + images.env + env examples + scripts + k8s)"
# compose files and the image set
for f in "$DEPLOY_DIR"/docker-compose.platform.yml "$DEPLOY_DIR"/docker-compose.admin-console.yml "$IMAGES_ENV"; do
  [ -f "$f" ] || die "expected manifest missing: $f"
  cp "$f" "$STAGE/deploy/" || die "cannot copy $f"
done
# env EXAMPLES ONLY — the *.example carry names/shapes, never values. We copy them by exact name so a
# filled `.env.platform` (were one to exist next to them) can NEVER be swept in by a glob.
for f in .env.platform.example .env.admin-console.example; do
  [ -f "$DEPLOY_DIR/$f" ] || die "expected env example missing: $DEPLOY_DIR/$f"
  cp "$DEPLOY_DIR/$f" "$STAGE/deploy/" || die "cannot copy $f"
done
# 🔴 Guard: refuse to ship if a filled secret env slipped into the deploy dir and matches our copy set.
# A .env without the .example suffix is presumed to carry secrets and MUST NOT enter a package.
for leak in "$DEPLOY_DIR/.env.platform" "$DEPLOY_DIR/.env.admin-console" "$DEPLOY_DIR/.env.images"; do
  if [ -f "$STAGE/deploy/$(basename "$leak")" ]; then
    die "SECRET LEAK GUARD: $(basename "$leak") is in the staging set — packages carry only *.example, never a filled env"
  fi
done
# operator scripts (this packager, the verifier, the installer, the doctor, the backup discipline)
mkdir -p "$STAGE/deploy/scripts" || die "cannot create scripts dir in staging"
for f in package-airgapped.sh verify-package.sh install-airgapped.sh doctor.sh pg-backup.sh; do
  [ -f "$SCRIPT_DIR/$f" ] || die "expected script missing: $SCRIPT_DIR/$f"
  cp "$SCRIPT_DIR/$f" "$STAGE/deploy/scripts/" || die "cannot copy $f"
done
# the whole Kustomize tree (base + overlays), copied verbatim so the artifact audited IS the artifact
# applied (Decision 1). Optional only in the sense that a compose-only site may ignore it.
if [ -d "$DEPLOY_DIR/k8s" ]; then
  cp -R "$DEPLOY_DIR/k8s" "$STAGE/deploy/k8s" || die "cannot copy k8s tree"
else
  log "note: $DEPLOY_DIR/k8s absent — packaging compose substrate only"
fi

# ── 2b) 🔴 ZERO EXTERNAL ORIGINS (P24 task 1.8, design D1). ───────────────────────────────────────────
# P24 installs an analytics tag, a session recorder and an error reporter — all three configured for the
# platform's own hosted deployment ONLY. On this substrate they are absent, and absence is silent.
#
# The claim "this package references no external origin" is precisely the one an air-gapped customer
# cannot check for themselves: they have no egress with which to observe something trying to leave, and
# by the time anything is visible the tarball is already inside their machine room. So the claim is
# produced by the run that produces the artifact, not by a README. The gate is deliberately NOT copied
# into the package: it is a build-host check, and it carries example hostnames in its own self-test that
# would trip a scan of the package it just cleared.
ORIGIN_GATE="$SCRIPT_DIR/check-external-origins.sh"
[ -f "$ORIGIN_GATE" ] || die "missing $ORIGIN_GATE — the zero-external-origin gate is part of the package build, not optional"
log "checking the staged package references no external origin"
sh "$ORIGIN_GATE" "$STAGE" || die "the staged package references an external origin or a reporting identity (see above) — refusing to cut an air-gapped package that can phone home"

# ── 3) A VERSION marker (no secrets): records the identity install/rollback compare against, and the
#    exact pinned image set, so an operator can read what they hold without trusting a filename. ────────
{
  echo "package: $PKG_NAME"
  echo "version: $VERSION"
  echo "built_from_images_env: deploy/images.env"
  echo "images:"
  echo "$REFS" | sed 's/^/  - /'
} > "$STAGE/VERSION" || die "cannot write VERSION marker"

# ── 4) The integrity manifest. Cover EVERY artifact under the package root except SHA256SUMS itself.
#    Sorted (LC_ALL=C) so the manifest ordering is stable across builds. Paths are relative to the
#    package root, which is exactly what `sha256sum -c` needs from inside the extracted dir. ────────────
log "writing SHA256SUMS"
(
  cd "$STAGE" || die "cannot enter staging dir"
  # -type f, exclude the manifest we're about to write; stable order.
  find . -type f ! -name SHA256SUMS | LC_ALL=C sort | while IFS= read -r rel; do
    rel=${rel#./}
    # $SHA256 prints "<hash>  <path>"; we keep its exact two-space format for `-c` compatibility.
    $SHA256 "$rel" || die "checksum failed for $rel"
  done
) > "$STAGE/SHA256SUMS" || die "failed to build SHA256SUMS"
[ -s "$STAGE/SHA256SUMS" ] || die "SHA256SUMS came out empty — refusing to ship an unverifiable package"

# ── 5) The tarball. Top-level dir is the package name (clean extract). ────────────────────────────────
log "creating $TARBALL"
# -C so the archive's top entry is $PKG_NAME, not an absolute path. gzip via -z (dependency-light).
tar -C "$DIST_DIR" -czf "$TARBALL.partial" "$PKG_NAME" || { rm -f "$TARBALL.partial"; die "tar failed"; }
[ -s "$TARBALL.partial" ] || { rm -f "$TARBALL.partial"; die "tar produced an empty archive"; }
mv "$TARBALL.partial" "$TARBALL" || die "cannot finalize tarball"

# ── 6) The OUTER checksum, published out-of-band for first-hop trust (the inner SHA256SUMS is the
#    per-artifact gate verify-package.sh runs after extraction). ───────────────────────────────────────
( cd "$DIST_DIR" && $SHA256 "$PKG_NAME.tar.gz" ) > "$TARBALL.sha256" || die "cannot checksum the tarball"

log "DONE"
log "  package : $TARBALL"
log "  outer   : $TARBALL.sha256   (publish this out-of-band)"
log "  staging : $STAGE   (kept for inspection; safe to delete)"
log "next: transfer the tarball, then on the far side:"
log "  tar -xzf $PKG_NAME.tar.gz"
log "  $PKG_NAME/deploy/scripts/verify-package.sh $PKG_NAME"
log "  HEROS_ENV_FILE=/path/to/your/.env.platform $PKG_NAME/deploy/scripts/install-airgapped.sh $PKG_NAME"
