package authoring

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P18 §12 — the authored harness change.
//
// The thing this file exists to make impossible: a surface that lets a user buy a nine-turn loop without
// telling them it may cost nine times as much, or that tells them a strategy is "coming soon" when it is
// permanently not coming.

// engineCoverage adapts the transform engine's table — the SAME adapter the BFF uses, so these tests read
// the boundary from where the console reads it.
type engineCoverage struct{}

func (engineCoverage) HarnessCoverage(language string) []HarnessCoverageCell {
	var out []HarnessCoverageCell
	for _, c := range transform.CoverageFor(string(variantspec.DimHarness)) {
		if !strings.EqualFold(c.Language, language) {
			continue
		}
		out = append(out, HarnessCoverageCell{
			Language: c.Language, Strategy: c.Form,
			Materializes:    c.Status == transform.CoverageMaterializes,
			Cause:           string(c.Cause),
			MissingArtifact: c.MissingArtifact,
			Note:            c.Note,
		})
	}
	return out
}

// TestHarnessStrategyOptionsAreTheClosedSet — FR42. Only registered strategies are offered, and there is
// no free-text path: a name outside the closed set cannot resolve, so offering one would be offering a
// choice that fails at seal.
func TestHarnessStrategyOptionsAreTheClosedSet(t *testing.T) {
	opts := HarnessStrategyOptions()
	if len(opts) != registry.HarnessStrategySetSize {
		t.Fatalf("the surface offers %d strategies but the sealed vocabulary has %d",
			len(opts), registry.HarnessStrategySetSize)
	}
	if !opts[0].Identity || opts[0].Strategy != registry.StrategySingleShot {
		t.Errorf("the identity is not first; it is the baseline a user compares the others against")
	}
	for _, o := range opts {
		if o.Title == "" || o.Description == "" || len(o.ParamsSchema) == 0 {
			t.Errorf("%s is offered without a label or a schema; a surface would have to invent one", o.Strategy)
		}
		if registry.HarnessStrategyNamed(o.Strategy) == nil {
			t.Errorf("%s is offered but is not a builtin strategy", o.Strategy)
		}
	}
}

// TestHarnessCostIsStatedBeforeTheChoice — FR45 / NFR15 🔴. The one thing this surface must say that no
// other authoring surface has had to: a heavier scaffold costs more per run, up to its ceiling, on EVERY
// case — and whether that is worth it is verification's answer, not the selection's.
func TestHarnessCostIsStatedBeforeTheChoice(t *testing.T) {
	for _, o := range HarnessStrategyOptions() {
		if o.Identity {
			if o.CostWarning != "" {
				t.Errorf("the identity carries a cost warning (%q); one turn costs what the un-rewritten "+
					"call costs, and warning about it would train users to ignore the warning", o.CostWarning)
			}
			if o.MaxTurnCeiling != 1 {
				t.Errorf("the identity's ceiling is %d, want 1", o.MaxTurnCeiling)
			}
			continue
		}
		if o.MaxTurnCeiling <= 1 {
			t.Errorf("%s reports a ceiling of %d; a multi-turn strategy's ceiling IS its cost, and a "+
				"surface that could not state it would be asking for a blank cheque", o.Strategy, o.MaxTurnCeiling)
		}
		if o.CostWarning == "" {
			t.Fatalf("%s carries no cost warning; a user selecting a %d-turn loop would learn its price "+
				"from a bill", o.Strategy, o.MaxTurnCeiling)
		}
		for _, must := range []string{"multiply", "verification", "held-out"} {
			if !strings.Contains(o.CostWarning, must) {
				t.Errorf("%s's cost warning does not mention %q: %s", o.Strategy, must, o.CostWarning)
			}
		}
		// 🚫 And it must not read as a recommendation in either direction.
		for _, forbidden := range []string{"recommended", "you should", "better"} {
			if strings.Contains(strings.ToLower(o.CostWarning), forbidden) {
				t.Errorf("%s's cost warning reads as advice (%q): %s", o.Strategy, forbidden, o.CostWarning)
			}
		}
	}

	// The ceiling comes from the SCHEMA, so the number a user is warned about is the number the seal
	// enforces. Sabotaging that link would be invisible without this.
	if got := harnessCeilingFromSchema(registry.ReactLoopHarness{}.ParamsSchema()); got != registry.MaxTurnsCeiling {
		t.Errorf("the ceiling read from react-loop's schema is %d but the registry's cap is %d; a user "+
			"would be warned about a different number than the one enforced", got, registry.MaxTurnsCeiling)
	}
}

// TestHarnessApplicabilityIsPerCell — FR44 🔴. The boundary is PER STRATEGY, from the engine's own
// coverage table. A single verdict for the axis would be wrong in BOTH directions here.
func TestHarnessApplicabilityIsPerCell(t *testing.T) {
	py := byStrategy(HarnessApplicabilityFor(engineCoverage{}, "python"))
	goLang := byStrategy(HarnessApplicabilityFor(engineCoverage{}, "go"))

	if len(py) == 0 || len(goLang) == 0 {
		t.Fatal("no applicability was derived; the surface would render silence, which reads as 'not applicable'")
	}

	// The identity is applicable everywhere, and never presented as refused.
	for _, m := range []map[string]HarnessApplicability{py, goLang} {
		if !m[registry.StrategySingleShot].Applicable {
			t.Error("the identity strategy is reported as inapplicable; one turn IS the un-rewritten call site")
		}
	}

	// 🔴 Per-cell, not per-axis: reflexion differs between the two languages.
	if !py["reflexion"].Applicable {
		t.Error("python/reflexion is reported as inapplicable, but the engine materializes it")
	}
	if goLang["reflexion"].Applicable {
		t.Error("go/reflexion is reported as applicable, but the engine refuses it")
	}
	if py["reflexion"].Applicable == goLang["reflexion"].Applicable {
		t.Error("the two languages report the same answer for reflexion; the read is uniform, which would " +
			"mean it is not derived from the coverage table")
	}

	// 🔴 "Not yet" and "not ever, here" are different things to tell someone.
	for _, s := range []string{"react-loop", "plan-execute", "critic-loop"} {
		for lang, m := range map[string]map[string]HarnessApplicability{"python": py, "go": goLang} {
			a := m[s]
			if a.Applicable {
				t.Errorf("%s/%s is reported as applicable; a call site has nowhere to inject a tool "+
					"executor, a planner, or a critic in any language", lang, s)
			}
			if !a.Permanent {
				t.Errorf("%s/%s is reported as a temporary gap; it is a permanent fact about call sites, "+
					"and telling a user to wait would send them to wait for nothing", lang, s)
			}
			if a.MissingArtifact != "" {
				t.Errorf("%s/%s names a missing artifact %q; there is nothing to build", lang, s, a.MissingArtifact)
			}
			if a.Reason == "" {
				t.Errorf("%s/%s is refused with no reason a user can read", lang, s)
			}
		}
	}

	// 🔴 The cause spelling is pinned to the engine's constant, so the two cannot drift silently.
	if causeNotAtCallSite != string(transform.CauseNotAtCallSite) {
		t.Fatalf("causeNotAtCallSite = %q but the engine's is %q; every 'permanent' verdict above would "+
			"silently become false", causeNotAtCallSite, transform.CauseNotAtCallSite)
	}

	// A language the engine knows nothing about is inapplicable WITH A REASON, never silently true.
	for _, a := range HarnessApplicabilityFor(engineCoverage{}, "cobol") {
		if a.Applicable {
			t.Errorf("%s is reported applicable for a language with no coverage; an unknown is not a yes", a.Strategy)
		}
		if a.Reason == "" {
			t.Errorf("%s is refused for an unknown language with no reason", a.Strategy)
		}
	}
}

func byStrategy(in []HarnessApplicability) map[string]HarnessApplicability {
	out := make(map[string]HarnessApplicability, len(in))
	for _, a := range in {
		out[a.Strategy] = a
	}
	return out
}

// TestValidateHarnessSelectionRejectsBeforeSealing — FR42. Params that violate the schema — including a
// param the strategy does not declare — are rejected, naming what was wrong, and nothing is stored.
func TestValidateHarnessSelectionRejectsBeforeSealing(t *testing.T) {
	// The REAL store with a nil database: a rejection that reached a write would panic instead.
	v := registry.NewStore(nil, nil)

	for _, c := range []struct{ name, strategy, params string }{
		{"unknown strategy", "hyper-loop", `{}`},
		{"ceiling exceeded", "react-loop", `{"max_turns":99,"stop_condition":"no-tool-call"}`},
		{"undeclared param on the identity", registry.StrategySingleShot, `{"max_turns":3}`},
		{"missing required param", "reflexion", `{"max_turns":3,"stop_condition":"max-turns"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateHarnessSelection(v, "draft", c.strategy, json.RawMessage(c.params)); err == nil {
				t.Fatalf("the surface accepted %s %s; a form that accepts what the seal would reject teaches "+
					"a user their configuration exists when it does not", c.strategy, c.params)
			}
		})
	}

	if err := ValidateHarnessSelection(v, "draft", "reflexion",
		json.RawMessage(`{"max_turns":3,"stop_condition":"max-turns","reflection_prompt":"redo"}`)); err != nil {
		t.Fatalf("a valid selection was rejected: %v", err)
	}

	// 🚫 No validator must never read as "valid".
	if err := ValidateHarnessSelection(nil, "draft", "reflexion", nil); err == nil {
		t.Fatal("a surface wired without a validator accepted a selection")
	}
	if err := ValidateHarnessSelection(v, "draft", "  ", nil); err == nil {
		t.Fatal("an empty strategy was accepted")
	}
}

// TestHarnessEditSetsAndClears — FR43. Clearing is an empty ref, not an absent field, so the derived
// override's key disappears and the bytes return exactly to where they started.
func TestHarnessEditSetsAndClears(t *testing.T) {
	set := HarnessEdit("h1")
	if set.HarnessRef == nil || *set.HarnessRef != "h1" {
		t.Fatalf("HarnessEdit did not set the ref: %+v", set)
	}
	clear := ClearHarnessEdit()
	if clear.HarnessRef == nil {
		t.Fatal("ClearHarnessEdit left the field absent; absent means 'leave this dimension alone', which " +
			"is not the same as removing the override")
	}
	if *clear.HarnessRef != "" {
		t.Fatalf("ClearHarnessEdit set %q, want the empty ref", *clear.HarnessRef)
	}

	// And both are reported as touching the harness dimension, so nothing downstream mislabels them.
	for _, e := range []Edit{set, clear} {
		found := false
		for _, d := range e.Dimensions() {
			if d == variantspec.DimHarness {
				found = true
			}
		}
		if !found {
			t.Errorf("the edit does not report the harness dimension: %v", e.Dimensions())
		}
	}
	if HarnessDimension != string(variantspec.DimHarness) {
		t.Fatalf("HarnessDimension = %q, want %q", HarnessDimension, variantspec.DimHarness)
	}
}
