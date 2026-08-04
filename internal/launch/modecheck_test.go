package launch

import (
	"strings"

	"github.com/heros-foreal/agentd/internal/providergateway"
	"testing"
)

// modecheck_test.go pins the ONE decision that stands between this deployment and charging somebody.
//
// BILLING_MODE's presence mounts collection and its value picks the mode. Three properties matter, and
// the middle one is the reason this is a test rather than a comment:
//
//	unset    → nothing mounts, and the reason names the variable. The default state of every
//	           open-core install, and it must stay reachable without configuring anything.
//	test/live→ mounts, in exactly the mode named. Nothing infers the mode from a key prefix, a
//	           hostname or NODE_ENV; a wrong inference here charges a real customer.
//	garbage  → REFUSED, not defaulted. Defaulting a typo to `test` mounts a checkout button that takes
//	           no real money on a deployment whose operator believes it does — discovered by a
//	           customer, at the worst moment. Defaulting it to `live` is worse. Neither guess is safe,
//	           so the fence asserts neither is made.
func TestCollectionModeDeclaration(t *testing.T) {
	for _, tc := range []struct{ set, wantMode, wantIn string }{
		{"", "", "BILLING_MODE is unset"},
		{"test", "test", ""},
		{"live", "live", ""},
		{"prod", "", "is not a billing mode"},
		{"LIVE", "live", ""},
	} {
		t.Setenv(BillingModeEnv, tc.set)
		_, mode, why := collectionProvider(providergateway.EnvSecrets{})
		if string(mode) != tc.wantMode {
			t.Errorf("%q -> mode %q, want %q (why=%q)", tc.set, mode, tc.wantMode, why)
		}
		if tc.wantIn != "" && !strings.Contains(why, tc.wantIn) {
			t.Errorf("%q -> why %q, want it to contain %q", tc.set, why, tc.wantIn)
		}
		if tc.wantIn == "" && why != "" {
			t.Errorf("%q -> unexpected refusal %q", tc.set, why)
		}
	}
}
