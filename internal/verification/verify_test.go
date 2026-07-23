package verification

import (
	"context"
	"testing"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/evalstats"
)

// fakeRunner returns deterministic per-case success/cost/latency for a config, so the gate's behaviour
// can be proven against known deltas without a live provider. Success is constant across seeds for a
// case (the fixture controls the delta, not seed noise); cost/latency are per-config scalars.
type fakeRunner struct {
	success map[string]map[string]float64 // configHash -> caseID -> success (0..1)
	cost    map[string]float64            // configHash -> $/run
	latency map[string]float64            // configHash -> ms/run
}

func (f fakeRunner) Run(_ context.Context, req RunRequest) (RunResult, error) {
	sc := f.success[req.ConfigHash]
	var q, c, l evalstats.Series
	q.Metric, c.Metric, l.Metric = metricQuality, metricCost, metricLatency
	cost := f.cost[req.ConfigHash]
	lat := f.latency[req.ConfigHash]
	for _, id := range req.CaseIDs {
		v := sc[id]
		for _, seed := range req.Seeds {
			q.Obs = append(q.Obs, evalstats.Observation{CaseID: id, Seed: seed, Value: v})
			c.Obs = append(c.Obs, evalstats.Observation{CaseID: id, Seed: seed, Value: cost})
			l.Obs = append(l.Obs, evalstats.Observation{CaseID: id, Seed: seed, Value: lat})
		}
	}
	return RunResult{Quality: q, Cost: c, Latency: l}, nil
}

func succMap(ids []string, v float64) map[string]float64 {
	m := map[string]float64{}
	for _, id := range ids {
		m[id] = v
	}
	return m
}

const (
	baseCfg = "basebaseb"
	candCfg = "candcandc"
)

var heldOutCases = []string{"h1", "h2", "h3", "h4", "h5", "h6"}
var genCases = []string{"g1", "g2", "g3"}
var evalSet = append(append([]string(nil), genCases...), heldOutCases...)

func baseProposal() Proposal {
	return Proposal{
		ProposalID: "p1", CandidateConfigHash: candCfg, BaselineConfigHash: baseCfg,
		SourceRevision: "rev1", DiffHash: "diffhash",
		GeneratingCaseIDs: genCases,
	}
}

// §4.6: a NOISE proposal (true-zero held-out delta) is a tie and does NOT surface.
func TestVerify_NoiseWithheld(t *testing.T) {
	r := fakeRunner{
		success: map[string]map[string]float64{
			baseCfg: succMap(evalSet, 0.5),
			candCfg: succMap(evalSet, 0.5), // identical → tie
		},
		cost: map[string]float64{baseCfg: 0.01, candCfg: 0.01}, latency: map[string]float64{baseCfg: 500, candCfg: 500},
	}
	v, err := Verify(context.Background(), r, baseProposal(), evalSet, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if v.GateResult != GateFailSig {
		t.Fatalf("noise must fail the significance gate, got %s (%s)", v.GateResult, v.Reason)
	}
	if v.Passed() {
		t.Fatal("a noise proposal must not surface")
	}
	if len(Recommendations([]Verdict{v})) != 0 {
		t.Fatal("noise leaked into the recommendation surface")
	}
}

// §4.6 / §4.7: an OVERFIT proposal wins on its generating cases but ties on held-out. Verification
// runs on held-out, so the surfaced delta is the held-out tie and the proposal is withheld.
func TestVerify_OverfitWithheld(t *testing.T) {
	// candidate: big win on generating cases, identical to baseline on held-out.
	candSucc := succMap(heldOutCases, 0.4) // == baseline on held-out
	for _, g := range genCases {
		candSucc[g] = 1.0 // memorized generating cases
	}
	r := fakeRunner{
		success: map[string]map[string]float64{
			baseCfg: succMap(evalSet, 0.4),
			candCfg: candSucc,
		},
		cost: map[string]float64{baseCfg: 0.01, candCfg: 0.01}, latency: map[string]float64{baseCfg: 500, candCfg: 500},
	}
	v, err := Verify(context.Background(), r, baseProposal(), evalSet, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !v.HeldOut {
		t.Fatal("the verdict must be measured on the held-out split")
	}
	if v.GateResult != GateFailSig {
		t.Fatalf("an overfit proposal must fail the significance gate on held-out, got %s", v.GateResult)
	}
	if v.Delta.Mean > 0.05 {
		t.Errorf("the surfaced delta must be the held-out (tie) delta, got %.3f", v.Delta.Mean)
	}
}

// §4.7: a known-good proposal passes and its surfaced delta is the HELD-OUT delta.
func TestVerify_KnownGoodPassesOnHeldOut(t *testing.T) {
	r := fakeRunner{
		success: map[string]map[string]float64{
			baseCfg: succMap(evalSet, 0.3),
			candCfg: succMap(evalSet, 0.95), // real, generalizing gain
		},
		cost: map[string]float64{baseCfg: 0.01, candCfg: 0.011}, latency: map[string]float64{baseCfg: 500, candCfg: 520},
	}
	v, err := Verify(context.Background(), r, baseProposal(), evalSet, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if v.GateResult != GatePass {
		t.Fatalf("a real gain must pass, got %s (%s)", v.GateResult, v.Reason)
	}
	if !v.HeldOut {
		t.Fatal("delta must be the held-out delta")
	}
	if v.Delta.Mean < 0.5 {
		t.Errorf("held-out delta should reflect the real gain, got %.3f", v.Delta.Mean)
	}
	if len(v.CasesFixed) != len(heldOutCases) {
		t.Errorf("all held-out cases went failing→passing, want %d fixed, got %d", len(heldOutCases), len(v.CasesFixed))
	}
}

// §4.7: with no held-out split (every eval case generated the diagnosis) the verdict is flagged
// not-held-out but still runs the gates.
func TestVerify_NoHeldOutSplitFlagged(t *testing.T) {
	r := fakeRunner{
		success: map[string]map[string]float64{baseCfg: succMap(evalSet, 0.3), candCfg: succMap(evalSet, 0.9)},
		cost:    map[string]float64{baseCfg: 0.01, candCfg: 0.01}, latency: map[string]float64{baseCfg: 500, candCfg: 500},
	}
	p := baseProposal()
	p.GeneratingCaseIDs = evalSet // no complement
	v, err := Verify(context.Background(), r, p, evalSet, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if v.HeldOut {
		t.Fatal("verdict must be flagged NOT held-out when no split exists")
	}
	if v.GateResult != GatePass {
		t.Fatalf("a real full-set gain still passes (flagged not-held-out), got %s", v.GateResult)
	}
}

// §4.8: "fixed accuracy, tripled cost" fails the regression check on the hard cost budget; the verdict
// records the cost impact.
func TestVerify_CostRegressionFails(t *testing.T) {
	r := fakeRunner{
		success: map[string]map[string]float64{baseCfg: succMap(evalSet, 0.3), candCfg: succMap(evalSet, 0.95)},
		cost:    map[string]float64{baseCfg: 0.01, candCfg: 0.03}, // tripled
		latency: map[string]float64{baseCfg: 500, candCfg: 500},
	}
	cfg := DefaultConfig()
	cfg.Budget.MaxCostUSD = 0.02 // ceiling below the tripled cost
	v, err := Verify(context.Background(), r, baseProposal(), evalSet, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if v.GateResult != GateFailRegress {
		t.Fatalf("tripled cost must fail the regression check, got %s (%s)", v.GateResult, v.Reason)
	}
	if v.CostDelta <= 0 {
		t.Errorf("verdict must record the (positive) cost impact, got %.4f", v.CostDelta)
	}
	if v.Passed() {
		t.Fatal("a cost-regressing proposal must not surface")
	}
}

// §4.9: "fixed cluster A, broke cluster B" fails; the verdict lists cases broken alongside cases fixed.
func TestVerify_ClusterRegressionFails(t *testing.T) {
	clusterA := attribution.FailureCluster{ClusterID: "A", Label: "cluster-A", MemberCaseIDs: heldOutCases}
	clusterB := attribution.FailureCluster{ClusterID: "B", Label: "cluster-B", MemberCaseIDs: []string{"b1", "b2", "b3", "b4"}}

	baseSucc := succMap(evalSet, 0.3)
	for _, b := range clusterB.MemberCaseIDs {
		baseSucc[b] = 0.9 // cluster B was passing
	}
	candSucc := succMap(evalSet, 0.95) // fixes cluster A (held-out)
	for _, b := range clusterB.MemberCaseIDs {
		candSucc[b] = 0.1 // cluster B now broken
	}
	r := fakeRunner{
		success: map[string]map[string]float64{baseCfg: baseSucc, candCfg: candSucc},
		cost:    map[string]float64{baseCfg: 0.01, candCfg: 0.01}, latency: map[string]float64{baseCfg: 500, candCfg: 500},
	}
	p := baseProposal()
	p.Clusters = []attribution.FailureCluster{clusterA, clusterB}
	p.TargetClusterID = "A"

	v, err := Verify(context.Background(), r, p, evalSet, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if v.GateResult != GateFailRegress {
		t.Fatalf("breaking cluster B must fail the regression check, got %s (%s)", v.GateResult, v.Reason)
	}
	if len(v.CasesFixed) == 0 {
		t.Error("verdict must still list the cases fixed in cluster A")
	}
	if len(v.CasesBroken) == 0 {
		t.Error("verdict must list the cases broken in cluster B")
	}
}

// §4.4: a passing verdict carries the diff, delta+CI, cost/latency impact, and cases fixed/broken.
func TestVerify_VerdictContents(t *testing.T) {
	r := fakeRunner{
		success: map[string]map[string]float64{baseCfg: succMap(evalSet, 0.3), candCfg: succMap(evalSet, 0.9)},
		cost:    map[string]float64{baseCfg: 0.01, candCfg: 0.012}, latency: map[string]float64{baseCfg: 500, candCfg: 540},
	}
	v, err := Verify(context.Background(), r, baseProposal(), evalSet, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if v.DiffHash != "diffhash" {
		t.Error("verdict is missing the proposed change reference (diff hash)")
	}
	if v.Delta.Low == 0 && v.Delta.High == 0 {
		t.Error("verdict is missing the delta confidence interval")
	}
	if v.CostDelta == 0 || v.LatencyDelta == 0 {
		t.Errorf("verdict is missing cost/latency impact: cost=%.4f latency=%.1f", v.CostDelta, v.LatencyDelta)
	}
	if v.CasesFixed == nil {
		t.Error("verdict must carry cases fixed (even if empty, non-nil)")
	}
}

// §4.5: only gate-passing verdicts reach the recommendation surface.
func TestRecommendations_OnlyPassSurfaces(t *testing.T) {
	pass := Verdict{ProposalID: "ok", GateResult: GatePass}
	fail := Verdict{ProposalID: "bad", GateResult: GateFailSig}
	unrun := Verdict{ProposalID: "u", GateResult: GateUnrun}
	got := Recommendations([]Verdict{pass, fail, unrun})
	if len(got) != 1 || got[0].ProposalID != "ok" {
		t.Fatalf("only the gate-passing verdict may surface, got %+v", got)
	}
	if len(Withheld([]Verdict{pass, fail, unrun})) != 2 {
		t.Fatal("withheld must include the fail and the unrun verdicts")
	}
}
