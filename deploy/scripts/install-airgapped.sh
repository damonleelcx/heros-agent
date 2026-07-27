#!/bin/sh
# Air-gapped installer / upgrader / rollback verb (P19 §7.2–7.3, Decision 7, deployment-topology:
# "Deployment SHALL be declarative and idempotent, with upgrade by re-apply and no teardown";
# "Upgrade SHALL preserve state and rollback SHALL be re-applying the prior package").
#
# WHY ONE VERB FOR INSTALL, UPGRADE, AND ROLLBACK. A private-deploy operator only ever runs the
# installer — never our test suite, never a version-specific migration script we mailed them. So
# correctness has to be a property of the APPLY, not of a CI job they cannot run. This script therefore
# has exactly one action — "bring the platform to the state this package describes" — and:
#   • INSTALL  = run it against a package on a clean host.
#   • UPGRADE  = run the SAME command against a NEWER package. No teardown, no bespoke per-version step.
#                `docker load` adds the new pinned images; `compose up -d` converges the running set;
#                migrations run inside agentd on boot, idempotent and state-preserving (P19 §3.4).
#   • ROLLBACK = run the SAME command against the PRIOR package (pass it as the package dir, or via
#                --rollback-to). Re-applying the prior package is the whole rollback story — there is no
#                same-version "restore from backup" fallback, because that poisons the chain (Decision 7).
#
# WHY DECLARATIVE-IDEMPOTENT MATTERS HERE. Applying twice must converge to the same state. `compose up -d`
# is idempotent by construction — it reconciles to the compose file and touches only what drifted. So a
# second run is a no-op; a re-install reports "already present" EXACTLY ONCE (below) and does NOT
# re-prompt for a master password — because this installer never prompts for secrets at all. Secrets come
# from an env file OUTSIDE the package (--env-file / HEROS_ENV_FILE), read fresh each run; there is no
# interactive master-password step to re-prompt, by design (§8.6).
#
# WHY IT REFUSES AN UNVERIFIED PACKAGE. The first thing it does is run verify-package.sh. `docker load`ing
# a tampered image is an irreversible one-way door; the integrity gate is not optional and cannot be
# skipped by a flag. 安全 > 运维 > 实现: no convenience flag buys past the safety gate.
#
# 🔴 SECRETS ARE NEVER IN THE PACKAGE. This installer reads the digest-pinned image set from the package
# (deploy/images.env) and the SECRET env from a path you supply that lives OUTSIDE the package. If
# HEROS_ENV_FILE is unset it REFUSES to start (${VAR:?} posture) — a stack that comes up without its
# secrets is a stack that fails later somewhere less obvious.
#
# USAGE:
#   HEROS_ENV_FILE=/secure/.env.platform  install-airgapped.sh <package-dir>
#   install-airgapped.sh <package-dir> --env-file /secure/.env.platform
#   install-airgapped.sh <package-dir> --with-admin --admin-env-file /secure/.env.admin-console
#   install-airgapped.sh --rollback-to <prior-package-dir> --env-file /secure/.env.platform
# STATE: the installed package version is recorded under HEROS_STATE_DIR (default /var/lib/heros-deploy)
#   so "already present" and the never-same-version-rollback rule can be enforced.
set -eu

die() { echo "install-airgapped: FATAL: $*" >&2; exit 1; }
log() { echo "install-airgapped: $*"; }

# ── Argument parsing. ─────────────────────────────────────────────────────────────────────────────────
PKG_DIR=""
ENV_FILE="${HEROS_ENV_FILE:-}"
ADMIN_ENV_FILE="${HEROS_ADMIN_ENV_FILE:-}"
WITH_ADMIN=0
ROLLBACK=0
while [ $# -gt 0 ]; do
  case "$1" in
    --env-file)       shift; [ $# -gt 0 ] || die "--env-file needs a path"; ENV_FILE="$1" ;;
    --admin-env-file) shift; [ $# -gt 0 ] || die "--admin-env-file needs a path"; ADMIN_ENV_FILE="$1" ;;
    --with-admin)     WITH_ADMIN=1 ;;
    --rollback-to)    shift; [ $# -gt 0 ] || die "--rollback-to needs a prior-package dir"; PKG_DIR="$1"; ROLLBACK=1 ;;
    -h|--help)        sed -n '1,45p' "$0"; exit 0 ;;
    -*)               die "unknown flag: $1 (see --help)" ;;
    *)                [ -z "$PKG_DIR" ] || die "more than one package dir given"; PKG_DIR="$1" ;;
  esac
  shift
done
[ -n "$PKG_DIR" ] || die "no package dir given (usage: install-airgapped.sh <package-dir> --env-file <path>)"
PKG_DIR=$(CDPATH= cd -- "$PKG_DIR" 2>/dev/null && pwd) || die "package dir does not exist: $PKG_DIR"

# 🔴 Refuse-to-start on the required secret env path. We do NOT read the values — only the path — and we
# never echo them; but without them there is nothing to bring up correctly.
[ -n "$ENV_FILE" ] || die "HEROS_ENV_FILE (or --env-file) is required — the platform's secrets live OUTSIDE the package and must be supplied at apply time"
[ -f "$ENV_FILE" ] || die "secret env file not found: $ENV_FILE"

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || die "cannot resolve script dir"

# ── The packaged layout: <pkg>/SHA256SUMS, <pkg>/images/*.tar, <pkg>/deploy/{compose,images.env,k8s}.
#    If these are absent, the operator pointed us at the source repo, not an extracted package. ─────────
[ -f "$PKG_DIR/SHA256SUMS" ] || die "$PKG_DIR has no SHA256SUMS — install runs from an EXTRACTED PACKAGE (built by package-airgapped.sh), not the source tree"
[ -d "$PKG_DIR/images" ]     || die "$PKG_DIR/images missing — this is not an air-gapped package"
COMPOSE_PLATFORM="$PKG_DIR/deploy/docker-compose.platform.yml"
COMPOSE_ADMIN="$PKG_DIR/deploy/docker-compose.admin-console.yml"
IMAGES_ENV="$PKG_DIR/deploy/images.env"
for f in "$COMPOSE_PLATFORM" "$IMAGES_ENV"; do
  [ -f "$f" ] || die "packaged manifest missing: $f"
done

# ── Dependency-light preflight. ───────────────────────────────────────────────────────────────────────
command -v docker >/dev/null 2>&1 || die "docker not found"
# `docker compose` (v2 plugin) preferred; fall back to legacy `docker-compose`. Fail loud if neither.
if docker compose version >/dev/null 2>&1; then DC="docker compose";
elif command -v docker-compose >/dev/null 2>&1; then DC="docker-compose";
else die "neither 'docker compose' nor 'docker-compose' found"; fi

# ── STEP 1: verify BEFORE apply. Non-negotiable, un-skippable. ────────────────────────────────────────
VERIFY="$SCRIPT_DIR/verify-package.sh"
[ -x "$VERIFY" ] || [ -f "$VERIFY" ] || die "verify-package.sh not alongside installer at $SCRIPT_DIR"
log "STEP 1/4: verifying package integrity before any load/apply"
sh "$VERIFY" "$PKG_DIR" || die "package failed integrity verification — refusing to install (see failures above)"

# ── Version bookkeeping: read the package's declared version, and the currently-installed one (if any),
#    so we can enforce the idempotency + never-same-version-rollback rules. ────────────────────────────
pkg_version=$(sed -n 's/^version:[[:space:]]*//p' "$PKG_DIR/VERSION" 2>/dev/null | head -n1)
[ -n "$pkg_version" ] || pkg_version="(unversioned)"
STATE_DIR="${HEROS_STATE_DIR:-/var/lib/heros-deploy}"
STATE_FILE="$STATE_DIR/installed.version"
installed_version=""
[ -f "$STATE_FILE" ] && installed_version=$(head -n1 "$STATE_FILE" 2>/dev/null || true)

# Rollback rule (Decision 7): re-applying the PRIOR package is the rollback; it must NOT be the same
# version as what is installed — a same-version "fallback" is exactly the poisoned chain we forbid.
if [ "$ROLLBACK" = "1" ]; then
  log "ROLLBACK requested: re-applying prior package '$pkg_version' (currently installed: '${installed_version:-none}')"
  if [ -n "$installed_version" ] && [ "$pkg_version" = "$installed_version" ]; then
    die "rollback target '$pkg_version' EQUALS the installed version — rollback re-applies a DIFFERENT (prior) package, never a same-version fallback (Decision 7)"
  fi
fi

# Idempotency signal: a re-install of the already-installed version says "already present" EXACTLY ONCE,
# then still runs the idempotent apply (which converges to a no-op). No master-password re-prompt exists.
if [ "$ROLLBACK" = "0" ] && [ -n "$installed_version" ] && [ "$pkg_version" = "$installed_version" ]; then
  log "already present: version '$pkg_version' is installed — converging (expect no changes; no re-prompt for any secret)"
fi

# ── STEP 2: load every saved image. `docker load` is idempotent — loading a digest already present is a
#    no-op layer-wise. No public egress happens; everything comes from the package. ────────────────────
log "STEP 2/4: docker load the packaged image set (no egress)"
loaded=0
for tar in "$PKG_DIR"/images/*.tar; do
  [ -f "$tar" ] || die "no image tars found under $PKG_DIR/images — package is empty or corrupt"
  log "  docker load < $(basename "$tar")"
  docker load -i "$tar" || die "docker load failed for $tar"
  loaded=$((loaded + 1))
done
log "  loaded $loaded image archive(s)"

# ── STEP 3: apply. Two --env-file inputs, on purpose (mirrors the compose header): the pinned image set,
#    and the operator's SECRET env. `up -d` is the one converging verb for install/upgrade/rollback. ──
set -- --env-file "$IMAGES_ENV" --env-file "$ENV_FILE" -f "$COMPOSE_PLATFORM"
if [ "$WITH_ADMIN" = "1" ]; then
  [ -f "$COMPOSE_ADMIN" ] || die "--with-admin given but $COMPOSE_ADMIN is not in the package"
  [ -n "$ADMIN_ENV_FILE" ] || die "--with-admin needs --admin-env-file (or HEROS_ADMIN_ENV_FILE) — the admin credential lives OUTSIDE the package"
  [ -f "$ADMIN_ENV_FILE" ] || die "admin env file not found: $ADMIN_ENV_FILE"
  set -- "$@" --env-file "$ADMIN_ENV_FILE" -f "$COMPOSE_ADMIN"
fi
log "STEP 3/4: docker compose up -d (declarative-idempotent apply; unset required secret => refuse to start)"
# --remove-orphans so a service dropped between package versions is reconciled away (converge, not
# accumulate). NO teardown, NO down: upgrade is this same up -d against the newer package.
$DC "$@" up -d --remove-orphans || die "compose up failed — if a required secret was unset, compose refused to start and named it above"

# ── STEP 4: record the installed version (the compare basis for the next apply's idempotency/rollback
#    rules). Best-effort on the state dir; if we cannot record it, say so LOUDLY rather than pretend. ──
log "STEP 4/4: recording installed version"
if mkdir -p "$STATE_DIR" 2>/dev/null && printf '%s\n' "$pkg_version" > "$STATE_FILE" 2>/dev/null; then
  log "  recorded '$pkg_version' at $STATE_FILE"
else
  echo "install-airgapped: WARNING: could not write $STATE_FILE (set HEROS_STATE_DIR to a writable path)." >&2
  echo "install-airgapped: WARNING: the apply SUCCEEDED, but 'already present' / rollback-version checks won't have a basis next run." >&2
fi

log "DONE: applied package '$pkg_version'."
log "next: run the doctor to confirm the platform is healthy —"
log "  $SCRIPT_DIR/doctor.sh"
