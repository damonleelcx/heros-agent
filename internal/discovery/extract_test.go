package discovery

import (
	"strings"
	"testing"
)

// extractNodes runs detection + extraction on one source file (test helper).
func extractNodes(t *testing.T, reg *Registry, decl *declaredIndex, src string) []ExtractedNode {
	t.Helper()
	pf, err := parseSingle("github.com/acme/app/internal/svc", "svc.go", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg := &Package{PkgPath: pf.PkgPath, Files: []*ParsedFile{pf}}
	sites, _ := DetectPackage(pkg, reg, decl)
	merged, _ := Merge(sites)
	return ExtractFile(pf, merged)
}

func ambCodes(n ExtractedNode) []string {
	var out []string
	for _, a := range n.Ambiguities {
		out = append(out, a.Code)
	}
	return out
}

func hasCode(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

// A resolvable model constant + inline prompt are extracted; no ambiguity flags (FR3).
func TestExtractResolvedModelAndPrompt(t *testing.T) {
	reg := mustRegistry(t)
	// A declared wrapper with a positional string prompt and provider hint.
	cfg, _ := LoadLLMEval([]byte(`
version: "1.0.0"
entrypoints:
  - symbol: "github.com/acme/app/internal/svc.complete"
    provider: anthropic
    args:
      model: { name: "modelID" }
      prompt: { index: 1 }
`))
	decl := newDeclaredIndex(cfg)
	src := `package svc
func complete(ctx any, prompt string) {}
func run() { complete(nil, "summarize the ticket") }`
	nodes := extractNodes(t, reg, decl, src)
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	n := nodes[0]
	if n.Prompt.Unresolved || n.Prompt.Inline != "summarize the ticket" {
		t.Fatalf("prompt not resolved: %+v", n.Prompt)
	}
	if n.Model.Provider != "anthropic" {
		t.Fatalf("provider: want anthropic, got %q", n.Model.Provider)
	}
	// model locator is ParamName which we cannot resolve without types, and no model arg is present at
	// the call -> unresolved + a model ambiguity flag (honest, never guessed).
	if !n.Model.Unresolved {
		t.Fatalf("param-name model should be unresolved, got %+v", n.Model)
	}
	if !hasCode(ambCodes(n), CodeModelUnresolved) && !hasCode(ambCodes(n), CodeModelConstructionBound) {
		t.Fatalf("unresolved model should carry a model ambiguity flag, got %v", ambCodes(n))
	}
}

// The anthropic SDK call: model is a package-qualified constant (symbolic), prompt is a constructed
// message list -> unresolved + PROMPT_CONSTRUCTED flag, never guessed (FR3/FR8, I5).
func TestExtractAnthropicConstructedPrompt(t *testing.T) {
	reg := mustRegistry(t)
	src := `package svc
import "github.com/anthropics/anthropic-sdk-go"
func run(client *anthropic.Client) {
	client.Messages.New(nil, anthropic.MessageNewParams{
		Model:    anthropic.ModelClaudeOpus4_6,
		Messages: []anthropic.MessageParam{},
	})
}`
	nodes := extractNodes(t, reg, nil, src)
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	n := nodes[0]
	if n.Model.Unresolved || !strings.Contains(n.Model.ModelID, "ModelClaudeOpus4_6") {
		t.Fatalf("model should resolve to the symbolic constant, got %+v", n.Model)
	}
	if n.Model.Provider != "anthropic" {
		t.Fatalf("provider: want anthropic, got %q", n.Model.Provider)
	}
	if !n.Prompt.Unresolved || !hasCode(ambCodes(n), CodePromptConstructed) {
		t.Fatalf("constructed prompt should be unresolved+flagged, got %+v / %v", n.Prompt, ambCodes(n))
	}
}

// Bedrock InvokeModel: the opaque Body means prompt is unresolved with the opaque-body reason (I5).
func TestExtractBedrockOpaqueBody(t *testing.T) {
	reg := mustRegistry(t)
	src := `package svc
import "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
func run(client *bedrockruntime.Client) {
	client.InvokeModel(nil, &bedrockruntime.InvokeModelInput{ModelId: aws.String("anthropic.claude")})
}`
	nodes := extractNodes(t, reg, nil, src)
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	n := nodes[0]
	if !n.Prompt.Unresolved || !hasCode(ambCodes(n), CodePromptOpaqueBody) {
		t.Fatalf("opaque body prompt should be flagged PROMPT_OPAQUE_BODY, got %v", ambCodes(n))
	}
	// ModelId is still readable through the aws.String wrapper.
	if n.Model.Unresolved || !strings.Contains(n.Model.ModelID, "anthropic.claude") {
		t.Fatalf("model should unwrap aws.String, got %+v", n.Model)
	}
}

// §4.5: a call inside a loop is variable_at_runtime; a plain call is single; NO fixed count emitted (I2).
func TestInvocationSemantics(t *testing.T) {
	reg := mustRegistry(t)
	loopSrc := `package svc
import "github.com/anthropics/anthropic-sdk-go"
func run(client *anthropic.Client, xs []int) {
	for range xs {
		client.Messages.New(nil, anthropic.MessageNewParams{})
	}
}`
	nodes := extractNodes(t, reg, nil, loopSrc)
	if len(nodes) != 1 || nodes[0].Invocation.Type != "loop" || !nodes[0].Invocation.VariableAtRuntime {
		t.Fatalf("loop call should be variable_at_runtime loop, got %+v", nodes[0].Invocation)
	}

	singleSrc := `package svc
import "github.com/anthropics/anthropic-sdk-go"
func run(client *anthropic.Client) { client.Messages.New(nil, anthropic.MessageNewParams{}) }`
	nodes = extractNodes(t, reg, nil, singleSrc)
	if nodes[0].Invocation.Type != "single" || nodes[0].Invocation.VariableAtRuntime {
		t.Fatalf("plain call should be single/false, got %+v", nodes[0].Invocation)
	}
}

// §4.3: node A's result feeding node B's args produces an A→B data edge.
func TestDataEdge(t *testing.T) {
	reg := mustRegistry(t)
	cfg, _ := LoadLLMEval([]byte(`
version: "1.0.0"
entrypoints:
  - symbol: "github.com/acme/app/internal/svc.classify"
    args: { prompt: { index: 0 } }
  - symbol: "github.com/acme/app/internal/svc.answer"
    args: { prompt: { index: 0 } }
`))
	decl := newDeclaredIndex(cfg)
	src := `package svc
func classify(in string) string { return "" }
func answer(in string) string { return "" }
func run() {
	label := classify("ticket")
	_ = answer(label)
}`
	pf, _ := parseSingle("github.com/acme/app/internal/svc", "svc.go", src)
	pkg := &Package{PkgPath: pf.PkgPath, Files: []*ParsedFile{pf}}
	sites, _ := DetectPackage(pkg, reg, decl)
	merged, _ := Merge(sites)
	g := BuildGraph([]*ParsedFile{pf}, merged)
	if len(g.Nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(g.Nodes))
	}
	var dataEdges int
	for _, e := range g.Edges {
		if e.Kind == "data" {
			dataEdges++
		}
	}
	if dataEdges != 1 {
		t.Fatalf("want exactly 1 data edge (classify->answer), got %d: %+v", dataEdges, g.Edges)
	}
}
