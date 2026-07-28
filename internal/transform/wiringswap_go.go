package transform

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

// wiringswap_go.go resolves a Go call site to its enclosing STATEMENT and says what that statement
// binds and reads (P15 15c §13).
//
// # Why the statement, not the call
//
// Discovery points at a call expression. A call is not a movable unit: `x := client.Do(req)` cannot be
// exchanged with its neighbour by moving the call — the binding goes with it or the program stops
// compiling. So the unit here is the statement that CONTAINS the call, and it must be a direct child
// of a block (a sibling), because that is the only position where "exchange with the next one" has an
// unambiguous meaning.
//
// # Why go/ast rather than lines
//
// Independence is the load-bearing check, and it needs to know which identifiers a statement BINDS and
// which it READS. Go's AST answers that exactly: the left side of an assignment or a short variable
// declaration binds; every other identifier occurrence reads. A line-based approximation would have to
// guess, and a wrong guess here does not fail loudly — it produces a program that compiles and computes
// something else.

// resolveGoStatement finds the statement enclosing the given 1-based line and projects it into the
// shape the swap needs. Every failure is a refusal naming what could not be established.
func resolveGoStatement(src []byte, nodeID string, line int) (stmtBlock, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "site.go", src, parser.ParseComments)
	if err != nil {
		return stmtBlock{}, refuseWiringMaterialize(nodeID, fmt.Sprintf(
			"the file containing %s does not parse as Go (%v), so this engine cannot say what the "+
				"statement binds or reads", nodeID, err))
	}

	// The INNERMOST block containing the line, and within it the direct child statement covering the
	// line. Innermost matters: a call inside an `if` body is a sibling of that body's statements, not of
	// the statements around the `if`.
	var found ast.Stmt
	var foundBlock *ast.BlockStmt
	ast.Inspect(file, func(n ast.Node) bool {
		block, ok := n.(*ast.BlockStmt)
		if !ok {
			return true
		}
		for _, st := range block.List {
			if fset.Position(st.Pos()).Line <= line && line <= fset.Position(st.End()).Line {
				found, foundBlock = st, block // keep descending: a nested block overwrites this
			}
		}
		return true
	})
	if found == nil || foundBlock == nil {
		return stmtBlock{}, refuseWiringMaterialize(nodeID, fmt.Sprintf(
			"the call for %s at line %d is not a direct statement of any block (it may be an argument, a "+
				"composite literal element, or a package-level declaration), so there is no sibling "+
				"statement to exchange it with", nodeID, line))
	}

	start := fset.Position(found.Pos()).Offset
	end := fset.Position(found.End()).Offset
	startByte, endByte, startLine, endLine, indent, ok := wholeLineSpan(src, start, end, "//")
	if !ok {
		return stmtBlock{}, refuseWiringMaterialize(nodeID, fmt.Sprintf(
			"the statement for %s shares its line with other code, so it cannot be exchanged by moving "+
				"whole lines", nodeID))
	}

	blk := stmtBlock{
		nodeID: nodeID, startLine: startLine, endLine: endLine,
		startByte: startByte, endByte: endByte, indent: indent,
		binds: map[string]bool{}, reads: map[string]bool{},
	}
	blk.control, blk.kind = goControlKind(found)
	goBindsAndReads(found, blk.binds, blk.reads)
	return blk, nil
}

// goControlKind reports whether a statement's POSITION is part of its meaning. These are not refused
// because they are hard to move — they are refused because moving them is a different change:
// exchanging a `return` with its neighbour deletes the neighbour's execution.
func goControlKind(st ast.Stmt) (bool, string) {
	switch s := st.(type) {
	case *ast.ReturnStmt:
		return true, "return statement"
	case *ast.BranchStmt:
		return true, s.Tok.String() + " statement"
	case *ast.DeferStmt:
		return true, "defer statement"
	case *ast.GoStmt:
		return true, "go statement"
	case *ast.LabeledStmt:
		return true, "labeled statement"
	case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
		// A compound statement CAN be exchanged in principle, but its body may bind names in ways this
		// analysis does not model (a `for` binds its loop variable, an `if` its init). Refuse rather than
		// analyse a shape whose independence we cannot state precisely.
		return true, "compound statement"
	default:
		return false, "statement"
	}
}

// goBindsAndReads fills the two name sets.
//
// The rule is deliberately blunt: identifiers on the left of `=` / `:=`, and names declared by a `var`
// or `const` statement, BIND; every other identifier occurrence READS. Blunt is right here — the sets
// are used only to REFUSE, so over-counting reads costs a missed opportunity while under-counting them
// would let a dependent pair through.
//
// Two exclusions, both to avoid false dependencies rather than to admit more swaps:
//
//	`_`          the blank identifier binds nothing and can be shared freely.
//	selector     in `a.b`, only `a` is a name in this scope; `b` is a field, and treating it as one
//	             would make two unrelated statements that both write `.Body` look dependent.
func goBindsAndReads(st ast.Stmt, binds, reads map[string]bool) {
	record := func(set map[string]bool, id *ast.Ident) {
		if id == nil || id.Name == "_" {
			return
		}
		set[id.Name] = true
	}

	switch s := st.(type) {
	case *ast.AssignStmt:
		for _, lhs := range s.Lhs {
			if id, ok := lhs.(*ast.Ident); ok {
				record(binds, id)
				continue
			}
			// `m[k] = v` / `p.f = v`: the target is not a fresh binding but the container IS mutated, so
			// the base name counts as BOTH — a later reader of it is dependent on this statement.
			collectGoReads(lhs, reads)
			if base := goBaseIdent(lhs); base != nil {
				record(binds, base)
			}
		}
		for _, rhs := range s.Rhs {
			collectGoReads(rhs, reads)
		}
	case *ast.DeclStmt:
		if gd, ok := s.Decl.(*ast.GenDecl); ok {
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					record(binds, name)
				}
				for _, v := range vs.Values {
					collectGoReads(v, reads)
				}
			}
		}
	default:
		collectGoReads(st, reads)
	}
}

// collectGoReads records every identifier an expression reads, skipping selector fields and the blank
// identifier.
func collectGoReads(n ast.Node, reads map[string]bool) {
	ast.Inspect(n, func(x ast.Node) bool {
		switch e := x.(type) {
		case *ast.SelectorExpr:
			collectGoReads(e.X, reads) // the base is a name; the field is not
			return false
		case *ast.Ident:
			if e.Name != "_" {
				reads[e.Name] = true
			}
		}
		return true
	})
}

// goBaseIdent returns the root identifier of an assignable expression (`m[k]` → `m`, `p.f.g` → `p`).
func goBaseIdent(e ast.Expr) *ast.Ident {
	for {
		switch v := e.(type) {
		case *ast.Ident:
			return v
		case *ast.SelectorExpr:
			e = v.X
		case *ast.IndexExpr:
			e = v.X
		case *ast.StarExpr:
			e = v.X
		case *ast.ParenExpr:
			e = v.X
		default:
			return nil
		}
	}
}
