#!/bin/sh
# Postgres backup for the platform deploy (P19 Decision 6). Accepting single-Postgres as a SPOF is a
# sound stability call ONLY IF disaster recovery from backup is real — so this ships WITH the deploy,
# not as a follow-up.
#
# 🔴 THE DUMP FAILS LOUD ON EMPTY. A `pg_dump` that fails after the shell has already created the output
# file leaves a plausible-looking zero-byte backup that silently poisons the rollback chain. EVERY
# failure path here deletes the partial file, and a zero-byte or too-small dump is a FAILURE, not a
# success — the restore chain can never form from a lie.
#
# It runs as a long-lived container that wakes on a cron-shaped cadence (BACKUP_CRON is documentation
# of intent; the loop sleeps to the next day's HH:MM). Restore is documented in deploy/README.md and is
# a plain `pg_restore` — no bespoke tool an operator would have to trust.
set -eu

BACKUP_DIR="${BACKUP_DIR:-/var/backups/heros}"
RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-14}"
# Minimum plausible dump size in bytes: an empty database still dumps its schema, so anything under this
# is a truncated/failed dump masquerading as success.
MIN_BYTES="${BACKUP_MIN_BYTES:-512}"

mkdir -p "$BACKUP_DIR"

backup_once() {
  ts="$(date -u +%Y%m%dT%H%M%SZ)"
  out="$BACKUP_DIR/heros-${PGDATABASE:-heros}-${ts}.dump"
  tmp="${out}.partial"

  # pg_dump to a temp file first; the final name only ever exists for a COMPLETE, VERIFIED dump.
  if ! pg_dump --format=custom --file="$tmp" 2>"${tmp}.err"; then
    echo "backup: pg_dump FAILED at ${ts}:" >&2
    cat "${tmp}.err" >&2 || true
    rm -f "$tmp" "${tmp}.err"          # delete the partial — no zero-byte poison
    return 1
  fi

  size="$(wc -c < "$tmp" 2>/dev/null || echo 0)"
  if [ "$size" -lt "$MIN_BYTES" ]; then
    echo "backup: dump at ${ts} is only ${size} bytes (< ${MIN_BYTES}); treating as FAILED" >&2
    rm -f "$tmp" "${tmp}.err"          # delete the too-small file — it is not a real backup
    return 1
  fi

  rm -f "${tmp}.err"
  mv "$tmp" "$out"                       # atomically promote the verified dump to its final name
  echo "backup: OK ${out} (${size} bytes)"

  # Retention: delete dumps older than N days. Never touches a .partial (there are none after success).
  find "$BACKUP_DIR" -name 'heros-*.dump' -type f -mtime "+${RETENTION_DAYS}" -print -delete 2>/dev/null || true
}

# One-shot mode for the doctor/preflight and for `docker compose run`: RUN_ONCE=1 does a single backup
# and exits with the dump's real status, so a caller can gate on it.
if [ "${RUN_ONCE:-0}" = "1" ]; then
  backup_once
  exit $?
fi

echo "backup: service up; schedule=${BACKUP_CRON:-daily} retention=${RETENTION_DAYS}d dir=${BACKUP_DIR}"
while true; do
  # Sleep to the next 03:17 UTC (or whatever the operator set as the daily minute-of-day). Kept simple
  # and dependency-free: no cron daemon inside the container, just a computed sleep.
  now_s="$(date -u +%s)"
  target_s="$(date -u -d 'tomorrow 03:17' +%s 2>/dev/null || echo $((now_s + 86400)))"
  sleep "$(( target_s - now_s ))" || sleep 86400
  backup_once || echo "backup: run failed; will retry next cycle (this is LOUD on purpose)" >&2
done
