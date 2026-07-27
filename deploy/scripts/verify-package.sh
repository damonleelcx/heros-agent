#!/bin/sh
# Air-gapped package integrity gate (P19 §7.1, deployment-topology: "integrity is verifiable BEFORE
# apply"). This is the gate that must pass before any `docker load` or `compose up` touches the host.
#
# WHY A PRE-APPLY GATE, NOT A POST-HOC AUDIT. In an air-gapped install the package crossed an air gap —
# a USB stick, an internal file drop, a courier. Between our build host and the operator's machine room
# there is no TLS session vouching for the bytes; the tarball could be truncated, bit-rotted, or swapped.
# `docker load`ing a tampered image, or `compose up`ing a swapped manifest, is an IRREVERSIBLE step —
# once a poisoned image runs, the blast radius is the whole platform. So integrity is proven FIRST, from
# the checksum manifest the package carries, with the operator running exactly this, offline, without us.
#
# WHY IT REFUSES ON *ANY* DISCREPANCY, LOUDLY. The manifest (SHA256SUMS, written by package-airgapped.sh)
# lists every artifact and its digest. This gate fails — non-zero, on stderr — on three distinct tampers:
#   (1) a CHANGED file       (checksum mismatch)      — bytes were altered.
#   (2) a MISSING file       (listed, not present)    — the package was truncated / an artifact dropped.
#   (3) an EXTRA file        (present, not listed)    — something was INJECTED that we never shipped.
# A verifier that only checks (1) and (2) — which a bare `sha256sum -c` does — would wave through (3), an
# injected script or a swapped-in image tar, which is precisely the interesting attack. So we check the
# set both ways: manifest ⊆ present AND present ⊆ manifest.
#
# 八级法则: 安全 first. A false PASS here is the worst outcome in the whole delivery chain — it is the one
# moment the operator TRUSTS the bytes. So there is no `|| true`, no `2>/dev/null` swallowing, no partial
# credit: it is all-green or it is a refusal.
#
# USAGE:
#   verify-package.sh <extracted-package-dir>      # verify an already-extracted package (the common case)
#   verify-package.sh <package>.tar.gz             # extract to a temp dir, verify, then discard
# EXIT: 0 = every artifact verified; non-zero = refuse to apply.
set -eu

die() { echo "verify-package: FAIL: $*" >&2; exit 1; }
log() { echo "verify-package: $*"; }

[ $# -ge 1 ] || die "usage: verify-package.sh <extracted-package-dir | package.tar.gz>"
TARGET="$1"
[ -e "$TARGET" ] || die "no such path: $TARGET"

# ── sha256 tool detection. Both forms support `-c <manifest>`; fail loud if neither exists. ───────────
if command -v sha256sum >/dev/null 2>&1; then SHA256C="sha256sum -c"; SHA256="sha256sum";
elif command -v shasum   >/dev/null 2>&1; then SHA256C="shasum -a 256 -c"; SHA256="shasum -a 256";
else die "no sha256 tool (sha256sum or shasum) — cannot verify integrity"; fi

# ── If handed a tarball, extract to a temp dir we own and clean up on every exit path. Verifying the
#    tarball's CONTENTS (not just its outer sum) is what lets the operator trust what they'll `load`. ──
CLEANUP=""
cleanup() { [ -n "$CLEANUP" ] && rm -rf "$CLEANUP"; }
trap cleanup EXIT INT TERM

PKG_DIR="$TARGET"
case "$TARGET" in
  *.tar.gz|*.tgz)
    command -v tar >/dev/null 2>&1 || die "tar not found — needed to unpack $TARGET"
    tmp=$(mktemp -d 2>/dev/null) || die "cannot create temp dir for extraction"
    CLEANUP="$tmp"
    log "extracting $TARGET to a temp dir for verification"
    tar -xzf "$TARGET" -C "$tmp" || die "extraction failed for $TARGET"
    # The package's top-level dir is its name; find the one dir containing SHA256SUMS.
    PKG_DIR=$(find "$tmp" -maxdepth 2 -name SHA256SUMS -type f 2>/dev/null | head -n1 | sed 's#/SHA256SUMS$##')
    [ -n "$PKG_DIR" ] || die "extracted archive has no SHA256SUMS — not a heros air-gapped package"
    ;;
esac

[ -d "$PKG_DIR" ] || die "$PKG_DIR is not a directory"
MANIFEST="$PKG_DIR/SHA256SUMS"
[ -f "$MANIFEST" ] || die "no SHA256SUMS in $PKG_DIR — cannot verify (is this an extracted package root?)"

log "verifying package at $PKG_DIR"

# ── Check (1)+(2): every file the manifest lists matches its recorded digest and is present. `-c` reads
#    "<hash>  <relative-path>" and returns non-zero on any mismatch or unreadable/missing file. We run it
#    from inside the package root so the relative paths resolve. Its per-file output goes straight to the
#    operator (no swallowing) so a failure NAMES the artifact. ──────────────────────────────────────────
log "checking recorded checksums (changed / missing files)"
if ! ( cd "$PKG_DIR" && $SHA256C SHA256SUMS ); then
  die "checksum verification FAILED — one or more artifacts are altered or missing (see the FAILED lines above). Refusing to apply."
fi

# ── Check (3): nothing present that the manifest does not list. This is the INJECTION check a bare
#    `sha256sum -c` cannot do. We diff the set of files actually on disk against the manifest's path list.
#    Anything extra (other than SHA256SUMS itself, which is never self-listed) is a tamper signal. ──────
log "checking for injected / unlisted files"
listed=$(mktemp 2>/dev/null) || die "cannot create temp file"
present=$(mktemp 2>/dev/null) || { rm -f "$listed"; die "cannot create temp file"; }
# Extend cleanup to remove these too.
CLEANUP="${CLEANUP:+$CLEANUP }$listed $present"
trap 'for p in $CLEANUP; do rm -rf "$p"; done' EXIT INT TERM

# Manifest path column: strip the leading "<hash>  " (two spaces after the hash). Normalize any leading
# "./" so both sides compare on identical relative paths.
sed -e 's/^[0-9a-fA-F][0-9a-fA-F]*[[:space:]][[:space:]]*//' -e 's#^\./##' "$MANIFEST" | LC_ALL=C sort > "$listed"
( cd "$PKG_DIR" && find . -type f ! -name SHA256SUMS | sed 's#^\./##' | LC_ALL=C sort ) > "$present"

# Files present but NOT listed => injected. `comm -13` = lines only in the second (present) set.
extra=$(LC_ALL=C comm -13 "$listed" "$present" || true)
if [ -n "$extra" ]; then
  echo "verify-package: FAIL: files present in the package but NOT in SHA256SUMS (injected/unexpected):" >&2
  echo "$extra" | sed 's/^/    + /' >&2
  die "refusing to apply a package containing unlisted files"
fi

log "PASS: every artifact matches SHA256SUMS, nothing missing, nothing injected."
log "package is safe to apply: $PKG_DIR"
exit 0
