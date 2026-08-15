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

// providerendpoint_test.go proves the endpoint overrides are CONNECTED at the composition root, and
// connected to the RIGHT gateway each.
//
// 🔴 Why this file exists separately from providergateway's own tests: `WithBaseURL` sat on the Gateway
// for the whole life of this project with its only caller a demo binary. The mechanism was complete,
// tested in its own package, and reachable from nothing. Fencing providergateway alone would reproduce
// that — a green suite over a feature the running binary never invokes.

// A malformed override must fail mountCapabilities — which launch.go turns into a refused boot.
func TestAMalformedProviderEndpointRefusesTheBoot(t *testing.T) {
	t.Setenv(providergateway.BaseURLEnvName(providergateway.ScopeAnalysis, providergateway.ProviderOpenAI),
		"http://relay.example.com/v1")

	_, _, err := mountCapabilities(api.New(nil, config.Config{}), nil, t.TempDir(), "",
		providergateway.EnvSecrets{}, nil)
	if err == nil {
		t.Fatal("a plaintext provider endpoint did not stop the boot.\n" +
			"Either the validation stopped refusing it, or — the failure this file exists for — " +
			"mountCapabilities no longer reads the endpoint overrides at all, leaving the variable " +
			"inert and every gateway pointed at the vendor while an operator believes otherwise.")
	}
	if !strings.Contains(err.Error(), "provider endpoints") {
		t.Errorf("the boot failure is not attributed to the provider endpoints.\nerror: %v", err)
	}
}

// 🔴 BOTH scopes are read. Reading only one leaves the other's variable inert — and the inert one
// would be discovered by an operator wondering why their relay is not being used, or worse, not
// discovered at all because the gate quietly kept calling the vendor.
func TestBothScopesAreReadAtTheCompositionRoot(t *testing.T) {
	for _, scope := range []providergateway.BaseURLScope{
		providergateway.ScopeRehearsal, providergateway.ScopeAnalysis,
	} {
		t.Run(scope.Name(), func(t *testing.T) {
			// A value only THIS scope's reader would reject.
			t.Setenv(providergateway.BaseURLEnvName(scope, providergateway.ProviderOpenAI), "not-a-url")

			_, _, err := mountCapabilities(api.New(nil, config.Config{}), nil, t.TempDir(), "",
				providergateway.EnvSecrets{}, nil)
			if err == nil {
				t.Fatalf("the %s scope is not read at the composition root: a malformed value in it "+
					"did not stop the boot, so that variable is inert", scope.Name())
			}
		})
	}
}

// The retired single-scope name must stop the boot rather than be ignored — an operator who set it
// believes their traffic is redirected.
func TestTheRetiredSingleScopeVariableStopsTheBoot(t *testing.T) {
	t.Setenv("HEROS_PROVIDER_OPENAI_BASE_URL", "https://relay.example.com/v1")

	_, _, err := mountCapabilities(api.New(nil, config.Config{}), nil, t.TempDir(), "",
		providergateway.EnvSecrets{}, nil)
	if err == nil {
		t.Fatal("the retired HEROS_PROVIDER_*_BASE_URL was ignored. It shipped in this feature's first " +
			"revision, so it is the name an operator is most likely to still have set, and ignoring it " +
			"sends their traffic to the vendor while they believe it goes to their relay.")
	}
	// The refusal has to name both replacements, or the operator cannot act on it.
	for _, want := range []string{"HEROS_REHEARSAL_PROVIDER_OPENAI_BASE_URL", "HEROS_ANALYSIS_PROVIDER_OPENAI_BASE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s.\nerror: %v", want, err)
		}
	}
}

// And the ordinary case still boots: no override changes nothing about what mounts.
func TestNoProviderEndpointOverrideLeavesTheBootUnchanged(t *testing.T) {
	if _, _, err := mountCapabilities(api.New(nil, config.Config{}), nil, t.TempDir(), "",
		providergateway.EnvSecrets{}, nil); err != nil {
		t.Fatalf("a deployment with no endpoint override failed to mount: %v", err)
	}
}

// 🔴 THE FENCE THAT MATTERS MOST, and it caught a real crossing while this was being written.
//
// The two gateways must carry DIFFERENT scopes: the rehearsal's the fixtures path, the platform
// analyser's the customer-source path. Wiring both to the same one is the whole defect this split
// exists to prevent — and it compiles, renders and boots exactly like the correct version.
//
// It happened here: a rewrite keyed on the indentation of `Gateway:` matched BOTH sites, because
// `"Gateway:     "` contains `"Gateway:   "`. Both got the rehearsal scope, the build stayed green,
// and every customer analysis would have been posted to the gate's relay. Nothing else in this
// repository would have noticed.
func TestTheTwoGatewaysCarryDifferentEndpointScopes(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(".", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	type site struct {
		file, line, text string
	}
	var sites []site
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, rerr := os.ReadFile(f)
		if rerr != nil {
			t.Fatalf("read %s: %v", f, rerr)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if strings.Contains(line, "providergateway.New(") {
				sites = append(sites, site{f, string(rune('0' + i%10)), strings.TrimSpace(line)})
			}
		}
	}

	// 🔴 Measuring something. If the constructions move or are renamed this reads zero sites and would
	// otherwise pass vacuously — the way a fence stops pointing at anything.
	if len(sites) != 2 {
		t.Fatalf("expected exactly 2 provider gateways in internal/launch, found %d: %+v\n"+
			"A third gateway needs a deliberate decision about which data it carries; zero means this "+
			"fence is looking in the wrong place and the crossing is unguarded.", len(sites), sites)
	}

	var rehearsal, analysis int
	for _, s := range sites {
		hasRehearsal := strings.Contains(s.text, "rehearsalEndpoints.Options()")
		hasAnalysis := strings.Contains(s.text, "analysisEndpoints.Options()")
		switch {
		case hasRehearsal && !hasAnalysis:
			rehearsal++
		case hasAnalysis && !hasRehearsal:
			analysis++
		default:
			t.Errorf("a gateway carries neither scope, or both:\n  %s\n"+
				"Every gateway must state which data path it serves.", s.text)
		}
	}
	if rehearsal != 1 || analysis != 1 {
		t.Errorf("the two gateways do not carry one scope each: %d rehearsal, %d analysis.\n"+
			"🔴 Both on the same scope is the crossing this split exists to prevent — if both are "+
			"`rehearsalEndpoints`, every customer analysis goes to the endpoint configured for the "+
			"gate's fixtures. It compiles and boots identically to the correct wiring.\nsites: %+v",
			rehearsal, analysis, sites)
	}
}
