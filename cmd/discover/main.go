// Command discover runs the P1 Discovery Engine over a Go repo and emits the Workflow IR + run report.
//
//	discover --repo <path> --config llm-eval.yaml --out ir.json
//
// It reads the repo as untrusted text and NEVER executes it (invariant I1). Repo URL and commit are
// derived from .git files (plain reads, no `git` subprocess) unless overridden by flags.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
)

func main() {
	repo := flag.String("repo", ".", "path to the target Go repo (read-only)")
	config := flag.String("config", "", "path to llm-eval.yaml (optional; declares in-house wrappers)")
	out := flag.String("out", "ir.json", "output path for the Workflow IR")
	reportOut := flag.String("report", "discovery-report.json", "output path for the discovery run report")
	repoURL := flag.String("repo-url", "", "workflow.repo.url (default: derived from .git/config)")
	commit := flag.String("commit", "", "workflow.repo.commit_sha (default: derived from .git/HEAD)")
	workflowID := flag.String("workflow-id", "", "workflow.id (default: the module path)")
	flag.Parse()

	if err := run(*repo, *config, *out, *reportOut, *repoURL, *commit, *workflowID); err != nil {
		fmt.Fprintln(os.Stderr, "discover: "+err.Error())
		os.Exit(1)
	}
}

var hex7to64 = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

func run(repo, config, out, reportOut, repoURL, commit, workflowID string) error {
	url := repoURL
	if url == "" {
		if u := gitRemoteURL(repo); u != "" {
			url = u
		} else {
			url = "local://" + filepath.Base(mustAbs(repo))
		}
	}
	sha := commit
	if sha == "" {
		sha = gitCommit(repo)
	}
	if !hex7to64.MatchString(sha) {
		// The IR schema requires a 7–64 hex commit; use a clearly-fake placeholder when unknown, and say so.
		fmt.Fprintln(os.Stderr, "discover: warning: no commit resolved from .git; emitting placeholder commit_sha (pass --commit for a real one)")
		sha = "0000000"
	}

	res, err := discovery.Run(discovery.Options{
		Repo:       repo,
		ConfigPath: config,
		WorkflowID: workflowID,
		RepoURL:    url,
		CommitSHA:  sha,
	})
	if err != nil {
		return err
	}

	irBytes, err := discovery.MarshalIR(res.IR)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, irBytes, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	repBytes, err := discovery.MarshalReport(res.Report)
	if err != nil {
		return err
	}
	if err := os.WriteFile(reportOut, repBytes, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", reportOut, err)
	}

	s := res.Report.Summary
	d := res.Report.DetectionsBySource
	fmt.Printf("discovered %d node(s) across %d package(s) — registry:%d declared:%d framework:%d\n",
		s.NodesEmitted, s.PackagesScanned, d["registry"], d["declared"], d["framework"])
	fmt.Printf("edges:%d  ambiguity-flags:%d  files-skipped:%d\n", s.EdgesEmitted, s.AmbiguityFlags, s.FilesSkipped)
	if d["declared"] == 0 && config == "" {
		fmt.Println("note: no llm-eval.yaml given — in-house SDK wrappers may be undetected (see docs/discovery/05).")
	}
	fmt.Printf("wrote %s and %s\n", out, reportOut)
	return nil
}

func mustAbs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// gitCommit resolves HEAD to a commit sha by reading .git files only (no `git` subprocess — I1).
func gitCommit(repo string) string {
	head, err := os.ReadFile(filepath.Join(repo, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(head))
	if ref, ok := strings.CutPrefix(s, "ref: "); ok {
		if b, err := os.ReadFile(filepath.Join(repo, ".git", filepath.FromSlash(ref))); err == nil {
			return strings.TrimSpace(string(b))
		}
		// Fall back to packed-refs.
		if sha := packedRef(repo, ref); sha != "" {
			return sha
		}
		return ""
	}
	return s // detached HEAD: the sha itself
}

func packedRef(repo, ref string) string {
	b, err := os.ReadFile(filepath.Join(repo, ".git", "packed-refs"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == ref {
			return fields[0]
		}
	}
	return ""
}

// gitRemoteURL reads the origin remote url from .git/config (plain read — no `git` subprocess).
func gitRemoteURL(repo string) string {
	b, err := os.ReadFile(filepath.Join(repo, ".git", "config"))
	if err != nil {
		return ""
	}
	inOrigin := false
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			inOrigin = t == `[remote "origin"]`
			continue
		}
		if inOrigin {
			if u, ok := strings.CutPrefix(t, "url = "); ok {
				return strings.TrimSpace(u)
			}
			if u, ok := strings.CutPrefix(t, "url="); ok {
				return strings.TrimSpace(u)
			}
		}
	}
	return ""
}
