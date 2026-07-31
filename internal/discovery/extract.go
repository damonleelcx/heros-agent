package discovery

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"strconv"
	"strings"
)

// ExtractedNode is one detected call site with its metadata resolved (§4.1) — the model binding, prompt
// construction, tools, context policy, invocation semantics, and any ambiguity flags. Statically
// unresolvable fields carry the UnresolvedSentinel and a matching ambiguity flag; they are never omitted
// and never guessed (FR3/FR8, invariant I5). This is the in-memory node the IR emitter (§5) serializes.
type ExtractedNode struct {
	NodeID      string
	Site        DetectedCallSite
	Model       ResolvedModel
	Prompt      ResolvedPrompt
	ToolsSkills []string
	// Tools and Skills are the P14 split of ToolsSkills (decisions.md D-14.1), classified HERE because
	// the frontend is the only component that can see which discovered entry is which. Nil when the node
	// declares none, so the emitted IR omits them and a pre-P14 document round-trips byte-identically.
	Tools   []IRTool
	Skills  []string
	Context ContextAssembly
	// Memory is the memory strategy this call site already implements — what the node carries ACROSS
	// invocations (P17 task 6.2). Derived by the frontend, which is the only component that sees the
	// call site, so no consumer has to re-derive it (a re-derivation would be a second classifier, and
	// two classifiers are two answers).
	Memory string
	// Harness is the control loop this call site already runs inside — the node's scaffold (P18 task 4.1).
	// Derived by the frontend for the same reason Memory is: it is the only component that sees the call
	// site, so no consumer has to re-derive it.
	Harness     string
	Invocation  InvocationSemantics
	Ambiguities []AmbiguityFlag
}

// ResolvedModel is the model binding at a call site. Unresolved => sentinel provider/model_id (I5).
type ResolvedModel struct {
	Provider   string
	ModelID    string
	Params     map[string]any
	Unresolved bool
}

// ResolvedPrompt is the prompt at a call site: exactly one of Inline/TemplateRef, plus variable slots.
type ResolvedPrompt struct {
	Inline      string
	TemplateRef string
	Variables   []string
	Unresolved  bool
}

// ContextAssembly is the context-building policy (schema requires a non-empty policy — doc 01 Finding C).
type ContextAssembly struct {
	Policy      string
	Description string
}

// InvocationSemantics is the static-vs-runtime distinction (§4.5, FR6). VariableAtRuntime is set for
// loop/agent nodes; a fixed runtime count is NEVER emitted (invariant I2).
type InvocationSemantics struct {
	Type              string // "single" | "loop" | "conditional"
	VariableAtRuntime bool
}

// AmbiguityFlag marks an unresolved field as a P5 dynamic-trace candidate with a machine-readable reason
// (§4.2, FR8, doc 09). AI Eng owns the fidelity judgment behind these.
type AmbiguityFlag struct {
	NodeID      string `json:"node_id"`
	Field       string `json:"field"`
	Reason      string `json:"reason"`
	Code        string `json:"code"`
	P5Candidate bool   `json:"p5_candidate"`
}

// ExtractFile resolves metadata for every detected site in a single file. It walks the file once to
// compute per-call context (loop/conditional nesting, enclosing function body), then extracts each node.
func ExtractFile(f *ParsedFile, sites []DetectedCallSite) []ExtractedNode {
	meta := analyzeFile(f)
	var out []ExtractedNode
	for _, s := range sites {
		if s.File != f {
			continue
		}
		out = append(out, extractOne(s, meta[s.Call]))
	}
	return out
}

// callMeta is the static context of one call site, gathered in a single walk.
type callMeta struct {
	loopDepth int
	condDepth int
	funcBody  *ast.BlockStmt // enclosing function body, for intra-procedural resolution
}

func analyzeFile(f *ParsedFile) map[*ast.CallExpr]callMeta {
	m := map[*ast.CallExpr]callMeta{}
	ast.Walk(&metaVisitor{out: m, maxDepth: defaultMaxWalkDepth}, f.AST)
	return m
}

type metaVisitor struct {
	out       map[*ast.CallExpr]callMeta
	loopDepth int
	condDepth int
	funcBody  *ast.BlockStmt
	depth     int
	maxDepth  int
}

func (v *metaVisitor) Visit(n ast.Node) ast.Visitor {
	if n == nil || v.depth > v.maxDepth {
		return nil
	}
	nv := *v
	nv.depth = v.depth + 1
	switch x := n.(type) {
	case *ast.FuncDecl:
		nv.funcBody = x.Body
	case *ast.FuncLit:
		nv.funcBody = x.Body
	case *ast.ForStmt, *ast.RangeStmt:
		nv.loopDepth = v.loopDepth + 1
	case *ast.IfStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
		nv.condDepth = v.condDepth + 1
	case *ast.CallExpr:
		v.out[x] = callMeta{loopDepth: v.loopDepth, condDepth: v.condDepth, funcBody: v.funcBody}
	}
	return &nv
}

func extractOne(s DetectedCallSite, m callMeta) ExtractedNode {
	n := ExtractedNode{NodeID: s.NodeID, Site: s}
	fset := s.File.Fset

	// §4.5 invocation semantics — declared override never downgrades; a surrounding loop upgrades.
	n.Invocation = invocationFor(m, s.Invocation)

	if s.DetectOnly {
		n.Model = ResolvedModel{Provider: UnresolvedSentinel, ModelID: UnresolvedSentinel, Unresolved: true}
		n.Prompt = ResolvedPrompt{Unresolved: true}
		n.Context = ContextAssembly{Policy: "unresolved", Description: "detect-only declaration: metadata not resolved"}
		// Memory is `none` here for the same reason it is `none` everywhere else, NOT "unresolved":
		// a cross-invocation store was never statically visible at any call site, so a detect-only
		// declaration is not missing an answer this engine could otherwise have given. Marking it
		// unresolved would imply a resolvable fact was skipped and would leave the resolver without a
		// concrete base (see deriveMemory).
		n.Memory = deriveMemory(n)
		n.Harness = deriveHarness(n)
		n.Ambiguities = []AmbiguityFlag{
			flag(s.NodeID, "model", CodeModelUnresolved, "detect_only declaration resolves no model"),
			flag(s.NodeID, "prompt", CodePromptUnresolved, "detect_only declaration resolves no prompt"),
		}
		return n
	}

	n.Model, n.Ambiguities = extractModel(s, m, fset, n.Ambiguities)
	n.Prompt, n.Ambiguities = extractPrompt(s, m, fset, n.Ambiguities)
	n.ToolsSkills = extractTools(s, fset)
	n.Tools, n.Skills = classifyToolsSkills(s, fset)
	n.Context = deriveContext(n)
	n.Memory = deriveMemory(n)
	n.Harness = deriveHarness(n)
	return n
}

func invocationFor(m callMeta, declared string) InvocationSemantics {
	if declared == "loop" || m.loopDepth > 0 {
		return InvocationSemantics{Type: "loop", VariableAtRuntime: true}
	}
	if declared == "conditional" || m.condDepth > 0 {
		// 0-or-1 firings: conditional, but statically bounded — not a variable count (I2).
		return InvocationSemantics{Type: "conditional", VariableAtRuntime: false}
	}
	return InvocationSemantics{Type: "single", VariableAtRuntime: false}
}

func extractModel(s DetectedCallSite, m callMeta, fset *token.FileSet, amb []AmbiguityFlag) (ResolvedModel, []AmbiguityFlag) {
	loc := s.ArgMap.Model
	provider := s.ProviderHint
	if provider == "" {
		provider = UnresolvedSentinel
	}
	unres := func(code, reason string) (ResolvedModel, []AmbiguityFlag) {
		return ResolvedModel{Provider: provider, ModelID: UnresolvedSentinel, Unresolved: true},
			append(amb, flag(s.NodeID, "model", code, reason))
	}
	if loc == nil {
		if s.ProviderHint == "" {
			return unres(CodeModelConstructionBound, "model not bound at the call site (construction/env-bound)")
		}
		return unres(CodeModelUnresolved, "no model locator for this entrypoint")
	}
	expr, ok := locateArg(s.Call, loc, fset)
	if !ok {
		return unres(CodeModelConstructionBound, "model argument not present at the call site (construction/env-bound)")
	}
	val := resolveExpr(expr, m.funcBody, loc.Unwrap, fset, 0)
	if val.Unresolved {
		return unres(CodeModelUnresolved, "model argument is not a static literal/constant")
	}
	return ResolvedModel{Provider: provider, ModelID: val.Text, Params: map[string]any{}}, amb
}

func extractPrompt(s DetectedCallSite, m callMeta, fset *token.FileSet, amb []AmbiguityFlag) (ResolvedPrompt, []AmbiguityFlag) {
	loc := s.ArgMap.Prompt
	unres := func(code, reason string) (ResolvedPrompt, []AmbiguityFlag) {
		return ResolvedPrompt{Unresolved: true, Variables: []string{}},
			append(amb, flag(s.NodeID, "prompt", code, reason))
	}
	if loc == nil {
		return unres(CodePromptUnresolved, "no prompt locator for this entrypoint")
	}
	if loc.Form == LocOpaque {
		return unres(CodePromptOpaqueBody, "prompt lives in an opaque serialized body (e.g. Bedrock InvokeModel Body)")
	}
	expr, ok := locateArg(s.Call, loc, fset)
	if !ok {
		return unres(CodePromptUnresolved, "prompt argument not present at the call site")
	}
	// A composite literal (message slice / struct) is a constructed prompt — honest unresolved (P5).
	if _, isComposite := unwrapExpr(expr).(*ast.CompositeLit); isComposite {
		return unres(CodePromptConstructed, "prompt built from a constructed message list")
	}
	val := resolveExpr(expr, m.funcBody, loc.Unwrap, fset, 0)
	if val.Unresolved || val.Kind != "literal" {
		return unres(CodePromptConstructed, "prompt is not a static string literal (assembled at runtime)")
	}
	return ResolvedPrompt{Inline: val.Text, Variables: []string{}}, amb
}

func extractTools(s DetectedCallSite, fset *token.FileSet) []string {
	loc := s.ArgMap.Tools
	if loc == nil {
		return []string{}
	}
	if loc.Form == LocConst {
		// declaration asserted a literal tools value (e.g. []) — honor it as "no tools".
		return []string{}
	}
	expr, ok := locateArg(s.Call, loc, fset)
	if !ok {
		return []string{}
	}
	cl, isComposite := unwrapExpr(expr).(*ast.CompositeLit)
	if !isComposite {
		return []string{}
	}
	var tools []string
	for _, el := range cl.Elts {
		if t := renderExpr(fset, el); t != "" {
			tools = append(tools, t)
		}
	}
	return tools
}

// classifyToolsSkills splits the call site's declared entries into provider-native TOOLS and
// registered platform SKILLS (P14 task 4.2, decisions.md D-14.1).
//
// # The classification is recorded, not left to a consumer
//
// The two have opposite apply mechanics — a tool is selected (pruned), a skill is bound (constructed) —
// so somebody has to say which is which. The frontend is the only component with the evidence: it is
// looking at the declaration. A downstream re-derivation would be a second classifier, and two
// classifiers are two answers to a question that has one.
//
// # 🔴 Fail closed: the default is TOOL
//
// A provider-native tool that happens to WRAP a platform skill is recorded as a tool (PRD §14 Q3),
// because a tool is what the call site declares. Getting that wrong in the other direction is the
// dangerous one: classifying a provider tool as a skill would offer it to the BINDING path, which
// CONSTRUCTS a value — so a misclassification would replace a tool the author wrote with a
// reconstruction of something else. Classifying a skill as a tool merely means it is pruned rather than
// re-bound, which the discovered-set validation still keeps honest.
//
// # nil vs empty
//
// Both results are nil when the node declares nothing, so the emitted IR omits both fields and a node
// that predates the split serialises byte-identically.
func classifyToolsSkills(s DetectedCallSite, fset *token.FileSet) ([]IRTool, []string) {
	loc := s.ArgMap.Tools
	if loc == nil || loc.Form == LocConst {
		return nil, nil
	}
	expr, ok := locateArg(s.Call, loc, fset)
	if !ok {
		return nil, nil
	}
	inner := unwrapExpr(expr)
	cl, isComposite := inner.(*ast.CompositeLit)
	if !isComposite {
		// 🔴 A tool set assembled at runtime (`Tools: buildTools(ctx)`). It is RECORDED, as one entry with
		// no location, rather than dropped: "this node offers tools we cannot address" and "this node
		// offers no tools" are different facts, and only the first one must make a prune refuse (FR14).
		// Dropping it would leave a prune reporting "no such tool" about a set that is plainly right there.
		name := renderExpr(fset, inner)
		if name == "" {
			return nil, nil
		}
		return []IRTool{{Name: name}}, nil
	}
	var tools []IRTool
	var skills []string
	for i, el := range cl.Elts {
		text := renderExpr(fset, el)
		if text == "" {
			continue
		}
		if name, isSkill := platformSkillName(text); isSkill {
			skills = append(skills, name)
			continue
		}
		tools = append(tools, IRTool{
			Name:       text,
			DeclaredAt: &IRToolLocation{Line: fset.Position(el.Pos()).Line, Index: i},
		})
	}
	return tools, skills
}

// platformSkillForms are the call-site spellings that identify a REGISTERED PLATFORM SKILL rather than
// a provider-native tool, and the whole of what this classifier will accept as evidence.
//
// The list is short because the evidence is: a platform skill is a thing the platform binds by handle,
// so the only honest marker at a call site is a reference to that handle. Everything else — an SDK tool
// struct, a helper the target repo wrote, a variable — is a tool, which is the fail-closed direction
// (see classifyToolsSkills). Adding a form here is adding a row, and a row is a claim that this
// spelling can only mean a registered skill.
var platformSkillForms = []string{
	"heros.Skill(",  // the platform's Go binding helper
	"heros.Skills.", // a named skill from the generated skill set
	`"skill://`,     // a skill URI written inline
	`"builtin:`,     // a registry impl_handle written inline
}

// platformSkillName reports whether a declared entry names a registered platform skill, and if so the
// name it goes by. The name is the first quoted string in the entry when there is one — that is the
// handle every recognised form carries — falling back to the entry's own text.
func platformSkillName(text string) (string, bool) {
	matched := false
	for _, f := range platformSkillForms {
		if strings.Contains(text, f) {
			matched = true
			break
		}
	}
	if !matched {
		return "", false
	}
	if q := firstQuoted(text); q != "" {
		return q, true
	}
	return text, true
}

// firstQuoted returns the first double-quoted substring's contents, or "".
func firstQuoted(s string) string {
	start := strings.IndexByte(s, '"')
	if start < 0 {
		return ""
	}
	end := strings.IndexByte(s[start+1:], '"')
	if end < 0 {
		return ""
	}
	return s[start+1 : start+1+end]
}

// deriveMemory records the memory strategy the call site already implements (P17 task 6.2).
//
// It returns `none` for every node, and that is a claim about the EVIDENCE rather than a placeholder or
// unfinished work. A memory strategy is a store read and written BETWEEN turns — which is precisely why
// `MemoryManagement` is a BEHAVIORAL pattern in the classifier, confirmed by "memory read/write against a
// store between turns" (internal/patternclassifier/taxonomy.go), not a structural one. P1 extracts a
// single call site; a cross-invocation store is not visible there, by definition.
//
// 🚫 The tempting alternative is to guess from context — an imported vector-store package, a variable
// named `history`, a `.save()` call nearby. That would be a plausible-but-wrong default of exactly the
// kind this engine declines everywhere else (the UnresolvedSentinel exists for the same reason): a node
// mislabelled `vector-recall` would resolve, hash, and be compared as a configuration the source never
// had. `none` is the honest floor, and what raises it is a trace-backed detector that can see the
// between-turns behaviour — a future memory-runtime phase's job, not a heuristic here.
func deriveMemory(ExtractedNode) string { return "none" }

// deriveHarness returns the scaffold this call site PROVES it runs inside, which today is always
// `single-shot` (P18 task 4.1).
//
// 🔴 The floor is `single-shot` because P1's evidence cannot distinguish the two things that look alike.
// `invocationFor` already records `loop` when the call sits inside one, and reaching for that here is the
// obvious move — but a `for` loop over a list of tickets fires one node many times with NO scaffold, while
// an agent loop is the MODEL choosing to take another turn. Loop depth cannot tell them apart, and the
// difference is the whole of the axis: one is a fan-out, the other is a bounded control loop whose turns
// are model-decided.
//
// 🚫 So this does not guess. Emitting `react-loop` because a call sat inside a `for` would hash a
// configuration nobody authored, and the resolver would then merge overrides onto a base that was never
// true. A detector that can prove a model-decided turn — a trace-backed one, not a syntactic one — is what
// raises this floor; until then the honest answer is the identity.
func deriveHarness(ExtractedNode) string { return "single-shot" }

func deriveContext(n ExtractedNode) ContextAssembly {
	// P1 has no context-strategy analysis (P3); record an honest, non-empty policy for the schema.
	if len(n.ToolsSkills) > 0 {
		return ContextAssembly{Policy: "inline_messages_with_tools", Description: "static message list plus bound tools at the call site"}
	}
	return ContextAssembly{Policy: "inline_messages", Description: "static message list at the call site"}
}

// ---- argument location & value resolution (bounded, intra-procedural) ----

// locateArg finds the AST expression an ArgLocator points at within a call (doc 04 §2.4).
func locateArg(call *ast.CallExpr, loc *ArgLocator, fset *token.FileSet) (ast.Expr, bool) {
	switch loc.Form {
	case LocPositional:
		if loc.Index >= 0 && loc.Index < len(call.Args) {
			return call.Args[loc.Index], true
		}
		return nil, false
	case LocFieldPath:
		field := loc.Field
		if i := strings.LastIndex(field, "."); i >= 0 {
			field = field[i+1:] // "params.Model" -> "Model"
		}
		cl := findStructArg(call)
		if cl == nil {
			return nil, false
		}
		return structFieldValue(cl, field)
	case LocOptionCtor:
		for _, a := range call.Args {
			if ce, ok := a.(*ast.CallExpr); ok {
				_, _, chain := selectorParts(ce.Fun)
				if len(chain) > 0 && chain[len(chain)-1] == loc.Option && len(ce.Args) > 0 {
					return ce.Args[0], true
				}
			}
		}
		return nil, false
	case LocParamName:
		// Without go/types we cannot map a param name to a positional index; treat as unresolved.
		return nil, false
	}
	return nil, false
}

func findStructArg(call *ast.CallExpr) *ast.CompositeLit {
	for _, a := range call.Args {
		if cl, ok := unwrapExpr(a).(*ast.CompositeLit); ok {
			return cl
		}
	}
	return nil
}

func structFieldValue(cl *ast.CompositeLit, name string) (ast.Expr, bool) {
	for _, el := range cl.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == name {
				return kv.Value, true
			}
		}
	}
	return nil, false
}

// resolvedValue is the outcome of resolving one expression to a static value.
type resolvedValue struct {
	Text       string
	Kind       string // "literal" | "symbolic" | "unresolved"
	Unresolved bool
}

// resolveExpr resolves an expression to a static value, seeing through value-wrappers (aws.String,
// anthropic.F) and doing one level of intra-procedural ident resolution. Bounded by depth (I7).
func resolveExpr(e ast.Expr, body *ast.BlockStmt, unwrap []string, fset *token.FileSet, depth int) resolvedValue {
	if e == nil || depth > 8 {
		return resolvedValue{Unresolved: true}
	}
	e = unwrapWrappers(e, unwrap)
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			if s, err := unquoteLit(x.Value); err == nil {
				return resolvedValue{Text: s, Kind: "literal"}
			}
		}
		return resolvedValue{Text: x.Value, Kind: "literal"}
	case *ast.SelectorExpr:
		// A package-qualified constant (anthropic.ModelClaudeOpus4_6): statically present but symbolic.
		return resolvedValue{Text: renderExpr(fset, x), Kind: "symbolic"}
	case *ast.Ident:
		if v, ok := resolveIdent(x.Name, body); ok {
			return resolveExpr(v, body, nil, fset, depth+1)
		}
		return resolvedValue{Unresolved: true}
	}
	return resolvedValue{Unresolved: true}
}

// resolveIdent finds the last static assignment/definition of name in a function body (intra-procedural).
func resolveIdent(name string, body *ast.BlockStmt) (ast.Expr, bool) {
	if body == nil {
		return nil, false
	}
	var found ast.Expr
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range x.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name == name && i < len(x.Rhs) {
					found = x.Rhs[i]
				}
			}
		case *ast.ValueSpec:
			for i, nm := range x.Names {
				if nm.Name == name && i < len(x.Values) {
					found = x.Values[i]
				}
			}
		}
		return true
	})
	return found, found != nil
}

// unwrapExpr strips parens and a leading address-of so we can see the underlying node.
func unwrapExpr(e ast.Expr) ast.Expr {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.UnaryExpr:
			if x.Op == token.AND {
				e = x.X
				continue
			}
			return e
		default:
			return e
		}
	}
}

// unwrapWrappers sees through known value-wrapper calls (aws.String, anthropic.F, openai.F) to the value.
func unwrapWrappers(e ast.Expr, unwrap []string) ast.Expr {
	if len(unwrap) == 0 {
		return e
	}
	if ce, ok := e.(*ast.CallExpr); ok && len(ce.Args) > 0 {
		root, _, chain := selectorParts(ce.Fun)
		name := root
		if len(chain) > 0 {
			name = root + "." + strings.Join(chain, ".")
		}
		for _, w := range unwrap {
			if name == w || (len(chain) > 0 && chain[len(chain)-1] == lastDot(w)) {
				return unwrapWrappers(ce.Args[0], unwrap)
			}
		}
	}
	return e
}

func lastDot(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

func renderExpr(fset *token.FileSet, e ast.Expr) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, e); err != nil {
		return ""
	}
	return b.String()
}

func unquoteLit(s string) (string, error) {
	return strconv.Unquote(s)
}

func flag(nodeID, field, code, reason string) AmbiguityFlag {
	return AmbiguityFlag{NodeID: nodeID, Field: field, Reason: reason, Code: code, P5Candidate: true}
}
