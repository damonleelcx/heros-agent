package discovery

import (
	"context"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"
)

// pythonAnalyzer parses Python via tree-sitter (a pure parser — it NEVER executes source, which is why
// tree-sitter is the multi-language substrate, I1). It yields the normalized SyntacticUnit the shared
// core consumes. Resolution is syntactic (no types): import-presence + selector + declared entrypoints +
// keyword string literals (the 10.5 floor).
type pythonAnalyzer struct{}

// NewPythonFrontend returns the tree-sitter-backed Python LanguageFrontend.
func NewPythonFrontend() LanguageFrontend { return NewSyntacticFrontend(&pythonAnalyzer{}) }

func (*pythonAnalyzer) Language() string     { return "python" }
func (*pythonAnalyzer) Extensions() []string { return []string{".py", ".pyi"} }

func (a *pythonAnalyzer) Analyze(rel string, src []byte) (SyntacticUnit, []Diagnostic) {
	unit := SyntacticUnit{RelPath: rel, PkgPath: pyPkgPath(rel), Imports: map[string]string{}, ImportPaths: map[string]bool{}}

	parser := sitter.NewParser()
	parser.SetLanguage(python.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil || tree == nil {
		return unit, []Diagnostic{{Code: CodeParseError, Severity: SeverityError, File: rel, Message: "tree-sitter: python parse failed"}}
	}
	defer tree.Close()

	root := tree.RootNode()
	collectPyImports(root, src, &unit)
	walkPyCalls(root, src, "<module>", 0, 0, &unit)
	return unit, nil
}

// pyPkgPath derives a stable, language-neutral module path from the file path: "pkg/svc.py" -> "pkg.svc".
func pyPkgPath(rel string) string {
	p := strings.TrimSuffix(rel, ".py")
	p = strings.TrimSuffix(p, ".pyi")
	p = strings.TrimSuffix(p, "/__init__")
	return strings.ReplaceAll(p, "/", ".")
}

func collectPyImports(n *sitter.Node, src []byte, unit *SyntacticUnit) {
	switch n.Type() {
	case "import_statement":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			switch c.Type() {
			case "dotted_name", "identifier":
				mod := c.Content(src)
				unit.Imports[lastDotSeg(mod)] = mod
				unit.ImportPaths[mod] = true
			case "aliased_import":
				name := c.ChildByFieldName("name")
				alias := c.ChildByFieldName("alias")
				if name != nil {
					mod := name.Content(src)
					unit.ImportPaths[mod] = true
					if alias != nil {
						unit.Imports[alias.Content(src)] = mod
					} else {
						unit.Imports[lastDotSeg(mod)] = mod
					}
				}
			}
		}
	case "import_from_statement":
		modNode := n.ChildByFieldName("module_name")
		mod := ""
		if modNode != nil {
			mod = modNode.Content(src)
			unit.ImportPaths[mod] = true
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if modNode != nil && c == modNode {
				continue
			}
			switch c.Type() {
			case "dotted_name", "identifier":
				unit.Imports[lastDotSeg(c.Content(src))] = mod
			case "aliased_import":
				alias := c.ChildByFieldName("alias")
				if alias != nil {
					unit.Imports[alias.Content(src)] = mod
				}
			}
		}
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		collectPyImports(n.NamedChild(i), src, unit)
	}
}

// walkPyCalls walks the tree tracking the enclosing symbol and loop/conditional nesting, collecting
// `call` nodes as normalized RawCallSites in source order (deterministic occurrence indexing).
func walkPyCalls(n *sitter.Node, src []byte, sym string, loop, cond int, unit *SyntacticUnit) {
	nsym, nloop, ncond := sym, loop, cond
	switch n.Type() {
	case "function_definition":
		if name := n.ChildByFieldName("name"); name != nil {
			nsym = joinPySym(sym, name.Content(src))
		}
	case "class_definition":
		if name := n.ChildByFieldName("name"); name != nil {
			nsym = joinPySym(sym, name.Content(src))
		}
	case "for_statement", "while_statement":
		nloop = loop + 1
	case "if_statement", "with_statement":
		if n.Type() == "if_statement" {
			ncond = cond + 1
		}
	case "call":
		if fn := n.ChildByFieldName("function"); fn != nil {
			root, ri, chain := pyCallTarget(fn, src)
			unit.CallSites = append(unit.CallSites, RawCallSite{
				Root: root, RootIdent: ri, Chain: chain,
				EnclosingSymbol:   sym,
				LineStart:         int(n.StartPoint().Row) + 1,
				LineEnd:           int(n.EndPoint().Row) + 1,
				Invocation:        invHint(loop, cond),
				KeywordStrings:    pyKeywordStrings(n, src),
				PositionalStrings: pyPositionalStrings(n, src),
			})
		}
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		walkPyCalls(n.NamedChild(i), src, nsym, nloop, ncond, unit)
	}
}

// pyCallTarget unwinds a call's function node into root + selector chain: `client.messages.create` ->
// ("client", true, ["messages","create"]).
func pyCallTarget(fn *sitter.Node, src []byte) (root string, rootIdent bool, chain []string) {
	cur := fn
	for cur != nil {
		switch cur.Type() {
		case "attribute":
			attr := cur.ChildByFieldName("attribute")
			if attr != nil {
				chain = append([]string{attr.Content(src)}, chain...)
			}
			cur = cur.ChildByFieldName("object")
		case "identifier":
			return cur.Content(src), true, chain
		default:
			return "", false, chain
		}
	}
	return "", false, chain
}

func pyKeywordStrings(call *sitter.Node, src []byte) map[string]string {
	out := map[string]string{}
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return out
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		c := args.NamedChild(i)
		if c.Type() != "keyword_argument" {
			continue
		}
		name := c.ChildByFieldName("name")
		val := c.ChildByFieldName("value")
		if name != nil && val != nil && val.Type() == "string" {
			out[name.Content(src)] = pyStringValue(val, src)
		}
	}
	return out
}

// pyPositionalStrings returns the string-literal positional args of a call, in order (for framework
// builder calls like add_edge("a", "b")). It also gathers string literals nested in a dict value (the
// routing map of add_conditional_edges), so control targets are captured.
func pyPositionalStrings(call *sitter.Node, src []byte) []string {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	var out []string
	var collect func(n *sitter.Node)
	collect = func(n *sitter.Node) {
		switch n.Type() {
		case "string":
			out = append(out, pyStringValue(n, src))
			return
		case "pair": // a dict entry: collect the VALUE (a routing target), never the KEY (a label)
			if v := n.ChildByFieldName("value"); v != nil {
				collect(v)
			}
			return
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			collect(n.NamedChild(i))
		}
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		c := args.NamedChild(i)
		if c.Type() == "keyword_argument" {
			continue // positional only
		}
		collect(c)
	}
	return out
}

// pyStringValue extracts the text of a Python string literal (via the string_content child when present,
// else by stripping the surrounding quotes/prefix).
func pyStringValue(str *sitter.Node, src []byte) string {
	for i := 0; i < int(str.NamedChildCount()); i++ {
		if c := str.NamedChild(i); c.Type() == "string_content" {
			return c.Content(src)
		}
	}
	s := str.Content(src)
	// strip a possible prefix (f, r, b, u) then surrounding quotes.
	for len(s) > 0 && (s[0] == 'f' || s[0] == 'r' || s[0] == 'b' || s[0] == 'u' || s[0] == 'F' || s[0] == 'R' || s[0] == 'B' || s[0] == 'U') {
		s = s[1:]
	}
	s = strings.TrimPrefix(s, `"""`)
	s = strings.TrimSuffix(s, `"""`)
	s = strings.Trim(s, `"'`)
	return s
}

func invHint(loop, cond int) string {
	if loop > 0 {
		return "loop"
	}
	if cond > 0 {
		return "conditional"
	}
	return "single"
}

func joinPySym(sym, name string) string {
	if sym == "" || sym == "<module>" {
		return name
	}
	return sym + "." + name
}

func lastDotSeg(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}
