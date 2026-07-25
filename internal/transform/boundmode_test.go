package transform

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P10 §7 bound apply mode tests: the hard gate (7.3), byte-identical regeneration (7.4), one-change
// bundling / single revert (7.5), and the data/structure line (7.6).

func boundResolved(t *testing.T, withValues bool) *variantspec.Resolved {
	t.Helper()
	tmpl, err := registry.ParseTemplate("Triage {{ticket}} tier {{tier}} region {{region}}")
	if err != nil {
		t.Fatal(err)
	}
	node := variantspec.ResolvedNode{
		NodeID: "n_triage", ModelRef: "anthropic/claude-sonnet-5",
		ProviderParams: map[string]any{"max_tokens": 1024},
	}
	ov := variantspec.ResolvedOverride{}
	if withValues {
		node.Bindings = map[string]variantspec.ResolvedBinding{
			"tier":   {Kind: "literal", Value: "gold"},
			"region": {Kind: "env", Value: "AWS_REGION"},
			"ticket": {Kind: "expr", Value: "ticket"}, // call-site structure — must NOT enter the document
		}
		ov.Prompt = &registry.PromptEntry{VersionID: "p1", Name: "triage", Template: tmpl}
	} else {
		// A bound node with an indirection but nothing to put behind it.
		node.ModelRef = ""
		node.ProviderParams = map[string]any{}
	}
	return &variantspec.Resolved{
		SourceRevision: "rev1", ConfigHash: "abc123", Language: "go",
		Config:     variantspec.ResolvedConfig{IRVersion: "1.0.0", Nodes: []variantspec.ResolvedNode{node}},
		Overrides:  map[string]variantspec.ResolvedOverride{"n_triage": ov},
		ApplyModes: map[string]variantspec.ApplyMode{"n_triage": variantspec.ApplyBound},
	}
}

func TestBound_RejectsIndirectionWithoutResolvedValues(t *testing.T) {
	_, err := GenerateBoundArtifacts(boundResolved(t, false))
	var gate *ErrBoundWithoutValues
	if !errors.As(err, &gate) {
		t.Fatalf("a bound node with no values must be rejected by the hard gate, got %v", err)
	}
	if gate.NodeID != "n_triage" {
		t.Fatalf("the rejection must name the node, got %q", gate.NodeID)
	}
}

func TestBound_RegenerationIsByteIdentical(t *testing.T) {
	first, err := GenerateBoundArtifacts(boundResolved(t, true))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := GenerateBoundArtifacts(boundResolved(t, true))
		if err != nil {
			t.Fatal(err)
		}
		for path, content := range first {
			if !bytes.Equal(content, again[path]) {
				t.Fatalf("regeneration of %s is not byte-identical", path)
			}
		}
	}
}

func TestBound_DataStructureLine_LiteralAndEnvInDocument_ExprNot(t *testing.T) {
	files, err := GenerateBoundArtifacts(boundResolved(t, true))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(files[bindingDocPath])
	// literal and env bindings, the model and the prompt template are DATA — they live in the document.
	for _, want := range []string{"literal_bindings", "gold", "env_bindings", "AWS_REGION", "claude-sonnet-5", "prompt_template"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("binding document is missing runtime-changeable data %q:\n%s", want, doc)
		}
	}
	// The expr binding value "ticket" is call-site STRUCTURE — it must NOT be written into the document
	// under a binding key. (It legitimately appears inside the prompt_template as the slot text, which is
	// fine; what must be absent is an expr entry in literal_bindings/env_bindings.)
	if strings.Contains(doc, `"ticket":"ticket"`) || strings.Contains(doc, `"ticket": "ticket"`) {
		t.Fatalf("an expr binding leaked into the binding document (it is call-site structure):\n%s", doc)
	}
}

func TestBound_AccessorIsDependencyFreeAndPerNode(t *testing.T) {
	files, err := GenerateBoundArtifacts(boundResolved(t, true))
	if err != nil {
		t.Fatal(err)
	}
	src := string(files[agentcfgFilePath])
	// Standard library only — no external import path (anything with a dot in the import).
	if strings.Contains(src, "github.com/") || strings.Contains(src, "golang.org/") {
		t.Fatalf("the generated accessor must be dependency-free:\n%s", src)
	}
	// A per-node accessor function, so referencing an absent node is a compile error, not a map miss.
	if !strings.Contains(src, "func Node_n_triage() NodeConfig") {
		t.Fatalf("expected a per-node accessor function:\n%s", src)
	}
}

// Task 7.2 / 7.5: the whole change — call-site rewrite (inline base), the accessor, and the document —
// is ONE Patch, so a single revert restores everything. Here we assert the two bound artifacts land in
// the same Patch as the (empty, in this fixture) base and are diffed together.
func TestBound_ArtifactsShipInOnePatch(t *testing.T) {
	// A bound node whose values come from the resolved config (model_id) with no inline call-site
	// rewrite to perform, so the bundling of the two generated artifacts is what is under test.
	resolved := &variantspec.Resolved{
		SourceRevision: "rev1", ConfigHash: "abc123", Language: "go",
		Config: variantspec.ResolvedConfig{IRVersion: "1.0.0", Nodes: []variantspec.ResolvedNode{
			{NodeID: "n_triage", ModelRef: "anthropic/claude-sonnet-5", ProviderParams: map[string]any{"max_tokens": 1024}},
		}},
		Overrides:  map[string]variantspec.ResolvedOverride{},
		ApplyModes: map[string]variantspec.ApplyMode{"n_triage": variantspec.ApplyBound},
	}
	spec := &variantspec.VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev1", Order: []string{"n_triage"},
		Nodes: map[string]variantspec.NodeOverride{"n_triage": {ApplyMode: variantspec.ApplyBound}},
	}
	patch, err := GenerateTransform(resolved, spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := patch.Files[bindingDocPath]; !ok {
		t.Fatal("the binding document must be in the patch")
	}
	if _, ok := patch.Files[agentcfgFilePath]; !ok {
		t.Fatal("the generated accessor must be in the patch")
	}
	if !bytes.Contains(patch.Diff, []byte(bindingDocPath)) || !bytes.Contains(patch.Diff, []byte(agentcfgFilePath)) {
		t.Fatal("both bound artifacts must appear in the single unified diff (one revert restores all)")
	}
	if patch.DiffHash == "" {
		t.Fatal("the combined patch must carry one content hash over the whole change")
	}
}
