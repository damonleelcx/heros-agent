#!/usr/bin/env bash
set -euo pipefail

# Ubuntu uses the same desktop installer flow as Linux.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "${SCRIPT_DIR}/Install-Heros-Desktop-Linux.sh"
