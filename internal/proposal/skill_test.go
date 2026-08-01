package proposal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
	"github.com/heros-foreal/agentd/internal/verification"
)

// P14 §3 — the skill operators, and the gate that decides whether any of them ships.

// ── 3.1 remove-skill ─────────────────────────────────────────────────────────────────────────────

func TestRemoveSkillOperatorEmitsCandidate(t *testing.T) {
	e := Engine{Menu: multiPatternMenu(), Base: multiPatternBase()}

	// tool_a binds `sql-tool` (refTool). The recorded usage says it errored — the grounding a removal
	// needs.
	em := e.Propose([]Target{{
		Diagnosis: diag("tool_a", diagnosis.CauseToolSchemaMismatch, "c1", "c2"),
		Pattern:   patternclassifier.ToolUse,
		Usage:     ToolUsage{Discovered: []string{"sql-tool"}, Erroring: []string{"sql-tool"}},
	}})

	c := findCandidate(t, em.Candidates, OpRemoveSkill, "tool_a")
	if got := c.Spec.Nodes["tool_a"].SkillRefs; len(got) != 0 {
		t.Errorf("the removal did not unbind the skill; SkillRefs = %v", got)
	}
	if !strings.Contains(c.Rationale, "sql-tool") {
		t.Errorf("the rationale must name the skill being removed, got %q", c.Rationale)
	}
	if len(c.EvidenceCaseIDs) == 0 {
		t.Error("a removal must carry the failing cases it is grounded on")
	}
	// The baseline is never mutated — a candidate is a derivation, not an edit.
	if len(e.Base.Nodes["tool_a"].SkillRefs) != 1 {
		t.Error("the baseline spec was mutated by the removal operator")
	}
}

// A skill the eval set never called is removable on that evidence alone: it is pure declared-tool cost.
func TestRemoveSkillFiresOnNeverExercised(t *testing.T) {
	e := Engine{Menu: multiPatternMenu(), Base: multiPatternBase()}
	em := e.Propose([]Target{{
		Diagnosis: diag("tool_a", diagnosis.CauseToolSchemaMismatch, "c1"),
		Pattern:   patternclassifier.ToolUse,
		Usage:     ToolUsage{Discovered: []string{"sql-tool"}, Exercised: []string{}},
	}})
	c := findCandidate(t, em.Candidates, OpRemoveSkill, "tool_a")
	if !strings.Contains(c.Rationale, "never exercised") {
		t.Errorf("the rationale must say WHY the skill is removable, got %q", c.Rationale)
	}
}

// 🔴 Grounded or silent. Two negatives, and both matter more than the positive above: unbinding a
// capability the workflow needs is the asymmetric mistake, so the operator must decline when it has no
// evidence, and must decline for a skill the evidence says is working.
func TestRemoveSkillDeclinesWithoutEvidence(t *testing.T) {
	e := Engine{Menu: multiPatternMenu(), Base: multiPatternBase()}

	t.Run("no usage recorded at all", func(t *testing.T) {
		em := e.Propose([]Target{{
			Diagnosis: diag("tool_a", diagnosis.CauseToolSchemaMismatch, "c1"),
			Pattern:   patternclassifier.ToolUse,
		}})
		if hasCandidate(em.Candidates, OpRemoveSkill, "tool_a") {
			t.Error("a skill was proposed for removal with no usage evidence at all")
		}
	})

	t.Run("the skill was exercised and did not error", func(t *testing.T) {
		em := e.Propose([]Target{{
			Diagnosis: diag("tool_a", diagnosis.CauseToolSchemaMismatch, "c1"),
			Pattern:   patternclassifier.ToolUse,
			Usage:     ToolUsage{Discovered: []string{"sql-tool"}, Exercised: []string{"sql-tool"}},
		}})
		if hasCandidate(em.Candidates, OpRemoveSkill, "tool_a") {
			t.Error("a working, exercised skill was proposed for removal")
		}
	})
}

// remove-skill is inadmissible off a Tool Use node, exactly as add-rerank is off a RAG node.
func TestRemoveSkillInadmissibleOffToolUse(t *testing.T) {
	e := Engine{Menu: multiPatternMenu(), Base: multiPatternBase()}
	em := e.Propose([]Target{{
		Diagnosis: diag("router", diagnosis.CauseToolSchemaMismatch, "c1"),
		Pattern:   patternclassifier.Routing,
		Usage:     ToolUsage{Erroring: []string{"sql-tool"}},
	}})
	if hasCandidate(em.Candidates, OpRemoveSkill, "router") {
		t.Error("remove-skill fired on a Routing node")
	}
}

// A removal preserves the ORDER of the surviving skills. Order is identity-bearing, so a removal that
// also reordered would be two changes under one rationale and verification could not attribute the
// score to either.
func TestRemoveSkillPreservesSurvivingOrder(t *testing.T) {
	const refA, refB, refC = "aaaa", "bbbb", "cccc"
	spec := &variantspec.VariantSpec{
		SourceRevision: "rev1", Order: []string{"n"},
		Nodes: map[string]variantspec.NodeOverride{"n": {SkillRefs: []string{refA, refB, refC}}},
	}
	removeSkill(spec, "n", refB)
	got := spec.Nodes["n"].SkillRefs
	if len(got) != 2 || got[0] != refA || got[1] != refC {
		t.Fatalf("removal reordered the survivors: %v", got)
	}
}

// ── 3.2 every skill change is verification-gated ─────────────────────────────────────────────────

// stubRunner returns a fixed quality series per config_hash, so the GATE can be proven without a
// provider — the codebase's "the only stub is the provider" discipline.
type stubRunner struct{ quality map[string]float64 }

func (s stubRunner) Run(_ context.Context, req verification.RunRequest) (verification.RunResult, error) {
	rate, ok := s.quality[req.ConfigHash]
	if !ok {
		rate = 0.5
	}
	return verification.RunResult{
		Quality: successSeries(req.ConfigHash, req.CaseIDs, req.Seeds, rate),
		Cost:    successSeries(req.ConfigHash, req.CaseIDs, req.Seeds, 0.001),
		Latency: successSeries(req.ConfigHash, req.CaseIDs, req.Seeds, 100),
	}, nil
}

// TestSkillChangeShipsOnlyOnVerifiedNonRegression is task 3.2's assertion, and it is deliberately
// stated as a PAIR.
//
// A gate that only admitted improvements would be satisfied by a gate that admits nothing; a gate that
// only rejected regressions would be satisfied by one that admits everything it is shown. So the same
// materialized skill change is run twice against the same baseline — once measuring better, once
// measuring worse — and the verdict has to move with the MEASUREMENT, not with the operator's prior.
func TestSkillChangeShipsOnlyOnVerifiedNonRegression(t *testing.T) {
	const baseline = "cfg-baseline"
	cases := caseIDs(30)
	cfg := verification.DefaultConfig()

	verdictFor := func(t *testing.T, candHash string, candRate float64) verification.Verdict {
		t.Helper()
		runner := stubRunner{quality: map[string]float64{baseline: 0.60, candHash: candRate}}
		p := verification.Proposal{
			ProposalID: "p-skill", CandidateConfigHash: candHash, BaselineConfigHash: baseline,
			SourceRevision: "rev1", DiffHash: strings.Repeat("d", 64),
			GeneratingCaseIDs: cases[:5],
			Clusters:          []attribution.FailureCluster{{ClusterID: "cl-1", MemberCaseIDs: cases[:5]}},
			TargetClusterID:   "cl-1",
		}
		v, err := verification.Verify(context.Background(), runner, p, cases, cfg)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		return v
	}

	worse := verdictFor(t, "cfg-skill-regresses", 0.35)
	if worse.Passed() {
		t.Fatalf("a materialized skill change that REGRESSES the measured score must not ship; "+
			"gate_result=%q reason=%q", worse.GateResult, worse.Reason)
	}
	if worse.Reason == "" {
		t.Error("a withheld skill change must say why; a refusal with no reason reads as a broken tool")
	}

	better := verdictFor(t, "cfg-skill-improves", 0.90)
	if !better.Passed() {
		t.Fatalf("a materialized skill change that IMPROVES the measured score must be admitted; "+
			"gate_result=%q reason=%q", better.GateResult, better.Reason)
	}

	// The decision is the MEASURED verdict, not the diagnosis: the two runs share an operator, a node,
	// and a prior, and differ only in what the harness observed.
	if worse.GateResult == better.GateResult {
		t.Error("the gate returned the same result for a regressing and an improving change; the verdict " +
			"is not being driven by the measurement")
	}
}

// Every skill operator's candidate is emitted as a PROPOSAL — never pre-applied to the baseline and
// never marked shippable by the operator itself. The operator layer has no field that could say
// "ship this"; this asserts that stays true as the catalog grows.
func TestSkillCandidatesAreProposalsNotDecisions(t *testing.T) {
	e := Engine{Menu: multiPatternMenu(), Base: multiPatternBase()}
	em := e.Propose([]Target{
		{Diagnosis: diag("tool_a", diagnosis.CauseToolSchemaMismatch, "c1"), Pattern: patternclassifier.ToolUse,
			Usage: ToolUsage{Discovered: []string{"sql-tool"}, Erroring: []string{"sql-tool"}}},
		{Diagnosis: diag("rag", diagnosis.CauseRetrievalMiss, "c2"), Pattern: patternclassifier.RetrievalRAG},
	})
	if len(em.Candidates) == 0 {
		t.Fatal("no skill candidates were emitted")
	}
	baselineJSON, _ := json.Marshal(multiPatternBase())
	liveJSON, _ := json.Marshal(e.Base)
	if string(baselineJSON) != string(liveJSON) {
		t.Error("proposing mutated the baseline spec; a candidate must be a derivation, not an application")
	}
	for _, c := range em.Candidates {
		if c.ExpectedGain < 0 {
			t.Errorf("%s carries a negative pre-verification estimate", c.Operator)
		}
	}
}

// ── 3.3 the skill operators now produce an APPLICABLE diff ───────────────────────────────────────

// p14SkillEntry builds a sealed skill contract through the registry's own compiler, so the diff below
// is materialized from a schema the registry would actually have accepted.
func p14SkillEntry(t *testing.T, versionID, name string) *registry.SkillEntry {
	t.Helper()
	e, err := registry.NewSkillEntry(versionID, name, registry.SkillSpec{
		ImplHandle:   "builtin:" + name,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("NewSkillEntry: %v", err)
	}
	return e
}

// TestSkillCatalogProducesApplicableDiff closes the loop P14 exists to close.
//
// Before 14a, every one of these operators emitted a candidate that resolved, hashed, and scored — and
// then produced NO diff, because the codemod refused the skills dimension. This drives each operator's
// candidate through the real compiler (variantspec-shaped Resolved → transform.Generate → build gate)
// and asserts a reviewable diff comes out the other end.
func TestSkillCatalogProducesApplicableDiff(t *testing.T) {
	root := targetRoot(t)
	sites, err := discovery.IndexGoCallSites(root, nil)
	if err != nil {
		t.Fatalf("IndexGoCallSites: %v", err)
	}
	var agentID string
	for id, s := range sites {
		if strings.Contains(readSymbol(t, root, s), "agent") {
			agentID = id
		}
	}
	if agentID == "" {
		t.Fatal("fixture node `agent` not found")
	}

	// Each of the four skill-bearing operators lands in SkillRefs, so each resolves to the same shape of
	// override — a bound skill at the node. What is under test is that the shape is now APPLICABLE.
	for _, op := range []OperatorKind{OpAddSkill, OpAddRerank, OpFixSchemaBinding, OpRAGTune} {
		t.Run(string(op), func(t *testing.T) {
			resolved := &variantspec.Resolved{
				ConfigHash: strings.Repeat("c", 64), SourceRevision: "rev1", Language: "go",
				Overrides: map[string]variantspec.ResolvedOverride{
					agentID: {Skills: []*registry.SkillEntry{
						p14SkillEntry(t, strings.Repeat("5", 64), "search_kb")}},
				},
			}
			comp := Compiler{Resolver: fixedResolver{resolved}, Root: root, Build: okBuild{}}
			got, err := comp.Compile(context.Background(), Candidate{
				Operator: op, NodeID: agentID, Dimensions: []string{"skills"}, Spec: baseSpec()})
			if err != nil {
				t.Fatalf("%s no longer compiles to a diff: %v", op, err)
			}
			if got.Patch == nil || got.Patch.IsEmpty() {
				t.Fatalf("%s produced no diff; before P14 this operator was proposal-only, and the whole "+
					"point of 14a is that it is not any more", op)
			}
			if !strings.Contains(string(got.Patch.Diff), "search_kb") {
				t.Errorf("%s's diff does not bind the selected skill:\n%s", op, got.Patch.Diff)
			}
			if !got.Surfaceable() {
				t.Errorf("%s compiled but is not surfaceable (build status %q)", op, got.BuildStatus)
			}
		})
	}
}
