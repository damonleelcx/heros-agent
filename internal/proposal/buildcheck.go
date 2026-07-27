package proposal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/heros-foreal/agentd/internal/transform"
)

// BuildStatus is a candidate's build-gate outcome. A build_failed candidate is retained for
// diagnostics but is NEVER ranked, verified, or surfaced (ADR-001, §1b.2).
type BuildStatus string

const (
	BuildUnbuilt BuildStatus = "unbuilt"
	BuildBuilt   BuildStatus = "built"
	BuildFailed  BuildStatus = "build_failed"
	// BuildRefused is a candidate the TRANSFORM refused before a diff existed — an un-applicable skill
	// binding, a prune over a runtime-assembled tool set (P14 D-14.3). It is not a build failure and must
	// not be reported as one: a build failure means "we wrote code and it did not compile", a refusal
	// means "we declined to write code we could not stand behind", and a user acts on them differently.
	//
	// 🔴 It is a STATUS rather than an error for the reason task 8.2 exists: a refusal returned as an
	// error aborts the whole batch and disappears into a log, so the one change the engine deliberately
	// declined to make is the one the user never hears about. Carried as a status, it reaches the surface
	// by name, next to the candidates that did compile.
	BuildRefused BuildStatus = "refused"
)

// BuildResult is the outcome of applying a candidate's diff to an isolated worktree and building it.
type BuildResult struct {
	Builds bool
	Log    string
}

// BuildChecker applies a candidate's source diff to an ISOLATED worktree/branch — never the user's
// working tree in place — and builds/compiles the target (§1b.1). It is the pre-surface build gate.
//
// It is an interface so the fast unit tests can drive the gate's rejection logic with a fake, while
// the live path (the demo, §5.5) wires either GoBuildChecker (a real `go build` on an isolated copy)
// or a worktree.Applier adapter (a real git branch + language verifier).
type BuildChecker interface {
	Check(ctx context.Context, patch *transform.Patch) (BuildResult, error)
}

// GoBuildChecker applies a patch to an isolated copy of a Go module and runs `go build ./...`. The
// copy is a throwaway temp directory — the user's Root is opened read-only and never mutated, which is
// the isolation §1b.1 requires. A non-building diff yields Builds=false with the compiler log.
type GoBuildChecker struct {
	// Root is the module the patch's files are relative to. It is copied, never written.
	Root string
}

// Check materializes an isolated copy of Root, overlays the patch's transformed files, and compiles
// it. The copy proves isolation: the codemod's output is built as the code that would ship, without
// touching the user's tree.
func (g GoBuildChecker) Check(ctx context.Context, patch *transform.Patch) (BuildResult, error) {
	if patch == nil {
		return BuildResult{}, fmt.Errorf("proposal: build check requires a patch")
	}
	work, err := os.MkdirTemp("", "proposal-build-*")
	if err != nil {
		return BuildResult{}, err
	}
	defer func() { _ = os.RemoveAll(work) }()

	if err := copyTree(g.Root, work); err != nil {
		return BuildResult{}, err
	}
	// Overlay the transformed file bytes (the codemod output) onto the isolated copy.
	for rel, b := range patch.Files {
		dst := filepath.Join(work, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return BuildResult{}, err
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			return BuildResult{}, err
		}
	}

	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return BuildResult{Builds: false, Log: string(out)}, nil // a build failure is data, not an error
	}
	return BuildResult{Builds: true, Log: string(out)}, nil
}

// copyTree copies a directory tree from src to dst (files only, preserving structure). It is a plain
// recursive copy so the build check operates on an independent tree.
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
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
}
