package proposal

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P14 §6 — tool pruning and minimization as catalog operators.

func toolTarget(node string, usage ToolUsage) Target {
	return Target{
		Diagnosis: diagnosis.Diagnosis{NodeID: node, EvidenceCaseIDs: []string{"c1", "c2"}},
		Signal:    SignalUnusedTools,
		Pattern:   patternclassifier.ToolUse,
		Usage:     usage,
	}
}

func toolEngine() Engine {
	return Engine{Menu: multiPatternMenu(), Base: multiPatternBase()}
}

// ── 6.3 tool-prune ───────────────────────────────────────────────────────────────────────────────

func TestToolPruneOperatorEmitsCandidate(t *testing.T) {
	e := toolEngine()
	em := e.Propose([]Target{toolTarget("tool_a", ToolUsage{
		Discovered: []string{"weatherTool", "sqlTool", "searchTool"},
		Exercised:  []string{"weatherTool"},
	})})

	// One candidate PER unused tool — the smaller blast radius, and the only shape whose verdict is
	// attributable to a specific drop.
	var kept [][]string
	for _, c := range em.Candidates {
		if c.Operator != OpToolPrune {
			continue
		}
		kept = append(kept, c.Spec.Nodes["tool_a"].ToolSelection)
		if len(c.Dimensions) != 1 || c.Dimensions[0] != "tools" {
			t.Errorf("a prune must declare the tools dimension, got %v", c.Dimensions)
		}
	}
	if len(kept) != 2 {
		t.Fatalf("want one prune candidate per unused tool (2), got %d: %v", len(kept), kept)
	}
	for _, k := range kept {
		if len(k) != 2 {
			t.Errorf("each prune drops exactly one tool, got kept=%v", k)
		}
		if !containsString(k, "weatherTool") {
			t.Errorf("a prune dropped the tool the eval set DOES exercise: kept=%v", k)
		}
	}

	c := findCandidate(t, em.Candidates, OpToolPrune, "tool_a")
	if !strings.Contains(c.Rationale, "never calls it") {
		t.Errorf("the rationale must state the evidence, got %q", c.Rationale)
	}
}

// 🔴 Grounded or silent, the same rule the skill removal follows: never prune a tool set nobody looked
// at, and never propose dropping a tool the eval set actually calls.
func TestToolPruneDeclinesWithoutEvidence(t *testing.T) {
	e := toolEngine()

	t.Run("no usage recorded", func(t *testing.T) {
		em := e.Propose([]Target{toolTarget("tool_a", ToolUsage{})})
		if hasCandidate(em.Candidates, OpToolPrune, "tool_a") {
			t.Error("a tool was proposed for pruning with no usage evidence")
		}
	})

	t.Run("every declared tool is exercised", func(t *testing.T) {
		em := e.Propose([]Target{toolTarget("tool_a", ToolUsage{
			Discovered: []string{"weatherTool"}, Exercised: []string{"weatherTool"}})})
		if hasCandidate(em.Candidates, OpToolPrune, "tool_a") {
			t.Error("a fully-exercised tool set produced a prune candidate")
		}
		if hasCandidate(em.Candidates, OpToolMinimize, "tool_a") {
			t.Error("a fully-exercised tool set produced a minimization candidate; there is nothing to minimize")
		}
	})
}

// ── 6.3 tool-minimize ────────────────────────────────────────────────────────────────────────────

func TestToolMinimizeEmitsMinimalSet(t *testing.T) {
	e := toolEngine()
	em := e.Propose([]Target{toolTarget("tool_a", ToolUsage{
		Discovered: []string{"weatherTool", "sqlTool", "searchTool"},
		Exercised:  []string{"weatherTool", "searchTool"},
	})})

	c := findCandidate(t, em.Candidates, OpToolMinimize, "tool_a")
	got := c.Spec.Nodes["tool_a"].ToolSelection
	if len(got) != 2 || !containsString(got, "weatherTool") || !containsString(got, "searchTool") {
		t.Fatalf("the minimal set must be exactly the exercised tools, got %v", got)
	}
	if containsString(got, "sqlTool") {
		t.Error("the minimal set kept a tool the eval set never exercised")
	}
	if !strings.Contains(c.Rationale, "scored against the full set") {
		t.Errorf("the rationale must say the minimal set is a hypothesis to be measured, got %q", c.Rationale)
	}

	// The whole-set bet and the per-tool bets are emitted TOGETHER, not instead of each other: they are
	// different hypotheses and the harness scores all of them against the same baseline.
	if !hasCandidate(em.Candidates, OpToolPrune, "tool_a") {
		t.Error("minimization suppressed the individual prunes; they are separate bets and both should be measured")
	}
}

// 🔴 An EMPTY minimal set is never emitted. "The eval set exercised no tool" is at least as likely to
// mean the eval set does not cover this node as it is to mean the node needs no tools, and unbinding
// every tool on that reading is the asymmetric mistake.
func TestToolMinimizeNeverProposesTheEmptySet(t *testing.T) {
	e := toolEngine()
	em := e.Propose([]Target{toolTarget("tool_a", ToolUsage{
		Discovered: []string{"weatherTool", "sqlTool"},
		Exercised:  nil,
	})})
	for _, c := range em.Candidates {
		if c.Operator == OpToolMinimize {
			t.Fatalf("an empty minimal set was proposed (kept=%v); the eval set exercising nothing is not "+
				"evidence that the node needs nothing", c.Spec.Nodes["tool_a"].ToolSelection)
		}
	}
	// The incremental version is still offered — one prune per unused tool.
	if !hasCandidate(em.Candidates, OpToolPrune, "tool_a") {
		t.Error("with no tool exercised, the per-tool prunes should still be on the table")
	}
}

// A tool selection lands ONLY in the tools dimension. A prune that also touched skills would be two
// changes under one rationale, and D-14.1 exists precisely so those two cannot be confused.
func TestToolPruneTouchesOnlyTheToolsDimension(t *testing.T) {
	e := toolEngine()
	em := e.Propose([]Target{toolTarget("tool_a", ToolUsage{
		Discovered: []string{"weatherTool", "sqlTool"}, Exercised: []string{"weatherTool"}})})
	c := findCandidate(t, em.Candidates, OpToolPrune, "tool_a")
	base := multiPatternBase().Nodes["tool_a"]
	got := c.Spec.Nodes["tool_a"]
	if !equalStrings(got.SkillRefs, base.SkillRefs) {
		t.Errorf("a tool prune changed the node's bound skills: %v -> %v", base.SkillRefs, got.SkillRefs)
	}
	if got.ModelRef != base.ModelRef || got.PromptRef != base.PromptRef || got.ContextPolicy != base.ContextPolicy {
		t.Error("a tool prune touched a dimension other than tools")
	}
}

// ── 6.4 🚫 no new metric ─────────────────────────────────────────────────────────────────────────

// TestPrunedSetScoredByExistingMetrics is a negative test, and negative tests about "we did not add a
// thing" are worth writing down precisely because nothing else would notice.
//
// The saving from a prune is fewer declared-tool tokens and, where a pruned tool was erroring, a lower
// tool-error rate — both already in the harness's standard family. A bespoke "tools_pruned" metric would
// measure the CHANGE rather than its EFFECT, and a change that measures itself always looks like a win.
func TestPrunedSetScoredByExistingMetrics(t *testing.T) {
	// The two metrics P14 claims carry the win are the ones already there.
	for _, want := range []string{evalharness.MetricRunTokens, evalharness.MetricToolErrorRate} {
		if !containsString(evalharness.StandardFamily, want) {
			t.Fatalf("%s is not in the harness's standard family; P14's saving has nowhere to surface", want)
		}
	}
	if evalharness.MetricRunTokens != "eval_tokens_total" || evalharness.MetricToolErrorRate != "tool_error_rate" {
		t.Error("the metric names P14's spec cites have drifted from the harness constants")
	}

	// 🚫 And nothing tool- or skill-shaped was added alongside them.
	for _, m := range append(append([]string(nil), evalharness.StandardFamily...), evalharness.ContributionFamily...) {
		lower := strings.ToLower(m)
		for _, banned := range []string{"prune", "minimi", "tools_removed", "skill_bound", "tool_count"} {
			if strings.Contains(lower, banned) {
				t.Errorf("the metric family gained %q, which measures the change rather than its effect; "+
					"P14 ships no new metric (design Decision 6)", m)
			}
		}
	}

	// The standard family is exactly six metrics; a seventh would be an eval change P14 promised not to
	// make. Asserting the COUNT is what makes the promise checkable rather than aspirational.
	if len(evalharness.StandardFamily) != 6 {
		t.Errorf("the standard metric family has %d members: %v — P14 adds none",
			len(evalharness.StandardFamily), evalharness.StandardFamily)
	}
}

// A pruned candidate resolves and hashes through the ordinary path: the harness consumes only
// config_hash and the trace, so a prune needs no dimension-specific scoring code.
func TestPrunedCandidateIsAnOrdinaryConfig(t *testing.T) {
	e := toolEngine()
	em := e.Propose([]Target{toolTarget("tool_a", ToolUsage{
		Discovered: []string{"weatherTool", "sqlTool"}, Exercised: []string{"weatherTool"}})})
	c := findCandidate(t, em.Candidates, OpToolPrune, "tool_a")

	// It is a plain Variant Spec: the same type every other operator emits, valid on its own terms.
	if err := c.Spec.Validate(); err != nil {
		t.Fatalf("a pruned candidate must be a structurally valid Variant Spec: %v", err)
	}
	var _ *variantspec.VariantSpec = c.Spec
	if VerifyOrderHint(OpToolPrune) >= VerifyOrderHint(OpPromptRewrite) {
		t.Error("a single tool deletion should verify before an expensive prompt sweep")
	}
}
