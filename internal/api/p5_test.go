package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/discovery"
)

// stubP5 serves one fixed IR: A produces `answer`, B requires `response`, C requires `summary`.
type stubP5 struct{ ir *discovery.IR }

func (s stubP5) IR(id string) (*discovery.IR, bool) {
	if id != "wf" {
		return nil, false
	}
	return s.ir, true
}

func fixtureIR() *discovery.IR {
	obj := func(props map[string]any, req ...string) map[string]any {
		r := make([]any, len(req))
		for i, x := range req {
			r[i] = x
		}
		m := map[string]any{"type": "object", "properties": props}
		if len(r) > 0 {
			m["required"] = r
		}
		return m
	}
	str := map[string]any{"type": "string"}
	return &discovery.IR{
		IRVersion: discovery.IRVersion, Workflow: discovery.IRWorkflow{ID: "wf", Language: "go"},
		Nodes: []discovery.IRNode{
			{NodeID: "A", Kind: "static_definition", IOContract: discovery.IRIOContract{
				InputSchema: map[string]any{"type": "object"}, OutputSchema: obj(map[string]any{"answer": str})}},
			{NodeID: "B", Kind: "static_definition", IOContract: discovery.IRIOContract{
				InputSchema: obj(map[string]any{"response": str}, "response"), OutputSchema: obj(map[string]any{"summary": str})}},
			{NodeID: "C", Kind: "static_definition", IOContract: discovery.IRIOContract{
				InputSchema: obj(map[string]any{"summary": str}, "summary"), OutputSchema: map[string]any{"type": "object"}}},
		},
		Edges: []discovery.IREdge{
			{FromNodeID: "A", ToNodeID: "B", Kind: "data"},
			{FromNodeID: "B", ToNodeID: "C", Kind: "data"},
		},
	}
}

func newP5Server(t *testing.T) *Server {
	t.Helper()
	s := New(nil, config.Config{})
	s.MountP5(stubP5{ir: fixtureIR()})
	return s
}

func post(t *testing.T, s *Server, path, body string) (*httptest.ResponseRecorder, validateResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	var v validateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	return rec, v
}

func TestP5IR_ServesReadModel(t *testing.T) {
	s := newP5Server(t)
	req := httptest.NewRequest(http.MethodGet, "/api/p5/workflows/wf/ir", nil)
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var v irView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Nodes) != 3 || v.Language != "go" {
		t.Fatalf("unexpected IR view: %+v", v)
	}
}

func TestP5IR_UnknownWorkflow404(t *testing.T) {
	s := newP5Server(t)
	req := httptest.NewRequest(http.MethodGet, "/api/p5/workflows/nope/ir", nil)
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

// The happy path: the discovered order is coherent.
func TestP5Validate_Coherent(t *testing.T) {
	s := newP5Server(t)
	_, v := post(t, s, "/api/p5/workflows/wf/validate",
		`{"order":["A","B","C"],"edges":[{"from_node_id":"A","to_node_id":"B","kind":"data"},{"from_node_id":"B","to_node_id":"C","kind":"data"}]}`)
	if v.Kind != "adapted" {
		// A→B is answer vs response → adapter; B→C is summary→summary coherent. So the whole is adapted.
		t.Fatalf("A→B needs a rename adapter; expected adapted, got %q (%+v)", v.Kind, v)
	}
	if len(v.Adapters) != 1 || v.Adapters[0].Kind != "rename" {
		t.Fatalf("want one rename adapter, got %+v", v.Adapters)
	}
	// The adapter carries a previewed source diff that names the file.
	if v.Adapters[0].PreviewDiff == "" || !strings.Contains(v.Adapters[0].PreviewDiff, "response") {
		t.Fatalf("adapter must preview a source diff:\n%s", v.Adapters[0].PreviewDiff)
	}
}

// The unhappy path: consumer before producer → rejected, edge-anchored diagnostic, commit blocked.
func TestP5Validate_RejectedEdgeAnchored(t *testing.T) {
	s := newP5Server(t)
	// Put C before B: C requires `summary` produced by B → missing producer.
	_, v := post(t, s, "/api/p5/workflows/wf/validate",
		`{"order":["A","C","B"],"edges":[{"from_node_id":"B","to_node_id":"C","kind":"data"}]}`)
	if v.Kind != "rejected" {
		t.Fatalf("want rejected, got %q (%+v)", v.Kind, v)
	}
	d := v.Diagnostics[0]
	if d.FromNodeID != "B" || d.ToNodeID != "C" {
		t.Fatalf("diagnostic must anchor to the offending edge B→C, got %+v", d)
	}
	found := false
	for _, f := range d.Fields {
		if f == "summary" {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostic must name the field summary, got %+v", d.Fields)
	}
	if v.Announcement == "" || !strings.Contains(strings.ToLower(v.Announcement), "blocked") {
		t.Fatalf("rejected verdict must announce the commit is blocked: %q", v.Announcement)
	}
}

// TASK 3.6: a committed coherent/adapted edit produces a lineage-tracked spec + a reviewable diff.
func TestP5Commit_AdaptedProducesLineageAndDiff(t *testing.T) {
	s := newP5Server(t)
	req := httptest.NewRequest(http.MethodPost, "/api/p5/workflows/wf/commit",
		strings.NewReader(`{"parent_variant_id":"wf:root","order":["A","B","C"],"edges":[{"from_node_id":"A","to_node_id":"B","kind":"data"},{"from_node_id":"B","to_node_id":"C","kind":"data"}]}`))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	var c commitResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	if c.Status != "committed" {
		t.Fatalf("want committed, got %q (%s)", c.Status, rec.Body.String())
	}
	if c.ParentID != "wf:root" {
		t.Fatalf("committed spec must carry parent lineage, got %q", c.ParentID)
	}
	if c.Diff == "" || c.DiffHash == "" || !strings.Contains(c.Diff, "heros_adapters/") {
		t.Fatalf("committed edit must produce a reviewable adapter diff, got %q", c.Diff)
	}
	if len(c.Adapters) != 1 {
		t.Fatalf("committed adapted spec must record the inserted adapter, got %+v", c.Adapters)
	}
}

// TASK 3.2: a rejected edit produces NO source diff.
func TestP5Commit_RejectedGeneratesNoDiff(t *testing.T) {
	s := newP5Server(t)
	req := httptest.NewRequest(http.MethodPost, "/api/p5/workflows/wf/commit",
		strings.NewReader(`{"parent_variant_id":"wf:root","order":["A","C","B"],"edges":[{"from_node_id":"B","to_node_id":"C","kind":"data"}]}`))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	var c commitResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	if c.Status != "rejected" {
		t.Fatalf("want rejected, got %q", c.Status)
	}
	if c.Diff != "" {
		t.Fatalf("a rejected reorder must generate no source diff, got %q", c.Diff)
	}
	if len(c.Diagnostics) == 0 {
		t.Fatalf("rejected commit must carry diagnostics")
	}
}

// TASK 3.5: incremental per-edge validation stays responsive on a large IR (target < 200 ms).
func TestP5Validate_LargeIRResponsive(t *testing.T) {
	// A 400-node chain: node_i produces field f_i, node_{i+1} requires f_i (coherent).
	obj := func(props map[string]any, req ...string) map[string]any {
		r := make([]any, len(req))
		for i, x := range req {
			r[i] = x
		}
		m := map[string]any{"type": "object", "properties": props}
		if len(r) > 0 {
			m["required"] = r
		}
		return m
	}
	str := map[string]any{"type": "string"}
	const N = 400
	ir := &discovery.IR{IRVersion: discovery.IRVersion, Workflow: discovery.IRWorkflow{ID: "big", Language: "go"}}
	var order []string
	var edges []string
	for i := 0; i < N; i++ {
		id := fmt.Sprintf("n%03d", i)
		order = append(order, id)
		in := map[string]any{"type": "object"}
		var req []string
		if i > 0 {
			in = obj(map[string]any{fmt.Sprintf("f%03d", i-1): str}, fmt.Sprintf("f%03d", i-1))
			_ = req
		}
		out := obj(map[string]any{fmt.Sprintf("f%03d", i): str})
		ir.Nodes = append(ir.Nodes, discovery.IRNode{NodeID: id, Kind: "static_definition",
			IOContract: discovery.IRIOContract{InputSchema: in, OutputSchema: out}})
		if i > 0 {
			edges = append(edges, fmt.Sprintf(`{"from_node_id":"n%03d","to_node_id":"n%03d","kind":"data"}`, i-1, i))
		}
	}
	s := New(nil, config.Config{})
	s.MountP5(bigSource{ir: ir})

	body := fmt.Sprintf(`{"order":["%s"],"edges":[%s]}`, strings.Join(order, `","`), strings.Join(edges, ","))
	start := time.Now()
	req := httptest.NewRequest(http.MethodPost, "/api/p5/workflows/big/validate", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	elapsed := time.Since(start)
	var v validateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.Kind != "coherent" {
		t.Fatalf("the chain is coherent, got %q", v.Kind)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("validation of a %d-node IR took %v, want < 200ms", N, elapsed)
	}
}

// The orderings endpoint ranks all arrangements: approved first, rejected below, with truncation
// metadata surfaced.
func TestP5Orderings_RankedApprovedFirst(t *testing.T) {
	s := newP5Server(t)
	// A→B→C: A produces answer (B needs response → adapter), B produces summary (C needs summary).
	req := httptest.NewRequest(http.MethodPost, "/api/p5/workflows/wf/orderings",
		strings.NewReader(`{"order":["A","B","C"],"edges":[{"from_node_id":"A","to_node_id":"B","kind":"data"},{"from_node_id":"B","to_node_id":"C","kind":"data"}]}`))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var rank struct {
		Arrangements []struct {
			Order    []string `json:"order"`
			Kind     string   `json:"kind"`
			Score    float64  `json:"score"`
			Approved bool     `json:"approved"`
		} `json:"arrangements"`
		Total         int64 `json:"total"`
		Considered    int   `json:"considered"`
		ApprovedCount int   `json:"approved_count"`
		RejectedCount int   `json:"rejected_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rank); err != nil {
		t.Fatal(err)
	}
	if rank.Total != 6 || rank.Considered != 6 {
		t.Fatalf("3 nodes → 6 orderings, got total=%d considered=%d", rank.Total, rank.Considered)
	}
	if rank.ApprovedCount < 1 {
		t.Fatalf("at least the topological order must be approved, got %d", rank.ApprovedCount)
	}
	// Approved arrangements come before rejected ones, and scores are non-increasing.
	seenRejected := false
	last := 2.0
	for _, a := range rank.Arrangements {
		if !a.Approved {
			seenRejected = true
		} else if seenRejected {
			t.Fatalf("an approved arrangement ranked below a rejected one: %+v", a)
		}
		if a.Score > last+1e-9 {
			t.Fatalf("arrangements must be sorted by score descending")
		}
		last = a.Score
	}
	if rank.Arrangements[0].Kind == "rejected" {
		t.Fatalf("the top arrangement must be approved, got %q", rank.Arrangements[0].Kind)
	}
}

// The streaming endpoint emits NDJSON: a meta line, one arrangement line per ordering (as discovered),
// then a done line with the final summary.
func TestP5OrderingsStream_NDJSON(t *testing.T) {
	s := newP5Server(t)
	req := httptest.NewRequest(http.MethodPost, "/api/p5/workflows/wf/orderings/stream",
		strings.NewReader(`{"order":["A","B","C"],"edges":[{"from_node_id":"A","to_node_id":"B","kind":"data"},{"from_node_id":"B","to_node_id":"C","kind":"data"}]}`))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("want ndjson content-type, got %q", ct)
	}
	var meta, done map[string]any
	arrangementLines := 0
	for _, line := range strings.Split(strings.TrimSpace(rec.Body.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("each line must be JSON: %v (%q)", err, line)
		}
		switch m["type"] {
		case "meta":
			meta = m
		case "arrangement":
			arrangementLines++
		case "done":
			done = m
		}
	}
	if meta == nil || done == nil {
		t.Fatalf("stream must start with meta and end with done; meta=%v done=%v", meta, done)
	}
	if meta["total"].(float64) != 6 {
		t.Fatalf("meta.total for 3 nodes must be 6, got %v", meta["total"])
	}
	if arrangementLines != 6 {
		t.Fatalf("must stream one line per ordering (6), got %d", arrangementLines)
	}
	if done["considered"].(float64) != 6 {
		t.Fatalf("done.considered must be 6, got %v", done["considered"])
	}
	if done["approved_count"].(float64) < 1 {
		t.Fatalf("at least one approved arrangement expected")
	}
}

type bigSource struct{ ir *discovery.IR }

func (b bigSource) IR(id string) (*discovery.IR, bool) {
	if id != "big" {
		return nil, false
	}
	return b.ir, true
}

// P15 task 4.3 — the P5 commit path's wiring outcome, corrected.
//
// The first version of this test asserted that ANY reorder commits as `rejected_transform`. That was
// the over-broad gate CI caught: the engine was comparing the spec's Order against the IR's
// node-EMISSION order and calling every difference a rearrangement.
//
// The truthful rule: the source states a relative order between two calls only when it ORDERS them —
// consecutive sibling statements in one block. This stub IR has no tree behind it at all, so nothing
// states an order and a reorder is a DECLARATION, not a rewire. What is still refused here is a spec
// whose node SET differs from the source's: the call the spec dropped is demonstrably still there, so
// its config_hash would be scored against a program that runs one more node than the graph records.
func TestP5Commit_WiringOutcomes(t *testing.T) {
	s := newP5Server(t)
	commit := func(body string) commitResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/p5/workflows/wf/commit", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.Mux.ServeHTTP(rec, req)
		var c commitResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body.String())
		}
		return c
	}

	// A reorder of calls nothing orders: committed, because there is no source order to contradict.
	if c := commit(`{"parent_variant_id":"wf:root","order":["C","B","A"],"edges":[]}`); c.Status != "committed" {
		t.Fatalf("an ordering the source does not state must not be refused, got %q (%s)", c.Status, c.BuildError)
	}

	// A PRUNE: the spec drops node C. Still refused, still naming the wiring axis, still no diff.
	c := commit(`{"parent_variant_id":"wf:root","order":["A","B"],"edges":[]}`)
	if c.Status != "rejected_transform" {
		t.Fatalf("dropping a node the source still contains must be refused, got %q", c.Status)
	}
	if !strings.Contains(c.BuildError, "wiring") {
		t.Fatalf("the refusal must name the wiring axis, got %q", c.BuildError)
	}
	if c.Diff != "" || c.DiffHash != "" {
		t.Fatalf("a refused commit must produce no diff, got %q", c.Diff)
	}
}
