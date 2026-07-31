// Package evale2e is the P4 end-to-end proof: one in-process pipeline from IR to leaderboard, with
// every claim in the M5 exit checklist (PRD §13) asserted against what it actually produces.
//
// It exists because the per-package tests each prove one link. The failure this catches is the one
// none of them can: a chain where every link holds and the chain still lies — a coverage report that
// never reaches the scorer, a pattern label that never reaches metric selection, a weak reference
// that gets dropped somewhere between the generator and the gate.
package evale2e

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalgen"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalrun"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/runqueue"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// ─────────────────────────────────────────────────────────────────────────────
// Task 8.1 — the fixtures
// ─────────────────────────────────────────────────────────────────────────────

// fxIR is the multi-pattern workflow the whole phase is proved against:
// Routing -> per-branch Tool Use -> Reflection (a self-edge loop).
func fxIR() *discovery.IR {
	node := func(id string, p patternclassifier.Pattern) discovery.IRNode {
		return discovery.IRNode{
			NodeID: id, Kind: "static_definition",
			PatternLabels: []discovery.IRPatternLabel{{
				Pattern:         string(p),
				Confidence:      patternclassifier.ConfidenceTopologyDetermined,
				Source:          string(patternclassifier.SourceRule),
				DetectorID:      "evale2e",
				TaxonomyVersion: patternclassifier.TaxonomyVersion,
			}},
			IOContract: discovery.IRIOContract{
				InputSchema: map[string]any{
					"type": "object", "required": []any{"route", "q"},
					"properties": map[string]any{
						"route": map[string]any{"type": "string"},
						"q":     map[string]any{"type": "string"},
						"turns": map[string]any{"type": "integer"},
					},
				},
				OutputSchema: map[string]any{
					"type": "object", "required": []any{"a"},
					"properties": map[string]any{"a": map[string]any{"type": "string"}},
				},
			},
		}
	}
	return &discovery.IR{
		IRVersion: discovery.IRVersionPatternLabels,
		Workflow:  discovery.IRWorkflow{ID: "wf-evale2e", Language: "python"},
		Nodes: []discovery.IRNode{
			node("router", patternclassifier.Routing),
			node("branch_a", patternclassifier.ToolUse),
			node("branch_b", patternclassifier.RetrievalRAG),
			node("reflect", patternclassifier.Reflection),
		},
		Edges: []discovery.IREdge{
			{FromNodeID: "router", ToNodeID: "branch_a", Kind: "control"},
			{FromNodeID: "router", ToNodeID: "branch_b", Kind: "control"},
			{FromNodeID: "branch_a", ToNodeID: "reflect", Kind: "data"},
			{FromNodeID: "branch_b", ToNodeID: "reflect", Kind: "data"},
			{FromNodeID: "reflect", ToNodeID: "reflect", Kind: "control"},
		},
	}
}

// fxIRWithDeadBranch adds a branch outcome no input can select.
func fxIRWithDeadBranch() *discovery.IR {
	ir := fxIR()
	ir.Nodes = append(ir.Nodes, discovery.IRNode{
		NodeID: "branch_dead", Kind: "static_definition",
		IOContract: discovery.IRIOContract{
			InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
			OutputSchema: map[string]any{"type": "object"},
		},
	})
	ir.Edges = append(ir.Edges, discovery.IREdge{FromNodeID: "router", ToNodeID: "branch_dead", Kind: "control"})
	return ir
}

// fxHandAuthored is the USER's eval set — two cases a person wrote, with gold references.
func fxHandAuthored() []evalharness.Case {
	mk := func(id, route, q string, turns int) evalharness.Case {
		in, _ := json.Marshal(map[string]any{"route": route, "q": q, "turns": turns})
		ref, _ := json.Marshal(map[string]any{"a": "correct"})
		return evalharness.Case{
			CaseID: id, WorkflowID: "wf-evale2e", Suite: "hand",
			Input: in, Reference: ref, Label: evalharness.LabelGold,
			Origin: evalharness.OriginHandAuthored,
		}
	}
	return []evalharness.Case{
		mk("hand-1", "branch_a", "summarize the quarterly revenue report", 1),
		mk("hand-2", "branch_b", "look up the shipping status for order 4471", 2),
	}
}

// simRuntime interprets a case the way the fixture workflow would and emits real telemetry spans,
// so coverage is derived through the production TraceEvidence path rather than asserted.
type simRuntime struct {
	reachable map[string]bool
	mu        sync.Mutex
	probes    int
}

func newSimRuntime(routes ...string) *simRuntime {
	m := map[string]bool{}
	for _, r := range routes {
		m[r] = true
	}
	return &simRuntime{reachable: m}
}

func (p *simRuntime) route(c evalharness.Case) (string, int) {
	var in map[string]any
	_ = json.Unmarshal(c.Input, &in)
	route, _ := in["route"].(string)
	if !p.reachable[route] {
		route = "branch_a"
	}
	turns := 1
	if t, ok := in["turns"].(float64); ok && int(t) > 0 {
		turns = int(t)
	}
	if turns > 8 {
		turns = 8
	}
	return route, turns
}

func (p *simRuntime) Probe(_ context.Context, ir *discovery.IR, c evalharness.Case) (evalgen.Evidence, error) {
	p.mu.Lock()
	p.probes++
	p.mu.Unlock()
	route, turns := p.route(c)
	var spans []telemetry.Span
	add := func(node string, i int) {
		spans = append(spans, telemetry.Span{
			TraceID: telemetry.TraceID(c.CaseID), Kind: telemetry.SpanKindNode,
			SpanID:     telemetry.NodeSpanID(c.CaseID + node + fmt.Sprint(i)),
			Status:     telemetry.SpanStatusOK,
			Attributes: map[string]any{telemetry.AttrNodeID: node},
		})
	}
	add("router", 0)
	add(route, 1)
	for i := 0; i < turns; i++ {
		add("reflect", 2+i)
	}
	return evalgen.TraceEvidence(ir, c.CaseID, telemetry.Trace{Spans: spans}), nil
}

// targetedGenerator is the LLM-driven layer's model seam: it answers the targets it is handed, and
// labels its references weak — because that is what an unreviewed synthetic reference is.
type targetedGenerator struct {
	mu         sync.Mutex
	requests   []evalgen.CaseRequest
	answerable map[string]bool
}

func newTargetedGenerator(unanswerable ...string) *targetedGenerator {
	blocked := map[string]bool{}
	for _, u := range unanswerable {
		blocked[u] = true
	}
	return &targetedGenerator{answerable: blocked}
}

func (g *targetedGenerator) GenerateCases(_ context.Context, req evalgen.CaseRequest) ([]evalgen.GeneratedCase, error) {
	g.mu.Lock()
	g.requests = append(g.requests, req)
	g.mu.Unlock()

	var out []evalgen.GeneratedCase
	ref, _ := json.Marshal(map[string]any{"a": "correct"})
	for _, target := range req.Targets {
		if g.answerable[target] {
			continue // this generator honestly cannot reach it
		}
		route, turns := "branch_a", 1
		if _, to, ok := evalgen.ParseEdgeID(target); ok && (to == "branch_a" || to == "branch_b") {
			route = to
		}
		if _, outcome, ok := evalgen.ParseBranchID(target); ok && (outcome == "branch_a" || outcome == "branch_b") {
			route = outcome
		}
		if node, bound, ok := evalgen.ParseLoopBoundID(target); ok && node == "reflect" {
			turns = evalgen.DefaultLoopBounds[bound]
		}
		in, _ := json.Marshal(map[string]any{"route": route, "q": "targeted case for " + target, "turns": turns})
		out = append(out, evalgen.GeneratedCase{Input: in, Reference: ref, Targets: []string{target}})
	}
	for _, k := range req.EdgeCases {
		in, _ := json.Marshal(map[string]any{"route": "branch_a", "q": "edge case " + string(k), "turns": 1})
		out = append(out, evalgen.GeneratedCase{Input: in, Reference: ref, EdgeCase: k, Targets: []string{string(k)}})
	}
	return out, nil
}

// baseline measures case difficulty by running the baseline configuration for real.
type baseline struct {
	mu sync.Mutex
	n  int
}

func (b *baseline) Pass(_ context.Context, c evalharness.Case) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.n++
	switch c.EdgeCase {
	case evalharness.EdgeCaseNone:
		return b.n%5 != 0, nil
	default:
		return b.n%3 == 0, nil
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Variants and the stubbed provider
// ─────────────────────────────────────────────────────────────────────────────

type variantSpec struct {
	id, label, configHash string
	providers             []string
	quality               float64
	costPerNode           float64
	latencyPerNode        float64
}

func hash64(seed string) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 64)
	for i := range out {
		out[i] = hex[(int(seed[i%len(seed)])*7+i*3)%16]
	}
	return string(out)
}

func spec(id string, quality, cost, latency float64, providers ...string) variantSpec {
	if len(providers) == 0 {
		providers = []string{"anthropic"}
	}
	return variantSpec{id: id, label: id, configHash: hash64(id), providers: providers,
		quality: quality, costPerNode: cost, latencyPerNode: latency}
}

// runHandler executes one {variant, case, seed}. Only the PROVIDER is stubbed: the spans it emits
// are the shape the P2.5 instrument emits, and everything downstream is the shipped path.
type runHandler struct {
	v        variantSpec
	set      evalrun.EvalSet
	registry *evalharness.Registry
	store    *evalrun.MemStore
	meter    *evalrun.Meter
	ir       *discovery.IR
	sim      *simRuntime
	caseOf   map[string]string
}

func (h *runHandler) Handle(ctx context.Context, item runqueue.Item) error {
	caseID, ok := h.caseOf[item.RunID]
	if !ok {
		return fmt.Errorf("no case for run %s", item.RunID)
	}
	c, _ := h.set.CaseByID(caseID)
	rc := telemetry.RunContext{
		VariantID: h.v.id, RunID: item.RunID, ConfigHash: h.v.configHash,
		Seed: item.Seed, CaseID: caseID,
	}
	tr, cost := h.stubProviderRun(rc, c)
	if err := h.meter.Charge(evalrun.SpendExecution, cost); err != nil {
		return err
	}
	values, err := evalharness.RunMetrics(ctx, h.registry, tr, c)
	if err != nil {
		return err
	}
	values = append(values, evalharness.ContributionMetrics(tr, c)...)

	// The node's P3.5 label rides onto every row, so "slice the board by pattern" is a query.
	pattern := ""
	if p, _ := evalharness.NodePattern(h.ir, "router"); p != "" {
		pattern = string(p)
	}
	rows := evalrun.ResultsFrom(rc, h.ir.Workflow.ID, h.set.Hash, c, pattern, values, time.Unix(1_800_000_000, 0))
	return h.store.PutResults(ctx, rows)
}

func (h *runHandler) stubProviderRun(rc telemetry.RunContext, c evalharness.Case) (evalharness.Trace, float64) {
	rng := rand.New(rand.NewSource(int64(len(rc.ConfigHash))*31 + rc.Seed*7919 +
		int64(len(c.CaseID))*104729 + int64(c.CaseID[len(c.CaseID)-1])))
	route, turns := h.sim.route(c)

	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{}, NodeInputs: map[string]json.RawMessage{}}
	var spans []telemetry.Span
	now := time.Unix(1_800_000_000, 0)
	var total float64
	nodes := append([]string{"router", route}, repeat("reflect", turns)...)
	for i, node := range nodes {
		cost := h.v.costPerNode * (0.85 + rng.Float64()*0.3)
		latency := h.v.latencyPerNode * (0.8 + rng.Float64()*0.4)
		total += cost
		start := now.Add(time.Duration(i) * time.Duration(latency) * time.Millisecond)
		spans = append(spans, telemetry.Span{
			TraceID:   telemetry.TraceID(rc.RunID),
			SpanID:    telemetry.NodeSpanID(rc.RunID + ":" + node + ":" + fmt.Sprint(i)),
			Kind:      telemetry.SpanKindNode,
			Name:      "chat " + node,
			StartTime: start,
			EndTime:   start.Add(time.Duration(latency) * time.Millisecond),
			Status:    telemetry.SpanStatusOK,
			Attributes: map[string]any{
				telemetry.AttrNodeID:           node,
				telemetry.AttrRunID:            rc.RunID,
				telemetry.AttrVariantID:        rc.VariantID,
				telemetry.AttrConfigHash:       rc.ConfigHash,
				telemetry.AttrCaseID:           rc.CaseID,
				telemetry.AttrSeed:             rc.Seed,
				telemetry.AttrCostUSD:          cost,
				telemetry.AttrLatencyMS:        latency,
				telemetry.AttrNodeFailed:       false,
				telemetry.AttrGenAIUsageInput:  200 + rng.Intn(120),
				telemetry.AttrGenAIUsageOutput: 80 + rng.Intn(60),
			},
		})
	}
	tr.Trace = telemetry.Trace{Run: rc, Spans: spans}
	if rng.Float64() < h.v.quality {
		tr.Output = json.RawMessage(`{"a":"correct"}`)
	} else {
		tr.Output = json.RawMessage(`{"a":"wrong"}`)
	}
	return tr, total
}

func repeat(s string, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, s)
	}
	return out
}

// memQueue is an in-memory dispatcher with the queue's lease/ack semantics.
type memQueue struct {
	mu     sync.Mutex
	ready  []runqueue.Item
	leased map[string]bool
	acked  int
}

func newMemQueue(units []evalrun.Unit) *memQueue {
	q := &memQueue{leased: map[string]bool{}}
	for _, u := range units {
		q.ready = append(q.ready, runqueue.Item{
			RunID: u.RunID, ConfigHash: u.ConfigHash, SourceRevision: u.SourceRevision, Seed: u.Seed,
		})
	}
	return q
}

func (q *memQueue) Dequeue(context.Context, string) (*runqueue.Item, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.ready) == 0 {
		return nil, runqueue.ErrEmpty
	}
	it := q.ready[0]
	q.ready = q.ready[1:]
	q.leased[it.RunID] = true
	return &it, nil
}

func (q *memQueue) Ack(_ context.Context, runID, _ string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.leased, runID)
	q.acked++
	return nil
}

func (q *memQueue) Nack(_ context.Context, runID, _ string, _ error, _ time.Duration) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.leased, runID)
	return nil
}

func (q *memQueue) Renew(context.Context, string, string) error { return nil }

// tableJudge answers from a lookup table so a calibration assertion is about the AGREEMENT
// ARITHMETIC rather than about a model's behaviour.
//
// It keys on the case's rendered INPUT, which RenderJudgePrompt embeds verbatim — not on a synthetic
// marker. An earlier version looked for a `"case":"<id>"` field the fixture's inputs do not carry,
// so the judge silently answered 0 for every case and the calibration measured nothing. A stub that
// fails to match must be caught by the assertion, which is why the eligible-judge half of this test
// exists at all.
type tableJudge struct {
	// byInput maps a case's exact input JSON to the score the judge returns for it.
	byInput map[string]float64
}

func (j *tableJudge) Judge(_ context.Context, req evalharness.JudgeRequest) (evalharness.RawVerdict, error) {
	for input, score := range j.byInput {
		if contains(req.Prompt, input) {
			v := score
			return evalharness.RawVerdict{Score: &v, Rationale: "table"}, nil
		}
	}
	return evalharness.RawVerdict{}, fmt.Errorf("tableJudge: no table entry matches the rendered prompt")
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func containsStr(hay []string, want string) bool {
	for _, h := range hay {
		if h == want {
			return true
		}
	}
	return false
}
