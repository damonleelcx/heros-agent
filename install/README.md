# Installing Heros

## Prebuilt binaries (no Go)

Official builds are attached to **GitHub Releases** when a maintainer pushes a tag like **`v0.1.0`** (workflow: [`.github/workflows/release.yml`](../.github/workflows/release.yml)).

| Asset | Use case |
|-------|----------|
| **`heros-<tag>-windows-amd64.zip`** (and **`-arm64`**) | Unzip, run **`heros.exe`**. Optional: **`heros.exe -add-path`** once so that folder is on your user **PATH**. |
| **`heros-<tag>-linux-amd64.tar.gz`** / **`-arm64`** | Extract, **`chmod +x heros-...`**, run **`./heros-...`** (or rename to **`heros`**). Optional: **`./heros -add-path`**. |
| **`heros-<tag>-x86_64.AppImage`** | **`chmod +x`** then run the AppImage (amd64 only). |

Verify downloads with **`SHA256SUMS-<tag>.txt`**.

**In-app auto-update** is not implemented; upgrade by downloading a newer release.

**`heros -version`** prints the release tag baked in at build time.

---

## Clickable installers (need Go)

These install **`heros`** from a **local clone** (they run `go install ./cmd/heros` at the repo root, then **`heros -add-path`**). You must have **[Go 1.22+](https://go.dev/dl/)** on your **PATH**.

| OS | What to use |
|----|-------------|
| **Windows** | Double-click **`Install-Heros-Windows.cmd`**. |
| **Linux** | In a terminal: `chmod +x install/Install-Heros-Linux.sh install/generate-linux-desktop.sh`, then either run `./install/Install-Heros-Linux.sh`, or run `bash install/generate-linux-desktop.sh` and double-click **Install Heros** on your desktop. |

**Published module** (no clone): use `go install github.com/heros-foreal/agentd/cmd/heros@latest` and **`heros -add-path`** as in the root **README**.

After any PATH change, **open a new terminal** so **`PATH`** is picked up.
