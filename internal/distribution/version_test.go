package distribution

import (
	"strings"
	"testing"
)

// version_test.go proves the tag is the single source of the version (task 2.4) — including the cases a
// shell parameter expansion would have accepted.

// TestParseTagRefusesWhatAShellWouldAccept is the reason this parser exists. Each input below survives
// `${GITHUB_REF#refs/tags/v}` and becomes a "version" that reaches a binary, an asset name, and a Homebrew
// formula.
func TestParseTagRefusesWhatAShellWouldAccept(t *testing.T) {
	bad := []string{
		"", " ", "latest", "v1.2", "1.2.3", "v1.2.3.4", "vv1.2.3", "v 1.2.3",
		"v01.2.3",        // leading zero: two tags, one version
		"v1.2.3+build.7", // '+' is illegal in a container tag and a .deb version
		"release-1.2.3",  // a tag that is not a version
		"v1.2.3-",        // empty prerelease
	}
	for _, in := range bad {
		if v, err := ParseTag(in); err == nil {
			t.Errorf("ParseTag(%q) accepted it as version %q", in, v.Version)
		}
	}
	// A trailing-whitespace tag is trimmed, not rejected: the whitespace comes from the CI plumbing, not
	// from the human who tagged.
	if v, err := ParseTag("  v1.2.3\n"); err != nil || v.Version != "1.2.3" {
		t.Errorf("ParseTag with CI whitespace = (%v, %v)", v, err)
	}
}

// TestParseTagChannelDecidesPublication — draft/non-draft and latest are functions of the tag alone
// (tasks 2.3, 2.7). A rehearsal that publishes as GA is the failure this replaces.
func TestParseTagChannelDecidesPublication(t *testing.T) {
	ga, err := ParseTag("v0.20.0")
	if err != nil {
		t.Fatal(err)
	}
	if ga.Channel != ChannelGA || ga.Draft() || !ga.Latest() || !ga.RequiresSignature() {
		t.Errorf("GA tag: channel=%s draft=%v latest=%v signed=%v — GA must publish non-draft, as latest, signed",
			ga.Channel, ga.Draft(), ga.Latest(), ga.RequiresSignature())
	}
	rc, err := ParseTag("v0.20.0-rc.1")
	if err != nil {
		t.Fatal(err)
	}
	if rc.Channel != ChannelPrerelease || !rc.Draft() || rc.Latest() {
		t.Errorf("rc tag: channel=%s draft=%v latest=%v — a rehearsal must be a draft and must not move latest",
			rc.Channel, rc.Draft(), rc.Latest())
	}
	if !rc.RequiresSignature() {
		t.Error("a rehearsal that skips the signature rehearses nothing (task 2.7)")
	}
	if dev := DevVersion(); dev.RequiresSignature() || dev.Draft() || dev.Latest() {
		t.Errorf("dev channel is not the unsigned local case: %+v", dev)
	}
}

// TestImageTagsOnlyMoveLatestOnGA — :latest is what `docker run ghcr.io/…/heros` resolves to for anyone
// who did not pin. A prerelease moving it ships a rehearsal to every unpinned user.
func TestImageTagsOnlyMoveLatestOnGA(t *testing.T) {
	ga, _ := ParseTag("v1.0.0")
	tags := ga.ImageTags(ImageRepo)
	if len(tags) != 2 || tags[0] != ImageRepo+":1.0.0" || tags[1] != ImageRepo+":latest" {
		t.Errorf("GA image tags = %v", tags)
	}
	rc, _ := ParseTag("v1.0.0-rc.2")
	if tags := rc.ImageTags(ImageRepo); len(tags) != 1 || strings.HasSuffix(tags[0], ":latest") {
		t.Errorf("prerelease image tags = %v — a rehearsal moved :latest", tags)
	}
}

// TestLDFlagsStampTheOneVersionVariable holds the ldflags to the variable the CLI actually reports. A
// typo'd symbol path in -X is silently ignored by the Go linker: the build succeeds and the binary reports
// the hard-coded dev version, which is exactly the drift task 2.4 forbids.
func TestLDFlagsStampTheOneVersionVariable(t *testing.T) {
	v, _ := ParseTag("v0.20.1")
	got := v.LDFlags()
	want := "-X github.com/heros-foreal/agentd/internal/cli.ToolVersion=0.20.1"
	if !strings.Contains(got, want) {
		t.Fatalf("ldflags %q does not stamp %q", got, want)
	}
	if !strings.Contains(got, "-trimpath") && !strings.Contains(got, "-s -w") {
		t.Errorf("ldflags %q dropped the reproducibility flags", got)
	}
	if strings.Contains(got, "v0.20.1") {
		t.Errorf("ldflags stamp the tag rather than the version: %q — the CLI would report a leading v that "+
			"no package manifest can carry", got)
	}
}

// TestCompareRefusesToCallADowngradeAnUpgrade — `heros upgrade` decides with this. An older release is a
// legitimately signed artifact, so signature verification alone cannot tell an upgrade from a rollback
// served by someone who would like you to run the version with the bug.
func TestCompareRefusesToCallADowngradeAnUpgrade(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"v1.2.4", "v1.2.3", 1},
		{"v1.2.3", "v1.3.0", -1},
		{"v2.0.0", "v1.99.99", 1},
		{"v1.2.3-rc.1", "v1.2.3", -1}, // a prerelease is older than its own GA
		{"v1.2.3", "v1.2.3-rc.1", 1},
		{"v1.2.3-rc.1", "v1.2.3-rc.2", -1},
	}
	for _, c := range cases {
		a, err1 := ParseTag(c.a)
		b, err2 := ParseTag(c.b)
		if err1 != nil || err2 != nil {
			t.Fatalf("setup: %v %v", err1, err2)
		}
		if got := a.Compare(b); got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestImageNamespaceIsThePublishingOwner is the pre-flight this repository learned to need the hard way.
//
// Before the first rehearsal tag, ImageRepo said `ghcr.io/heros-foreal/heros` — and `heros-foreal` does not exist
// on GitHub, as either a user or an org. The image job authenticates to ghcr.io with the run's own GITHUB_TOKEN,
// scoped to the building repository, so it could never have created a package under that namespace. It would have
// failed on permissions, and since `publish` needs `image`, a full five-runner matrix would have produced nothing.
//
// The failure was cheap to find and expensive to hit. This test plus the plan-time assertion in release.yml make
// it a seconds-long red instead of a twenty-minute one.
func TestImageNamespaceIsThePublishingOwner(t *testing.T) {
	const registry = "ghcr.io/"
	if !strings.HasPrefix(ImageRepo, registry) {
		t.Fatalf("ImageRepo %q is not a ghcr.io path; the workflow's login and the plan-time namespace check both "+
			"assume it is", ImageRepo)
	}
	rest := strings.TrimPrefix(ImageRepo, registry)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		t.Fatalf("ImageRepo %q is not ghcr.io/<owner>/<name>", ImageRepo)
	}
	if parts[0] != ImageOwner {
		t.Errorf("ImageRepo's namespace is %q but ImageOwner says %q — the gate compares ImageOwner, so a "+
			"disagreement here means the gate is checking the wrong value", parts[0], ImageOwner)
	}
	// The owner must be the one that actually builds and pushes. It is asserted against the module's own
	// repository owner as recorded in PackageHomepage, which is the one place the GitHub repo is named.
	const homepagePrefix = "https://github.com/"
	if !strings.HasPrefix(PackageHomepage, homepagePrefix) {
		t.Fatalf("PackageHomepage %q is not a github.com URL", PackageHomepage)
	}
	repoOwner := strings.SplitN(strings.TrimPrefix(PackageHomepage, homepagePrefix), "/", 2)[0]
	if ImageOwner != repoOwner {
		t.Errorf("the image publishes under ghcr.io/%s/… but the repository that builds it is owned by %q.\n"+
			"A run's GITHUB_TOKEN is scoped to its own repository, so it cannot create a package in another "+
			"owner's namespace: the image job would fail on permissions and publish would produce nothing. "+
			"Either set ImageOwner/ImageRepo to %q, or give the image job a token that can write to %q.",
			ImageOwner, repoOwner, repoOwner, ImageOwner)
	}
}

// TestTheMuslAnswerPointsAtTheRealImage — the Alpine row's answer is the command a user with no other option
// pastes. A second, stale copy of the image path there is a `docker pull` that 404s for exactly the reader who
// had nowhere else to go.
func TestTheMuslAnswerPointsAtTheRealImage(t *testing.T) {
	musl, ok := TargetFor("linux", "")
	if !ok {
		t.Fatal("no musl row in the matrix")
	}
	if !strings.Contains(musl.Answer, ImageRepo) {
		t.Errorf("the musl row's answer (%q) does not name %s", musl.Answer, ImageRepo)
	}
}
