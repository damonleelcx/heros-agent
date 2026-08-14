package clilink

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// P30 task 7.1 — the decision `heros analyse` makes before it spends anything.

// 🔴 Each refusal says something DIFFERENT, and that is the assertion. A single "cannot analyse" would
// be correct, unhelpful, and indistinguishable across three situations whose fixes are unrelated: ask an
// operator to enable you, look at the console because the answer is already there, or wait for somebody
// to publish a definition.
func TestAnalyseRefusesEachPlacementForItsOwnReason(t *testing.T) {
	runnable := func(p herosagent.Placement) runlink.AgentDefinition {
		return runlink.AgentDefinition{
			Placement: string(p), ConfigHash: "cfg-1", Prompt: "instruction",
			Provider: "anthropic", ModelID: "claude-x", ConfidenceFloor: 0.7,
		}
	}
	for _, c := range []struct {
		name string
		def  runlink.AgentDefinition
		says string
	}{
		{"platform-placed", runnable(herosagent.PlacementPlatform), "already has the answer"},
		{"disabled", runnable(herosagent.PlacementDisabled), "which is the default"},
		{"a placement this build does not know", runlink.AgentDefinition{Placement: "somewhere-else"}, "heros upgrade"},
		{
			// Placement says `customer` and the platform sent no definition. Proceeding would call a
			// provider with a missing instruction — a bill, and an answer to a question nobody asked.
			"customer-placed with nothing published",
			runlink.AgentDefinition{Placement: string(herosagent.PlacementCustomer)},
			"published no active agent definition",
		},
	} {
		err := refuseUnrunnable(c.def)
		if err == nil {
			t.Errorf("%s: proceeded", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: says %q, which does not tell the reader %q", c.name, err, c.says)
		}
	}

	if err := refuseUnrunnable(runnable(herosagent.PlacementCustomer)); err != nil {
		t.Errorf("a customer-placed tenant with a complete definition was refused: %v", err)
	}
}

// A definition missing ANY of the four things a run needs is not runnable. Checked field by field
// rather than on the placement, because a platform that answered `customer` and sent an empty prompt is
// the case the placement check cannot see.
func TestAPartialDefinitionIsNotRunnable(t *testing.T) {
	full := runlink.AgentDefinition{
		Placement: string(herosagent.PlacementCustomer), ConfigHash: "cfg-1", Prompt: "instruction",
		Provider: "anthropic", ModelID: "claude-x", ConfidenceFloor: 0.7,
	}
	for name, strip := range map[string]func(*runlink.AgentDefinition){
		"no config hash": func(d *runlink.AgentDefinition) { d.ConfigHash = "" },
		"no prompt":      func(d *runlink.AgentDefinition) { d.Prompt = "" },
		"no provider":    func(d *runlink.AgentDefinition) { d.Provider = "" },
		"no model":       func(d *runlink.AgentDefinition) { d.ModelID = "" },
		"no floor":       func(d *runlink.AgentDefinition) { d.ConfidenceFloor = 0 },
	} {
		d := full
		strip(&d)
		if d.Runnable() {
			t.Errorf("a definition with %s reported itself runnable", name)
		}
	}
}

// Both required inputs are refused by NAME before anything reaches the network.
func TestAnalyseNamesTheInputItIsMissing(t *testing.T) {
	t.Setenv("HEROS_CONFIG_DIR", t.TempDir()) // no credential on disk
	var out, errBuf strings.Builder
	s := cli.Streams{Out: &out, Err: &errBuf}

	err := Commands{}.Analyse(cli.Config{}, s)
	if err == nil || !strings.Contains(err.Error(), "heros login") {
		t.Errorf("an unauthenticated analyse gave %v, want a refusal naming `heros login` — it has to ask "+
			"the platform which definition is active before it can run anything", err)
	}
}

// ── The pinned model parameters (the B5 wire-contract gap) ──────────────────────────────────────────
//
// `runLocally` built `ModelSpec{Provider, ModelID}` and stopped, so a customer-placed run executed the
// operator's model under NOBODY's parameters. A ModelSpec bundles all three as one versioned unit
// exactly so a ref resolves what was stored; this seam un-bundled it.
//
// Silent for OpenAI — the run just used different settings from the ones the config_hash names. FATAL
// for Anthropic: the gateway refuses a call that sets no max_tokens rather than inventing a ceiling on
// somebody's bill, so every `claude-*` definition died at the first inference, on the customer's
// machine, after their repository had already been read.

func intp(i int) *int         { return &i }
func f64p(f float64) *float64 { return &f }

func TestThePinnedParametersReachTheModelSpec(t *testing.T) {
	def := runlink.AgentDefinition{
		Provider: "anthropic", ModelID: "claude-sonnet-5",
		ModelParams: &runlink.ModelParams{MaxTokens: intp(4096), Temperature: f64p(0.2)},
	}
	got, err := modelParamsFrom(def)
	if err != nil {
		t.Fatalf("modelParamsFrom: %v", err)
	}
	if got.MaxTokens == nil || *got.MaxTokens != 4096 {
		t.Errorf("max_tokens did not reach the spec: %v — the gateway will refuse this call", got.MaxTokens)
	}
	if got.Temperature == nil || *got.Temperature != 0.2 {
		t.Errorf("temperature did not reach the spec: %v — the run would not match the definition its "+
			"own config_hash names", got.Temperature)
	}
}

// 🔴 An Anthropic definition with no parameters is refused HERE, naming the platform as the fix.
//
// This is the older-platform case: `model_params` is optional so a newer CLI still runs against a
// platform that does not send it. Letting it through would surface as the gateway's "model entry … does
// not set it", which reads as a fault in an entry the customer cannot see, on a machine with no
// registry to inspect.
func TestAnAnthropicDefinitionWithNoParametersIsRefusedWithTheRealCause(t *testing.T) {
	def := runlink.AgentDefinition{Provider: "anthropic", ModelID: "claude-sonnet-5"}
	_, err := modelParamsFrom(def)
	if err == nil {
		t.Fatal("an anthropic definition carrying no max_tokens was accepted; it dies at the first call")
	}
	msg := err.Error()
	for _, want := range []string{"max_tokens", "claude-sonnet-5", "operator"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q, so the reader cannot act on it: %s", want, msg)
		}
	}
}

// A provider that needs no parameters runs fine without them. Without this the refusal above could be
// widened to every provider and nothing would notice.
func TestANonAnthropicDefinitionNeedsNoParameters(t *testing.T) {
	def := runlink.AgentDefinition{Provider: "openai", ModelID: "gpt-5"}
	got, err := modelParamsFrom(def)
	if err != nil {
		t.Fatalf("an openai definition with no params was refused: %v", err)
	}
	if got.MaxTokens != nil {
		t.Errorf("a max_tokens was invented for openai: %v", *got.MaxTokens)
	}
}

// The provider match is case-insensitive. A catalog that spells it "Anthropic" must not slip past the
// check and fail at the vendor instead.
func TestTheAnthropicCheckIsCaseInsensitive(t *testing.T) {
	def := runlink.AgentDefinition{Provider: "Anthropic", ModelID: "claude-sonnet-5"}
	if _, err := modelParamsFrom(def); err == nil {
		t.Fatal(`provider "Anthropic" bypassed the max_tokens check`)
	}
}
