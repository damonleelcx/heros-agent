# Runs ON the k3s node, shipped there by deploy/bootstrap-db.sh over SSM.
#
# Expects SECRET_ID and DRY_RUN to have been set by lines prepended before this file. Not executable
# on its own on purpose: it has no shebang and no defaults, so it cannot be run somewhere it would
# silently target the wrong instance.
set -euo pipefail

dsn=$(aws secretsmanager get-secret-value --secret-id "$SECRET_ID" --region us-east-1 \
        --query SecretString --output text | jq -r '."database-url"')
if [ -z "$dsn" ] || [ "$dsn" = "null" ]; then
  echo "no database-url in $SECRET_ID" >&2; exit 1
fi

# 🔴 The DSN is the single source of truth for all four values. Deriving role, password and database
# name from the string the deployment actually connects with means the database this script creates
# and the database the pod opens cannot disagree — the failure mode when they are typed twice is a
# deployment that boots against an empty database it just created itself.
rest=${dsn#*://}
creds=${rest%%@*}
ROLE=${creds%%:*}
ROLE_PASSWORD=${creds#*:}
hostpath=${rest#*@}
DB=${hostpath#*/}
DB=${DB%%\?*}

# The SQL below single-quotes the password. Rather than implement Postgres string escaping, refuse
# anything that would need it — the generator produces alphanumerics, so this only fires if somebody
# changes that, and it fires loudly instead of creating a role with a truncated password.
case "$ROLE_PASSWORD" in
  *[!A-Za-z0-9]*)
    echo "the password in database-url is not alphanumeric; this script does not escape it" >&2
    exit 1
    ;;
esac
echo "role=$ROLE database=$DB password=<${#ROLE_PASSWORD} chars>"

psql_super() {
  k3s kubectl -n heros exec -i postgres-0 -- psql -v ON_ERROR_STOP=1 -U heros -d "$1" -qtA
}

if [ "$DRY_RUN" = 1 ]; then
  echo "--- dry run; the statements that would run:"
  echo "  CREATE ROLE $ROLE LOGIN PASSWORD '<redacted>'   (ALTER, if the role exists)"
  echo "  CREATE DATABASE $DB OWNER $ROLE                 (skipped, if it exists)"
  echo "  REVOKE CONNECT ON DATABASE $DB FROM PUBLIC"
  echo "  GRANT  CONNECT ON DATABASE $DB TO $ROLE"
  exit 0
fi

# 🔴 The password reaches psql on STDIN, never in argv: `psql -v pw=…` would put it in the node's
# process list for the duration of the call.
psql_super postgres <<SQL
DO \$do\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '$ROLE') THEN
    CREATE ROLE $ROLE LOGIN PASSWORD '$ROLE_PASSWORD';
  ELSE
    ALTER ROLE $ROLE LOGIN PASSWORD '$ROLE_PASSWORD';
  END IF;
END
\$do\$;
SQL
echo "role ok"

# CREATE DATABASE runs in no transaction and in no DO block, so it is guarded by a lookup instead.
exists=$(echo "SELECT 1 FROM pg_database WHERE datname='$DB';" | psql_super postgres)
if [ -z "$exists" ]; then
  echo "CREATE DATABASE $DB OWNER $ROLE;" | psql_super postgres
  echo "database created"
else
  echo "database already present - left alone"
fi

psql_super "$DB" <<SQL
REVOKE CONNECT ON DATABASE $DB FROM PUBLIC;
GRANT CONNECT ON DATABASE $DB TO $ROLE;
SQL

# Prove the lockdown rather than assert it. A script that only prints "done" is how the previous
# instance of exactly this mistake survived on this instance unnoticed.
echo "--- verifying isolation"
# 🔴 `postgres` is EXCLUDED, and the exclusion is the honest form of this check rather than a hole
# in it. The maintenance database grants CONNECT to PUBLIC cluster-wide — every role on this
# instance can open it, including opportunity-bridge's, and that predates this deployment. It holds
# zero non-system tables, so what it exposes is the shared catalogs, which are readable from inside
# any database anyway.
#
# Revoking it would change a grant shared with another product, which is not this script's to do.
# What matters, and what is asserted below, is that this role cannot open ANOTHER PRODUCT'S
# database. Listing `postgres` as a failure here would make the check permanently red and therefore
# permanently ignored — which is how a real finding gets lost among an expected one.
others=$(echo "SELECT datname FROM pg_database WHERE datistemplate=false AND datname NOT IN ('$DB','postgres');" | psql_super postgres)
fail=0
for other in $others; do
  # No -i, and stdin closed: see the note in ssm.sh about `kubectl exec -i` eating the script.
  if k3s kubectl -n heros exec postgres-0 -- \
       env PGPASSWORD="$ROLE_PASSWORD" psql -U "$ROLE" -h 127.0.0.1 -d "$other" -qtAc 'SELECT 1' </dev/null >/dev/null 2>&1; then
    echo "FAIL: $ROLE can open database $other" >&2
    fail=1
  else
    echo "ok: $ROLE cannot open $other"
  fi
done
if k3s kubectl -n heros exec postgres-0 -- \
     env PGPASSWORD="$ROLE_PASSWORD" psql -U "$ROLE" -h 127.0.0.1 -d "$DB" -qtAc 'SELECT 1' </dev/null >/dev/null 2>&1; then
  echo "ok: $ROLE can open $DB"
else
  echo "FAIL: $ROLE cannot open its OWN database $DB" >&2
  fail=1
fi
exit $fail
