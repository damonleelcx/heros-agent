package discovery

import (
	"sort"
	"testing"
)

func mustRegistry(t *testing.T) *Registry {
	t.Helper()
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	return reg
}

// detect parses one source file and runs registry+declared detection through the merge stage.
func detect(t *testing.T, reg *Registry, decl *declaredIndex, src string) ([]DetectedCallSite, []Diagnostic) {
	t.Helper()
	pf, err := parseSingle("github.com/acme/app/internal/svc", "svc.go", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg := &Package{Dir: "internal/svc", PkgPath: pf.PkgPath, Files: []*ParsedFile{pf}}
	sites, diags := DetectPackage(pkg, reg, decl)
	merged, _ := Merge(sites)
	return merged, diags
}

func rowIDs(sites []DetectedCallSite) []string {
	var out []string
	for _, s := range sites {
		out = append(out, s.RegistryRow)
	}
	sort.Strings(out)
	return out
}

// Each seed SDK call shape must be detected (the whole point of the registry — FR1).
func TestRegistryDetectsEachSeedSDK(t *testing.T) {
	reg := mustRegistry(t)
	cases := []struct {
		name    string
		src     string
		wantRow string
	}{
		{
			name: "anthropic messages.new (nested service method)",
			src: `package svc
import "github.com/anthropics/anthropic-sdk-go"
func run(client *anthropic.Client) { client.Messages.New(nil, anthropic.MessageNewParams{}) }`,
			wantRow: "anthropic.messages.new",
		},
		{
			name: "openai chat.completions.new (doubly-nested)",
			src: `package svc
import "github.com/openai/openai-go"
func run(client *openai.Client) { client.Chat.Completions.New(nil, openai.ChatCompletionNewParams{}) }`,
			wantRow: "openai.chat.completions.new",
		},
		{
			name: "sashabaranov createchatcompletion (client method)",
			src: `package svc
import "github.com/sashabaranov/go-openai"
func run(client *openai.Client) { client.CreateChatCompletion(nil, openai.ChatCompletionRequest{}) }`,
			wantRow: "sashabaranov.createchatcompletion",
		},
		{
			name: "langchaingo generatecontent (interface method)",
			src: `package svc
import "github.com/tmc/langchaingo/llms"
func run(llm llms.Model) { llm.GenerateContent(nil, nil) }`,
			wantRow: "langchaingo.model.generatecontent",
		},
		{
			name: "langchaingo generatefromsingleprompt (package func)",
			src: `package svc
import "github.com/tmc/langchaingo/llms"
func run(m llms.Model) { llms.GenerateFromSinglePrompt(nil, m, "hi") }`,
			wantRow: "langchaingo.generatefromsingleprompt",
		},
		{
			name: "bedrock converse (client method, aws.String model)",
			src: `package svc
import "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
func run(client *bedrockruntime.Client) { client.Converse(nil, &bedrockruntime.ConverseInput{}) }`,
			wantRow: "bedrock.converse",
		},
		{
			name: "bedrock invokemodel (opaque body)",
			src: `package svc
import "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
func run(client *bedrockruntime.Client) { client.InvokeModel(nil, &bedrockruntime.InvokeModelInput{}) }`,
			wantRow: "bedrock.invokemodel",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sites, _ := detect(t, reg, nil, tc.src)
			if len(sites) != 1 {
				t.Fatalf("want 1 node, got %d (%v)", len(sites), rowIDs(sites))
			}
			if sites[0].RegistryRow != tc.wantRow {
				t.Fatalf("want row %q, got %q", tc.wantRow, sites[0].RegistryRow)
			}
			if len(sites[0].Sources) != 1 || sites[0].Sources[0] != SourceRegistry {
				t.Fatalf("want single registry source, got %v", sites[0].Sources)
			}
		})
	}
}

// Two same-named `openai` packages must be disambiguated by import path, not local identifier (doc 04 §2.3).
func TestRegistryDisambiguatesByImportPath(t *testing.T) {
	reg := mustRegistry(t)
	// Only the community client is imported; a Chat.Completions.New would NOT match here, and the
	// official client's method must not fire off the sashabaranov import.
	src := `package svc
import "github.com/sashabaranov/go-openai"
func run(c *openai.Client) { c.CreateChatCompletion(nil, openai.ChatCompletionRequest{}) }`
	sites, _ := detect(t, reg, nil, src)
	if len(sites) != 1 || sites[0].RegistryRow != "sashabaranov.createchatcompletion" {
		t.Fatalf("want sashabaranov row, got %v", rowIDs(sites))
	}
}

// A method selector that is NOT backed by an imported SDK path must not be detected (no false positive).
func TestNoDetectionWithoutSDKImport(t *testing.T) {
	reg := mustRegistry(t)
	src := `package svc
type X struct{}
func (X) Converse(a, b any) {}
func run(x X) { x.Converse(nil, nil) }` // "Converse" but no bedrock import
	sites, _ := detect(t, reg, nil, src)
	if len(sites) != 0 {
		t.Fatalf("want no detection without SDK import, got %v", rowIDs(sites))
	}
}

// The wrapper mechanism (§6.2): a hidden in-house wrapper is found ONLY when declared in llm-eval.yaml,
// and disappears when the declaration is removed — proving user-declared entrypoints (FR2).
func TestWrapperFoundOnlyWhenDeclared(t *testing.T) {
	reg := mustRegistry(t)
	// The SDK is hidden behind internal/llm.Complete; the call site has no SDK symbol.
	src := `package svc
import "github.com/acme/app/internal/llm"
func run() { llm.Complete(nil, "summarize this") }`

	// (a) no declaration -> the wrapper node is invisible.
	sites, _ := detect(t, reg, nil, src)
	if len(sites) != 0 {
		t.Fatalf("without declaration: want 0 nodes, got %v", rowIDs(sites))
	}

	// (b) declare it -> the node appears, sourced from the declaration.
	cfg, err := LoadLLMEval([]byte(`
version: "1.0.0"
entrypoints:
  - symbol: "github.com/acme/app/internal/llm.Complete"
    provider: anthropic
    args:
      prompt: { index: 1 }
`))
	if err != nil {
		t.Fatalf("LoadLLMEval: %v", err)
	}
	decl := newDeclaredIndex(cfg)
	sites, _ = detect(t, reg, decl, src)
	if len(sites) != 1 {
		t.Fatalf("with declaration: want 1 node, got %d", len(sites))
	}
	if len(sites[0].Sources) != 1 || sites[0].Sources[0] != SourceDeclared {
		t.Fatalf("want declared source, got %v", sites[0].Sources)
	}
	if sites[0].ProviderHint != "anthropic" {
		t.Fatalf("want provider anthropic from declaration, got %q", sites[0].ProviderHint)
	}
}

// Multi-source dedup (§6.6): a call site hit by BOTH the registry and a declaration is ONE node crediting
// both sources.
func TestMultiSourceDedup(t *testing.T) {
	reg := mustRegistry(t)
	// A bedrock Converse call that the user ALSO redundantly declares as an entrypoint.
	src := `package svc
import "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
func run(client *bedrockruntime.Client) { client.Converse(nil, &bedrockruntime.ConverseInput{}) }`
	cfg, err := LoadLLMEval([]byte(`
version: "1.0.0"
entrypoints:
  - symbol: "github.com/aws/aws-sdk-go-v2/service/bedrockruntime.(*Client).Converse"
    provider: bedrock
`))
	if err != nil {
		t.Fatalf("LoadLLMEval: %v", err)
	}
	decl := newDeclaredIndex(cfg)

	raw, _ := func() ([]DetectedCallSite, []Diagnostic) {
		pf, _ := parseSingle("github.com/acme/app/internal/svc", "svc.go", src)
		pkg := &Package{PkgPath: pf.PkgPath, Files: []*ParsedFile{pf}}
		return DetectPackage(pkg, reg, decl)
	}()
	if len(raw) != 2 {
		t.Fatalf("pre-merge: want 2 single-source detections, got %d", len(raw))
	}
	merged, merges := Merge(raw)
	if len(merged) != 1 {
		t.Fatalf("post-merge: want 1 node, got %d", len(merged))
	}
	if got := distinctSources(merged[0].Sources); len(got) != 2 {
		t.Fatalf("want 2 distinct sources on the merged node, got %v", merged[0].Sources)
	}
	if len(merges) != 1 || merges[0].NodeID != merged[0].NodeID {
		t.Fatalf("want one merge record for the node, got %v", merges)
	}
}
