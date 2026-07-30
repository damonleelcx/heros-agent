package distribution

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// workflow_test.go holds `.github/workflows/release.yml` to the contracts it cannot express itself.
//
// GitHub needs runner labels statically, so the build matrix is a second copy of Shipped(). Two copies are
// tolerable only with a gate between them — otherwise the failure is a release that quietly stops shipping
// a platform, discovered by the users of that platform.

func readWorkflow(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("release workflow not readable: %v", err)
	}
	return string(b)
}

var matrixRow = regexp.MustCompile(`\{\s*goos:\s*(\w+),\s*goarch:\s*(\w+),\s*runner:\s*([\w.\-]+)\s*\}`)

// TestReleaseWorkflowMatchesTargetContract is the gate design.md's ratification record points at for D1: the
// workflow's matrix must be exactly the frozen shipped set, runner for runner.
func TestReleaseWorkflowMatchesTargetContract(t *testing.T) {
	wf := readWorkflow(t)
	rows := matrixRow.FindAllStringSubmatch(wf, -1)
	if len(rows) == 0 {
		t.Fatal("release.yml has no recognisable build matrix rows — either the matrix is gone or its format changed")
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r[1]+"/"+r[2]] = r[3]
	}
	want := map[string]string{}
	for _, tt := range Shipped() {
		want[tt.Key()] = tt.Runner
	}
	for key, runner := range want {
		if got[key] == "" {
			t.Errorf("shipped target %s has NO build job — the release would publish an incomplete matrix", key)
			continue
		}
		if got[key] != runner {
			t.Errorf("%s builds on %q in release.yml but the contract says %q — a mismatch here is a "+
				"cross-CGO build (D1)", key, got[key], runner)
		}
	}
	for key := range got {
		if want[key] == "" {
			t.Errorf("release.yml builds %s, which is not a shipped target — it would publish an asset no "+
				"channel installs and no limit row explains", key)
		}
	}
}

// TestReleaseWorkflowHasNoHumanInTheUploadPath — DevOps rule 2 (nobody uploads to a Release by hand) and
// task 2.3's "assert zero manual upload steps". The assertion is structural: the publish job must be the
// only writer, and it must not be able to leave a release half-published waiting for a human.
func TestReleaseWorkflowHasNoHumanInTheUploadPath(t *testing.T) {
	wf := readWorkflow(t)
	must := []struct{ needle, why string }{
		{"gh release upload", "nothing in the pipeline uploads the assets, so a human would have to"},
		{"--clobber", "a re-run of the same tag would fail on an already-uploaded asset and need manual cleanup (task 2.6)"},
		{"gh release create", "the Release itself is never created by the pipeline"},
		{"gh release edit", "a re-run cannot converge on an existing Release without an edit path (task 2.6)"},
	}
	for _, m := range must {
		if !strings.Contains(wf, m.needle) {
			t.Errorf("release.yml does not use %q: %s", m.needle, m.why)
		}
	}
	// A draft GA release is a button someone has to press, which is exactly the manual step task 2.3
	// removes. Draft is reached only through the plan's `draft` output, which is true only for a prerelease.
	if strings.Contains(wf, "--draft") && !strings.Contains(wf, `[ "$DRAFT" = "true" ] && flags+=(--draft`) {
		t.Error("release.yml passes --draft outside the plan's draft decision — a GA release could publish as a draft")
	}
}

// TestReleaseWorkflowGatesCannotBeSkipped — a gate with continue-on-error is decoration. The reproducibility
// test, the signing step and the release gate must each be able to fail the run.
func TestReleaseWorkflowGatesCannotBeSkipped(t *testing.T) {
	wf := readWorkflow(t)
	if strings.Contains(wf, "continue-on-error") {
		t.Error("release.yml contains continue-on-error — a release gate that cannot fail the run is not a gate")
	}
	for _, needle := range []string{
		"TestReproducibleBuild",       // task 2.5 reproducibility regression
		"herosdist gate",              // the fail-closed gate itself
		"herossign sign",              // task 2.2 signing
		"HEROS_RELEASE_PRIVATE_KEY:?", // refuse-to-start on a missing key
	} {
		if !strings.Contains(wf, needle) {
			t.Errorf("release.yml is missing %q", needle)
		}
	}
	// cancel-in-progress on a release lets a second push abort a run between "assets uploaded" and
	// "release published" — the one state a re-run cannot reason about.
	if !strings.Contains(wf, "cancel-in-progress: false") {
		t.Error("release.yml allows a release run to be cancelled mid-flight")
	}
}

// TestReleaseWorkflowParsesTheTagExactlyOnce — task 2.4. Every job that needs the version must read plan's
// output. A second `${GITHUB_REF#refs/tags/}` anywhere is a second interpretation of what is being released.
func TestReleaseWorkflowParsesTheTagExactlyOnce(t *testing.T) {
	wf := readWorkflow(t)
	if n := strings.Count(wf, "GITHUB_REF#"); n > 0 {
		t.Errorf("release.yml strips the tag from GITHUB_REF by hand in %d place(s) — the tag is parsed by "+
			"`herosdist plan`, which can refuse a malformed tag; a shell expansion cannot", n)
	}
	if n := strings.Count(wf, "herosdist plan --tag"); n == 0 && !strings.Contains(wf, "herosdist plan \\") {
		t.Error("release.yml never runs `herosdist plan` — nothing resolves the version")
	}
	// The stamped version must be asserted against the binary's own MACHINE-READABLE report, on every
	// platform. Asserting the narration on stderr instead would pass on a build whose stdout contract
	// carried the wrong version — and stdout is the copy that reaches linked run metadata.
	if strings.Count(wf, `"tool_version": "${{ needs.plan.outputs.version }}"`) < 2 {
		t.Error("release.yml does not assert the built binary's reported tool_version on both POSIX and " +
			"Windows — a misspelled -X symbol path is silently ignored by the Go linker")
	}
}

// TestReleaseScriptStampsTheOneVersionVariable holds the shell fallback and the Go generator to the same
// ldflags. Two copies of the ldflags string is how a local build and a CI build come to stamp different
// symbols, and the linker reports neither.
func TestReleaseScriptStampsTheOneVersionVariable(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "scripts", "release-cli.sh"))
	if err != nil {
		t.Skipf("release script not readable: %v", err)
	}
	script := string(b)
	v, err := ParseTag("v9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	// The script's fallback, with ${VERSION} substituted, must equal what Version.LDFlags() produces.
	fallback := strings.ReplaceAll(
		`-s -w -X github.com/heros-foreal/agentd/internal/cli.ToolVersion=${VERSION}`,
		"${VERSION}", v.Version)
	if fallback != v.LDFlags() {
		t.Errorf("the script's ldflags fallback and Version.LDFlags() have drifted:\n  script: %s\n  go:     %s",
			fallback, v.LDFlags())
	}
	if !strings.Contains(script, `LDFLAGS="${HEROS_LDFLAGS:-`) {
		t.Error("release-cli.sh does not accept HEROS_LDFLAGS — CI would compute the version twice")
	}
	if !strings.Contains(script, `sig="$(go run ./cmd/herossign sign`) {
		t.Error("release-cli.sh writes the signature with a redirect — a failed signing would leave a " +
			"zero-byte .sig that later steps read as a present signature")
	}
}
