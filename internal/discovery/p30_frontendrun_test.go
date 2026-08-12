package discovery

import (
	"path/filepath"
	"strings"
	"testing"
)

// P30 task 1.1 — the discovery report records WHICH frontend produced a graph and how deeply it
// analyses, because that is the only pair of facts that can explain an edgeless graph. Asserted against
// a real repository fixture rather than a hand-built report: the value of this record is that it comes
// out of a run, and a test that constructs it proves nothing about the run.

func runOver(t *testing.T, dir string) Result {
	t.Helper()
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	out, err := Run(Options{Repo: dir, Registry: reg, WorkflowID: "wf"})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReportRecordsTheSyntacticFrontendThatProducedAnEdgelessGraph(t *testing.T) {
	out := runOver(t, filepath.Join("testdata", "fixtures", "python"))

	if len(out.IR.Nodes) == 0 {
		t.Fatal("the python fixture produced no nodes, so this test proves nothing")
	}
	if len(out.IR.Edges) != 0 {
		t.Fatalf("the python fixture now emits %d edges — this test's premise is stale", len(out.IR.Edges))
	}

	var python *FrontendRun
	for i := range out.Report.Frontends {
		if out.Report.Frontends[i].Language == "python" {
			python = &out.Report.Frontends[i]
		}
	}
	if python == nil {
		t.Fatalf("the report does not record the frontend that produced the graph: %+v", out.Report.Frontends)
	}
	if python.AnalysisKind != AnalysisSyntactic {
		t.Errorf("analysis_kind = %q, want %q", python.AnalysisKind, AnalysisSyntactic)
	}
	if python.Nodes != len(out.IR.Nodes) {
		t.Errorf("the record says %d nodes, the IR has %d", python.Nodes, len(out.IR.Nodes))
	}

	// 🚫 Only contributing frontends. Recording the Rust frontend against a Python repository would
	// make the explanation name a language that is not in the source.
	for _, f := range out.Report.Frontends {
		if f.Nodes == 0 {
			t.Errorf("a frontend that contributed nothing is recorded as producing this graph: %+v", f)
		}
	}

	// The diagnostic half: the "why is this missing" oracle carries the same fact in words.
	var found bool
	for _, d := range out.Report.FileDiagnostics {
		if d.Code == CodeFrontendSyntactic {
			found = true
			if !strings.Contains(d.Message, "python") {
				t.Errorf("the diagnostic does not name the frontend: %s", d.Message)
			}
			if d.Severity != SeverityInfo {
				t.Errorf("severity = %q, want %q — nothing failed", d.Severity, SeverityInfo)
			}
		}
	}
	if !found {
		t.Error("no FRONTEND_SYNTACTIC diagnostic was recorded for an edgeless syntactic run")
	}
}

// The Go frontend declares typed, and a Go repository that DOES emit edges records no syntactic
// diagnostic — so the diagnostic marks a real limit rather than firing on every run.
func TestGoRunRecordsATypedFrontendAndNoSyntacticDiagnostic(t *testing.T) {
	out := runOver(t, filepath.Join("testdata", "samplerepo"))

	var goRun *FrontendRun
	for i := range out.Report.Frontends {
		if out.Report.Frontends[i].Language == "go" {
			goRun = &out.Report.Frontends[i]
		}
	}
	if goRun == nil {
		t.Fatalf("the go frontend is not recorded: %+v", out.Report.Frontends)
	}
	if goRun.AnalysisKind != AnalysisTyped {
		t.Errorf("analysis_kind = %q, want %q", goRun.AnalysisKind, AnalysisTyped)
	}
	for _, d := range out.Report.FileDiagnostics {
		if d.Code == CodeFrontendSyntactic && strings.Contains(d.Message, "go frontend") {
			t.Errorf("the go frontend is reported as syntactic: %s", d.Message)
		}
	}
}

// Every shipped frontend declares an analysis kind from the closed vocabulary. A frontend that answered
// with something else would make the graph view's explanation unresolvable, and the compiler cannot
// catch a string.
func TestEveryShippedFrontendDeclaresAKnownAnalysisKind(t *testing.T) {
	for _, fe := range DefaultFrontends() {
		switch fe.AnalysisKind() {
		case AnalysisTyped, AnalysisSyntactic:
		default:
			t.Errorf("the %s frontend declares analysis kind %q, which is not in the vocabulary",
				fe.Language(), fe.AnalysisKind())
		}
	}
}
