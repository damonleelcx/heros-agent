package discovery

import (
	"context"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// jsAnalyzer parses JavaScript/TypeScript via tree-sitter (pure parser, never executes source, I1). It
// selects the TS grammar for .ts/.tsx and the JS grammar for .js/.jsx/.mjs/.cjs. Resolution is syntactic:
// import-presence + selector + declared entrypoints + object-literal string args (the 10.5 floor).
type jsAnalyzer struct {
	lang string // "typescript" or "javascript"
	exts []string
}

// NewTypeScriptFrontend / NewJavaScriptFrontend return the tree-sitter-backed TS/JS frontends. They share
// one analyzer implementation; only the grammar + extensions differ.
func NewTypeScriptFrontend() LanguageFrontend {
	return NewSyntacticFrontend(&jsAnalyzer{lang: "typescript", exts: []string{".ts", ".tsx", ".mts", ".cts"}})
}
func NewJavaScriptFrontend() LanguageFrontend {
	return NewSyntacticFrontend(&jsAnalyzer{lang: "javascript", exts: []string{".js", ".jsx", ".mjs", ".cjs"}})
}

func (a *jsAnalyzer) Language() string     { return a.lang }
func (a *jsAnalyzer) Extensions() []string { return a.exts }

func (a *jsAnalyzer) grammar() *sitter.Language {
	if a.lang == "typescript" {
		return typescript.GetLanguage()
	}
	return javascript.GetLanguage()
}

func (a *jsAnalyzer) Analyze(rel string, src []byte) (SyntacticUnit, []Diagnostic) {
	unit := SyntacticUnit{RelPath: rel, PkgPath: jsPkgPath(rel), Imports: map[string]string{}, ImportPaths: map[string]bool{}}
	parser := sitter.NewParser()
	parser.SetLanguage(a.grammar())
	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil || tree == nil {
		return unit, []Diagnostic{{Code: CodeParseError, Severity: SeverityError, File: rel, Message: "tree-sitter: js/ts parse failed"}}
	}
	defer tree.Close()
	root := tree.RootNode()
	collectJSImports(root, src, &unit)
	walkJSCalls(root, src, "<module>", 0, 0, &unit)
	return unit, nil
}

func jsPkgPath(rel string) string {
	p := rel
	for _, e := range []string{".tsx", ".ts", ".jsx", ".js", ".mjs", ".cjs", ".mts", ".cts"} {
		p = strings.TrimSuffix(p, e)
	}
	p = strings.TrimSuffix(p, "/index")
	return strings.ReplaceAll(p, "/", ".")
}

func collectJSImports(n *sitter.Node, src []byte, unit *SyntacticUnit) {
	switch n.Type() {
	case "import_statement":
		srcNode := n.ChildByFieldName("source")
		mod := ""
		if srcNode != nil {
			mod = jsStringValue(srcNode, src)
		}
		if mod == "" {
			break
		}
		unit.ImportPaths[mod] = true
		// import_clause -> default identifier, named_imports (import_specifier), namespace_import
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if c.Type() != "import_clause" {
				continue
			}
			for j := 0; j < int(c.NamedChildCount()); j++ {
				cc := c.NamedChild(j)
				switch cc.Type() {
				case "identifier": // default import
					unit.Imports[cc.Content(src)] = mod
				case "namespace_import": // * as ns
					if id := lastIdent(cc, src); id != "" {
						unit.Imports[id] = mod
					}
				case "named_imports":
					for k := 0; k < int(cc.NamedChildCount()); k++ {
						spec := cc.NamedChild(k)
						if spec.Type() == "import_specifier" {
							name := spec.ChildByFieldName("name")
							alias := spec.ChildByFieldName("alias")
							bind := name
							if alias != nil {
								bind = alias
							}
							if bind != nil {
								unit.Imports[bind.Content(src)] = mod
							}
						}
					}
				}
			}
		}
	case "variable_declarator":
		// const OpenAI = require("openai")
		val := n.ChildByFieldName("value")
		name := n.ChildByFieldName("name")
		if val != nil && name != nil && val.Type() == "call_expression" {
			fn := val.ChildByFieldName("function")
			if fn != nil && fn.Type() == "identifier" && fn.Content(src) == "require" {
				if args := val.ChildByFieldName("arguments"); args != nil {
					for i := 0; i < int(args.NamedChildCount()); i++ {
						if s := args.NamedChild(i); s.Type() == "string" {
							mod := jsStringValue(s, src)
							unit.Imports[name.Content(src)] = mod
							unit.ImportPaths[mod] = true
						}
					}
				}
			}
		}
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		collectJSImports(n.NamedChild(i), src, unit)
	}
}

func walkJSCalls(n *sitter.Node, src []byte, sym string, loop, cond int, unit *SyntacticUnit) {
	nsym, nloop, ncond := sym, loop, cond
	switch n.Type() {
	case "function_declaration", "generator_function_declaration", "method_definition":
		if name := n.ChildByFieldName("name"); name != nil {
			nsym = joinPySym(sym, name.Content(src))
		}
	case "class_declaration":
		if name := n.ChildByFieldName("name"); name != nil {
			nsym = joinPySym(sym, name.Content(src))
		}
	case "variable_declarator":
		// arrow/function assigned to a name: `const run = async () => {...}`
		if name := n.ChildByFieldName("name"); name != nil {
			if val := n.ChildByFieldName("value"); val != nil && (val.Type() == "arrow_function" || val.Type() == "function") {
				nsym = joinPySym(sym, name.Content(src))
			}
		}
	case "for_statement", "for_in_statement", "while_statement", "do_statement":
		nloop = loop + 1
	case "if_statement":
		ncond = cond + 1
	case "call_expression":
		if fn := n.ChildByFieldName("function"); fn != nil {
			root, ri, chain := jsCallTarget(fn, src)
			unit.CallSites = append(unit.CallSites, RawCallSite{
				Root: root, RootIdent: ri, Chain: chain,
				EnclosingSymbol:   sym,
				LineStart:         int(n.StartPoint().Row) + 1,
				LineEnd:           int(n.EndPoint().Row) + 1,
				Invocation:        invHint(loop, cond),
				KeywordStrings:    jsObjectStrings(n, src),
				PositionalStrings: jsPositionalStrings(n, src),
			})
		}
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		walkJSCalls(n.NamedChild(i), src, nsym, nloop, ncond, unit)
	}
}

// jsCallTarget unwinds a member_expression chain: `client.chat.completions.create` ->
// ("client", true, ["chat","completions","create"]);  `generateText` -> ("generateText", true, []).
func jsCallTarget(fn *sitter.Node, src []byte) (root string, rootIdent bool, chain []string) {
	cur := fn
	for cur != nil {
		switch cur.Type() {
		case "member_expression":
			prop := cur.ChildByFieldName("property")
			if prop != nil {
				chain = append([]string{prop.Content(src)}, chain...)
			}
			cur = cur.ChildByFieldName("object")
		case "identifier", "shorthand_property_identifier":
			return cur.Content(src), true, chain
		default:
			return "", false, chain
		}
	}
	return "", false, chain
}

// jsObjectStrings extracts string-valued pairs from the FIRST object-literal argument
// (`create({ model: "gpt-4o", ... })`), mapping key -> string value for the model/prompt floor.
func jsObjectStrings(call *sitter.Node, src []byte) map[string]string {
	out := map[string]string{}
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return out
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		obj := args.NamedChild(i)
		if obj.Type() != "object" {
			continue
		}
		for j := 0; j < int(obj.NamedChildCount()); j++ {
			pair := obj.NamedChild(j)
			if pair.Type() != "pair" {
				continue
			}
			key := pair.ChildByFieldName("key")
			val := pair.ChildByFieldName("value")
			if key != nil && val != nil && val.Type() == "string" {
				out[jsKeyName(key, src)] = jsStringValue(val, src)
			}
		}
	}
	return out
}

func jsPositionalStrings(call *sitter.Node, src []byte) []string {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	var out []string
	var collect func(n *sitter.Node)
	collect = func(n *sitter.Node) {
		switch n.Type() {
		case "string":
			out = append(out, jsStringValue(n, src))
			return
		case "pair": // object entry: collect the VALUE (routing target), never the KEY (label)
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
		collect(args.NamedChild(i))
	}
	return out
}

func jsKeyName(key *sitter.Node, src []byte) string {
	if key.Type() == "string" {
		return jsStringValue(key, src)
	}
	return key.Content(src)
}

func jsStringValue(str *sitter.Node, src []byte) string {
	for i := 0; i < int(str.NamedChildCount()); i++ {
		if c := str.NamedChild(i); c.Type() == "string_fragment" {
			return c.Content(src)
		}
	}
	s := str.Content(src)
	return strings.Trim(s, "\"'`")
}

func lastIdent(n *sitter.Node, src []byte) string {
	var id string
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if c := n.NamedChild(i); c.Type() == "identifier" {
			id = c.Content(src)
		}
	}
	return id
}
