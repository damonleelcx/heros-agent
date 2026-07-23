package linkage

import (
	"context"
	"sort"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"
)

// pyextract.go is the REAL static-linkage extractor (task 13.1's source side): it parses Python with
// tree-sitter — a pure parser that NEVER executes source (invariant I1) — and extracts, per enclosing
// function, the two signals InferStatic consumes: the resolved CALLEES (the local call graph) and the
// shared-STATE references (`self.<attr>` reads/writes). It is the thing that turns a hand-fed CallSite
// into one recovered from actual code, so InferStatic is no longer fed by fixtures alone.
//
// It reuses tree-sitter (the same substrate discovery's Python frontend uses) rather than a regex,
// because the AI-engineer discipline forbids guessing structure a parser can read exactly. It is the
// minimum-viable static subset (call-graph + shared-state, design Q11); full inter-procedural data-flow
// is the later fidelity uplift.

// LLMSite identifies one discovered LLM call site to attach recovered signals to: its node id and the
// fully-qualified enclosing symbol P1 already records (e.g. "TrajectoryCompressor._generate_summary_async").
type LLMSite struct {
	NodeID          string
	EnclosingSymbol string
}

// ExtractPythonCallSites parses one Python source file and returns a CallSite per input LLMSite,
// populated with the callees and shared-state refs of that site's enclosing function. A site whose
// enclosing function is not found in this file yields a CallSite with empty signals (the function
// lives in another file) rather than being dropped — the caller merges per-file results.
func ExtractPythonCallSites(src []byte, sites []LLMSite) []CallSite {
	fns := extractPythonFunctions(src)

	out := make([]CallSite, 0, len(sites))
	for i, s := range sites {
		cs := CallSite{NodeID: s.NodeID, EnclosingSymbol: s.EnclosingSymbol, Order: i}
		if f, ok := fns[s.EnclosingSymbol]; ok {
			cs.Callees = append([]string(nil), f.callees...)
			cs.StateRefs = append([]string(nil), f.stateRefs...)
		}
		out = append(out, cs)
	}
	return out
}

// funcSignals is one function's recovered linkage signals.
type funcSignals struct {
	callees   []string
	stateRefs []string
}

// extractPythonFunctions walks the tree once and returns, per fully-qualified function symbol, its
// resolved callees and shared-state refs. A `self.method(...)` call resolves to "<enclosing class>.method"
// so it matches the callee's own enclosing symbol; a bare or module call resolves to the trailing name.
func extractPythonFunctions(src []byte) map[string]funcSignals {
	parser := sitter.NewParser()
	parser.SetLanguage(python.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil || tree == nil {
		return map[string]funcSignals{}
	}
	defer tree.Close()

	acc := map[string]*sigAcc{}
	touch := func(sym string) *sigAcc {
		a, ok := acc[sym]
		if !ok {
			a = &sigAcc{callee: map[string]bool{}, state: map[string]bool{}}
			acc[sym] = a
		}
		return a
	}
	var walk func(n *sitter.Node, sym, class string)
	walk = func(n *sitter.Node, sym, class string) {
		nsym, nclass := sym, class
		switch n.Type() {
		case "class_definition":
			if name := n.ChildByFieldName("name"); name != nil {
				nclass = name.Content(src)
				nsym = joinSym(sym, nclass)
			}
		case "function_definition":
			if name := n.ChildByFieldName("name"); name != nil {
				nsym = joinSym(sym, name.Content(src))
				touch(nsym) // register the function even if it has no signals
			}
		case "call":
			if fn := n.ChildByFieldName("function"); fn != nil {
				if callee := resolveCallee(fn, src, class); callee != "" && sym != "" {
					touch(sym).callee[callee] = true
				}
			}
		case "attribute":
			// `self.<attr>` — a shared-state reference. Only within a function.
			if obj := n.ChildByFieldName("object"); obj != nil && obj.Type() == "identifier" && obj.Content(src) == "self" {
				if attr := n.ChildByFieldName("attribute"); attr != nil && sym != "" {
					touch(sym).state["self."+attr.Content(src)] = true
				}
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i), nsym, nclass)
		}
	}
	walk(tree.RootNode(), "", "")

	out := make(map[string]funcSignals, len(acc))
	for sym, a := range acc {
		out[sym] = funcSignals{callees: sortedKeys(a.callee), stateRefs: sortedKeys(a.state)}
	}
	return out
}

type sigAcc struct {
	callee map[string]bool
	state  map[string]bool
}

// resolveCallee turns a call's function node into a symbol that can match a callee's enclosing symbol:
//   - `self.method(...)`  → "<class>.method"  (resolves within the class)
//   - `obj.method(...)`   → "method"          (trailing name; best-effort)
//   - `func(...)`         → "func"            (a module-level function symbol)
func resolveCallee(fn *sitter.Node, src []byte, class string) string {
	switch fn.Type() {
	case "identifier":
		return fn.Content(src)
	case "attribute":
		attr := fn.ChildByFieldName("attribute")
		obj := fn.ChildByFieldName("object")
		if attr == nil {
			return ""
		}
		name := attr.Content(src)
		if obj != nil && obj.Type() == "identifier" && obj.Content(src) == "self" && class != "" {
			return class + "." + name // self.method → Class.method
		}
		return name
	}
	return ""
}

func joinSym(sym, name string) string {
	if sym == "" {
		return name
	}
	return sym + "." + name
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
