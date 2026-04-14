#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo ""
echo "=== Heros Desktop installer (macOS) ==="
echo "This will run: go install ./cmd/heros-desktop"
echo "Then add Go's bin directory to your PATH (same as: heros-desktop -add-path)."
echo ""

if ! command -v go >/dev/null 2>&1; then
  echo "[ERROR] Go is not on PATH."
  echo "Install Go from https://go.dev/dl/ then open a new Terminal and run this script again."
  read -r -p "Press Enter to close..."
  exit 1
fi

echo "Using:"
go version
echo ""

go install ./cmd/heros-desktop

GOBIN="$(go env GOBIN)"
if [[ -n "${GOBIN}" ]]; then
  BIN="${GOBIN}"
else
  BIN="$(go env GOPATH)/bin"
fi

if [[ ! -x "${BIN}/heros-desktop" ]]; then
  echo "[ERROR] Expected executable at ${BIN}/heros-desktop after install."
  read -r -p "Press Enter to close..."
  exit 1
fi

echo ""
echo "Adding Go bin to your shell profile (heros-desktop -add-path)..."
"${BIN}/heros-desktop" -add-path

echo ""
echo "=== Done ==="
echo "Open a NEW terminal window, then run:  heros-desktop"
echo ""
read -r -p "Press Enter to close..."
