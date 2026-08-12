package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// p30_agent_seam_test.go is workstream 7 at the HTTP boundary: the definition read, and a customer-side
// result arriving on the ingest that already carries structure.

// agentSeam wires a server with a real PlatformSource over in-memory stores, and returns the placement
// store so a test can decide what the tenant is.
func agentSeam(t *testing.T) (*Server, *herosagent.MemPlacementStore, *herosagent.MemInferenceStore, string) {
	t.Helper()
	ctx := context.Background()

	versions := herosagent.NewMemVersionStore()
	const hash = "cfg-seam"
	if err := versions.Put(ctx, herosagent.Version{
		ConfigHash: hash, RehearsalState: herosagent.RehearsalPassed, CreatedAtMS: 1,
		Definition: herosagent.Definition{
			PromptRef: "prm-1", ModelRef: "mdl-1", CredentialRef: "anthropic",
			ContextRef: "ctx-1", HarnessRef: "hrn-1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := versions.Activate(ctx, hash, 2); err != nil {
		t.Fatal(err)
	}

	placements := herosagent.NewMemPlacementStore()
	inferences := herosagent.NewMemInferenceStore()
	src, err := herosagent.NewPlatformSource(herosagent.PlatformSourceConfig{
		Placements: placements,
		Versions:   versions,
		Prompts:    fixedPrompt{"You are the analyst. Propose edges only for the pairs you are shown."},
		Models:     fixedModel{provider: "anthropic", modelID: "claude-x"},
		Inferences: inferences,
		Floor:      herosagent.DefaultConfidenceFloor,
		Budget:     herosagent.DefaultBudget(),
		NowMS:      func() int64 { return 42 },
	})
	if err != nil {
		t.Fatal(err)
	}

	s := New(nil, config.Config{})
	s.MountWorkflowIR(linkingest.NewMemWorkflowIRStore())
	s.MountHerosAgent(src)
	return s, placements, inferences, hash
}

type fixedPrompt struct{ body string }

func (f fixedPrompt) Render(context.Context, string) (string, bool, error) { return f.body, true, nil }

type fixedModel struct{ provider, modelID string }

func (f fixedModel) Resolve(context.Context, string) (string, string, bool, error) {
	return f.provider, f.modelID, true, nil
}

func placeTenant(t *testing.T, store *herosagent.MemPlacementStore, tenant string, p herosagent.Placement) {
	t.Helper()
	if err := store.Set(context.Background(), herosagent.TenantPlacement{
		TenantID: tenant, Placement: p, Reason: "a test decided this", SetBy: "test",
	}); err != nil {
		t.Fatal(err)
	}
}

func seamDo(t *testing.T, s *Server, req *http.Request, tenant string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	ctx := auth.WithPrincipal(req.Context(), auth.Principal{TenantID: tenant})
	s.Mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func fetchDefinition(t *testing.T, s *Server, tenant string) runlink.AgentDefinition {
	t.Helper()
	rec := seamDo(t, s, httptest.NewRequest(http.MethodGet, runlink.AgentDefinitionPath, nil), tenant)
	if rec.Code != http.StatusOK {
		t.Fatalf("definition read returned %d: %s", rec.Code, rec.Body.String())
	}
	var def runlink.AgentDefinition
	if err := json.Unmarshal(rec.Body.Bytes(), &def); err != nil {
		t.Fatal(err)
	}
	return def
}

// 🔴 The prompt crosses ONLY to a tenant whose placement is `customer` — the placement whose whole
// meaning is that the definition executes on their hardware. D1 leaves the definition operator-only for
// SURFACES, and this is what keeps that true everywhere it can be true.
func TestTheDefinitionCrossesOnlyToACustomerPlacedTenant(t *testing.T) {
	s, placements, _, hash := agentSeam(t)

	placeTenant(t, placements, "t-customer", herosagent.PlacementCustomer)
	got := fetchDefinition(t, s, "t-customer")
	if got.ConfigHash != hash || got.Prompt == "" || !got.Runnable() {
		t.Fatalf("a customer-placed tenant got %+v, want a runnable definition", got)
	}

	for _, c := range []struct {
		tenant string
		place  herosagent.Placement
	}{
		{"t-platform", herosagent.PlacementPlatform},
		{"t-off", herosagent.PlacementDisabled},
	} {
		placeTenant(t, placements, c.tenant, c.place)
		def := fetchDefinition(t, s, c.tenant)
		if def.Prompt != "" || def.ConfigHash != "" || def.ModelID != "" {
			t.Errorf("a %s-placed tenant received the definition: %+v", c.place, def)
		}
		// 🔴 And the placement IS answered. A refusal carrying nothing would leave a CLI unable to tell
		// "you are disabled" from "the platform analyses you", which send a developer to opposite places.
		if def.Placement != string(c.place) {
			t.Errorf("placement is %q, want %q — withholding the definition must not withhold the reason",
				def.Placement, c.place)
		}
	}

	// A tenant nobody has ever configured gets the default, not an error.
	def := fetchDefinition(t, s, "t-never-seen")
	if def.Placement != string(herosagent.PlacementDisabled) {
		t.Errorf("an unconfigured tenant got placement %q, want the Q2 default", def.Placement)
	}
}

// 🚫 No field of the response can hold a provider key. Asserted REFLECTIVELY rather than by reading the
// struct, so a field added tomorrow is checked by a test nobody has to remember to update — this is the
// assertion publicroutes.go points at when it declares the route public.
func TestTheDefinitionResponseHasNoFieldForAKey(t *testing.T) {
	typ := reflect.TypeOf(runlink.AgentDefinition{})
	forbidden := []string{"key", "secret", "token", "credential", "password", "apikey", "auth"}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		// 🔴 STRING fields only, and the exclusion is reasoned rather than convenient: a credential is a
		// string, and no number or boolean can hold one. Checking every kind caught `MaxTokens` — a token
		// COUNT — which is the false positive that teaches somebody to loosen the whole rule. Narrowing
		// by TYPE keeps the check absolute on the fields where it can actually fail.
		if f.Type.Kind() != reflect.String {
			continue
		}
		name := strings.ToLower(f.Name)
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Errorf("AgentDefinition.%s could carry a credential to a customer's machine. The "+
					"platform holds no customer provider key under any placement (Q1) and does not ship "+
					"one down either: the customer's own secrets source resolves the provider NAME.", f.Name)
			}
		}
	}
	// And the value that IS sent is a provider name — short, no whitespace, no vendor key prefix.
	s, placements, _, _ := agentSeam(t)
	placeTenant(t, placements, "t-customer", herosagent.PlacementCustomer)
	def := fetchDefinition(t, s, "t-customer")
	if def.Provider != "anthropic" {
		t.Errorf("provider is %q, want the credential REFERENCE", def.Provider)
	}
}

func structurePayload(hash string, edges ...runlink.WireIREdge) runlink.WorkflowIRPayload {
	return runlink.WorkflowIRPayload{
		ContractVersion: runlink.WorkflowIRContractVersion,
		WorkflowID:      "wf-1", SourceRevision: "rev-1", IRVersion: "v1",
		AgentConfigHash: hash,
		Nodes: []runlink.WireIRNode{
			{NodeID: "a", Symbol: "one", File: "a.py"},
			{NodeID: "b", Symbol: "two", File: "b.py"},
			{NodeID: "c", Symbol: "three", File: "c.py"},
		},
		Edges: edges,
	}
}

func postStructure(t *testing.T, s *Server, tenant string, p runlink.WorkflowIRPayload) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, runlink.WorkflowIRPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return seamDo(t, s, req, tenant)
}

// 🔴 TASK 7.3 — NO SECOND TRANSPORT. The result arrives on the structure route, and the platform stores
// an inference for it.
func TestACustomerPlacedResultRidesTheStructureIngest(t *testing.T) {
	s, placements, inferences, hash := agentSeam(t)
	placeTenant(t, placements, "t-1", herosagent.PlacementCustomer)

	rec := postStructure(t, s, "t-1", structurePayload(hash,
		runlink.WireIREdge{From: "a", To: "b", Kind: "sequential", Author: runlink.AuthorFrontend},
		runlink.WireIREdge{From: "a", To: "c", Kind: "data", Author: runlink.AuthorHEROS, Confidence: 0.92},
		// Below the floor: not written, and recorded.
		runlink.WireIREdge{From: "b", To: "c", Kind: "data", Author: runlink.AuthorHEROS, Confidence: 0.11},
	))
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingest returned %d: %s", rec.Code, rec.Body.String())
	}

	stored, ok, err := inferences.Get(context.Background(), "wf-1", "rev-1", hash)
	if err != nil || !ok {
		t.Fatalf("no inference was stored (ok=%v err=%v) — the result rode the route and was dropped", ok, err)
	}
	if len(stored.Edges) != 1 || stored.Edges[0].To != "c" || stored.Edges[0].From != "a" {
		t.Errorf("stored edges are %+v, want only the above-floor one", stored.Edges)
	}
	if stored.Placement != herosagent.PlacementCustomer {
		t.Errorf("the inference is attributed to %q — the graph would name the wrong machine", stored.Placement)
	}
	var found bool
	for _, a := range stored.Abstentions {
		if a.Subject == "b→c" && a.Reason == herosagent.AbstainBelowFloor {
			found = true
		}
	}
	if !found {
		t.Errorf("the below-floor fact was dropped without a record: %+v", stored.Abstentions)
	}
}

// A `disabled` tenant's SUBMISSION is refused with a stated reason, and nothing is written — not the
// inference and not the structure.
func TestADisabledTenantsSubmissionIsRefused(t *testing.T) {
	s, placements, inferences, hash := agentSeam(t)
	placeTenant(t, placements, "t-off", herosagent.PlacementDisabled)

	rec := postStructure(t, s, "t-off", structurePayload(hash,
		runlink.WireIREdge{From: "a", To: "c", Kind: "data", Author: runlink.AuthorHEROS, Confidence: 0.92}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("returned %d, want 403 — the payload is well formed and this tenant may not submit it: %s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "default") {
		t.Errorf("the refusal does not say WHY: %s", rec.Body.String())
	}
	if inferences.Len() != 0 {
		t.Error("a refused submission stored an inference")
	}
}

// An unknown `agent_config_hash` is refused by name.
func TestAnUnknownAgentVersionIsRefusedAtTheSeam(t *testing.T) {
	s, placements, _, _ := agentSeam(t)
	placeTenant(t, placements, "t-1", herosagent.PlacementCustomer)

	rec := postStructure(t, s, "t-1", structurePayload("cfg-from-somewhere-else",
		runlink.WireIREdge{From: "a", To: "c", Kind: "data", Author: runlink.AuthorHEROS, Confidence: 0.92}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("returned %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cfg-from-somewhere-else"[:12]) {
		t.Errorf("the refusal does not name the hash: %s", rec.Body.String())
	}
}

// 🔴 THE DEPLOY-DAY ASSERTION. Q2 makes `disabled` the default, so on the day this ships every tenant is
// disabled — and a pre-P30 structure upload must be completely unaffected. This is the same submission
// that would be refused above, minus the agent facts.
func TestAPreP30StructureUploadIsUnaffectedByAnyPlacement(t *testing.T) {
	for _, place := range []herosagent.Placement{
		herosagent.PlacementDisabled, herosagent.PlacementPlatform, herosagent.PlacementCustomer,
	} {
		s, placements, inferences, _ := agentSeam(t)
		placeTenant(t, placements, "t-1", place)

		p := structurePayload("")
		p.Edges = []runlink.WireIREdge{{From: "a", To: "b", Kind: "sequential"}}
		rec := postStructure(t, s, "t-1", p)
		if rec.Code != http.StatusCreated {
			t.Errorf("a %s-placed tenant's plain structure upload returned %d: %s — on deploy day that is "+
				"every customer, and `heros link --with-ir` would break for the whole fleet at once",
				place, rec.Code, rec.Body.String())
		}
		if inferences.Len() != 0 {
			t.Errorf("a plain structure upload created an inference under placement %s", place)
		}
	}
}

// A deployment that runs no agent must not silently accept facts it cannot attribute.
func TestAnUnmountedAgentRefusesAgentFactsRatherThanStoringThem(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountWorkflowIR(linkingest.NewMemWorkflowIRStore())
	s.MountHerosAgent(nil)

	rec := postStructure(t, s, "t-1", structurePayload("cfg-anything",
		runlink.WireIREdge{From: "a", To: "c", Kind: "data", Author: runlink.AuthorHEROS, Confidence: 0.92}))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("returned %d, want 503 — a deployment with no version store cannot tell a published "+
			"config_hash from a string, and accepting the facts anyway would store what nothing can "+
			"re-derive: %s", rec.Code, rec.Body.String())
	}
}
