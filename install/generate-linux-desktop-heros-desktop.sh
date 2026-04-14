#!/usr/bin/env bash
# Writes a double-clickable .desktop launcher on your Desktop (or XDG_DESKTOP_DIR).
# Run once after cloning:  bash install/generate-linux-desktop-heros-desktop.sh
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="${XDG_DESKTOP_DIR:-$HOME/Desktop}"
OUT="${OUT_DIR}/Install Heros Desktop.desktop"

mkdir -p "${OUT_DIR}"

cat > "${OUT}" << EOF
[Desktop Entry]
Version=1.0
Type=Application
Name=Install Heros Desktop
GenericName=Heros Desktop installer
Comment=Requires Go 1.22+. Runs go install and updates PATH (heros-desktop -add-path).
Path=${DIR}
Exec=bash -c './Install-Heros-Desktop-Linux.sh; echo; read -rp "Press Enter to close..."'
Terminal=true
Categories=Development;
Keywords=heros;desktop;go;install;
EOF

chmod +x "${OUT}"

if command -v gio >/dev/null 2>&1; then
  gio set "${OUT}" metadata::trusted true 2>/dev/null || true
fi

echo "Wrote: ${OUT}"
echo "Double-click \"Install Heros Desktop\" on your desktop. If your desktop environment asks to trust the launcher, approve it."
