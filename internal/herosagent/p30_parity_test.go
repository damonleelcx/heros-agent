package herosagent

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/providercall"
)

// 🔴 TASK 7.6 — THE CI PARITY ASSERTION.
//
// # What would make this test worthless, and what stops it
//
// The lazy version of this test hands both placements the same fake model and asserts the edges match.
// It passes unconditionally: the fake returns a constant, so the assertion is that a constant equals
// itself, and it would keep passing after somebody wrote a second context assembler — which is the one
// failure D6 names.
//
// So the fake here answers FROM WHAT IT WAS SHOWN. It parses the assembled context, proposes an edge
// for each candidate pair, and its narrative names the host. Three consequences:
//
//   - if the two hosts assemble different context, they are shown different pairs and produce different
//     EDGE SETS, and the assertion fails on the thing that actually diverged;
//   - the narratives are different by construction, so a version of this test that quietly began
//     comparing them would fail — which is the only way to know it still asserts what it claims;
//   - the fixture is a real IR with a frontend edge in it, so the residue is a proper subset of the
//     pairs and the D3 fence is exercised on both hosts rather than assumed.

// residueEchoModel answers from the context it was given, so its output is a FUNCTION of the assembled
// input rather than a constant.
type residueEchoModel struct {
	host string
	seen []byte
}

func (m *residueEchoModel) Infer(_ context.Context, in Input) (RawResult, providercall.Usage, error) {
	assembled, err := AssembleModelInput(in)
	if err != nil {
		return RawResult{}, providercall.Usage{}, err
	}
	b, err := assembled.Bytes()
	if err != nil {
		return RawResult{}, providercall.Usage{}, err
	}
	m.seen = b

	// One proposed edge per candidate pair it was SHOWN. A host shown a different residue answers
	// differently, which is exactly what the parity assertion needs to be able to catch.
	out := RawResult{
		// 🔴 The narrative names the host. Task 7.6 says the assertion is on the edge set and NOT on the
		// narrative, and this is what keeps that claim checkable: prose that differs by construction.
		Narrative: "Assessed on the " + m.host + " host. This paragraph is deliberately not comparable.",
	}
	c := 0.9
	for _, p := range in.Residue.Pairs {
		out.Edges = append(out.Edges, RawEdge{From: p.From, To: p.To, Kind: "data", Confidence: &c})
	}
	return out, providercall.Usage{InputTokens: len(b), OutputTokens: 10}, nil
}

func TestBothPlacementsProduceTheSameEdgeSet(t *testing.T) {
	// A fixture with a real frontend edge, so the residue is a proper subset of all pairs and D3's fence
	// is exercised rather than trivially satisfied.
	ir := irWith([]string{"a", "b", "c", "d"}, [2]string{"a", "b"})
	in := Input{
		TenantID: "t1", WorkflowID: "parity-fixture", SourceRevision: "rev-1", RuleIR: ir,
		Residue: SelectResidue(ir, discovery.DiscoveryReport{}, nil),
		Budget:  DefaultBudget(),
	}
	const configHash = "cfg-parity"

	platformModel := &residueEchoModel{host: "platform"}
	customerModel := &residueEchoModel{host: "customer"}

	platform, err := NewRunner(platformModel, NewMemInferenceStore(), DefaultConfidenceFloor,
		func() int64 { return 1 })
	if err != nil {
		t.Fatal(err)
	}
	customer, err := NewCustomerRunner(customerModel, NewMemInferenceStore(), DefaultConfidenceFloor,
		func() int64 { return 1 })
	if err != nil {
		t.Fatal(err)
	}

	fromPlatform, err := platform.Infer(context.Background(), in, BindHash(configHash), PlacementPlatform)
	if err != nil {
		t.Fatal(err)
	}
	fromCustomer, err := customer.Infer(context.Background(), in, BindHash(configHash), PlacementCustomer)
	if err != nil {
		t.Fatal(err)
	}

	if len(fromPlatform.Edges) == 0 {
		t.Fatal("the platform host produced no edges, so an equality assertion would hold vacuously")
	}

	// 🔴 THE ASSERTION: equal EDGE SETS at one config_hash.
	if p, c := edgeSet(fromPlatform.Edges), edgeSet(fromCustomer.Edges); !equalSets(p, c) {
		t.Errorf("the two placements produced different edge sets at %s.\n  platform: %v\n  customer: %v\n\n"+
			"  D6: two runners with one prompt is the classic shape that produces train/serve skew, and "+
			"the divergence is invisible because both `work`.", configHash, p, c)
	}

	// 🚫 The narrative is NOT compared, and this asserts that it COULD NOT have been: the two differ, so
	// a future edit that folded prose into the comparison above would turn this test red.
	if fromPlatform.Narrative == fromCustomer.Narrative {
		t.Error("both hosts produced identical narrative, so this test can no longer demonstrate that " +
			"the parity assertion excludes it — asserting a model phrases a paragraph identically on two " +
			"machines would be a test that fails for the wrong reason")
	}

	// And the reason the edge sets agree is that the CONTEXT agreed. Asserted separately: two hosts
	// could coincidentally produce the same edges from different context on a small fixture, and then
	// the parity claim would be luck rather than structure.
	if string(platformModel.seen) != string(customerModel.seen) {
		t.Errorf("the edge sets matched but the assembled context did not:\n  platform: %s\n  customer: %s",
			platformModel.seen, customerModel.seen)
	}

	// Both stored inferences name their own host, so the graph attributes each correctly (task 8.6).
	if fromPlatform.InferenceID != fromCustomer.InferenceID {
		t.Errorf("one key produced two inference ids (%s / %s) — D2's key is (workflow, revision, "+
			"config_hash) and the HOST is not part of it, so the same analysis run on either side must "+
			"resolve to the same row", fromPlatform.InferenceID, fromCustomer.InferenceID)
	}
}

func edgeSet(edges []ProvenancedEdge) []string {
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		out = append(out, e.From+"→"+e.To+":"+e.Kind)
	}
	sort.Strings(out)
	return out
}

func equalSets(a, b []string) bool {
	return len(a) == len(b) && strings.Join(a, "|") == strings.Join(b, "|")
}
