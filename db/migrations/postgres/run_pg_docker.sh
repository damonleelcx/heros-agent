#!/usr/bin/env bash
# Boot an EPHEMERAL Postgres in Docker, run a command against it, tear it down.
#
# Sibling of run_pg_proof.sh, which needs initdb/pg_ctl/postgres on PATH. This one needs only Docker,
# which is the difference between "the live proof runs on the maintainer's machine" and "the live
# proof runs anywhere" — a proof nobody can run locally gets skipped in practice.
#
# Exports for the command it runs:
#   HEROS_TEST_POSTGRES_URL  — libpq URL (also PGHOST/PGPORT/PGUSER/PGPASSWORD/PGDATABASE)
#
# Usage:
#   bash db/migrations/postgres/run_pg_docker.sh go test -tags pgproof ./internal/registry/
#   bash db/migrations/postgres/run_pg_docker.sh psql -c '\dt'
set -euo pipefail

IMAGE="${PG_IMAGE:-postgres:16-alpine}"
NAME="heros-pgproof-$$"
PORT="${PG_PORT:-0}"   # 0 => ask the kernel for a free port

if [ "$#" -eq 0 ]; then
  echo "usage: $0 <command> [args...]" >&2
  exit 2
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not found. Install Docker, or use run_pg_proof.sh with initdb/pg_ctl on PATH." >&2
  exit 1
fi

cleanup() { docker rm -f "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "== start $IMAGE (container $NAME) =="
# --tmpfs: the whole cluster lives in RAM. It is ephemeral by construction, so a crashed run cannot
# leave a stale volume that a later run silently reuses and reads as a "fresh" database.
docker run -d --name "$NAME" \
  -e POSTGRES_PASSWORD=pgproof \
  -e POSTGRES_DB=pgproof \
  -e POSTGRES_USER=postgres \
  --tmpfs /var/lib/postgresql/data:rw \
  -p "127.0.0.1:${PORT}:5432" \
  "$IMAGE" >/dev/null

# Ask docker which host port it actually bound (PORT=0 => kernel-assigned), so concurrent runs and a
# developer's own Postgres on 5432 cannot collide.
HOST_PORT="$(docker port "$NAME" 5432/tcp | head -1 | sed 's/.*://')"
if [ -z "$HOST_PORT" ]; then
  echo "could not determine the mapped port" >&2
  docker logs "$NAME" >&2 || true
  exit 1
fi

echo "== wait for readiness on 127.0.0.1:$HOST_PORT =="
ready=""
for _ in $(seq 1 60); do
  if docker exec "$NAME" pg_isready -U postgres -q 2>/dev/null; then ready=1; break; fi
  sleep 0.5
done
if [ -z "$ready" ]; then
  echo "Postgres did not become ready in 30s" >&2
  docker logs "$NAME" >&2 || true
  exit 1
fi

export PGHOST=127.0.0.1 PGPORT="$HOST_PORT" PGUSER=postgres PGPASSWORD=pgproof PGDATABASE=pgproof
export HEROS_TEST_POSTGRES_URL="postgres://postgres:pgproof@127.0.0.1:${HOST_PORT}/pgproof?sslmode=disable"

echo "== run: $* =="
"$@"
