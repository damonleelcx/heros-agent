#!/usr/bin/env bash
# Starts the wired operator-console proof stack (cmd/proof/operatorconsole) for local verification.
set -euo pipefail
cd "$(dirname "$0")/.."
export GOWORK=off
exec go run ./cmd/proof/operatorconsole -addr "127.0.0.1:${PORT:-4340}"
