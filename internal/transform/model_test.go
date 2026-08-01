package transform

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P13 §3 — model selection at the transform boundary: param tuning materializes in bound mode and is
// refused inline; a cross-provider swap is refused, an intra-provider swap applies.

// 3.5: a parameter tune (temperature/max-tokens) on a BOUND node materializes as data in the binding
// document and participates in the config hash through provider_params.
func TestParamTuneMaterializesInBoundMode(t *testing.T) {
	node := variantspec.ResolvedNode{
		NodeID:         "n_answer",
		ModelRef:       "anthropic/claude-sonnet-5",
		ProviderParams: map[string]any{"temperature": 0.2, "max_tokens": float64(512)},
	}
	resolved := &variantspec.Resolved{
		SourceRevision: "rev1", ConfigHash: "cfg13", Language: "go",
		Config:     variantspec.ResolvedConfig{IRVersion: "1.0.0", Nodes: []variantspec.ResolvedNode{node}},
		Overrides:  map[string]variantspec.ResolvedOverride{"n_answer": {ParamTune: true}},
		ApplyModes: map[string]variantspec.ApplyMode{"n_answer": variantspec.ApplyBound},
	}
	files, err := GenerateBoundArtifacts(resolved)
	if err != nil {
		t.Fatalf("GenerateBoundArtifacts: %v", err)
	}
	doc := files["agentcfg/bindings.json"]
	if doc == nil {
		t.Fatal("no binding document was generated for the bound param tune")
	}
	var parsed struct {
		Nodes map[string]struct {
			Params map[string]any `json:"params"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("binding document does not parse: %v", err)
	}
	got := parsed.Nodes["n_answer"].Params
	if got["temperature"] != 0.2 {
		t.Errorf("temperature not materialized in the binding document: %v", got)
	}
	if _, ok := got["max_tokens"]; !ok {
		t.Errorf("max_tokens not materialized in the binding document: %v", got)
	}
}

// 3.6: a parameter tune on an INLINE node is refused with a named cause and produces no diff — never
// silently dropped.
func TestInlineParamOverrideRefusedNotDropped(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	r := &variantspec.Resolved{
		ConfigHash: strings.Repeat("c", 64), SourceRevision: "rev1", Language: "go",
		Overrides: map[string]variantspec.ResolvedOverride{
			ids["classify"]: {Model: modelEntry("anthropic", "claude-sonnet-5"), ParamTune: true},
		},
		// No ApplyModes entry → inline (the default). A param tune here has no call-site rewriter.
	}
	_, err := Generate(r, root)
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("want ErrUnsafeRewrite for an inline param tune, got %v", err)
	}
	var re *RewriteError
	if !errors.As(err, &re) || re.NodeID != ids["classify"] {
		t.Errorf("the refusal must name the node, got: %v", err)
	}
	if !strings.Contains(err.Error(), "bound") {
		t.Errorf("the refusal should point at bound apply mode as the honest path, got: %v", err)
	}
}

// 3.7: a cross-provider swap at a user call site is refused with a named cause and produces no diff.
func TestCrossProviderSwapRefusedNoDiff(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	// The "classify" call site is an anthropic SDK call; selecting an openai model is a cross-provider swap.
	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["classify"]: {Model: modelEntry("openai", "gpt-5")},
	}), root)
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("want ErrUnsafeRewrite for a cross-provider swap, got %v (patch=%v)", err, p)
	}
	var re *RewriteError
	if !errors.As(err, &re) || re.Dim != "model" {
		t.Errorf("the refusal must name the model dimension, got: %v", err)
	}
	if p != nil {
		t.Error("a refused cross-provider swap must produce no patch/diff")
	}
	// Distinguishable from a nonexistent reference: it names both providers.
	if !strings.Contains(err.Error(), "openai") || !strings.Contains(err.Error(), "anthropic") {
		t.Errorf("the refusal should name both providers, got: %v", err)
	}
}

// 3.7 (mirror): an intra-provider swap applies and produces a reviewable diff.
func TestIntraProviderSwapApplies(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["classify"]: {Model: modelEntry("anthropic", "claude-haiku-4-5")},
	}), root)
	if err != nil {
		t.Fatalf("an intra-provider swap must apply, got: %v", err)
	}
	out := string(p.Files["pipeline.go"])
	if !strings.Contains(out, `"claude-haiku-4-5"`) {
		t.Errorf("the model was not rewritten within the same provider:\n%s", out)
	}
	if len(p.Diff) == 0 {
		t.Error("an applied swap must produce a reviewable diff")
	}
}
