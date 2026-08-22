package discovery

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// AnalysisKind is how deeply a frontend analyses the source it reads. It is the fact that decides
// whether "this graph has no edges" is a statement about the CODE or a limit of the ANALYSIS, and those
// two readings send a reader to completely different places.
//
// It is a closed vocabulary, and the method below is a REQUIRED part of LanguageFrontend rather than an
// optional interface a frontend may implement. An optional one defaults, and a defaulted answer here is
// a frontend silently claiming a capability it does not have — which is how a syntactic frontend's empty
// edge list comes to read as "this workflow's calls are independent".
type AnalysisKind string

const (
	// AnalysisTyped: the frontend resolves values across statements — it can follow what one call
	// produced into what the next consumed — so it establishes topology. Zero edges from a typed
	// frontend is a fact about the code.
	AnalysisTyped AnalysisKind = "typed"
	// AnalysisSyntactic: the frontend enumerates call sites by pattern-matching a syntax tree. It cannot
	// follow a value across a statement, let alone a module, so it emits NODES AND NO EDGES. Zero edges
	// from a syntactic frontend says nothing at all about the code.
	AnalysisSyntactic AnalysisKind = "syntactic"
)

// LanguageFrontend is the ONLY language-specific layer of Discovery (design D0). It parses a repo in
// one language and produces language-neutral discovery output; the core (report assembly, IR emission)
// consumes only this interface and never touches a language AST. Adding a language is adding a frontend
// + registry rows + fixtures — the core is untouched. A frontend NEVER executes target code (I1).
type LanguageFrontend interface {
	// Language is the row-selection key into the signature registry and the IR `workflow.language` value.
	Language() string
	// AnalysisKind declares how deeply this frontend analyses — and therefore whether an empty edge
	// list from it means anything. See AnalysisKind.
	AnalysisKind() AnalysisKind
	// Handles reports whether this frontend is responsible for a given source file (by extension).
	Handles(path string) bool
	// Discover parses the files it Handles, detects call sites (registry + declared), extracts metadata,
	// builds intra-unit edges, and reads framework graphs — returning a language-neutral result.
	Discover(repo string, reg *Registry, decl *declaredIndex) (FrontendResult, error)
}

// FrontendRun records one frontend's participation in a discovery run: which frontend produced part of
// this graph, how deeply it analyses, and what it contributed.
//
// 🔴 This is the SOURCE for the graph view's `no_topology` statement. The view names the frontend and
// its analysis kind from these records; it does not carry a hand-written sentence about Python, which
// would keep saying the same thing on the day the Python frontend learns to emit edges.
type FrontendRun struct {
	Language     string       `json:"language"`
	AnalysisKind AnalysisKind `json:"analysis_kind"`
	Nodes        int          `json:"nodes"`
	Edges        int          `json:"edges"`
}

// FrontendResult is the language-neutral output of one frontend over a repo.
type FrontendResult struct {
	Nodes           []ExtractedNode
	Edges           []GraphEdge
	Frameworks      []FrameworkGraph
	Diagnostics     []Diagnostic
	Merges          []MergeRecord
	FilesScanned    int
	PackagesScanned int
	CallSites       int
	WorkflowID      string // the frontend's suggested workflow.id (e.g. Go module path); "" if none
}

// knownSourceExt maps source-file extensions to a language label, so a repo containing source in a
// language no registered frontend Handles can be honestly reported (I4: explain why 0 nodes) rather
// than silently ignored. Non-source extensions (.md/.yaml/.json/…) are intentionally excluded.
var knownSourceExt = map[string]string{
	".go": "go", ".py": "python", ".pyi": "python",
	".ts": "typescript", ".tsx": "typescript", ".js": "javascript", ".jsx": "javascript",
	".mjs": "javascript", ".cjs": "javascript",
	".java": "java", ".kt": "kotlin", ".kts": "kotlin", ".rs": "rust", ".rb": "ruby",
	".cs": "csharp", ".php": "php", ".swift": "swift", ".scala": "scala",
}

// unsupportedLanguageDiagnostics walks the repo (read-only) and reports any source language present that
// no registered frontend handles — so a Python/TS/… repo yielding zero nodes is explained, not silent
// (resolves the honesty half of PRD Q6/Q7). This is the "no silently-dropped language" guard.
func unsupportedLanguageDiagnostics(repo string, frontends []LanguageFrontend) []Diagnostic {
	present := map[string]bool{} // language -> seen a source file
	_ = filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != repo && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if lang, ok := knownSourceExt[strings.ToLower(filepath.Ext(path))]; ok {
			if !anyHandles(frontends, path) {
				present[lang] = true
			}
		}
		return nil
	})
	var langs []string
	for l := range present {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	var out []Diagnostic
	for _, l := range langs {
		out = append(out, Diagnostic{
			Code:     CodeLanguageUnsupported,
			Severity: SeverityWarn,
			// 🔴 The LANGUAGE, in a field, as well as in the sentence. It was in the sentence only, and
			// the first consumer that needed it — P33's assessment, which reports "this build has no
			// frontend for ruby" as a refusal a customer cannot act on — would have had to take the
			// first word of a human message. A regex over prose is a contract nobody declared: it
			// breaks the day somebody improves the wording, silently, and the symptom is a refusal that
			// names no language.
			//
			// `Symbol` is the right field rather than a new one: it is what this diagnostic is ABOUT,
			// which for a parse error is a function and here is a language.
			Symbol:  l,
			Message: l + " source is present but no frontend for it is registered — 0 nodes extracted for that language (add a " + l + " frontend + registry rows)",
		})
	}
	return out
}

func anyHandles(frontends []LanguageFrontend, path string) bool {
	for _, f := range frontends {
		if f.Handles(path) {
			return true
		}
	}
	return false
}

// workflowLanguageLabel derives IR `workflow.language`: the single frontend's language when one frontend,
// otherwise "mixed" if more than one contributed nodes, else the sole contributor's language.
func workflowLanguageLabel(frontends []LanguageFrontend, contributed map[string]bool) string {
	if len(frontends) == 1 {
		return frontends[0].Language()
	}
	var langs []string
	for l := range contributed {
		langs = append(langs, l)
	}
	switch len(langs) {
	case 0:
		if len(frontends) > 0 {
			return frontends[0].Language()
		}
		return "unknown"
	case 1:
		return langs[0]
	default:
		return "mixed"
	}
}
