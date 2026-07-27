#!/bin/sh
# Operator preflight / diagnostic + backup/restore (P19 §7.4, deployment-topology: "a `doctor`/preflight
# check and backup + restore procedures an operator runs WITHOUT the platform team").
#
# WHY A DOCTOR SHIPS IN THE PACKAGE. The self-service acceptance test (NFR7) is the pass/fail judge of the
# whole air-gapped delivery: an engineer new to the system must be able to install, operate, and diagnose
# from the package + docs alone. That means the answer to "is it healthy? why not? how do I back up? how
# do I restore?" cannot be "open a ticket with us" — it has to be a script in their hands. This is it.
#
# WHY IT READS ENDPOINTS, NOT A DASHBOARD. Health in this platform is an externally-readable, AGGREGATED
# endpoint, never a UI (a rulebook invariant). So the doctor curls the same `/readyz` the orchestrator's
# readiness probe reads — /readyz is the ONE truth that names its degraded component. A green console page
# proves the console renders; only /readyz proves the platform's dependencies are actually reachable. We
# also hit /healthz (liveness: the process serves) and each console's /api/health (a dead BFF is a dead
# container — ADR-006 — and must show as such), because "the process is up" and "its dependencies are up"
# are two different questions and conflating them is how a half-dead stack looks healthy.
#
# WHY THE BACKUP CHECK IS FIRST-CLASS. Postgres is an accepted single point of failure ONLY BECAUSE backup
# is real (Decision 6). "Is there a recent, NON-ZERO dump?" is therefore a health question, not a nicety —
# an infinite-RPO deploy has silently voided the premise that made single-Postgres acceptable. So the
# doctor checks a real, non-empty dump exists, and --backup / --restore give the operator the two verbs
# that keep that premise true, reusing pg-backup.sh's zero-byte-poison discipline verbatim.
#
# 🔴 NEVER LEAVE A ZERO-BYTE DUMP. --backup delegates to pg-backup.sh's RUN_ONCE=1 path, which deletes any
# partial/too-small dump on every failure — a plausible-looking empty backup poisons the rollback chain.
# --restore refuses an empty/missing dump before it touches the database. No failure path here is silent:
# every one is stderr + non-zero (八级法则: 安全 > 稳定; a lie about backup state is the dangerous outcome).
#
# USAGE:
#   doctor.sh                         # run all preflight/health checks; PASS/FAIL summary; non-zero on any FAIL
#   doctor.sh --with-admin            # also check the operator console origin (:4310 /api/health)
#   doctor.sh --backup                # take ONE backup now (pg-backup.sh RUN_ONCE=1 discipline)
#   doctor.sh --restore <dumpfile>    # 🔴 DESTRUCTIVE: pg_restore the dump into the running postgres
# ENV OVERRIDES (all optional; sane defaults matching the compose ports):
#   DOCTOR_HOST (127.0.0.1)  AGENTD_PORT (4321)  CONSOLE_PORT (4320)  ADMIN_CONSOLE_PORT (4310)
#   HEROS_PG_CONTAINER / HEROS_BACKUP_CONTAINER  (override container auto-discovery)
#   BACKUP_DIR (/var/backups/heros — where the backup service writes, checked via the backup container)
set -eu

# ── Output + counters. Every check reports PASS/FAIL/SKIP; the summary is the operator's verdict. ─────
PASS_N=0; FAIL_N=0; SKIP_N=0
pass() { echo "  [PASS] $*";               PASS_N=$((PASS_N + 1)); }
fail() { echo "  [FAIL] $*" >&2;           FAIL_N=$((FAIL_N + 1)); }
skip() { echo "  [SKIP] $*";               SKIP_N=$((SKIP_N + 1)); }
die()  { echo "doctor: FATAL: $*" >&2; exit 1; }
log()  { echo "doctor: $*"; }

# ── Args. ─────────────────────────────────────────────────────────────────────────────────────────────
MODE="check"; RESTORE_FILE=""; WITH_ADMIN=0
while [ $# -gt 0 ]; do
  case "$1" in
    --with-admin) WITH_ADMIN=1 ;;
    --backup)     MODE="backup" ;;
    --restore)    MODE="restore"; shift; [ $# -gt 0 ] || die "--restore needs a dump file path"; RESTORE_FILE="$1" ;;
    -h|--help)    sed -n '1,40p' "$0"; exit 0 ;;
    *)            die "unknown argument: $1 (see --help)" ;;
  esac
  shift
done

command -v docker >/dev/null 2>&1 || die "docker not found — the doctor needs it to reach the running stack"

# ── Container discovery. Compose v2 names look like <project>-postgres-1 / <project>-postgres-backup-1.
#    We find the DB container (postgres, but NOT the backup one) and the backup container by name. An
#    operator can override either via env when their project name is unusual. ──────────────────────────
find_pg_container() {
  [ -n "${HEROS_PG_CONTAINER:-}" ] && { echo "$HEROS_PG_CONTAINER"; return 0; }
  docker ps --format '{{.Names}}' 2>/dev/null | grep postgres | grep -v postgres-backup | head -n1
}
find_backup_container() {
  [ -n "${HEROS_BACKUP_CONTAINER:-}" ] && { echo "$HEROS_BACKUP_CONTAINER"; return 0; }
  docker ps --format '{{.Names}}' 2>/dev/null | grep postgres-backup | head -n1
}

# ── HTTP health helper. Uses the status-code form (not curl -f) so we can ALWAYS surface the response
#    body on failure — /readyz names its degraded component in that body, and swallowing it would hide the
#    single most useful diagnostic. ────────────────────────────────────────────────────────────────────
HOST="${DOCTOR_HOST:-127.0.0.1}"
TIMEOUT="${DOCTOR_TIMEOUT:-5}"
check_http() {
  label="$1"; url="$2"
  command -v curl >/dev/null 2>&1 || { fail "$label: curl not found (cannot probe $url)"; return; }
  body_file=$(mktemp 2>/dev/null) || { fail "$label: cannot create temp file"; return; }
  code=$(curl -s -o "$body_file" -w '%{http_code}' --max-time "$TIMEOUT" "$url" 2>/dev/null || echo "000")
  case "$code" in
    2??) pass "$label ($url) -> HTTP $code" ;;
    000) fail "$label ($url): no response (connection refused / timed out) — is the service up and the port published?" ;;
    *)   fail "$label ($url) -> HTTP $code"
         # Show the named-degraded body (e.g. /readyz {"degraded":["admin_console"]}) — this is the point.
         if [ -s "$body_file" ]; then echo "         response: $(head -c 600 "$body_file")" >&2; fi ;;
  esac
  rm -f "$body_file"
}

# ═══ MODE: --backup ═══ take a single, verified backup now, reusing the shipped discipline. ────────────
if [ "$MODE" = "backup" ]; then
  bc=$(find_backup_container) || true
  [ -n "$bc" ] || die "no running postgres-backup container found (set HEROS_BACKUP_CONTAINER, or bring the stack up first)"
  log "taking a one-shot backup via '$bc' (pg-backup.sh RUN_ONCE=1: fails loud on empty, deletes partials)"
  # RUN_ONCE=1 makes pg-backup.sh do a single dump and EXIT with the dump's real status — so this whole
  # command's exit status IS the backup's success. The container already has PG* env + the script mounted.
  docker exec -e RUN_ONCE=1 "$bc" sh /opt/heros/pg-backup.sh || die "backup FAILED (see the loud pg-backup output above) — no zero-byte file was left behind"
  log "backup OK."
  exit 0
fi

# ═══ MODE: --restore ═══ 🔴 DESTRUCTIVE. pg_restore the given dump into the running database. ──────────
if [ "$MODE" = "restore" ]; then
  [ -f "$RESTORE_FILE" ] || die "restore dump not found: $RESTORE_FILE"
  # Never restore from a zero-byte / truncated dump — that is the poisoned link Decision 6 warns of.
  size=$(wc -c < "$RESTORE_FILE" 2>/dev/null || echo 0)
  [ "$size" -ge "${RESTORE_MIN_BYTES:-512}" ] || die "refusing to restore from '$RESTORE_FILE' — only ${size} bytes (looks empty/truncated; a poisoned backup must not overwrite live data)"
  pc=$(find_pg_container) || true
  [ -n "$pc" ] || die "no running postgres container found (set HEROS_PG_CONTAINER, or bring the stack up first)"
  echo "doctor: 🔴 RESTORE is DESTRUCTIVE — it will --clean and reload the database in container '$pc'" >&2
  echo "doctor: 🔴 restoring from: $RESTORE_FILE (${size} bytes)" >&2
  # Stream the dump into pg_restore INSIDE the container. We reference the container's OWN credential env
  # BY NAME (never its value); the secret never appears in this script, its args, or any log line.
  # --clean --if-exists so the reload is idempotent-ish (drops-then-creates); --exit-on-error so a partial
  # restore fails LOUD instead of leaving a half-restored database looking fine.
  if docker exec -i "$pc" sh -c 'PGPASSWORD="$POSTGRES_PASSWORD" pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --exit-on-error --no-owner' < "$RESTORE_FILE"; then
    log "restore OK from $RESTORE_FILE"
    exit 0
  else
    die "pg_restore FAILED — the database may be partially restored; investigate before serving (this is LOUD on purpose)"
  fi
fi

# ═══ MODE: check ═══ the preflight/diagnostic. ────────────────────────────────────────────────────────
log "running preflight + health checks (host=$HOST)"

# ── 1) Tooling present. ───────────────────────────────────────────────────────────────────────────────
echo "docker / compose tooling:"
if command -v docker >/dev/null 2>&1; then pass "docker present ($(docker --version 2>/dev/null || echo '?'))"; else fail "docker not found"; fi
if docker compose version >/dev/null 2>&1; then pass "docker compose (v2 plugin) present";
elif command -v docker-compose >/dev/null 2>&1; then pass "docker-compose (legacy) present";
else fail "neither 'docker compose' nor 'docker-compose' found"; fi
if command -v curl >/dev/null 2>&1; then pass "curl present"; else fail "curl not found (health probes cannot run)"; fi

# ── 2) Service health via endpoints (the aggregated truth, plus liveness and each console). ───────────
echo "platform health endpoints:"
check_http "agentd liveness  /healthz" "http://$HOST:${AGENTD_PORT:-4321}/healthz"
check_http "agentd readiness /readyz (AGGREGATED — names any degraded component)" "http://$HOST:${AGENTD_PORT:-4321}/readyz"
check_http "customer console /api/health" "http://$HOST:${CONSOLE_PORT:-4320}/api/health"
if [ "$WITH_ADMIN" = "1" ]; then
  check_http "operator console /api/health" "http://$HOST:${ADMIN_CONSOLE_PORT:-4310}/api/health"
else
  skip "operator console /api/health (pass --with-admin to include the second origin)"
fi

# ── 3) The backup precondition: a recent, NON-ZERO dump exists. Checked through the backup container so
#    the doctor needs no host mount knowledge and no secrets. ──────────────────────────────────────────
echo "postgres backup precondition (Decision 6 — the SPOF is accepted only because backup is real):"
bc=$(find_backup_container) || true
if [ -z "$bc" ]; then
  fail "no running postgres-backup container found — backup automation is NOT running (SPOF premise voided). Set HEROS_BACKUP_CONTAINER or bring the stack up."
else
  bdir="${BACKUP_DIR:-/var/backups/heros}"
  # Newest dump + its byte size, computed inside the container. A missing dir, no dumps, or a zero-byte
  # newest dump are all FAILs — an empty backup is not a backup.
  newest=$(docker exec "$bc" sh -c "ls -1t '$bdir'/*.dump 2>/dev/null | head -n1" 2>/dev/null || true)
  if [ -z "$newest" ]; then
    fail "no *.dump found in $bdir inside '$bc' — no backup has completed yet. Run: doctor.sh --backup"
  else
    bsize=$(docker exec "$bc" sh -c "wc -c < '$newest' 2>/dev/null || echo 0" 2>/dev/null || echo 0)
    # Guard against a non-numeric result before the arithmetic test.
    case "$bsize" in ''|*[!0-9]*) bsize=0 ;; esac
    if [ "$bsize" -ge "${BACKUP_MIN_BYTES:-512}" ]; then
      pass "newest backup: $newest (${bsize} bytes)"
    else
      fail "newest backup $newest is only ${bsize} bytes — a zero-byte/truncated dump is a POISONED backup, not a real one"
    fi
  fi
fi

# ── Summary + verdict. ────────────────────────────────────────────────────────────────────────────────
echo "------------------------------------------------------------"
echo "doctor summary: PASS=$PASS_N  FAIL=$FAIL_N  SKIP=$SKIP_N"
if [ "$FAIL_N" -gt 0 ]; then
  echo "doctor: RESULT = FAIL ($FAIL_N check(s) failed) — the platform is NOT healthy; see the [FAIL] lines above." >&2
  exit 1
fi
echo "doctor: RESULT = PASS — all checks green."
exit 0
