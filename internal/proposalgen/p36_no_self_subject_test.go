package proposalgen

import (
	"context"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/herosagent"
)

// 🚫 P36 D5 / decisions.md D-36.8 — NO PROPOSAL TARGETS THE AGENT'S OWN DEFINITION (task 9.14).
//
// This is the fence for a decision rather than for a bug, and it is here because the decision is the
// obvious next request. An agent that proposes changes to itself is an evaluator grading its own
// configuration; the circularity is not fixed by adding a gate, because whatever gates it is running
// on the configuration being judged.
func TestNoProposalTargetsTheAgentsOwnDefinition(t *testing.T) {
	ctx := context.Background()

	// 🔴 A Generator with NO stores wired. That is the point: if the refusal fired anywhere later than
	// first, this call would nil-panic on a store read — so the test proves the ORDER, not just the
	// outcome. A refusal that ran after the reads would have already loaded the platform's own graph
	// into the path whose whole purpose is to propose changes to what it loaded.
	g := &Generator{}

	for _, id := range []string{"heros", "HEROS", "  heros  ", "Heros"} {
		res, err := g.Generate(ctx, "acme", id)
		if err != nil {
			t.Fatalf("%q: the refusal did not fire first — the pass reached a store: %v", id, err)
		}
		if res.State != StateSelfSubject {
			t.Errorf("%q produced state %q, want %q. A workflow id that walks past this refusal is a "+
				"refusal that is not there — and the id arrives from an HTTP path and a CLI argument.",
				id, res.State, StateSelfSubject)
		}
		if len(res.ProposalIDs) != 0 {
			t.Errorf("%q produced %d proposal(s) against the platform's own agent", id, len(res.ProposalIDs))
		}
		for _, want := range []string{"evaluator", "gate", "operator"} {
			if !strings.Contains(strings.ToLower(res.Detail), want) {
				t.Errorf("%q: the refusal does not mention %q, so a reader has to re-derive the argument "+
					"instead of finding it: %s", id, want, res.Detail)
			}
		}
	}

	// 🔴 ANTI-VACUITY, both directions.
	//
	// A customer workflow must NOT be refused — a fence that refused everything would pass the loop
	// above while breaking the product entirely.
	//
	// 🔴 Asserted on the PREDICATE rather than through `Generate`, because `Generate` on a customer
	// workflow proceeds to read the stores and this Generator deliberately has none. Going through it
	// would prove only that a nil store panics.
	for _, id := range []string{"checkout-agent", "heros-checkout", "my-heros-clone", "", "herosagent"} {
		if isPlatformAgentWorkflow(id) {
			t.Errorf("%q was matched as the platform's own agent. The fence is matching too much, which "+
				"passes the loop above and refuses customers' proposals.", id)
		}
	}
}

// 🔴 The literal this package guards and the id the agent actually publishes under CANNOT DRIFT.
//
// `platformAgentWorkflowID` is a string constant here rather than an import, so the only thing keeping
// it true is this assertion. Without it the two can diverge silently and the refusal stops matching the
// thing it refuses — which looks exactly like a working fence.
func TestTheGuardedWorkflowIDIsTheOneTheAgentPublishesUnder(t *testing.T) {
	spec := herosagent.SingleNode(herosagent.Node{NodeID: herosagent.DefaultNodeID}).Spec()
	if spec.WorkflowID != platformAgentWorkflowID {
		t.Errorf("this package refuses proposals for workflow_id %q and the agent's own spec declares "+
			"%q. The refusal no longer matches the thing it refuses, and nothing else would notice.",
			platformAgentWorkflowID, spec.WorkflowID)
	}
}
