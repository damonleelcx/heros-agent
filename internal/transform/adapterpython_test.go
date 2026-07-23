package transform

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// TASK 2.7 (Python): the generated Python adapter runs and, in the data path, produces output that
// satisfies the downstream consumer — the same e2e guarantee proven for Go, now for the large Python
// share of real agents (hermes-agent among them).
func TestGenerateTransform_PythonAdapterRunsNoHalt(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	resolved := &variantspec.Resolved{Language: "python", SourceRevision: "rev1", ConfigHash: "cfg"}
	spec := &variantspec.VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev1",
		InsertedAdapters: []variantspec.InsertedAdapter{renameAdapter()},
	}
	p, err := GenerateTransform(resolved, spec, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(p.Diff), ".py") {
		t.Fatalf("python transform must emit a .py adapter:\n%s", p.Diff)
	}

	dir := t.TempDir()
	for rel, content := range p.Files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A driver that imports the generated adapter and applies it to the producer's output.
	writeFile(t, dir, "heros_adapters/__init__.py", "")
	writeFile(t, dir, "driver.py", `import json
from heros_adapters.adapter_A_B_rename import adapter_A_to_B_rename
producer_output = {"answer": "42"}
print(json.dumps(adapter_A_to_B_rename(producer_output)))
`)

	cmd := exec.Command(py, "driver.py")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python adapter must run: %v\n%s", err, out)
	}
	var value map[string]any
	if err := json.Unmarshal(out, &value); err != nil {
		t.Fatalf("adapter output not JSON: %v (%q)", err, out)
	}
	// The consumer requires `response`; the adapter renamed answer→response → satisfied, no halt.
	if value["response"] != "42" || value["answer"] != nil {
		t.Fatalf("python adapter must rename answer→response, got %v", value)
	}
}
