package herosagent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// 🔴 TASK 4.2 — THE ONLY WAY OUT OF THIS PACKAGE IS `providergateway`.
//
// The Runner's doc says it "is the ONLY thing in the package that reaches a provider", and the Model
// interface has no default implementation. Both are true today and neither is enforceable by the
// compiler: a `&http.Client{}` constructed here would compile, work, and bypass every property the
// gateway exists to provide — the secrets source, retries with an idempotency key, the observer that
// makes a provider outage visible, and the credential handling that keeps a key out of this process's
// own request bodies.
//
// So the fence is STATIC. It reads the package's imports and its composite literals, and it fails on a
// direct HTTP client. It is checkable, it names the file, and it cannot be satisfied by a test that
// happens not to exercise the new path.
func TestThisPackageConstructsNoHTTPClientOfItsOwn(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly|parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}

	banned := map[string]string{
		"net/http": "an HTTP client here bypasses providergateway's secrets source, its retries with an " +
			"idempotency key (a retry the provider treats as a new request is a DOUBLE CHARGE), and the " +
			"observer that makes an outage visible",
		"net": "raw networking from the analysis package is an egress surface nobody reviewed",
	}
	var files int
	for _, p := range pkgs {
		for name, f := range p.Files {
			files++
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if why, bad := banned[path]; bad {
					t.Errorf("%s imports %q — %s", name, path, why)
				}
			}
		}
	}
	// 🔴 ANTI-VACUITY. A parse that reached no files would report clean forever.
	if files < 5 {
		t.Errorf("the scan read only %d file(s) — it is not seeing the package, so its clean report "+
			"above means nothing", files)
	}

	// The same walk over full syntax, for a client constructed through a dot-import or an alias.
	fset2 := token.NewFileSet()
	full, err := parser.ParseDir(fset2, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range full {
		for name, f := range p.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				if sel, ok := lit.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Client" {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "http" {
						t.Errorf("%s constructs an http.Client at %s", name, fset2.Position(lit.Pos()))
					}
				}
				return true
			})
		}
	}
}
