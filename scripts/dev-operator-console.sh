#!/usr/bin/env bash
# Starts the operator console's dev server against the wired proof stack.
#
# It exists because the console's BFF needs two environment values — the admin API's base URL and the
# platform credential — and a launch configuration carries a command, not an environment. Putting them
# in a script keeps the credential out of a JSON file and gives the two servers one documented entry
# point. Both values here are the DEMO's, printed by cmd/proof/operatorconsole; neither authenticates
# against anything real.
set -euo pipefail
cd "$(dirname "$0")/../web/admin-console"
export ADMIN_API_BASE="${ADMIN_API_BASE:-http://127.0.0.1:4340}"
export ADMIN_PLATFORM_CREDENTIAL="${ADMIN_PLATFORM_CREDENTIAL:-p8hermes-demo-platform-credential-do-not-ship}"
exec npx next dev --port "${PORT:-4341}"
