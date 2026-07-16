package discovery

import (
	"fmt"
	"os"
)

// Options configures a discovery run.
type Options struct {
	Repo       string             // path to the target repo (read-only)
	ConfigPath string             // path to llm-eval.yaml (optional — doc 05 §3.2)
	WorkflowID string             // workflow id for the IR (defaults to a frontend suggestion / module path)
	RepoURL    string             // repo url for workflow.repo.url
	CommitSHA  string             // commit the IR is extracted from
	Registry   *Registry          // signature registry (defaults to the embedded seed registry)
	Frameworks []FrameworkReader  // framework readers for the default Go frontend (test/override hook)
	Frontends  []LanguageFrontend // language frontends (defaults to the Go frontend); add a language here
}

// Result is a completed discovery run.
type Result struct {
	IR     IR
	Report DiscoveryReport
}

// Run executes the full P1 discovery pipeline on one repo. It drives one or more language frontends
// (Go by default) — each does the language-specific parse → detect → extract → graph → framework work —
// then assembles the language-neutral run report and emits the Workflow IR. Frontends stream units to
// bound memory (NFR3) and NEVER execute the target repo (I1).
func Run(opts Options) (Result, error) {
	reg := opts.Registry
	if reg == nil {
		var err error
		if reg, err = DefaultRegistry(); err != nil {
			return Result{}, fmt.Errorf("registry: %w", err)
		}
	}
	frontends := opts.Frontends
	if frontends == nil {
		// Default frontend set — one per shipped language. Adding a language is appending a frontend here.
		frontends = []LanguageFrontend{
			NewGoFrontend(opts.Frameworks...),
			NewPythonFrontend(),
			NewTypeScriptFrontend(),
			NewJavaScriptFrontend(),
			NewRustFrontend(),
			NewJavaFrontend(),
		}
	}

	// Load llm-eval.yaml if provided. A missing file is valid (optional); a malformed one fails loud.
	var declCfg *LLMEvalConfig
	if opts.ConfigPath != "" {
		data, err := os.ReadFile(opts.ConfigPath)
		switch {
		case err == nil:
			if declCfg, err = LoadLLMEval(data); err != nil {
				return Result{}, err // fail-loud on structural config faults (doc 08 F2)
			}
		case os.IsNotExist(err):
			// optional: absence is valid, surfaced via detections_by_source.declared == 0
		default:
			return Result{}, fmt.Errorf("read %s: %w", opts.ConfigPath, err)
		}
	}
	decl := newDeclaredIndex(declCfg)

	rb := newReportBuilder()
	var allNodes []ExtractedNode
	var allEdges []GraphEdge
	var wfID string
	contributed := map[string]bool{} // language -> emitted at least one node (10.3)

	for _, fe := range frontends {
		r, err := fe.Discover(opts.Repo, reg, decl)
		if err != nil {
			return Result{}, err
		}
		rb.filesScanned += r.FilesScanned
		rb.packagesScanned += r.PackagesScanned
		rb.callSites += r.CallSites
		rb.merges = append(rb.merges, r.Merges...)
		rb.fileDiags = append(rb.fileDiags, r.Diagnostics...)
		rb.frameworks = append(rb.frameworks, r.Frameworks...)
		for _, n := range r.Nodes {
			rb.addNode(n)
		}
		allNodes = append(allNodes, r.Nodes...)
		allEdges = append(allEdges, r.Edges...)
		if len(r.Nodes) > 0 {
			contributed[fe.Language()] = true
		}
		if wfID == "" {
			wfID = r.WorkflowID
		}
	}

	// 10.3: report any source language present that no frontend handles (I4 — explain a 0-node language).
	rb.fileDiags = append(rb.fileDiags, unsupportedLanguageDiagnostics(opts.Repo, frontends)...)

	for _, d := range rb.fileDiags {
		if d.Severity == SeverityError {
			rb.filesSkipped++
		}
	}

	wf := IRWorkflow{
		ID:       resolveWorkflowID(opts, wfID),
		Repo:     IRRepo{URL: opts.RepoURL, CommitSHA: opts.CommitSHA},
		Language: workflowLanguageLabel(frontends, contributed),
	}

	ir := BuildIR(wf, allNodes, allEdges)
	report := rb.build(wf, len(ir.Edges), decl.unmatchedDiagnostics())

	// Invariant I4 (structural): every IR node has a provenance row and vice-versa.
	if report.Summary.NodesEmitted != len(ir.Nodes) {
		return Result{}, fmt.Errorf("internal: report nodes (%d) != IR nodes (%d)", report.Summary.NodesEmitted, len(ir.Nodes))
	}
	return Result{IR: ir, Report: report}, nil
}

func resolveWorkflowID(opts Options, frontendSuggestion string) string {
	if opts.WorkflowID != "" {
		return opts.WorkflowID
	}
	if frontendSuggestion != "" {
		return frontendSuggestion
	}
	return "workflow"
}
