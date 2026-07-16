package discovery

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func runSample(t *testing.T) Result {
	t.Helper()
	cfg := writeTemp(t, "llm-eval.yaml", `
version: "1.0.0"
entrypoints:
  - symbol: "example.com/sample/internal/llm.Complete"
    provider: anthropic
    args:
      prompt: { index: 1 }
`)
	res, err := Run(Options{
		Repo:       "testdata/samplerepo",
		ConfigPath: cfg,
		RepoURL:    "https://github.com/example/sample",
		CommitSHA:  "abc1234",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return p
}

// The emitted IR is structurally sound: required fields populated, kind fixed, referential integrity
// holds, and nodes_emitted == len(IR.nodes) (invariant I4, CI-enforced).
func TestEmitStructural(t *testing.T) {
	res := runSample(t)
	ir := res.IR

	if ir.IRVersion != "1.0.0" || ir.Workflow.Language != "go" {
		t.Fatalf("bad envelope: %+v", ir.Workflow)
	}
	if len(ir.Nodes) != 2 {
		t.Fatalf("want 2 nodes (registry anthropic + declared wrapper), got %d", len(ir.Nodes))
	}
	if res.Report.Summary.NodesEmitted != len(ir.Nodes) {
		t.Fatalf("report nodes (%d) != IR nodes (%d) — I4 violated", res.Report.Summary.NodesEmitted, len(ir.Nodes))
	}

	ids := map[string]bool{}
	for _, n := range ir.Nodes {
		if n.Kind != "static_definition" {
			t.Fatalf("node kind must be static_definition, got %q", n.Kind)
		}
		if n.NodeID == "" || n.Model.Provider == "" || n.Model.ModelID == "" || n.ContextAssembly.Policy == "" {
			t.Fatalf("required field empty on node %+v", n)
		}
		if n.CallSite.File == "" || n.CallSite.Symbol == "" || n.CallSite.LineStart < 1 {
			t.Fatalf("bad call_site on %s: %+v", n.NodeID, n.CallSite)
		}
		if n.IOContract.InputSchema == nil || n.IOContract.OutputSchema == nil {
			t.Fatalf("io_contract stubs missing on %s", n.NodeID)
		}
		ids[n.NodeID] = true
	}
	for _, e := range ir.Edges {
		if !ids[e.FromNodeID] || !ids[e.ToNodeID] {
			t.Fatalf("edge references a node not in the set: %+v", e)
		}
	}

	// The broken file's absence is explainable from the report (I4).
	var sawBroken bool
	for _, d := range res.Report.FileDiagnostics {
		if d.Code == CodeParseError {
			sawBroken = true
		}
	}
	if !sawBroken {
		t.Fatal("report must carry the parse-error diagnostic for the skipped broken file (I4)")
	}
}

// Re-running discovery on unchanged source produces byte-identical IR (determinism / §6.7 / I3).
func TestDeterministicEmission(t *testing.T) {
	a := runSample(t)
	b := runSample(t)
	ba, err := MarshalIR(a.IR)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := MarshalIR(b.IR)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ba, bb) {
		t.Fatal("IR is not byte-stable across two runs of unchanged source (I3)")
	}
}

// The emitted IR validates against the FROZEN workflow-ir.schema.json (real schema conformance). Uses
// the repo's Python + jsonschema validator; skipped if unavailable.
func TestEmittedIRValidatesAgainstSchema(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	res := runSample(t)
	irBytes, err := MarshalIR(res.IR)
	if err != nil {
		t.Fatal(err)
	}
	irPath := filepath.Join(t.TempDir(), "ir.json")
	if err := os.WriteFile(irPath, irBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join("..", "..", "schemas", "workflow-ir.schema.json")

	script := `import json,sys,jsonschema
jsonschema.validate(json.load(open(sys.argv[1])), json.load(open(sys.argv[2])))
print("ok")`
	cmd := exec.Command(py, "-c", script, irPath, schemaPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if bytes.Contains(out, []byte("ModuleNotFoundError")) {
			t.Skip("jsonschema not installed")
		}
		t.Fatalf("emitted IR failed schema validation:\n%s", out)
	}
}
