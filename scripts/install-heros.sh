#!/usr/bin/env bash
# Install heros and add Go's bin dir to your user PATH (same as: go install && heros -add-path).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
go install ./cmd/heros
BIN="$(go env GOPATH)/bin"
if [[ ! -x "$BIN/heros" ]]; then
  echo "install-heros: expected executable at $BIN/heros" >&2
  exit 1
fi
exec "$BIN/heros" -add-path
