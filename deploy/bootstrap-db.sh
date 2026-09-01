#!/usr/bin/env bash
# Create the eval deployment's database and role on the SHARED Postgres instance.
#
# # Why a separate database and role rather than a schema in an existing one
#
# The instance in the `heros` namespace now serves more than one product. A role per product means a
# credential leak in one is not read access to the others, and it means "drop this product" is one
# statement rather than an audit of which tables belonged to whom. This is the arrangement already
# used by opportunity-bridge, and it is repeated here rather than invented.
#
# 🔴 REVOKE CONNECT FROM PUBLIC IS NOT OPTIONAL AND IS NOT BELT-AND-BRACES.
# Postgres grants CONNECT on a new database to PUBLIC, and every role inherits PUBLIC. Without the
# revoke below, this deployment's role can open every other database on the instance — measured
# previously on this very instance, where the opportunity-bridge role could open `heros` and
# enumerate its 191 tables. `REVOKE ... FROM <role>` does NOT fix that: the grant is not on the role,
# so revoking it from the role removes nothing and reports success.
#
# Idempotent: safe to run repeatedly. Supports --dry-run.
set -euo pipefail

DB=${DB:-heros_eval}
ROLE=${ROLE:-heros_eval}
NAMESPACE=${NAMESPACE:-heros}
POD=${POD:-postgres-0}
SUPERUSER=${SUPERUSER:-heros}
DRY_RUN=0

for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    -h|--help) sed -n '2,25p' "$0"; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

if [[ -z "${ROLE_PASSWORD:-}" ]]; then
  echo "ROLE_PASSWORD is not set. Generate one and put it in the DSN stored at heros/eval." >&2
  exit 2
fi

psql_super() {
  # -v ON_ERROR_STOP=1 so a failed statement stops the script instead of being reported as success
  # by the last statement in the batch.
  kubectl -n "$NAMESPACE" exec -i "$POD" -- \
    psql -v ON_ERROR_STOP=1 -U "$SUPERUSER" -d "$1" -qtA
}

# 🔴 The password is passed as a bound parameter through a here-doc into psql's own quoting, never
# interpolated into a command line — a password in argv is visible in `ps` on the node.
sql_create=$(cat <<SQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '${ROLE}') THEN
    CREATE ROLE ${ROLE} LOGIN PASSWORD :'pw';
  ELSE
    ALTER ROLE ${ROLE} LOGIN PASSWORD :'pw';
  END IF;
END
\$\$;
SQL
)

# CREATE DATABASE cannot run inside a DO block or a transaction, so it is guarded by a lookup.
sql_db="SELECT 'CREATE DATABASE ${DB} OWNER ${ROLE}' WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = '${DB}')\\gexec"

sql_lockdown=$(cat <<SQL
REVOKE CONNECT ON DATABASE ${DB} FROM PUBLIC;
GRANT CONNECT ON DATABASE ${DB} TO ${ROLE};
SQL
)

if [[ "$DRY_RUN" == 1 ]]; then
  echo "--- would run against ${NAMESPACE}/${POD} as ${SUPERUSER}:"
  echo "[postgres] ${sql_create//:\'pw\'/'<ROLE_PASSWORD>'}"
  echo "[postgres] ${sql_db}"
  echo "[${DB}]  ${sql_lockdown}"
  echo "--- dry run: nothing was changed"
  exit 0
fi

printf '%s\n' "$sql_create" | psql_super postgres -v pw="$ROLE_PASSWORD"
printf '%s\n' "$sql_db" | psql_super postgres
printf '%s\n' "$sql_lockdown" | psql_super "$DB"

# Prove the lockdown rather than assert it: the role must be able to open its own database and must
# NOT be able to open the neighbours'. A script that only prints "done" is how the previous instance
# of this mistake survived.
echo "--- verifying"
for other in $(printf '%s\n' "SELECT datname FROM pg_database WHERE datistemplate = false AND datname <> '${DB}';" | psql_super postgres); do
  if PGPASSWORD="$ROLE_PASSWORD" kubectl -n "$NAMESPACE" exec -i "$POD" -- \
      psql -U "$ROLE" -d "$other" -h 127.0.0.1 -qtAc 'SELECT 1' >/dev/null 2>&1; then
    echo "FAIL: ${ROLE} can open database ${other}" >&2
    exit 1
  fi
  echo "ok: ${ROLE} cannot open ${other}"
done
echo "ok: ${DB} created and reachable only by ${ROLE}"
