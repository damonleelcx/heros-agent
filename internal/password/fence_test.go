package password

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fence_test.go is the 🔴 rule ADR-012 Decision 2 turns on, made machine-enforced.
//
//	a value with 256 bits of crypto/rand behind it is looked up by SHA-256 hash (tenancy.HashSecret);
//	a value a person typed is verified by argon2id (this package).
//
// Both functions are correct, and each is catastrophic in the other's place. `HashSecret` on a password is the
// L1-for-L8 trade the priority law names as its first example — it would look like ordinary reuse in review,
// produce a working sign-in, pass every behavioural test, and leave the whole table crackable at GPU speed.
//
// A comment cannot stop that; this can. The database holds the same rule from the other side
// (`user_password.encoded` CHECKs the argon2id tag), so a code path that gets past this fence still cannot
// write past that one.
//
// # Why an AST scan rather than a type-checked analysis
//
// The thing being detected is a NAME — a variable a human called `password` handed to the wrong hasher — and
// names are exactly what an AST carries. A full type-check would tell us both are strings, which is true and
// useless.

// bannedArgs are the identifier fragments that mean "this is a human-chosen secret". Matched
// case-insensitively against the rendered argument expression.
var bannedArgs = []string{"password", "passphrase", "plaintext", "pwd"}

func TestNoPasswordReachesTheMintedSecretHash(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	fset := token.NewFileSet()

	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// `testdata` holds fixtures the discovery engine parses, including deliberately malformed ones. It is
		// skipped by name rather than by tolerating parse errors: a parse error ANYWHERE ELSE means this fence
		// silently stopped scanning a file, which is the failure mode a fence must not have.
		if info.IsDir() {
			if info.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// This file names the banned identifiers in order to ban them.
		if strings.HasSuffix(path, "fence_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			if !isHashSecret(call.Fun) {
				return true
			}
			arg := strings.ToLower(render(call.Args[0]))
			for _, banned := range bannedArgs {
				if strings.Contains(arg, banned) {
					violations = append(violations, fset.Position(call.Pos()).String()+
						": HashSecret("+render(call.Args[0])+")")
					break
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(violations) > 0 {
		t.Fatalf("a human-chosen secret was passed to the MINTED-secret hash (SHA-256). Use password.Hash / "+
			"password.Verify — see ADR-012 Decision 2:\n  %s", strings.Join(violations, "\n  "))
	}
}

// isHashSecret matches both `tenancy.HashSecret(x)` and, from inside the tenancy package, `HashSecret(x)`.
func isHashSecret(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name == "HashSecret"
	case *ast.SelectorExpr:
		return f.Sel.Name == "HashSecret"
	}
	return false
}

// render renders an expression back to something close enough to source for a substring test.
func render(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return render(x.X) + "." + x.Sel.Name
	case *ast.CallExpr:
		var args []string
		for _, a := range x.Args {
			args = append(args, render(a))
		}
		return render(x.Fun) + "(" + strings.Join(args, ", ") + ")"
	case *ast.IndexExpr:
		return render(x.X) + "[" + render(x.Index) + "]"
	case *ast.StarExpr:
		return "*" + render(x.X)
	case *ast.UnaryExpr:
		return x.Op.String() + render(x.X)
	case *ast.BasicLit:
		return x.Value
	case *ast.ParenExpr:
		return "(" + render(x.X) + ")"
	}
	return ""
}

// The fence has to be able to FAIL, or it is decoration. This asserts the detector on a synthetic tree rather
// than by breaking the repository, so the proof lives beside the fence instead of in a commit message.
func TestFenceDetectorActuallyFires(t *testing.T) {
	const offending = `package x

func f(password string) string { return tenancy.HashSecret(password) }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", offending, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fired := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 || !isHashSecret(call.Fun) {
			return true
		}
		arg := strings.ToLower(render(call.Args[0]))
		for _, banned := range bannedArgs {
			if strings.Contains(arg, banned) {
				fired = true
			}
		}
		return true
	})
	if !fired {
		t.Fatal("the fence does not detect the violation it exists to detect")
	}
}
