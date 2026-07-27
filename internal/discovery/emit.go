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

// IRVersionEdgeProvenance is the IR version that added the OPTIONAL edge provenance/confidence/signal
// fields (recovered-topology, P4.5 Decision 8). Additive-only: a 1.1.0 document without these fields
// still validates, and a consumer pinned below 1.2.0 parses them leniently. The emitter declares this
// version only when it actually writes a provenance-tagged edge.
const IRVersionEdgeProvenance = "1.2.0"

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
	// DeclaredEnv is the set of environment variables the discovered workflow declares (P10 task 3.4).
	// ADDITIVE and omitempty: an IR that predates it serialises byte-identically. An `env` binding is
	// validated against this set at spec-resolve; a variable not declared here is rejected (fail
	// closed), so an undeclared env read cannot reach a provider as an empty substitution.
	DeclaredEnv []string `json:"declared_env,omitempty"`
}

// DeclaresEnv reports whether name is a declared environment variable of this workflow.
func (ir IR) DeclaresEnv(name string) bool {
	for _, v := range ir.DeclaredEnv {
		if v == name {
			return true
		}
	}
	return false
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

	// ── P14 tools≠skills split (decisions.md D-14.1) ────────────────────────────────────────────────
	//
	// ToolsSkills above conflates two things with OPPOSITE apply mechanics: a *tool* is a provider-native
	// function the model may call, SELECTED (kept or pruned) from what the model is offered; a *skill* is
	// a registered platform capability with a sealed contract, BOUND by constructing a value. One flat
	// slice cannot express "prune this unused tool" without the same sentence also reading as "unbind
	// this platform skill".
	//
	// 🔴 The split is ADDITIVE and ToolsSkills is FROZEN. Both fields are `omitempty` and nil-when-empty
	// (the DeclaredEnv pattern, :39-43), so an IR that predates them serialises byte-identically, the P0
	// golden vectors keep reproducing, and every `config_hash`-keyed row stays addressable. ToolsSkills
	// is never repurposed: it remains the conflated view a pre-P14 consumer reads.
	//
	// The FRONTEND populates these at extraction, because it is the only component that can see which
	// discovered entry is which. A consumer never re-derives the split — a re-derivation would be a
	// second classifier, and two classifiers are two answers.
	Tools  []IRTool `json:"tools,omitempty"`
	Skills []string `json:"skills,omitempty"`
}

// IRTool is one provider-native tool the node offers the model, as the call site declares it.
//
// A tool is identified by the text the CALL SITE wrote, not by a registry version_id, and that is a
// decision rather than an omission: a tool is already identified by its call site, so sealing it into
// the registry would invent a second identity for something that already has one (decisions.md D-14.2).
// A tool selection names tools by these identifiers and is validated against this set, fail-closed.
type IRTool struct {
	Name string `json:"name"`
	// DeclaredAt locates the tool as a STATIC, deletable element of a written list.
	//
	// 🔴 nil is load-bearing and is not "unknown": it means the node's tool set is ASSEMBLED AT RUNTIME,
	// so there is no declaration a prune could delete. That is exactly the case FR14 / D-14.3 require the
	// transform to REFUSE rather than guess at, and recording it here is what lets the refusal name the
	// reason instead of reporting a tool it could not find.
	DeclaredAt *IRToolLocation `json:"declared_at,omitempty"`
}

// Locatable reports whether this tool is a static declaration a prune could delete. A tool that is not
// locatable drives the FR14 refusal.
func (t IRTool) Locatable() bool { return t.DeclaredAt != nil }

// IRToolLocation is where a statically-declared tool sits in its list. The file is the node's own
// call_site.file — repeating it here would be a second copy of one fact — so only the position within
// the file and within the list is recorded.
type IRToolLocation struct {
	Line int `json:"line"`
	// Index is the tool's position in the written list, so a deletion addresses the element rather than
	// searching for text that may appear more than once.
	Index int `json:"index"`
}

// DeclaresTool reports whether name is one of the tools the IR records for this node. This is the
// fail-closed check a tool selection is validated against — the same shape as IR.DeclaresEnv and
// IRCallSite.HasInScope, and for the same reason: a selection over a tool the node does not offer must
// be rejected, never applied to nothing.
func (n IRNode) DeclaresTool(name string) bool {
	for _, t := range n.Tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// ToolByName returns the recorded tool and whether it was found.
func (n IRNode) ToolByName(name string) (IRTool, bool) {
	for _, t := range n.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return IRTool{}, false
}

// ToolsRecorded reports whether this node carries a P14 tool split at all. nil (absent) means "this IR
// predates the split", which is NOT the same as "this node offers no tools" — a consumer that conflated
// them would treat every pre-P14 node as tool-free and silently accept a selection over nothing.
func (n IRNode) ToolsRecorded() bool { return n.Tools != nil || n.Skills != nil }

type IRCallSite struct {
	File      string `json:"file"`
	Symbol    string `json:"symbol"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	ASTPath   string `json:"ast_path,omitempty"`
	// InScope is the set of symbols the IR records as in scope at this call site (P10 task 3.7,
	// decisions.md D-1.2). ADDITIVE to the x-frozen: additive-only node object: omitempty, so an IR that
	// predates this field serialises byte-identically and unmarshals InScope to nil.
	//
	// It is deliberately CONSERVATIVE — only the symbols already reaching the call, not the full lexical
	// scope — so a binding naming an unrecorded expression is rejected (a false rejection), never a
	// non-recorded one accepted (a false acceptance that would ship a non-building codemod). An `expr`
	// binding is validated against this set at spec-resolve.
	//
	// nil (absent) vs a recorded set is meaningful: a nil InScope means "this IR does not record scope,"
	// and resolve then defers the exactly-once satisfaction check to the transform's own promptExprFor,
	// preserving pre-P10 behaviour exactly. A recorded set engages resolve-time enforcement. Real call
	// sites always have symbols in scope, so an empty recorded set does not arise in practice.
	InScope []string `json:"in_scope,omitempty"`
}

// InScopeRecorded reports whether this call site carries a recorded in-scope symbol set. Used by
// resolve to decide whether to enforce binding satisfaction at resolve time (recorded) or defer to the
// transform's own check (not recorded), which keeps pre-P10 IRs behaving exactly as before.
func (cs IRCallSite) InScopeRecorded() bool { return cs.InScope != nil }

// HasInScope reports whether name is a recorded in-scope symbol at this call site.
func (cs IRCallSite) HasInScope(name string) bool {
	for _, s := range cs.InScope {
		if s == name {
			return true
		}
	}
	return false
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
	// Provenance / Confidence / Signal are OPTIONAL (IR 1.2.0, recovered-topology / P4.5 Decision 8).
	// Absent Provenance means "unrecorded" — a consumer treats it as framework-strength. An inferred
	// edge is a hypothesis carrying its confidence; a consumer never renders it as framework-certain.
	Provenance string  `json:"provenance,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Signal     string  `json:"signal,omitempty"`
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
		// P14 split — passed through NIL-WHEN-EMPTY, deliberately unlike ToolsSkills above, which is
		// normalised to `[]`. ToolsSkills' emptiness is part of the frozen bytes; these fields' ABSENCE
		// is what has to stay byte-compatible, and absence is achieved by omission, not by an empty array.
		Tools:  n.Tools,
		Skills: n.Skills,
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
