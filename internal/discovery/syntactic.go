package discovery

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RawCallSite is a language-neutral call site produced by a syntactic (type-free) analyzer — the
// normalized shape the tree-sitter substrate yields (10.4). It carries exactly what the shared detector
// needs: the call target's root + selector chain, the enclosing symbol, the source span, an invocation
// hint (loop/conditional), and any keyword string-literal args (for the resolution floor, 10.5).
type RawCallSite struct {
	Root              string            // leftmost identifier of the call target ("" if not an ident)
	RootIdent         bool              // true when Root is a bare identifier
	Chain             []string          // selector names after the root, e.g. ["messages","create"]
	EnclosingSymbol   string            // enclosing function/method/class FQN, or "<module>"
	LineStart         int               // 1-based
	LineEnd           int               // 1-based
	Invocation        string            // "single" | "loop" | "conditional" (from surrounding control flow)
	KeywordStrings    map[string]string // keyword/object-arg name -> string-literal value (for model/prompt floor)
	PositionalStrings []string          // string-literal positional args in order (for framework builder calls)
}

// SyntacticUnit is one analyzed source file in normalized form.
type SyntacticUnit struct {
	RelPath     string
	PkgPath     string            // language-neutral module/package path for node identity (per-frontend spelling)
	Imports     map[string]string // local name -> module/import path
	ImportPaths map[string]bool   // set of imported module paths
	CallSites   []RawCallSite
}

// LanguageAnalyzer parses ONE language's source into normalized units. The tree-sitter parser (or any
// other syntactic, type-free parser) lives behind this interface. It NEVER executes source (I1).
type LanguageAnalyzer interface {
	Language() string
	Extensions() []string
	Analyze(relPath string, src []byte) (SyntacticUnit, []Diagnostic)
}

// SyntacticFrontend adapts any LanguageAnalyzer to LanguageFrontend, reusing the language-neutral
// detection matching, node-ID scheme, merge, and the resolution-floor extractor. This is the shared
// tree-sitter substrate base (10.4): the analyzer supplies the parse-tree walk; everything to its right
// is the same core the Go frontend uses.
type SyntacticFrontend struct {
	analyzer LanguageAnalyzer
}

// NewSyntacticFrontend wraps an analyzer as a LanguageFrontend.
func NewSyntacticFrontend(a LanguageAnalyzer) *SyntacticFrontend {
	return &SyntacticFrontend{analyzer: a}
}

func (f *SyntacticFrontend) Language() string { return f.analyzer.Language() }

func (f *SyntacticFrontend) Handles(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range f.analyzer.Extensions() {
		if ext == e {
			return true
		}
	}
	return false
}

// Discover walks the repo (read-only), analyzes each file this frontend handles, and runs the shared
// detector + floor extractor. A per-file panic degrades to a diagnostic (I7); source is never executed.
func (f *SyntacticFrontend) Discover(repo string, reg *Registry, decl *declaredIndex) (FrontendResult, error) {
	langReg := reg.ForLanguage(f.Language())
	var res FrontendResult

	err := filepath.WalkDir(repo, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			res.Diagnostics = append(res.Diagnostics, Diagnostic{Code: CodeWalkError, Severity: SeverityWarn, Message: werr.Error()})
			return nil
		}
		if d.IsDir() {
			if path != repo && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			res.Diagnostics = append(res.Diagnostics, Diagnostic{Code: CodeSymlinkCycleSkipped, Severity: SeverityWarn, Message: "symlink skipped"})
			return nil
		}
		if !f.Handles(path) {
			return nil
		}
		rel, _ := filepath.Rel(repo, path)
		rel = filepath.ToSlash(rel)
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			res.Diagnostics = append(res.Diagnostics, Diagnostic{Code: CodeWalkError, Severity: SeverityWarn, File: rel, Message: rerr.Error()})
			return nil
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					res.Diagnostics = append(res.Diagnostics, Diagnostic{Code: CodePackagePanic, Severity: SeverityWarn, File: rel, Message: "recovered panic analyzing file"})
				}
			}()
			unit, adiags := f.analyzer.Analyze(rel, src)
			res.FilesScanned++
			res.PackagesScanned++ // per-file granularity for syntactic frontends
			res.Diagnostics = append(res.Diagnostics, adiags...)

			nodes, sites, merges := detectSyntacticUnit(unit, langReg, decl)
			res.Nodes = append(res.Nodes, nodes...)
			res.CallSites += sites
			res.Merges = append(res.Merges, merges...)
			res.Frameworks = append(res.Frameworks, syntacticFrameworks(f.Language(), unit)...)
		}()
		return nil
	})
	return res, err
}

// detectSyntacticUnit runs the shared registry + declared matchers over a unit's raw call sites and
// applies the resolution-floor extractor. Edges are not inferred at the syntactic floor (no intra-file
// data-flow without types) — honestly omitted, never guessed.
func detectSyntacticUnit(unit SyntacticUnit, reg *Registry, decl *declaredIndex) ([]ExtractedNode, int, []MergeRecord) {
	importsPath := func(p string) bool { return unit.ImportPaths[p] }
	occ := map[string]int{}
	var sites []DetectedCallSite

	for _, cs := range unit.CallSites {
		sel := selectorString(cs.Root, cs.RootIdent, cs.Chain)
		if sel == "" {
			continue
		}
		key := cs.EnclosingSymbol + "\x00" + sel
		idx := occ[key]
		occ[key]++
		id := NodeIdentity{ModulePkgPath: unit.PkgPath, EnclosingSymbolFQN: cs.EnclosingSymbol, Selector: sel, OccurrenceIndex: idx}
		nodeID := id.NodeID()
		base := DetectedCallSite{
			Identity: id, NodeID: nodeID,
			FileRel: unit.RelPath, LineStart: cs.LineStart, LineEnd: cs.LineEnd,
			Keywords: cs.KeywordStrings, Invocation: cs.Invocation,
		}
		if row, basis, ok := matchRegistryRows(reg, unit.Imports, importsPath, cs.Root, cs.RootIdent, cs.Chain); ok {
			s := base
			s.Sources = []DetectionSource{SourceRegistry}
			s.Basis = []MatchBasis{basis}
			s.RegistryRow = row.ID
			s.ArgMap = row.ArgMap
			s.ProviderHint = row.ProviderHint
			s.Opacity = row.Opacity
			sites = append(sites, s)
		}
		if e, ok := matchDeclaredEntries(decl, unit.Imports, importsPath, unit.PkgPath, cs.Root, cs.RootIdent, cs.Chain); ok {
			s := base
			s.Sources = []DetectionSource{SourceDeclared}
			s.Basis = []MatchBasis{BasisDeclared}
			s.DeclaredSym = e.Symbol
			s.ArgMap = e.Args
			s.ProviderHint = e.Provider
			s.DetectOnly = e.DetectOnly
			if e.Invocation != "" {
				s.Invocation = e.Invocation
			}
			sites = append(sites, s)
		}
	}

	merged, merges := Merge(sites)
	nodes := make([]ExtractedNode, 0, len(merged))
	for _, s := range merged {
		nodes = append(nodes, extractSyntacticFloor(s))
	}
	return nodes, len(sites), merges
}

// extractSyntacticFloor is the type-free extraction floor (10.5, resolves PRD Q7): resolve a field only
// when it is a keyword string literal; otherwise emit the `unresolved` sentinel + a flagged reason. It
// never guesses (I5). This is the honest boundary for tree-sitter frontends (no type resolution).
func extractSyntacticFloor(s DetectedCallSite) ExtractedNode {
	n := ExtractedNode{NodeID: s.NodeID, Site: s}
	n.Invocation = invocationFromHint(s.Invocation)

	provider := s.ProviderHint
	if provider == "" {
		provider = UnresolvedSentinel
	}

	// model: resolvable only if the locator names a keyword whose value is a string literal.
	if v, ok := keywordFor(s, s.ArgMap.Model); ok {
		n.Model = ResolvedModel{Provider: provider, ModelID: v, Params: map[string]any{}}
	} else {
		n.Model = ResolvedModel{Provider: provider, ModelID: UnresolvedSentinel, Unresolved: true}
		n.Ambiguities = append(n.Ambiguities, flag(s.NodeID, "model", CodeModelUnresolved,
			"syntactic frontend floor: model is not a keyword string literal (no type resolution)"))
	}

	// prompt: string-literal keyword resolves inline; anything else (a message list, a variable) is
	// unresolved — the common Python/TS case, honestly flagged for P5.
	if v, ok := keywordFor(s, s.ArgMap.Prompt); ok {
		n.Prompt = ResolvedPrompt{Inline: v, Variables: []string{}}
	} else {
		n.Prompt = ResolvedPrompt{Unresolved: true, Variables: []string{}}
		n.Ambiguities = append(n.Ambiguities, flag(s.NodeID, "prompt", CodePromptUnresolved,
			"syntactic frontend floor: prompt is not a static string literal (assembled at runtime / no type resolution)"))
	}

	n.ToolsSkills = []string{}
	n.Context = ContextAssembly{Policy: "inline_messages", Description: "static call site (syntactic frontend; no type resolution)"}
	return n
}

// keywordFor resolves an ArgLocator against the call's keyword string literals (the only thing a
// type-free parser can resolve). Returns the literal and true only for a ParamName locator with a
// matching keyword string.
func keywordFor(s DetectedCallSite, loc *ArgLocator) (string, bool) {
	if loc == nil || loc.Form != LocParamName || s.Keywords == nil {
		return "", false
	}
	v, ok := s.Keywords[loc.Name]
	return v, ok && v != ""
}

// syntacticFrameworkReader reads a declarative agent-graph definition from a normalized SyntacticUnit
// (the tree-sitter-substrate analogue of the Go FrameworkReader, which is *Package-based). Per-language
// framework support = registering a reader here; the report/IR plumbing is shared (10.11, resolves the
// doc 07 §4 Go-vs-Python scope conflict — Python LangGraph + CrewAI now ship).
type syntacticFrameworkReader interface {
	Name() string
	Read(unit SyntacticUnit) (FrameworkGraph, bool)
}

// syntacticFrameworkReaders is the registry of framework readers for tree-sitter frontends.
var syntacticFrameworkReaders = []syntacticFrameworkReader{langGraphReader{}, crewAIReader{}}

func syntacticFrameworks(lang string, unit SyntacticUnit) []FrameworkGraph {
	var out []FrameworkGraph
	for _, r := range syntacticFrameworkReaders {
		if g, ok := r.Read(unit); ok {
			out = append(out, g)
		}
	}
	return out
}

// langGraphReader reads a LangGraph/LangChain declarative state graph: add_node / add_edge /
// add_conditional_edges / set_entry_point (snake_case Python, camelCase JS).
type langGraphReader struct{}

func (langGraphReader) Name() string { return "langgraph" }

func (langGraphReader) Read(unit SyntacticUnit) (FrameworkGraph, bool) {
	if !unitImports(unit, "langgraph", "langchain") {
		return FrameworkGraph{}, false
	}
	g := FrameworkGraph{FrameworkSource: "langgraph", Version: "v0", Recognized: true, SubgraphID: "sg_" + sanitizeID(unit.PkgPath)}
	nodeSet := map[string]bool{}
	edgeSeen := map[FrameworkEdge]bool{}
	addEdge := func(e FrameworkEdge) {
		if !edgeSeen[e] {
			edgeSeen[e] = true
			g.Edges = append(g.Edges, e)
		}
	}
	for _, cs := range unit.CallSites {
		switch normalizeBuilderMethod(builderLeaf(cs)) {
		case "add_node", "set_entry_point":
			if len(cs.PositionalStrings) >= 1 {
				nodeSet[cs.PositionalStrings[0]] = true
			}
		case "add_edge":
			if len(cs.PositionalStrings) >= 2 {
				from, to := cs.PositionalStrings[0], cs.PositionalStrings[1]
				addEdge(FrameworkEdge{From: from, To: to, Kind: "data"})
				nodeSet[from], nodeSet[to] = true, true
			}
		case "add_conditional_edges":
			if len(cs.PositionalStrings) >= 1 {
				from := cs.PositionalStrings[0]
				nodeSet[from] = true
				for _, to := range cs.PositionalStrings[1:] { // routing-map values (keys were skipped upstream)
					if to != from {
						addEdge(FrameworkEdge{From: from, To: to, Kind: "control"})
						nodeSet[to] = true
					}
				}
			}
		}
	}
	if len(nodeSet) == 0 {
		return FrameworkGraph{}, false // imported langgraph but declared no graph
	}
	for n := range nodeSet {
		g.Nodes = append(g.Nodes, n)
	}
	sortStrings(g.Nodes)
	sortFrameworkEdges(g.Edges)
	return g, true
}

// crewAIReader reads a CrewAI crew: it recognizes Agent(role=...) definitions as nodes and the presence
// of a Crew(...) / .kickoff(). CrewAI's process is sequential/hierarchical rather than explicit edges, so
// no edges are emitted; if the crew is present but agents are not role-named, the subgraph is degraded
// (present but topology unresolved — honest, never guessed).
type crewAIReader struct{}

func (crewAIReader) Name() string { return "crewai" }

func (crewAIReader) Read(unit SyntacticUnit) (FrameworkGraph, bool) {
	if !unitImports(unit, "crewai") {
		return FrameworkGraph{}, false
	}
	g := FrameworkGraph{FrameworkSource: "crewai", Version: "v0", Recognized: true, SubgraphID: "sg_crew_" + sanitizeID(unit.PkgPath)}
	nodeSet := map[string]bool{}
	sawCrew := false
	for _, cs := range unit.CallSites {
		switch builderLeaf(cs) {
		case "Agent":
			if role, ok := cs.KeywordStrings["role"]; ok && role != "" {
				nodeSet[role] = true
			}
		case "Crew", "kickoff":
			sawCrew = true
		}
	}
	if !sawCrew && len(nodeSet) == 0 {
		return FrameworkGraph{}, false // imported crewai but no crew declared
	}
	for n := range nodeSet {
		g.Nodes = append(g.Nodes, n)
	}
	sortStrings(g.Nodes)
	g.Degraded = len(g.Nodes) == 0 // crew present but no role-named agents => topology unresolved
	return g, true
}

// builderLeaf returns the call's leaf method/constructor name (the chain leaf, or the root for a bare call).
func builderLeaf(cs RawCallSite) string {
	if len(cs.Chain) > 0 {
		return cs.Chain[len(cs.Chain)-1]
	}
	return cs.Root
}

func unitImports(unit SyntacticUnit, needles ...string) bool {
	for p := range unit.ImportPaths {
		lp := strings.ToLower(p)
		for _, n := range needles {
			if strings.Contains(lp, n) {
				return true
			}
		}
	}
	return false
}

// normalizeBuilderMethod maps snake_case (Python) and camelCase (JS) builder method names to a canonical form.
func normalizeBuilderMethod(m string) string {
	switch m {
	case "add_node", "addNode":
		return "add_node"
	case "add_edge", "addEdge":
		return "add_edge"
	case "add_conditional_edges", "addConditionalEdges":
		return "add_conditional_edges"
	case "set_entry_point", "setEntryPoint":
		return "set_entry_point"
	}
	return ""
}

func invocationFromHint(h string) InvocationSemantics {
	switch h {
	case "loop":
		return InvocationSemantics{Type: "loop", VariableAtRuntime: true}
	case "conditional":
		return InvocationSemantics{Type: "conditional", VariableAtRuntime: false}
	default:
		return InvocationSemantics{Type: "single", VariableAtRuntime: false}
	}
}

func sortStrings(s []string) { sort.Strings(s) }

func sortFrameworkEdges(e []FrameworkEdge) {
	sort.Slice(e, func(i, j int) bool {
		if e[i].From != e[j].From {
			return e[i].From < e[j].From
		}
		if e[i].To != e[j].To {
			return e[i].To < e[j].To
		}
		return e[i].Kind < e[j].Kind
	})
}
