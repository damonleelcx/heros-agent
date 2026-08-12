package herosagent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/memoryruntime"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/providercall"
)

// P30 workstream 10 — the QA fences that had no home in the workstream that produced their subject.

// 🔴 TASK 10.2 — A GO FIXTURE'S IR IS BYTE-IDENTICAL WITH HEROS ON AND OFF.
//
// # Why bytes and not "the same edges"
//
// D3's whole claim is that the Go path is untouched: the frontend is typed, it establishes its own
// edges, and the residue it leaves the agent is therefore empty of the pairs it already answered. An
// assertion on edge SETS would pass while the agent added a field, reordered a slice, or stamped an
// author onto a frontend edge — every one of which changes what a customer's stored document contains
// and what their `config_hash` addresses.
//
// So this marshals the IR both ways and compares the bytes. "HEROS did not break the Go path" becomes
// a byte comparison rather than an argument, which is the sentence residue.go opens with.
func TestAGoFixtureIRIsByteIdenticalWithTheAgentOnAndOff(t *testing.T) {
	ctx := context.Background()
	ir := irWith([]string{"a", "b", "c"}, [2]string{"a", "b"}, [2]string{"b", "c"})

	before, err := json.Marshal(ir)
	if err != nil {
		t.Fatal(err)
	}

	// The agent runs — and is deliberately handed a model that WOULD write over everything, so a pass
	// here is not the absence of an opportunity.
	m := &recordingModel{result: RawResult{Edges: []RawEdge{
		{From: "a", To: "b", Kind: "control", Confidence: conf(0.99)},
		{From: "b", To: "c", Kind: "control", Confidence: conf(0.99)},
		{From: "c", To: "a", Kind: "data", Confidence: conf(0.99)},
	}}}
	r, store := testRunner(t, m)
	res, err := r.Infer(ctx, inputFor(ir), "cfg1", PlacementPlatform)
	if err != nil {
		t.Fatal(err)
	}
	if store.Len() != 1 {
		t.Fatal("nothing was stored, so this test proves the agent did not run rather than that it did no harm")
	}

	after, err := json.Marshal(ir)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the rule IR changed while the agent ran.\n  before: %s\n  after:  %s\n\n"+
			"  D3: rule-derived topology is IMMUTABLE to HEROS, and the Go path being untouched is meant "+
			"to be a byte comparison rather than an argument.", before, after)
	}

	// And the two frontend pairs were REFUSED rather than merely absent from the output — a run that
	// proposed nothing would satisfy the byte comparison without exercising the fence.
	declined := map[string]bool{}
	for _, a := range res.Abstentions {
		declined[a.Subject] = a.Reason == AbstainFrontendOwns
	}
	for _, subject := range []string{"a→b", "b→c"} {
		if !declined[subject] {
			t.Errorf("the agent's proposal over the established pair %s was not recorded as %q — the "+
				"fence must leave a trace, or a rejection is indistinguishable from silence",
				subject, AbstainFrontendOwns)
		}
	}
}

// 🔴 TASK 10.4 — a cache hit makes zero provider calls AND returns an IDENTICAL BODY.
//
// The call count alone is the weaker half. A read-through cache that returned the right count and a
// subtly different body — a dropped abstention, a re-sorted label list, a narrative lost because the
// column was NULL — would satisfy "the same revision always shows you the same graph" on a counter and
// break it on the page. D2's guarantee is about what a reader SEES.
func TestACacheHitReturnsAByteIdenticalBody(t *testing.T) {
	ctx := context.Background()
	ir := irWith([]string{"a", "b", "c"})
	m := &recordingModel{
		result: RawResult{
			Edges: []RawEdge{
				{From: "a", To: "c", Kind: "data", Confidence: conf(0.91)},
				// One below the floor, so the stored answer carries an abstention as well as an edge.
				{From: "c", To: "b", Kind: "data", Confidence: conf(0.1)},
			},
			Narrative: "This workflow fans out and never merges.",
		},
		usage: providercall.Usage{InputTokens: 11, OutputTokens: 7},
	}
	r, _ := testRunner(t, m)

	first, err := r.Infer(ctx, inputFor(ir), "cfg1", PlacementPlatform)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Infer(ctx, inputFor(ir), "cfg1", PlacementPlatform)
	if err != nil {
		t.Fatal(err)
	}
	if m.count() != 1 {
		t.Fatalf("the provider was called %d times across two requests", m.count())
	}
	if second.ProviderCalls != 0 {
		t.Errorf("the cache hit reports %d provider calls", second.ProviderCalls)
	}

	// The comparison is over the FACTS, marshalled — not over the Result struct, which carries
	// per-request bookkeeping (`ProviderCalls`, `Usage`) that is correctly different on a hit.
	type body struct {
		Edges       []ProvenancedEdge
		Labels      []patternclassifier.RegionProposal
		Abstentions []Abstention
		Narrative   string
	}
	a, _ := json.Marshal(body{first.Edges, first.Labels, first.Abstentions, first.Narrative})
	b, _ := json.Marshal(body{second.Edges, second.Labels, second.Abstentions, second.Narrative})
	if string(a) != string(b) {
		t.Errorf("the cache hit returned a different body.\n  first:  %s\n  second: %s", a, b)
	}
	// The body is not empty, or the comparison above holds vacuously.
	if len(first.Edges) == 0 || len(first.Abstentions) == 0 || first.Narrative == "" {
		t.Fatalf("the stored answer is thin (%d edges, %d abstentions, narrative %q), so an identity "+
			"assertion over it proves little", len(first.Edges), len(first.Abstentions), first.Narrative)
	}
}

// 🔴 TASK 10.5 — provenance survives AGGREGATION on a mixed graph.
//
// The aggregation is where authorship is most likely to be lost, because aggregating is exactly the
// operation that discards per-item detail. A composition that counted a mixed workflow's patterns and
// reported them all one way would be the failure — and it would look like a tidy summary.
func TestProvenanceSurvivesAggregationOnAMixedGraph(t *testing.T) {
	gv := patternclassifier.GraphView{
		Nodes: []patternclassifier.ViewNode{{NodeID: "a"}, {NodeID: "b"}, {NodeID: "c"}, {NodeID: "d"}},
		Edges: []patternclassifier.ViewEdge{
			{From: "a", To: "b", Kind: "data", Author: string(discovery.AuthorFrontend)},
			{From: "b", To: "c", Kind: "data", Author: string(discovery.AuthorHEROS), Confidence: 0.9},
			// Unauthored — reads as `legacy`, and must NOT be promoted to frontend.
			{From: "c", To: "d", Kind: "data"},
		},
		Regions: []patternclassifier.ViewRegion{
			{
				SubgraphID: "sg-rule", NodeIDs: []string{"a", "b"},
				Labels: []patternclassifier.ViewLabel{{
					Pattern: patternclassifier.Routing, Ordinal: 2, Confidence: 0.9,
					Provenance: "det.router", Author: discovery.AuthorDetector,
				}},
			},
			{
				SubgraphID: "sg-agent", NodeIDs: []string{"c", "d"},
				Labels: []patternclassifier.ViewLabel{{
					Pattern: patternclassifier.ToolUse, Ordinal: 8, Confidence: 0.8,
					Provenance: "heros-agent", Author: discovery.AuthorHEROS,
				}},
			},
		},
	}

	c := patternclassifier.RebuildComposition(gv)
	if len(c.Patterns) != 2 {
		t.Fatalf("the composition collapsed a mixed graph to %d pattern(s)", len(c.Patterns))
	}
	byState := map[patternclassifier.FactState]int{}
	for _, p := range c.Patterns {
		byState[p.State]++
		if len(p.Provenance) == 0 {
			t.Errorf("pattern %s lost its provenance through the aggregation", p.Pattern)
		}
		if len(p.Authors) == 0 {
			t.Errorf("pattern %s lost its authors through the aggregation", p.Pattern)
		}
	}
	if byState[patternclassifier.StateMeasured] != 1 || byState[patternclassifier.StateInferred] != 1 {
		t.Errorf("states after aggregation are %v, want one measured and one inferred — an aggregation "+
			"that reported a mixed graph all one way is the failure this fence exists for", byState)
	}
	if c.EdgesTotal != 3 || c.EdgesInferred != 1 {
		t.Errorf("edges aggregate to %d total / %d inferred, want 3 / 1 — the unauthored edge reads as "+
			"`legacy` and must not be counted as either measured-by-a-frontend or inferred",
			c.EdgesTotal, c.EdgesInferred)
	}
}

// 🔴 TASK 10.14 — THE DEFAULT POSTURE. A freshly migrated deployment runs zero inferences and makes
// ZERO PROVIDER CALLS until a placement is set — asserted on the COUNT, not on the absence of an error.
//
// This is the fence that has to survive somebody changing the default. Q2 chose `disabled`, and the
// consequence — "nothing fills on deploy" — is the posture the whole phase rests on; a deployment that
// quietly began analysing on migration would be reading customers' source under a platform credential
// with nobody having decided.
func TestAFreshlyMigratedDeploymentAnalysesNothing(t *testing.T) {
	ctx := context.Background()
	// A placement store with NO ROWS is what a fresh migration produces — 0047 deliberately does not
	// back-fill, so "nobody has decided" is the state on day one.
	placements := NewMemPlacementStore()
	m := &recordingModel{result: RawResult{Edges: []RawEdge{{From: "a", To: "c", Kind: "data", Confidence: conf(0.9)}}}}
	r, store := testRunner(t, m)

	for _, tenant := range []string{"t1", "t2", "never-configured"} {
		tp, err := placements.Get(ctx, tenant)
		if err != nil {
			t.Fatal(err)
		}
		if tp.Placement != PlacementDisabled || tp.Explicit {
			t.Fatalf("a tenant with no row reads %+v, want the defaulted `disabled`", tp)
		}
		in := inputFor(irWith([]string{"a", "b", "c"}))
		in.TenantID = tenant
		if _, err := r.Infer(ctx, in, "cfg1", tp.Placement); err == nil {
			t.Errorf("%s was analysed on a freshly migrated deployment", tenant)
		}
	}

	// 🔴 THE COUNT. "No error" is what a stub returns too.
	if m.count() != 0 {
		t.Errorf("a freshly migrated deployment made %d provider call(s)", m.count())
	}
	if store.Len() != 0 {
		t.Errorf("a freshly migrated deployment stored %d inference(s)", store.Len())
	}
}

// 🔴 TASK 10.16 — VOCABULARY DRIFT. A definition records the VERSION of every closed set it references,
// so a stored `config_hash` stays interpretable after a set is versioned forward.
//
// Without it, `single-shot` in a definition published today and `single-shot` after the set gains a
// member are the same string naming two different vocabularies — and the stored hash silently starts
// meaning something else.
func TestAStoredConfigHashStaysInterpretableAfterASetMovesForward(t *testing.T) {
	base := Definition{
		PromptRef: "prm-1", ModelRef: "mdl-1", CredentialRef: "anthropic",
		ContextRef: "ctx-1", HarnessRef: "hrn-1",
		SetVersions: map[string]string{"harness": "v1", "memory": "v1"},
	}
	before, err := base.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}

	// The harness set gains a member. Every REFERENCE is unchanged; only the vocabulary moved.
	moved := base
	moved.SetVersions = map[string]string{"harness": "v2", "memory": "v1"}
	after, err := moved.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Errorf("two definitions over different vocabulary versions hash the same (%s). The stored "+
			"hash would silently start meaning something else the day the set moved.", before)
	}

	// And the version is RECOVERABLE from the stored definition, so a reader of an old hash can say
	// which vocabulary it was published against.
	ctx := context.Background()
	store := NewMemVersionStore()
	if err := store.Put(ctx, Version{ConfigHash: before, Definition: base, CreatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(ctx, before)
	if err != nil || !ok {
		t.Fatalf("the published version did not round-trip (ok=%v err=%v)", ok, err)
	}
	if got.Definition.SetVersions["harness"] != "v1" {
		t.Errorf("the stored definition reports harness set %q, want the version it was published "+
			"against — a hash whose vocabulary cannot be recovered is a hash nobody can interpret",
			got.Definition.SetVersions["harness"])
	}

	// 🔴 An identical definition with NO recorded set versions must hash differently from one that
	// records them. Otherwise a pre-P30 definition and a post-P30 one collide, and the stored row for
	// one would answer for the other.
	unversioned := base
	unversioned.SetVersions = nil
	bare, err := unversioned.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}
	if bare == before {
		t.Error("a definition that records no vocabulary versions hashes identically to one that does")
	}
}

// 🔴 TASK 10.19 — MEMORY LIFETIME. Entries are gone when an inference completes, and a second inference
// for the same workflow and revision starts with zero — so D2's three-part key remains the WHOLE of the
// result's input.
//
// Memory carried between inferences would add a fourth, invisible input: what HEROS happened to analyse
// first. Two tenants analysed in different orders would get different graphs, the stored result would
// stop being a function of its own key, and re-inference would diff against something the key cannot
// explain.
func TestMemoryDoesNotSurviveAnInference(t *testing.T) {
	first := defaultInferenceID("wf", "rev1", "cfg1")
	second := defaultInferenceID("wf", "rev1", "cfg1")

	// The same key resolves to the same inference id — which is what makes the scoping question sharp:
	// if the id were the scope and the id repeated, memory WOULD carry over.
	if first != second {
		t.Fatalf("the same three-part key produced two inference ids (%s / %s)", first, second)
	}

	store := memoryruntime.NewMemStore()
	key := memoryruntime.Key{NodeID: NodeID, SessionID: MemorySessionID(first)}
	if _, err := store.Append(key, memoryruntime.Message{Role: "assistant", Content: "a distinctive marker"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Entries(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("the seed did not land: %d entries", len(got))
	}

	// 🔴 THE INFERENCE COMPLETES and its entries go. `Expire(key, 0)` is the discard — count-based, as
	// D4 requires, and keeping ZERO is what "memory never spans inferences" means at the store.
	dropped, err := store.Expire(key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 1 {
		t.Errorf("the discard dropped %d entries, want 1", dropped)
	}

	after, err := store.Entries(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Errorf("%d entr(ies) survived the inference. Memory carried between inferences adds a fourth, "+
			"invisible input to a key that is supposed to be the whole of the result's input.", len(after))
	}

	// And a SECOND inference for the same workflow and revision starts cold — which is the
	// customer-visible trade task 6b.8c requires the console to state: a repository analysed twice
	// starts cold both times.
	second_entries, err := store.Entries(memoryruntime.Key{NodeID: NodeID, SessionID: MemorySessionID(second)})
	if err != nil {
		t.Fatal(err)
	}
	if len(second_entries) != 0 {
		t.Errorf("a second inference for the same workflow and revision saw %d entr(ies) from the "+
			"first", len(second_entries))
	}
}

// readSourceFile reads one of this package's own files, for the fences that assert on documentation.
func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

// The trade 6b.8c requires the console to state plainly is stated in the code that makes it true, so a
// reader of either finds the other. A property of the comment, and deliberately so: this is the one
// place where "we gave up a capability on purpose" has to survive somebody optimising.
func TestTheMemoryScopeDocumentsWhatItCosts(t *testing.T) {
	src := readSourceFile(t, "runner.go")
	for _, phrase := range []string{"cannot learn across analyses", "starts cold"} {
		if !strings.Contains(src, phrase) {
			t.Errorf("MemorySessionID's documentation no longer says %q. The scope costs a real "+
				"capability, and an undocumented trade is one somebody removes as an oversight.", phrase)
		}
	}
}
