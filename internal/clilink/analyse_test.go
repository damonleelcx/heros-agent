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
