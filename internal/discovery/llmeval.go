package discovery

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// LLMEvalConfig is the parsed llm-eval.yaml (design doc 05): user-declared LLM wrappers the signature
// registry cannot see. The file is optional (doc 05 §3.2, resolves Q5); an empty/absent file is valid.
type LLMEvalConfig struct {
	Version     string          `yaml:"version"`
	Entrypoints []DeclaredEntry `yaml:"entrypoints"`
}

// DeclaredEntry is one declared wrapper entrypoint, parsed and resolved.
type DeclaredEntry struct {
	Symbol     string `yaml:"symbol"`      // "importpath.Func" | "importpath.(*Recv).Method"
	Provider   string `yaml:"provider"`    // static model.provider hint; "" => unresolved
	Args       ArgMap `yaml:"args"`        // reuses the registry ArgLocator forms
	DetectOnly bool   `yaml:"detect_only"` // count the node but resolve nothing (all fields unresolved)
	Invocation string `yaml:"invocation"`  // "single"|"loop"|"conditional"; "" => single
	Streaming  bool   `yaml:"streaming"`

	// Resolved from Symbol at load time:
	ImportPath string `yaml:"-"`
	Recv       string `yaml:"-"` // receiver type name, for a method
	FuncName   string `yaml:"-"`
	IsMethod   bool   `yaml:"-"`
}

// LoadLLMEval parses and validates an llm-eval.yaml. STRUCTURAL faults (bad YAML, unknown version
// MAJOR, unparseable symbol) fail LOUD here at load time (doc 08 F2, deploy-time fail-loud) — never a
// silent fallback to "no declarations", which would hide a config error as "no wrappers found".
// A symbol that parses but does not resolve to a real call is NOT a load error (doc 08 F3); that is a
// run-time declaration_diagnostic surfaced during detection.
func LoadLLMEval(data []byte) (*LLMEvalConfig, error) {
	var cfg LLMEvalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("llm-eval.yaml: parse: %w", err)
	}
	if v := strings.TrimSpace(cfg.Version); v != "" {
		if !strings.HasPrefix(v, "1.") {
			return nil, fmt.Errorf("llm-eval.yaml: unsupported version %q (this build supports 1.x)", v)
		}
	}
	for i := range cfg.Entrypoints {
		e := &cfg.Entrypoints[i]
		if strings.TrimSpace(e.Symbol) == "" {
			return nil, fmt.Errorf("llm-eval.yaml: entrypoint %d: symbol is required", i)
		}
		if err := parseDeclaredSymbol(e); err != nil {
			return nil, fmt.Errorf("llm-eval.yaml: entrypoint %d: %w", i, err)
		}
		if e.Invocation != "" && e.Invocation != "single" && e.Invocation != "loop" && e.Invocation != "conditional" {
			return nil, fmt.Errorf("llm-eval.yaml: entrypoint %d: invocation must be single|loop|conditional", i)
		}
	}
	return &cfg, nil
}

// parseDeclaredSymbol splits "importpath.Func" or "importpath.(*Recv).Method" into its parts.
func parseDeclaredSymbol(e *DeclaredEntry) error {
	s := strings.TrimSpace(e.Symbol)
	// Method form: <path>.(*Recv).Method or <path>.(Recv).Method
	if i := strings.Index(s, ".("); i >= 0 {
		importPath := s[:i]
		rest := s[i+2:] // "*Recv).Method"
		j := strings.Index(rest, ")")
		if j < 0 {
			return fmt.Errorf("malformed method symbol %q (missing ')')", s)
		}
		recv := strings.TrimPrefix(rest[:j], "*")
		method := strings.TrimPrefix(rest[j+1:], ".")
		if importPath == "" || recv == "" || method == "" {
			return fmt.Errorf("malformed method symbol %q", s)
		}
		e.ImportPath, e.Recv, e.FuncName, e.IsMethod = importPath, recv, method, true
		return nil
	}
	// Free-function form: <path>.Func
	k := strings.LastIndex(s, ".")
	if k <= 0 || k == len(s)-1 {
		return fmt.Errorf("malformed symbol %q (want importpath.Func or importpath.(*Recv).Method)", s)
	}
	e.ImportPath, e.FuncName, e.IsMethod = s[:k], s[k+1:], false
	return nil
}

// declaredIndex tracks declared entrypoints and which ones matched, so unmatched declarations become
// DECL_SYMBOL_NOT_FOUND diagnostics (doc 08 F3 / invariant I4).
type declaredIndex struct {
	entries []DeclaredEntry
	matched map[int]bool // index -> matched at least one call site
}

func newDeclaredIndex(cfg *LLMEvalConfig) *declaredIndex {
	idx := &declaredIndex{matched: map[int]bool{}}
	if cfg != nil {
		idx.entries = cfg.Entrypoints
	}
	return idx
}

// unmatchedDiagnostics returns a DECL_SYMBOL_NOT_FOUND warning for every declared entrypoint that never
// matched a call site — so a missing wrapper node is always explainable (I4).
func (d *declaredIndex) unmatchedDiagnostics() []Diagnostic {
	var out []Diagnostic
	for i := range d.entries {
		if d.matched[i] {
			continue
		}
		out = append(out, Diagnostic{
			Code:     CodeDeclSymbolNotFound,
			Severity: SeverityWarn,
			Symbol:   d.entries[i].Symbol,
			Message:  "declared entrypoint did not resolve to any call site in the scanned repo",
		})
	}
	return out
}
