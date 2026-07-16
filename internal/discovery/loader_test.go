package discovery

import (
	"strings"
	"testing"
)

// The loader walks a repo read-only, reads the module path, skips vendor, and streams packages in a
// deterministic order (§3.1). A broken file is skip-and-reported while other packages still load (I7).
func TestLoaderWalksRepo(t *testing.T) {
	l, err := NewLoader("testdata/samplerepo")
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	if l.ModulePath != "example.com/sample" {
		t.Fatalf("module path: want example.com/sample, got %q", l.ModulePath)
	}

	var pkgPaths []string
	diags, err := l.ForEachPackage(func(p *Package) error {
		pkgPaths = append(pkgPaths, p.PkgPath)
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachPackage: %v", err)
	}

	want := map[string]bool{
		"example.com/sample":              false,
		"example.com/sample/internal/llm": false,
		"example.com/sample/cmd/app":      false,
	}
	for _, p := range pkgPaths {
		if _, ok := want[p]; ok {
			want[p] = true
		}
		if strings.Contains(p, "/vendor/") || strings.HasSuffix(p, "/vendor") {
			t.Fatalf("vendor package must be skipped, got %q", p)
		}
		if strings.Contains(p, "/broken") {
			// broken has no parseable file, so it must not be emitted as a package.
			t.Fatalf("broken package should not be emitted (its only file fails to parse), got %q", p)
		}
	}
	for p, seen := range want {
		if !seen {
			t.Fatalf("expected package %q not discovered (got %v)", p, pkgPaths)
		}
	}

	// The broken file is reported, not swallowed and not fatal (I4/I7).
	var sawParseErr bool
	for _, d := range diags {
		if d.Code == CodeParseError && strings.Contains(d.File, "broken/bad.go") {
			sawParseErr = true
		}
	}
	if !sawParseErr {
		t.Fatalf("want a PARSE_ERROR diagnostic for broken/bad.go, got %v", diags)
	}
}

// End-to-end over the sample repo: the registry finds the direct anthropic call in main, and a
// declaration finds the internal/llm.Complete wrapper call in cmd/app — proving both sources on a real
// on-disk tree (§3.3 + §3.4 + §3.5).
func TestLoaderEndToEndDetection(t *testing.T) {
	reg := mustRegistry(t)
	cfg, err := LoadLLMEval([]byte(`
version: "1.0.0"
entrypoints:
  - symbol: "example.com/sample/internal/llm.Complete"
    provider: anthropic
    args:
      prompt: { index: 1 }
`))
	if err != nil {
		t.Fatalf("LoadLLMEval: %v", err)
	}
	decl := newDeclaredIndex(cfg)

	l, _ := NewLoader("testdata/samplerepo")
	var all []DetectedCallSite
	_, err = l.ForEachPackage(func(p *Package) error {
		sites, _ := DetectPackage(p, reg, decl)
		all = append(all, sites...)
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachPackage: %v", err)
	}
	merged, _ := Merge(all)

	var registryHit, declaredHit bool
	for _, s := range merged {
		switch {
		case s.RegistryRow == "anthropic.messages.new":
			registryHit = true
		case s.DeclaredSym == "example.com/sample/internal/llm.Complete":
			declaredHit = true
		}
	}
	if !registryHit {
		t.Fatalf("registry did not find the anthropic call in main.go; got %d nodes", len(merged))
	}
	if !declaredHit {
		t.Fatalf("declaration did not find the internal/llm.Complete wrapper call in cmd/app")
	}
}
