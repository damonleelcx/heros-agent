package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/conversation"
	"github.com/heros-foreal/agentd/internal/herosagent"
)

// p36_customer_projection_test.go is the fence for decisions.md D-36.2 (PRD §14 Q2): the producing
// NODE is operator-side only.
//
// # Why this needs a fence rather than a convention
//
// P36 stores per-node attribution on every edge and every inference, and the whole point of storing it
// is that surfaces render it. The operator console must. The customer console must not — a node id is
// meaningless to a customer and *actionable-looking*: `heros_critic` beside a finding invites "your
// critic is wrong", which is a conversation about our implementation instead of about their code.
//
// The failure is one line in a projection function, it is invisible in review because the field is
// already on the source struct, and once it has shipped it is a thing we cannot change without a
// change note.

// 🔴 A stored inference carrying node attribution projects to a customer pin that carries NONE of it.
func TestTheCustomerProjectionCarriesNoNodeAttribution(t *testing.T) {
	stored := herosagent.Stored{
		InferenceID: "inf-1", WorkflowID: "wf", SourceRevision: "rev-1",
		AgentConfigHash: "cfg-1",
		Narrative:       "the retriever and the ranker are not sharing a cache",
		Edges: []herosagent.ProvenancedEdge{
			{From: "a", To: "b", Kind: "data", Confidence: 0.9, ProducedByNode: "heros_critic"},
		},
		Nodes: []herosagent.NodeRun{
			{NodeID: "heros_triage", ProviderCalls: 1, TokensIn: 10, TokensOut: 5},
			{NodeID: "heros_critic", ProviderCalls: 1, TokensIn: 20, TokensOut: 8},
		},
	}

	pin := pinFrom(stored, "rev-1", conversation.IntentSpec{Intent: "cost"})
	b, err := json.Marshal(pin)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(b)
	for _, leaked := range []string{"heros_critic", "heros_triage", "produced_by_node"} {
		if strings.Contains(rendered, leaked) {
			t.Errorf("the customer-facing pin carries %q. The producing node is OPERATOR-SIDE ONLY "+
				"(decisions.md D-36.2): a customer sees the evidence, not our topology, and a node id "+
				"beside a finding invites a conversation about our implementation instead of their "+
				"code.\n  %s", leaked, rendered)
		}
	}
	// 🔴 ANTI-VACUITY. The projection must still carry the things it IS for — otherwise a pin that
	// rendered nothing at all would pass the check above.
	if !strings.Contains(rendered, "inf-1") || !strings.Contains(rendered, "retriever") {
		t.Errorf("the pin carries neither its evidence reference nor its claim, so the leak check above "+
			"passed over an empty projection: %s", rendered)
	}
}

// 🔴 And no customer-facing TYPE has a field a node id could travel in — the structural half, which
// holds for a projection nobody has written yet.
func TestNoCustomerFacingTypeHasANodeAttributionField(t *testing.T) {
	// Listed by VALUE so the compiler keeps it honest: a type renamed or removed fails to build here.
	types := []any{
		conversation.Pin{}, conversation.SurfaceReading{},
	}
	// Name COMPONENTS, not substrings. `NodeIDs` on a customer's own workflow graph is legitimate —
	// those are the CUSTOMER's nodes — so the pattern targets the attribution vocabulary this phase
	// introduced rather than the word "node".
	offending := []string{"producedbynode", "produced_by_node", "agentnode", "agent_node",
		"producingnode", "producing_node"}

	var scanned int
	var seen = map[reflect.Type]bool{}
	var walk func(rt reflect.Type, path string)
	walk = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Ptr || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			scanned++
			hay := strings.ToLower(f.Name + " " + f.Tag.Get("json"))
			for _, bad := range offending {
				if strings.Contains(hay, bad) {
					t.Errorf("%s.%s could carry the producing agent node to a customer. It is "+
						"operator-side only (decisions.md D-36.2).", path, f.Name)
				}
			}
			walk(f.Type, path+"."+f.Name)
		}
	}
	for _, v := range types {
		rt := reflect.TypeOf(v)
		walk(rt, rt.Name())
	}
	// The walk must have reached something, or its clean report means nothing.
	if scanned < 8 {
		t.Errorf("the walk inspected only %d field(s) — it is not reaching the types", scanned)
	}
	// And the pattern must be able to fire.
	if !strings.Contains(strings.ToLower("ProducedByNode"), offending[0]) {
		t.Error("the pattern does not match `ProducedByNode` — it reports clean because it cannot see")
	}
}
