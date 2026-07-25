package studio

import (
	"context"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

// M4.4 — the echo completer is deterministic (same input → same output/tokens).
func TestEchoCompleter_IsDeterministic(t *testing.T) {
	ec := EchoCompleter{}
	entry := &registry.ModelEntry{Spec: registry.ModelSpec{Provider: "openai", ModelID: "gpt-x"}}
	req := providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "hello world"}}}
	first, err := ec.Complete(context.Background(), entry, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		got, _ := ec.Complete(context.Background(), entry, req, nil)
		if got.Content != first.Content || got.Usage != first.Usage {
			t.Fatal("echo completer is not deterministic")
		}
	}
	if first.Usage.InputTokens == 0 || first.Usage.OutputTokens == 0 {
		t.Fatal("echo completer must report nonzero token counts")
	}
	if first.ModelID != "gpt-x" {
		t.Fatalf("echo must echo the model id, got %q", first.ModelID)
	}
}

// M4.4 — a run through the echo completer meters studio spend under the studio kind.
func TestEchoRun_MetersStudioSpend(t *testing.T) {
	meter := NewSpendMeter(Cap{})
	runner := NewRunner(EchoCompleter{}, meter, FlatPricer(1.0), func() time.Time { return time.Unix(0, 0) })
	entry := &registry.ModelEntry{Spec: registry.ModelSpec{Provider: "openai", ModelID: "gpt-x"}}
	caller := Caller{TenantID: "t", UserID: "u"}
	req := providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "a longer prompt to price"}}}

	res, err := runner.Run(context.Background(), caller, entry, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output == "" || res.CostUSD <= 0 || res.InputTokens == 0 {
		t.Fatalf("run must return output/cost/tokens: %+v", res)
	}
	rep := meter.Report(caller)
	if rep.ByKind[SpendStudio] != res.CostUSD {
		t.Fatalf("studio spend must be recorded under the studio kind: %+v", rep.ByKind)
	}
}

// M3.4 / M6.3 — one bound cell per column: binding a second cell replaces the first.
func TestBindStore_OneBoundCellPerNode(t *testing.T) {
	bs := NewBindStore()
	bs.Bind("t", "wf", Binding{NodeID: "n1", ModelID: "openai/gpt-x", PromptVersionID: "p1"})
	bs.Bind("t", "wf", Binding{NodeID: "n1", ModelID: "anthropic/claude", PromptVersionID: "p2"}) // replaces
	bs.Bind("t", "wf", Binding{NodeID: "n2", ModelID: "openai/gpt-x", PromptVersionID: "p3"})

	got := bs.Bindings("t", "wf")
	if len(got) != 2 {
		t.Fatalf("expected 2 nodes bound, got %d", len(got))
	}
	if got["n1"].ModelID != "anthropic/claude" || got["n1"].PromptVersionID != "p2" {
		t.Fatalf("the second bind must replace the first for n1, got %+v", got["n1"])
	}
	if got["n1"].Verified {
		t.Fatal("a studio bind must never be verified")
	}
}

func TestBindStore_ScopedByTenantAndWorkflow(t *testing.T) {
	bs := NewBindStore()
	bs.Bind("t1", "wf", Binding{NodeID: "n1", ModelID: "m1"})
	if len(bs.Bindings("t2", "wf")) != 0 {
		t.Fatal("bindings must be scoped by tenant")
	}
	if len(bs.Bindings("t1", "other")) != 0 {
		t.Fatal("bindings must be scoped by workflow")
	}
}

// M3.2 — workflow node catalog returns columns from a loaded IR.
func TestWorkflowCatalog_NodesFromIR(t *testing.T) {
	c := NewWorkflowCatalog()
	if _, ok := c.Nodes("missing"); ok {
		t.Fatal("an unloaded workflow must report not-found, distinct from zero nodes")
	}
	ir := &discovery.IR{Nodes: []discovery.IRNode{
		{NodeID: "n_b", CallSite: discovery.IRCallSite{Symbol: "B", File: "b.py"}, Model: discovery.IRModel{Provider: "openai", ModelID: "gpt-x"}},
		{NodeID: "n_a", CallSite: discovery.IRCallSite{Symbol: "A", File: "a.py"}, Model: discovery.IRModel{Provider: "anthropic", ModelID: "claude"}},
	}}
	c.Load("wf", ir)
	nodes, ok := c.Nodes("wf")
	if !ok || len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d ok=%v", len(nodes), ok)
	}
	if nodes[0].NodeID != "n_a" {
		t.Fatalf("nodes must be sorted by id, got %s first", nodes[0].NodeID)
	}
	if nodes[0].PromptName != "node/n_a" {
		t.Fatalf("node-scoped prompt name wrong: %q", nodes[0].PromptName)
	}
	if nodes[0].DiscoveredModel != "anthropic/claude" {
		t.Fatalf("discovered model wrong: %q", nodes[0].DiscoveredModel)
	}
}
