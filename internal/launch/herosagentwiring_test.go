package launch

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
)

// herosagentwiring_test.go fences the ADAPTER, not the contract.
//
// 🔴 Why this file exists at all. The seam tests in internal/api assert that a definition's
// `model_params` reach a customer — and they do it through a hand-written `fixedModel`. That fake
// returns whatever the test sets, so it proves the CONTRACT and can never prove the ADAPTER. The
// defect lived in the adapter: `Resolve` returned provider and model id and dropped
// `entry.Spec.Params`, and removing that mapping again passed every test in the repository.
//
// A fence over a fake is not a fence over the code the fake stands in for.

func TestTheAdapterCarriesThePinnedParameters(t *testing.T) {
	max, temp := 4096, 0.2
	seed := int64(7)
	got := resolvedFrom(&registry.ModelEntry{
		Spec: registry.ModelSpec{
			Provider: "anthropic", ModelID: "claude-sonnet-5",
			Params: registry.ModelParams{MaxTokens: &max, Temperature: &temp, Seed: &seed},
		},
	})
	if got.Provider != "anthropic" || got.ModelID != "claude-sonnet-5" {
		t.Fatalf("identity did not survive: %+v", got)
	}
	if got.Params == nil {
		t.Fatal("the pinned parameters were dropped — a customer-placed run would call anthropic with " +
			"no max_tokens, which the gateway refuses at the first inference")
	}
	if got.Params.MaxTokens == nil || *got.Params.MaxTokens != 4096 {
		t.Errorf("max_tokens = %v, want 4096", got.Params.MaxTokens)
	}
	if got.Params.Temperature == nil || *got.Params.Temperature != 0.2 {
		t.Errorf("temperature = %v, want 0.2", got.Params.Temperature)
	}
	if got.Params.Seed == nil || *got.Params.Seed != 7 {
		t.Errorf("seed = %v, want 7 — a pinned seed that does not travel makes the run irreproducible",
			got.Params.Seed)
	}
}

// A model version pinning nothing produces NO params object, not an empty one. `omitempty` then keeps
// the field off the wire entirely, which is what "this model declares no parameters" means — and is
// distinguishable from a platform that sent the field and failed to fill it.
func TestAModelPinningNothingProducesNoParamsObject(t *testing.T) {
	got := resolvedFrom(&registry.ModelEntry{
		Spec: registry.ModelSpec{Provider: "openai", ModelID: "gpt-5"},
	})
	if got.Params != nil {
		t.Errorf("an empty params object was produced for a model that pins none: %+v", got.Params)
	}
}

// One pinned field is enough to produce the object. Guards the "all nil → nil" shortcut from being
// written as "max_tokens nil → nil", which would silently drop a pinned temperature or seed.
func TestASinglePinnedFieldStillTravels(t *testing.T) {
	temp := 0.9
	got := resolvedFrom(&registry.ModelEntry{
		Spec: registry.ModelSpec{
			Provider: "openai", ModelID: "gpt-5",
			Params: registry.ModelParams{Temperature: &temp},
		},
	})
	if got.Params == nil || got.Params.Temperature == nil {
		t.Fatal("a pinned temperature was dropped because no max_tokens was set alongside it")
	}
}
