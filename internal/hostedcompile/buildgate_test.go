package hostedcompile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/sandbox"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/worktree"
)

// buildgate_test.go proves the gate that COMPILES: that it really rejects code that does not build,
// that it really passes code that does, and — the part that matters most — that every way it can fail
// to run is reported as unavailable rather than as a verdict about the change.

// goModule writes a tiny self-contained Go module: no dependencies, so the isolate's denied egress is
// not in the way of judging the code itself.
func goModule(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/target\n\ngo 1.24\n")
	write("main.go", body)
	return root
}

// hostSandbox is the isolate as this machine can enforce it.
//
// NewSubprocessEnforcer advertises NetworkDeny=false and FilesystemScope=false on a bare host, so a
// spec REQUIRING them fails closed — which is precisely the behaviour the unavailable-path tests below
// exercise, and it is not a weakness of the test. The contained enforcer is what a deployment running
// under deploy/docker-compose.sandbox.yml gets.
func hostSandbox() *sandbox.Sandbox { return sandbox.New(sandbox.NewSubprocessEnforcer()) }

// containedSandbox advertises the posture a sandbox-profile deployment provides, so the gate's own
// logic can be exercised. It does NOT make this host isolated — it stands in for the runtime that is.
func containedSandbox() *sandbox.Sandbox { return sandbox.New(sandbox.NewContainedEnforcer()) }

func requireGo(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no Go toolchain on PATH; the compiling gate has nothing to run")
	}
	return bin
}

func patchOf(files map[string][]byte) *transform.Patch {
	return &transform.Patch{Files: files, Diff: []byte("--- a\n+++ b\n"), DiffHash: strings.Repeat("d", 64)}
}

// 🔴 THE CLAIM. The gate compiles the transformed tree and passes code that builds.
func TestTheGateCompilesAndPasses(t *testing.T) {
	bin := requireGo(t)
	root := goModule(t, "package main\n\nfunc main() { println(greeting()) }\n\nfunc greeting() string { return \"a\" }\n")

	res, err := SandboxGate{Language: "go", Root: root, Sandbox: containedSandbox(), GoBin: bin}.
		Check(context.Background(), patchOf(map[string][]byte{
			"main.go": []byte("package main\n\nfunc main() { println(greeting()) }\n\nfunc greeting() string { return \"b\" }\n"),
		}))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Unavailable != "" {
		t.Fatalf("the gate could not run: %s", res.Unavailable)
	}
	if !res.Builds {
		t.Fatalf("a building change was rejected:\n%s", res.Log)
	}
}

// ...and REJECTS a change that does not compile. A parse gate cannot catch this: the file is
// syntactically perfect and the types are wrong, which is the whole reason a compile gate is what
// delivery requires.
func TestTheGateRejectsATypeError(t *testing.T) {
	bin := requireGo(t)
	root := goModule(t, "package main\n\nfunc main() { println(greeting()) }\n\nfunc greeting() string { return \"a\" }\n")

	broken := "package main\n\nfunc main() { println(greeting()) }\n\nfunc greeting() string { return 42 }\n"
	// The premise: this file PARSES. If it did not, this test would be re-proving the parse gate.
	if pr, _ := (ParseGate{Language: "go"}).Check(context.Background(),
		patchOf(map[string][]byte{"main.go": []byte(broken)})); pr.Builds || pr.Unavailable == "" {
		t.Fatalf("the fixture does not parse cleanly, so this test would not be about the compiler: %+v", pr)
	}

	res, err := SandboxGate{Language: "go", Root: root, Sandbox: containedSandbox(), GoBin: bin}.
		Check(context.Background(), patchOf(map[string][]byte{"main.go": []byte(broken)}))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Unavailable != "" {
		t.Fatalf("the gate could not run: %s", res.Unavailable)
	}
	if res.Builds {
		t.Fatal("a change that does not type-check passed the build gate")
	}
	if !strings.Contains(res.Log, "greeting") && !strings.Contains(res.Log, "main.go") {
		t.Errorf("the rejection must carry the compiler's own diagnosis, got %q", res.Log)
	}
}

// 🔴 The gate FAILS CLOSED. On a host that cannot deny egress or scope the filesystem, the isolate is
// not created and the gate reports unavailable — it does not build on the host.
//
// This is the test that would go quiet if somebody "fixed" the gate by dropping the requirements, so it
// asserts the reason as well as the outcome.
func TestTheGateFailsClosedRatherThanBuildingOnTheHost(t *testing.T) {
	bin := requireGo(t)
	root := goModule(t, "package main\n\nfunc main() {}\n")

	caps := sandbox.NewSubprocessEnforcer().Capabilities()
	if caps.NetworkDeny && caps.FilesystemScope {
		t.Skip("this host enforces both restrictions; the fail-closed path cannot be reached here")
	}

	res, err := SandboxGate{Language: "go", Root: root, Sandbox: hostSandbox(), GoBin: bin}.
		Check(context.Background(), patchOf(map[string][]byte{"main.go": []byte("package main\n\nfunc main() {}\n")}))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Builds {
		t.Fatal("the gate compiled a customer's repository outside an isolate that could not enforce " +
			"network denial or filesystem scope — a fallback-to-host on sandbox failure is a security " +
			"bypass, not a degraded mode")
	}
	if res.Unavailable == "" {
		t.Fatal("an unenforceable isolate was reported as a build FAILURE — the candidate would be " +
			"retired for an environment problem")
	}
	if !strings.Contains(res.Unavailable, "isolate") {
		t.Errorf("the reason must name what could not be established, got %q", res.Unavailable)
	}
}

// Every "could not run" cause is Unavailable, never Builds=false. Each would otherwise mark the
// proposal `build_failed` — a permanent verdict that the code does not compile, which nobody
// established.
func TestEveryUnrunnableCauseIsUnavailable(t *testing.T) {
	root := goModule(t, "package main\n\nfunc main() {}\n")
	p := patchOf(map[string][]byte{"main.go": []byte("package main\n\nfunc main() {}\n")})

	for name, gate := range map[string]SandboxGate{
		"no isolate configured": {Language: "go", Root: root},
		"no toolchain in the image": {Language: "go", Root: root, Sandbox: containedSandbox(),
			GoBin: "definitely-not-a-real-compiler"},
		"a language this deployment cannot build": {Language: "rust", Root: root,
			Sandbox: containedSandbox(), GoBin: "go"},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := gate.Check(context.Background(), p)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if res.Builds {
				t.Error("a gate that could not run reported a PASS")
			}
			if res.Unavailable == "" {
				t.Error("a gate that could not run reported a build FAILURE, retiring the candidate for " +
					"an environment problem")
			}
		})
	}
}

// A module graph that cannot be fetched is an ENVIRONMENT problem, not a verdict. The isolate denies
// egress by design, so an unvendored tree cannot resolve its dependencies — and reporting that as
// `build_failed` retires a change whose only fault is that nobody vendored the repository.
func TestUnresolvedDependenciesAreNotAVerdict(t *testing.T) {
	for _, log := range []string{
		"go: example.com/dep@v1.2.3: cannot find module providing package example.com/dep",
		"go: missing go.sum entry for module providing package example.com/dep",
		"go: example.com/dep@v1.0.0: dial tcp: lookup proxy.golang.org: no such host",
	} {
		if !looksLikeUnresolvedDependencies(log) {
			t.Errorf("a dependency-resolution failure was not recognised: %q", log)
		}
	}
	// ...and a real compile error must NOT be excused as one. A false match would leave a broken change
	// looking merely un-judged.
	for _, log := range []string{
		"./main.go:5:9: cannot use 42 (untyped int constant) as string value in return statement",
		"./main.go:3:1: syntax error: unexpected }",
		"# example.com/target\n./x.go:1:1: undefined: foo",
	} {
		if looksLikeUnresolvedDependencies(log) {
			t.Errorf("a genuine compile error was excused as a dependency problem: %q", log)
		}
	}
}

// The strength claim matches what ran: a compile is type-checked, which is the level
// Strength.AllowsAutonomousApply requires — and the parse gate can never reach it.
func TestTheCompilingGateClaimsTypeChecked(t *testing.T) {
	if got := (SandboxGate{Language: "go"}).Strength(); got != worktree.StrengthTypeChecked {
		t.Errorf("strength = %q, want %q", got, worktree.StrengthTypeChecked)
	}
	if got := (SandboxGate{Language: "python"}).Strength(); got.Valid() {
		t.Errorf("a language this gate cannot build claimed strength %q", got)
	}
	// The ladder: the parse gate must never claim what the compiling one does.
	if (ParseGate{Language: "go"}).Strength() == (SandboxGate{Language: "go"}).Strength() {
		t.Error("the parse gate and the compiling gate claim the same strength; the whole reason both " +
			"exist is that one proves strictly less")
	}
}

// The compiler picks the strongest gate the deployment can run, and never assumes one.
func TestTheCompilerPicksTheStrongestAvailableGate(t *testing.T) {
	withIsolate := (&Compiler{Sandbox: containedSandbox()}).gateFor("go", "/x")
	if _, ok := withIsolate.(SandboxGate); !ok {
		t.Errorf("with an isolate the gate must compile, got %T", withIsolate)
	}
	without := (&Compiler{}).gateFor("go", "/x")
	if _, ok := without.(ParseGate); !ok {
		t.Errorf("with no isolate the gate must fall back to parsing, got %T", without)
	}
}

// The snapshot is never written. The gate builds a COPY; the tree the IR was derived from must come out
// byte-identical, or a second compile of the same proposal would be building different code.
func TestTheSnapshotIsNotMutated(t *testing.T) {
	bin := requireGo(t)
	const original = "package main\n\nfunc main() { println(\"original\") }\n"
	root := goModule(t, original)

	if _, err := (SandboxGate{Language: "go", Root: root, Sandbox: containedSandbox(), GoBin: bin}).
		Check(context.Background(), patchOf(map[string][]byte{
			"main.go": []byte("package main\n\nfunc main() { println(\"rewritten\") }\n"),
		})); err != nil {
		t.Fatalf("check: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != original {
		t.Errorf("the gate wrote into the snapshot:\n%s", b)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("the gate left artifacts in the snapshot: %v", entries)
	}
}

var _ proposal.BuildChecker = SandboxGate{}
