#!/usr/bin/env bash
set -euo pipefail

# Ubuntu uses the same installer flow as Linux.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "${SCRIPT_DIR}/Install-Heros-Linux.sh"
