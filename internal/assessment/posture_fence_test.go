package assessment

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// posture_fence_test.go is task 6.3 as a machine: **assessment executes customer code UNDER the P3
// sandbox, not beside it.**
//
// # Why a static fence and not a review note
//
// "Under the sandbox" is a property that is true until somebody needs a quick answer at 6pm and writes
// `exec.Command("python", entry)` — which works, which passes every functional test, and which runs a
// customer's code in this process with this process's credentials and this process's filesystem. There
// is nothing about that diff that looks alarming in review; it looks like the obvious way to run a
// program.
//
// So the package is forbidden the ABILITY. It imports no process API, no raw networking and no HTTP
// client, and the only way it can cause customer code to execute is by asking `Sandbox` — an interface
// whose single method returns a BOOLEAN and cannot run anything at all.

// TestThisPackageCannotExecuteAnything is the fence.
func TestThisPackageCannotExecuteAnything(t *testing.T) {
	banned := map[string]string{
		"os/exec": "executing a process from here runs customer code BESIDE the P3 sandbox rather than " +
			"under it — in this process, with this process's credentials and filesystem. Ask `Sandbox` " +
			"instead; running belongs inside internal/sandbox, where the posture lives",
		"syscall":          "a syscall here is process control by another name",
		"golang.org/x/sys": "the same, with a nicer import path",
		"net/http": "an HTTP client here bypasses providergateway's secrets source, its retries carrying " +
			"an idempotency key (a retry the provider treats as a new request is a DOUBLE CHARGE), and " +
			"the observer that makes a provider outage visible",
		"net": "raw networking from the assessment package is an egress surface nobody reviewed",
		"plugin": "loading a plugin is executing code chosen at runtime, which is the thing the sandbox " +
			"exists to contain",
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
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
	// 🔴 ANTI-VACUITY. A parse that reached no files would report clean forever, and this file would
	// look like a guarantee while asserting nothing.
	if files < 8 {
		t.Fatalf("the scan read only %d file(s) — it is not seeing the package, so its clean report "+
			"above means nothing", files)
	}
}

// TestTheSandboxSeamCannotRunAnything is the other half, and it is the one that survives a refactor.
//
// An import ban stops the obvious route. This asserts the SHAPE: the interface this package uses to
// reach the sandbox has one method, it answers a question, and there is nowhere in its signature for
// a command, an argument list, an environment or a working directory to travel.
func TestTheSandboxSeamCannotRunAnything(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	var decl string
	for _, p := range pkgs {
		for _, f := range p.Files {
			src, err := os.ReadFile(fset.Position(f.Pos()).Filename)
			if err != nil {
				continue
			}
			body := string(src)
			if i := strings.Index(body, "type Sandbox interface {"); i >= 0 {
				decl = body[i : i+strings.Index(body[i:], "\n}")]
			}
		}
	}
	if decl == "" {
		t.Fatal("the Sandbox interface was not found — this fence's scan is broken, not the code")
	}
	methods := 0
	for _, line := range strings.Split(decl, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "type ") {
			continue
		}
		methods++
	}
	if methods != 1 {
		t.Fatalf("the Sandbox seam has %d methods. One is the whole guarantee: it ASKS whether the "+
			"sandbox would execute a workflow, and the running happens inside internal/sandbox where "+
			"the posture lives.\n%s", methods, decl)
	}
	for _, word := range []string{"Run(", "Exec(", "Start(", "cmd", "argv", "env"} {
		if strings.Contains(decl, word) {
			t.Fatalf("the Sandbox seam names %q. There must be nowhere in this signature for a command, "+
				"an argument list or an environment to travel:\n%s", word, decl)
		}
	}
}
