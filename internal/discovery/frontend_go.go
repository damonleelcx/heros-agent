package discovery

import (
	"fmt"
	"strings"
)

// GoFrontend is the Go LanguageFrontend (frontend #1): it parses `.go` files via `go/ast` (real import
// resolution) and runs the registry + declared detectors, extractor, graph builder, and Go framework
// readers — all the language-specific work — returning a language-neutral FrontendResult. It NEVER
// executes target code (I1): parsing is `go/parser` only, no subprocess.
type GoFrontend struct {
	readers []FrameworkReader
}

// NewGoFrontend builds the Go frontend. With no readers it defaults to the Go declarative-graph reader.
func NewGoFrontend(readers ...FrameworkReader) *GoFrontend {
	if len(readers) == 0 {
		readers = []FrameworkReader{NewGoGraphBuilderReader()}
	}
	return &GoFrontend{readers: readers}
}

func (f *GoFrontend) Language() string { return "go" }

func (f *GoFrontend) Handles(path string) bool {
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}

// Discover runs the Go pipeline over the repo, streaming one package at a time (bounded memory, NFR3).
func (f *GoFrontend) Discover(repo string, reg *Registry, decl *declaredIndex) (FrontendResult, error) {
	goReg := reg.ForLanguage("go") // 10.2: a Go call can only match a Go registry row
	loader, err := NewLoader(repo)
	if err != nil {
		return FrontendResult{}, err
	}
	var res FrontendResult
	res.WorkflowID = loader.ModulePath

	loaderDiags, err := loader.ForEachPackage(func(pkg *Package) error {
		// A panic in one package's processing (a reader bug, an unexpected AST) degrades to a per-package
		// diagnostic instead of killing the run (I7 / doc 08 F10); the rest of the repo continues.
		defer func() {
			if r := recover(); r != nil {
				res.Diagnostics = append(res.Diagnostics, Diagnostic{
					Code: CodePackagePanic, Severity: SeverityWarn, File: pkg.Dir,
					Message: fmt.Sprintf("recovered panic while processing package: %v", r),
				})
			}
		}()

		res.PackagesScanned++
		res.FilesScanned += len(pkg.Files)

		sites, detDiags := DetectPackage(pkg, goReg, decl)
		res.CallSites += len(sites)
		res.Diagnostics = append(res.Diagnostics, detDiags...)

		merged, merges := Merge(sites)
		res.Merges = append(res.Merges, merges...)

		g := BuildGraph(pkg.Files, merged)
		res.Nodes = append(res.Nodes, g.Nodes...)
		res.Edges = append(res.Edges, g.Edges...)

		for _, reader := range f.readers {
			fg, fdiags, present := safeFramework(reader, pkg)
			if present {
				res.Frameworks = append(res.Frameworks, fg)
				res.Diagnostics = append(res.Diagnostics, fdiags...)
			}
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	res.Diagnostics = append(append([]Diagnostic{}, loaderDiags...), res.Diagnostics...)
	return res, nil
}

// safeFramework runs one framework reader with a recover guard: a reader panic (a versioned reader bug on
// drifted input) becomes a flagged diagnostic, never a crash (doc 07 §2.1 / doc 08 F10).
func safeFramework(reader FrameworkReader, pkg *Package) (fg FrameworkGraph, diags []Diagnostic, present bool) {
	defer func() {
		if r := recover(); r != nil {
			present = true
			fg = FrameworkGraph{FrameworkSource: reader.Name(), Degraded: true, SubgraphID: "sg_" + sanitizeID(pkg.PkgPath)}
			diags = []Diagnostic{{
				Code: CodeFrameworkReaderErr, Severity: SeverityWarn, Symbol: fg.SubgraphID,
				Message: fmt.Sprintf("framework reader %q panicked; subgraph degraded: %v", reader.Name(), r),
			}}
		}
	}()
	if _, p, _ := reader.Detect(pkg); !p {
		return FrameworkGraph{}, nil, false
	}
	fg, diags = reader.ReadDAG(pkg)
	return fg, diags, true
}
