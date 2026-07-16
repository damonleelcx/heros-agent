package discovery

import "testing"

// Structural faults in llm-eval.yaml fail LOUD at load (doc 08 F2), never silently degrade to "no
// declarations" — that would hide a config error as "no wrappers found".
func TestLLMEvalFailsLoudOnStructuralFaults(t *testing.T) {
	bad := []struct {
		name string
		yaml string
	}{
		{"malformed yaml", "entrypoints: [ : bad"},
		{"unsupported version", "version: \"2.0.0\"\nentrypoints: []"},
		{"missing symbol", "version: \"1.0.0\"\nentrypoints:\n  - provider: openai"},
		{"malformed method symbol", "version: \"1.0.0\"\nentrypoints:\n  - symbol: \"x.(*Svc.Do\""},
		{"bad invocation", "version: \"1.0.0\"\nentrypoints:\n  - symbol: \"a/b.Do\"\n    invocation: sometimes"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadLLMEval([]byte(tc.yaml)); err == nil {
				t.Fatalf("want load error for %s, got nil", tc.name)
			}
		})
	}
}

func TestLLMEvalParsesSymbols(t *testing.T) {
	cfg, err := LoadLLMEval([]byte(`
version: "1.0.0"
entrypoints:
  - symbol: "github.com/acme/app/internal/llm.Complete"
  - symbol: "github.com/acme/app/internal/llm.(*Service).Summarize"
`))
	if err != nil {
		t.Fatalf("LoadLLMEval: %v", err)
	}
	free := cfg.Entrypoints[0]
	if free.IsMethod || free.ImportPath != "github.com/acme/app/internal/llm" || free.FuncName != "Complete" {
		t.Fatalf("free func parsed wrong: %+v", free)
	}
	meth := cfg.Entrypoints[1]
	if !meth.IsMethod || meth.Recv != "Service" || meth.FuncName != "Summarize" {
		t.Fatalf("method parsed wrong: %+v", meth)
	}
}

// An empty / absent config is valid (doc 05 §3.2, resolves Q5) — the file is optional-but-recommended.
func TestLLMEvalEmptyIsValid(t *testing.T) {
	cfg, err := LoadLLMEval([]byte(""))
	if err != nil {
		t.Fatalf("empty config should be valid, got %v", err)
	}
	if len(cfg.Entrypoints) != 0 {
		t.Fatalf("want 0 entrypoints, got %d", len(cfg.Entrypoints))
	}
}

// A declared entrypoint that never resolves to a call site becomes a DECL_SYMBOL_NOT_FOUND diagnostic
// (doc 08 F3 / invariant I4: every missing node explainable).
func TestUnmatchedDeclarationDiagnostic(t *testing.T) {
	reg := mustRegistry(t)
	cfg, err := LoadLLMEval([]byte(`
version: "1.0.0"
entrypoints:
  - symbol: "github.com/acme/app/internal/old.LegacyGenerate"
`))
	if err != nil {
		t.Fatalf("LoadLLMEval: %v", err)
	}
	decl := newDeclaredIndex(cfg)
	// Detect over a file that does NOT call the declared symbol.
	_, _ = detect(t, reg, decl, `package svc
func run() { _ = 1 }`)
	diags := decl.unmatchedDiagnostics()
	if len(diags) != 1 || diags[0].Code != CodeDeclSymbolNotFound {
		t.Fatalf("want one DECL_SYMBOL_NOT_FOUND, got %v", diags)
	}
}
