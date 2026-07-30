package distribution

import (
	"fmt"
	"sort"
	"strings"
)

// notes.go generates the release notes body (P20 tasks 2.3, 4.3).
//
// # Why generated
//
// Release notes are where a claim is cheapest to make and most expensive to be wrong about. A human writing
// them copies last release's text, and last release's text described last release's trust posture — so the
// first release after a posture change silently ships the previous release's promises. Generating them from
// the same `Attestation` the installer reads means the notes cannot describe a property the artifacts do not
// have, and the day OS signing lands the notes change themselves.
//
// What is NOT generated: the human's changelog. This function produces the *distribution* half — what is in
// the release, how to verify it, what the trust posture is, and what the disclosed limits are. A maintainer
// prepends whatever the release is actually about.

// ReleaseNotes renders the distribution section of a release's notes.
//
// imageTags is passed in rather than derived because the notes must state the tags that were actually
// pushed. If the image job failed and the release went out without it, the notes saying `docker pull …` is a
// broken instruction with our name on it.
func ReleaseNotes(a Attestation, imageTags []string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## Install\n\n")
	fmt.Fprintf(&b, "`heros` is a single self-contained binary. It needs no account and no network for "+
		"`discover`, `apply`, `eval`, `doctor` or `init` — see the install runbook for the one-command "+
		"install per OS, and `docs/release/install.md` for the offline verification steps.\n\n")

	fmt.Fprintf(&b, "## Trust posture\n\n")
	fmt.Fprintf(&b, "%s\n", a.Describe())

	fmt.Fprintf(&b, "### Verify this release yourself (offline, no account)\n\n")
	fmt.Fprintf(&b, "```sh\n")
	fmt.Fprintf(&b, "# 1. the download is intact\n")
	fmt.Fprintf(&b, "sha256sum -c SHA256SUMS        # or: shasum -a 256 -c SHA256SUMS\n")
	fmt.Fprintf(&b, "# 2. the manifest came from the holder of the heros release key\n")
	fmt.Fprintf(&b, "herossign verify --pub \"$(cat docs/release/heros-release.pub)\" \\\n")
	fmt.Fprintf(&b, "  --in SHA256SUMS --sig SHA256SUMS.sig\n")
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "Both steps run with no network beyond this download and no account. Every install "+
		"channel performs them for you and refuses to place the binary on your PATH if either fails.\n\n")

	fmt.Fprintf(&b, "## Artifacts\n\n")
	assets := append([]string(nil), a.Assets...)
	sort.Strings(assets)
	for _, name := range assets {
		fmt.Fprintf(&b, "- `%s`\n", name)
	}
	if len(imageTags) > 0 {
		fmt.Fprintf(&b, "\nContainer image:\n\n")
		for _, tag := range imageTags {
			fmt.Fprintf(&b, "- `%s`\n", tag)
		}
	}

	fmt.Fprintf(&b, "\n## Not in this release\n\n")
	fmt.Fprintf(&b, "These are stated because a missing row reads as *should work*:\n\n")
	for _, l := range Limits() {
		fmt.Fprintf(&b, "- **%s** — %s. Instead: %s.\n", l.Platform, l.Limit, l.Answer)
	}

	return b.String()
}
