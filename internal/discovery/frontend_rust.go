package discovery

import (
	"context"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/rust"
)

// rustAnalyzer parses Rust via tree-sitter (pure parser, never executes source, I1). Rust LLM SDKs
// (async-openai, anthropic crates) build request structs and call `.create(req).await`, so the model is
// usually bound in a struct (unresolved at the floor); detection rests on crate import-presence +
// method selector. Resolution is syntactic (no types).
type rustAnalyzer struct{}

// NewRustFrontend returns the tree-sitter-backed Rust LanguageFrontend.
func NewRustFrontend() LanguageFrontend { return NewSyntacticFrontend(&rustAnalyzer{}) }

func (*rustAnalyzer) Language() string     { return "rust" }
func (*rustAnalyzer) Extensions() []string { return []string{".rs"} }

func (a *rustAnalyzer) Analyze(rel string, src []byte) (SyntacticUnit, []Diagnostic) {
	unit := SyntacticUnit{RelPath: rel, PkgPath: rustPkgPath(rel), Imports: map[string]string{}, ImportPaths: map[string]bool{}}
	parser := sitter.NewParser()
	parser.SetLanguage(rust.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil || tree == nil {
		return unit, []Diagnostic{{Code: CodeParseError, Severity: SeverityError, File: rel, Message: "tree-sitter: rust parse failed"}}
	}
	defer tree.Close()
	root := tree.RootNode()
	collectRustUses(root, src, &unit)
	walkRustCalls(root, src, "<module>", 0, 0, &unit)
	return unit, nil
}

func rustPkgPath(rel string) string {
	p := strings.TrimSuffix(rel, ".rs")
	p = strings.TrimSuffix(p, "/mod")
	return strings.ReplaceAll(p, "/", "::")
}

// collectRustUses records the crate roots of `use` declarations as import paths. The crate (first
// segment of a scoped_identifier / use_list) is what import-presence checks against.
func collectRustUses(n *sitter.Node, src []byte, unit *SyntacticUnit) {
	if n.Type() == "use_declaration" {
		for _, crate := range rustCratesOf(n, src) {
			unit.ImportPaths[crate] = true
		}
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		collectRustUses(n.NamedChild(i), src, unit)
	}
}

func rustCratesOf(n *sitter.Node, src []byte) []string {
	var out []string
	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		switch node.Type() {
		case "scoped_identifier", "scoped_use_list", "use_list", "use_as_clause", "identifier":
			// the crate root is the leftmost identifier segment
			if seg := firstIdentSegment(node, src); seg != "" {
				out = append(out, seg)
			}
		}
		for i := 0; i < int(node.NamedChildCount()); i++ {
			walk(node.NamedChild(i))
		}
	}
	walk(n)
	// dedupe
	seen := map[string]bool{}
	var uniq []string
	for _, c := range out {
		if !seen[c] {
			seen[c] = true
			uniq = append(uniq, c)
		}
	}
	return uniq
}

func firstIdentSegment(n *sitter.Node, src []byte) string {
	cur := n
	for cur != nil {
		if cur.Type() == "identifier" {
			return cur.Content(src)
		}
		if cur.NamedChildCount() == 0 {
			return ""
		}
		// descend into the leftmost path segment (the "path" field of a scoped_identifier)
		if p := cur.ChildByFieldName("path"); p != nil {
			cur = p
			continue
		}
		cur = cur.NamedChild(0)
	}
	return ""
}

func walkRustCalls(n *sitter.Node, src []byte, sym string, loop, cond int, unit *SyntacticUnit) {
	nsym, nloop, ncond := sym, loop, cond
	switch n.Type() {
	case "function_item":
		if name := n.ChildByFieldName("name"); name != nil {
			nsym = joinPySym(sym, name.Content(src))
		}
	case "for_expression", "while_expression", "loop_expression":
		nloop = loop + 1
	case "if_expression", "match_expression":
		if n.Type() == "if_expression" {
			ncond = cond + 1
		}
	case "call_expression":
		if fn := n.ChildByFieldName("function"); fn != nil {
			root, ri, chain := rustCallTarget(fn, src)
			unit.CallSites = append(unit.CallSites, RawCallSite{
				Root: root, RootIdent: ri, Chain: chain,
				EnclosingSymbol:   sym,
				LineStart:         int(n.StartPoint().Row) + 1,
				LineEnd:           int(n.EndPoint().Row) + 1,
				Invocation:        invHint(loop, cond),
				KeywordStrings:    map[string]string{},
				PositionalStrings: rustPositionalStrings(n, src),
			})
		}
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		walkRustCalls(n.NamedChild(i), src, nsym, nloop, ncond, unit)
	}
}

// rustCallTarget unwinds a call's function node: `client.chat().create` uses field_expression; scoped
// `Client::new` uses scoped_identifier. `client.chat.create` -> ("client", true, ["chat","create"]).
func rustCallTarget(fn *sitter.Node, src []byte) (root string, rootIdent bool, chain []string) {
	cur := fn
	for cur != nil {
		switch cur.Type() {
		case "field_expression":
			field := cur.ChildByFieldName("field")
			if field != nil {
				chain = append([]string{field.Content(src)}, chain...)
			}
			cur = cur.ChildByFieldName("value")
		case "call_expression":
			// method chains like client.chat().create(): unwrap the inner call to its function
			cur = cur.ChildByFieldName("function")
		case "identifier", "field_identifier":
			return cur.Content(src), true, chain
		case "scoped_identifier":
			// Crate::Type::method — treat the last name as chain leaf, crate as root
			if name := cur.ChildByFieldName("name"); name != nil {
				chain = append([]string{name.Content(src)}, chain...)
			}
			cur = cur.ChildByFieldName("path")
		default:
			return "", false, chain
		}
	}
	return "", false, chain
}

func rustPositionalStrings(call *sitter.Node, src []byte) []string {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	var out []string
	var collect func(n *sitter.Node)
	collect = func(n *sitter.Node) {
		if n.Type() == "string_literal" {
			out = append(out, strings.Trim(n.Content(src), "\"#"))
			return
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			collect(n.NamedChild(i))
		}
	}
	collect(args)
	return out
}
