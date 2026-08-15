package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// p30_agent_seam_test.go is workstream 7 at the HTTP boundary: the definition read, and a customer-side
// result arriving on the ingest that already carries structure.

// agentSeam wires a server with a real PlatformSource over in-memory stores, and returns the placement
// store so a test can decide what the tenant is.
func agentSeam(t *testing.T) (*Server, *herosagent.MemPlacementStore, *herosagent.MemInferenceStore, string) {
	t.Helper()
	return agentSeamWithModel(t, fixedModel{provider: "anthropic", modelID: "claude-x"})
}

// agentSeamWithModel is agentSeam with the bound model chosen by the caller, so a test can vary what
// the model version PINS. The credential ref is taken from the model's provider: the source refuses a
// definition whose two disagree, and that refusal is a different test's subject.
func agentSeamWithModel(t *testing.T, models fixedModel) (*Server, *herosagent.MemPlacementStore, *herosagent.MemInferenceStore, string) {
	t.Helper()
	ctx := context.Background()

	versions := herosagent.NewMemVersionStore()
	const hash = "cfg-seam"
	if err := versions.Put(ctx, herosagent.Version{
		ConfigHash: hash, RehearsalState: herosagent.RehearsalPassed, CreatedAtMS: 1,
		Definition: herosagent.Definition{
			PromptRef: "prm-1", ModelRef: "mdl-1", CredentialRef: models.provider,
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
		Models:     models,
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

type fixedModel struct {
	provider, modelID string
	// params are what the model version pins. Carried so a test can assert they reach the wire, which
	// is the whole of the defect this seam had: provider and id crossed, params did not.
	params *runlink.ModelParams
}

func (f fixedModel) Resolve(context.Context, string) (herosagent.ResolvedModel, bool, error) {
	return herosagent.ResolvedModel{Provider: f.provider, ModelID: f.modelID, Params: f.params}, true, nil
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

// ── the graph page's agent panel (tasks 8.2, 8.5–8.8) ────────────────────────────────────────────

// graphSeam wires a server serving one graph view, with an agent source a test controls.
func graphSeam(t *testing.T, view patternclassifier.GraphView, agent HerosAgentSource) *Server {
	t.Helper()
	s := New(nil, config.Config{})
	s.MountPatternGraph(stubGraphs{view: view})
	if agent != nil {
		s.MountHerosAgent(agent)
	}
	return s
}

type stubGraphs struct{ view patternclassifier.GraphView }

func (g stubGraphs) GraphView(string, string) (patternclassifier.GraphView, bool) {
	return g.view, true
}

// panelSource is a HerosAgentSource whose every answer a test sets.
type panelSource struct {
	placement herosagent.Placement
	placeErr  error
	narrative string
	narrErr   error
}

func (p panelSource) PlacementFor(context.Context, string) (herosagent.Placement, error) {
	return p.placement, p.placeErr
}
func (p panelSource) ActiveDefinition(context.Context) (runlink.AgentDefinition, bool, error) {
	return runlink.AgentDefinition{}, false, nil
}
func (p panelSource) Accept(context.Context, herosagent.Submission) (herosagent.IngestResult, error) {
	return herosagent.IngestResult{}, nil
}
func (p panelSource) NarrativeFor(context.Context, string, string) (string, bool, error) {
	return p.narrative, p.narrative != "", p.narrErr
}

func readGraph(t *testing.T, s *Server) patternclassifier.GraphView {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/wf-1/pattern-graph", nil)
	rec := seamDo(t, s, req, "t-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("graph read returned %d: %s", rec.Code, rec.Body.String())
	}
	var got patternclassifier.GraphView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// inferredGraph is a view carrying one measured edge and one inferred one.
func inferredGraph() patternclassifier.GraphView {
	return patternclassifier.GraphView{
		WorkflowID: "wf-1",
		Nodes:      []patternclassifier.ViewNode{{NodeID: "a"}, {NodeID: "b"}},
		Edges: []patternclassifier.ViewEdge{
			{From: "a", To: "b", Kind: "data"},
			{From: "b", To: "a", Kind: "control", Author: "heros", Confidence: 0.88},
		},
		Composition: patternclassifier.Composition{EdgesTotal: 2, EdgesInferred: 1},
	}
}

// 🔴 TASK 8.8 — A HEROS FAILURE IS A PANEL, NOT A PAGE. The graph still answers 200 and still carries
// its nodes and edges; only the agent panel reports the fault.
func TestAnAgentFailureDegradesToAPanelAndTheGraphStillRenders(t *testing.T) {
	s := graphSeam(t, inferredGraph(), panelSource{placeErr: errors.New("the placement store is down")})
	got := readGraph(t, s)

	if len(got.Nodes) != 2 || len(got.Edges) != 2 {
		t.Fatalf("the graph lost its rule-derived facts: %d nodes, %d edges", len(got.Nodes), len(got.Edges))
	}
	if got.Agent == nil {
		t.Fatal("no agent panel was attached, so the failure is invisible")
	}
	if got.Agent.State != patternclassifier.StateUnavailable {
		t.Errorf("state is %q, want %q — `not_analysed` would claim nobody looked, which is not "+
			"something a failed read knows", got.Agent.State, patternclassifier.StateUnavailable)
	}
	if got.Agent.Failure == "" || got.Agent.Action != patternclassifier.ActionNone {
		t.Errorf("panel is %+v, want a stated failure and no offered action", got.Agent)
	}
}

// 🔴 TASK 8.2 — the narrative is ABSENT, not fabricated, when the agent is off.
func TestTheNarrativeIsAbsentRatherThanFabricatedWhenTheAgentIsOff(t *testing.T) {
	s := graphSeam(t, inferredGraph(), panelSource{
		placement: herosagent.PlacementDisabled,
		// Even with prose available, a disabled tenant gets none: the facts on the page were not the
		// agent's, so prose about them would be prose about somebody else's analysis.
		narrative: "This workflow routes on an intent classifier.",
	})
	got := readGraph(t, s)
	if got.Agent.Narrative != "" {
		t.Errorf("a disabled tenant's page carries a narrative: %q", got.Agent.Narrative)
	}
	if got.Agent.State != patternclassifier.StateNotAnalysed {
		t.Errorf("state is %q, want %q", got.Agent.State, patternclassifier.StateNotAnalysed)
	}
	// And it is NOT rendered as a failure. This is the default for every organization, so an alarming
	// panel here would report a deliberate configuration as a problem on every first visit.
	if got.Agent.Failure != "" {
		t.Errorf("the DEFAULT state carries a failure: %q", got.Agent.Failure)
	}
}

// 🔴 TASK 8.6 — the placement is attributed on the graph, and the two placements say different things
// about whose machine read the source.
func TestThePlacementIsAttributedAndTheTwoDifferInWhatTheyClaim(t *testing.T) {
	platform := readGraph(t, graphSeam(t, inferredGraph(), panelSource{
		placement: herosagent.PlacementPlatform, narrative: "assessed prose",
	}))
	customer := readGraph(t, graphSeam(t, inferredGraph(), panelSource{
		placement: herosagent.PlacementCustomer, narrative: "assessed prose",
	}))

	for _, c := range []struct {
		name string
		got  *patternclassifier.ViewAgent
		want string
		says string
	}{
		{"platform", platform.Agent, "platform", "reading your source"},
		{"customer", customer.Agent, "customer", "your own machine"},
	} {
		if c.got.Placement != c.want {
			t.Errorf("%s: placement is %q, want %q", c.name, c.got.Placement, c.want)
		}
		if !strings.Contains(c.got.PlacementSentence, c.says) {
			t.Errorf("%s: attribution reads %q, which does not tell the reader %q — whose machine read "+
				"the source is the only part a security review cares about",
				c.name, c.got.PlacementSentence, c.says)
		}
		if c.got.State != patternclassifier.StateInferred {
			t.Errorf("%s: state is %q, want %q", c.name, c.got.State, patternclassifier.StateInferred)
		}
		if c.got.Narrative == "" {
			t.Errorf("%s: the narrative was dropped from a graph carrying inferred facts", c.name)
		}
	}

	// 🔴 TASK 8.7 — the customer placement still offers an ACTION, because the reader can run it even
	// though the platform cannot. A refusal with no next step is a dead end, and the next step is one
	// line of shell.
	if customer.Agent.Action != patternclassifier.ActionRunLocally {
		t.Errorf("a customer-placed tenant is offered %q, want %q",
			customer.Agent.Action, patternclassifier.ActionRunLocally)
	}
	if !strings.Contains(customer.Agent.ActionReason, "heros analyse") {
		t.Errorf("the customer action does not name the command: %q", customer.Agent.ActionReason)
	}
}

// A graph with NO inferred facts, on a tenant whose agent is on, is `not_analysed` for THIS workflow —
// and carries no narrative, because prose about an analysis with no conclusions on the page is prose
// about something else.
func TestAnEnabledTenantWithNothingInferredIsNotAnalysedForThisWorkflow(t *testing.T) {
	plain := patternclassifier.GraphView{
		WorkflowID: "wf-1",
		Nodes:      []patternclassifier.ViewNode{{NodeID: "a"}},
		Edges:      []patternclassifier.ViewEdge{},
	}
	got := readGraph(t, graphSeam(t, plain, panelSource{
		placement: herosagent.PlacementPlatform, narrative: "assessed prose",
	}))
	if got.Agent.State != patternclassifier.StateNotAnalysed {
		t.Errorf("state is %q, want %q", got.Agent.State, patternclassifier.StateNotAnalysed)
	}
	if got.Agent.Narrative != "" {
		t.Errorf("a graph with no inferred fact carries a narrative: %q", got.Agent.Narrative)
	}
	// 🔴 AND IT MUST NOT SAY ANALYSIS IS OFF. Found by reading the rendered page: a `platform`-placed
	// organization's graph said "Analysis is off for this organization, which is the default" while
	// analysis was running for it — one sentence over two situations, wrong in the one it was in.
	if got.Agent.StateSentence != patternclassifier.SentenceNotAnalysedYet {
		t.Errorf("the sentence is %q.\n  An ENABLED organization with nothing inferred yet must not be "+
			"told analysis is off — that sends a reader to ask an operator to enable something that is "+
			"already enabled.", got.Agent.StateSentence)
	}

	// The switched-off tenant keeps the other sentence, so the two situations stay distinguishable.
	off := readGraph(t, graphSeam(t, plain, panelSource{placement: herosagent.PlacementDisabled}))
	if off.Agent.StateSentence == got.Agent.StateSentence {
		t.Error("a disabled organization and an enabled one with nothing inferred read identically")
	}
}

// 🔴 NIL, not a panel, when this deployment runs no agent at all. A panel reading "not analysed" would
// imply an agent that could be switched on, and on this deployment there is nothing to switch.
func TestADeploymentWithNoAgentAttachesNoPanel(t *testing.T) {
	got := readGraph(t, graphSeam(t, inferredGraph(), nil))
	if got.Agent != nil {
		t.Errorf("a deployment with no agent attached a panel: %+v", got.Agent)
	}
	if len(got.Edges) != 2 {
		t.Error("the graph lost its edges")
	}
}

// A narrative read that FAILS costs a paragraph and nothing else. Turning it into an `unavailable`
// panel would hide the inferred facts — which are real and on the page — to report the loss of their
// commentary.
func TestAFailedNarrativeReadDoesNotDowngradeThePanel(t *testing.T) {
	got := readGraph(t, graphSeam(t, inferredGraph(), panelSource{
		placement: herosagent.PlacementPlatform, narrErr: errors.New("the inference store is slow"),
	}))
	if got.Agent.State != patternclassifier.StateInferred {
		t.Errorf("state is %q, want %q — the facts are on the page and are real",
			got.Agent.State, patternclassifier.StateInferred)
	}
	if got.Agent.Failure != "" {
		t.Errorf("a lost paragraph became a panel failure: %q", got.Agent.Failure)
	}
}

// 🔴 The panel's copy is rendered as PLAIN TEXT by the console, so a backtick is a backtick.
//
// Found by reading the rendered page: the customer action said "Run `heros analyse --ir <path>`",
// which looks like markdown that failed to render — on the one actionable sentence the panel has. The
// fence is over every sentence the panel can produce, because the next one will be written by somebody
// who has markdown in their fingers.
func TestNoPanelSentenceCarriesMarkupTheConsoleCannotRender(t *testing.T) {
	views := []patternclassifier.GraphView{inferredGraph(), {WorkflowID: "wf-1"}}
	placements := []herosagent.Placement{
		herosagent.PlacementPlatform, herosagent.PlacementCustomer, herosagent.PlacementDisabled,
	}
	for _, view := range views {
		for _, p := range placements {
			got := readGraph(t, graphSeam(t, view, panelSource{placement: p, narrative: "prose"}))
			if got.Agent == nil {
				continue
			}
			for label, sentence := range map[string]string{
				"state_sentence":     got.Agent.StateSentence,
				"placement_sentence": got.Agent.PlacementSentence,
				"action_reason":      got.Agent.ActionReason,
			} {
				for _, markup := range []string{"`", "**", "<code>", "](", "_ "} {
					if strings.Contains(sentence, markup) {
						t.Errorf("%s under placement %s contains %q: %q\n"+
							"  The console renders these strings verbatim. Markup that does not render "+
							"makes the sentence look broken, and these are the sentences a reader acts on.",
							label, p, markup, sentence)
					}
				}
			}
		}
	}
	// The unavailable panel's sentence too — it is produced by a constructor rather than the handler.
	if strings.Contains(patternclassifier.AgentUnavailable("x").ActionReason, "`") {
		t.Error("the unavailable panel's reason carries a backtick")
	}
}

// 🔴 TASK 10.6 — AN UNRESOLVABLE CREDENTIAL SURFACES AS `unavailable`, and the graph still renders.
//
// Without the link, the two readings are indistinguishable on the page: the agent is enabled, it
// cannot reach its provider, it contributes nothing, and the panel reports "nothing has looked at
// this" — which is false, and is the reassuring one of the two. The customer waits; nobody is coming.
func TestAnUnresolvableCredentialSurfacesAsUnavailableRatherThanNotAnalysed(t *testing.T) {
	plain := patternclassifier.GraphView{
		WorkflowID: "wf-1",
		Nodes:      []patternclassifier.ViewNode{{NodeID: "a"}},
		Edges:      []patternclassifier.ViewEdge{},
	}
	s := graphSeam(t, plain, panelSource{placement: herosagent.PlacementPlatform})
	s.SetAgentReadiness(func(context.Context) herosagent.Readiness {
		return herosagent.Readiness{
			State:  herosagent.ReadyCredentialUnresolved,
			Detail: "the secret is missing",
		}
	})

	got := readGraph(t, s)
	if got.Agent.State != patternclassifier.StateUnavailable {
		t.Errorf("state is %q, want %q — an enabled agent that cannot reach its provider contributes "+
			"nothing, and reporting that as `not analysed` tells a customer to wait for something that "+
			"is not coming", got.Agent.State, patternclassifier.StateUnavailable)
	}
	if got.Agent.Failure == "" {
		t.Error("the panel carries no failure to render")
	}
	// The placement is still attributed — withholding the definition must not withhold the reason.
	if got.Agent.Placement != string(herosagent.PlacementPlatform) {
		t.Errorf("placement is %q", got.Agent.Placement)
	}
	// 🚫 And the graph still renders. Task 8.8: a HEROS failure is never a full-screen error.
	if len(got.Nodes) != 1 {
		t.Error("the rule-derived graph was lost")
	}
}

// A DISABLED tenant is not reported as `unavailable` even when the credential is broken. Nothing is
// trying to run for them, so a fault in machinery they are not using is not their state.
func TestADisabledTenantIsNotReportedAsUnavailable(t *testing.T) {
	s := graphSeam(t, inferredGraph(), panelSource{placement: herosagent.PlacementDisabled})
	s.SetAgentReadiness(func(context.Context) herosagent.Readiness {
		return herosagent.Readiness{State: herosagent.ReadyCredentialUnresolved, Detail: "broken"}
	})
	got := readGraph(t, s)
	if got.Agent.State != patternclassifier.StateNotAnalysed {
		t.Errorf("state is %q, want %q — a fault in machinery this organization is not using is not "+
			"their state", got.Agent.State, patternclassifier.StateNotAnalysed)
	}
}

// ── The pinned model parameters cross the seam ──────────────────────────────────────────────────────

// The parameters a model version pins travel with it. `ModelResolver` used to return only provider and
// model id, so `entry.Spec.Params` was dropped at the adapter and the customer ran the operator's model
// with none of the operator's settings.
func TestThePinnedModelParametersCrossToTheCustomer(t *testing.T) {
	max := 4096
	s, placements, _, _ := agentSeamWithModel(t, fixedModel{
		provider: "anthropic", modelID: "claude-x",
		params: &runlink.ModelParams{MaxTokens: &max},
	})
	placeTenant(t, placements, "t-customer", herosagent.PlacementCustomer)

	got := fetchDefinition(t, s, "t-customer")
	if got.ModelParams == nil {
		t.Fatal("the definition carries no model_params — a customer-side run would call anthropic " +
			"with no max_tokens, which the gateway refuses at the first inference")
	}
	if got.ModelParams.MaxTokens == nil || *got.ModelParams.MaxTokens != 4096 {
		t.Errorf("max_tokens = %v, want 4096", got.ModelParams.MaxTokens)
	}
}

// 🔴 THE TWO MaxTokens ARE DIFFERENT NUMBERS.
//
// `AgentDefinition.MaxTokens` is the CUMULATIVE run budget, checked against input+output after each
// call. `AgentDefinition.ModelParams.MaxTokens` is the ceiling on ONE completion and goes to the vendor.
// They share a name and nothing else. Collapsing them would either truncate every answer to the run
// budget or let a single completion spend the entire run — and both would look plausible in a diff.
func TestTheRunBudgetIsNotTheModelsMaxTokens(t *testing.T) {
	perAnswer := 4096
	s, placements, _, _ := agentSeamWithModel(t, fixedModel{
		provider: "anthropic", modelID: "claude-x",
		params: &runlink.ModelParams{MaxTokens: &perAnswer},
	})
	placeTenant(t, placements, "t-customer", herosagent.PlacementCustomer)

	got := fetchDefinition(t, s, "t-customer")
	if got.ModelParams == nil || got.ModelParams.MaxTokens == nil {
		t.Fatal("no model params crossed")
	}
	if got.MaxTokens == *got.ModelParams.MaxTokens {
		t.Fatalf("the run budget (%d) and the model's per-answer max_tokens (%d) are the same value — "+
			"either they have been wired to one source, or this fixture no longer distinguishes them",
			got.MaxTokens, *got.ModelParams.MaxTokens)
	}
	if got.MaxTokens <= 0 {
		t.Errorf("the run budget did not survive: %d", got.MaxTokens)
	}
}

// A model version that pins nothing sends nothing. `omitempty` plus a nil means the wire carries no
// `model_params` at all, which is what an OpenAI entry legitimately looks like — distinguishable from a
// platform that sent the field and left it blank.
func TestAModelPinningNoParametersSendsNone(t *testing.T) {
	s, placements, _, _ := agentSeamWithModel(t, fixedModel{provider: "openai", modelID: "gpt-5"})
	placeTenant(t, placements, "t-customer", herosagent.PlacementCustomer)

	if got := fetchDefinition(t, s, "t-customer"); got.ModelParams != nil {
		t.Errorf("parameters were invented for a model that pins none: %+v", got.ModelParams)
	}
}
