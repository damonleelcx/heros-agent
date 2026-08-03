package runlink

// workflowir.go is the OPT-IN second payload: a workflow's discovered structure, transmitted only when
// a developer asks for it by name.
//
// # Why this is separate from the linked run, and not a richer version of it
//
// `heros link` is the default path and its payload is deliberately thin — node ids, model refs, metrics,
// scores. That thinness is the product's promise, and the promise is worth more than any console feature,
// so this does not widen it. A run link transmits exactly what it always did, byte for byte.
//
// What this adds is a SECOND, separately-requested transmission carrying the shape a graph is drawn from:
// which symbol a call sits in, which file and lines, which model, which context policy, how many tools,
// and the edges between nodes. Without it the hosted graph is nineteen unlabelled dots, because the
// platform is never told anything else. With it the console can show the workflow — and the developer
// chose to send it, in a command they typed, with a flag whose name says what it does.
//
// # What is still refused, and why the refusal is structural
//
// Prompt text is not on this allowlist and there is no field it could occupy: `BuildWorkflowIR` reads
// named fields into a fresh struct, so a prompt cannot arrive by being forgotten. The same is true of
// I/O-contract schemas (they carry literals lifted out of source), in-scope symbol sets (a lexical dump
// rather than a call-site fact), prompt variable NAMES, and tool NAMES — the graph needs a tool COUNT,
// so a count is what crosses. Every one of those was available at the projection and left behind.
//
// The rule this package opened with holds here: a field added to `discovery.IRNode` tomorrow is ABSENT
// from this payload until somebody adds it below, on purpose, with a justification a reviewer reads.

// WorkflowIRPath is the authenticated ingest path an opt-in workflow structure is POSTed to, under
// PlatformBaseURL. Pinned in this package for the same reason LinkPath is: the destination of anything
// leaving a customer's machine is a constant a reviewer can find, never a value a flag can move.
const WorkflowIRPath = "/api/v1/workflows/"

// WorkflowIRContractVersion versions this payload independently of the run-link contract. They move for
// different reasons and a deployment may accept one and refuse the other.
const WorkflowIRContractVersion = "p11.workflow-ir.v1"

// WorkflowIRAllowlist is the ratified field set for the opt-in structure payload — the same
// security-review artifact `Allowlist` is, for the same reason, kept separate so a reviewer can see at a
// glance what the OPT-IN adds over the default.
//
// 🔴 Never permitted here, and deliberately not expressible: prompt text (`IRPrompt.Inline`), prompt
// variable names, I/O-contract schemas, in-scope symbol sets, tool and skill names, environment values,
// file CONTENTS. A path and a line range say where a call is; they do not carry what it says.
var WorkflowIRAllowlist = []AllowlistField{
	{"workflow_id", "identity", "Which workflow this structure describes — the same id the run link carries."},
	{"source_revision", "identity", "The commit the structure was discovered at — a revision id, never the code at it."},
	{"ir_version", "identity", "The IR schema version, so the platform reads the shape it was given, not the shape it assumes."},
	{"nodes.node_id", "shape", "Node identity — the join key between this structure and the run's per-node metrics."},
	{"nodes.symbol", "shape", "The enclosing function name. A LABEL on the graph: without it every node is an opaque hash, which is the whole reason this payload exists."},
	{"nodes.file", "shape", "The file path the call sits in — a location, not the file. Nothing reads the file."},
	{"nodes.line_start", "shape", "First line of the call span — lets the console point a reviewer at the call."},
	{"nodes.line_end", "shape", "Last line of the call span."},
	{"nodes.provider", "shape", "The provider a node calls (e.g. openai) — which vendor, not what was asked."},
	{"nodes.model_id", "shape", "The model a node calls, or `unresolved` when a syntactic frontend could not follow it."},
	{"nodes.context_policy", "shape", "The context-assembly policy NAME (e.g. inline_messages) — a strategy label, never assembled context."},
	{"nodes.tool_count", "shape", "How many tools this call site offers. A COUNT: the names are not sent, and the graph only ever renders a number."},
	{"edges.from", "shape", "Edge source node id — the workflow's shape."},
	{"edges.to", "shape", "Edge target node id."},
	{"edges.kind", "shape", "Edge kind (e.g. sequential) — how two nodes relate, not what flows between them."},
}

// WorkflowIRPayload is the exact bytes on the wire. Built field by field by BuildWorkflowIR.
type WorkflowIRPayload struct {
	ContractVersion string       `json:"contract_version"`
	WorkflowID      string       `json:"workflow_id"`
	SourceRevision  string       `json:"source_revision"`
	IRVersion       string       `json:"ir_version"`
	Nodes           []WireIRNode `json:"nodes"`
	Edges           []WireIREdge `json:"edges"`
}

// WireIRNode is one call site's shape. Every field here is on WorkflowIRAllowlist.
type WireIRNode struct {
	NodeID string `json:"node_id"`
	Symbol string `json:"symbol"`
	File   string `json:"file"`
	// LineStart/LineEnd bound the call. They are a span, not an extract: the platform never receives
	// the lines they name.
	LineStart     int    `json:"line_start"`
	LineEnd       int    `json:"line_end"`
	Provider      string `json:"provider"`
	ModelID       string `json:"model_id"`
	ContextPolicy string `json:"context_policy"`
	// ToolCount is a number, and that is the point. The tool NAMES are call-site identifiers from the
	// customer's source; the graph renders "3 tools", so three is what crosses the boundary.
	ToolCount int `json:"tool_count"`
}

// WireIREdge is one edge.
type WireIREdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// WorkflowIRAllowlistKeys returns the permitted wire keys for the opt-in payload.
func WorkflowIRAllowlistKeys() []string {
	out := make([]string, 0, len(WorkflowIRAllowlist))
	for _, f := range WorkflowIRAllowlist {
		out = append(out, f.Name)
	}
	return out
}

// WorkflowIRPermitted reports whether a dotted wire key is on the opt-in allowlist.
func WorkflowIRPermitted(key string) bool {
	for _, f := range WorkflowIRAllowlist {
		if f.Name == key {
			return true
		}
	}
	return false
}
