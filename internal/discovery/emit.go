package discovery

import (
	"encoding/json"
	"fmt"
	"sort"
)

// IRVersion is the frozen Workflow IR schema version this emitter targets (P0, schemas/workflow-ir.schema.json).
// Discovery emits an UNLABELLED IR, which is complete at 1.0.0.
const IRVersion = "1.0.0"

// IRVersionPatternLabels is the version a document declares once the P3.5 Pattern Classifier has
// populated pattern_labels. The P3.5 expand step added OPTIONAL properties to the record P0 reserved
// (source, subgraph_ref, detector_id, llm_run_ref, taxonomy_version, candidate), which by the
// schema's own rule bumps MINOR and leaves MAJOR untouched — so a labelled and an unlabelled IR
// validate at the same MAJOR, and a pre-P3.5 consumer pinned to MAJOR 1 still parses both.
//
// The bump is declared rather than skipped because a document that uses 1.1.0 fields while claiming
// 1.0.0 is lying about which contract it was written against.
const IRVersionPatternLabels = "1.1.0"

// IR is the emitted Workflow IR document (frozen P0 contract). Field names and shapes MUST match
// workflow-ir.schema.json exactly; the emitter populates only frozen fields (invariant I6, additive-only).
type IR struct {
	IRVersion string     `json:"ir_version"`
	Workflow  IRWorkflow `json:"workflow"`
	Nodes     []IRNode   `json:"nodes"`
	Edges     []IREdge   `json:"edges"`
	// Subgraphs are the P3.5 pattern-labelling units. Reserved in P0, unset by Discovery, populated
	// by the classifier. omitempty: an unlabelled IR must serialise exactly as it did before P3.5.
	Subgraphs []IRSubgraph `json:"subgraphs,omitempty"`
}

// IRSubgraph is a named region of the graph carrying pattern labels (P0-reserved, P3.5-populated).
type IRSubgraph struct {
	SubgraphID    string           `json:"subgraph_id"`
	NodeIDs       []string         `json:"node_ids"`
	PatternLabels []IRPatternLabel `json:"pattern_labels,omitempty"`
}

// IRPatternLabel is the WIRE SHAPE of one pattern label, and nothing more. The vocabulary it draws
// from, the validation rules, and the confidence calibration all live in internal/patternclassifier
// — this package owns the IR contract, that package owns the classification semantics, and the
// dependency runs one way (classifier → discovery) so the contract does not depend on its producer.
//
// A drift test in the classifier asserts these two representations serialise identically, so the
// split cannot silently become two different formats.
type IRPatternLabel struct {
	Pattern         string  `json:"pattern"`
	Confidence      float64 `json:"confidence"`
	Source          string  `json:"source,omitempty"`
	SubgraphRef     string  `json:"subgraph_ref,omitempty"`
	DetectorID      string  `json:"detector_id,omitempty"`
	LLMRunRef       string  `json:"llm_run_ref,omitempty"`
	TaxonomyVersion string  `json:"taxonomy_version,omitempty"`
	Candidate       bool    `json:"candidate,omitempty"`
}

type IRWorkflow struct {
	ID       string `json:"id"`
	Repo     IRRepo `json:"repo"`
	Language string `json:"language"`
}

type IRRepo struct {
	URL       string `json:"url"`
	CommitSHA string `json:"commit_sha"`
}

type IRNode struct {
	NodeID              string            `json:"node_id"`
	Kind                string            `json:"kind"` // always "static_definition"
	CallSite            IRCallSite        `json:"call_site"`
	Model               IRModel           `json:"model"`
	Prompt              IRPrompt          `json:"prompt"`
	ToolsSkills         []string          `json:"tools_skills"`
	ContextAssembly     IRContextAssembly `json:"context_assembly"`
	IOContract          IRIOContract      `json:"io_contract"`
	InvocationSemantics IRInvocationSem   `json:"invocation_semantics"`
	// PatternLabels are node-scoped labels (a capability like Tool Use co-existing on this node).
	// Reserved in P0, unset by Discovery, populated by the P3.5 classifier. omitempty: an unlabelled
	// node must serialise byte-identically to how it did before P3.5.
	PatternLabels []IRPatternLabel `json:"pattern_labels,omitempty"`
}

type IRCallSite struct {
	File      string `json:"file"`
	Symbol    string `json:"symbol"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	ASTPath   string `json:"ast_path,omitempty"`
}

type IRModel struct {
	Provider string         `json:"provider"`
	ModelID  string         `json:"model_id"`
	Params   map[string]any `json:"params"`
}

// IRPrompt emits exactly one of template_ref/inline (schema oneOf). P1 always emits inline (possibly the
// empty sentinel for an unresolved prompt — doc 08 §3 / Finding B), plus the declared variable slots.
type IRPrompt struct {
	Inline    string   `json:"inline"`
	Variables []string `json:"variables"`
}

type IRContextAssembly struct {
	Policy      string `json:"policy"`
	Description string `json:"description"`
}

type IRIOContract struct {
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema"`
}

type IRInvocationSem struct {
	Type              string `json:"type"`
	VariableAtRuntime bool   `json:"variable_at_runtime"`
}

type IREdge struct {
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	Kind       string `json:"kind"`
}

// BuildIR maps the extracted graph to the frozen IR. Nodes and edges are sorted for byte-stable,
// diffable output (I3); edges referencing a node not in the set are dropped so referential integrity
// holds (the CI semantic check beyond JSON Schema).
func BuildIR(wf IRWorkflow, nodes []ExtractedNode, edges []GraphEdge) IR {
	ir := IR{IRVersion: IRVersion, Workflow: wf, Nodes: make([]IRNode, 0, len(nodes)), Edges: make([]IREdge, 0, len(edges))}

	present := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		present[n.NodeID] = true
	}
	for _, n := range nodes {
		ir.Nodes = append(ir.Nodes, buildNode(n))
	}
	for _, e := range edges {
		if present[e.From] && present[e.To] {
			ir.Edges = append(ir.Edges, IREdge{FromNodeID: e.From, ToNodeID: e.To, Kind: e.Kind})
		}
	}

	sort.Slice(ir.Nodes, func(i, j int) bool { return ir.Nodes[i].NodeID < ir.Nodes[j].NodeID })
	sort.Slice(ir.Edges, func(i, j int) bool {
		if ir.Edges[i].FromNodeID != ir.Edges[j].FromNodeID {
			return ir.Edges[i].FromNodeID < ir.Edges[j].FromNodeID
		}
		if ir.Edges[i].ToNodeID != ir.Edges[j].ToNodeID {
			return ir.Edges[i].ToNodeID < ir.Edges[j].ToNodeID
		}
		return ir.Edges[i].Kind < ir.Edges[j].Kind
	})
	return ir
}

func buildNode(n ExtractedNode) IRNode {
	// Positions are language-neutral (set by the frontend); the emitter never touches a language AST.
	params := n.Model.Params
	if params == nil {
		params = map[string]any{}
	}
	tools := n.ToolsSkills
	if tools == nil {
		tools = []string{}
	}
	vars := n.Prompt.Variables
	if vars == nil {
		vars = []string{}
	}
	return IRNode{
		NodeID: n.NodeID,
		Kind:   "static_definition",
		CallSite: IRCallSite{
			File:      n.Site.FileRel,
			Symbol:    n.Site.Identity.EnclosingSymbolFQN,
			LineStart: n.Site.LineStart,
			LineEnd:   n.Site.LineEnd,
			ASTPath:   n.Site.Identity.EnclosingSymbolFQN + "/" + n.Site.Identity.Selector,
		},
		Model:           IRModel{Provider: n.Model.Provider, ModelID: n.Model.ModelID, Params: params},
		Prompt:          IRPrompt{Inline: n.Prompt.Inline, Variables: vars},
		ToolsSkills:     tools,
		ContextAssembly: IRContextAssembly{Policy: n.Context.Policy, Description: n.Context.Description},
		// Permissive typed I/O-contract stubs (P1 allowance — doc 01 §2.1). Refinable additively.
		IOContract: IRIOContract{
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
		},
		InvocationSemantics: IRInvocationSem{Type: n.Invocation.Type, VariableAtRuntime: n.Invocation.VariableAtRuntime},
	}
}

// MarshalIR renders the IR as deterministic, indented JSON (byte-stable across runs — I3).
func MarshalIR(ir IR) ([]byte, error) {
	b, err := json.MarshalIndent(ir, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal IR: %w", err)
	}
	return append(b, '\n'), nil
}
