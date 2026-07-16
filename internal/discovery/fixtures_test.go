package discovery

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// runFixture runs discovery over a fixture repo with deterministic workflow identity. If the fixture
// dir contains an llm-eval.yaml it is used unless useConfig is false.
func runFixture(t *testing.T, name string, useConfig bool) Result {
	t.Helper()
	dir := filepath.Join("testdata", "fixtures", name)
	cfg := ""
	if useConfig {
		if p := filepath.Join(dir, "llm-eval.yaml"); fileExists(p) {
			cfg = p
		}
	}
	res, err := Run(Options{
		Repo:       dir,
		ConfigPath: cfg,
		RepoURL:    "local://" + name,
		CommitSHA:  "0000000",
	})
	if err != nil {
		t.Fatalf("Run(%s): %v", name, err)
	}
	return res
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func nodeBy(t *testing.T, ir IR, symbolSuffix string) IRNode {
	t.Helper()
	for _, n := range ir.Nodes {
		if hasSuffix(n.CallSite.Symbol, symbolSuffix) {
			return n
		}
	}
	t.Fatalf("no node with enclosing symbol %q in %d nodes", symbolSuffix, len(ir.Nodes))
	return IRNode{}
}

func hasSuffix(s, suf string) bool { return len(s) >= len(suf) && s[len(s)-len(suf):] == suf }

// 6.1 — documented expected node COUNT is matched exactly across the fixtures.
func TestFixtureNodeCounts(t *testing.T) {
	cases := []struct {
		name      string
		useConfig bool
		want      int
	}{
		{"golden", true, 3},
		{"wrapper", true, 1},
		{"wrapper", false, 0}, // 6.2: no declaration -> the hidden node disappears
		{"loop", true, 1},
		{"malformed", true, 1},
		{"dedup", true, 1},
		{"framework", true, 0}, // framework builder fns are nil -> no LLM call-site nodes (subgraph is in the report)
	}
	for _, tc := range cases {
		label := tc.name
		if !tc.useConfig {
			label += "-noconfig"
		}
		t.Run(label, func(t *testing.T) {
			res := runFixture(t, tc.name, tc.useConfig)
			if len(res.IR.Nodes) != tc.want {
				t.Fatalf("%s: want %d nodes, got %d", label, tc.want, len(res.IR.Nodes))
			}
			if res.Report.Summary.NodesEmitted != len(res.IR.Nodes) {
				t.Fatalf("%s: report/IR node count mismatch (I4)", label)
			}
		})
	}
}

// 6.2 — the wrapper node exists ONLY when declared (proves user-declared entrypoints, FR2).
func TestWrapperFixture(t *testing.T) {
	with := runFixture(t, "wrapper", true)
	if len(with.IR.Nodes) != 1 {
		t.Fatalf("declared: want 1 node, got %d", len(with.IR.Nodes))
	}
	n := with.IR.Nodes[0]
	if n.Prompt.Inline != "summarize the ticket" {
		t.Fatalf("declared wrapper prompt: want 'summarize the ticket', got %q", n.Prompt.Inline)
	}
	if with.Report.DetectionsBySource["declared"] != 1 {
		t.Fatalf("want detections_by_source.declared==1, got %+v", with.Report.DetectionsBySource)
	}

	without := runFixture(t, "wrapper", false)
	if len(without.IR.Nodes) != 0 {
		t.Fatalf("no declaration: want 0 nodes, got %d", len(without.IR.Nodes))
	}
}

// 6.3 — the framework DAG is read declaratively and tagged with framework_source.
func TestFrameworkFixture(t *testing.T) {
	res := runFixture(t, "framework", true)
	if len(res.Report.FrameworkSubgraphs) != 1 {
		t.Fatalf("want 1 framework subgraph, got %d", len(res.Report.FrameworkSubgraphs))
	}
	sg := res.Report.FrameworkSubgraphs[0]
	if sg.FrameworkSource != "go-graph-builder" || !sg.Recognized {
		t.Fatalf("framework subgraph provenance wrong: %+v", sg)
	}
	wantNodes := map[string]bool{"classify": true, "answer": true, "escalate": true, "route": true}
	if len(sg.Nodes) != len(wantNodes) {
		t.Fatalf("framework nodes: want %v, got %v", keys(wantNodes), sg.Nodes)
	}
	var data, control int
	for _, e := range sg.Edges {
		switch e.Kind {
		case "data":
			data++
		case "control":
			control++
			if e.To != "answer" && e.To != "escalate" {
				t.Fatalf("control target must be a routing-map value, got %+v", e)
			}
		}
	}
	if data != 1 || control != 2 {
		t.Fatalf("want 1 data + 2 control edges, got %d/%d: %+v", data, control, sg.Edges)
	}
}

// 6.4 — the loop node is variable_at_runtime and NO fixed runtime count is emitted (I2).
func TestLoopFixture(t *testing.T) {
	res := runFixture(t, "loop", true)
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("want 1 node (loop is one static definition), got %d", len(res.IR.Nodes))
	}
	n := res.IR.Nodes[0]
	if n.InvocationSemantics.Type != "loop" || !n.InvocationSemantics.VariableAtRuntime {
		t.Fatalf("want loop/variable_at_runtime, got %+v", n.InvocationSemantics)
	}
}

// 6.5 — the broken file is skip-and-reported; the good file's node is still discovered; no crash.
func TestMalformedFixture(t *testing.T) {
	res := runFixture(t, "malformed", true)
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("want 1 node (good.go still discovered), got %d", len(res.IR.Nodes))
	}
	if hasSuffix(res.IR.Nodes[0].CallSite.Symbol, "good") == false {
		t.Fatalf("expected the Converse node in good(), got %q", res.IR.Nodes[0].CallSite.Symbol)
	}
	var sawParse bool
	for _, d := range res.Report.FileDiagnostics {
		if d.Code == CodeParseError && hasSuffix(d.File, "bad.go") {
			sawParse = true
		}
	}
	if !sawParse {
		t.Fatalf("want PARSE_ERROR for bad.go, got %+v", res.Report.FileDiagnostics)
	}
	if res.Report.Summary.FilesSkipped < 1 {
		t.Fatalf("want files_skipped>=1, got %d", res.Report.Summary.FilesSkipped)
	}
}

// 6.6 — a call site hit by BOTH sources is one node crediting both.
func TestDedupFixture(t *testing.T) {
	res := runFixture(t, "dedup", true)
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(res.IR.Nodes))
	}
	d := res.Report.DetectionsBySource
	if d["registry"] != 1 || d["declared"] != 1 {
		t.Fatalf("want registry=1 declared=1, got %+v", d)
	}
	if len(res.Report.DedupMerges) != 1 {
		t.Fatalf("want 1 dedup_merge record, got %+v", res.Report.DedupMerges)
	}
}

// 6.7 — golden-IR diff: emitted IR matches the committed golden byte-for-byte (determinism + regression).
func TestGoldenIR(t *testing.T) {
	res := runFixture(t, "golden", true)
	got, err := MarshalIR(res.IR)
	if err != nil {
		t.Fatal(err)
	}
	goldenPath := filepath.Join("testdata", "fixtures", "golden", "expected-ir.json")

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("golden updated")
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with UPDATE_GOLDEN=1 to create): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("emitted IR differs from golden expected-ir.json.\n--- got ---\n%s", got)
	}
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
