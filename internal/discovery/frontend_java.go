package discovery

import (
	"context"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/java"
)

// javaAnalyzer parses Java via tree-sitter (pure parser, never executes source, I1). Java LLM SDKs
// (langchain4j, Spring AI, Bedrock) use builders that bind the model at construction, so the model is
// usually unresolved at the floor; detection rests on import-presence of the SDK package + the call's
// method-selector chain. Resolution is syntactic (no types).
type javaAnalyzer struct{}

// NewJavaFrontend returns the tree-sitter-backed Java LanguageFrontend.
func NewJavaFrontend() LanguageFrontend { return NewSyntacticFrontend(&javaAnalyzer{}) }

func (*javaAnalyzer) Language() string     { return "java" }
func (*javaAnalyzer) Extensions() []string { return []string{".java"} }

func (a *javaAnalyzer) Analyze(rel string, src []byte) (SyntacticUnit, []Diagnostic) {
	unit := SyntacticUnit{RelPath: rel, PkgPath: javaPkgPath(rel), Imports: map[string]string{}, ImportPaths: map[string]bool{}}
	parser := sitter.NewParser()
	parser.SetLanguage(java.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil || tree == nil {
		return unit, []Diagnostic{{Code: CodeParseError, Severity: SeverityError, File: rel, Message: "tree-sitter: java parse failed"}}
	}
	defer tree.Close()
	root := tree.RootNode()
	collectJavaImports(root, src, &unit)
	walkJavaCalls(root, src, "<file>", 0, 0, &unit)
	return unit, nil
}

func javaPkgPath(rel string) string {
	p := strings.TrimSuffix(rel, ".java")
	return strings.ReplaceAll(p, "/", ".")
}

// collectJavaImports records imported types AND their package prefixes as import paths, so a registry
// row keyed on the SDK package (e.g. "dev.langchain4j") matches by import-presence.
func collectJavaImports(n *sitter.Node, src []byte, unit *SyntacticUnit) {
	if n.Type() == "import_declaration" {
		full := ""
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if c.Type() == "scoped_identifier" || c.Type() == "identifier" {
				full = c.Content(src)
			}
		}
		if full != "" {
			unit.ImportPaths[full] = true
			// also record dotted prefixes so a package-level registry key matches
			parts := strings.Split(full, ".")
			for i := 2; i <= len(parts); i++ {
				unit.ImportPaths[strings.Join(parts[:i], ".")] = true
			}
			unit.Imports[parts[len(parts)-1]] = full
		}
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		collectJavaImports(n.NamedChild(i), src, unit)
	}
}

func walkJavaCalls(n *sitter.Node, src []byte, sym string, loop, cond int, unit *SyntacticUnit) {
	nsym, nloop, ncond := sym, loop, cond
	switch n.Type() {
	case "method_declaration", "constructor_declaration":
		if name := n.ChildByFieldName("name"); name != nil {
			nsym = joinPySym(sym, name.Content(src))
		}
	case "class_declaration", "interface_declaration":
		if name := n.ChildByFieldName("name"); name != nil {
			nsym = joinPySym(sym, name.Content(src))
		}
	case "for_statement", "enhanced_for_statement", "while_statement", "do_statement":
		nloop = loop + 1
	case "if_statement", "switch_expression":
		if n.Type() == "if_statement" {
			ncond = cond + 1
		}
	case "method_invocation":
		root, ri, chain := javaCallTarget(n, src)
		unit.CallSites = append(unit.CallSites, RawCallSite{
			Root: root, RootIdent: ri, Chain: chain,
			EnclosingSymbol:   sym,
			LineStart:         int(n.StartPoint().Row) + 1,
			LineEnd:           int(n.EndPoint().Row) + 1,
			Invocation:        invHint(loop, cond),
			KeywordStrings:    map[string]string{},
			PositionalStrings: javaPositionalStrings(n, src),
		})
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		walkJavaCalls(n.NamedChild(i), src, nsym, nloop, ncond, unit)
	}
}

// javaCallTarget builds the selector chain from a (possibly nested) method_invocation:
// `client.chat().create(req)` -> ("client", true, ["chat","create"]).
func javaCallTarget(mi *sitter.Node, src []byte) (root string, rootIdent bool, chain []string) {
	// The leaf method name is this invocation's `name`.
	name := mi.ChildByFieldName("name")
	if name != nil {
		chain = []string{name.Content(src)}
	}
	obj := mi.ChildByFieldName("object")
	for obj != nil {
		switch obj.Type() {
		case "method_invocation":
			if nm := obj.ChildByFieldName("name"); nm != nil {
				chain = append([]string{nm.Content(src)}, chain...)
			}
			obj = obj.ChildByFieldName("object")
		case "field_access":
			if f := obj.ChildByFieldName("field"); f != nil {
				chain = append([]string{f.Content(src)}, chain...)
			}
			obj = obj.ChildByFieldName("object")
		case "identifier":
			return obj.Content(src), true, chain
		default:
			return "", false, chain
		}
	}
	return "", false, chain
}

func javaPositionalStrings(mi *sitter.Node, src []byte) []string {
	args := mi.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(args.NamedChildCount()); i++ {
		if c := args.NamedChild(i); c.Type() == "string_literal" {
			out = append(out, strings.Trim(c.Content(src), "\""))
		}
	}
	return out
}
