package distribution

import (
	"fmt"
	"sort"
	"strings"
)

// manifests.go generates every package-manager manifest from the release (P20 D5, tasks 3.3–3.6).
//
// # Why generated, in one sentence
//
// A hand-maintained formula holds a SECOND copy of the version and the checksum, and second copies drift in
// one direction: the tag moves, the release ships, the formula still points at last release's URL and last
// release's sha256, and the user who runs `brew install` gets the previous binary while `heros version`
// disagrees with the Release page. Generating from the one build makes that drift structurally impossible —
// there is one version (the tag) and one checksum set (the signed manifest), and every channel is a
// projection of them.
//
// # Why the checksums come from the signed manifest rather than from the files
//
// Because that is what links each channel back to the signature. `brew` checks a sha256 it finds in the
// formula; the formula's authority is that its sha256 was COPIED FROM THE DOCUMENT THE RELEASE KEY SIGNED.
// If the generator hashed the files itself, the formula would attest to whatever bytes the generator happened
// to be holding, and the chain from `brew install` back to the release key would be broken in a way nobody
// could see by reading the formula.
//
// So: Generate takes the parsed manifest, and it FAILS if a target it needs is missing from it. A formula
// with an empty sha256 field installs nothing and reports a confusing error; a release that refuses to
// generate one is a release that tells you now.

// GeneratedFile is one emitted manifest.
type GeneratedFile struct {
	// Path is relative to the packaging output directory.
	Path string
	// Content is the complete file.
	Content string
	// Channel is which channel consumes it, so the workflow can skip publishing a channel that is not
	// deliverable yet without guessing from filenames.
	Channel string
	// Mode is 0o755 for scripts, 0o644 otherwise.
	Mode uint32
}

// Package is the package identity every channel shares. Held in one place because winget, Homebrew, Scoop and
// nfpm each want it in a different shape, and four spellings of the same product is how a user ends up with
// two of it installed.
const (
	PackageName        = "heros"
	PackageIdentifier  = "HerosForeal.Heros"
	PackagePublisher   = "Heros Foreal"
	PackageHomepage    = "https://github.com/damonleelcx/heros-agent"
	PackageLicense     = "Apache-2.0"
	PackageDescription = "Discover, optimize and evaluate the agent workflow already in your repository."
	PackageSummary     = "The heros CLI — agent workflow discovery, optimization and evaluation."

	// PackageRelease is the distro package RELEASE number — the `-1` in `heros-0.20.0-1.x86_64.rpm`.
	//
	// 🔴 It exists as a constant because it was WRONG in a shipped channel command. The `.rpm` install
	// line said `heros-{{version}}.x86_64.rpm`; the release publishes `heros-0.20.0-1.x86_64.rpm`, and
	// the difference is this number. A reader following the documented command got a 404.
	//
	// nfpm defaults an unset `release:` to 1, so the generated config and this constant agree today by
	// coincidence rather than by construction. `DebFileName` and `RPMFileName` below close that: the
	// channel commands and the packaging now derive their filenames from one function, and
	// `TestChannelCommandsNameThePackagesTheReleaseActuallyPublishes` asserts the result.
	PackageRelease = "1"
)

// rpmArch maps a Go architecture to the one RPM prints in a filename. They differ, and the difference is
// exactly where a hand-typed install command goes wrong: `amd64` is `x86_64`, `arm64` is `aarch64`.
func rpmArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	}
	return goarch
}

// DebFileName is the .deb nfpm produces for a version and a Go architecture.
//
// Derived rather than written into each channel command, so the documented filename and the built
// artifact cannot disagree. nfpm's Debian naming is `name_version_arch.deb`.
func DebFileName(version, goarch string) string {
	return fmt.Sprintf("%s_%s_%s.deb", PackageName, version, goarch)
}

// RPMFileName is the .rpm nfpm produces for a version and a Go architecture.
//
// nfpm's RPM naming is `name-version-release.rpmarch.rpm`, and BOTH the release number and the arch
// spelling are places a typed filename goes wrong. This is the function that was missing when the `.rpm`
// channel shipped a command pointing at an asset that does not exist.
func RPMFileName(version, goarch string) string {
	return fmt.Sprintf("%s-%s-%s.%s.rpm", PackageName, version, PackageRelease, rpmArch(goarch))
}

// Generate emits channel manifests for a release.
//
// sums is the parsed SHA256SUMS — the signed document — mapping asset name to hex checksum.
//
// only, when non-empty, restricts output to those channel ids AND restricts which assets are required. That
// second half is the point: a `.deb` is built from the linux binaries and has nothing to do with the darwin
// ones, so demanding all five targets to emit an nfpm config couples a channel to platforms it never mentions.
// A caller packaging one channel from a partial set — a rehearsal, a single-platform smoke run — is doing
// something legitimate, and refusing it taught nothing.
//
// With no filter, every channel is generated and every shipped target is required, which is what a real release
// wants: a Release that published a formula pointing at four platforms and a manifest listing three is broken in
// a way only the fourth platform's users discover.
func Generate(v Version, sums map[string]string, a Attestation, only ...string) ([]GeneratedFile, error) {
	wanted := map[string]bool{}
	for _, id := range only {
		wanted[id] = true
	}
	// want reports whether a channel is in scope. An empty filter means everything.
	want := func(id string) bool { return len(wanted) == 0 || wanted[id] }

	// need resolves an asset's checksum from the SIGNED manifest. It is called lazily, per channel, so a
	// channel that is out of scope never demands its platforms.
	need := func(goos, goarch string) (name, sum string, err error) {
		name = AssetName(v.Version, goos, goarch)
		sum, ok := sums[name]
		if !ok {
			return "", "", fmt.Errorf("generate: the signed manifest does not list %s — a package manifest "+
				"generated without it would carry an empty checksum, which installs nothing and reports a "+
				"confusing error on the user's machine", name)
		}
		return name, sum, nil
	}

	var darwinAmd, darwinAmdSum, darwinArm, darwinArmSum string
	var linuxAmd, linuxAmdSum, linuxArm, linuxArmSum string
	var winAmd, winAmdSum string
	var err error

	if want("homebrew") {
		if darwinAmd, darwinAmdSum, err = need("darwin", "amd64"); err != nil {
			return nil, err
		}
		if darwinArm, darwinArmSum, err = need("darwin", "arm64"); err != nil {
			return nil, err
		}
	}
	// Homebrew's formula branches on all four unix rows and the Dockerfile copies the amd64 asset, so those two
	// channels need their platforms unconditionally. The .deb/.rpm configs are PER-ARCH and are handled below,
	// where a scoped run may legitimately have only one of them.
	if want("homebrew") || want("container") {
		if linuxAmd, linuxAmdSum, err = need("linux", "amd64"); err != nil {
			return nil, err
		}
	}
	if want("homebrew") {
		if linuxArm, linuxArmSum, err = need("linux", "arm64"); err != nil {
			return nil, err
		}
	}
	if want("scoop") || want("winget") {
		if winAmd, winAmdSum, err = need("windows", "amd64"); err != nil {
			return nil, err
		}
	}

	dl := PackageHomepage + "/releases/download/" + v.Tag
	url := func(asset string) string { return dl + "/" + asset }

	var out []GeneratedFile

	// ── Homebrew formula ────────────────────────────────────────────────────────────────────────────────
	// Ruby, four platform branches. `version` is stated explicitly rather than inferred from the URL so
	// `brew info` reports the tag rather than a guess.
	if want("homebrew") {
		var brew strings.Builder
		fmt.Fprintf(&brew, "# Generated by `herosdist manifests` from the signed release manifest of %s.\n", v.Tag)
		fmt.Fprintf(&brew, "# 🚫 Do not edit. A hand-edited formula is a second copy of the version and the\n")
		fmt.Fprintf(&brew, "# checksum, and it drifts to the previous release the first time a tag moves.\n")
		fmt.Fprintf(&brew, "class Heros < Formula\n")
		fmt.Fprintf(&brew, "  desc %q\n", PackageSummary)
		fmt.Fprintf(&brew, "  homepage %q\n", PackageHomepage)
		fmt.Fprintf(&brew, "  version %q\n", v.Version)
		fmt.Fprintf(&brew, "  license %q\n\n", PackageLicense)
		fmt.Fprintf(&brew, "  on_macos do\n")
		fmt.Fprintf(&brew, "    on_arm do\n      url %q\n      sha256 %q\n    end\n", url(darwinArm), darwinArmSum)
		fmt.Fprintf(&brew, "    on_intel do\n      url %q\n      sha256 %q\n    end\n", url(darwinAmd), darwinAmdSum)
		fmt.Fprintf(&brew, "  end\n\n")
		fmt.Fprintf(&brew, "  on_linux do\n")
		fmt.Fprintf(&brew, "    on_arm do\n      url %q\n      sha256 %q\n    end\n", url(linuxArm), linuxArmSum)
		fmt.Fprintf(&brew, "    on_intel do\n      url %q\n      sha256 %q\n    end\n", url(linuxAmd), linuxAmdSum)
		fmt.Fprintf(&brew, "  end\n\n")
		fmt.Fprintf(&brew, "  def install\n    bin.install Dir[\"heros-*\"].first => \"heros\"\n  end\n\n")
		// The test block runs on every `brew install --build-from-source` and on brew's own CI. Asserting the
		// version rather than just "it runs" is what would catch a formula pointing at the wrong release.
		fmt.Fprintf(&brew, "  test do\n")
		fmt.Fprintf(&brew, "    assert_match %q, shell_output(\"#{bin}/heros version\")\n", v.Version)
		fmt.Fprintf(&brew, "  end\nend\n")
		out = append(out, GeneratedFile{Path: "homebrew/heros.rb", Content: brew.String(), Channel: "homebrew", Mode: 0o644})
	}

	// ── Scoop manifest ─────────────────────────────────────────────────────────────────────────────────
	if want("scoop") {
		var scoop strings.Builder
		fmt.Fprintf(&scoop, "{\n")
		fmt.Fprintf(&scoop, "  \"##\": \"Generated by `herosdist manifests` from the signed release manifest of %s. Do not edit.\",\n", v.Tag)
		fmt.Fprintf(&scoop, "  \"version\": %q,\n", v.Version)
		fmt.Fprintf(&scoop, "  \"description\": %q,\n", PackageDescription)
		fmt.Fprintf(&scoop, "  \"homepage\": %q,\n", PackageHomepage)
		fmt.Fprintf(&scoop, "  \"license\": %q,\n", PackageLicense)
		fmt.Fprintf(&scoop, "  \"architecture\": {\n    \"64bit\": {\n")
		fmt.Fprintf(&scoop, "      \"url\": %q,\n", url(winAmd))
		fmt.Fprintf(&scoop, "      \"hash\": %q\n", winAmdSum)
		fmt.Fprintf(&scoop, "    }\n  },\n")
		// Scoop shims the downloaded file under a stable name; without this the command would be
		// `heros-0.20.0-windows-amd64.exe`, which changes every release.
		fmt.Fprintf(&scoop, "  \"pre_install\": [\"Rename-Item \\\"$dir\\\\%s\\\" \\\"heros.exe\\\"\"],\n", winAmd)
		fmt.Fprintf(&scoop, "  \"bin\": \"heros.exe\",\n")
		fmt.Fprintf(&scoop, "  \"checkver\": {\n    \"github\": %q\n  },\n", PackageHomepage)
		fmt.Fprintf(&scoop, "  \"autoupdate\": {\n    \"architecture\": {\n      \"64bit\": {\n        \"url\": %q\n      }\n    }\n  }\n}\n",
			PackageHomepage+"/releases/download/v$version/heros-$version-windows-amd64.exe")
		out = append(out, GeneratedFile{Path: "scoop/heros.json", Content: scoop.String(), Channel: "scoop", Mode: 0o644})
	}

	// ── winget: the three-file manifest ────────────────────────────────────────────────────────────────
	// winget requires a version file, an installer file and at least one locale file, all carrying the same
	// PackageIdentifier and PackageVersion. Generating them together is what keeps those three copies equal.
	if want("winget") {
		wingetDir := "winget/" + PackageIdentifier + "/" + v.Version + "/"
		ver := fmt.Sprintf(`# Generated by `+"`herosdist manifests`"+` from the signed release manifest of %s. Do not edit.
PackageIdentifier: %s
PackageVersion: %s
DefaultLocale: en-US
ManifestType: version
ManifestVersion: 1.6.0
`, v.Tag, PackageIdentifier, v.Version)
		out = append(out, GeneratedFile{Path: wingetDir + PackageIdentifier + ".yaml", Content: ver, Channel: "winget", Mode: 0o644})

		// InstallerType: portable — the artifact is a bare .exe, not an installer. Declaring it as anything else
		// would make winget try to run a silent install that does not exist.
		//
		// The publisher metadata below is declared whether or not the binary is Authenticode-signed (task 4.2): a
		// user who sees "unknown publisher" learns nothing, and one who sees the org name can at least compare it
		// with the docs.
		inst := fmt.Sprintf(`# Generated by `+"`herosdist manifests`"+` from the signed release manifest of %s. Do not edit.
PackageIdentifier: %s
PackageVersion: %s
Platform:
  - Windows.Desktop
MinimumOSVersion: 10.0.17763.0
InstallerType: portable
Commands:
  - heros
ReleaseDate: ""
Installers:
  - Architecture: x64
    InstallerUrl: %s
    InstallerSha256: %s
    NestedInstallerFiles:
      - RelativeFilePath: %s
        PortableCommandAlias: heros
ManifestType: installer
ManifestVersion: 1.6.0
`, v.Tag, PackageIdentifier, v.Version, url(winAmd), strings.ToUpper(winAmdSum), winAmd)
		out = append(out, GeneratedFile{Path: wingetDir + PackageIdentifier + ".installer.yaml", Content: inst, Channel: "winget", Mode: 0o644})

		// The locale file carries the user-visible strings. `ShortDescription` is what `winget search` shows.
		locale := fmt.Sprintf(`# Generated by `+"`herosdist manifests`"+` from the signed release manifest of %s. Do not edit.
PackageIdentifier: %s
PackageVersion: %s
PackageLocale: en-US
Publisher: %s
PublisherUrl: %s
PackageName: %s
PackageUrl: %s
License: %s
ShortDescription: %s
Description: %s
ManifestType: defaultLocale
ManifestVersion: 1.6.0
`, v.Tag, PackageIdentifier, v.Version, PackagePublisher, PackageHomepage, PackageName,
			PackageHomepage, PackageLicense, PackageSummary, PackageDescription)
		out = append(out, GeneratedFile{Path: wingetDir + PackageIdentifier + ".locale.en-US.yaml", Content: locale, Channel: "winget", Mode: 0o644})
	}

	// ── nfpm configs for .deb / .rpm ───────────────────────────────────────────────────────────────────
	// One per architecture, because nfpm's `arch` and the source binary differ per row and a single config
	// with a substituted arch would produce a package whose contents do not match its metadata.
	if want("deb") || want("rpm") {
		// Per-arch, and each arch is required INDEPENDENTLY. An unscoped run (a real release) must produce both,
		// because a Release offering a .deb for one architecture and not the other is a gap only that
		// architecture's users find. A scoped run packages what it actually has, which is what lets a
		// single-platform smoke test exercise the dpkg install path at all.
		emitted := 0
		for _, arch := range []string{"amd64", "arm64"} {
			asset, _, aerr := need("linux", arch)
			if aerr != nil {
				if len(wanted) == 0 {
					return nil, aerr
				}
				continue
			}
			row := struct{ arch, asset string }{arch, asset}
			emitted++
			nfpm := fmt.Sprintf(`# Generated by `+"`herosdist manifests`"+` from the signed release manifest of %s. Do not edit.
name: %s
arch: %s
platform: linux
version: %q
section: devel
priority: optional
maintainer: %s <%s>
description: |
  %s
vendor: %s
homepage: %s
license: %s
contents:
  - src: %s
    dst: /usr/bin/heros
    file_info:
      mode: 0755
# No postinstall script. A package that runs code on install is a package a security reviewer has to read,
# and this one only needs to place one self-contained binary.
`, v.Tag, PackageName, row.arch, v.Version, PackagePublisher, "release@heros-agent.space",
				PackageDescription, PackagePublisher, PackageHomepage, PackageLicense, row.asset)
			out = append(out, GeneratedFile{Path: "nfpm/nfpm-" + row.arch + ".yaml", Content: nfpm, Channel: "deb", Mode: 0o644})
		}
		if emitted == 0 {
			return nil, fmt.Errorf("generate: the signed manifest lists no linux binary, so no .deb or .rpm can " +
				"be built — packaging an empty payload would produce an installable package that installs nothing")
		}
	}

	// ── container image ────────────────────────────────────────────────────────────────────────────────
	// A glibc base, because the CLI links CGO tree-sitter against glibc (D6). This image is the answer for
	// Alpine/musl users, so basing it on Alpine would defeat its whole purpose.
	//
	// The binary is COPIED IN rather than rebuilt: the bytes in the image are then the same bytes the release
	// manifest covers and the signature attests to. An image that recompiled from source would be a second,
	// unsigned artifact wearing the release's version number.
	if want("container") {
		docker := fmt.Sprintf(`# Generated by `+"`herosdist manifests`"+` from the signed release manifest of %s. Do not edit.
#
# Debian slim, not Alpine: the CLI links CGO tree-sitter frontends against glibc, and this image exists
# precisely so musl users have a working answer (D6). The binary is copied from the verified release
# artifacts, never rebuilt, so what runs here is what the release key signed.
FROM debian:12-slim

RUN apt-get update -qq \
 && apt-get install -y -qq --no-install-recommends ca-certificates git \
 && rm -rf /var/lib/apt/lists/*

COPY %s /usr/local/bin/heros
RUN chmod 0755 /usr/local/bin/heros

# A non-root user: the CLI reads a repository and writes an IR file, and needs nothing else.
RUN useradd --create-home --uid 10001 heros
USER heros
WORKDIR /repo

LABEL org.opencontainers.image.title="heros" \
      org.opencontainers.image.description=%q \
      org.opencontainers.image.version=%q \
      org.opencontainers.image.source=%q \
      org.opencontainers.image.licenses=%q

ENTRYPOINT ["/usr/local/bin/heros"]
CMD ["--help"]
`, v.Tag, linuxAmd, PackageDescription, v.Version, PackageHomepage, PackageLicense)
		out = append(out, GeneratedFile{Path: "container/Dockerfile", Content: docker, Channel: "container", Mode: 0o644})
	}

	// ── the honest capability map, as data ─────────────────────────────────────────────────────────────
	// Emitted with the manifests so the release carries its own answer to "which of these can I actually
	// use", rather than leaving a reader to infer it from which files are present.
	if len(wanted) == 0 {
		out = append(out, GeneratedFile{Path: "CHANNELS.md", Content: ChannelReport(v, a), Channel: "", Mode: 0o644})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// ChannelReport renders the channel capability map for a release: what is installable today, what is
// generated but not yet publishable, and why.
//
// Sales lens (task 8.3): a customer's engineer reads this before committing to the tool, so a row that
// overstates is a promise the product will be caught not keeping.
func ChannelReport(v Version, a Attestation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Install channels — heros %s\n\n", v.Version)
	fmt.Fprintf(&b, "## ✅ Available now\n\n")
	for _, c := range Channels() {
		if !c.Delivered() {
			continue
		}
		fmt.Fprintf(&b, "### %s (%s)\n\n", c.Label, strings.Join(c.GOOSes, ", "))
		fmt.Fprintf(&b, "```sh\n%s\n```\n\n", Command(c.Install, v.Version))
		fmt.Fprintf(&b, "- verification: %s\n", c.Verification)
		fmt.Fprintf(&b, "- upgrade: `%s`\n", Command(c.Upgrade, v.Version))
		fmt.Fprintf(&b, "- uninstall: `%s`\n", Command(c.Uninstall, v.Version))
		fmt.Fprintf(&b, "- install a specific version: `%s`\n\n", Command(c.Pin, v.Version))
	}
	fmt.Fprintf(&b, "## ⛔ Generated but not yet publishable\n\n")
	fmt.Fprintf(&b, "The manifests below are generated from this release's signed manifest and attached to it, so "+
		"they are correct — but nothing a package manager reads points at them yet. They are listed rather than "+
		"omitted because an absent channel reads as *not supported*, and that is not what is true here.\n\n")
	for _, c := range Channels() {
		if c.Delivered() {
			continue
		}
		fmt.Fprintf(&b, "- **%s** — %s\n", c.Label, c.Blocker)
	}
	fmt.Fprintf(&b, "\n## Trust posture\n\n%s\n", a.Describe())
	return b.String()
}
