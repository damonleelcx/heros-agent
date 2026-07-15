#!/usr/bin/env bash
# Boot an ephemeral Postgres, apply 0001_p0_lineage, prove every constraint fires, tear down.
# Task 2.1 live verification. No sudo, no system Postgres install — uses whatever `initdb`/`pg_ctl`/
# `postgres` are on PATH (e.g. the zonky embedded binaries, or a real local install).
#
# Requires on PATH: initdb, pg_ctl, postgres.  Requires Python: pip install "psycopg[binary]".
#
# Usage:
#   PATH="/path/to/pg/bin:$PATH" bash db/migrations/postgres/run_pg_proof.sh
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
PROOF="${1:-prove_constraints.py}"   # which proof script to run (default: constraints)
WORK="$(mktemp -d)"
# Postgres caps the unix-socket dir path at ~103 bytes, so keep the socket dir short & separate.
SOCK="$(mktemp -d /tmp/p0pg.XXXX)"
PGDATA="$WORK/data"
export LC_ALL=C

cleanup() {
  pg_ctl -D "$PGDATA" -m immediate stop >/dev/null 2>&1 || true
  rm -rf "$WORK" "$SOCK"
}
trap cleanup EXIT

echo "== initdb =="
initdb -D "$PGDATA" -U postgres --auth=trust -E UTF8 >/dev/null
printf "\nunix_socket_directories = '%s'\nlisten_addresses = ''\n" "$SOCK" >> "$PGDATA/postgresql.conf"

echo "== start server =="
pg_ctl -D "$PGDATA" -l "$WORK/pg.log" -w start >/dev/null

echo "== run proof: $PROOF =="
PGHOST="$SOCK" PGUSER="postgres" PGDATABASE="postgres" python3 "$HERE/$PROOF"
rc=$?

echo "== stop server (cleanup via trap) =="
exit $rc
