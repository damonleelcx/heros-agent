package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
	"github.com/heros-foreal/agentd/internal/worktree"
)

// apply.go is the `apply` command: a Variant Spec becomes a REVIEWABLE DIFF produced against an
// ISOLATED working copy (PRD FR1, task 2.2, contracts doc Q3). It NEVER modifies the caller's working
// tree in place — strict worktree isolation per ADR-001. Developers who want the change applied run
// `git apply` on the emitted diff themselves, keeping the destructive act in their hands.

// ApplyData is the machine payload for `apply`.
type ApplyData struct {
	ConfigHash     string   `json:"config_hash"`
	SourceRevision string   `json:"source_revision"`
	DiffPath       string   `json:"diff_path"`
	DiffHash       string   `json:"diff_hash"`
	Empty          bool     `json:"empty"`
	TouchedFiles   []string `json:"touched_files"`
	Isolation      string   `json:"isolation"` // worktree | temp-copy
}

// Apply resolves a Variant Spec against the discovered IR and emits the reviewable diff.
func Apply(cfg Config, s Streams) error {
	specPath, err := cfg.Require("spec")
	if err != nil {
		return err
	}
	repo, err := cfg.resolveRepo()
	if err != nil {
		return err
	}
	outPath := cfg.Get("out")
	if outPath == "" {
		outPath = "variant.diff"
	}

	spec, err := loadSpec(specPath)
	if err != nil {
		return err
	}

	_, sha := repoIdentity(repo, cfg.Get("repo-url"), cfg.Get("commit"), s)
	if spec.SourceRevision == "" {
		spec.SourceRevision = sha
	}

	s.Narratef("apply: discovering %s to resolve the spec against its IR…", repo)
	res, err := discovery.Run(discovery.Options{Repo: repo, ConfigPath: cfg.Get("config"), WorkflowID: cfg.Get("workflow-id"), CommitSHA: sha})
	if err != nil {
		return operational("discovery for apply failed", err)
	}
	ir := res.IR

	resolved, err := variantspec.Resolve(context.Background(), spec, &ir, noopRegistries{})
	if err != nil {
		// An unresolved ref offline is an invalid-config, not a crash: the CLI cannot reach a registry,
		// and the user must supply a spec whose refs are resolvable or run against the platform.
		return invalidConfig("apply: cannot resolve the spec offline: " + err.Error())
	}

	// Isolation: prefer a git worktree at the source revision (ADR-001); fall back to an isolated
	// temp copy for a non-git tree. Either way the caller's working tree is never touched.
	isolatedDir, isolation, cleanup, err := isolate(repo, spec.SourceRevision, s)
	if err != nil {
		return operational("apply: could not create an isolated working copy", err)
	}
	defer cleanup()

	patch, err := transform.Generate(resolved, isolatedDir)
	if err != nil {
		return operational("apply: transform failed", err)
	}

	if err := os.WriteFile(outPath, patch.Diff, 0o644); err != nil {
		return operational("apply: write diff", err)
	}

	touched := make([]string, 0, len(patch.Touched))
	seen := map[string]bool{}
	for _, t := range patch.Touched {
		if !seen[t.File] {
			seen[t.File] = true
			touched = append(touched, t.File)
		}
	}

	data := ApplyData{
		ConfigHash: patch.ConfigHash, SourceRevision: patch.SourceRevision, DiffPath: outPath,
		DiffHash: patch.DiffHash, Empty: patch.IsEmpty(), TouchedFiles: touched, Isolation: isolation,
	}
	if patch.IsEmpty() {
		s.Narratef("apply: spec is a baseline (no overrides) — empty diff; your working tree is untouched")
	} else {
		s.Narratef("apply: reviewable diff (%d file(s)) → %s; your working tree is untouched (apply it with `git apply %s`)", len(touched), outPath, outPath)
	}
	return s.EmitJSON("apply", ExitOK, data, nil, nil)
}

// loadSpec reads and validates a Variant Spec JSON file.
func loadSpec(path string) (*variantspec.VariantSpec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, invalidConfig("apply: no spec file at " + path)
		}
		return nil, operational("apply: read spec", err)
	}
	var spec variantspec.VariantSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		return nil, invalidConfig("apply: spec is not valid JSON: " + err.Error())
	}
	if err := spec.Validate(); err != nil {
		return nil, invalidConfig("apply: spec is invalid: " + err.Error())
	}
	return &spec, nil
}

// isolate returns an isolated working copy of repo at rev. It never mutates repo. The returned cleanup
// removes the isolated copy.
func isolate(repo, rev string, s Streams) (dir, kind string, cleanup func(), err error) {
	// Prefer a git worktree (ADR-001) when repo is a git repository with the revision available.
	if isGitRepo(repo) {
		if wtDir, wtClean, werr := acquireWorktree(repo, rev); werr == nil {
			return wtDir, "worktree", wtClean, nil
		} else {
			s.Narratef("apply: git worktree isolation unavailable (%v); using an isolated temp copy", werr)
		}
	}
	// Fallback: an isolated temp copy of the tree. Still never touches the caller's working tree.
	tmp, e := os.MkdirTemp("", "heros-apply-copy-")
	if e != nil {
		return "", "", func() {}, e
	}
	if e := copyTree(repo, tmp); e != nil {
		_ = os.RemoveAll(tmp)
		return "", "", func() {}, e
	}
	return tmp, "temp-copy", func() { _ = os.RemoveAll(tmp) }, nil
}

// acquireWorktree clones repo bare and checks rev out into an isolated worktree (ADR-001).
func acquireWorktree(repo, rev string) (dir string, cleanup func(), err error) {
	root, err := os.MkdirTemp("", "heros-apply-pool-")
	if err != nil {
		return "", nil, err
	}
	ctx := context.Background()
	pool, err := worktree.NewPool(ctx, repo, root)
	if err != nil {
		_ = os.RemoveAll(root)
		return "", nil, err
	}
	wt, err := pool.Acquire(ctx, rev, "heros-apply")
	if err != nil {
		_ = os.RemoveAll(root)
		return "", nil, err
	}
	return wt.Dir, func() { _ = os.RemoveAll(root) }, nil
}

func isGitRepo(repo string) bool {
	fi, err := os.Stat(filepath.Join(repo, ".git"))
	return err == nil && (fi.IsDir() || fi.Mode().IsRegular())
}

// copyTree copies repo into dst, skipping the .git directory and the run store. It reads the source
// read-only.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		top := firstPathSegment(rel)
		if top == ".git" || top == ".heros" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil // skip symlinks/devices — the analysis path reads regular files only
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}

func firstPathSegment(p string) string {
	for i := 0; i < len(p); i++ {
		if p[i] == filepath.Separator || p[i] == '/' {
			return p[:i]
		}
	}
	return p
}

// noopRegistries answers every ref lookup with ErrNotFound. It is correct for OFFLINE apply of a spec
// with no registry-backed overrides (baseline or reorder-only); a spec that overrides a model/prompt/
// skill fails loud with a named ref, which is the honest offline behavior (no registry to reach).
type noopRegistries struct{}

func (noopRegistries) ResolveModel(context.Context, string) (*registry.ModelEntry, error) {
	return nil, registry.ErrNotFound
}
func (noopRegistries) ResolvePrompt(context.Context, string) (*registry.PromptEntry, error) {
	return nil, registry.ErrNotFound
}
func (noopRegistries) ResolveSkill(context.Context, string) (*registry.SkillEntry, error) {
	return nil, registry.ErrNotFound
}
func (noopRegistries) ResolveContextPolicy(context.Context, string) (*registry.ContextEntry, error) {
	return nil, registry.ErrNotFound
}

// ResolveMemory completes variantspec.Registries (P17). It fails closed like its siblings: this
// harness pins no memory strategy, so a memory_ref here names nothing and must not resolve to something.
func (noopRegistries) ResolveMemory(context.Context, string) (*registry.MemoryEntry, error) {
	return nil, registry.ErrNotFound
}

func (noopRegistries) ResolveHarness(context.Context, string) (*registry.HarnessEntry, error) {
	return nil, registry.ErrNotFound
}
