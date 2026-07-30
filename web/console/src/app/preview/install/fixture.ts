import type { InstallView } from "@/lib/types.generated";

/**
 * PREVIEW_INSTALL is the distribution contract, snapshotted from the engine for the browser-checkable preview.
 *
 * A snapshot rather than a live fetch because /preview has no session and no BFF. The ACTUAL surface
 * (/app/install) reads the platform, so a stale snapshot here can only make the preview look wrong — never the
 * product. Regenerate with:
 *
 *   HEROS_INSTALL_DUMP=/tmp/install.json go test ./internal/api/ -run TestDumpInstallReadModel
 *
 * 🔴 `delivered` is absent, and that is the state worth previewing: no release has been published yet, so the
 * surface must render the ABSENCE of a trust posture rather than the ratified decision dressed up as one. The
 * second export supplies a delivered posture so both renderings can be seen side by side.
 */
export const PREVIEW_INSTALL: InstallView = {
  "matrix_version": "targets-01db3d54ab55cab1",
  "documented_release": "v0.20.0",
  "ratified_posture": "documented-clear",
  "targets": [
    {
      "key": "darwin/amd64",
      "platform": "macOS 12+ (Intel)",
      "arch": "amd64",
      "support": "shipped",
      "runner": "macos-15-intel",
      "channels": [
        "curl-sh",
        "homebrew",
        "container"
      ]
    },
    {
      "key": "darwin/arm64",
      "platform": "macOS 12+ (Apple silicon)",
      "arch": "arm64",
      "support": "shipped",
      "runner": "macos-15",
      "channels": [
        "curl-sh",
        "homebrew",
        "container"
      ]
    },
    {
      "key": "linux/amd64",
      "platform": "Linux glibc 2.31+ (Ubuntu 20.04+, Debian 11+, RHEL 9+)",
      "arch": "amd64",
      "support": "shipped",
      "runner": "ubuntu-22.04",
      "channels": [
        "curl-sh",
        "homebrew",
        "deb",
        "rpm",
        "container"
      ]
    },
    {
      "key": "linux/arm64",
      "platform": "Linux glibc 2.31+ on arm64",
      "arch": "arm64",
      "support": "shipped",
      "runner": "ubuntu-22.04-arm",
      "channels": [
        "curl-sh",
        "homebrew",
        "deb",
        "rpm",
        "container"
      ]
    },
    {
      "key": "windows/amd64",
      "platform": "Windows 10/11 (x64)",
      "arch": "amd64",
      "support": "shipped",
      "runner": "windows-2022",
      "channels": [
        "powershell",
        "scoop",
        "winget"
      ]
    },
    {
      "key": "windows/arm64",
      "platform": "Windows 11 (arm64)",
      "arch": "arm64",
      "support": "limit",
      "limit": "not built \u2014 no native windows/arm64 runner in the matrix, and the CGO tree-sitter frontends make a cross-build a different, less-tested artifact (D1)",
      "answer": "run the windows/amd64 build under Windows' x64 emulation, or ask for the row: adding it is a new runner, not a redesign",
      "channels": null
    },
    {
      "key": "linux/*",
      "platform": "Alpine / any musl Linux",
      "arch": "any",
      "support": "limit",
      "limit": "no native musl binary \u2014 the CLI links CGO tree-sitter frontends against glibc, and a glibc binary does not run on musl (D6)",
      "answer": "use the container image ghcr.io/damonleelcx/heros:<version>, which carries the same CLI in a glibc base",
      "channels": null
    }
  ],
  "channels": [
    {
      "id": "curl-sh",
      "label": "curl | sh",
      "oses": [
        "darwin",
        "linux"
      ],
      "delivered": true,
      "publication": "published",
      "verification": "the script verifies the sha256 against the release manifest and the manifest's ed25519 signature against a key pinned in the script, before placing anything on PATH; any failure is a hard stop",
      "manager_owned": false,
      "install": "curl -fsSL https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.20.0/scripts/install.sh | sh",
      "upgrade": "heros upgrade",
      "uninstall": "curl -fsSL https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.20.0/scripts/install.sh | HEROS_UNINSTALL=1 sh",
      "pin": "curl -fsSL .../install.sh | HEROS_VERSION=0.20.0 sh"
    },
    {
      "id": "powershell",
      "label": "PowerShell (irm | iex)",
      "oses": [
        "windows"
      ],
      "delivered": true,
      "publication": "published",
      "verification": "the script verifies the sha256 and then the ed25519 signature against a pinned key, before adding anything to PATH; any failure is a hard stop",
      "manager_owned": false,
      "install": "irm https://raw.githubusercontent.com/damonleelcx/heros-agent/v0.20.0/scripts/install.ps1 | iex",
      "upgrade": "heros upgrade",
      "uninstall": "$env:HEROS_UNINSTALL=1; irm .../install.ps1 | iex",
      "pin": "$env:HEROS_VERSION='0.20.0'; irm .../install.ps1 | iex"
    },
    {
      "id": "deb",
      "label": ".deb package",
      "oses": [
        "linux"
      ],
      "delivered": true,
      "publication": "published",
      "verification": "the package's sha256 is listed in the signed release manifest; verify it with the documented two steps before installing, since dpkg has no signature to check without a hosted repo",
      "manager_owned": true,
      "install": "curl -fsSLO https://github.com/damonleelcx/heros-agent/releases/download/v0.20.0/heros_0.20.0_amd64.deb && sudo dpkg -i heros_0.20.0_amd64.deb",
      "upgrade": "sudo dpkg -i heros_<newer>_amd64.deb",
      "uninstall": "sudo dpkg -r heros",
      "pin": "download the .deb for the exact version from that release and dpkg -i it"
    },
    {
      "id": "rpm",
      "label": ".rpm package",
      "oses": [
        "linux"
      ],
      "delivered": true,
      "publication": "published",
      "verification": "the package's sha256 is listed in the signed release manifest; verify it with the documented two steps before installing, since rpm has no signature to check without a hosted repo",
      "manager_owned": true,
      "install": "sudo rpm -i https://github.com/damonleelcx/heros-agent/releases/download/v0.20.0/heros-0.20.0.x86_64.rpm",
      "upgrade": "sudo rpm -U heros-<newer>.x86_64.rpm",
      "uninstall": "sudo rpm -e heros",
      "pin": "rpm -i the exact version's package URL"
    },
    {
      "id": "container",
      "label": "container image",
      "oses": [
        "darwin",
        "linux",
        "windows"
      ],
      "delivered": true,
      "publication": "published",
      "verification": "the image is digest-pinnable and is built in the same run from the same verified binary; pull by digest to pin exactly what you audited",
      "manager_owned": true,
      "install": "docker run --rm -v \"$PWD:/repo\" ghcr.io/damonleelcx/heros:0.20.0 discover --repo /repo",
      "upgrade": "docker pull ghcr.io/damonleelcx/heros:<newer>",
      "uninstall": "docker rmi ghcr.io/damonleelcx/heros:0.20.0",
      "pin": "docker run --rm ghcr.io/damonleelcx/heros:0.20.0   (or @sha256:\u2026 for a digest pin)"
    },
    {
      "id": "homebrew",
      "label": "Homebrew",
      "oses": [
        "darwin",
        "linux"
      ],
      "delivered": false,
      "publication": "pending-external-repo",
      "blocker": "the formula is generated from the signed manifest and attached to every Release, but the tap repository heros-foreal/homebrew-tap does not exist yet and pushing to it needs a token secret. Until then `brew install heros-foreal/tap/heros` would fail",
      "verification": "brew checks the sha256 in the formula, and the formula's sha256 is copied from the signed release manifest by the generator \u2014 so the chain to the signature is intact, with brew as the last link",
      "manager_owned": true,
      "install": "brew install heros-foreal/tap/heros",
      "upgrade": "brew upgrade heros",
      "uninstall": "brew uninstall heros",
      "pin": "brew install heros-foreal/tap/heros@0.20.0"
    },
    {
      "id": "scoop",
      "label": "Scoop",
      "oses": [
        "windows"
      ],
      "delivered": false,
      "publication": "pending-external-repo",
      "blocker": "the manifest is generated and attached to every Release, but the bucket repository heros-foreal/scoop-bucket does not exist yet and pushing to it needs a token secret",
      "verification": "scoop checks the hash in the manifest, and the generator copies that hash from the signed release manifest",
      "manager_owned": true,
      "install": "scoop bucket add heros https://github.com/heros-foreal/scoop-bucket; scoop install heros",
      "upgrade": "scoop update heros",
      "uninstall": "scoop uninstall heros",
      "pin": "scoop install heros@0.20.0"
    },
    {
      "id": "winget",
      "label": "winget",
      "oses": [
        "windows"
      ],
      "delivered": false,
      "publication": "pending-upstream-pr",
      "blocker": "the three-file winget manifest is generated and attached to every Release, but publication is a pull request into microsoft/winget-pkgs whose review and merge are not ours to schedule",
      "verification": "winget checks the InstallerSha256 in the manifest, which the generator copies from the signed release manifest",
      "manager_owned": true,
      "install": "winget install HerosForeal.Heros",
      "upgrade": "winget upgrade HerosForeal.Heros",
      "uninstall": "winget uninstall HerosForeal.Heros",
      "pin": "winget install HerosForeal.Heros --version 0.20.0"
    }
  ]
};

/**
 * PREVIEW_INSTALL_PUBLISHED is the posture a release ACTUALLY ships under the ratified (B) decision: the
 * checksum manifest is signed with the release key, and nothing is signed by the OS.
 *
 * It replaced a fully-signed "for comparison" fixture. That one existed to let a reader see, at a glance, what
 * the D3-(A) spend was buying — and on 2026-07-30 the owner reversed D3 to (B): no spend on signing. Keeping a
 * notarized rendering around would have shown a state this project does not intend to reach, which is the same
 * class of mistake as claiming it.
 *
 * What still has to be seen rather than asserted: that one EARNED claim and three UNEARNED ones are
 * distinguishable at a glance, by icon, by colour, and by the wording of the sentence. That is the whole job of
 * this section, and it is exercised better by the real posture than by an aspirational one.
 */
export const PREVIEW_INSTALL_PUBLISHED: InstallView = {
  ...PREVIEW_INSTALL,
  delivered: {
    version: "0.20.0-rc.1",
    signing_key_id: "heros-release-2026b",
    claims: [
      {
        id: "signed-manifest",
        earned: true,
        text: "The checksum manifest is signed with the heros release key (ed25519). Verify it offline, with no account.",
      },
      {
        id: "macos-signed",
        earned: false,
        text: "The macOS binaries carry no Apple code signature.",
      },
      {
        id: "macos-notarized",
        earned: false,
        text: "The macOS binaries are NOT notarized. macOS quarantines internet downloads: clear the flag with the one command the installer prints, or install with Homebrew, which is not quarantined.",
      },
      {
        id: "windows-signed",
        earned: false,
        text: "The Windows binary is NOT Authenticode-signed. SmartScreen warns on first run: More info \u2192 Run anyway. Publisher metadata is declared where a package can carry it \u2014 the winget manifest and the .deb/.rpm metadata name Heros Foreal \u2014 but the bare .exe carries none of its own, because on Windows the Authenticode signature IS the publisher declaration. Its file properties will show no publisher.",
      },
    ],
  },
};
