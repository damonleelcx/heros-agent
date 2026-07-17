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
	return unit, syntaxErrorDiagnostics(a.lang, rel, root)
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
				Keywords:          jsObjectArgs(n, src),
				PositionalStrings: jsPositionalStrings(n, src),
			})
			cs := &unit.CallSites[len(unit.CallSites)-1]
			cs.KeywordInsert, cs.KeywordUnpacking = argInsertAtEnd(
				jsOptionsObject(n), src, "}", ": ", jsArgUnpackings)
		}
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		walkJSCalls(n.NamedChild(i), src, nsym, nloop, ncond, unit)
	}
}

// jsArgUnpackings is nil, and the nil is a FINDING, not an omission — the same question Python
// answers with `dictionary_splat` (see pyArgUnpackings), asked of JS/TS and answered "no".
//
// JS/TS has the obvious analogue of `**kwargs`: object spread, `create({...opts, model: "x"})`, a
// `spread_element` sitting in the very object argInsertAtEnd appends to. The reason it is not a
// hazard is not that it is rare — it is that the language's semantics make our insertion RIGHT:
//
//	{ ...opts, model: "gpt-4o" }   ->   model is "gpt-4o". Last key wins, always.
//
// Where Python's `create(**kwargs, model="x")` RAISES because two sources both bind `model`, JS
// simply resolves the conflict, and it resolves it in our favour: a later key overwrites an earlier
// spread. So an appended `model:` is exactly the override the spec asked for — the very thing
// ArgInsert's premise claims, true here for a reason Python cannot offer. Blocking it would refuse
// correct, common rewrites for a danger this language does not have.
//
// 🔴 That safety hangs entirely on appending at the END. `{ model: "x", ...opts }` is the opposite
// program: opts wins, and the override becomes a silent no-op — a diff that looks applied and does
// nothing, which is worse than a refusal because it is green. argInsertAtEnd appends at the end for
// its own (syntactic) reasons; this file depends on it for a SEMANTIC one, so the property is pinned
// by a test rather than left to the next person to rediscover.
//
// Verified against the real runtime, not from memory: `node -e 'console.log({...{model:"old"},
// model:"new"}.model)'` prints `new`.
var jsArgUnpackings map[string]bool

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

// jsObjectArgs extracts the pairs of a call's object-literal arguments
// (`create({ model: "gpt-4o", ... })`), mapping key -> located value for the model/prompt floor and
// for the language-neutral apply path.
//
// Every pair is recorded, not only the string-valued ones: a pair the floor cannot read gets
// Text == "", which keywordFor rejects exactly as it rejected a pair that was absent from this map,
// so the IR is unchanged while `model: MODEL_ID` becomes rewritable.
func jsObjectArgs(call *sitter.Node, src []byte) map[string]ArgValue {
	out := map[string]ArgValue{}
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
			if key == nil || val == nil {
				continue
			}
			out[jsKeyName(key, src)] = jsArgValue(val, src)
		}
	}
	return out
}

// jsOptionsObject returns the call's FIRST object-literal argument — the options object every JS/TS
// SDK row in the registry targets (`create({model, messages})`). It is where a keyword argument the
// call site does not write would be added.
//
// First, specifically: openai's client takes a SECOND options object for per-request settings
// (`create({model}, {timeout})`), and `model` belongs in the first. Adding it to the second would
// compile and then be ignored at runtime — silently not applying the override, which is worse than
// refusing. jsObjectArgs reads pairs from every object argument (it always has); this names the one
// an insertion may target, and they agree on the shape that matters: no registry row addresses a
// call whose dimensions are split across two object arguments.
func jsOptionsObject(call *sitter.Node) *sitter.Node {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		if obj := args.NamedChild(i); obj.Type() == "object" {
			return obj
		}
	}
	return nil
}

// jsArgValue normalizes one JS/TS value expression.
//
// The READ rule is unchanged: only a `string` node yields text. A template literal is deliberately
// NOT read — it never has been, and making it start would change the emitted IR (a separate decision
// that is not this one's to take). It is still CLASSIFIED, because a rewriter needs to know that
// “ `${sys}\n${q}` “ is a string whose bytes must not be replaced wholesale, and that a
// substitution-free “ `gpt-4o` “ is one whose bytes may be. That is why Kind is not derived from
// Text: here the value is replaceable and yet the floor reads nothing from it.
func jsArgValue(val *sitter.Node, src []byte) ArgValue {
	v := ArgValue{Value: spanOf(val), Kind: ArgExpression}
	switch val.Type() {
	case "string":
		v.Kind, v.Text = ArgLiteralString, jsStringValue(val, src)
	case "template_string":
		v.Kind = ArgLiteralString
		if firstChildOfType(val, "template_substitution") != nil {
			v.Kind = ArgInterpolatedString
		}
	}
	return v
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
