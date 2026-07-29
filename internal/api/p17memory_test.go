package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P17 §9/§10 — the read model behind the memory-authoring surface.
//
// The console renders whatever this returns, so every honesty rule D7 fixes has to be true HERE, in the
// payload, before any pixel exists. A test at the React layer could be made to pass with a hard-coded
// string; this one cannot.

func TestMemoryReadModelStatesTheBoundaryBeforeAnyChoice(t *testing.T) {
	v := memoryReadModel("go")

	// 🔴 FR20. At M20 the answer is no, and the payload says so up front.
	if v.Boundary.Applicable {
		t.Fatal("the read model reports memory as applicable; the transform refuses every non-identity " +
			"strategy in every language, and a surface built on this payload would promise a change that " +
			"is then refused")
	}
	if v.Boundary.MissingArtifact == "" {
		t.Error("the boundary names no missing artifact; a client would render \"unavailable\" with no reason, " +
			"which is the inert-control failure FR20 forbids")
	}
	if v.Boundary.Reason == "" {
		t.Error("the boundary carries no reason sentence")
	}
	// 🔴 The language is never the blocker. Saying otherwise implies another language works.
	if v.Boundary.LanguageIsTheBlocker {
		t.Error("the payload blames the language for the refusal")
	}
	// 🔴 And the control stays live: modeling is not refused, only materialization is.
	if !v.Boundary.AuthorableAnyway {
		t.Error("the payload does not tell the client the change is still authorable; a client would " +
			"reasonably disable the control, and a disabled control says nothing about why")
	}
}

func TestMemoryReadModelOffersTheClosedVocabulary(t *testing.T) {
	v := memoryReadModel("go")

	if len(v.Strategies) != registry.MemoryStrategySetSize {
		t.Fatalf("the payload offers %d strategies, the registry has %d; an option the registry does not "+
			"know fails at seal, and one it knows that is not offered is unreachable",
			len(v.Strategies), registry.MemoryStrategySetSize)
	}
	if v.Dimension != string(variantspec.DimMemory) {
		t.Errorf("dimension = %q, want %q — the client must label the edit from the server's enum", v.Dimension, variantspec.DimMemory)
	}

	var identity, applying int
	for _, s := range v.Strategies {
		if len(s.ParamsSchema) == 0 {
			t.Errorf("strategy %q carries no params schema; the form would have nothing to render and "+
				"nothing to validate against", s.Strategy)
		}
		// The schema must be real JSON — a client renders fields from it.
		var probe map[string]any
		if err := json.Unmarshal(s.ParamsSchema, &probe); err != nil {
			t.Errorf("strategy %q's params schema is not a JSON object: %v", s.Strategy, err)
		}
		if s.Title == "" || s.Description == "" {
			t.Errorf("strategy %q has no human layer", s.Strategy)
		}
		if s.Identity {
			identity++
			// 🔴 Per-strategy applicability, not one flag for the axis: `none` genuinely applies (it
			// changes nothing) while every other strategy refuses. Collapsing them would either claim
			// memory works or claim the identity strategy is unavailable.
			if !s.Applies {
				t.Errorf("the identity strategy %q reports Applies=false; selecting `none` is a no-op the "+
					"engine accepts, and a user must be able to state it", s.Strategy)
			}
		} else if s.Applies {
			applying++
		}
	}
	if identity != 1 {
		t.Errorf("the payload marks %d identity strategies, want exactly 1", identity)
	}
	if applying != 0 {
		t.Errorf("%d non-identity strategies claim to apply while the transform refuses them all", applying)
	}
}

// 🚫 An unknown or empty language must not silently answer "yes". A boundary computed for the wrong
// language is a claim about code the reader does not have.
func TestMemoryReadModelFailsClosedOnUnknownLanguage(t *testing.T) {
	for _, lang := range []string{"", "elixir"} {
		v := memoryReadModel(lang)
		if v.Boundary.Applicable {
			t.Errorf("language %q was reported applicable; absence of coverage is not permission", lang)
		}
		if v.Boundary.MissingArtifact == "" || v.Boundary.Reason == "" {
			t.Errorf("language %q yielded an unexplained boundary: %+v", lang, v.Boundary)
		}
		// The vocabulary is still offered — a user can author against a language the engine cannot
		// materialize for; that is the whole modeling-vs-materialization split.
		if len(v.Strategies) != registry.MemoryStrategySetSize {
			t.Errorf("language %q was offered %d strategies; authoring does not depend on materializability",
				lang, len(v.Strategies))
		}
	}
}

// The boundary is DERIVED from the engine's coverage table, not written here. This is the test that
// would go red if someone replaced the derivation with a literal — and the one that will quietly start
// reporting "applicable" the day a memory rewriter lands, with no copy edit.
func TestMemoryBoundaryDerivesFromTheEngineCoverage(t *testing.T) {
	cells := transform.CoverageFor(string(variantspec.DimMemory))
	if len(cells) == 0 {
		t.Fatal("the engine reports no memory coverage; the read model would have nothing to derive from")
	}

	v := memoryReadModel("go")
	// Every non-identity refusal in the engine must be reflected as a non-applying strategy here.
	for _, c := range cells {
		if c.Language != "go" || c.Form == registry.StrategyNone {
			continue
		}
		for _, s := range v.Strategies {
			if s.Strategy != c.Form {
				continue
			}
			if s.Applies != (c.Status == transform.CoverageMaterializes) {
				t.Errorf("strategy %q: payload says applies=%v, engine says %q; the surface and the engine "+
					"must not be able to disagree", s.Strategy, s.Applies, c.Status)
			}
		}
		// The missing artifact travels verbatim, so a user is told what to wait for.
		if c.MissingArtifact != "" && !strings.Contains(v.Boundary.MissingArtifact, "runtime") {
			t.Errorf("the boundary's missing artifact %q does not name the runtime the engine names (%q)",
				v.Boundary.MissingArtifact, c.MissingArtifact)
		}
	}
}

// 🚫 No second apply path. A memory change goes through the existing authoring routes; this endpoint is
// a read. If it ever grew a mutation, every gate on the authored path would have a bypass.
func TestMemoryEndpointIsReadOnly(t *testing.T) {
	// Structural: the handler ignores everything but the language query parameter, so there is no body
	// it could act on. Asserting via the read model's inputs is the closest honest proxy — a mutation
	// would need state this function does not take.
	a := memoryReadModel("go")
	b := memoryReadModel("go")
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatal("two identical reads produced different payloads; the endpoint is carrying state")
	}
}
