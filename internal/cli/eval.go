package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// eval.go is the `eval` command: a multi-seed, scored evaluation that runs offline with the customer's
// own keys (PRD FR1/FR2, design Decision 8).
//
// 🚫 It does NOT implement a local scorer. The intervals come from evalstats.Aggregate — the SAME
// bootstrap the platform runs — so a local eval and a hosted run over the same inputs agree (parity,
// task 7.6). What is pluggable is only the NODE RUNTIME: the thing that actually calls a model. In
// production that runtime is provider-backed with the customer's keys (network to the PROVIDER, never
// the platform); the offline/free/air-gapped path uses a deterministic reference runtime so the command
// works with no provider and its determinism (NFR5) is testable. The statistics are identical either
// way — that is the whole point of not writing a second scorer.

// NodeRuntime executes one node for one {case, seed} and returns its measured cost/latency/tokens and a
// correctness signal. It is the ONE seam where a real provider call would happen; everything above it is
// the real harness math.
type NodeRuntime interface {
	// Name identifies the runtime in the output ("reference" | "provider:<name>") so a reader can tell a
	// deterministic offline run from a real provider run — a single-seed or simulated run must never be
	// presented as a hosted result.
	Name() string
	// RunNode measures one node call. model is "provider/model" from the IR.
	RunNode(nodeID, model, caseID string, seed int64) NodeMeasurement
}

// NodeMeasurement is one node call's measured quantities.
type NodeMeasurement struct {
	CostUSD   float64
	LatencyMS float64
	TokensIn  int64
	TokensOut int64
	Correct   bool
}

// Gates are the customer-configured quality gates. A gate failing is the customer's remedy (fix the
// regression) and fails the build with ExitGateFailed — distinct from the tool breaking.
type Gates struct {
	MinQuality    *float64
	MaxCostPerRun *float64
	LatencySLAMs  *float64
}

// EvalData is the machine payload for `eval`.
type EvalData struct {
	RunID      string          `json:"run_id"`
	WorkflowID string          `json:"workflow_id"`
	ConfigHash string          `json:"config_hash"`
	Runtime    string          `json:"runtime"`
	Seeds      []int64         `json:"seeds"`
	Cases      int             `json:"cases"`
	Scores     []runlink.Score `json:"scores"`
	Metrics    runlink.Metrics `json:"metrics"`
	StoredAt   string          `json:"stored_at"`
	SingleSeed bool            `json:"single_seed"`
}

// Eval runs a scored, multi-seed evaluation and writes an allowlist-shaped RunRecord to the local run
// store. rt is the node runtime (reference by default); gates are the customer's configured gates.
func Eval(cfg Config, s Streams, rt NodeRuntime, gates Gates) error {
	if rt == nil {
		rt = ReferenceRuntime{}
	}
	repo := cfg.Get("repo")
	if repo == "" {
		repo = "."
	}
	url, sha := repoIdentity(repo, cfg.Get("repo-url"), cfg.Get("commit"), s)

	s.Narratef("eval: discovering %s…", repo)
	res, err := discovery.Run(discovery.Options{
		Repo: repo, ConfigPath: cfg.Get("config"), WorkflowID: cfg.Get("workflow-id"),
		RepoURL: url, CommitSHA: sha,
	})
	if err != nil {
		return operational("discovery for eval failed", err)
	}
	ir := res.IR
	if len(ir.Nodes) == 0 {
		return operational("eval: discovery found no LLM nodes to evaluate", nil)
	}

	seeds := seedList(cfg.Int("seeds", 5))
	nCases := cfg.Int("cases", 8)
	cases := deterministicCases(ir.Workflow.ID, nCases)
	configHash := evalConfigHash(ir.Workflow.ID, sha, seeds, cases)

	s.Narratef("eval: %d nodes × %d cases × %d seeds via runtime %q…", len(ir.Nodes), len(cases), len(seeds), rt.Name())

	// One Series per metric, accumulated across every {node, case, seed}. quality is 0/1 correctness per
	// run; cost_usd and latency_ms are per-run sums.
	quality := evalstats.Series{VariantID: ir.Workflow.ID, ConfigHash: configHash, Metric: "quality"}
	costS := evalstats.Series{VariantID: ir.Workflow.ID, ConfigHash: configHash, Metric: "cost_usd"}
	latS := evalstats.Series{VariantID: ir.Workflow.ID, ConfigHash: configHash, Metric: "latency_ms"}

	var totCost, totLat float64
	var totIn, totOut int64
	nRuns := 0
	perNode := map[string]*runlink.NodeMetric{}

	for _, seed := range seeds {
		for _, caseID := range cases {
			var runCost, runLat float64
			correctNodes := 0
			for _, node := range ir.Nodes {
				model := modelRef(node)
				m := rt.RunNode(node.NodeID, model, caseID, seed)
				runCost += m.CostUSD
				runLat += m.LatencyMS
				totIn += m.TokensIn
				totOut += m.TokensOut
				if m.Correct {
					correctNodes++
				}
				nm := perNode[node.NodeID]
				if nm == nil {
					nm = &runlink.NodeMetric{}
					perNode[node.NodeID] = nm
				}
				nm.CostUSD += m.CostUSD
				nm.LatencyMS += m.LatencyMS
				nm.TokensIn += m.TokensIn
				nm.TokensOut += m.TokensOut
			}
			// Quality is the FRACTION of nodes that answered correctly this run — a continuous 0..1 that
			// stays meaningful on a large graph, rather than an all-or-nothing that collapses to 0 the
			// moment any one of forty nodes misses.
			q := 1.0
			if len(ir.Nodes) > 0 {
				q = float64(correctNodes) / float64(len(ir.Nodes))
			}
			quality.Obs = append(quality.Obs, evalstats.Observation{CaseID: caseID, Seed: seed, Value: q})
			costS.Obs = append(costS.Obs, evalstats.Observation{CaseID: caseID, Seed: seed, Value: runCost})
			latS.Obs = append(latS.Obs, evalstats.Observation{CaseID: caseID, Seed: seed, Value: runLat})
			totCost += runCost
			totLat += runLat
			nRuns++
		}
	}

	statCfg := evalstats.DefaultConfig()
	// A single-seed run is honestly labelled rather than presented as a result (AI-engineer lens). The
	// bootstrap still runs; the label carries the caveat.
	singleSeed := len(seeds) < statCfg.MinSeeds
	statCfg.MinSeeds = 1 // permit the CLI's short local loop; the label, not a silent floor, carries the caveat

	scores, err := aggregateScores(statCfg, quality, costS, latS)
	if err != nil {
		return operational("eval statistics", err)
	}

	// Aggregate metrics are per-run means (so "cost" is the cost of ONE run, comparable across variants).
	record := runlink.RunRecord{
		RunID:          runID(ir.Workflow.ID, configHash, sha),
		WorkflowID:     ir.Workflow.ID,
		ConfigHash:     configHash,
		SourceRevision: sha,
		Timestamp:      nowRFC3339(),
		Seeds:          seeds,
		ToolVersion:    ToolVersion,
		Metrics: runlink.Metrics{
			CostUSD:   mean(totCost, nRuns),
			LatencyMS: mean(totLat, nRuns),
			TokensIn:  totIn / int64(max1(nRuns)),
			TokensOut: totOut / int64(max1(nRuns)),
			PerNode:   perNodeMetrics(perNode, nRuns),
		},
		IR:           irStructure(ir),
		Scores:       scores,
		RunsReported: 1,
	}

	store := OpenRunStore(repo)
	if err := store.Put(record); err != nil {
		return operational("write run record", err)
	}

	gate := evalGate(scores, record.Metrics, gates)
	data := EvalData{
		RunID: record.RunID, WorkflowID: record.WorkflowID, ConfigHash: configHash,
		Runtime: rt.Name(), Seeds: seeds, Cases: len(cases), Scores: scores,
		Metrics: record.Metrics, StoredAt: store.dir, SingleSeed: singleSeed,
	}

	if singleSeed {
		s.Narratef("eval: WARNING single-seed run — reported as provisional, not a hosted-grade result")
	}
	s.Narratef("eval: run %s stored under %s (link it with `heros link %s`)", record.RunID, store.dir, record.RunID)

	if gate != nil && !gate.Passed {
		s.Narratef("eval: gate %q FAILED: %s", gate.Name, gate.Detail)
		return &ExitError{Code: ExitGateFailed, Msg: fmt.Sprintf("configured gate %q failed: %s", gate.Name, gate.Detail),
			Err: emitErr(s, "eval", ExitGateFailed, data, gate, &OutputError{Code: ExitGateFailed, Kind: "gate", Message: gate.Detail})}
	}
	return s.EmitJSON("eval", ExitOK, data, gate, nil)
}

// aggregateScores runs the real bootstrap over each metric and returns scores carrying the interval.
func aggregateScores(cfg evalstats.Config, series ...evalstats.Series) ([]runlink.Score, error) {
	var out []runlink.Score
	for _, s := range series {
		iv, err := evalstats.Aggregate(s, cfg)
		if err != nil {
			return nil, fmt.Errorf("aggregate %s: %w", s.Metric, err)
		}
		out = append(out, runlink.Score{Metric: s.Metric, Value: iv.Mean, CILow: iv.Low, CIHigh: iv.High})
	}
	return out, nil
}

// evalGate evaluates the customer-configured gates against the aggregated scores. It names the FIRST
// failing gate (a gate failure that does not name the gate is indistinguishable from a crash).
func evalGate(scores []runlink.Score, m runlink.Metrics, g Gates) *GateResult {
	byMetric := map[string]runlink.Score{}
	for _, s := range scores {
		byMetric[s.Metric] = s
	}
	if g.MinQuality != nil {
		q := byMetric["quality"].Value
		if q < *g.MinQuality {
			return &GateResult{Name: "min-quality", Passed: false, Metric: "quality", Value: q, Bound: *g.MinQuality,
				Detail: fmt.Sprintf("quality %.4f is below the configured minimum %.4f", q, *g.MinQuality)}
		}
	}
	if g.MaxCostPerRun != nil {
		if m.CostUSD > *g.MaxCostPerRun {
			return &GateResult{Name: "max-cost-per-run", Passed: false, Metric: "cost_usd", Value: m.CostUSD, Bound: *g.MaxCostPerRun,
				Detail: fmt.Sprintf("cost/run %.6f exceeds the configured maximum %.6f", m.CostUSD, *g.MaxCostPerRun)}
		}
	}
	if g.LatencySLAMs != nil {
		if m.LatencyMS > *g.LatencySLAMs {
			return &GateResult{Name: "latency-sla", Passed: false, Metric: "latency_ms", Value: m.LatencyMS, Bound: *g.LatencySLAMs,
				Detail: fmt.Sprintf("latency %.1fms exceeds the configured SLA %.1fms", m.LatencyMS, *g.LatencySLAMs)}
		}
	}
	// If any gate was configured, report the passing outcome so success is legible too.
	if g.MinQuality != nil || g.MaxCostPerRun != nil || g.LatencySLAMs != nil {
		return &GateResult{Name: "configured-gates", Passed: true}
	}
	return nil
}

// emitErr writes a failure envelope and returns any write error, so the gate-failed path still produces
// machine output before the process exits non-zero.
func emitErr(s Streams, cmd string, code int, data any, gate *GateResult, oerr *OutputError) error {
	return s.EmitJSON(cmd, code, data, gate, oerr)
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

func modelRef(n discovery.IRNode) string {
	if n.Model.Provider != "" || n.Model.ModelID != "" {
		return n.Model.Provider + "/" + n.Model.ModelID
	}
	return ""
}

func irStructure(ir discovery.IR) runlink.IRStructure {
	out := runlink.IRStructure{ModelRefs: map[string]string{}, PatternLabels: map[string]string{}}
	for _, n := range ir.Nodes {
		out.NodeIDs = append(out.NodeIDs, n.NodeID)
		if r := modelRef(n); r != "" {
			out.ModelRefs[n.NodeID] = r
		}
		if len(n.PatternLabels) > 0 {
			out.PatternLabels[n.NodeID] = n.PatternLabels[0].Pattern
		}
	}
	sort.Strings(out.NodeIDs)
	for _, e := range ir.Edges {
		out.Edges = append(out.Edges, runlink.Edge{From: e.FromNodeID, To: e.ToNodeID, Kind: e.Kind})
	}
	if len(out.ModelRefs) == 0 {
		out.ModelRefs = nil
	}
	if len(out.PatternLabels) == 0 {
		out.PatternLabels = nil
	}
	return out
}

func perNodeMetrics(m map[string]*runlink.NodeMetric, nRuns int) map[string]runlink.NodeMetric {
	if len(m) == 0 || nRuns == 0 {
		return nil
	}
	out := make(map[string]runlink.NodeMetric, len(m))
	for k, v := range m {
		out[k] = runlink.NodeMetric{
			CostUSD: v.CostUSD / float64(nRuns), LatencyMS: v.LatencyMS / float64(nRuns),
			TokensIn: v.TokensIn / int64(nRuns), TokensOut: v.TokensOut / int64(nRuns),
		}
	}
	return out
}

func seedList(n int) []int64 {
	if n < 1 {
		n = 1
	}
	out := make([]int64, n)
	for i := range out {
		out[i] = int64(1000 + i) // deterministic seeds — same run twice yields the same IR/config_hash
	}
	return out
}

func deterministicCases(workflowID string, n int) []string {
	if n < 1 {
		n = 1
	}
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("case-%s-%03d", shortHash(workflowID), i)
	}
	return out
}

func evalConfigHash(workflowID, rev string, seeds []int64, cases []string) string {
	h := sha256.New()
	h.Write([]byte("heros.p11.eval.config.v1\x00"))
	h.Write([]byte(workflowID))
	h.Write([]byte{0})
	h.Write([]byte(rev))
	for _, s := range seeds {
		fmt.Fprintf(h, "\x00s%d", s)
	}
	for _, c := range cases {
		h.Write([]byte{0})
		h.Write([]byte(c))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func runID(workflowID, configHash, rev string) string {
	h := sha256.Sum256([]byte("heros.p11.run.v1\x00" + workflowID + "\x00" + configHash + "\x00" + rev))
	return "run-" + hex.EncodeToString(h[:6])
}

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:4])
}

func mean(total float64, n int) float64 {
	if n == 0 {
		return 0
	}
	return total / float64(n)
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
