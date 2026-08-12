package discovery

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ReportVersion is the discovery-report schema version (Discovery-owned artifact, lighter governance
// than the frozen IR — doc 09 §3.4).
const ReportVersion = "1.0.0"

// DiscoveryReport is the run report emitted alongside the IR (§5.2, doc 09). It is the authoritative
// oracle for "why is this node missing?" (invariant I4): parse-skipped, undeclared, deduped, or degraded.
type DiscoveryReport struct {
	ReportVersion          string           `json:"report_version"`
	Workflow               IRWorkflow       `json:"workflow"`
	Summary                ReportSummary    `json:"summary"`
	DetectionsBySource     map[string]int   `json:"detections_by_source"`
	Nodes                  []ReportNode     `json:"nodes"`
	AmbiguityFlags         []AmbiguityFlag  `json:"ambiguity_flags"`
	DedupMerges            []MergeRecord    `json:"dedup_merges"`
	DeclarationDiagnostics []Diagnostic     `json:"declaration_diagnostics"`
	FileDiagnostics        []Diagnostic     `json:"file_diagnostics"`
	FrameworkSubgraphs     []FrameworkGraph `json:"framework_subgraphs"`
	// Frontends are the frontends that actually contributed nodes to this run, with the analysis kind
	// each declares. It answers "why does this graph have no edges?" with the only two facts that can
	// answer it — which frontend produced the graph, and whether that frontend can produce edges at all.
	//
	// 🔴 Only CONTRIBUTING frontends are listed. All seven run on every repository, and recording the
	// Rust frontend against a Python repository would make the explanation name a language that is not
	// in the source — a true sentence about the wrong thing, which reads as a false one.
	Frontends []FrontendRun `json:"frontends"`
}

type ReportSummary struct {
	FilesScanned      int `json:"files_scanned"`
	FilesSkipped      int `json:"files_skipped"`
	PackagesScanned   int `json:"packages_scanned"`
	CallSitesDetected int `json:"call_sites_detected"`
	NodesEmitted      int `json:"nodes_emitted"`
	EdgesEmitted      int `json:"edges_emitted"`
	DedupMerges       int `json:"dedup_merges"`
	AmbiguityFlags    int `json:"ambiguity_flags"`
}

// ReportNode is per-node provenance — the home for detected_by / ambiguity / framework metadata that
// the frozen IR node cannot hold (contract Finding A).
type ReportNode struct {
	NodeID            string            `json:"node_id"`
	DetectedBy        []DetectionSource `json:"detected_by"`
	Basis             []MatchBasis      `json:"basis,omitempty"`
	SignatureRow      string            `json:"signature_row,omitempty"`
	DeclaredSymbol    string            `json:"declared_symbol,omitempty"`
	UnresolvedFields  []string          `json:"unresolved_fields"`
	VariableAtRuntime bool              `json:"variable_at_runtime"`
}

// reportBuilder accumulates run state across the streamed packages, then assembles the report.
type reportBuilder struct {
	filesScanned    int
	filesSkipped    int
	packagesScanned int
	callSites       int
	fileDiags       []Diagnostic
	frameworks      []FrameworkGraph
	nodes           []ReportNode
	ambiguities     []AmbiguityFlag
	merges          []MergeRecord
	bySource        map[string]int
	frontends       []FrontendRun
}

func newReportBuilder() *reportBuilder {
	return &reportBuilder{bySource: map[string]int{"registry": 0, "declared": 0, "framework": 0}}
}

// addNode records one extracted node's provenance and ambiguity flags.
func (rb *reportBuilder) addNode(n ExtractedNode) {
	var unresolved []string
	if n.Model.Unresolved {
		unresolved = append(unresolved, "model")
	}
	if n.Prompt.Unresolved {
		unresolved = append(unresolved, "prompt")
	}
	rb.nodes = append(rb.nodes, ReportNode{
		NodeID:            n.NodeID,
		DetectedBy:        distinctSources(n.Site.Sources),
		Basis:             n.Site.Basis,
		SignatureRow:      n.Site.RegistryRow,
		DeclaredSymbol:    n.Site.DeclaredSym,
		UnresolvedFields:  unresolved,
		VariableAtRuntime: n.Invocation.VariableAtRuntime,
	})
	rb.ambiguities = append(rb.ambiguities, n.Ambiguities...)
	for _, s := range distinctSources(n.Site.Sources) {
		rb.bySource[string(s)]++
	}
}

// build assembles the final report. declDiags are the unmatched-declaration diagnostics (I4).
func (rb *reportBuilder) build(wf IRWorkflow, edgesEmitted int, declDiags []Diagnostic) DiscoveryReport {
	sort.Slice(rb.nodes, func(i, j int) bool { return rb.nodes[i].NodeID < rb.nodes[j].NodeID })
	sort.Slice(rb.ambiguities, func(i, j int) bool {
		if rb.ambiguities[i].NodeID != rb.ambiguities[j].NodeID {
			return rb.ambiguities[i].NodeID < rb.ambiguities[j].NodeID
		}
		return rb.ambiguities[i].Field < rb.ambiguities[j].Field
	})
	return DiscoveryReport{
		ReportVersion: ReportVersion,
		Workflow:      wf,
		Summary: ReportSummary{
			FilesScanned:      rb.filesScanned,
			FilesSkipped:      rb.filesSkipped,
			PackagesScanned:   rb.packagesScanned,
			CallSitesDetected: rb.callSites,
			NodesEmitted:      len(rb.nodes),
			EdgesEmitted:      edgesEmitted,
			DedupMerges:       len(rb.merges),
			AmbiguityFlags:    len(rb.ambiguities),
		},
		DetectionsBySource:     rb.bySource,
		Nodes:                  rb.nodes,
		AmbiguityFlags:         rb.ambiguities,
		DedupMerges:            nonNilMerges(rb.merges),
		DeclarationDiagnostics: nonNilDiags(declDiags),
		FileDiagnostics:        nonNilDiags(rb.fileDiags),
		FrameworkSubgraphs:     rb.frameworks,
		Frontends:              nonNilFrontends(rb.frontends),
	}
}

func nonNilDiags(d []Diagnostic) []Diagnostic {
	if d == nil {
		return []Diagnostic{}
	}
	return d
}

func nonNilFrontends(f []FrontendRun) []FrontendRun {
	if f == nil {
		return []FrontendRun{}
	}
	return f
}

func nonNilMerges(m []MergeRecord) []MergeRecord {
	if m == nil {
		return []MergeRecord{}
	}
	return m
}

// MarshalReport renders the report as deterministic, indented JSON.
func MarshalReport(r DiscoveryReport) ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal report: %w", err)
	}
	return append(b, '\n'), nil
}
