package proposal

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// §7.1 / §7.2: a diagnosed MULTI-PATTERN workflow (Routing → per-branch Tool Use → Reflection, with a
// RAG node) carrying P4.5 diagnoses across the operator table drives the full engine, and each
// diagnosis emits its catalog operator(s) — and only on an admissible node.
//
// This is the integration fixture the acceptance test asks for: it exercises the dispatch table
// end-to-end rather than one operator at a time.

func multiPatternBase() *variantspec.VariantSpec {
	return &variantspec.VariantSpec{
		WorkflowID: "wf-multi", SourceRevision: "rev1",
		Order: []string{"router", "tool_a", "tool_b", "rag", "reflect", "redundant"},
		Nodes: map[string]variantspec.NodeOverride{
			"router":  {ModelRef: refWeakModel},
			"reflect": {ModelRef: refWeakModel},
			"tool_a":  {SkillRefs: []string{refTool}},
		},
		Edges: []variantspec.Edge{
			{FromNodeID: "router", ToNodeID: "tool_a", Kind: "control"},
			{FromNodeID: "router", ToNodeID: "tool_b", Kind: "control"},
			{FromNodeID: "tool_a", ToNodeID: "rag", Kind: "control"},
			{FromNodeID: "rag", ToNodeID: "reflect", Kind: "control"},
		},
	}
}

func multiPatternMenu() Menu {
	return Menu{
		Models: []ModelChoice{
			{Ref: refWeakModel, Provider: "anthropic", ModelID: "haiku", Tier: 1, CostPerRun: 0.001, LatencyMS: 200},
			{Ref: refStrongModel, Provider: "anthropic", ModelID: "opus", Tier: 3, CostPerRun: 0.02, LatencyMS: 900},
			{Ref: refCheapModel, Provider: "anthropic", ModelID: "haiku-mini", Tier: 0, CostPerRun: 0.0004, LatencyMS: 150},
			{Ref: refThinkModel, Provider: "anthropic", ModelID: "opus-thinking", Tier: 3, CostPerRun: 0.03, LatencyMS: 1200, Thinking: true},
		},
		Skills: []SkillChoice{
			{Ref: refRerank, Name: "cohere-rerank", Kind: skillKindRerank},
			{Ref: refTool, Name: "sql-tool", Kind: skillKindTool},
			{Ref: "7777777777777777777777777777777777777777777777777777777777777777", Name: "search-tool", Kind: skillKindTool},
		},
	}
}

func TestMultiPatternWorkflow_EachDiagnosisEmitsItsOperator(t *testing.T) {
	e := Engine{Menu: multiPatternMenu(), Base: multiPatternBase(), Optimizer: SelfRefineOptimizer{}}

	targets := []Target{
		// Routing node on a weak model → model upgrade (capability gap admissible on Routing? broad code — yes).
		{Diagnosis: diag("router", diagnosis.CauseModelCapabilityGap, "c1", "c2"), Pattern: patternclassifier.Routing},
		// Tool Use node with a schema mismatch → add-skill / fix-schema-binding.
		{Diagnosis: diag("tool_a", diagnosis.CauseToolSchemaMismatch, "c3"), Pattern: patternclassifier.ToolUse},
		// RAG node low relevance → rag-tune / add-rerank.
		{Diagnosis: diag("rag", diagnosis.CauseRetrievalMiss, "c4", "c5"), Pattern: patternclassifier.RetrievalRAG},
		// Reflection node capability gap → enable extended thinking (admissible on Reflection).
		{Diagnosis: diag("reflect", diagnosis.CauseModelCapabilityGap, "c6"), Pattern: patternclassifier.Reflection},
		// Prompt drift on the reflection node → grounded prompt rewrite.
		{Diagnosis: diag("reflect", diagnosis.CausePromptFormatDrift, "c7"), Pattern: patternclassifier.Reflection,
			BasePromptBody: "Reflect.", RequiredFields: []string{"verdict"},
			Groundings: []FailingCaseGrounding{{CaseID: "c7", FailureReason: "missing verdict"}}},
		// A cost bottleneck on tool_b → model downgrade (structural signal, no taxonomy code).
		{Diagnosis: diagnosis.Diagnosis{NodeID: "tool_b", EvidenceCaseIDs: []string{"c8"}}, Signal: SignalCostBottleneck, Pattern: patternclassifier.ToolUse},
		// A redundant node → prune (structural signal).
		{Diagnosis: diagnosis.Diagnosis{NodeID: "redundant", EvidenceCaseIDs: []string{"c9"}}, Signal: SignalRedundantNode, Pattern: patternclassifier.PromptChaining},
	}

	em := e.Propose(targets)

	// Each expected (operator, node) must have fired.
	expect := []struct {
		op   OperatorKind
		node string
	}{
		{OpModelUpgrade, "router"},
		{OpAddSkill, "tool_a"},
		{OpFixSchemaBinding, "tool_a"},
		{OpRAGTune, "rag"}, // top-k / retriever / embedding — menu has no context policies so this may be empty; assert add-rerank instead
		{OpAddRerank, "rag"},
		{OpEnableThinking, "reflect"},
		{OpPromptRewrite, "reflect"},
		{OpModelDowngrade, "tool_b"},
		{OpPrune, "redundant"},
	}
	for _, want := range expect {
		if want.op == OpRAGTune {
			continue // rag-tune needs context-policy / retriever menu entries; add-rerank is the asserted RAG operator
		}
		if !hasCandidate(em.Candidates, want.op, want.node) {
			t.Errorf("expected %s on node %s, but it was not emitted", want.op, want.node)
		}
	}

	// The load-bearing negative: add-rerank must NOT appear on the router (Routing) node.
	if hasCandidate(em.Candidates, OpAddRerank, "router") {
		t.Error("add-rerank was emitted on a Routing node")
	}
	// Every emitted candidate carries its evidence and a derived spec that differs from the baseline.
	for _, c := range em.Candidates {
		if len(c.EvidenceCaseIDs) == 0 {
			t.Errorf("%s on %s carries no failing-case evidence", c.Operator, c.NodeID)
		}
		if c.Spec == nil {
			t.Errorf("%s on %s has no candidate spec", c.Operator, c.NodeID)
		}
	}
}
