package deploy

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// selfserve_posture_test.go is P27 task 10.4: the air-gapped package asserts self-serve sign-up OFF at
// package-build time, beside the existing zero-external-origin assertion.
//
// It is the sibling of external_origins_test.go and is deliberately shaped the same way, because the two
// guarantees fail the same way — not by somebody enabling the thing, but by somebody removing the line
// that checks, in a shell script nobody reads outside a release.
//
// # Why the posture is a package property and not an install-time setting
//
// It IS also an install-time setting, and that is the problem. `HEROS_SELF_SERVE_SIGNUP` is read from
// the process environment, so an air-gapped operator could set it — and would have no way to know they
// should not, on a network where the consequence (anyone the customer's own IdP admits can mint an
// organization) produces no traffic anybody outside the room can see. The package is the last point at
// which we can state the intended posture in a way that travels with the bytes.

const selfServeGate = "../../deploy/scripts/check-self-serve-off.sh"

func runSelfServeGate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{selfServeGate}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestSelfServeGateIsConnected proves the gate goes red before anything relies on it being green.
//
// A green result from an unconnected gate is indistinguishable from a green result from a correct tree,
// and the second one is what everybody assumes — the same argument the origin gate's own self-test
// makes, applied to the gate written beside it.
func TestSelfServeGateIsConnected(t *testing.T) {
	out, err := runSelfServeGate(t, "--self-test")
	if err != nil {
		t.Fatalf("the self-serve gate's self-test failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "self-test passed") {
		t.Fatalf("the self-serve gate's self-test did not report passing:\n%s", out)
	}
	// The four cases the self-test covers, named here so deleting one from the script is visible from
	// the outside. The silent and prose cases are the ones that would otherwise rot: they assert what
	// does NOT count as a declaration, and a gate that quietly started counting prose would go green on
	// a package that says nothing.
	for _, want := range []string{
		"an affirmative value is detected",
		"an explicit negative is detected as off",
		"a package that declares nothing yields no declaration",
		"prose naming the variable is not a declaration",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the self-test no longer covers %q:\n%s", want, out)
		}
	}
}

// TestAirGappedManifestsDeclareSelfServeOff is the assertion itself.
func TestAirGappedManifestsDeclareSelfServeOff(t *testing.T) {
	out, err := runSelfServeGate(t, "../../deploy/k8s")
	if err != nil {
		t.Fatalf("the deployment manifests do not declare self-serve sign-up off:\n%s", out)
	}
	if !strings.Contains(out, "declared OFF") {
		t.Fatalf("the gate did not report the posture as declared off:\n%s", out)
	}
}

// TestTheAirGappedPackageBuildInvokesTheSelfServeGate is the wiring assertion, and it is the one that
// matters most: the gate above can be perfect and unreached.
func TestTheAirGappedPackageBuildInvokesTheSelfServeGate(t *testing.T) {
	const packager = "../../deploy/scripts/package-airgapped.sh"
	b, err := os.ReadFile(packager)
	if err != nil {
		t.Fatalf("read %s: %v", packager, err)
	}
	src := string(b)
	if !strings.Contains(src, "check-self-serve-off.sh") {
		t.Fatalf("%s no longer invokes the self-serve posture gate", packager)
	}
	// 🔴 And it must be a hard failure. A WARN-and-continue step is how a broken enterprise build shipped
	// an OSS binary in this repository's own recent history — the same sentence external_origins_test.go
	// carries, because it is the same mistake and it has already been made once here.
	idx := strings.Index(src, "check-self-serve-off.sh")
	tail := src[idx:]
	if end := strings.Index(tail, "\n# ──"); end > 0 {
		tail = tail[:end]
	}
	if !strings.Contains(tail, "die ") {
		t.Errorf("%s invokes the self-serve gate but does not fail on it; a gate whose failure is a "+
			"warning is a gate that ships the thing it refuses", packager)
	}
}
