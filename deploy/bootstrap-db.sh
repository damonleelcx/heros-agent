#!/usr/bin/env bash
# Create the eval deployment's database and role on the SHARED Postgres instance.
#
# # Why a separate database and role rather than a schema in an existing one
#
# The Postgres instance in the `heros` namespace serves more than one product. A role per product
# means a credential leak in one is not read access to the others, and it means "remove this product"
# is one statement rather than an audit of which tables belonged to whom. This is the arrangement
# already used by opportunity-bridge; it is repeated here rather than invented.
#
# # Why the work happens on the node
#
# There is no kubeconfig on any developer machine — the cluster is reachable only over SSM — so a
# script that shells out to a local `kubectl` is a script nobody can run.
#
# # 🔴 Why the password is fetched on the node and never passed in
#
# Everything sent through `aws ssm send-command` is retained and readable afterwards with
# `get-command-invocation`. A password interpolated into the script body would be a password in an
# audit log. The node holds the `heros-vm` instance role, so it reads the DSN from Secrets Manager
# itself and the credential never leaves AWS.
#
# Idempotent. `--dry-run` prints the statements without running them.
set -euo pipefail

SECRET_ID=${SECRET_ID:-heros/eval}
DRY_RUN=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    -h|--help) sed -n '2,25p' "$0"; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

HERE=$(cd "$(dirname "$0")" && pwd)

# The remote half is a separate file rather than a here-doc inside a command substitution: the only
# bash on macOS is 3.2, which mis-parses `$(cat <<'EOF' … )` when the body contains unbalanced
# parentheses — and a shell script full of SQL always does.
{
  printf 'SECRET_ID=%s\nDRY_RUN=%s\n' "$SECRET_ID" "$DRY_RUN"
  cat "$HERE/bootstrap-db.remote.sh"
} | "$HERE/ssm.sh"
