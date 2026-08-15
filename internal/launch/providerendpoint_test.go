package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// providerendpoint_test.go proves the endpoint override is CONNECTED at the composition root.
//
// 🔴 The reason this file exists separately from the package's own tests: `WithBaseURL` sat on the
// Gateway for the whole life of this project with its only caller a demo binary. The mechanism was
// complete, tested in its own package, and reachable from nothing — so a deployment holding a relay
// key had no way to use it. Fencing `providergateway` alone would reproduce exactly that: a green
// suite over a feature the running binary never invokes.
//
// A capability is not connected until the composition root reads it.

// A malformed override must fail mountCapabilities — which launch.go turns into a refused boot.
//
// The assertion is deliberately about the ERROR PATH rather than the happy one: an accepted override is
// hard to observe from here without building a real gateway and issuing a request (providergateway's
// own end-to-end test does that), whereas a refusal proves the environment was READ by this function.
// If the wiring is deleted, this env var becomes inert and the error disappears.
func TestAMalformedProviderEndpointRefusesTheBoot(t *testing.T) {
	t.Setenv(providergateway.BaseURLEnvName(providergateway.ProviderOpenAI), "http://relay.example.com/v1")

	_, _, err := mountCapabilities(api.New(nil, config.Config{}), nil, t.TempDir(), "",
		providergateway.EnvSecrets{}, nil)
	if err == nil {
		t.Fatal("a plaintext provider endpoint did not stop the boot.\n" +
			"Either the validation stopped refusing it, or — the failure this file exists for — " +
			"mountCapabilities no longer reads the endpoint overrides at all, leaving the variable " +
			"inert and every gateway pointed at the vendor while an operator believes otherwise.")
	}
	if !strings.Contains(err.Error(), "provider endpoints") {
		t.Errorf("the boot failure is not attributed to the provider endpoints, so an operator has to "+
			"guess which variable stopped their deployment.\nerror: %v", err)
	}
	if !strings.Contains(err.Error(), providergateway.BaseURLEnvName(providergateway.ProviderOpenAI)) {
		t.Errorf("the boot failure does not name the variable.\nerror: %v", err)
	}
}

// And the ordinary case still boots: an unset override changes nothing about what mounts.
func TestNoProviderEndpointOverrideLeavesTheBootUnchanged(t *testing.T) {
	if _, _, err := mountCapabilities(api.New(nil, config.Config{}), nil, t.TempDir(), "",
		providergateway.EnvSecrets{}, nil); err != nil {
		t.Fatalf("a deployment with no endpoint override failed to mount: %v", err)
	}
}

// 🔴 THE FENCE THE DRILL DEMANDED. Deleting `baseURLs.Options()...` from the gateway constructions
// left every test in this repository GREEN.
//
// The tests above prove the environment is READ; providergateway's prove the option WORKS. Neither
// notices if the value stops being handed to the gateway — the override would be parsed, validated,
// logged at boot, and then dropped, which is a more convincing failure than not reading it at all
// because the boot log would still announce the redirect.
//
// That is precisely the defect class this feature exists to fix, reproduced inside its own fix: a
// mechanism that is complete, tested, and connected to nothing. So the invariant is asserted directly —
// every gateway this package builds carries the overrides.
func TestEveryGatewayBuiltAtTheCompositionRootCarriesTheEndpointOverrides(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(".", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, rerr := os.ReadFile(f)
		if rerr != nil {
			t.Fatalf("read %s: %v", f, rerr)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if !strings.Contains(line, "providergateway.New(") {
				continue
			}
			found++
			if !strings.Contains(line, "baseURLs.Options()") {
				t.Errorf("%s:%d builds a provider gateway WITHOUT the endpoint overrides:\n  %s\n"+
					"Every gateway this package builds must carry them, or a deployment's redirect "+
					"applies to some of its provider traffic and not the rest — and the two gateways "+
					"here are the rehearsal's and the serving path's, so that split would gate on one "+
					"endpoint and serve from another.", f, i+1, strings.TrimSpace(line))
			}
		}
	}
	// 🔴 And the fence must be measuring something. If the constructions move or are renamed, this
	// reads zero call sites and passes vacuously — the way a fence stops pointing at anything.
	if found == 0 {
		t.Fatal("no providergateway.New( call site was found in internal/launch. Either the gateway is " +
			"built somewhere else now — in which case this fence is looking at the wrong place and the " +
			"invariant is unguarded — or it is no longer built here at all.")
	}
}
