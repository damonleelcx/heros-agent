package discovery

import (
	"bytes"
	"encoding/json"
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

func hasSuffix(s, suf string) bool { return len(s) >= len(suf) && s[len(s)-len(suf):] == suf }

// fixtureSpec mirrors one entry of testdata/fixtures/expected.json.
type fixtureSpec struct {
	Nodes  int  `json:"nodes"`
	Config bool `json:"config"`
	Golden bool `json:"golden"`
}

// loadFixtureManifest reads expected.json — the SINGLE source of truth for per-fixture expected node
// counts, shared by this test and scripts/discovery_ci.py. It is deliberately not duplicated into a
// hardcoded list here: two lists of the same fact drift, and the drift would show up as CI and the Go
// tests disagreeing about what a fixture should emit.
func loadFixtureManifest(t *testing.T) map[string]fixtureSpec {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "fixtures", "expected.json"))
	if err != nil {
		t.Fatalf("read fixture manifest: %v", err)
	}
	var doc struct {
		Fixtures map[string]fixtureSpec `json:"fixtures"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse fixture manifest: %v", err)
	}
	if len(doc.Fixtures) == 0 {
		t.Fatal("fixture manifest is empty — this test would pass vacuously")
	}
	return doc.Fixtures
}

// 6.1 — every fixture's documented expected node COUNT is matched exactly, for EVERY fixture in the
// manifest. This is auto-discovering rather than a hand-maintained case list: a hand-maintained list only
// covers the fixtures someone remembered to add to it, so a new fixture would default to UNTESTED.
func TestFixtureNodeCounts(t *testing.T) {
	for name, spec := range loadFixtureManifest(t) {
		t.Run(name, func(t *testing.T) {
			res := runFixture(t, name, spec.Config)
			if len(res.IR.Nodes) != spec.Nodes {
				t.Fatalf("%s: want %d nodes, got %d (expected.json vs emitted)", name, spec.Nodes, len(res.IR.Nodes))
			}
			if res.Report.Summary.NodesEmitted != len(res.IR.Nodes) {
				t.Fatalf("%s: report/IR node count mismatch (I4)", name)
			}
		})
	}
}

// Guard on the guard: every fixture DIRECTORY must be registered in the manifest, and every manifest entry
// must have a real directory + an EXPECTED.md. Without this, adding a fixture dir and forgetting the
// manifest row leaves it silently untested — the "whitelist only protects what someone remembered to add"
// failure. The check auto-discovers directories, so it cannot be satisfied by forgetting.
func TestEveryFixtureDirIsRegisteredAndDocumented(t *testing.T) {
	manifest := loadFixtureManifest(t)
	entries, err := os.ReadDir(filepath.Join("testdata", "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	var dirs int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirs++
		if _, ok := manifest[e.Name()]; !ok {
			t.Errorf("fixture dir %q is not registered in expected.json — it is therefore covered by NO "+
				"node-count assertion and NO CI gate", e.Name())
		}
		if !fileExists(filepath.Join("testdata", "fixtures", e.Name(), "EXPECTED.md")) {
			t.Errorf("fixture %q has no EXPECTED.md — the expected counts and their rationale must be "+
				"documented, including an explicit N/A where a fixture kind does not apply", e.Name())
		}
	}
	if dirs == 0 {
		t.Fatal("no fixture directories found — every assertion above would pass vacuously")
	}
	for name := range manifest {
		if !fileExists(filepath.Join("testdata", "fixtures", name)) {
			t.Errorf("expected.json registers %q but no such fixture dir exists", name)
		}
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

// ---------------------------------------------------------------------------------------------------
// 10.6–10.9 — per-language fixture coverage. The six fixture kinds (wrapper / framework-DAG / loop /
// malformed / dedup / golden) used to hold for GO ONLY. These drive the same claims for every language,
// so each language's EXPECTED.md is an assertion rather than prose.
// ---------------------------------------------------------------------------------------------------

// Each language's DEDUP fixture: one call site matched by BOTH registry and declaration is ONE node
// crediting both, with a merge record. Proves §3.5 merge is language-neutral, not a Go-only behaviour.
func TestDedupFixturesPerLanguage(t *testing.T) {
	for _, name := range []string{"python_dedup", "typescript_dedup", "rust_dedup", "java_dedup", "kotlin_dedup"} {
		t.Run(name, func(t *testing.T) {
			res := runFixture(t, name, true)
			if len(res.IR.Nodes) != 1 {
				t.Fatalf("%s: one call site hit by two sources must be ONE node, got %d", name, len(res.IR.Nodes))
			}
			d := res.Report.DetectionsBySource
			if d["registry"] != 1 || d["declared"] != 1 {
				t.Fatalf("%s: want registry=1 declared=1, got %+v", name, d)
			}
			if len(res.Report.DedupMerges) != 1 {
				t.Fatalf("%s: want 1 dedup_merge record, got %+v", name, res.Report.DedupMerges)
			}
		})
	}
}

// Each language's WRAPPER fixture: the in-house wrapper node exists ONLY when declared (FR2).
func TestWrapperFixturesPerLanguage(t *testing.T) {
	for _, name := range []string{"python_wrapper", "typescript_wrapper", "rust_wrapper", "java_wrapper", "kotlin_wrapper"} {
		t.Run(name, func(t *testing.T) {
			with := runFixture(t, name, true)
			if len(with.IR.Nodes) != 1 {
				t.Fatalf("%s: declared -> want 1 node, got %d", name, len(with.IR.Nodes))
			}
			if with.Report.DetectionsBySource["declared"] != 1 {
				t.Fatalf("%s: want detections_by_source.declared==1, got %+v", name, with.Report.DetectionsBySource)
			}
			without := runFixture(t, name, false)
			if len(without.IR.Nodes) != 0 {
				t.Fatalf("%s: undeclared -> the wrapper must be invisible, got %d nodes", name, len(without.IR.Nodes))
			}
		})
	}
}

// Each language's MALFORMED fixture: a broken file is REPORTED, and the good file's node survives.
//
// 🔴 This is the guard on the honesty gap the Kotlin work exposed: tree-sitter is error-tolerant, so
// before syntaxErrorDiagnostics a malformed .py/.ts/.rs/.java/.kt file was silently PARTIALLY analyzed —
// nodes in the broken region vanished and the report was clean. Delete that check and these go red.
func TestMalformedFixturesPerLanguage(t *testing.T) {
	cases := map[string]string{
		"python_malformed":     "bad.py",
		"typescript_malformed": "bad.ts",
		"rust_malformed":       "bad.rs",
		"java_malformed":       "Bad.java",
		"kotlin_malformed":     "Bad.kt",
	}
	for name, badFile := range cases {
		t.Run(name, func(t *testing.T) {
			res := runFixture(t, name, false)
			if len(res.IR.Nodes) != 1 {
				t.Fatalf("%s: the GOOD file's node must survive one broken file, got %d", name, len(res.IR.Nodes))
			}
			var sawParse bool
			for _, d := range res.Report.FileDiagnostics {
				if d.Code == CodeParseError && hasSuffix(d.File, badFile) {
					sawParse = true
					if d.Severity != SeverityWarn {
						t.Fatalf("%s: a RECOVERED tree-sitter parse must be warn-severity — error-severity "+
							"would increment summary.files_skipped and claim a skip that did not happen; got %q",
							name, d.Severity)
					}
				}
			}
			if !sawParse {
				t.Fatalf("%s: a malformed file MUST produce a PARSE_ERROR naming %s — tree-sitter recovers "+
					"silently, so without this the broken region's call sites vanish with a clean report; got %+v",
					name, badFile, res.Report.FileDiagnostics)
			}
		})
	}
}

// The TypeScript FRAMEWORK fixture: LangGraph.js is read declaratively, camelCase builder API and all.
func TestTypeScriptFrameworkFixture(t *testing.T) {
	res := runFixture(t, "typescript_framework", false)
	if len(res.Report.FrameworkSubgraphs) != 1 {
		t.Fatalf("want 1 framework subgraph, got %d", len(res.Report.FrameworkSubgraphs))
	}
	sg := res.Report.FrameworkSubgraphs[0]
	if sg.FrameworkSource != "langgraph" || !sg.Recognized {
		t.Fatalf("framework subgraph provenance wrong: %+v", sg)
	}
	wantNodes := map[string]bool{"classify": true, "answer": true, "escalate": true, "route": true}
	if len(sg.Nodes) != len(wantNodes) {
		t.Fatalf("framework nodes: want %v, got %v", keys(wantNodes), sg.Nodes)
	}
	for _, n := range sg.Nodes {
		if !wantNodes[n] {
			t.Fatalf("unexpected framework node %q — routing-map KEYS (faq/esc) are labels and must never "+
				"become nodes; got %v", n, sg.Nodes)
		}
	}
	var data, control int
	for _, e := range sg.Edges {
		switch e.Kind {
		case "data":
			data++
		case "control":
			control++
			if e.To != "answer" && e.To != "escalate" {
				t.Fatalf("control target must be a routing-map VALUE, not a label: %+v", e)
			}
		}
	}
	if data != 1 || control != 2 {
		t.Fatalf("want 1 data + 2 control edges, got %d/%d: %+v", data, control, sg.Edges)
	}
}

// Java's LOOP fixture: an enhanced-for call site is ONE static definition, variable at runtime (I2).
func TestJavaLoopFixture(t *testing.T) {
	res := runFixture(t, "java_loop", false)
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("a loop is ONE static definition, got %d nodes", len(res.IR.Nodes))
	}
	n := res.IR.Nodes[0]
	if n.InvocationSemantics.Type != "loop" || !n.InvocationSemantics.VariableAtRuntime {
		t.Fatalf("want loop/variable_at_runtime for an enhanced-for, got %+v", n.InvocationSemantics)
	}
}

// The Kotlin GOLDEN fixture's two nodes: one single + one loop, both with an honestly-unresolved model.
func TestKotlinGoldenFixture(t *testing.T) {
	res := runFixture(t, "kotlin", false)
	if res.IR.Workflow.Language != "kotlin" {
		t.Fatalf("workflow language: want kotlin, got %q", res.IR.Workflow.Language)
	}
	if len(res.IR.Nodes) != 2 {
		t.Fatalf("want 2 nodes (classify single + batch loop), got %d", len(res.IR.Nodes))
	}
	byInv := map[string]bool{}
	for _, n := range res.IR.Nodes {
		byInv[n.InvocationSemantics.Type] = true
		if n.Model.ModelID != UnresolvedSentinel {
			t.Fatalf("langchain4j binds the model at construction -> must stay the unresolved sentinel "+
				"(I5: never guess), got %q", n.Model.ModelID)
		}
	}
	if !byInv["single"] || !byInv["loop"] {
		t.Fatalf("want one single + one loop invocation, got %+v", byInv)
	}
}
