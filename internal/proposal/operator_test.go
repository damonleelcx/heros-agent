package proposal

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

const (
	refWeakModel   = "1111111111111111111111111111111111111111111111111111111111111111"
	refStrongModel = "2222222222222222222222222222222222222222222222222222222222222222"
	refCheapModel  = "3333333333333333333333333333333333333333333333333333333333333333"
	refThinkModel  = "4444444444444444444444444444444444444444444444444444444444444444"
	refRerank      = "5555555555555555555555555555555555555555555555555555555555555555"
	refTool        = "6666666666666666666666666666666666666666666666666666666666666666"
)

func testMenu() Menu {
	return Menu{
		Models: []ModelChoice{
			{Ref: refWeakModel, Provider: "anthropic", ModelID: "haiku", Tier: 1, CostPerRun: 0.001},
			{Ref: refStrongModel, Provider: "anthropic", ModelID: "opus", Tier: 3, CostPerRun: 0.02},
			{Ref: refCheapModel, Provider: "anthropic", ModelID: "haiku-mini", Tier: 0, CostPerRun: 0.0005},
			{Ref: refThinkModel, Provider: "anthropic", ModelID: "opus-thinking", Tier: 3, CostPerRun: 0.03, Thinking: true},
		},
		Skills: []SkillChoice{
			{Ref: refRerank, Name: "cohere-rerank", Kind: skillKindRerank},
			{Ref: refTool, Name: "sql-tool", Kind: skillKindTool},
		},
	}
}

func baseSpec() *variantspec.VariantSpec {
	return &variantspec.VariantSpec{
		WorkflowID:     "wf1",
		SourceRevision: "rev1",
		Order:          []string{"router", "rag", "answer"},
		Nodes: map[string]variantspec.NodeOverride{
			"rag": {ModelRef: refWeakModel},
		},
		Edges: []variantspec.Edge{
			{FromNodeID: "router", ToNodeID: "rag", Kind: "control"},
			{FromNodeID: "rag", ToNodeID: "answer", Kind: "data"},
		},
	}
}

func diag(node string, code diagnosis.TaxonomyCode, cases ...string) diagnosis.Diagnosis {
	return diagnosis.Diagnosis{
		DiagID: "diag-" + node, NodeID: node, TaxonomyCode: code, Confidence: 0.8,
		EvidenceCaseIDs: cases, Source: diagnosis.SourceRule,
	}
}

// §1.5 / §7.2: `add rerank` is emitted on a Retrieval (RAG) node and NOT on a Routing node.
func TestAddRerank_GatedToRAGNode(t *testing.T) {
	// Direct proof of the operator's own pattern gate, independent of the taxonomy.
	if admissibleForPattern(addRerankOp{}, patternclassifier.Routing) {
		t.Fatal("add-rerank must be inadmissible for a Routing node")
	}
	if !admissibleForPattern(addRerankOp{}, patternclassifier.RetrievalRAG) {
		t.Fatal("add-rerank must be admissible for a Retrieval (RAG) node")
	}

	e := Engine{Menu: testMenu(), Base: baseSpec()}
	em := e.Propose([]Target{
		{Diagnosis: diag("rag", diagnosis.CauseRetrievalMiss, "c1", "c2"), Pattern: patternclassifier.RetrievalRAG},
		{Diagnosis: diag("router", diagnosis.CauseRetrievalMiss, "c1"), Pattern: patternclassifier.Routing},
	})

	if !hasCandidate(em.Candidates, OpAddRerank, "rag") {
		t.Error("add-rerank was not emitted on the RAG node")
	}
	if hasCandidate(em.Candidates, OpAddRerank, "router") {
		t.Error("add-rerank was wrongly emitted on the Routing node")
	}
}

// A weak-model diagnosis emits a model-upgrade candidate that differs from the baseline ONLY in the
// node's model_ref, and references the upgraded model by registry ID (never inline).
func TestModelUpgrade_ChangesOnlyModelRefByID(t *testing.T) {
	e := Engine{Menu: testMenu(), Base: baseSpec()}
	em := e.Propose([]Target{
		{Diagnosis: diag("rag", diagnosis.CauseModelCapabilityGap, "c1"), Pattern: patternclassifier.RetrievalRAG},
	})
	c := findCandidate(t, em.Candidates, OpModelUpgrade, "rag")

	got := c.Spec.Nodes["rag"]
	if got.ModelRef != refStrongModel {
		t.Errorf("candidate must upgrade to the stronger model by ID, got %q", got.ModelRef)
	}
	// Nothing but the model changed at the node.
	if got.PromptRef != "" || len(got.SkillRefs) != 0 || got.ContextPolicy != "" {
		t.Errorf("upgrade changed a dimension other than model: %+v", got)
	}
	// The baseline is untouched (no aliasing).
	if e.Base.Nodes["rag"].ModelRef != refWeakModel {
		t.Error("the baseline spec was mutated by the operator")
	}
	// Every other node is byte-identical to the baseline.
	if len(c.Spec.Nodes) != len(e.Base.Nodes) {
		t.Errorf("upgrade added/removed a node override")
	}
	// The ref is a 64-hex registry version_id, not inline config.
	if len(got.ModelRef) != 64 {
		t.Errorf("model_ref must be a registry version_id, got %q", got.ModelRef)
	}
}

// A cost bottleneck (a Signal, not a taxonomy code) emits a model-downgrade candidate.
func TestModelDowngrade_FromCostSignal(t *testing.T) {
	e := Engine{Menu: testMenu(), Base: baseSpec()}
	em := e.Propose([]Target{
		{Diagnosis: diagnosis.Diagnosis{NodeID: "rag", EvidenceCaseIDs: []string{"c1"}},
			Signal: SignalCostBottleneck, Pattern: patternclassifier.RetrievalRAG},
	})
	c := findCandidate(t, em.Candidates, OpModelDowngrade, "rag")
	if c.Spec.Nodes["rag"].ModelRef != refCheapModel {
		t.Errorf("downgrade must pick the cheaper model, got %q", c.Spec.Nodes["rag"].ModelRef)
	}
}

// A reasoning-node capability gap can enable extended thinking (a thinking-budget model).
func TestEnableThinking_OnReasoningNode(t *testing.T) {
	e := Engine{Menu: testMenu(), Base: baseSpec()}
	em := e.Propose([]Target{
		{Diagnosis: diag("answer", diagnosis.CauseModelCapabilityGap, "c1"), Pattern: patternclassifier.Reflection},
	})
	c := findCandidate(t, em.Candidates, OpEnableThinking, "answer")
	if c.Spec.Nodes["answer"].ModelRef != refThinkModel {
		t.Errorf("enable-thinking must select a thinking-budget model, got %q", c.Spec.Nodes["answer"].ModelRef)
	}
}

// §1.5 / §7.2: a candidate that would violate the typed I/O contract is NOT emitted; the engine
// records the violation instead of surfacing a broken Variant Spec.
func TestContractViolatingCandidate_NotEmitted(t *testing.T) {
	// A→B data edge: A produces `summary`, B requires it. Reordering B before A makes the data unavailable.
	ir := &discovery.IR{IRVersion: discovery.IRVersion}
	ir.Nodes = append(ir.Nodes,
		discovery.IRNode{NodeID: "A", Kind: "static_definition", IOContract: discovery.IRIOContract{
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: objSchema(map[string]any{"summary": strField()}),
		}},
		discovery.IRNode{NodeID: "B", Kind: "static_definition", IOContract: discovery.IRIOContract{
			InputSchema:  objSchema(map[string]any{"summary": strField()}, "summary"),
			OutputSchema: map[string]any{"type": "object"},
		}},
	)
	base := &variantspec.VariantSpec{
		WorkflowID: "wf1", SourceRevision: "rev1",
		Order: []string{"A", "B"},
		Nodes: map[string]variantspec.NodeOverride{},
		Edges: []variantspec.Edge{{FromNodeID: "A", ToNodeID: "B", Kind: "data"}},
	}
	e := Engine{Menu: testMenu(), Base: base, IR: ir, Gate: NewTypedContractGate(ir)}

	// Diagnose B as lost-in-middle → reorder tries to move B before A → contract violation.
	em := e.Propose([]Target{
		{Diagnosis: diag("B", diagnosis.CauseLostInMiddle, "c1"), Pattern: patternclassifier.PromptChaining},
	})
	if hasCandidate(em.Candidates, OpReorder, "B") {
		t.Error("a contract-violating reorder was surfaced as a candidate")
	}
	if !hasRefusal(em.Refusals, OpReorder) {
		t.Errorf("the contract violation was not recorded as a refusal: %+v", em.Refusals)
	}
}

// §7.2: prompt rewrite is grounded in the attached failing cases and traceable to them; an ungrounded
// rewrite is not emitted.
func TestPromptRewrite_GroundedAndTraceable(t *testing.T) {
	e := Engine{Menu: testMenu(), Base: baseSpec(), Optimizer: SelfRefineOptimizer{}}
	em := e.Propose([]Target{
		{
			Diagnosis:      diag("answer", diagnosis.CausePromptFormatDrift, "c1", "c2", "c3"),
			Pattern:        patternclassifier.PromptChaining,
			BasePromptBody: "Answer the question.",
			RequiredFields: []string{"label", "rationale"},
			Groundings: []FailingCaseGrounding{
				{CaseID: "c1", FailureReason: "missing field label", TraceRef: strings.Repeat("a", 64)},
				{CaseID: "c2", FailureReason: "missing field rationale", TraceRef: strings.Repeat("b", 64)},
				{CaseID: "c3", FailureReason: "missing field label", TraceRef: strings.Repeat("c", 64)},
			},
		},
	})
	c := findCandidate(t, em.Candidates, OpPromptRewrite, "answer")
	if c.Grounding == nil {
		t.Fatal("prompt-rewrite candidate carries no grounding bundle")
	}
	for _, id := range []string{"c1", "c2", "c3"} {
		if !c.Grounding.GroundedIn(id) {
			t.Errorf("rewrite is not traceable to attached case %s", id)
		}
	}
	if c.Grounding.Hash == "" || len(c.Grounding.Hash) != 64 {
		t.Errorf("grounding bundle is not content-hashed: %q", c.Grounding.Hash)
	}
	if !strings.Contains(c.NewPromptBody, "label") || !strings.Contains(c.NewPromptBody, "rationale") {
		t.Errorf("format constraint did not pin the violated fields:\n%s", c.NewPromptBody)
	}

	// An ungrounded rewrite (no attached cases) is refused, not emitted.
	em2 := Engine{Menu: testMenu(), Base: baseSpec(), Optimizer: SelfRefineOptimizer{}}.Propose([]Target{
		{Diagnosis: diag("answer", diagnosis.CausePromptFormatDrift), Pattern: patternclassifier.PromptChaining,
			BasePromptBody: "Answer.", Groundings: nil},
	})
	if hasCandidate(em2.Candidates, OpPromptRewrite, "answer") {
		t.Error("an ungrounded prompt rewrite was emitted")
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

func hasCandidate(cs []Candidate, op OperatorKind, node string) bool {
	for _, c := range cs {
		if c.Operator == op && c.NodeID == node {
			return true
		}
	}
	return false
}

func findCandidate(t *testing.T, cs []Candidate, op OperatorKind, node string) Candidate {
	t.Helper()
	for _, c := range cs {
		if c.Operator == op && c.NodeID == node {
			return c
		}
	}
	t.Fatalf("no %s candidate on node %s", op, node)
	return Candidate{}
}

func hasRefusal(rs []Refusal, op OperatorKind) bool {
	for _, r := range rs {
		if r.Operator == op {
			return true
		}
	}
	return false
}

func objSchema(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	reqAny := make([]any, len(required))
	for i, r := range required {
		reqAny[i] = r
	}
	return map[string]any{"type": "object", "properties": props, "required": reqAny}
}

func strField() map[string]any { return map[string]any{"type": "string"} }
