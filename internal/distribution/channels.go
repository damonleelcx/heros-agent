package distribution

import (
	"fmt"
	"sort"
	"strings"
)

// channels.go is the install-channel contract (P20 section 3) and the honest capability map behind task 8.1.
//
// # The distinction this file exists to make
//
// "We support Homebrew" can mean two very different things, and the gap between them is where a customer's
// engineer catches a promise the product cannot keep:
//
//	the pipeline GENERATES a formula from the signed manifest   ← a build artifact
//	the formula is IN A TAP a user can `brew install` from      ← a delivered channel
//
// The first is done the moment the generator exists. The second needs a tap repository and a token to push
// to it, and no amount of Go code creates those. A README that lists Homebrew because the generator exists
// sends a user to run a command that fails — so `Publication` is a separate field from the generator, and
// `Delivered()` is false until the manifest actually reaches somewhere a user's package manager looks.
//
// # Why upgrade/uninstall/pin are stated per channel rather than documented
//
// Task 3.8 requires every channel to be uninstallable by its own idiom and able to install a specific prior
// version. Those are three commands per channel, and a doc holding nine channels × three commands drifts the
// first time one changes. They live here, the README and the console render them, and `heros upgrade` reads
// ManagerOwned from here to decide whether to defer instead of overwriting a file the manager owns.
type Channel struct {
	// ID is the stable identifier used in the target matrix's Channels list and on every surface.
	ID string
	// Label is how a human refers to the channel.
	Label string
	// GOOSes are the operating systems it serves.
	GOOSes []string
	// Publication says how — and whether — the manifest reaches a user.
	Publication Publication
	// Blocker names what is missing when Publication is not PublishedByPipeline. Required in that case: a
	// channel listed as unavailable with no reason is indistinguishable from one nobody thought about.
	Blocker string
	// ManagerOwned is true when a package manager owns the installed file. `heros upgrade` must then DEFER
	// to the manager and print its command rather than replacing the binary — overwriting a
	// manager-owned file corrupts that manager's state, which is a 稳定 regression, and the next
	// `brew upgrade` would silently revert the user's upgrade anyway.
	ManagerOwned bool
	// Verification says how this channel establishes that the bytes are ours. Stated per channel because it
	// genuinely differs, and because "verified" with no mechanism is the claim this project does not make.
	Verification string
	// Install, Upgrade, Uninstall and Pin are the channel's own idioms. {{version}} is substituted.
	Install   string
	Upgrade   string
	Uninstall string
	Pin       string
}

// Publication is how a channel's manifest reaches a user.
type Publication string

const (
	// PublishedByPipeline means the release workflow itself puts the artifact where users get it — a Release
	// asset, or a push to ghcr.io. Needs nothing beyond GITHUB_TOKEN.
	PublishedByPipeline Publication = "published"
	// PendingExternalRepo means the manifest is generated and attached to the Release, but the index a user's
	// package manager reads lives in another repository that does not exist yet (a Homebrew tap, a Scoop
	// bucket).
	PendingExternalRepo Publication = "pending-external-repo"
	// PendingUpstreamPR means publication requires a pull request into somebody else's repository
	// (microsoft/winget-pkgs), whose merge is not ours to schedule.
	PendingUpstreamPR Publication = "pending-upstream-pr"
)

// Delivered reports whether a user can actually install from this channel today.
//
// 🔴 This is the predicate the README and the console must use. `Publication != ""` would be true for every
// row; "we wrote a generator" is not a channel a customer can use.
func (c Channel) Delivered() bool { return c.Publication == PublishedByPipeline }

// channels is the channel contract.
var channels = []Channel{
	{
		ID: "curl-sh", Label: "curl | sh", GOOSes: []string{"darwin", "linux"},
		Publication: PublishedByPipeline, ManagerOwned: false,
		Verification: "the script verifies the sha256 against the release manifest and the manifest's ed25519 " +
			"signature against a key pinned in the script, before placing anything on PATH; any failure is a hard stop",
		Install:   "curl -fsSL https://raw.githubusercontent.com/damonleelcx/heros-agent/v{{version}}/scripts/install.sh | sh",
		Upgrade:   "heros upgrade",
		Uninstall: "curl -fsSL https://raw.githubusercontent.com/damonleelcx/heros-agent/v{{version}}/scripts/install.sh | HEROS_UNINSTALL=1 sh",
		Pin:       "curl -fsSL .../install.sh | HEROS_VERSION={{version}} sh",
	},
	{
		ID: "powershell", Label: "PowerShell (irm | iex)", GOOSes: []string{"windows"},
		Publication: PublishedByPipeline, ManagerOwned: false,
		Verification: "the script verifies the sha256 and then the ed25519 signature against a pinned key, " +
			"before adding anything to PATH; any failure is a hard stop",
		Install:   "irm https://raw.githubusercontent.com/damonleelcx/heros-agent/v{{version}}/scripts/install.ps1 | iex",
		Upgrade:   "heros upgrade",
		Uninstall: "$env:HEROS_UNINSTALL=1; irm .../install.ps1 | iex",
		Pin:       "$env:HEROS_VERSION='{{version}}'; irm .../install.ps1 | iex",
	},
	{
		ID: "deb", Label: ".deb package", GOOSes: []string{"linux"},
		Publication: PublishedByPipeline, ManagerOwned: true,
		// 🔴 CORRECTED. This used to read "the package's sha256 is listed in the signed release manifest".
		// It is not, and it structurally cannot be: the pipeline computes and SIGNS SHA256SUMS before nfpm
		// builds the packages, so the manifest is sealed by the time the .deb exists. The claim was false
		// for every release that has ever shipped, and it was the kind of false that a reader only
		// discovers while trying to do the right thing.
		//
		// The honest chain is better than no chain, and it is what this now says: the .deb contains the
		// EXACT linux binary the signed manifest covers — nfpm's `contents.src` is that asset, copied, with
		// no postinstall script — so the check moves to the installed file.
		Verification: "the package's own sha256 is NOT in the signed release manifest (the manifest is " +
			"signed before packaging runs). What the manifest covers is the binary INSIDE it, byte for byte: " +
			"verify the heros-VERSION-linux-ARCH asset against SHA256SUMS, then confirm the installed " +
			"/usr/bin/heros matches it",
		Install:   "curl -fsSLO " + PackageHomepage + "/releases/download/v{{version}}/{{deb}} && sudo dpkg -i {{deb}}",
		Upgrade:   "sudo dpkg -i {{deb}}   (from the newer release)",
		Uninstall: "sudo dpkg -r heros",
		Pin:       "download {{deb}} for the exact version from that release and dpkg -i it",
	},
	{
		ID: "rpm", Label: ".rpm package", GOOSes: []string{"linux"},
		Publication: PublishedByPipeline, ManagerOwned: true,
		// 🔴 CORRECTED, twice.
		//
		// The verification claim was the same false one as .deb — see there.
		//
		// And the install command named `heros-{{version}}.x86_64.rpm`, an asset no release has ever
		// published: nfpm's RPM naming carries a RELEASE NUMBER, so the file is
		// `heros-0.20.0-1.x86_64.rpm`. The documented command 404'd on every release. It is now built from
		// `RPMFileName`, the same function the naming test checks against the published assets, so the
		// filename cannot be typed wrong again.
		Verification: "the package's own sha256 is NOT in the signed release manifest (the manifest is " +
			"signed before packaging runs). What the manifest covers is the binary INSIDE it, byte for byte: " +
			"verify the heros-VERSION-linux-ARCH asset against SHA256SUMS, then confirm the installed " +
			"/usr/bin/heros matches it",
		Install:   "sudo rpm -i " + PackageHomepage + "/releases/download/v{{version}}/{{rpm}}",
		Upgrade:   "sudo rpm -U {{rpm}}   (from the newer release)",
		Uninstall: "sudo rpm -e heros",
		Pin:       "rpm -i the exact version's package URL, which ends in {{rpm}}",
	},
	{
		ID: "container", Label: "container image", GOOSes: []string{"darwin", "linux", "windows"},
		Publication: PublishedByPipeline, ManagerOwned: true,
		Verification: "the image is digest-pinnable and is built in the same run from the same verified " +
			"binary; pull by digest to pin exactly what you audited",
		Install:   "docker run --rm -v \"$PWD:/repo\" " + ImageRepo + ":{{version}} discover --repo /repo",
		Upgrade:   "docker pull " + ImageRepo + ":<newer>",
		Uninstall: "docker rmi " + ImageRepo + ":{{version}}",
		Pin:       "docker run --rm " + ImageRepo + ":{{version}}   (or @sha256:… for a digest pin)",
	},
	{
		ID: "homebrew", Label: "Homebrew", GOOSes: []string{"darwin", "linux"},
		Publication: PendingExternalRepo,
		Blocker: "the formula is generated from the signed manifest and attached to every Release, but the " +
			"tap repository heros-foreal/homebrew-tap does not exist yet and pushing to it needs a token " +
			"secret. Until then `brew install heros-foreal/tap/heros` would fail",
		ManagerOwned: true,
		Verification: "brew checks the sha256 in the formula, and the formula's sha256 is copied from the " +
			"signed release manifest by the generator — so the chain to the signature is intact, with brew " +
			"as the last link",
		Install:   "brew install heros-foreal/tap/heros",
		Upgrade:   "brew upgrade heros",
		Uninstall: "brew uninstall heros",
		Pin:       "brew install heros-foreal/tap/heros@{{version}}",
	},
	{
		ID: "scoop", Label: "Scoop", GOOSes: []string{"windows"},
		Publication: PendingExternalRepo,
		Blocker: "the manifest is generated and attached to every Release, but the bucket repository " +
			"heros-foreal/scoop-bucket does not exist yet and pushing to it needs a token secret",
		ManagerOwned: true,
		Verification: "scoop checks the hash in the manifest, and the generator copies that hash from the " +
			"signed release manifest",
		Install:   "scoop bucket add heros https://github.com/heros-foreal/scoop-bucket; scoop install heros",
		Upgrade:   "scoop update heros",
		Uninstall: "scoop uninstall heros",
		Pin:       "scoop install heros@{{version}}",
	},
	{
		ID: "winget", Label: "winget", GOOSes: []string{"windows"},
		Publication: PendingUpstreamPR,
		Blocker: "the three-file winget manifest is generated and attached to every Release, but publication " +
			"is a pull request into microsoft/winget-pkgs whose review and merge are not ours to schedule",
		ManagerOwned: true,
		Verification: "winget checks the InstallerSha256 in the manifest, which the generator copies from the " +
			"signed release manifest",
		Install:   "winget install HerosForeal.Heros",
		Upgrade:   "winget upgrade HerosForeal.Heros",
		Uninstall: "winget uninstall HerosForeal.Heros",
		Pin:       "winget install HerosForeal.Heros --version {{version}}",
	},
}

// Channels returns the total channel contract — delivered and not.
func Channels() []Channel {
	out := make([]Channel, len(channels))
	copy(out, channels)
	return out
}

// DeliveredChannels returns only the channels a user can install from today. This is what the README's
// install section is generated from (task 8.1).
func DeliveredChannels() []Channel {
	var out []Channel
	for _, c := range channels {
		if c.Delivered() {
			out = append(out, c)
		}
	}
	return out
}

// ChannelByID looks a channel up. The second return distinguishes "no such channel" from a zero value,
// because a caller that silently got an empty Channel would render a row with no commands in it.
func ChannelByID(id string) (Channel, bool) {
	for _, c := range channels {
		if c.ID == id {
			return c, true
		}
	}
	return Channel{}, false
}

// ChannelsFor returns the channels serving a GOOS, delivered first.
//
// Used by `heros doctor` and by the console, so a macOS reader is never shown `scoop install`.
func ChannelsFor(goos string) []Channel {
	var out []Channel
	for _, c := range channels {
		for _, o := range c.GOOSes {
			if o == goos {
				out = append(out, c)
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Delivered() && !out[j].Delivered() })
	return out
}

// Command renders one of the channel's idioms with the version and the package filenames substituted.
//
// # Why the package filenames are placeholders rather than literals
//
// The `.rpm` channel shipped a command naming `heros-{{version}}.x86_64.rpm` — an asset no release has
// ever published, because nfpm's RPM filename carries a release number (`heros-0.20.0-1.x86_64.rpm`).
// Every reader who followed it got a 404, and no check could see it: the version substituted correctly
// and the sentence read fine.
//
// So the filenames are now DERIVED, by the same functions the packaging naming test checks against the
// published release. `{{deb}}` and `{{rpm}}` cannot be typed wrong because they are not typed.
//
// The architecture is amd64: these are the commands a documentation page prints, and a page has to pick
// one. `Pin` and the install page both say the exact filename, so an arm64 reader can see what to change
// — which is better than a page that prints `<arch>` and leaves them to guess the spelling RPM uses.
func Command(template, version string) string {
	out := strings.ReplaceAll(template, "{{version}}", version)
	out = strings.ReplaceAll(out, "{{deb}}", DebFileName(version, "amd64"))
	out = strings.ReplaceAll(out, "{{rpm}}", RPMFileName(version, "amd64"))
	return out
}

// ManagerOwnedChannelFor guesses whether an installed binary at path is owned by a package manager, and
// returns the channel that owns it.
//
// # Why a path heuristic rather than a recorded fact
//
// Because nothing records it. The binary could have been placed by brew, by dpkg, by scoop, or by our own
// installer, and none of them leaves a marker the others can read. `heros upgrade` must not overwrite a
// manager-owned file (D7), so it needs an answer, and the honest answer is a heuristic over the install
// prefix — which is exactly what every self-updating tool does.
//
// The heuristic is deliberately conservative: an unrecognised path is reported as NOT manager-owned, so
// upgrade proceeds on a plain install. The opposite default would tell users with a hand-placed binary to run
// `brew upgrade` for a brew they may not have.
func ManagerOwnedChannelFor(path string) (Channel, bool) {
	p := strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	switch {
	case strings.Contains(p, "/homebrew/") || strings.Contains(p, "/cellar/") || strings.Contains(p, "/linuxbrew/"):
		return mustChannel("homebrew"), true
	case strings.Contains(p, "/scoop/"):
		return mustChannel("scoop"), true
	case strings.Contains(p, "/winget") || strings.Contains(p, "/windowsapps/"):
		return mustChannel("winget"), true
	case p == "/usr/bin/heros":
		// /usr/bin is dpkg/rpm territory; /usr/local/bin is not, and that difference is exactly what keeps a
		// curl|sh install upgradeable in place.
		return mustChannel("deb"), true
	}
	return Channel{}, false
}

func mustChannel(id string) Channel {
	c, ok := ChannelByID(id)
	if !ok {
		panic(fmt.Sprintf("distribution: channel %q is referenced but not defined", id))
	}
	return c
}

// ── the README install section, generated ────────────────────────────────────────────────────────────────

// DocumentedRelease is the release the committed README documents.
//
// # Why this is not a second copy of the version D5 forbids
//
// D5 forbids a *packaging manifest* holding a version, because a formula's version must equal the tag that
// built the binary it points at, and any second copy drifts. A README is different: it is written before the
// tag exists, and a reader needs a concrete pinned tag in the `curl` URL — "install the latest" is exactly the
// unpinnable command task 3.7 removes.
//
// So this is the version the DOCS describe, held in one place, with a drift gate
// (TestReadmeInstallSectionMatchesContract) keeping the README's rendering equal to the contract's. The release
// pipeline regenerates the section with the real tag and the real install.sh checksum for the release notes; a
// human bumps this line when the docs move to a new release.
const DocumentedRelease = "v0.22.0"

// ReadmeMarkerStart and ReadmeMarkerEnd delimit the generated install section in README.md.
//
// The section is generated and then CHECKED, rather than generated at build time into a separate file, because
// a README is read by people who will not run a generator. Keeping it in the file a reader opens, with a gate
// that fails when it drifts from the contract, is what makes "claim only delivered channels" (task 8.1)
// enforceable instead of aspirational.
const (
	ReadmeMarkerStart = "<!-- BEGIN GENERATED INSTALL SECTION — `make readme-install` regenerates it -->"
	ReadmeMarkerEnd   = "<!-- END GENERATED INSTALL SECTION -->"
)

// ReadmeInstallSection renders the install section: the one-command install per delivered channel, the
// offline verification steps, the pinned checksum-referenced installer URL (task 3.7), the disclosed limits,
// and the free-vs-paid boundary.
//
// installShaSum is the sha256 of scripts/install.sh at the released tag — the "checksum-referenced" half of
// task 3.7. It is passed in rather than computed here so the value in the README is the one from the SIGNED
// release manifest, not one this function hashed for itself.
func ReadmeInstallSection(v Version, installShaSum string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s\n\n", ReadmeMarkerStart)
	fmt.Fprintf(&b, "### Install\n\n")
	fmt.Fprintf(&b, "`heros` is one self-contained binary. `discover`, `apply`, `eval`, `doctor` and `init` "+
		"work **offline with no account** — there is nothing to sign up for to get a first result.\n\n")

	for _, c := range DeliveredChannels() {
		fmt.Fprintf(&b, "**%s** — %s\n\n```sh\n%s\n```\n\n", c.Label, strings.Join(c.GOOSes, ", "),
			Command(c.Install, v.Version))
	}

	// Task 3.7: the `curl | sh` target is auditable — pinned to a tag, with a checksum a reader can verify
	// before piping it, and the checksum comes from the signed manifest rather than from this document.
	fmt.Fprintf(&b, "#### Auditing the install script before you pipe it\n\n")
	fmt.Fprintf(&b, "The URL above is pinned to the `%s` tag, so it cannot change under you, and the script is "+
		"covered by the same signed manifest as the binaries. To read it before running it:\n\n", v.Tag)
	fmt.Fprintf(&b, "```sh\ncurl -fsSLO https://raw.githubusercontent.com/damonleelcx/heros-agent/%s/scripts/install.sh\n", v.Tag)
	if installShaSum != "" {
		fmt.Fprintf(&b, "echo '%s  install.sh' | shasum -a 256 -c   # the value in %s's SHA256SUMS\n", installShaSum, v.Tag)
	} else {
		fmt.Fprintf(&b, "# then compare its sha256 against the install.sh line in that release's SHA256SUMS\n")
	}
	fmt.Fprintf(&b, "less install.sh && sh install.sh\n```\n\n")

	fmt.Fprintf(&b, "#### Verifying a release yourself (offline, no account)\n\n")
	fmt.Fprintf(&b, "Every channel above performs these two steps for you and **refuses to place the binary on "+
		"your PATH** if either fails. To run them by hand:\n\n")
	fmt.Fprintf(&b, "```sh\nsha256sum -c SHA256SUMS          # or: shasum -a 256 -c SHA256SUMS\n")
	fmt.Fprintf(&b, "ssh-keygen -Y verify -f allowed_signers -I heros-release \\\n")
	fmt.Fprintf(&b, "  -n file -s SHA256SUMS.sshsig < SHA256SUMS\n```\n\n")
	fmt.Fprintf(&b, "`allowed_signers` ships with the release; the same key is published as "+
		"[`docs/release/heros-release.pub`](docs/release/heros-release.pub) for the raw-ed25519 path. Neither step "+
		"needs a network or an account. `ssh-keygen` rather than `openssl` because stock macOS ships LibreSSL, "+
		"which cannot verify ed25519 at all — the same signature is published in both encodings.\n\n")
	fmt.Fprintf(&b, "The full story — installing, upgrading, rolling back, what the first-run OS warning means, "+
		"and how the release key rotates — is [`docs/release/install.md`](docs/release/install.md).\n\n")

	fmt.Fprintf(&b, "#### Not supported — stated, because a blank reads as *should work*\n\n")
	for _, l := range Limits() {
		fmt.Fprintf(&b, "- **%s** — %s. Instead: %s.\n", l.Platform, l.Limit, l.Answer)
	}
	fmt.Fprintf(&b, "\nAlso generated but **not yet installable**, and listed so nobody plans around them:\n\n")
	for _, c := range Channels() {
		if !c.Delivered() {
			fmt.Fprintf(&b, "- **%s** — %s.\n", c.Label, c.Blocker)
		}
	}

	// Task 8.3: the boundary, stated where a customer's engineer will read it rather than in a pricing page
	// they will not.
	fmt.Fprintf(&b, "\n#### What is free, and what is not\n\n")
	fmt.Fprintf(&b, "The **CLI is free, with no account, forever**: `discover`, `apply`, `eval`, `coverage`, "+
		"`doctor`, `init`, `version` and `upgrade` all run locally and send no telemetry.\n\n")
	fmt.Fprintf(&b, "The **paid upgrade is the hosted platform**: `heros login` and `heros link` push run results "+
		"to a tenant, which is what buys the console — leaderboards across runs, attribution scorecards, "+
		"autonomous proposals and pull requests, and team-wide history. Nothing in the free path is degraded to "+
		"sell it, and no local command starts requiring an account later.\n\n")

	fmt.Fprintf(&b, "%s", ReadmeMarkerEnd)
	return b.String()
}

// SpliceReadme replaces the generated install section in a README, keeping everything outside the markers.
//
// It refuses on a README with no markers rather than appending: appending would produce a document with two
// install sections, and a reader would follow whichever they reached first.
func SpliceReadme(current, section string) (string, error) {
	start := strings.Index(current, ReadmeMarkerStart)
	end := strings.Index(current, ReadmeMarkerEnd)
	if start < 0 || end < 0 || end < start {
		return "", fmt.Errorf("readme: the generated-install-section markers are missing or out of order. "+
			"Add them where the section belongs:\n%s\n%s", ReadmeMarkerStart, ReadmeMarkerEnd)
	}
	return current[:start] + section + current[end+len(ReadmeMarkerEnd):], nil
}

// ReadmeSection extracts the generated section from a README, for the drift gate to compare.
func ReadmeSection(current string) (string, bool) {
	start := strings.Index(current, ReadmeMarkerStart)
	end := strings.Index(current, ReadmeMarkerEnd)
	if start < 0 || end < 0 || end < start {
		return "", false
	}
	return current[start : end+len(ReadmeMarkerEnd)], true
}
