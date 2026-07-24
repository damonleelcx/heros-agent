package optimizer

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/evalstats"
)

// fakeEnum enumerates diagnosis-guided candidates at a node from a fixed table, so the search's
// ordering property is tested without the full P5.5 operator catalog.
type fakeEnum struct{ byNode map[string][]SearchCandidate }

func (f fakeEnum) Enumerate(t Target) []SearchCandidate { return f.byNode[t.Node] }

// fakeBlind returns blind candidates over the wider space.
type fakeBlind struct{ cands []SearchCandidate }

func (f fakeBlind) Expand(string, float64) []SearchCandidate { return f.cands }

// Section 1.2/1.3/1.5: diagnosis-guided candidates at the attributed node+dimension are enumerated
// first, before any blind candidate, and every candidate records its motivating diagnosis.
func TestSearch_DiagnosisGuidedBeforeBlind(t *testing.T) {
	enum := fakeEnum{byNode: map[string][]SearchCandidate{
		"node3": {
			{ConfigHash: "cand-a", Node: "node3", Dimension: "model", Operator: "model_upgrade", ExpectedGain: 0.3},
			{ConfigHash: "cand-b", Node: "node3", Dimension: "model", Operator: "enable_extended_thinking", ExpectedGain: 0.2},
		},
	}}
	blind := fakeBlind{cands: []SearchCandidate{
		{ConfigHash: "blind-x", Node: "node9", Dimension: "prompt", Operator: "grid"},
	}}
	s := Search{Enum: enum, Blind: blind}
	targets := []Target{{DiagnosisID: "d3", Node: "node3", Dimension: "model", Priority: 1.0}}

	// Not exhausted → only diagnosis-guided candidates, no blind (task 1.2).
	guided := s.NextCandidates(targets, "cur", Policy{TargetedExhausted: false, BudgetRemaining: 100, BlindBudgetRemaining: 100})
	if len(guided) != 2 {
		t.Fatalf("expected 2 diagnosis-guided candidates, got %d", len(guided))
	}
	for _, c := range guided {
		if c.Source != SourceDiagnosisGuided {
			t.Errorf("candidate %s source = %s, want diagnosis_guided", c.ConfigHash, c.Source)
		}
		if c.DiagnosisID != "d3" { // task 1.3: motivating diagnosis recorded on every candidate
			t.Errorf("candidate %s diagnosis = %q, want d3", c.ConfigHash, c.DiagnosisID)
		}
		if c.Node != "node3" {
			t.Errorf("candidate %s at node %s, want the attributed node3", c.ConfigHash, c.Node)
		}
	}

	// Exhausted → guided still first, blind appended after (task 1.4 fallback ordering).
	all := s.NextCandidates(targets, "cur", Policy{TargetedExhausted: true, BudgetRemaining: 100, BlindBudgetRemaining: 100})
	firstBlind := -1
	lastGuided := -1
	for i, c := range all {
		if c.Source == SourceBlind && firstBlind < 0 {
			firstBlind = i
		}
		if c.Source == SourceDiagnosisGuided {
			lastGuided = i
		}
	}
	if firstBlind < 0 {
		t.Fatal("expected a blind candidate once targeted set is exhausted")
	}
	if firstBlind < lastGuided {
		t.Errorf("blind candidate at %d precedes a guided candidate at %d — diagnosis-guided must come first", firstBlind, lastGuided)
	}
}

// Section 1.4: the blind fallback fires ONLY when targeted are exhausted AND both the run budget and
// the separate blind sub-budget remain.
func TestSearch_BlindFallbackTrigger(t *testing.T) {
	enum := fakeEnum{byNode: map[string][]SearchCandidate{}}
	blind := fakeBlind{cands: []SearchCandidate{{ConfigHash: "blind-x", Node: "n", Operator: "grid"}}}
	s := Search{Enum: enum, Blind: blind}
	targets := []Target{{DiagnosisID: "d", Node: "n0"}}

	cases := []struct {
		name   string
		policy Policy
		want   int
	}{
		{"not exhausted → no blind", Policy{TargetedExhausted: false, BudgetRemaining: 10, BlindBudgetRemaining: 10}, 0},
		{"exhausted but no blind budget → no blind", Policy{TargetedExhausted: true, BudgetRemaining: 10, BlindBudgetRemaining: 0}, 0},
		{"exhausted but no run budget → no blind", Policy{TargetedExhausted: true, BudgetRemaining: 0, BlindBudgetRemaining: 10}, 0},
		{"exhausted + both budgets → blind fires", Policy{TargetedExhausted: true, BudgetRemaining: 10, BlindBudgetRemaining: 10}, 1},
	}
	for _, tc := range cases {
		got := s.NextCandidates(targets, "cur", tc.policy)
		if len(got) != tc.want {
			t.Errorf("%s: got %d candidates, want %d", tc.name, len(got), tc.want)
		}
	}
}

// Section 1.1: the objective is the composite score and the gates are hard constraints — a
// higher-scoring gate-failing candidate is NOT selected; a lower-scoring gate-passing one is preferred.
func TestObjective_GateFailingHighScorerNotPreferred(t *testing.T) {
	metrics := []CandidateMetrics{
		{ConfigHash: "high-but-illegal", Composite: evalstats.Interval{Mean: 0.92, Low: 0.88}},
		{ConfigHash: "lower-but-legal", Composite: evalstats.Interval{Mean: 0.61, Low: 0.55}},
	}
	gates := []GateVerdict{
		{Passed: false, Failed: []string{GateProviderAllowlist}}, // the high scorer violates a gate
		{Passed: true},
	}
	best := PreferByComposite(metrics, gates)
	if best != 1 {
		t.Fatalf("PreferByComposite chose index %d (%s); a gate-failing high scorer must never be preferred over a gate-passing candidate", best, metrics[best].ConfigHash)
	}
}

// Section 1.1: EvaluateGates disqualifies on each hard constraint, never as a soft penalty.
func TestEvaluateGates(t *testing.T) {
	cons := Constraints{ProviderAllowlist: []string{"anthropic"}, MinQuality: 0.5, LatencySLAMs: 1000}

	pass := EvaluateGates(cons, CandidateMetrics{Providers: []string{"anthropic"}, Quality: 0.9, LatencyMS: 500}, 0)
	if !pass.Passed {
		t.Errorf("expected a compliant candidate to pass, failed: %v", pass.Failed)
	}

	badProvider := EvaluateGates(cons, CandidateMetrics{Providers: []string{"openai"}, Quality: 0.9, LatencyMS: 500}, 0)
	if badProvider.Passed || !contains(badProvider.Failed, GateProviderAllowlist) {
		t.Errorf("expected provider-allowlist failure, got %+v", badProvider)
	}

	lowQuality := EvaluateGates(cons, CandidateMetrics{Providers: []string{"anthropic"}, Quality: 0.3, LatencyMS: 500}, 0)
	if lowQuality.Passed || !contains(lowQuality.Failed, GateMinQuality) {
		t.Errorf("expected min-quality failure, got %+v", lowQuality)
	}

	slowLatency := EvaluateGates(cons, CandidateMetrics{Providers: []string{"anthropic"}, Quality: 0.9, LatencyMS: 5000}, 0)
	if slowLatency.Passed || !contains(slowLatency.Failed, GateLatencySLA) {
		t.Errorf("expected latency-SLA failure, got %+v", slowLatency)
	}

	pricey := EvaluateGates(cons, CandidateMetrics{Providers: []string{"anthropic"}, Quality: 0.9, LatencyMS: 500, CostUSD: 0.5}, 0.1)
	if pricey.Passed || !contains(pricey.Failed, GateMaxCostPerRun) {
		t.Errorf("expected max-cost-per-run failure, got %+v", pricey)
	}
}

// ── small test helpers ──

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
