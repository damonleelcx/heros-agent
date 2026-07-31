package distribution

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// version.go makes the git tag the single source of the release version (P20 task 2.4).
//
// # Why a parser and not `${GITHUB_REF#refs/tags/v}`
//
// Because the shell version cannot refuse. `v1.2` , `1.2.3`, `v1.2.3 ` and `latest` all survive a
// parameter expansion and become a "version" that gets stamped into a binary, embedded in a Homebrew
// formula, and written into an asset name — at which point the tag and the artifacts have quietly
// disagreed about what shipped, and the only place the truth exists is a Release page.
//
// So the tag is PARSED, and an unparseable tag fails the release before anything is built. The parsed
// value is then the one input every downstream artifact derives from: the ldflags stamp, the asset names,
// the manifest, the package manifests, the image tag, and the release notes.
//
// # Why the channel is derived here too
//
// The draft/non-draft decision (tasks 2.3 and 2.7) is a function of the tag and nothing else. Deriving it
// in the workflow YAML would put a second, untestable copy of the rule next to the `gh release create`
// call — and the failure mode of getting it wrong is publishing a rehearsal as if it were GA.

// ReleaseChannel is what a tag means for publication.
//
// Named ReleaseChannel rather than Channel because `Channel` in this package is an INSTALL channel (Homebrew,
// curl|sh, winget). Two different things called Channel in one package is how a reader ends up believing the
// release has a "homebrew channel" and the installer has a "prerelease channel".
type ReleaseChannel string

const (
	// ChannelGA is a plain semver tag: published as a non-draft Release, marked latest, and required to
	// be signed and complete.
	ChannelGA ReleaseChannel = "ga"
	// ChannelPrerelease is a tag with a prerelease part (v1.2.3-rc.1): published as a DRAFT and marked
	// prerelease. This is the rehearsal channel (task 2.7) — the same pipeline, the same gates, nothing
	// visible to a user who is not looking for it.
	ChannelPrerelease ReleaseChannel = "prerelease"
	// ChannelDev is a local or CI build with no release tag. It publishes nothing, and it is the ONLY
	// channel on which an unsigned manifest is tolerated.
	ChannelDev ReleaseChannel = "dev"
)

// Version is a parsed release tag.
type Version struct {
	// Tag is the git tag exactly as pushed, including the leading "v". Used to fetch and to name the
	// Release.
	Tag string
	// Version is the tag without the leading "v" — what goes into ldflags, asset names, and every
	// package manifest. Held separate from Tag because Homebrew and nfpm reject a leading "v" while the
	// git tag conventionally has one, and inferring one from the other at four call sites is how the two
	// drift.
	Version string
	// Prerelease is the part after "-", empty for a GA tag.
	Prerelease string
	Channel    ReleaseChannel
}

// tagPattern is deliberately strict: three numeric components, an optional dot-separated prerelease of
// alphanumerics, and nothing else. Build metadata ("+abc") is rejected rather than stripped, because a
// "+" is not legal in a Docker tag or a .deb version and a release that silently dropped it would produce
// artifacts whose version does not match the tag that made them.
var tagPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.\-]+))?$`)

// ParseTag resolves a git tag into the one version every artifact derives from.
//
// It fails closed on anything it does not fully understand. A release is the wrong place to be generous
// about input: the cost of refusing a malformed tag is re-tagging, and the cost of accepting one is
// artifacts that disagree with their own Release.
func ParseTag(tag string) (Version, error) {
	t := strings.TrimSpace(tag)
	if t == "" {
		return Version{}, fmt.Errorf("release version: empty tag — the release version has no source")
	}
	m := tagPattern.FindStringSubmatch(t)
	if m == nil {
		hint := ""
		if !strings.HasPrefix(t, "v") {
			hint = " (tags must start with 'v': vX.Y.Z or vX.Y.Z-rc.N)"
		} else if strings.Contains(t, "+") {
			hint = " (build metadata '+…' is not accepted: it is not legal in a container tag or a .deb version)"
		}
		return Version{}, fmt.Errorf("release version: %q is not a release tag%s", t, hint)
	}
	v := Version{Tag: t, Version: strings.TrimPrefix(t, "v"), Prerelease: m[4]}
	if v.Prerelease == "" {
		v.Channel = ChannelGA
	} else {
		v.Channel = ChannelPrerelease
	}
	return v, nil
}

// DevVersion is the version a build with no release tag carries. It is a valid Version so that every code
// path — including the fail-closed gates — is exercised by ordinary local builds rather than being reached
// for the first time during a real release.
func DevVersion() Version {
	return Version{Tag: "", Version: "0.0.0-dev", Prerelease: "dev", Channel: ChannelDev}
}

// Draft reports whether the Release for this version is published as a draft.
//
// GA is non-draft — task 2.3's "no manual step" means nobody has to press a button after the pipeline
// finishes, and a draft Release is exactly such a button. A prerelease IS a draft, because a rehearsal
// (task 2.7) that shows up as a real Release teaches users to install rehearsals.
func (v Version) Draft() bool { return v.Channel == ChannelPrerelease }

// Latest reports whether this Release should be marked as the repository's latest. Only GA.
func (v Version) Latest() bool { return v.Channel == ChannelGA }

// RequiresSignature reports whether an unsigned manifest is a hard failure. Every channel but dev.
//
// This is one half of task 2.5's fail-closed rule, and it is a method on Version rather than a check at
// the call site so that the answer cannot differ between the merge job and the publish job.
func (v Version) RequiresSignature() bool { return v.Channel != ChannelDev }

// ImageRepo is the one place the container repository is named (D6/task 3.6).
//
// It is a constant rather than a workflow variable because it appears in the image build, the README, the
// musl limit row's answer, and `heros doctor`'s Alpine refusal — and a repository name that is right in
// three of those four places is a broken `docker pull` for the reader who hit the fourth.
//
// 🔴 The NAMESPACE must be the owner of the repository that builds it. This said `heros-foreal` until
// 2026-07-30, when a pre-flight check before the first rehearsal tag found that `heros-foreal` does not exist
// on GitHub at all (404 as both a user and an org). The image job authenticates to ghcr.io with the run's own
// GITHUB_TOKEN, which is scoped to this repository — so it could never have created a package under someone
// else's namespace. The `image` job would have failed on permissions, and because `publish` needs it, the
// release would have produced nothing after twenty minutes of building on five runners.
//
// `TestImageNamespaceIsThePublishingOwner` and a plan-time assertion in release.yml now hold this to the
// repository owner, so the mismatch fails in seconds rather than after the matrix.
const ImageRepo = "ghcr.io/damonleelcx/heros"

// ImageOwner is the namespace ImageRepo publishes under — the segment that must equal the building
// repository's owner. Split out so the gate compares a value rather than parsing a string in two places.
const ImageOwner = "damonleelcx"

// ImageTags is the container tags this version publishes (task 3.6). GA also moves ":latest"; a
// prerelease never does — a rehearsal that moves :latest is a rehearsal that shipped.
func (v Version) ImageTags(repo string) []string {
	tags := []string{repo + ":" + v.Version}
	if v.Latest() {
		tags = append(tags, repo+":latest")
	}
	return tags
}

// LDFlags is the exact -ldflags value the release build uses, stamping this version into the one variable
// the CLI reports (task 2.4).
//
// It is generated rather than written in the workflow because a hand-written copy in YAML is a second
// source for the version, and the failure it produces — a binary reporting a version different from the
// tag that built it — is invisible until a user reports a bug against a version that was never released.
func (v Version) LDFlags() string {
	return "-s -w -X github.com/heros-foreal/agentd/internal/cli.ToolVersion=" + v.Version
}

// Compare orders two versions numerically, prerelease sorting before its own GA release. It returns -1, 0
// or +1.
//
// `heros upgrade` needs this to answer "am I current?" without asking a server what current means, and to
// refuse a DOWNGRADE presented as an upgrade — a real attack shape, since an old release is a signed,
// legitimately-verifying artifact.
func (v Version) Compare(o Version) int {
	a, b := v.numbers(), o.numbers()
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case v.Prerelease == o.Prerelease:
		return 0
	case v.Prerelease == "": // a GA release outranks any prerelease of the same numbers
		return 1
	case o.Prerelease == "":
		return -1
	case v.Prerelease < o.Prerelease:
		return -1
	default:
		return 1
	}
}

func (v Version) numbers() [3]int {
	var out [3]int
	base := strings.SplitN(strings.TrimPrefix(v.Tag, "v"), "-", 2)[0]
	if v.Tag == "" {
		base = strings.SplitN(v.Version, "-", 2)[0]
	}
	for i, part := range strings.SplitN(base, ".", 3) {
		if i > 2 {
			break
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return [3]int{}
		}
		out[i] = n
	}
	return out
}
