package launch

import (
	"os"
	"strings"
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

// ── The platform-side analysis is CONSTRUCTED outside the proof binaries ────────────────────────────
//
// 🔴 This is the same fence shape PR #99 needed for the rehearsal gate, for the same reason and one
// layer along. `herosagent.NewRunner` is documented as the platform-side runner and had exactly two
// callers: the rehearsal gate and `cmd/proof/acceptance`. Both passed `NewMemInferenceStore()`. So the
// mechanism was written, tested, and unreachable — nothing started a platform-placed analysis, and
// nothing could have kept one if it had.
//
// The assertion is the PROPERTY (a deployed path constructs it, against a durable store), not that a
// particular function exists — which would pass the moment somebody renamed it.

func TestThePlatformAnalysisIsConstructedOutsideTheProofBinaries(t *testing.T) {
	src, err := os.ReadFile("capabilities.go")
	if err != nil {
		t.Fatalf("read capabilities.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "platformanalyse.New(") {
		t.Fatal("no deployed path constructs platformanalyse.Service. The platform-side runner is " +
			"reachable only from a proof binary again, which is the state PR #99 existed to end")
	}
	// And it must be given the DURABLE store. A memory store here reproduces the exact defect: an
	// analysis that runs, costs money, and is forgotten.
	idx := strings.Index(body, "platformanalyse.New(")
	cfg := body[idx:min(idx+900, len(body))]
	if strings.Contains(cfg, "NewMemInferenceStore") {
		t.Error("the platform analysis is wired to an IN-MEMORY inference store — it would run, spend, " +
			"and forget, which is indistinguishable on every surface from never having run")
	}
	if !strings.Contains(cfg, "Inferences:  inferenceStore") && !strings.Contains(cfg, "Inferences: inferenceStore") {
		t.Error("the platform analysis is not wired to the durable inference store")
	}
}

// The analysis must never be able to fail the discovery it enriches. Asserted on the ADAPTER: Discover
// returns the summary and the error from the runner, and the analysis is started by a call that returns
// nothing — so there is no expression in which its failure reaches the caller.
func TestTheAnalysisCannotFailTheDiscovery(t *testing.T) {
	src, err := os.ReadFile("platformgraph.go")
	if err != nil {
		t.Fatalf("read platformgraph.go: %v", err)
	}
	body := string(src)
	// analyseAsync returns nothing: its result cannot be assigned into the error path.
	if !strings.Contains(body, "func (d discoveryAdapter) analyseAsync(ref sourceingest.Ref) {") {
		t.Fatal("analyseAsync no longer returns nothing — if it can return an error, Discover can " +
			"propagate it, and one unavailable provider takes out the graph as well as the commentary")
	}
	if !strings.Contains(body, "d.analyseAsync(ref)") {
		t.Fatal("discovery no longer starts the analysis")
	}
	// It must not run on the request's context: that context is cancelled when the response is written,
	// so every detached run would report as cancelled.
	if !strings.Contains(body, "context.WithTimeout(context.Background(), platformAnalysisWall)") {
		t.Error("the analysis runs on a context that is not detached — the request's context is " +
			"cancelled when the response is written, so the run would be killed immediately")
	}
	// And it must be bounded, by DROPPING rather than queueing. Asserted on the semantics, not on a
	// field name: the acquire is a select with a `default`, so a full channel falls through instead of
	// blocking. Without the default the send blocks, the goroutine count is still bounded — and every
	// push after the first waits on a provider call, which is the blocking behaviour the detachment
	// exists to avoid, arriving by a different door.
	if !strings.Contains(body, "case d.slots <- struct{}{}:") {
		t.Error("the analysis no longer acquires a slot — concurrent analyses are unbounded, and one " +
			"push storm becomes one provider call per push")
	}
	acquire := body[strings.Index(body, "case d.slots <- struct{}{}:"):]
	if !strings.Contains(acquire[:min(400, len(acquire))], "default:") {
		t.Error("the slot acquire has no `default` branch, so a full channel BLOCKS instead of " +
			"dropping — the caller waits on a provider call again, by a different route")
	}
	if !strings.Contains(body, "make(chan struct{}, 1)") {
		t.Error("the concurrency bound is not the deliberate single slot")
	}
}
