package launch

import (
	"strings"
	"testing"
)

// compilesandbox_test.go guards the one line where this platform could claim a security posture it does
// not have.
//
// sandbox.NewContainedEnforcer ADVERTISES network denial and filesystem scope as in force. It does not
// provide them — the outer container does — and its own doc calls using it on a bare host "the one thing
// the fail-closed design exists to prevent". The build gate runs a customer's compiler, so a wrong
// answer here is a customer's build executing with network access and a writable host.

// 🔴 The default is NO ISOLATE. A deployment that has declared nothing must not get a sandbox that
// claims containment, and the gate must fall back to parsing rather than to building on the host.
func TestWithoutADeclarationThereIsNoIsolate(t *testing.T) {
	t.Setenv(SandboxContainedEnv, "")
	s, why := compileSandbox()
	if s != nil {
		t.Fatal("an undeclared deployment was given a sandbox that advertises containment it may not have")
	}
	if !strings.Contains(why, SandboxContainedEnv) {
		t.Errorf("the capability line must name what is unset, got %q", why)
	}
	if !strings.Contains(why, "parsed") {
		t.Errorf("the capability line must say the diff is parsed rather than built, got %q", why)
	}
}

// Anything other than an exact opt-in is treated as no declaration. A posture claim is not the place to
// accept "true", "yes" or "0 " — a typo must fall to the safe side.
func TestOnlyAnExactOptInCounts(t *testing.T) {
	for _, v := range []string{"0", "true", "yes", "TRUE", "on", " 1x", "01"} {
		t.Setenv(SandboxContainedEnv, v)
		if s, _ := compileSandbox(); s != nil {
			t.Errorf("%q was read as a containment declaration; only an exact `1` may be", v)
		}
	}
}

func TestADeclaredDeploymentGetsTheIsolate(t *testing.T) {
	t.Setenv(SandboxContainedEnv, "1")
	s, why := compileSandbox()
	if s == nil {
		t.Fatal("a declared deployment got no isolate, so its build gate can never run")
	}
	if !strings.Contains(why, "compiled") {
		t.Errorf("the capability line must say the diff is compiled, got %q", why)
	}
}
