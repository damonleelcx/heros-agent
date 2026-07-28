package discovery

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// ArgSpan is a half-open byte range [Start,End) into the source bytes the analyzer was handed — the
// same []byte that produced the unit. Byte offsets, not line/column, because the only thing a rewriter
// does with them is slice and splice a []byte.
type ArgSpan struct {
	Start int
	End   int
}

// Len is the span's width in bytes.
func (s ArgSpan) Len() int { return s.End - s.Start }

// ArgKind classifies an argument's VALUE expression for the one question a rewriter has to answer
// before it touches it: may these bytes be replaced by a string literal without changing anything
// else the call site meant?
//
// This is deliberately a closed enum rather than a bool. "Literal or not" cannot express the case
// that actually bites — a string-SHAPED value that splices runtime data in (`f"triage {ticket}"`,
// “ `${sys}\n${q}` “, `"hi $name"`). A bool forces that case into one of two wrong answers: call it
// a literal and a rewrite silently DROPS `ticket` from the prompt (a diff that compiles and quietly
// corrupts every eval run under it — the exact failure rewritePrompt refuses in Go), or call it an
// expression and lose that it was a string at all. Naming it is what lets the rewriter refuse it for
// the right reason.
type ArgKind string

const (
	// ArgLiteralString: one static string literal. Its bytes may be replaced by another string
	// literal — the rewrite is type-preserving by construction, on any SDK, without the engine
	// knowing the SDK (the property ADR-001 requirement 2 leans on).
	ArgLiteralString ArgKind = "literal_string"
	// ArgInterpolatedString: string-shaped, but it splices runtime values in. NOT replaceable as a
	// whole. 🚫 A rewriter must refuse this, not treat it as its literal segments.
	ArgInterpolatedString ArgKind = "interpolated_string"
	// ArgExpression: anything else — a variable, a call, a list, a dict/object construction. Present
	// (so it HAS a span a model override can replace), but not a static text (so a prompt override
	// cannot claim to know what it says).
	ArgExpression ArgKind = "expression"
)

// ArgValue is one keyword/named argument the call site WRITES, normalized to what a language-neutral
// rewriter needs: where its value is, and whether that value may be replaced.
//
// 🔴 Kind and Text answer DIFFERENT questions and are deliberately independent — neither is derivable
// from the other:
//
//   - Text is a READ fact: what the type-free resolution floor (10.5) extracts from this value for the
//     IR. Its rule is unchanged by the introduction of spans, which is what keeps the emitted IR
//     byte-identical.
//   - Kind is a WRITE fact: whether a rewriter may replace Value's bytes.
//
// They disagree in both directions, and each disagreement is a real case, not an oversight:
// an f-string has Text (the floor has always read its first static segment) but is NOT replaceable;
// a substitution-free JS template literal IS replaceable but has no Text (the floor has never read
// template literals, and making it start would change the IR — a separate decision, not this one's).
type ArgValue struct {
	// Value is the byte span of the value expression — the bytes a rewrite of this argument replaces.
	// Always populated: an ArgValue exists only because an argument was located.
	Value ArgSpan
	// Kind says whether Value's bytes may be replaced. See ArgKind.
	Kind ArgKind
	// Text is the static text the resolution floor reads out of this value; "" when it reads none.
	// 🚫 Not the inverse of Kind — see the type comment.
	Text string
}

// ArgUnpacking is an unpacking at a call site — Python's `**kwargs` or `*args` — through which the
// call can supply arguments that the source text does not name.
//
// 🔴 It exists to kill a premise that reads as obviously true and is false. ArgInsert's whole
// justification is: "the argument is ABSENT, therefore the SDK's default applies, therefore inserting
// it is an OVERRIDE". Under an unpacking the first step already fails — `create(**kwargs)` may be
// passing `model` at runtime and the source says nothing either way, so "absent from the text" and
// "absent from the call" stop being the same statement. Appending then does not override the model;
// it makes the call raise:
//
//	create(**kwargs, model="gpt-4o")   ->   TypeError: got multiple values for keyword argument 'model'
//
// And nothing downstream catches it. `py_compile` PASSES — the file parses, the gate is green, the
// record says `built`, and the reviewer sees a green badge over a call that cannot execute. That is
// ADR-001's top-named risk ("a bad codemod can break a build or subtly change behavior") landing in
// the one language whose gate cannot see it (ADR-003: Python's floor is syntax, not types).
//
// The syntactic floor cannot prove the key is absent from the dict — that is a whole-program data-flow
// question, and in the real case that produced this type the dict is written two lines up
// (`kwargs["model"] = refreshed_model`). So the honest answer is to refuse, and to say why.
//
// 🚫 The engine must not try to be clever here — rewriting the `kwargs["model"] = …` above, or
// filtering the key out of the dict, is guessing on customer source, which is the failure this whole
// package is shaped to avoid.
//
// # This is a fact about REPLACEMENT vs INSERTION, not about splats in general
//
// An unpacking does NOT block replacing a keyword the call site DID write. `create(**kwargs,
// model="old")` already raises today if `kwargs` carries `model` — that breakage is the target's, not
// ours — and where it does not, replacing `"old"` is exactly the override that was asked for. Only
// the insertion's premise breaks, so only the insertion is refused. Blocking the replacement too
// would be over-reaching past the defect.
type ArgUnpacking struct {
	// Text is the unpacking's source text — `**kwargs`, `*args`. The refusal quotes it, because
	// "we cannot prove `**kwargs` does not already carry model" sends a human to the right line and
	// "this call site is not rewritable" does not.
	Text string
}

// ArgInsert is where and how a keyword argument the call site does NOT write could be added.
//
// It exists because "the call site never pinned a model" is a normal case that still needs
// overriding, not a reason to refuse: a call with no model argument still HAS a model (the SDK's
// default). The Go rewriter already inserts one (rewriteModel's absent-field branch); a
// language-neutral shape that could only replace what was already written would make every
// unpinned Python/TS call site un-overridable, which is most of them.
//
// A nil *ArgInsert means this call site offers NO place to add a keyword argument, and that is a
// fact about the language or the call, never a default: Java and Rust have no named arguments at
// all, and a JS call with no object-literal argument has no object to add a key to. A rewriter must
// refuse those loudly rather than synthesize a container.
//
// The rewriter splices:  Prefix + name + Assign + <value expression>  at At.
type ArgInsert struct {
	// At is the byte offset to splice at.
	At int
	// Prefix is the text that must precede the new argument to keep the container valid: "" when the
	// container is empty or already ends in a separator, ", " when an argument precedes it.
	Prefix string
	// Assign is how this LANGUAGE binds a keyword-argument name to its value: "=" (Python),
	// ": " (JS/TS object literal), " = " (Kotlin). It lives here, filled in by each analyzer, rather
	// than in a table on the rewriter's side, because it is per-language SYNTAX and this package is
	// where per-language syntax already lives. A copy on the rewriter's side would be a second place
	// to remember when a seventh language arrives (禁止分裂 source-of-truth).
	Assign string
}

// RawCallSite is a language-neutral call site produced by a syntactic (type-free) analyzer — the
// normalized shape the tree-sitter substrate yields (10.4). It carries exactly what the shared detector
// needs: the call target's root + selector chain, the enclosing symbol, the source span, an invocation
// hint (loop/conditional), and any keyword args (for the resolution floor, 10.5, and for the
// language-neutral apply path, ADR-003).
type RawCallSite struct {
	Root              string              // leftmost identifier of the call target ("" if not an ident)
	RootIdent         bool                // true when Root is a bare identifier
	Chain             []string            // selector names after the root, e.g. ["messages","create"]
	EnclosingSymbol   string              // enclosing function/method/class FQN, or "<module>"
	LineStart         int                 // 1-based
	LineEnd           int                 // 1-based
	Invocation        string              // "single" | "loop" | "conditional" (from surrounding control flow)
	Keywords          map[string]ArgValue // keyword/object-arg name -> located value (floor + rewrite)
	KeywordInsert     *ArgInsert          // where a keyword arg could be ADDED; nil => nowhere (see ArgInsert)
	KeywordUnpacking  *ArgUnpacking       // the unpacking that made KeywordInsert nil; nil otherwise
	PositionalStrings []string            // string-literal positional args in order (for framework builder calls)
}

// argInsertAtEnd builds an ArgInsert that splices at the END of a delimited container (an argument
// list, an object literal) — just before `closer`, the container's final token.
//
// 🔴 At the END, not just inside the opening delimiter, and that is not a formatting preference.
// Python and Kotlin both reject a keyword argument written BEFORE a positional one, so splicing at
// the front turns `create(ticket)` into `create(model="x", ticket)` — a SyntaxError. The verifier
// would catch it (ADR-003), but only by rejecting a call site that was perfectly rewritable: an
// engine that manufactures its own build breaks burns the trust the gate exists to earn.
//
// 🔴 Appending at the end is also what makes a JS/TS object spread SAFE rather than a hazard, and
// that is load-bearing rather than lucky — see jsArgUnpackings. Moving the insertion to the front
// would silently invert every `{...opts}` override into a no-op.
//
// Returns nil when the container is absent, when its closing delimiter is missing, or when
// tree-sitter RECOVERED any part of the container rather than parsing it (an ERROR/MISSING node
// inside it). nil is the fail-closed answer — the rewriter refuses — and a plausible-looking offset
// into a region the parser was GUESSING at is how a codemod corrupts a file.
//
// 🔴 It also returns nil, plus the ArgUnpacking that caused it, when the container holds one of
// `unpackings` — a form through which the call may ALREADY be passing the argument we would append.
// Appending is still SYNTACTICALLY valid there, which is exactly why this check has to exist: the
// language's own gate has nothing to object to, and the resulting call raises at runtime under a
// green `built` badge. See ArgUnpacking for the premise that breaks.
//
// The two returns are deliberately not one struct with a "blocked" flag. A blocked *ArgInsert would
// hand a rewriter a usable offset and TRUST it to notice the flag; returning nil means a rewriter
// that ignores the reason still cannot insert. The reason only improves the refusal — it can never
// be what stands between a splat and a corrupted file. Fail-closed by construction, not by
// convention.
//
// `unpackings` is per-language DATA supplied by the analyzer, and for two of the three languages it
// is deliberately nil. The set is a language's answer to "can an unpacking here supply the argument
// we are about to append, in a way this language's gate would not catch?" — and that answer really
// does differ (see pyArgUnpackings, jsArgUnpackings, kotlinArgUnpackings). A single hardcoded list of
// splat-ish node types would be a lie in both directions: it would block JS/TS rewrites that are
// correct, and it would say nothing about why Python's are not.
//
// The scan is over the container's DIRECT children, which is where Python's `dictionary_splat` /
// `list_splat` and JS's `spread_element` both sit. ⚠️ Kotlin nests its `spread_expression` one level
// down inside a `value_argument`; kotlinArgUnpackings is nil so nothing is missed today, but a future
// Kotlin set would need a deeper scan than this.
//
// 🔴 Note the asymmetry with ArgValue.Value, which is deliberate rather than an inconsistency. A
// replacement span reuses a position the parser handed us for a node it actually built. An insertion
// INVENTS a position from the container's structure — "where would a new argument go?" — and that
// question has no honest answer when the structure is a recovery guess. Different levels of trust,
// so different rules. tree-sitter is error-TOLERANT (see syntaxErrorDiagnostics), so this case is
// reachable and silent: `create(model="m", ,)` yields a perfectly ordinary-looking argument list
// with a real closing paren and an ERROR node in the middle.
func argInsertAtEnd(container *sitter.Node, src []byte, closer, assign string, unpackings map[string]bool) (*ArgInsert, *ArgUnpacking) {
	if container == nil {
		return nil, nil
	}
	n := int(container.ChildCount())
	if n < 2 {
		return nil, nil
	}
	last := container.Child(n - 1)
	if last.Type() != closer || last.IsMissing() {
		return nil, nil
	}
	if container.HasError() {
		return nil, nil
	}
	for i := 0; i < int(container.NamedChildCount()); i++ {
		if c := container.NamedChild(i); unpackings[c.Type()] {
			return nil, &ArgUnpacking{Text: c.Content(src)}
		}
	}
	ins := &ArgInsert{At: int(last.StartByte()), Assign: assign}
	switch {
	case container.NamedChildCount() == 0:
		ins.Prefix = "" // empty container: nothing to separate from
	case container.Child(n-2).Type() == ",":
		ins.Prefix = "" // a trailing comma already separates
	default:
		ins.Prefix = ", "
	}
	return ins, nil
}

// spanOf is a node's byte range.
func spanOf(n *sitter.Node) ArgSpan {
	return ArgSpan{Start: int(n.StartByte()), End: int(n.EndByte())}
}

// namedChildAfter returns the first named child of n that starts after `prev` ends.
func namedChildAfter(n, prev *sitter.Node) *sitter.Node {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if c := n.NamedChild(i); c.StartByte() >= prev.EndByte() {
			return c
		}
	}
	return nil
}

// SyntacticUnit is one analyzed source file in normalized form.
type SyntacticUnit struct {
	RelPath     string
	PkgPath     string            // language-neutral module/package path for node identity (per-frontend spelling)
	Imports     map[string]string // local name -> module/import path
	ImportPaths map[string]bool   // set of imported module paths
	CallSites   []RawCallSite
	// Src is the unit's source bytes, filled in by the walker after Analyze returns.
	//
	// It exists because the P14 tool split has to read the TEXT an argument span points at, and the parse
	// tree is closed by then (`defer tree.Close()`) — the same constraint that made ArgValue carry spans
	// rather than nodes. Carrying the bytes the spans are relative to is what makes those spans usable
	// without re-reading the file and risking a different revision. Analyzers do not set it; the walker
	// does, so no analyzer can forget.
	Src []byte
	// Language is the analyzer's language label, carried for the same reason SpanCallSite.Language is:
	// the code that splits a written list has to know whose syntax to split it as, and a caller that had
	// to remember would eventually name the wrong one.
	Language string
}

// LanguageAnalyzer parses ONE language's source into normalized units. The tree-sitter parser (or any
// other syntactic, type-free parser) lives behind this interface. It NEVER executes source (I1).
type LanguageAnalyzer interface {
	Language() string
	Extensions() []string
	Analyze(relPath string, src []byte) (SyntacticUnit, []Diagnostic)
}

// syntaxErrorDiagnostics reports a file whose tree-sitter parse contains syntax errors (失败要显眼).
//
// This exists because tree-sitter is ERROR-TOLERANT in a way go/parser is not: it never fails a parse, it
// recovers and returns a tree containing ERROR / MISSING nodes. Without this check a malformed source file
// is silently PARTIALLY analyzed — any call site inside the unparsable region just never appears, and the
// run reports zero problems. That is the exact "looks normal, is wrong" shape the report must never have:
// "why is this node missing?" has to stay answerable (I4).
//
// Severity is WARN, not ERROR, and the distinction is deliberate: the file was NOT skipped. tree-sitter
// recovered and the nodes outside the broken region ARE emitted, so counting it in summary.files_skipped
// (which is derived from error-severity diagnostics) would claim a skip that did not happen. The Go
// frontend's PARSE_ERROR is ERROR-severity precisely because go/parser yields no AST and the file really
// is skipped. Same code, honestly different severity, because the two situations genuinely differ.
func syntaxErrorDiagnostics(lang, rel string, root *sitter.Node) []Diagnostic {
	first, count := 0, 0
	var scan func(n *sitter.Node)
	scan = func(n *sitter.Node) {
		if n.IsError() || n.IsMissing() {
			count++
			if first == 0 {
				first = int(n.StartPoint().Row) + 1
			}
			return // do not descend into a broken subtree; one report per region
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			scan(n.Child(i))
		}
	}
	if !root.HasError() {
		return nil
	}
	scan(root)
	if count == 0 {
		return nil
	}
	return []Diagnostic{{
		Code: CodeParseError, Severity: SeverityWarn, File: rel, Line: first,
		Message: "tree-sitter: " + lang + " source has syntax errors; the file was recovered and analyzed " +
			"best-effort, so call sites inside the unparsable region(s) may be missing (partial extraction)",
	}}
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

	err := f.eachUnit(repo, &res.Diagnostics, func(unit SyntacticUnit) {
		res.FilesScanned++
		res.PackagesScanned++ // per-file granularity for syntactic frontends

		nodes, sites, merges := detectSyntacticUnit(unit, langReg, decl)
		res.Nodes = append(res.Nodes, nodes...)
		res.CallSites += sites
		res.Merges = append(res.Merges, merges...)
		graphs, fdiags := readFrameworks(f.Language(), unit)
		res.Frameworks = append(res.Frameworks, graphs...)
		res.Diagnostics = append(res.Diagnostics, fdiags...)
	})
	return res, err
}

// eachUnit walks the repo read-only and hands fn every analyzed unit this frontend handles.
//
// Extracted from Discover so the codemod's call-site index (IndexSpanCallSites) traverses through the
// SAME door rather than growing a second walk. The walk is not incidental detail: which directories are
// skipped, how a symlink is handled, and that a per-file panic degrades to a diagnostic instead of
// killing the run (I7) are all decisions, and a copy of them would drift — the index would find a call
// site in a directory Discover skips, produce a node_id no IR contains, and rewrite it.
//
// diags is appended to rather than returned so a caller that only wants units still cannot silently
// drop the walk's own diagnostics.
func (f *SyntacticFrontend) eachUnit(repo string, diags *[]Diagnostic, fn func(SyntacticUnit)) error {
	return filepath.WalkDir(repo, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			*diags = append(*diags, Diagnostic{Code: CodeWalkError, Severity: SeverityWarn, Message: werr.Error()})
			return nil
		}
		if d.IsDir() {
			if path != repo && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			*diags = append(*diags, Diagnostic{Code: CodeSymlinkCycleSkipped, Severity: SeverityWarn, Message: "symlink skipped"})
			return nil
		}
		if !f.Handles(path) {
			return nil
		}
		rel, _ := filepath.Rel(repo, path)
		rel = filepath.ToSlash(rel)
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			*diags = append(*diags, Diagnostic{Code: CodeWalkError, Severity: SeverityWarn, File: rel, Message: rerr.Error()})
			return nil
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					*diags = append(*diags, Diagnostic{Code: CodePackagePanic, Severity: SeverityWarn, File: rel, Message: "recovered panic analyzing file"})
				}
			}()
			unit, adiags := f.analyzer.Analyze(rel, src)
			unit.Src, unit.Language = src, f.Language()
			*diags = append(*diags, adiags...)
			fn(unit)
		}()
		return nil
	})
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
			Keywords: cs.Keywords, KeywordInsert: cs.KeywordInsert,
			KeywordUnpacking: cs.KeywordUnpacking, Invocation: cs.Invocation,
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

	// Binding sites are located AFTER matching, because only the matched row says which method or field
	// binds the value — and BEFORE Merge, so a merged site keeps the location its own row implied.
	for i := range sites {
		sites[i].BindingSites = locateRowBindings(unit.Src, unit.Language, sites[i])
	}

	merged, merges := Merge(sites)
	nodes := make([]ExtractedNode, 0, len(merged))
	for _, s := range merged {
		nodes = append(nodes, extractSyntacticFloor(s, unit.Src, unit.Language))
	}
	return nodes, len(sites), merges
}

// extractSyntacticFloor is the type-free extraction floor (10.5, resolves PRD Q7): resolve a field only
// when it is a keyword string literal; otherwise emit the `unresolved` sentinel + a flagged reason. It
// never guesses (I5). This is the honest boundary for tree-sitter frontends (no type resolution).
func extractSyntacticFloor(s DetectedCallSite, unitSrc []byte, language string) ExtractedNode {
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
	// 🔴 P14 wave 14d: the tool split is RECORDED here, for every syntactic language with a list
	// splitter. `ToolsSkills` above stays `[]` — it is frozen, and its emptiness is part of the bytes a
	// pre-P14 IR reproduces; the split fields are nil-when-empty and additive beside it.
	n.Tools, n.Skills = classifySyntacticToolsSkills(s, unitSrc, language)
	n.Context = ContextAssembly{Policy: "inline_messages", Description: "static call site (syntactic frontend; no type resolution)"}
	return n
}

// keywordFor resolves an ArgLocator against the call's keyword args (the only thing a type-free
// parser can resolve). Returns the floor's static text and true only for a ParamName locator whose
// keyword the floor could read a non-empty text out of.
//
// It reads ArgValue.Text and NOT ArgValue.Kind, and the distinction is what keeps this the same
// resolution it has always been: the map now also carries arguments the floor cannot read (they are
// there for their SPANS, so a rewriter can replace `model=MODEL_ID`), and those have Text == "" and
// are rejected here exactly as they were when they were absent from the map entirely.
func keywordFor(s DetectedCallSite, loc *ArgLocator) (string, bool) {
	if loc == nil || loc.Form != LocParamName {
		return "", false
	}
	v := s.Keywords[loc.Name]
	return v.Text, v.Text != ""
}

// langGraphReader reads a LangGraph/LangChain declarative state graph: add_node / add_edge /
// add_conditional_edges / set_entry_point (snake_case Python, camelCase JS).
type langGraphReader struct{}

func (langGraphReader) Name() string { return "langgraph" }

func (langGraphReader) Read(unit SyntacticUnit) (FrameworkGraph, []Diagnostic, bool) {
	if !unitImports(unit, "langgraph", "langchain") {
		return FrameworkGraph{}, nil, false
	}
	g := FrameworkGraph{FrameworkSource: "langgraph", Version: "v0", Recognized: true, SubgraphID: "sg_" + sanitizeID(unit.PkgPath)}
	readBuilderGraph(&g, unit, normalizeBuilderMethod)
	if len(g.Nodes) == 0 {
		return FrameworkGraph{}, nil, false // imported langgraph but declared no graph
	}
	return g, nil, true
}

// crewAIReader reads a CrewAI crew: it recognizes Agent(role=...) definitions as nodes and the presence
// of a Crew(...) / .kickoff(). CrewAI's process is sequential/hierarchical rather than explicit edges, so
// no edges are emitted; if the crew is present but agents are not role-named, the subgraph is degraded
// (present but topology unresolved — honest, never guessed).
type crewAIReader struct{}

func (crewAIReader) Name() string { return "crewai" }

func (crewAIReader) Read(unit SyntacticUnit) (FrameworkGraph, []Diagnostic, bool) {
	if !unitImports(unit, "crewai") {
		return FrameworkGraph{}, nil, false
	}
	g := FrameworkGraph{FrameworkSource: "crewai", Version: "v0", Recognized: true, SubgraphID: "sg_crew_" + sanitizeID(unit.PkgPath)}
	nodeSet := map[string]bool{}
	sawCrew := false
	for _, cs := range unit.CallSites {
		switch builderLeaf(cs) {
		case "Agent":
			if role := cs.Keywords["role"]; role.Text != "" {
				nodeSet[role.Text] = true
			}
		case "Crew", "kickoff":
			sawCrew = true
		}
	}
	if !sawCrew && len(nodeSet) == 0 {
		return FrameworkGraph{}, nil, false // imported crewai but no crew declared
	}
	for n := range nodeSet {
		g.Nodes = append(g.Nodes, n)
	}
	sortStrings(g.Nodes)
	g.Degraded = len(g.Nodes) == 0 // crew present but no role-named agents => topology unresolved
	return g, nil, true
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
