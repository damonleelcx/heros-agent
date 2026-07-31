// Command scorecard serves the P4.5 read-only scorecard against a REAL attribution + diagnosis
// run (task 11.5: drive the scorecard against a live, stubbed-provider run; confirm per-node breakdown,
// clusters, bottleneck flags, diagnosis cards with evidence, ablation CI/verdict, and all states — and
// that NO apply affordance exists).
//
// It stands the whole path up: IR → attribution + diagnosis engine → scorecard view → HTTP → page.
// Every localization, cluster, typed cause, ablation delta and state on screen came out of
// internal/attribution + internal/diagnosis, not a hand-written fixture. The ONLY stub is the LLM
// analyst and the ablation executor's provider — everything between the trace and the pixel is the
// shipped code path.
//
// Not a shipped service: a demo harness. It uses a canned analyst and a stub sandboxed executor, which
// is why it lives HERE and not in a package.
//
//	go run ./cmd/demo/scorecard   # then open the printed URLs
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/attrengine"
	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalrun"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/linkage"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/reportstore"
	"github.com/heros-foreal/agentd/internal/scorecard"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8486", "listen address")
	irPath := flag.String("ir", "",
		"path to a Workflow IR emitted by `discover` over a REAL repo; builds a scorecard over that "+
			"repo's real call sites with EXPLICITLY SYNTHETIC traces (the repo is never executed)")
	srcRoot := flag.String("src", "",
		"path to the real repo checkout; when set, the recovered topology is extracted from ACTUAL "+
			"source via tree-sitter (the real linkage extractor), not hand-built")
	flag.Parse()

	src := &memSource{views: map[string]scorecard.View{}}
	ctx := context.Background()

	// ── Variant 1: a rich failing run — every region populated, analyst below floor ──
	src.views["v-diag"] = buildRich(ctx)

	// ── Variant 2: a passing variant — the EMPTY state ──
	src.views["v-clean"] = buildEmpty(ctx)

	s := api.New(nil, config.Config{})
	s.MountP45(src)

	fmt.Println("p4.5 scorecard:")
	fmt.Printf("  rich (all regions) http://%s/p45/scorecard?variant=v-diag\n", *addr)
	fmt.Printf("  empty state        http://%s/p45/scorecard?variant=v-clean\n", *addr)

	if *irPath != "" {
		view, note := buildFromRealIR(ctx, *irPath, *srcRoot)
		src.views["v-real"] = view
		fmt.Printf("  REAL repo call sites http://%s/p45/scorecard?variant=v-real\n", *addr)
		fmt.Println()
		fmt.Println(note)
	}
	fmt.Println()
	if err := http.ListenAndServe(*addr, s.Handler); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// memSource
// ─────────────────────────────────────────────────────────────────────────────

type memSource struct{ views map[string]scorecard.View }

func (m *memSource) Scorecard(variantID string) (scorecard.View, bool) {
	v, ok := m.views[variantID]
	return v, ok
}

// ─────────────────────────────────────────────────────────────────────────────
// The rich run
// ─────────────────────────────────────────────────────────────────────────────

func buildRich(ctx context.Context) scorecard.View {
	ir := demoIR()
	v := attribution.Variant{VariantID: "v-diag", ConfigHash: hash64("v-diag"), EvalSetHash: hash64("es"), WorkflowID: "wf-diag"}

	cases := []attribution.FailingCase{
		// prompt-format-drift cluster (node3 drops its output contract)
		promptDrift("pd-1"), promptDrift("pd-2"), promptDrift("pd-3"),
		// tool-returns-empty cluster
		toolEmpty("te-1"), toolEmpty("te-2"),
		// multi-hop cluster
		multiHop("mh-1"), multiHop("mh-2"),
		// residue → analyst (reflect non-convergence)
		residue("rs-1"),
	}

	// A metered analyst (canned), below its floor so the uncalibrated banner shows.
	meter := evalrun.NewMeter("demo", evalrun.Budget{})
	analyst := attrengine.NewMeteredAnalyst(cannedAnalyst{}, meter, 0.02)
	cal := diagnosis.Calibrate("demo_analyst",
		map[string]diagnosis.TaxonomyCode{"h1": diagnosis.CauseNonConvergence, "h2": diagnosis.CauseRetrievalMiss, "h3": diagnosis.CauseMisroute, "h4": diagnosis.CauseContextOverflow},
		map[string]diagnosis.TaxonomyCode{"h1": diagnosis.CauseNonConvergence, "h2": diagnosis.CauseContextOverflow, "h3": diagnosis.CauseContextOverflow, "h4": diagnosis.CauseContextOverflow},
		diagnosis.DefaultAnalystFloor)

	// A sandboxed ablation runner: node3's swap lifts success (bottleneck); router's does not
	// (inconclusive).
	runner, err := attrengine.NewFanoutAblationRunner(stubExecutor{}, meter, 4)
	if err != nil {
		log.Fatalf("ablation runner: %v", err)
	}

	eng := &scorecard.Engine{Store: reportstore.NewMemStore(), Runner: runner, Analyst: analyst, Cal: cal}
	view, err := eng.Generate(ctx, scorecard.GenerateInput{
		IR:               ir,
		Variant:          v,
		FailingCases:     cases,
		Overall:          scorecard.OverallMetrics{TaskSuccess: 0.60, NCases: 20, NFailing: len(cases), CostUSD: 0.83, LatencyMS: 4200},
		AblationTopN:     3,
		SwappedConfigRef: func(node string) string { return "cfg-swap-" + node },
		AblationConfig: attribution.AblationConfig{
			Metric: "task_success", Direction: evalstats.HigherIsBetter,
			Seeds: []int64{0, 1, 2, 3, 4}, Stats: evalstats.DefaultConfig(),
		},
	})
	if err != nil {
		log.Fatalf("generate rich: %v", err)
	}
	return view
}

func buildEmpty(ctx context.Context) scorecard.View {
	ir := demoIR()
	v := attribution.Variant{VariantID: "v-clean", ConfigHash: hash64("v-clean"), EvalSetHash: hash64("es"), WorkflowID: "wf-diag"}
	eng := &scorecard.Engine{Store: reportstore.NewMemStore()}
	view, err := eng.Generate(ctx, scorecard.GenerateInput{
		IR: ir, Variant: v, FailingCases: nil,
		Overall: scorecard.OverallMetrics{TaskSuccess: 1.0, NCases: 20, NFailing: 0},
	})
	if err != nil {
		log.Fatalf("generate empty: %v", err)
	}
	return view
}

// ─────────────────────────────────────────────────────────────────────────────
// canned analyst + stub sandboxed executor (the only stubs)
// ─────────────────────────────────────────────────────────────────────────────

type cannedAnalyst struct{}

func (cannedAnalyst) Analyze(_ context.Context, fc attribution.FailingCase, r diagnosis.Rubric) (diagnosis.AnalystResponse, error) {
	// Constrained to the rubric: return a code the node's pattern admits.
	if len(r.Codes) > 0 {
		return diagnosis.AnalystResponse{Code: string(diagnosis.CauseNonConvergence), Confidence: 0.55}, nil
	}
	return diagnosis.AnalystResponse{}, nil
}

type stubExecutor struct{ faulty string }

func (stubExecutor) Sandboxed() bool { return true }

func (e stubExecutor) Execute(_ context.Context, unit attrengine.AblationUnit, v attribution.Variant, metric string) (attrengine.UnitResult, error) {
	// Baseline ≈ 0.4; swapping the faulty node lifts to ≈ 0.9 (bottleneck); any other swap stays
	// ≈ 0.4 (inconclusive). Deterministic per (case, seed).
	faulty := e.faulty
	if faulty == "" {
		faulty = "node3"
	}
	mean := 0.40
	if !unit.Baseline && unit.Node == faulty {
		mean = 0.90
	}
	var obs []evalstats.Observation
	for ci := 0; ci < 10; ci++ {
		caseID := fmt.Sprintf("case-%d", ci)
		jitter := 0.01 * float64((ci*7+int(unit.Seed)*3)%5)
		obs = append(obs, evalstats.Observation{CaseID: caseID, Seed: unit.Seed, Value: clamp(mean + jitter)})
	}
	return attrengine.UnitResult{Obs: obs, CostUSD: 0.001}, nil
}

func clamp(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// ─────────────────────────────────────────────────────────────────────────────
// Real-repo scorecard: REAL discovered call sites, EXPLICITLY SYNTHETIC traces
// ─────────────────────────────────────────────────────────────────────────────

// realIR is the subset of the discover-emitted Workflow IR this demo reads. It takes the REAL call
// sites (file + symbol + provider + invocation semantics) and nothing else — the repo is never run.
type realIR struct {
	Workflow struct {
		ID       string `json:"id"`
		Language string `json:"language"`
	} `json:"workflow"`
	Nodes []struct {
		NodeID   string `json:"node_id"`
		CallSite struct {
			File   string `json:"file"`
			Symbol string `json:"symbol"`
			Line   int    `json:"line_start"`
		} `json:"call_site"`
		Model struct {
			Provider string `json:"provider"`
		} `json:"model"`
		Invocation struct {
			Type string `json:"type"`
		} `json:"invocation_semantics"`
	} `json:"nodes"`
}

// semanticToPattern maps discovery's observed invocation semantics onto a P3.5 structural pattern. It
// is a HEURISTIC stand-in for the real P3.5 classifier (which this repo has no graph to run), stated
// as such: conditional dispatch reads as Routing, a retry/fallback loop as Reflection, a single call
// as Tool Use.
func semanticToPattern(sem string) patternclassifier.Pattern {
	switch sem {
	case "conditional":
		return patternclassifier.Routing
	case "loop":
		return patternclassifier.Reflection
	default:
		return patternclassifier.ToolUse
	}
}

// buildFromRealIR builds a scorecard over a real repo's discovered call sites. The NODE IDS are the
// repo's real symbols; the TRACES are synthesized and clearly labeled — the repo is never executed, so
// these are illustrative failures, not measured ones.
func buildFromRealIR(ctx context.Context, path, srcRoot string) (scorecard.View, string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read ir: %v", err)
	}
	var ir realIR
	if err := json.Unmarshal(raw, &ir); err != nil {
		log.Fatalf("parse ir: %v", err)
	}

	// Pick up to six real call sites from distinct files, in a stable order, so the node ids are
	// recognizable symbols rather than opaque hashes.
	sort.Slice(ir.Nodes, func(i, j int) bool {
		if ir.Nodes[i].CallSite.File != ir.Nodes[j].CallSite.File {
			return ir.Nodes[i].CallSite.File < ir.Nodes[j].CallSite.File
		}
		return ir.Nodes[i].CallSite.Line < ir.Nodes[j].CallSite.Line
	})
	seenID := map[string]bool{}
	type picked struct {
		id, file, sym string
		pat           patternclassifier.Pattern
	}
	// When extracting from real source, pick call sites from the BUSIEST single file so the tree-sitter
	// extractor can recover the intra-file call-graph / shared-state links between them (cross-file
	// selection would only ever share temporal linkage). Otherwise pick distinct files for a varied
	// illustrative view.
	sameFile := srcRoot != ""
	busiest := ""
	if sameFile {
		count := map[string]int{}
		for _, n := range ir.Nodes {
			count[n.CallSite.File]++
		}
		best := 0
		for f, c := range count {
			if c > best || (c == best && f < busiest) {
				best, busiest = c, f
			}
		}
	}
	seenFile := map[string]bool{}
	var chain []picked
	for _, n := range ir.Nodes {
		f := n.CallSite.File
		if sameFile {
			if f != busiest {
				continue
			}
		} else if seenFile[f] {
			continue
		}
		seenFile[f] = true
		id := n.CallSite.Symbol
		if id == "" || seenID[id] {
			id = fmt.Sprintf("%s:%d", f, n.CallSite.Line)
		}
		seenID[id] = true
		chain = append(chain, picked{id: id, file: f, sym: n.CallSite.Symbol, pat: semanticToPattern(n.Invocation.Type)})
		if len(chain) >= 8 {
			break
		}
	}
	if len(chain) < 3 {
		log.Fatalf("real IR yielded only %d usable call sites; need ≥3 for a workflow", len(chain))
	}

	// The faulty node: the first Tool-Use call site (drops its output contract). If none, the last.
	faulty := chain[len(chain)-1].id
	for _, p := range chain {
		if p.pat == patternclassifier.ToolUse {
			faulty = p.id
			break
		}
	}

	// Build the IR: real symbols as node ids, P3.5-style pattern labels, an output contract on the
	// faulty node so the prompt-format-drift detector can localize it.
	contract := map[string]any{"type": "object", "required": []any{"a"},
		"properties": map[string]any{"a": map[string]any{"type": "string"}}}
	var nodes []discovery.IRNode
	var edges []discovery.IREdge
	for _, p := range chain {
		out := map[string]any{"type": "object"}
		if p.id == faulty {
			out = contract
		}
		// FAITHFUL to the real repo: discovery over hermes-agent produced NO pattern labels and NO
		// edges (a hand-rolled agent, not a graph framework). So the nodes are left UNCLASSIFIED and
		// the workflow has no edges — exactly the degraded input FR13/FR14 must handle. Only the faulty
		// node carries an output contract (so the pattern-agnostic prompt-format-drift detector can
		// still localize it); everything else is diagnosed by pattern-agnostic rules only and reads
		// "not classified" on the scorecard.
		nodes = append(nodes, discovery.IRNode{
			NodeID: p.id, Kind: "static_definition",
			IOContract: discovery.IRIOContract{InputSchema: map[string]any{"type": "object"}, OutputSchema: out},
		})
	}
	_ = edges // no edges: hand-rolled agent, locality comes from trace order
	builtIR := &discovery.IR{IRVersion: discovery.IRVersionPatternLabels,
		Workflow: discovery.IRWorkflow{ID: "hermes-agent · hand-rolled, no graph · SYNTHETIC traces (illustration)", Language: ir.Workflow.Language},
		Nodes:    nodes}

	ids := make([]string, len(chain))
	for i, p := range chain {
		ids[i] = p.id
	}

	// Synthesize failing cases over the REAL node ids. Clearly synthetic: a fault dropped at `faulty`,
	// a tool-empty cluster, and a multi-hop cluster.
	var cases []attribution.FailingCase
	for i := 0; i < 3; i++ {
		cases = append(cases, realPromptDrift(fmt.Sprintf("pd-%d", i), ids, faulty))
	}
	for i := 0; i < 2; i++ {
		cases = append(cases, realToolEmpty(fmt.Sprintf("te-%d", i), ids))
	}
	for i := 0; i < 2; i++ {
		cases = append(cases, realMultiHop(fmt.Sprintf("mh-%d", i), ids))
	}

	meter := evalrun.NewMeter("real", evalrun.Budget{})
	analyst := attrengine.NewMeteredAnalyst(cannedAnalyst{}, meter, 0.02)
	cal := diagnosis.Calibrate("real_analyst",
		map[string]diagnosis.TaxonomyCode{"h1": diagnosis.CauseNonConvergence, "h2": diagnosis.CauseMisroute, "h3": diagnosis.CauseContextOverflow},
		map[string]diagnosis.TaxonomyCode{"h1": diagnosis.CauseNonConvergence, "h2": diagnosis.CauseNonConvergence, "h3": diagnosis.CauseNonConvergence},
		diagnosis.DefaultAnalystFloor)
	runner, err := attrengine.NewFanoutAblationRunner(stubExecutor{faulty: faulty}, meter, 4)
	if err != nil {
		log.Fatalf("runner: %v", err)
	}

	// Recovered static linkage. When --src points at the real checkout, the signals are EXTRACTED from
	// actual source with the tree-sitter extractor (linkage.ExtractPythonCallSites) — real callees and
	// shared-state refs, no hand-building. Otherwise (no source) the chain is constructed illustratively
	// so the topology card still renders. Either way the edges are recovered even though framework
	// detection found 0.
	var sites []linkage.CallSite
	extractedNote := "constructed illustratively (pass --src <repo> to extract from real source)"
	if srcRoot != "" {
		for i, p := range chain {
			src, err := os.ReadFile(srcRoot + "/" + p.file)
			if err != nil {
				continue
			}
			got := linkage.ExtractPythonCallSites(src, []linkage.LLMSite{{NodeID: p.id, EnclosingSymbol: p.sym}})
			for j := range got {
				got[j].Order = i
			}
			sites = append(sites, got...)
		}
		if len(sites) > 0 {
			extractedNote = fmt.Sprintf("EXTRACTED from real source via tree-sitter (%d call sites)", len(sites))
		}
	}
	if srcRoot == "" || len(sites) == 0 {
		sites = sites[:0]
		for i, p := range chain {
			cs := linkage.CallSite{NodeID: p.id, EnclosingSymbol: p.id, StateRefs: []string{"self._session_messages"}, Order: i}
			if i+1 < len(chain) {
				cs.Callees = []string{chain[i+1].id}
			}
			sites = append(sites, cs)
		}
	}

	v := attribution.Variant{VariantID: "v-real", ConfigHash: hash64("v-real"), EvalSetHash: hash64("real-es"),
		WorkflowID: builtIR.Workflow.ID}
	eng := &scorecard.Engine{Store: reportstore.NewMemStore(), Runner: runner, Analyst: analyst, Cal: cal}
	view, err := eng.Generate(ctx, scorecard.GenerateInput{
		IR: builtIR, Variant: v, FailingCases: cases,
		Overall:          scorecard.OverallMetrics{TaskSuccess: 0.58, NCases: 20, NFailing: len(cases), CostUSD: 0.42, LatencyMS: 3100},
		StaticCallSites:  sites,
		AblationTopN:     3,
		SwappedConfigRef: func(node string) string { return "swap-" + node },
		AblationConfig:   attribution.AblationConfig{Metric: "task_success", Direction: evalstats.HigherIsBetter, Seeds: []int64{0, 1, 2, 3, 4}, Stats: evalstats.DefaultConfig()},
	})
	if err != nil {
		log.Fatalf("generate real: %v", err)
	}
	note := fmt.Sprintf(
		"  ⚠️  v-real uses %d REAL call sites discovered in the repo (e.g. %q), but the traces are\n"+
			"      SYNTHETIC — the repo was never executed. The fault is injected at %q. This shows the\n"+
			"      scorecard SURFACE over real node names, not a measured diagnosis of the repo.\n"+
			"      Recovered topology: %s.",
		len(chain), chain[0].id, faulty, extractedNote)
	return view, note
}

func realNodeSpan(caseID, node string, i int, cost, lat float64, attrs map[string]any) telemetry.Span {
	return span(caseID, node, i, cost, lat, attrs)
}

func realPromptDrift(id string, ids []string, faulty string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{}}
	var spans []telemetry.Span
	for i, n := range ids {
		out := json.RawMessage(`{"a":"ok"}`)
		if n == faulty {
			out = json.RawMessage(`{"junk":"contract dropped"}`) // synthetic contract violation
		}
		tr.NodeOutputs[n] = out
		cost, lat := 0.002, 120.0
		if n == faulty {
			cost, lat = 0.090, 140.0 // faulty node also carries the spend, to exercise a cost flag
		}
		spans = append(spans, realNodeSpan(id, n, i, cost, lat, nil))
	}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: spans}
	tr.Output, tr.Failed = json.RawMessage(`{"a":"wrong"}`), true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "hermes"}, Trace: tr}
}

func realToolEmpty(id string, ids []string) attribution.FailingCase {
	node := ids[len(ids)/2]
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{}}
	spans := []telemetry.Span{realNodeSpan(id, ids[0], 0, 0.002, 100, nil), realNodeSpan(id, node, 1, 0.002, 100, nil)}
	spans = append(spans, telemetry.Span{TraceID: telemetry.TraceID(id), SpanID: telemetry.ToolSpanID(id+node, "search", 0),
		Kind: telemetry.SpanKindTool, Name: "search", Status: telemetry.SpanStatusOK,
		StartTime: time.Unix(1_700_000_100, 0), EndTime: time.Unix(1_700_000_100, 0).Add(30 * time.Millisecond),
		Attributes: map[string]any{telemetry.AttrNodeID: node, telemetry.AttrToolReason: "empty"}})
	tr.NodeOutputs[ids[0]] = json.RawMessage(`{"a":"x"}`)
	tr.NodeOutputs[node] = json.RawMessage(`{"a":"ok"}`)
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: spans}
	tr.Output, tr.Failed = json.RawMessage(`{"a":"wrong"}`), true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "hermes"}, Trace: tr}
}

func realMultiHop(id string, ids []string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{}}
	var spans []telemetry.Span
	// Walk the whole chain plus repeat the last node to make it a deep (multi-hop) run.
	walk := append(append([]string(nil), ids...), ids[len(ids)-1], ids[len(ids)-1])
	for i, n := range walk {
		tr.NodeOutputs[n] = json.RawMessage(`{"a":"ok"}`)
		spans = append(spans, realNodeSpan(id, n, i, 0.002, 120, nil))
	}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: spans}
	tr.Output, tr.Failed = json.RawMessage(`{"a":"wrong"}`), true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "hermes"}, Trace: tr}
}

// ─────────────────────────────────────────────────────────────────────────────
// fixtures
// ─────────────────────────────────────────────────────────────────────────────

func demoIR() *discovery.IR {
	contract := map[string]any{"type": "object", "required": []any{"a"},
		"properties": map[string]any{"a": map[string]any{"type": "string"}}}
	node := func(id string, p patternclassifier.Pattern) discovery.IRNode {
		return discovery.IRNode{NodeID: id, Kind: "static_definition",
			PatternLabels: []discovery.IRPatternLabel{{Pattern: string(p),
				Confidence: patternclassifier.ConfidenceTopologyDetermined, Source: string(patternclassifier.SourceRule),
				DetectorID: "demo", TaxonomyVersion: patternclassifier.TaxonomyVersion}},
			IOContract: discovery.IRIOContract{InputSchema: map[string]any{"type": "object"}, OutputSchema: contract}}
	}
	return &discovery.IR{IRVersion: discovery.IRVersionPatternLabels,
		Workflow: discovery.IRWorkflow{ID: "wf-diag", Language: "python"},
		Nodes: []discovery.IRNode{
			node("router", patternclassifier.Routing),
			node("node3", patternclassifier.ToolUse),
			node("reflect", patternclassifier.Reflection),
		}}
}

func span(caseID, node string, i int, cost, lat float64, attrs map[string]any) telemetry.Span {
	base := time.Unix(1_700_000_000, 0).Add(time.Duration(i) * time.Second)
	a := map[string]any{telemetry.AttrNodeID: node, telemetry.AttrCostUSD: cost, telemetry.AttrLatencyMS: lat, telemetry.AttrNodeFailed: false}
	for k, val := range attrs {
		a[k] = val
	}
	return telemetry.Span{TraceID: telemetry.TraceID(caseID), SpanID: telemetry.NodeSpanID(caseID + node),
		Kind: telemetry.SpanKindNode, Name: node, StartTime: base, EndTime: base.Add(time.Duration(lat) * time.Millisecond),
		Status: telemetry.SpanStatusOK, Attributes: a}
}

func promptDrift(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{
		"router": json.RawMessage(`{"a":"branch"}`), "node3": json.RawMessage(`{"junk":"no a"}`), "reflect": json.RawMessage(`{"a":"wrong"}`)}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		span(id, "router", 0, 0.001, 50, nil), span(id, "node3", 1, 0.100, 100, nil), span(id, "reflect", 2, 0.002, 800, nil)}}
	tr.Output, tr.Failed = json.RawMessage(`{"a":"wrong"}`), true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-diag"}, Trace: tr}
}

func toolEmpty(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{"router": json.RawMessage(`{"a":"x"}`), "node3": json.RawMessage(`{"a":"ok"}`)}}
	tool := telemetry.Span{TraceID: telemetry.TraceID(id), SpanID: telemetry.ToolSpanID(id+"node3", "search", 0),
		Kind: telemetry.SpanKindTool, Name: "search", Status: telemetry.SpanStatusOK,
		StartTime: time.Unix(1_700_000_100, 0), EndTime: time.Unix(1_700_000_100, 0).Add(30 * time.Millisecond),
		Attributes: map[string]any{telemetry.AttrNodeID: "node3", telemetry.AttrToolName: "search", telemetry.AttrToolReason: "empty"}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		span(id, "router", 0, 0.001, 50, nil), span(id, "node3", 1, 0.002, 100, nil), tool}}
	tr.Output, tr.Failed = json.RawMessage(`{"a":"wrong"}`), true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-diag"}, Trace: tr}
}

func multiHop(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{}}
	var spans []telemetry.Span
	for i, n := range []string{"router", "node3", "reflect", "reflect", "reflect"} {
		spans = append(spans, span(id, n, i, 0.002, 120, nil))
	}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: spans}
	// node3 & reflect emit schema-valid output so this is NOT a prompt-drift; it clusters as multi-hop.
	tr.NodeOutputs["router"] = json.RawMessage(`{"a":"x"}`)
	tr.NodeOutputs["node3"] = json.RawMessage(`{"a":"ok"}`)
	tr.NodeOutputs["reflect"] = json.RawMessage(`{"a":"still-wrong"}`)
	tr.Output, tr.Failed = json.RawMessage(`{"a":"still-wrong"}`), true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-diag"}, Trace: tr}
}

func residue(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{
		"router": json.RawMessage(`{"a":"x"}`), "node3": json.RawMessage(`{"a":"ok"}`), "reflect": json.RawMessage(`{"a":"plausible-but-wrong"}`)}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		span(id, "router", 0, 0.001, 50, nil), span(id, "node3", 1, 0.002, 100, nil), span(id, "reflect", 2, 0.002, 120, nil)}}
	tr.Output, tr.Failed = json.RawMessage(`{"a":"plausible-but-wrong"}`), true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-diag"}, Trace: tr}
}

func hash64(seed string) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 64)
	for i := range out {
		out[i] = hex[(int(seed[i%len(seed)])*7+i*3)%16]
	}
	return string(out)
}
