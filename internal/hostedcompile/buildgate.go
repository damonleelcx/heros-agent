package hostedcompile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/sandbox"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/worktree"
)

// buildgate.go is the build gate that actually COMPILES — the one ADR-001 hangs delivery on.
//
// # Why it runs inside the isolate
//
// `go build` on a customer's repository executes their build. cgo directives, a `//go:generate`
// somebody wired into a build tag, a toolchain directive that fetches a compiler — the surface is not
// small, and internal/sandbox exists because "repo tool code from a target repo is untrusted" is the
// platform's sharpest boundary. So the compiler runs inside an isolate that scrubs the environment of
// ambient credentials, denies egress, scopes the filesystem to the working set, and bounds CPU, memory
// and wall clock.
//
// 🔴 AND IT FAILS CLOSED. If the enforcer cannot guarantee network denial or filesystem scope, the
// isolate is not created and this gate reports UNAVAILABLE. It does not fall back to building on the
// host. sandbox's own doc says why in one sentence — "a fallback-to-host on sandbox failure would
// convert an operational error into a full security bypass exactly when the environment is already
// degraded" — and a build gate is precisely where that temptation lives, because the alternative is a
// proposal that never becomes deliverable.
//
// # Three outcomes, and why the third is not a nicety
//
// A build gate has to distinguish "the change does not compile" from "we could not compile it", and the
// second has several causes that are NOT the candidate's fault:
//
//	no toolchain in this image        the deployment cannot build this language
//	the isolate cannot be created     the posture is not enforceable here
//	dependencies cannot be resolved   egress is denied and the tree is not vendored
//
// Every one of them, reported as Builds=false, marks the proposal `build_failed` — a permanent verdict
// that the code we wrote does not compile, which we did not establish. The candidate is retired for an
// environment problem, and the log says "cannot find module providing package". So they are Unavailable
// with the reason, and the proposal stays `unbuilt`.

// SandboxGate compiles a candidate's transformed tree inside an isolate.
type SandboxGate struct {
	// Language selects the toolchain. Only Go is implemented; every other language reports unavailable
	// rather than passing, for the reason ParseGate does.
	Language string
	// Root is the materialized snapshot the patch's files are relative to. Copied, never written.
	Root string
	// Sandbox runs the compiler. Required: a nil sandbox is reported unavailable, never bypassed.
	Sandbox *sandbox.Sandbox
	// GoBin is the toolchain. Explicit because "deterministic build (pinned toolchain)" is a
	// requirement: whatever is first on PATH is not pinned, and two runs of one config_hash on two
	// machines must not build with different compilers.
	GoBin string
	// Bounds caps what the customer's build may consume. Zero takes sandbox's defaults.
	Bounds sandbox.ResourceBounds
}

// Check materializes the transformed tree and compiles it inside the isolate.
func (g SandboxGate) Check(ctx context.Context, patch *transform.Patch) (proposal.BuildResult, error) {
	if patch == nil {
		return proposal.BuildResult{}, fmt.Errorf("hostedcompile: the build gate requires a patch")
	}
	if lang := strings.ToLower(strings.TrimSpace(g.Language)); lang != "go" {
		return proposal.BuildResult{Unavailable: unavailableFor(lang)}, nil
	}
	if g.Sandbox == nil {
		// Not a fallback to the host. A build gate with no isolate is a build gate that must not run.
		return proposal.BuildResult{Unavailable: "No build gate ran: this deployment configured no " +
			"isolate, and compiling a customer's repository outside one is not an option this gate has."}, nil
	}
	bin := g.GoBin
	if bin == "" {
		bin = "go"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return proposal.BuildResult{Unavailable: noToolchain(bin)}, nil
	}

	// The transformed tree: a throwaway copy of the snapshot with the codemod's files overlaid. The
	// snapshot itself is never written — the diff must be built as the code that would ship, without
	// mutating the tree the IR was derived from.
	work, err := os.MkdirTemp("", "heros-buildgate-*")
	if err != nil {
		return proposal.BuildResult{}, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	if err := copyTree(g.Root, work); err != nil {
		return proposal.BuildResult{}, fmt.Errorf("hostedcompile: stage the transformed tree: %w", err)
	}
	for rel, b := range patch.Files {
		dst := filepath.Join(work, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return proposal.BuildResult{}, err
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			return proposal.BuildResult{}, err
		}
	}

	// 🔴 `-mod=vendor` when the tree is vendored, `-mod=mod` otherwise — and the distinction decides
	// whether this can work at all. The isolate denies egress, so an unvendored module graph cannot be
	// fetched; the build then fails with a missing-module error that has nothing to do with the change.
	// Vendored is the only configuration this gate can actually judge, and the other is reported as
	// unavailable below rather than as a rejection.
	vendored := dirExists(filepath.Join(work, "vendor"))
	modFlag := "-mod=mod"
	if vendored {
		modFlag = "-mod=vendor"
	}

	// 🔴 THE ARGV IS SHAPED BY HOW THE ISOLATE STAGES THE TREE, and getting it wrong looks like a build
	// failure rather than a mistake. The isolate copies each working-set path to
	// `<scratch>/work/<basename>` READ-ONLY and sets the child's cwd to `<scratch>/work` — so `./...`
	// resolves in the scratch, not in the module, and `go build` answers "directory prefix . does not
	// contain main module", which is indistinguishable from a broken repository at a glance.
	//
	//	-C <basename>   run in the staged module, since cwd is its parent
	//	-o os.DevNull   discard the binaries. The staged tree is READ-ONLY, so there is nowhere in it to
	//	                write them, and this gate wants the compiler's verdict rather than its output.
	//
	// HOME and TMPDIR point into the scratch (scrubbedEnv), so the build and module caches land on the
	// one writable surface the isolate has.
	staged := filepath.Base(work)

	res, err := g.Sandbox.Run(ctx, sandbox.Spec{
		NodeID:     "hostedcompile",
		WorkingSet: []string{work},
		Bounds:     g.Bounds,
		// Both required, so the isolate FAILS CLOSED where they cannot be enforced. This is the line
		// that decides whether a build gate is a security boundary or a hope.
		RequireNetworkIsolation: true,
		RequireFilesystemScope:  true,
	}, sandbox.Tool{
		Argv: []string{bin, "build", "-C", staged, modFlag, "-o", os.DevNull, "./..."},
	})
	// 🔴 THREE KINDS OF `err`, AND THEY ARE NOT THE SAME ANSWER. sandbox.Run returns an error for any
	// non-zero exit, so the compiler REJECTING the change arrives here as a failure — and taking that at
	// face value would report every genuine build failure as a gate malfunction, which is the exact
	// inversion of the mistake this file is otherwise written against.
	//
	//	ErrIsolateUnavailable   the posture could not be established  → unavailable
	//	ErrResourceBreach       OUR bounds stopped the build          → unavailable
	//	anything else non-zero  the compiler judged the code          → a verdict
	//
	// The resource breach is the subtle one: a build killed by our CPU or wall-clock cap did not fail to
	// compile, it was not allowed to finish. Recording that as `build_failed` retires a candidate
	// because the bound was too tight.
	switch {
	case errors.Is(err, sandbox.ErrIsolateUnavailable):
		return proposal.BuildResult{Unavailable: isolateUnavailable(err)}, nil
	case errors.Is(err, sandbox.ErrResourceBreach):
		return proposal.BuildResult{Unavailable: resourceBreach(res)}, nil
	}
	if res == nil {
		return proposal.BuildResult{}, fmt.Errorf("hostedcompile: build gate: %w", err)
	}

	log := strings.TrimSpace(string(res.Stdout) + "\n" + string(res.Stderr))
	if err == nil && res.ExitCode == 0 {
		return proposal.BuildResult{Builds: true, Log: log}, nil
	}
	// A non-zero exit is a REJECTION only when the compiler was judging the code. When it could not
	// resolve the module graph, it was judging the environment.
	if !vendored && looksLikeUnresolvedDependencies(log) {
		return proposal.BuildResult{Unavailable: unresolvedDependencies(log)}, nil
	}
	return proposal.BuildResult{Builds: false, Log: log}, nil
}

// Strength is what a passing run of this gate proved.
func (g SandboxGate) Strength() worktree.Strength {
	if strings.EqualFold(strings.TrimSpace(g.Language), "go") {
		return worktree.StrengthTypeChecked
	}
	return ""
}

// looksLikeUnresolvedDependencies reports whether the compiler failed because it could not obtain the
// module graph, rather than because the code is wrong.
//
// ⚠️ It matches on the toolchain's MESSAGE TEXT, which is the weakest kind of check and is used here
// because `go build` exits 1 for both cases and offers nothing else to key on. The direction of the
// weakness is deliberate: a missed match reports a real-looking build failure (the candidate is
// retired, and the log plainly says the module could not be found, so a human reading it is not
// misled), while a false match would report a genuine compile error as an environment problem and
// leave a broken change looking merely un-judged. So the patterns are narrow and anchored on the
// module system, not on the word "error".
func looksLikeUnresolvedDependencies(log string) bool {
	l := strings.ToLower(log)
	for _, sig := range []string{
		"cannot find module providing package",
		"missing go.sum entry",
		"module lookup disabled",
		"dial tcp",
		"no such host",
		"connection refused",
		"proxy.golang.org",
		"go: downloading",
	} {
		if strings.Contains(l, sig) {
			return true
		}
	}
	return false
}

func noToolchain(bin string) string {
	return fmt.Sprintf("No build gate ran: %q is not on PATH in this image, so the compiler could not "+
		"be invoked. The diff is generated and reviewable; it is not verified to build.", bin)
}

func isolateUnavailable(err error) string {
	return "No build gate ran: the isolate could not be created with every restriction a customer's " +
		"build requires (" + err.Error() + "). Compiling untrusted source outside the isolate is not " +
		"a fallback this gate takes — a fallback-to-host on sandbox failure turns an operational error " +
		"into a security bypass. The diff is generated and reviewable; it is not verified to build."
}

func unresolvedDependencies(log string) string {
	return "No build gate ran: the compiler could not resolve this module's dependencies. The isolate " +
		"denies network egress, so an unvendored module graph cannot be fetched — commit `vendor/` for " +
		"this revision and the build gate will judge the change. This is NOT a verdict about the " +
		"change:\n\n" + log
}

// resourceBreach names OUR limit as the cause, so nobody reads it as the change's fault.
func resourceBreach(res *sandbox.Result) string {
	what := "a resource bound"
	if res != nil && res.ResourceBreach != "" {
		what = res.ResourceBreach
	}
	return "No build gate ran to completion: the build was stopped by " + what + ". That is this " +
		"platform's limit, not a property of the change — the compiler never reached a verdict. Raise " +
		"the isolate's bounds for this workflow, or vendor the tree so the build does less work."
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// copyTree copies a directory tree, files only, preserving structure.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			// Symlinks and devices are not copied. A symlink out of the working set is exactly what the
			// filesystem scope exists to prevent, and recreating one here would carry it inside.
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
}
