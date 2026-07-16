package discovery

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The no-execution invariant (I1) is enforced STRUCTURALLY: the discovery analysis path must not import
// any package that can spawn a process, load a plugin, or shell out to the go toolchain (`go list`).
// This test parses the package's own source and fails if a forbidden import appears — the structural
// half of §7.1, kept here because it is cheap and must always be able to go red.
func TestNoExecutionImports(t *testing.T) {
	forbidden := map[string]string{
		"os/exec":                        "spawns processes",
		"plugin":                         "loads plugins (arbitrary code)",
		"golang.org/x/tools/go/packages": "shells out to `go list` (subprocess)",
		"go/build":                       "can invoke the go toolchain",
		// Least-privilege / no-egress (NFR7): the analysis path must not reach the network.
		"net":      "network access",
		"net/http": "network egress",
	}

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var checked int
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if why, bad := forbidden[path]; bad {
				t.Errorf("%s imports forbidden %q (%s) — violates no-execution invariant I1", name, path, why)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test .go files were checked; guard is not actually running")
	}
}
