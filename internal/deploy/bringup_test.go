package deploy

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// bringup_test.go fences the three defects that `make deploy-up` found by RUNNING, and that nothing
// else could have found.
//
// Each of them was invisible to the build, to every unit test, and to review. Each was a one-line
// omission with a failure mode measured in "the product does not start" or "the product silently stops
// being what its documentation says it is". None of them has a natural home in a suite that does not
// stand the stack up — so they are asserted here, statically, against the artifacts themselves.
//
// # What these fences DO and DO NOT prove
//
// They are STATIC. They assert that the fix is still present in the artifact — not that the stack comes
// up, which needs Docker and several minutes and is `make deploy-up`'s job. That limit is stated rather
// than left for someone to discover: a static fence that is mistaken for a behavioural one is how a
// green suite ends up meaning less than people think.
//
// What they buy is the thing that actually failed: all three fixes are DELETABLE without breaking
// anything a test currently notices. These make the deletion loud.

const (
	agentdDockerfile = "../../deploy/Dockerfile.agentd"
	upScript         = "../../deploy/scripts/up.sh"
)

func readFileForTest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestDataDirectoryIsOwnedByTheUserThatWritesIt fences SQLITE_CANTOPEN.
//
// The symptom was `unable to open database file: out of memory (14)` — modernc's rendering of
// SQLITE_CANTOPEN — on every boot, forever. The cause: the image never created HEROS_DATA_DIR, so
// Docker created the named volume EMPTY AND ROOT-OWNED, and the process runs as `nonroot` on a
// read-only rootfs. The same defect exists on Kubernetes for the same reason: a freshly provisioned
// PersistentVolume is root-owned too.
//
// Both halves are asserted, because fixing one substrate and not the other is precisely what happened
// the first time anyone looked.
func TestDataDirectoryIsOwnedByTheUserThatWritesIt(t *testing.T) {
	df := readFileForTest(t, agentdDockerfile)

	// Compose/Docker half: the runtime stage must seed the data directory from the image WITH ownership.
	// distroless has no shell, so `RUN mkdir` is unavailable there — a COPY --chown is the only way, and
	// seeding the path is what makes Docker apply the image's ownership to a new volume.
	if !regexp.MustCompile(`COPY --from=build --chown=nonroot:nonroot \S+ /var/lib/heros`).MatchString(df) {
		t.Errorf("%s no longer seeds /var/lib/heros with nonroot ownership.\n"+
			"Without it Docker creates the named volume EMPTY AND ROOT-OWNED, the nonroot process cannot "+
			"create its ledger, and agentd dies with `unable to open database file` (SQLITE_CANTOPEN) on "+
			"every boot. The runtime base is distroless — no shell — so a COPY --chown from the build "+
			"stage is the mechanism.", agentdDockerfile)
	}

	// The image must still run as nonroot; if that ever changes the fence above is measuring nothing.
	if !strings.Contains(df, "USER nonroot:nonroot") {
		t.Errorf("%s no longer runs as nonroot — re-derive the ownership fix before deleting this fence",
			agentdDockerfile)
	}

	// Kubernetes half: fsGroup makes the kubelet chown the PV on mount.
	var agentd *k8sObject
	for i, o := range loadBase(t) {
		if o.Kind == "Deployment" && o.Metadata.Name == "agentd" {
			agentd = &loadBase(t)[i]
			break
		}
	}
	if agentd == nil {
		t.Fatal("no agentd Deployment in the base — this fence found nothing to check")
	}
	tmpl, _ := agentd.Spec["template"].(map[string]any)
	spec, _ := tmpl["spec"].(map[string]any)
	sc, _ := spec["securityContext"].(map[string]any)
	if sc == nil || sc["fsGroup"] == nil {
		t.Fatalf("the agentd pod securityContext has no fsGroup.\n"+
			"A freshly provisioned PersistentVolume is ROOT-OWNED, and this pod runs as nonroot on a "+
			"read-only rootfs — so without fsGroup the kubelet never chowns the volume and agentd cannot "+
			"create its ledger. This is the same defect %s fixes for Docker; fixing one substrate and not "+
			"the other is what happened the first time.", agentdDockerfile)
	}
	// The GID must match the image's user. A number that merely "works" is a number that stops working
	// the day the base image's nonroot uid changes.
	if got := sc["fsGroup"]; got != 65532 {
		t.Errorf("fsGroup is %v, want 65532 — distroless's `nonroot`, the same identity %s gives the "+
			"data directory. If the base image changed, change BOTH.", got, agentdDockerfile)
	}
}

// TestBringUpScriptIsPortableToBSDUserland fences the `sed: undefined label` defect.
//
// up.sh's progress line used a GNU `t` branch. BSD sed — macOS, which is the platform this script is
// most often run from — rejects it, so a correct bring-up printed a parse error into its own progress
// output while it waited. A script that emits diagnostics from its own plumbing reads as broken.
//
// The fence is narrow on purpose: it does not ban sed, it bans the constructs that differ between the
// two userlands. Banning sed outright would be a rule people route around.
func TestBringUpScriptIsPortableToBSDUserland(t *testing.T) {
	src := readFileForTest(t, upScript)

	gnuisms := []struct {
		pattern *regexp.Regexp
		what    string
	}{
		{regexp.MustCompile(`sed [^|\n]*;\s*t\s*;`), "a `t` branch (GNU-only; BSD sed answers `undefined label`)"},
		{regexp.MustCompile(`sed -i [^'"]`), "`sed -i` without an explicit backup suffix (BSD requires `-i ''`)"},
		{regexp.MustCompile(`\bgrep -P\b`), "`grep -P` (no PCRE in BSD grep)"},
		{regexp.MustCompile(`\breadlink -f\b`), "`readlink -f` (not in BSD readlink)"},
		{regexp.MustCompile(`\bdate -d\b`), "`date -d` (BSD date uses -v/-j)"},
	}
	for _, g := range gnuisms {
		if loc := g.pattern.FindString(src); loc != "" {
			t.Errorf("%s uses %s: %q\n"+
				"This script's most common host is macOS. A GNU-only construct there either fails or, worse, "+
				"prints its own error into the operator's progress output while the bring-up is otherwise "+
				"working — which is how a correct run comes to look broken.", upScript, g.what, loc)
		}
	}
}

// TestThePortGuardDoesNotBreakTheIdempotencyItProtects fences the subtlest of the three.
//
// up.sh advertises "re-run it any time — it is idempotent", and its port preflight is meant to let its
// OWN deployment hold the ports. That check called `docker compose ps` WITHOUT the --env-file
// arguments, so the compose file's `${VAR:?}` interpolation failed, ps printed nothing, the guard
// concluded no stack was running, and the script refused to re-run against itself.
//
// It is only ever visible on a SECOND run — which is why it survived until somebody tested the property
// the README claims rather than the property that is easy to test.
func TestThePortGuardDoesNotBreakTheIdempotencyItProtects(t *testing.T) {
	src := readFileForTest(t, upScript)

	// Locate the preflight block: from the port loop to the credentials section.
	start := strings.Index(src, "for p in \"$AGENTD_PORT\"")
	end := strings.Index(src, "# ── 20_credentials")
	if start < 0 || end <= start {
		t.Fatal("could not locate up.sh's port preflight — this fence found nothing to check")
	}
	guard := src[start:end]

	// 🔴 Comments are STRIPPED before scanning, and the first draft of this fence proves why: the guard
	// carries a comment explaining that it must NOT use `docker compose ps`, and the fence matched that
	// explanation and failed on correct code. A fence that cannot tell code from prose ABOUT code
	// punishes the person who documented the trap.
	var code []string
	for _, line := range strings.Split(guard, "\n") {
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		code = append(code, line)
	}
	guard = strings.Join(code, "\n")

	if strings.Contains(guard, "docker compose") {
		t.Errorf("up.sh's port guard calls `docker compose` again.\n" +
			"Any compose invocation here needs the --env-file arguments, because the platform file uses " +
			"`${VAR:?}` and interpolation runs before anything else. Without them the command fails, the " +
			"guard sees no running stack, and the script refuses to re-run against its OWN deployment — " +
			"breaking the idempotency this guard exists to preserve, visible only on a second run.\n" +
			"Ask the container LABEL instead: `docker ps --filter label=com.docker.compose.project=…`.")
	}
	if !strings.Contains(guard, "com.docker.compose.project") {
		t.Error("up.sh's port guard no longer identifies our own stack by its compose project label.\n" +
			"Something has to distinguish `our deployment holds this port` from `another process does`, " +
			"or every re-run is refused.")
	}
}
