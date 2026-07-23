package transform

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/typedcontract"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

func adaptedSpec() (*variantspec.Resolved, *variantspec.VariantSpec) {
	resolved := &variantspec.Resolved{Language: "go", SourceRevision: "rev1", ConfigHash: "cfg-hash-abc"}
	spec := &variantspec.VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev1",
		Order:            []string{"A", "adapter:rename:A->B", "B"},
		Edges:            []variantspec.Edge{},
		InsertedAdapters: []variantspec.InsertedAdapter{renameAdapter()},
	}
	return resolved, spec
}

// TASK 2.4: the same config against the same source yields a byte-identical diff.
func TestGenerateTransform_DeterministicDiff(t *testing.T) {
	resolved, spec := adaptedSpec()
	first, err := GenerateTransform(resolved, spec, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.IsEmpty() {
		t.Fatal("an adapter-augmented spec must produce a non-empty diff")
	}
	for i := 0; i < 10; i++ {
		got, err := GenerateTransform(resolved, spec, "")
		if err != nil {
			t.Fatal(err)
		}
		if got.DiffHash != first.DiffHash || string(got.Diff) != string(first.Diff) {
			t.Fatalf("non-deterministic diff across regenerations")
		}
	}
}

// TASK 2.2: the inserted adapter appears in the diff as a new, inspectable file.
func TestGenerateTransform_AdapterAppearsInDiff(t *testing.T) {
	resolved, spec := adaptedSpec()
	p, err := GenerateTransform(resolved, spec, "")
	if err != nil {
		t.Fatal(err)
	}
	diff := string(p.Diff)
	if !strings.Contains(diff, "+++ b/"+AdapterPackageDir+"/") {
		t.Fatalf("adapter must appear as a new file in the diff:\n%s", diff)
	}
	if !strings.Contains(diff, "rename") {
		t.Fatalf("diff must name the adapter kind:\n%s", diff)
	}
}

// TASK 2.7 (e2e): the generated adapter builds and, in the data path, produces output that satisfies the
// downstream consumer contract — no runtime contract halt.
func TestGenerateTransform_AdapterBuildsAndRunsNoHalt(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain unavailable")
	}
	resolved, spec := adaptedSpec()
	p, err := GenerateTransform(resolved, spec, "")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	// Write every generated file into an isolated module.
	for rel, content := range p.Files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, dir, "go.mod", "module herostest\n\ngo 1.24\n")
	// A driver that runs the adapter on the producer's output and prints the consumer-facing result.
	adapterFn := "Adapter_A_to_B_rename"
	writeFile(t, dir, "main.go", `package main

import (
	"encoding/json"
	"fmt"
	adapters "herostest/`+AdapterPackageDir+`"
)

func main() {
	producerOutput := map[string]any{"answer": "42"}
	consumerInput := adapters.`+adapterFn+`(producerOutput)
	b, _ := json.Marshal(consumerInput)
	fmt.Print(string(b))
}
`)

	// Build-preserving: it must compile.
	if out, err := runGo(dir, "build", "./..."); err != nil {
		t.Fatalf("generated adapter must build: %v\n%s", err, out)
	}
	// Run it and capture the consumer-facing output.
	out, err := runGo(dir, "run", ".")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	var value any
	if err := json.Unmarshal([]byte(out), &value); err != nil {
		t.Fatalf("adapter output is not JSON: %v (%q)", err, out)
	}
	// The consumer's contract: requires `response`. The adapter renamed answer→response, so the runtime
	// contract check passes — no halt.
	consumerSchema, err := typedcontract.CompileSchema(renameAdapter().OutSchema)
	if err != nil {
		t.Fatal(err)
	}
	if err := typedcontract.ValidateValue(consumerSchema, value); err != nil {
		t.Fatalf("adapter output must satisfy the consumer contract (no halt), got: %v (%q)", err, out)
	}
	if m, ok := value.(map[string]any); !ok || m["response"] != "42" || m["answer"] != nil {
		t.Fatalf("adapter must rename answer→response, got %v", value)
	}
}

// TASK 2.5: a codemod that does not build is rejected before proposal — modelled here by breaking the
// generated file and asserting the build gate refuses it (mapped to RejectedTransform).
func TestBuildGate_BrokenTransformRejectedBeforeProposal(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain unavailable")
	}
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module brokentest\n\ngo 1.24\n")
	// A deliberately non-compiling source (an undefined identifier), standing in for a bad codemod.
	writeFile(t, dir, "broken.go", "package main\n\nfunc main() { undefinedSymbol() }\n")

	out, err := runGo(dir, "build", "./...")
	if err == nil {
		t.Fatalf("a non-building transform must be rejected by the build gate")
	}
	rej := NewRejectedTransform(out, "", "")
	if !strings.Contains(rej.Error(), "rejected before proposal") {
		t.Fatalf("build failure must lift into a RejectedTransform: %s", rej.Error())
	}
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGo(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	b, err := cmd.CombinedOutput()
	return string(b), err
}
