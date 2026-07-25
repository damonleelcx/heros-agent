package cli

import (
	"os"
	"path/filepath"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// discover.go is the `discover` command: the Workflow IR + discovery report from a repository, offline
// and unauthenticated (PRD FR1/FR2). It wraps internal/discovery unchanged — the CLI is a thin surface
// over the library so the CLI and the platform cannot produce different IRs from the same repo.

// DiscoverData is the machine payload emitted on stdout for `discover`.
type DiscoverData struct {
	WorkflowID     string `json:"workflow_id"`
	IRPath         string `json:"ir_path"`
	ReportPath     string `json:"report_path"`
	Nodes          int    `json:"nodes"`
	Edges          int    `json:"edges"`
	SourceRevision string `json:"source_revision"`
}

// Discover runs discovery and writes the IR and report. It contacts no network and requires no account.
func Discover(cfg Config, s Streams) error {
	repo := cfg.Get("repo")
	if repo == "" {
		repo = "."
	}
	outPath := cfg.Get("out")
	if outPath == "" {
		outPath = "ir.json"
	}
	reportPath := cfg.Get("report")
	if reportPath == "" {
		reportPath = "discovery-report.json"
	}

	url, sha := repoIdentity(repo, cfg.Get("repo-url"), cfg.Get("commit"), s)

	s.Narratef("discover: analyzing %s (offline, no account)…", repo)
	res, err := discovery.Run(discovery.Options{
		Repo:       repo,
		ConfigPath: cfg.Get("config"),
		WorkflowID: cfg.Get("workflow-id"),
		RepoURL:    url,
		CommitSHA:  sha,
	})
	if err != nil {
		return operational("discovery failed", err)
	}

	irBytes, err := discovery.MarshalIR(res.IR)
	if err != nil {
		return operational("marshal IR", err)
	}
	if err := os.WriteFile(outPath, irBytes, 0o644); err != nil {
		return operational("write IR", err)
	}
	repBytes, err := discovery.MarshalReport(res.Report)
	if err != nil {
		return operational("marshal report", err)
	}
	if err := os.WriteFile(reportPath, repBytes, 0o644); err != nil {
		return operational("write report", err)
	}

	data := DiscoverData{
		WorkflowID:     res.IR.Workflow.ID,
		IRPath:         outPath,
		ReportPath:     reportPath,
		Nodes:          len(res.IR.Nodes),
		Edges:          len(res.IR.Edges),
		SourceRevision: sha,
	}
	s.Narratef("discover: %d nodes, %d edges → %s", data.Nodes, data.Edges, outPath)
	return s.EmitJSON("discover", ExitOK, data, nil, nil)
}

// repoIdentity resolves the repo URL and commit for the IR, deriving from .git via the shared helper
// when not supplied. A missing commit is warned about and placeholdered exactly as cmd/discover does, so
// the two surfaces agree.
func repoIdentity(repo, urlFlag, commitFlag string, s Streams) (url, sha string) {
	url = urlFlag
	if url == "" {
		if u := gitRemoteURL(repo); u != "" {
			url = u
		} else {
			url = "local://" + filepath.Base(mustAbs(repo))
		}
	}
	sha = commitFlag
	if sha == "" {
		sha = gitCommit(repo)
	}
	if !hex7to64.MatchString(sha) {
		s.Narratef("discover: warning: no commit resolved from .git; emitting placeholder commit_sha (pass --commit for a real one)")
		sha = "0000000"
	}
	return url, sha
}
