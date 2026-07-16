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
	Context     ContextAssembly
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
		n.Ambiguities = []AmbiguityFlag{
			flag(s.NodeID, "model", CodeModelUnresolved, "detect_only declaration resolves no model"),
			flag(s.NodeID, "prompt", CodePromptUnresolved, "detect_only declaration resolves no prompt"),
		}
		return n
	}

	n.Model, n.Ambiguities = extractModel(s, m, fset, n.Ambiguities)
	n.Prompt, n.Ambiguities = extractPrompt(s, m, fset, n.Ambiguities)
	n.ToolsSkills = extractTools(s, fset)
	n.Context = deriveContext(n)
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
