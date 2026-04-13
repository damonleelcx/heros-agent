#!/usr/bin/env bash
# Double-click friendly: mark executable (chmod +x) or run from a terminal / desktop entry.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo ""
echo "=== Heros installer (Linux) ==="
echo "This will run: go install ./cmd/heros"
echo "Then add Go's bin directory to your PATH (same as: heros -add-path)."
echo ""

if ! command -v go >/dev/null 2>&1; then
  echo "[ERROR] Go is not on PATH."
  echo "Install Go from https://go.dev/dl/ then open a new terminal and run this script again."
  exit 1
fi

echo "Using:"
go version
echo ""

go install ./cmd/heros

GOBIN="$(go env GOBIN)"
if [[ -n "${GOBIN}" ]]; then
  BIN="${GOBIN}"
else
  BIN="$(go env GOPATH)/bin"
fi

if [[ ! -x "${BIN}/heros" ]]; then
  echo "[ERROR] Expected executable at ${BIN}/heros after install."
  exit 1
fi

echo ""
echo "Adding Go bin to your user shell config (heros -add-path)..."
"${BIN}/heros" -add-path

echo ""
echo "=== Done ==="
echo "Open a NEW terminal window, then run:  heros"
echo ""
