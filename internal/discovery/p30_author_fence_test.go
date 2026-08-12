package discovery

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 🔴 THE FENCE THAT CATCHES THE NEXT EDGE WRITER (P30 tasks 2.2, 2.8).
//
// WriteBack refuses an unauthored LABEL at the write boundary, which works because every label goes
// through one function. Edges do not: `discovery.IREdge` is a plain struct and four packages construct
// one — the emitter, and three recovered-topology writers (reconcile, irwriteback, linkage). A fifth
// added tomorrow that forgets `Author` produces facts that read as `legacy` forever, and NOTHING FAILS:
// the build is green, the graph renders, and the only symptom is that an incident asking "which of
// these edges did the model write" gets `legacy` for a fact this build produced.
//
// So the fence is STRUCTURAL rather than behavioural: it parses the tree and requires every composite
// literal of type IREdge outside a test to set Author. That is checkable, it names the file and line,
// and it cannot be satisfied by a test that happens not to exercise the new writer.

// authorExemptDirs are trees where an IREdge literal is legitimately unauthored.
var authorExemptDirs = map[string]string{
	// Demo fixtures are hand-built graphs no discovery run produced. cmd/demo/patterngraph's own
	// comment says so, and the graph view renders "discovery recorded no contributing frontend" for
	// exactly that case rather than attributing the fixture to an analysis that never ran.
	"cmd/demo": "hand-built demo fixtures, attributed to nothing on purpose",
}

func TestEveryIREdgeWriterStampsAnAuthor(t *testing.T) {
	root := repoRootFromDiscovery(t)
	fset := token.NewFileSet()

	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "testdata", "web", ".claude", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for dir := range authorExemptDirs {
			if strings.HasPrefix(rel, dir) {
				return nil
			}
		}

		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil // a file that does not parse is somebody else's failure
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isIREdgeType(lit.Type) {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Author" {
					return true
				}
			}
			offenders = append(offenders, rel+":"+itoa(fset.Position(lit.Pos()).Line))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("these IREdge literals set no Author, so the edges they write read as `legacy` forever "+
			"and NOTHING fails — the build is green, the graph renders, and an incident asking which "+
			"edges the model wrote gets the wrong answer for a fact this build produced:\n  %s\n\n"+
			"  Stamp one of: frontend (a parser established it), detector (a rule or a trace-recovery "+
			"did), heros (the agent inferred it), operator (a human corrected it).",
			strings.Join(offenders, "\n  "))
	}
	// 🔴 ANTI-VACUITY. A walk that matched nothing would report clean forever — which is how a fence
	// stops guarding without anybody noticing. There are four production writers today.
	if got := countIREdgeLiterals(t, root); got < 4 {
		t.Errorf("the scan found only %d IREdge literal(s) in the tree. It is not finding them, so its "+
			"clean report above means nothing.", got)
	}
}

// isIREdgeType reports whether a composite literal's type is discovery.IREdge or IREdge.
func isIREdgeType(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name == "IREdge"
	case *ast.SelectorExpr:
		return t.Sel.Name == "IREdge"
	}
	return false
}

func countIREdgeLiterals(t *testing.T, root string) int {
	t.Helper()
	fset := token.NewFileSet()
	n := 0
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(path, "/web/") || strings.Contains(path, "/node_modules/") ||
			strings.Contains(path, "/.claude/") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil
		}
		ast.Inspect(f, func(node ast.Node) bool {
			if lit, ok := node.(*ast.CompositeLit); ok && isIREdgeType(lit.Type) {
				n++
			}
			return true
		})
		return nil
	})
	return n
}

func repoRootFromDiscovery(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// The vocabulary is closed and `legacy` is not writable. Asserted here as well as in the classifier so
// the contract holds in the package that owns it.
func TestTheAuthorVocabularyIsClosed(t *testing.T) {
	if ValidAuthor(AuthorLegacy) {
		t.Error("`legacy` is writable. It is what an ABSENT author READS as; a writer that stamps it " +
			"has recorded ignorance as a fact, and no query can then tell it from a pre-P30 row.")
	}
	if ValidAuthor(FactAuthor("agent")) {
		t.Error("an unknown author is accepted — the set is not closed")
	}
	if AuthorOf("") != AuthorLegacy {
		t.Errorf("an empty author reads as %q, want %q", AuthorOf(""), AuthorLegacy)
	}
	if AuthorOf("frontend") == AuthorLegacy {
		t.Error("`frontend` and `legacy` are indistinguishable on read, which is the whole distinction " +
			"the no-backfill rule exists to preserve")
	}
	if Authored("") {
		t.Error("an empty author is reported as authored")
	}
}
