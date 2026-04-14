# Installing Heros

## Prebuilt binaries (no Go)

Official builds are attached to **GitHub Releases** when a maintainer pushes a tag like **`v0.1.0`** (workflow: [`.github/workflows/release.yml`](../.github/workflows/release.yml)).

| Asset | Use case |
|-------|----------|
| **`heros-<tag>-windows-amd64.zip`** (and **`-arm64`**) | Unzip, run **`heros.exe`**. Optional: **`heros.exe -add-path`** once so that folder is on your user **PATH**. |
| **`heros-<tag>-linux-amd64.tar.gz`** / **`-arm64`** | Extract, **`chmod +x heros-...`**, run **`./heros-...`** (or rename to **`heros`**). Optional: **`./heros -add-path`**. |
| **`heros-<tag>-x86_64.AppImage`** | **`chmod +x`** then run the AppImage (amd64 only). |

Verify downloads with **`SHA256SUMS-<tag>.txt`**.

Desktop GUI release assets are published by [`.github/workflows/release-desktop.yml`](../.github/workflows/release-desktop.yml):

| Asset | Use case |
|-------|----------|
| **`heros-desktop-<tag>-windows-amd64.zip`** | Unzip and run **`heros-desktop-<tag>-windows-amd64.exe`** (or rename to `heros-desktop.exe`). |
| **`heros-desktop-<tag>-linux-amd64.tar.gz`** | Extract and run **`./heros-desktop-<tag>-linux-amd64`**. |
| **`heros-desktop-<tag>-darwin-amd64.tar.gz`** / **`-darwin-arm64.tar.gz`** | Extract and run the matching binary on macOS (Intel or Apple Silicon). |
| **`SHA256SUMS-desktop-<tag>.txt`** | Verify desktop asset checksums. |

**In-app auto-update** is not implemented; upgrade by downloading a newer release.

**`heros -version`** prints the release tag baked in at build time.

---

## Clickable installers (need Go)

These install **`heros`** from a **local clone** (they run `go install ./cmd/heros` at the repo root, then **`heros -add-path`**). You must have **[Go 1.22+](https://go.dev/dl/)** on your **PATH**.

| OS | What to use |
|----|-------------|
| **Windows** | Double-click **`Install-Heros-Windows.cmd`**. |
| **Linux** | In a terminal: `chmod +x install/Install-Heros-Linux.sh install/generate-linux-desktop.sh`, then either run `./install/Install-Heros-Linux.sh`, or run `bash install/generate-linux-desktop.sh` and double-click **Install Heros** on your desktop. |
| **Ubuntu** | Run **`install/Install-Heros-Ubuntu.sh`** (wrapper of the Linux installer). |
| **macOS** | `chmod +x install/Install-Heros-macOS.command` then double-click it in Finder (or run from Terminal). |

**Published module** (no clone): use `go install github.com/heros-foreal/agentd/cmd/heros@latest` and **`heros -add-path`** as in the root **README**.

After any PATH change, **open a new terminal** so **`PATH`** is picked up.

---

## Heros Desktop installers (need Go)

These install the GUI app from a local clone (`go install ./cmd/heros-desktop`) and then run `heros-desktop -add-path`.

| OS | What to use |
|----|-------------|
| **Windows** | Double-click **`Install-Heros-Desktop-Windows.cmd`**. |
| **Linux / Ubuntu** | `chmod +x install/Install-Heros-Desktop-Linux.sh install/generate-linux-desktop-heros-desktop.sh`, then run `./install/Install-Heros-Desktop-Linux.sh`, or run `bash install/generate-linux-desktop-heros-desktop.sh` and double-click **Install Heros Desktop**. |
| **Ubuntu (explicit)** | Run **`install/Install-Heros-Desktop-Ubuntu.sh`** (wrapper of the Linux desktop installer). |
| **macOS** | `chmod +x install/Install-Heros-Desktop-macOS.command` then double-click it in Finder (or run from Terminal). |

Direct install command (any OS with Go 1.22+):

```bash
go install ./cmd/heros-desktop
heros-desktop -add-path
```
