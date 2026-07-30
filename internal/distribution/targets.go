// Package distribution is the frozen P20 distribution contract: which build targets exist, which are
// disclosed limits, and what each artifact is named.
//
// # Why this is a package and not a table in a README
//
// System Designer rule, ratified as P20 task 1.3: *a matrix on a README is a contract*. The moment
// "macOS arm64 ✅" is published, a user's CI depends on it, and quietly dropping the row is a breaking
// change dressed as a docs edit. A contract that only exists as prose has no gate — so the matrix lives
// here, in one place, and everything that would otherwise hold a second copy is generated from it:
//
//	release.yml's matrix rows      → checked against Shipped() by TestReleaseWorkflowMatrixMatchesContract
//	the Homebrew/Scoop/winget/nfpm manifests → generated from it (D5)
//	the README's support table     → checked against it (task 8.1's honesty gate)
//	`heros doctor` / `heros upgrade` → refuse on it, naming the row
//	the console's install surface   → renders it
//
// # Why the table is TOTAL, including the rows we do not ship
//
// This is the P13 coverage lesson applied to distribution. A matrix listing only what works forces the
// reader to infer everything else from an ABSENCE — and an absence reads as "should work, must be your
// setup". A user on `windows/arm64` who finds no row does not conclude "not built"; they conclude the
// download is broken and open a ticket. So `windows/arm64` and musl/Alpine are ROWS here, each with the
// reason it is not built and the thing to do instead. `TargetFor` returns them, so a refusal can name the
// row rather than saying "unsupported platform".
package distribution

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Support is a row's state. There are exactly two, and the second one is the reason this type exists: a
// row that is not shipped is *stated*, never absent.
type Support string

const (
	// SupportShipped means the pipeline builds this target on a native runner, the Release carries the
	// asset, and the checksum + signature cover it.
	SupportShipped Support = "shipped"
	// SupportLimit means this target is deliberately NOT built. The row carries why and what to do
	// instead. It is a disclosed limit (PRD NFR5), not a gap to be discovered by a user.
	SupportLimit Support = "limit"
)

// Target is one row of the supported-target matrix.
type Target struct {
	// GOOS/GOARCH identify the row to a program. Empty GOARCH means the row covers every arch (the
	// musl row: the libc, not the arch, is what excludes it).
	GOOS   string
	GOARCH string
	// Platform is the row as a human reads it, including the floor we actually test against. The glibc
	// floor is part of the contract: a binary built against a newer glibc will not run on an older
	// distro, and "Linux" without a version is the kind of claim that turns into a support ticket.
	Platform string
	// Runner is the GitHub-hosted runner label that builds this row NATIVELY (D1). Empty for a limit
	// row — and that emptiness is the assertion that no job builds it.
	Runner string
	// Support is shipped or a disclosed limit.
	Support Support
	// Limit is why the row is not built. Required when Support == SupportLimit.
	Limit string
	// Answer is what a user on this row does instead. Required when Support == SupportLimit — a limit
	// with no answer is an apology, and this matrix is not for apologising. It may name a different row
	// ("use the container image") or say plainly that there is nothing yet.
	Answer string
	// Channels are the install channels that serve this row, by channel ID. It is stated per row rather
	// than derived, because "Homebrew serves Linux" is true and "Scoop serves macOS" is not, and the
	// difference is not computable from the OS name.
	Channels []string
}

// Key is the "goos/goarch" identity used in asset names and log lines. An arch-less row keys as
// "linux/*", which is never a Go target and so can never be mistaken for one.
func (t Target) Key() string {
	if t.GOARCH == "" {
		return t.GOOS + "/*"
	}
	return t.GOOS + "/" + t.GOARCH
}

// targets is the frozen matrix, ratified as P20 task 1.3.
//
// 🔴 Changing this slice is a contract change, not an edit. Adding a shipped row means adding the runner
// job that builds it and a smoke row that proves it; removing one is a breaking change that belongs in
// release notes. TestTargetMatrixIsFrozen makes the change deliberate by failing on any drift.
var targets = []Target{
	{
		GOOS: "darwin", GOARCH: "amd64", Platform: "macOS 12+ (Intel)",
		Runner: "macos-13", Support: SupportShipped,
		Channels: []string{"curl-sh", "homebrew", "container"},
	},
	{
		GOOS: "darwin", GOARCH: "arm64", Platform: "macOS 12+ (Apple silicon)",
		Runner: "macos-14", Support: SupportShipped,
		Channels: []string{"curl-sh", "homebrew", "container"},
	},
	{
		GOOS: "linux", GOARCH: "amd64", Platform: "Linux glibc 2.31+ (Ubuntu 20.04+, Debian 11+, RHEL 9+)",
		Runner: "ubuntu-22.04", Support: SupportShipped,
		Channels: []string{"curl-sh", "homebrew", "deb", "rpm", "container"},
	},
	{
		GOOS: "linux", GOARCH: "arm64", Platform: "Linux glibc 2.31+ on arm64",
		Runner: "ubuntu-22.04-arm", Support: SupportShipped,
		Channels: []string{"curl-sh", "homebrew", "deb", "rpm", "container"},
	},
	{
		GOOS: "windows", GOARCH: "amd64", Platform: "Windows 10/11 (x64)",
		Runner: "windows-2022", Support: SupportShipped,
		Channels: []string{"powershell", "scoop", "winget"},
	},
	{
		GOOS: "windows", GOARCH: "arm64", Platform: "Windows 11 (arm64)",
		Support: SupportLimit,
		Limit:   "not built — no native windows/arm64 runner in the matrix, and the CGO tree-sitter frontends make a cross-build a different, less-tested artifact (D1)",
		Answer:  "run the windows/amd64 build under Windows' x64 emulation, or ask for the row: adding it is a new runner, not a redesign",
	},
	{
		GOOS: "linux", GOARCH: "", Platform: "Alpine / any musl Linux",
		Support: SupportLimit,
		Limit:   "no native musl binary — the CLI links CGO tree-sitter frontends against glibc, and a glibc binary does not run on musl (D6)",
		Answer:  "use the container image ghcr.io/heros-foreal/heros:<version>, which carries the same CLI in a glibc base",
	},
}

// Targets returns the total matrix — every shipped row and every disclosed limit.
func Targets() []Target {
	out := make([]Target, len(targets))
	copy(out, targets)
	return out
}

// Shipped returns only the rows the pipeline builds. This is what release.yml's matrix must equal.
func Shipped() []Target {
	var out []Target
	for _, t := range targets {
		if t.Support == SupportShipped {
			out = append(out, t)
		}
	}
	return out
}

// Limits returns only the disclosed limits. The README's limits section is generated from this, so a
// limit cannot be dropped from the docs while remaining true.
func Limits() []Target {
	var out []Target
	for _, t := range targets {
		if t.Support == SupportLimit {
			out = append(out, t)
		}
	}
	return out
}

// TargetFor answers "what does this machine get" for a GOOS/GOARCH pair.
//
// It returns limit rows too. That is the point: a caller on Alpine gets the musl row with its reason and
// its answer, and can refuse by NAMING it, instead of emitting "unsupported platform: linux/amd64" —
// which is both unhelpful and, on Alpine, wrong, since linux/amd64 is a shipped row.
//
// Exact arch match wins over an arch-less row, so linux/amd64 resolves to the shipped binary and an
// unknown linux arch falls through to nothing rather than being told it is a musl problem.
func TargetFor(goos, goarch string) (Target, bool) {
	for _, t := range targets {
		if t.GOOS == goos && t.GOARCH == goarch {
			return t, true
		}
	}
	return Target{}, false
}

// AssetName is the single definition of what a released binary is called.
//
// scripts/release-cli.sh builds this name, install.sh reconstructs it to download, the manifest lists it,
// `heros upgrade` asks for it, and every generated package manifest points at it. Five places, one
// definition — TestAssetNameMatchesReleaseScript holds the shell copy to this one.
func AssetName(version, goos, goarch string) string {
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("heros-%s-%s-%s%s", version, goos, goarch, ext)
}

// ExpectedAssets is the complete set of binary asset names a release of this version must carry.
//
// The pipeline's completeness gate compares the uploaded set against this one and fails closed on a
// short set (task 2.5). A release missing a target is worse than a failed release: the missing row's
// users get a 404 from an installer that otherwise looks healthy.
func ExpectedAssets(version string) []string {
	var out []string
	for _, t := range Shipped() {
		out = append(out, AssetName(version, t.GOOS, t.GOARCH))
	}
	sort.Strings(out)
	return out
}

// MatrixVersion is a content hash of the frozen matrix.
//
// It is displayed by the console and by `heros doctor` for the reason the coverage table's version is:
// when a user's binary and the website disagree about whether their platform is supported, the only
// diagnosable form of that conversation is one where both sides can name their table.
func MatrixVersion() string {
	var b strings.Builder
	for _, t := range targets {
		fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%s|%s\n",
			t.Key(), t.Platform, t.Runner, t.Support, t.Limit, t.Answer, strings.Join(t.Channels, ","))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "targets-" + hex.EncodeToString(sum[:8])
}
