package assessment

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// composite_fence_test.go is design D3 made reviewable (task 7.6, acceptance A8).
//
// # Why a fence and not just the absence
//
// A composite is the single most likely thing to be added later by request, in a hurry, by someone who
// did not read `report.go`. An absence is not self-defending. A fence is also the artifact that makes
// the REFUSAL reviewable: someone can read this file and see that the decision was made rather than
// overlooked.
//
// # What it actually asks
//
// Two questions, because "no composite" has two shapes and a word-list catches only one:
//
//  1. **Shape.** Does any function that can SEE more than one finding return a bare number? A
//     composite is exactly that: `func (a Assessment) Score() float64`. `Tally` returns a struct of
//     nine counts and passes, which is correct — a distribution is not a reduction.
//  2. **Vocabulary.** Does any exported identifier declared on the report side use composite words?
//     This catches the shape the first question misses: `Grade string`, `Level string`,
//     `MaturityBand Band` — a composite that is not a number at all.
//
// 🔴 Scope is THIS PACKAGE ONLY. The repo-wide version lives with the rest of the QA fences, because
// "no code path emits one" is a claim about the whole product and this file cannot see the console.

// compositeWords are the words a composite arrives under. `score` is absent on purpose and gets its
// own allowlist below: an eval set genuinely has a score, and banning the word here would force the
// honest name out of `EvalSetReport` and into a euphemism.
var compositeWords = []string{"grade", "maturity", "rating", "overall", "healthscore", "ranking", "percentile"}

// numericScalars are the result types a composite would arrive as.
var numericScalars = map[string]bool{"float64": true, "float32": true, "int": true, "int64": true}

// spanningTypes are the types that can SEE more than one axis. A number derived from one of these is
// a number derived from the whole report.
var spanningTypes = map[string]bool{"Assessment": true}

func parsePackage(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing the assessment package: %v", err)
	}
	var files []*ast.File
	for _, p := range pkgs {
		for _, f := range p.Files {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		t.Fatal("the fence parsed no files, so it is asserting nothing")
	}
	return fset, files
}

// TestNoFunctionReducesAnAssessmentToANumber is question 1.
func TestNoFunctionReducesAnAssessmentToANumber(t *testing.T) {
	fset, files := parsePackage(t)
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Type.Results == nil {
				continue
			}
			if !seesTheWholeReport(fn) {
				continue
			}
			for _, res := range fn.Type.Results.List {
				id, ok := res.Type.(*ast.Ident)
				if !ok || !numericScalars[id.Name] {
					continue
				}
				t.Fatalf("%s: %s reduces an assessment to a bare %s. That is a composite: ruling R4 "+
					"refuses one because no held-out set exists that would make it true, and a number "+
					"on a screen is quoted in a deck and written into a contract. If a summary is "+
					"genuinely needed, return a DISTRIBUTION the way Tally does.",
					fset.Position(fn.Pos()), fn.Name.Name, id.Name)
			}
		}
	}
}

// seesTheWholeReport reports whether a function can observe more than one finding — as a receiver, as
// a parameter, or as a slice of findings.
func seesTheWholeReport(fn *ast.FuncDecl) bool {
	if fn.Recv != nil {
		for _, r := range fn.Recv.List {
			if spanningTypes[typeName(r.Type)] {
				return true
			}
		}
	}
	if fn.Type.Params == nil {
		return false
	}
	for _, p := range fn.Type.Params.List {
		if spanningTypes[typeName(p.Type)] {
			return true
		}
		if arr, ok := p.Type.(*ast.ArrayType); ok && typeName(arr.Elt) == "Finding" {
			return true
		}
	}
	return false
}

func typeName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.StarExpr:
		return typeName(v.X)
	}
	return ""
}

// TestNoCompositeVocabularyIsDeclared is question 2 — the composite that is not a number.
func TestNoCompositeVocabularyIsDeclared(t *testing.T) {
	fset, files := parsePackage(t)
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			var name string
			var pos token.Pos
			switch v := n.(type) {
			case *ast.FuncDecl:
				name, pos = v.Name.Name, v.Pos()
			case *ast.TypeSpec:
				name, pos = v.Name.Name, v.Pos()
			case *ast.Field:
				if len(v.Names) == 0 {
					return true
				}
				name, pos = v.Names[0].Name, v.Pos()
			default:
				return true
			}
			lower := strings.ToLower(name)
			for _, w := range compositeWords {
				if strings.Contains(lower, w) {
					t.Fatalf("%s: %q names a composite. Nine axes do not reduce to one word any more "+
						"than they reduce to one number, and %q is how the second attempt arrives.",
						fset.Position(pos), name, w)
				}
			}
			return true
		})
	}
}

// TestTheOnlyScoreIsAnEvalSetsOwn is the allowlist, written as an assertion rather than as a comment.
//
// `Score` is a legitimate word here exactly once: an eval set produced a number, and that number is a
// measurement with an interval and a case list. Anywhere else it is the thing R4 refuses.
func TestTheOnlyScoreIsAnEvalSetsOwn(t *testing.T) {
	fset, files := parsePackage(t)
	const allowedOwner = "EvalSetReport"
	for _, f := range files {
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range st.Fields.List {
					for _, n := range field.Names {
						if !strings.Contains(strings.ToLower(n.Name), "score") {
							continue
						}
						if ts.Name.Name != allowedOwner {
							t.Fatalf("%s: %s.%s carries a score. The only score in this package is an "+
								"EVAL SET's, because it is a measurement with an interval and a case "+
								"list behind it. A score on anything else spans axes.",
								fset.Position(n.Pos()), ts.Name.Name, n.Name)
						}
					}
				}
			}
		}
	}
}
