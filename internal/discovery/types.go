package discovery

import (
	"go/ast"
	"go/token"
)

// DetectionSource is how a call site was found. The three sources are co-equal and merged (design D1).
type DetectionSource string

const (
	SourceRegistry  DetectionSource = "registry"
	SourceDeclared  DetectionSource = "declared"
	SourceFramework DetectionSource = "framework"
)

// MatchBasis records how confident a match is, for the run report's per-node provenance (doc 09).
type MatchBasis string

const (
	// BasisPackageQualified: the call root is a package identifier resolved via the file import map to
	// the matched import path — a high-confidence match (e.g. llms.GenerateFromSinglePrompt).
	BasisPackageQualified MatchBasis = "package_qualified"
	// BasisSelectorImport: a method call whose receiver we cannot type without go/types; matched by
	// "file imports the SDK path" + "selector suffix" (e.g. client.Messages.New). Lower confidence.
	BasisSelectorImport MatchBasis = "selector_import"
	// BasisDeclared: matched a user-declared llm-eval.yaml entrypoint.
	BasisDeclared MatchBasis = "declared"
)

// ParsedFile is one parsed Go source file (AST + text only; never executed).
type ParsedFile struct {
	RelPath     string            // repo-relative path
	PkgPath     string            // module-relative package path of this file's package
	Fset        *token.FileSet    // shared per package; for positions
	AST         *ast.File         // the parsed syntax tree
	Imports     map[string]string // local name -> import path (alias, or last path segment)
	importPaths map[string]bool   // set of imported paths (for import-presence checks)
}

// importsPath reports whether this file imports the given path (independent of local name).
func (f *ParsedFile) importsPath(p string) bool { return f.importPaths[p] }

// Package is one Go package (a directory of files), yielded by the loader one at a time (§3.1).
type Package struct {
	Dir     string // repo-relative directory
	PkgPath string // module path + "/" + dir (or dir if module unknown)
	Files   []*ParsedFile
}

// DetectedCallSite is the output of §3 detection: a matched LLM call expression plus provenance and
// the arg map telling §4 where model/prompt/tools live. It is NOT yet a full IR node (that is §4/§5).
type DetectedCallSite struct {
	Identity     NodeIdentity
	NodeID       string
	Sources      []DetectionSource // may be multiple after merge (§3.5)
	RegistryRow  string            // signature row id, if matched by registry
	DeclaredSym  string            // llm-eval symbol, if matched by declaration
	Basis        []MatchBasis      // one per source, aligned with Sources
	Call         *ast.CallExpr     // the matched call expression (Go frontend only; nil for tree-sitter)
	File         *ParsedFile       // owning file (Go frontend only; nil for tree-sitter)
	FileRel      string            // repo-relative source file (language-neutral; used by the emitter)
	LineStart    int               // 1-based start line (language-neutral)
	LineEnd      int               // 1-based end line (language-neutral)
	ArgMap       ArgMap            // where model/prompt/tools live (from the matching source)
	Keywords     map[string]string // keyword-arg name -> string-literal value (syntactic frontends; nil for Go)
	ProviderHint string            // static provider hint, if any ("" => unresolved provider)
	Opacity      []string          // IR fields inherently unresolvable for this entrypoint
	DetectOnly   bool              // declared entrypoint that resolves nothing (all fields unresolved)
	Invocation   string            // "single"|"loop"|"conditional" hint from a declaration ("" => single)
}
