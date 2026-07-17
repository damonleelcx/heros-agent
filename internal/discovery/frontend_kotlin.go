package discovery

import (
	"context"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/kotlin"
)

// kotlinAnalyzer parses Kotlin via tree-sitter (pure parser, never executes source, I1). Kotlin targets
// the JVM, so it consumes the same LLM SDKs as Java (langchain4j, Spring AI, the Bedrock Java SDK) plus
// the AWS SDK for Kotlin; those SDKs use builders that bind the model at construction, so the model is
// usually unresolved at the floor. Detection rests on import-presence of the SDK package + the call's
// selector chain. Resolution is syntactic (no types).
//
// Kotlin's grammar differs from Java's in ways that matter here (all verified against the grammar, not
// assumed):
//   - there is NO `name` field on declarations — Java's ChildByFieldName("name") returns nil for Kotlin.
//     Function names are the first `simple_identifier` child; class/object names are `type_identifier`.
//   - `interface X` parses as `class_declaration`, and `object X` as `object_declaration`.
//   - a call is `call_expression{ callee, call_suffix }`, where the callee is a `navigation_expression`
//     (`a.b`) or a bare `simple_identifier`; Java's flat `method_invocation` has no analogue.
//   - `if`/`when` are EXPRESSIONS (`if_expression` / `when_expression`), not statements.
type kotlinAnalyzer struct{}

// NewKotlinFrontend returns the tree-sitter-backed Kotlin LanguageFrontend.
func NewKotlinFrontend() LanguageFrontend { return NewSyntacticFrontend(&kotlinAnalyzer{}) }

func (*kotlinAnalyzer) Language() string { return "kotlin" }

// Extensions covers Kotlin sources and Kotlin scripts (.kts — Gradle build scripts and scratch files are
// Kotlin, and an LLM call in one is still an LLM call).
func (*kotlinAnalyzer) Extensions() []string { return []string{".kt", ".kts"} }

func (a *kotlinAnalyzer) Analyze(rel string, src []byte) (SyntacticUnit, []Diagnostic) {
	unit := SyntacticUnit{RelPath: rel, PkgPath: kotlinPkgPath(rel), Imports: map[string]string{}, ImportPaths: map[string]bool{}}
	parser := sitter.NewParser()
	parser.SetLanguage(kotlin.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil || tree == nil {
		return unit, []Diagnostic{{Code: CodeParseError, Severity: SeverityError, File: rel, Message: "tree-sitter: kotlin parse failed"}}
	}
	defer tree.Close()
	root := tree.RootNode()
	collectKotlinImports(root, src, &unit)
	walkKotlinCalls(root, src, "<file>", 0, 0, &unit)
	return unit, syntaxErrorDiagnostics("kotlin", rel, root)
}

func kotlinPkgPath(rel string) string {
	p := strings.TrimSuffix(rel, ".kts")
	p = strings.TrimSuffix(p, ".kt")
	return strings.ReplaceAll(p, "/", ".")
}

// collectKotlinImports records imported symbols AND their package prefixes as import paths, so a registry
// row keyed on the SDK package (e.g. "dev.langchain4j") matches by import-presence. It handles Kotlin's
// two extra import forms:
//   - `import a.b.C as Alias`  -> the local name is the ALIAS, not the last path segment.
//   - `import a.b.*`           -> a package wildcard: it binds no local name, so only the path is recorded.
//
// 🔴 The local-name binding maps to the PACKAGE (the path minus its last segment), which follows Python's
// convention and NOT Java's. This is a real semantic difference, not a style choice: `import a.b.complete`
// in Kotlin imports the MEMBER `complete` FROM package `a.b`, exactly like Python's
// `from a.b import complete` — whereas Java's `import a.b.C` names a type and Java has no top-level
// functions at all, so javaAnalyzer's local-name->full-path binding is never consulted for a free
// function. The declared-entrypoint matcher (matchDeclaredEntries) resolves a free-function wrapper via
// `imports[root] == entry.ImportPath`, where ImportPath is the declared symbol minus its last segment.
// Binding the local name to the full path here would make every Kotlin top-level wrapper silently
// unmatchable — the fixture kotlin_wrapper is the guard that holds this.
func collectKotlinImports(n *sitter.Node, src []byte, unit *SyntacticUnit) {
	if n.Type() == "import_header" {
		full, alias, wildcard := "", "", false
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			switch c.Type() {
			case "identifier":
				full = c.Content(src)
			case "import_alias":
				// `as Chat` -> the alias is the identifier inside.
				for j := 0; j < int(c.NamedChildCount()); j++ {
					if sub := c.NamedChild(j); sub.Type() == "type_identifier" || sub.Type() == "simple_identifier" {
						alias = sub.Content(src)
					}
				}
			case "wildcard_import":
				wildcard = true
			}
		}
		if full != "" {
			unit.ImportPaths[full] = true
			// Also record dotted prefixes so a package-level registry key matches.
			parts := strings.Split(full, ".")
			for i := 2; i <= len(parts); i++ {
				unit.ImportPaths[strings.Join(parts[:i], ".")] = true
			}
			// A wildcard import binds no local name; otherwise the local name (alias, or the last segment)
			// binds to the PACKAGE the member was imported from.
			if !wildcard && len(parts) >= 2 {
				pkg := strings.Join(parts[:len(parts)-1], ".")
				local := parts[len(parts)-1]
				if alias != "" {
					local = alias
				}
				unit.Imports[local] = pkg
			}
		}
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		collectKotlinImports(n.NamedChild(i), src, unit)
	}
}

func walkKotlinCalls(n *sitter.Node, src []byte, sym string, loop, cond int, unit *SyntacticUnit) {
	nsym, nloop, ncond := sym, loop, cond
	switch n.Type() {
	case "function_declaration":
		if name := firstChildOfType(n, "simple_identifier"); name != nil {
			nsym = joinPySym(sym, name.Content(src))
		}
	case "class_declaration", "object_declaration":
		// Kotlin names classes/objects/interfaces with a type_identifier (there is no `name` field).
		if name := firstChildOfType(n, "type_identifier"); name != nil {
			nsym = joinPySym(sym, name.Content(src))
		}
	case "for_statement", "while_statement", "do_while_statement":
		nloop = loop + 1
	case "if_expression", "when_expression":
		ncond = cond + 1
	case "call_expression":
		root, ri, chain := kotlinCallTarget(n, src)
		unit.CallSites = append(unit.CallSites, RawCallSite{
			Root: root, RootIdent: ri, Chain: chain,
			EnclosingSymbol:   sym,
			LineStart:         int(n.StartPoint().Row) + 1,
			LineEnd:           int(n.EndPoint().Row) + 1,
			Invocation:        invHint(loop, cond),
			Keywords:          kotlinKeywords(n, src),
			PositionalStrings: kotlinPositionalStrings(n, src),
		})
		cs := &unit.CallSites[len(unit.CallSites)-1]
		cs.KeywordInsert, cs.KeywordUnpacking = argInsertAtEnd(
			kotlinValueArguments(n), src, ")", " = ", kotlinArgUnpackings)
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		walkKotlinCalls(n.NamedChild(i), src, nsym, nloop, ncond, unit)
	}
}

// kotlinArgUnpackings is nil, for two independent reasons — either one sufficient, which is why the
// nil is safe rather than lucky.
//
//  1. Kotlin's spread (`f(*arr, x = 1)`, a `spread_expression`) feeds a VARARG parameter. It cannot
//     silently also bind the named argument we append: Kotlin binds named arguments by name, and if
//     the two ever did collide, `kotlinc` says so. That is the difference that matters — Kotlin's gate
//     is a TYPE CHECKER (ADR-003: type-checked), so a conflicting insertion is rejected loudly at the
//     build gate and never reaches a reviewer wearing a green badge. Python's gate is `py_compile`,
//     which has nothing to object to. The guard belongs exactly where the gate is blind.
//
//  2. This code path is unreachable for Kotlin anyway: all five Kotlin rows in registry.yaml declare
//     no arg_map (langchain4j, Spring AI and Bedrock bind the model on a BUILDER at construction), so
//     spanLocator refuses long before an insertion is considered. See argumentForms in
//     internal/transform, which is where that reason is written down once.
//
// ⚠️ If a Kotlin row with an arg_map ever lands, reason (1) still holds — but note that Kotlin nests
// `spread_expression` inside a `value_argument`, one level below where argInsertAtEnd scans.
var kotlinArgUnpackings map[string]bool

// kotlinCallTarget builds the selector chain from a call_expression:
//
//	model.generate("x")     -> ("model",  true,  ["generate"])
//	client.chat().call(...) -> ("client", true,  ["chat", "call"])
//	generateText(...)       -> ("generateText", true, [])   // bare call: root is the imported name
func kotlinCallTarget(ce *sitter.Node, src []byte) (root string, rootIdent bool, chain []string) {
	callee := kotlinCallee(ce)
	for callee != nil {
		switch callee.Type() {
		case "navigation_expression":
			// navigation_expression{ receiver, navigation_suffix{ simple_identifier } }
			if suf := firstChildOfType(callee, "navigation_suffix"); suf != nil {
				if id := firstChildOfType(suf, "simple_identifier"); id != nil {
					chain = append([]string{id.Content(src)}, chain...)
				}
			}
			callee = callee.NamedChild(0) // the receiver
		case "call_expression":
			callee = kotlinCallee(callee) // e.g. the `client.chat()` in `client.chat().call()`
		case "simple_identifier":
			return callee.Content(src), true, chain
		default:
			return "", false, chain
		}
	}
	return "", false, chain
}

// kotlinCallee returns the callee expression of a call_expression — its first named child that is not the
// argument list / trailing lambda.
func kotlinCallee(ce *sitter.Node) *sitter.Node {
	for i := 0; i < int(ce.NamedChildCount()); i++ {
		switch c := ce.NamedChild(i); c.Type() {
		case "call_suffix", "type_arguments", "annotated_lambda", "lambda_literal":
			continue
		default:
			return c
		}
	}
	return nil
}

// kotlinKeywords returns the call's NAMED arguments — Kotlin's named-argument form
// `generate(prompt = "hi")`. A value_argument with a leading simple_identifier is a named arg; without
// one it is positional (and a bare positional identifier, `generate(prompt)`, has no value expression
// after the name, so it is not one either — the guard below is what tells those apart).
//
// Named string literals are the only thing the type-free floor can resolve (10.5); every named
// argument is nonetheless recorded, for its span.
func kotlinKeywords(ce *sitter.Node, src []byte) map[string]ArgValue {
	out := map[string]ArgValue{}
	for _, va := range kotlinValueArgs(ce) {
		name := firstChildOfType(va, "simple_identifier")
		if name == nil {
			continue // positional
		}
		val := namedChildAfter(va, name)
		if val == nil {
			continue // `f(x)`: the identifier IS the value, not a name bound to one
		}
		out[name.Content(src)] = kotlinArgValue(val, src)
	}
	return out
}

// kotlinArgValue normalizes one Kotlin value expression. As in Python, the READ rule is unchanged
// (a string_literal yields kotlinStringValue) and the WRITE rule is stricter: a string template
// (`"hi $name"`) splices a runtime value in, so its bytes must not be replaced wholesale.
func kotlinArgValue(val *sitter.Node, src []byte) ArgValue {
	v := ArgValue{Value: spanOf(val), Kind: ArgExpression}
	if val.Type() != "string_literal" {
		return v
	}
	v.Text = kotlinStringValue(val, src)
	v.Kind = ArgLiteralString
	if firstChildOfType(val, "interpolated_expression") != nil || firstChildOfType(val, "interpolated_identifier") != nil {
		v.Kind = ArgInterpolatedString
	}
	return v
}

// kotlinPositionalStrings returns the call's positional string-literal args in source order (for framework
// builder calls). It mirrors pyPositionalStrings: named args are excluded, so the shared builder walk sees
// the same shape for every language.
func kotlinPositionalStrings(ce *sitter.Node, src []byte) []string {
	var out []string
	for _, va := range kotlinValueArgs(ce) {
		if firstChildOfType(va, "simple_identifier") != nil {
			continue // named arg, not positional
		}
		if lit := firstChildOfType(va, "string_literal"); lit != nil {
			out = append(out, kotlinStringValue(lit, src))
		}
	}
	return out
}

// kotlinValueArguments returns a call's value_arguments node (its parenthesized argument list), or nil
// when the call has none — `runBlocking { ... }` and other trailing-lambda-only calls really have no
// argument list, and therefore no place to insert a named argument.
func kotlinValueArguments(ce *sitter.Node) *sitter.Node {
	suffix := firstChildOfType(ce, "call_suffix")
	if suffix == nil {
		return nil
	}
	return firstChildOfType(suffix, "value_arguments")
}

// kotlinValueArgs returns the value_argument nodes of a call: call_expression > call_suffix >
// value_arguments > value_argument.
func kotlinValueArgs(ce *sitter.Node) []*sitter.Node {
	args := kotlinValueArguments(ce)
	if args == nil {
		return nil
	}
	var out []*sitter.Node
	for i := 0; i < int(args.NamedChildCount()); i++ {
		if c := args.NamedChild(i); c.Type() == "value_argument" {
			out = append(out, c)
		}
	}
	return out
}

// kotlinStringValue extracts a Kotlin string literal's text via its string_content child, so the quotes
// (and any interpolation markers) are not included. A template with interpolation yields only its literal
// segments — it is NOT a static string, and the floor flags it unresolved rather than guessing.
func kotlinStringValue(lit *sitter.Node, src []byte) string {
	var sb strings.Builder
	for i := 0; i < int(lit.NamedChildCount()); i++ {
		if c := lit.NamedChild(i); c.Type() == "string_content" {
			sb.WriteString(c.Content(src))
		}
	}
	if sb.Len() == 0 {
		return strings.Trim(lit.Content(src), "\"")
	}
	return sb.String()
}

// firstChildOfType returns the first NAMED child of the given type, or nil.
func firstChildOfType(n *sitter.Node, typ string) *sitter.Node {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if c := n.NamedChild(i); c.Type() == typ {
			return c
		}
	}
	return nil
}
