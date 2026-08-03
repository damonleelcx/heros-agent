package hostedscorecard

import (
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/scorecard"
)

// hostedscorecard_test.go pins the one thing this card must never do: show per-node FAILURE columns it
// did not compute. Every NodeRow it emits has FailureShare 0, and a reader must be told that zero means
// "not investigated" rather than "not to blame" — those lead to opposite next steps.

func linked() linkingest.LinkedRun {
	return linkingest.LinkedRun{
		RunID: "run-1", TenantID: "t1", WorkflowID: "wf", ConfigHash: "aaaa000000001111",
		LinkedAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		Scores: []runlink.Score{
			{Metric: "quality", Value: 0.75},
			{Metric: "cost_usd", Value: 0.02},
			{Metric: "latency_ms", Value: 900},
		},
		Eval: runlink.EvalSummary{CaseCount: 8, SeedCount: 5, GateOutcome: runlink.GatePass},
		PerNode: map[string]runlink.NodeMetric{
			"n_cheap": {CostUSD: 1, LatencyMS: 100},
			"n_dear":  {CostUSD: 3, LatencyMS: 300},
		},
	}
}

// TestFailureAttributionIsReportedUnavailable is the central refusal.
func TestFailureAttributionIsReportedUnavailable(t *testing.T) {
	v := Build(linked())
	if v.FailureAttribution != scorecard.FailureUnavailable {
		t.Fatalf("failure_attribution = %q, want %q — every NodeRow here has FailureShare 0, and without "+
			"this field that reads as 'no node caused any failures'",
			v.FailureAttribution, scorecard.FailureUnavailable)
	}
	if v.Message == "" {
		t.Error("the card does not say in words why failure attribution is missing")
	}
}

// TestNoFailureDerivativesAreInvented: clusters, diagnoses and ablations are all built on per-node
// correctness. None of it crossed, so none of them may appear.
func TestNoFailureDerivativesAreInvented(t *testing.T) {
	v := Build(linked())
	if len(v.Clusters) != 0 {
		t.Errorf("card carries %d failure cluster(s) computed from correctness data it never received", len(v.Clusters))
	}
	if len(v.Diagnoses) != 0 {
		t.Errorf("card carries %d diagnosis card(s) with no attribution behind them", len(v.Diagnoses))
	}
	if len(v.Ablations) != 0 {
		t.Errorf("card carries %d ablation(s); no ablation ran", len(v.Ablations))
	}
	for _, n := range v.Nodes {
		if n.FirstDivergenceCount != 0 {
			t.Errorf("node %s reports a first-divergence count from data that does not cross", n.NodeID)
		}
	}
}

// TestNFailingIsNotDerivedFromQuality guards a specific plausible invention. quality here is the mean
// FRACTION OF NODES answering correctly per run, not a per-case pass rate, so round((1-q)*NCases) is a
// number multiplied by an unrelated number — and it would appear on the one card whose whole subject is
// failure.
func TestNFailingIsNotDerivedFromQuality(t *testing.T) {
	v := Build(linked())
	if v.Overall.NFailing != 0 {
		t.Fatalf("n_failing = %d — it was derived rather than reported; quality is a per-node fraction, "+
			"so (1-quality)*n_cases counts nothing that exists", v.Overall.NFailing)
	}
	if v.Overall.NCases != 8 {
		t.Errorf("n_cases = %d, want the reported 8", v.Overall.NCases)
	}
}

// TestCostSharesAreRealAndSumToOne: this is what migration 0023 actually bought. An aggregate cannot say
// WHICH node is expensive; these shares can.
func TestCostSharesAreRealAndSumToOne(t *testing.T) {
	v := Build(linked())
	if len(v.Nodes) != 2 {
		t.Fatalf("want 2 node rows, got %d", len(v.Nodes))
	}
	if v.Nodes[0].NodeID != "n_dear" {
		t.Errorf("rows are not ordered most-expensive-first: %s came first", v.Nodes[0].NodeID)
	}
	var total float64
	for _, n := range v.Nodes {
		total += n.MeanCostShare
	}
	if total < 0.999 || total > 1.001 {
		t.Errorf("cost shares sum to %v, want 1 — a share of an unrelated denominator explains nothing", total)
	}
	if got := v.Nodes[0].MeanCostShare; got != 0.75 {
		t.Errorf("n_dear cost share = %v, want 0.75 (3 of 4)", got)
	}
}

// TestRunWithoutPerNodeIsEmptyNotReady: a card with no rows and a "ready" badge reads as "we looked and
// found nothing".
func TestRunWithoutPerNodeIsEmptyNotReady(t *testing.T) {
	lr := linked()
	lr.PerNode = nil
	v := Build(lr)
	if v.State != scorecard.StateEmpty {
		t.Fatalf("state = %q, want empty when no per-node metrics crossed", v.State)
	}
	if !strings.Contains(v.Message, "heros eval") {
		t.Errorf("the message does not name the remedy: %q", v.Message)
	}
}

// TestUnclassifiedNodesAreCounted: "not classified" must not be mistaken for "nothing wrong".
func TestUnclassifiedNodesAreCounted(t *testing.T) {
	v := Build(linked())
	if v.UnclassifiedNodeCount != 2 || v.ClassifiedNodeCount != 0 {
		t.Fatalf("classified=%d unclassified=%d, want 0/2 — this assembler reads no pattern labels",
			v.ClassifiedNodeCount, v.UnclassifiedNodeCount)
	}
}

// TestScorecardIsScopedToTheTenant. A variant id IS a config hash, so two tenants running the same
// configuration produce the same id — a guaranteed collision, not an unlucky one.
func TestScorecardIsScopedToTheTenant(t *testing.T) {
	store := &stubStore{runs: map[string][]linkingest.LinkedRun{"tenant-a": {linked()}}}
	src := NewSource(store)

	if _, ok := src.Scorecard("tenant-a", "aaaa000000001111"); !ok {
		t.Fatal("tenant-a cannot read its own scorecard")
	}
	if _, ok := src.Scorecard("tenant-b", "aaaa000000001111"); ok {
		t.Fatal("tenant-b was served tenant-a's scorecard for an identical config hash")
	}
}

func TestNewestRunWinsMatchingTheBoard(t *testing.T) {
	older := linked()
	older.RunID = "run-old"
	older.LinkedAt = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	older.PerNode = map[string]runlink.NodeMetric{"n_only": {CostUSD: 9}}

	store := &stubStore{runs: map[string][]linkingest.LinkedRun{"t1": {older, linked()}}}
	v, ok := NewSource(store).Scorecard("t1", "aaaa000000001111")
	if !ok {
		t.Fatal("no scorecard")
	}
	if len(v.Nodes) != 2 {
		t.Fatalf("the older run was used; a board row and its scorecard must describe the SAME measurement "+
			"(got %d node rows, want the newest run's 2)", len(v.Nodes))
	}
}

type stubStore struct {
	runs map[string][]linkingest.LinkedRun
}

func (s *stubStore) ForWorkflow(tenantID, _ string) ([]linkingest.LinkedRun, error) {
	return s.runs[tenantID], nil
}

func (s *stubStore) LinkedRunIDs(tenantID string) ([]string, error) {
	var out []string
	for _, lr := range s.runs[tenantID] {
		out = append(out, lr.RunID)
	}
	return out, nil
}

func (s *stubStore) Get(tenantID, runID string) (linkingest.LinkedRun, bool, error) {
	for _, lr := range s.runs[tenantID] {
		if lr.RunID == runID {
			return lr, true, nil
		}
	}
	return linkingest.LinkedRun{}, false, nil
}
