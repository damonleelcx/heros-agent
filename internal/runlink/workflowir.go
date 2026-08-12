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
//
// 🔴 P29 · FLAT, and it was not. This was `"/api/v1/workflows/"`, with the workflow id and — for the
// source routes — the revision appended as path segments. A path with a caller-supplied segment cannot be
// matched by an `Exact` Kubernetes Ingress rule, and the only rule that WOULD match it is a `Prefix` rule
// under `/api/v1/workflows/`, which publishes `commit`, `orderings`, `validate`, `proposals/generate`,
// `open-pr`, `pattern-graph`, `eval-board` and `nodes` to the internet beside the wanted routes. So the
// paths were never published at all: three commands 404'd at the edge, behind a green build and a healthy
// deployment, and the fence that should have caught it skipped every path ending in `/`.
//
// The identifiers moved into the payload — where `WorkflowIRPayload.WorkflowID` and `SourceRevision`
// already were, so the URL segment was duplicating the body — and each of these is now one Exact rule.
const WorkflowIRPath = "/api/v1/workflow-ir"

// WorkflowSourcePath is where a source snapshot is PUT and DELETEd.
//
// Its body is an opaque gzip archive, so there is no field for an identifier to move into. It travels in
// the `X-Heros-Workflow-Id` and `X-Heros-Source-Revision` headers instead — not the query string, which
// `IsLinkTarget` refuses so that a destination stays fully readable from PlatformBaseURL.
const WorkflowSourcePath = "/api/v1/workflow-source"

// SourceDiscoveryPath asks the platform to run discovery over an already-pushed snapshot.
//
// 🔴 Its own constant rather than `WorkflowSourcePath + "/discover"`, and the difference is the whole
// point of this file: the ingress fence reads `c.base + <constant>` out of the transport's source, so a
// URL assembled from a LOCAL variable is invisible to it. `RunDiscovery` used to be written
// `base, _ := c.sourceURL(…); url := base + "/discover"` — a fifth machine-addressed path that no fence
// could see and no manifest published.
const SourceDiscoveryPath = "/api/v1/workflow-source-discovery"

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

	// ── P29 · what makes `coverage × your nodes` answerable ────────────────────────────────────────
	//
	// 🔴 Read the design's D2 before adding anything here. The platform is FORBIDDEN from deriving a
	// verdict, and these three fields are what makes that refusal affordable rather than a dead end.
	// Each is an identifier, a closed-set member or a count. None can hold free text originating in the
	// customer's repository, and `TestEveryOptInFieldIsAnIdentifierOrACount` asserts it field by field.
	{"coverage_version", "identity", "The coverage table version the verdicts below were computed against — a content hash of the table, so the console can label a projection STALE instead of silently mixing two builds' answers. A build fact, not a repository fact."},
	{"nodes.language", "shape", "The language of this node's call site, as the discovery FRONTEND that found it reports. A closed set (the registered frontends), absent when no frontend reported one — never derived from the file extension, because a guessed language would make a guessed verdict look computed."},
	{"nodes.axis_verdicts.axis", "shape", "Which optimization axis a verdict is about — a member of transform.CoverageAxes(), which is closed."},
	{"nodes.axis_verdicts.status", "shape", "`applies` or `refused`. Two values, and a third is not expressible: a verdict the CLI could not compute is ABSENT, which the platform renders `not-reported`. That asymmetry is the whole safety property — an uncomputable verdict must never arrive looking like a computed one."},
	{"nodes.axis_verdicts.cause", "shape", "The refusal's stable cause identifier — one of the three transform.CauseClass values, and nothing else. NEVER the refusal's sentence: the console renders its own copy from its own catalogue, so a CLI three versions old cannot put stale prose on a paid surface, and a `Detail` string (which names arguments, symbols and policies lifted out of source) has no field to occupy here."},

	// ── P30 · what a CUSTOMER-PLACED analysis submits (task 7.3) ───────────────────────────────────
	//
	// 🔴 These four fields are the whole of "no second transport". A tenant placed `customer` runs the
	// platform's own agent definition on its own machine under its own credential, and the ANSWER comes
	// back here — through the ingest that already carries structure, versioned by the same contract,
	// scoped by the same authenticated tenant. Inventing a `/api/v1/heros-results` beside it would have
	// been a second egress surface for the same bytes, with its own auth, its own allowlist and its own
	// chance of disagreeing with this one about what a workflow id means.
	//
	// Each is an identifier, a closed-set member or a number, exactly as every row above is.
	{"agent_config_hash", "identity", "Which agent definition produced the inferred facts below — the `config_hash` of a published version. A content hash of a configuration the PLATFORM published, so the platform can refuse a submission naming a version it has no row for rather than storing facts it can never re-derive. Absent when the submission carries no inferred facts at all."},
	{"edges.author", "shape", "WHO wrote this edge: `frontend` (a language frontend parsed it out of the source) or `heros` (the analysis agent inferred it). A closed set of two on this boundary. It is the field that keeps `measured` and `inferred` distinct on every surface downstream — without it a hypothesis and a parsed fact arrive indistinguishable, and the console would have to guess which it was drawing."},
	{"edges.confidence", "shape", "How confident the agent was in an inferred edge, 0–1. A NUMBER, and it is here because the platform applies its own confidence floor at ingest (task 7.4) — a floor cannot be applied to a value that did not cross. Absent on a frontend-authored edge, which is not a hypothesis and has no confidence to report."},
	{"abstentions.subject", "shape", "What the agent DECLINED to answer about: a node id, or a pair of them written `a→b`. The same identifiers `edges.from` and `edges.to` already carry, and nothing else — an abstention names a subject, never a sentence about it."},
	{"abstentions.cause", "shape", "Why it declined — one of the five closed AbstentionReason values. Named `cause` and not `reason` for the same purpose `nodes.axis_verdicts.cause` is: on this boundary a `cause` is a stable class identifier and a `reason` would be a sentence, and TestNoOptInFieldIsShapedLikeContent enforces the distinction by name. Not knowing is an OUTPUT (FR3.4), transmitted so a customer-placed tenant's stored inference has the same shape as a platform-placed one."},
	{"abstentions.confidence", "shape", "The value that fell below the floor, when there was one. A number. Absent when the agent offered no candidate at all, which is a different fact from one it offered at 0.0."},
}

// TransformReceiptAllowlist is the ratified field set for the THIRD opt-in payload (P29 §2.8) — what a
// transform DID, transmitted so `/app/transforms/{config_hash}/{source_revision}` resolves for a change
// the customer applied on their own machine.
//
// 🔴 Never permitted here, and deliberately not expressible: the DIFF, or any part of it. Not a hunk, not
// a line, not a file's before or after, not a path. `TransformReceipt` carries three integers where a
// diff would go — files changed, lines added, lines removed — and there is no field a diff could occupy,
// which is the same construction argument the run link rests on. A reviewer looking for "could a diff get
// in here by accident" finds three `int`s and stops.
//
// The per-node outcomes are the same closed set the structure payload's verdicts are, for the same
// reason: the console branches on data and renders its own sentence.
var TransformReceiptAllowlist = []AllowlistField{
	{"config_hash", "identity", "Which configuration produced this transform — one half of the key /app/transforms resolves by. A hash, not the configuration."},
	{"source_revision", "identity", "The commit the transform was generated against — the other half of the key. A revision id, never the code at it."},
	{"workflow_id", "identity", "Which workflow this transform belongs to — the join key to the structure the console draws it against."},
	{"tool_version", "identity", "The CLI build that generated the transform. A diffstat is only comparable to another from the same engine, and this is the only place that is recorded."},
	{"coverage_version", "identity", "The coverage table version the engine ran with, for the same staleness reason the structure payload carries one."},
	{"status", "shape", "The transform's own terminal state, verbatim from the engine — a closed set, not a sentence."},
	{"node_outcomes.node_id", "shape", "Which node an outcome is about — the join key to the reported structure."},
	{"node_outcomes.outcome", "shape", "`applied` or `refused`. Closed, and an outcome the engine did not produce is ABSENT rather than defaulted."},
	{"node_outcomes.cause", "shape", "The refusal's stable cause identifier, exactly as the structure payload's verdicts carry — never the sentence."},
	{"files_changed", "metrics", "How many files the diff touches. A COUNT: the paths are not sent, and the console renders a number."},
	{"lines_added", "metrics", "How many lines the diff adds. A count, never the lines."},
	{"lines_removed", "metrics", "How many lines the diff removes. A count, never the lines."},
}

// WorkflowIRPayload is the exact bytes on the wire. Built field by field by BuildWorkflowIR.
type WorkflowIRPayload struct {
	ContractVersion string `json:"contract_version"`
	WorkflowID      string `json:"workflow_id"`
	SourceRevision  string `json:"source_revision"`
	IRVersion       string `json:"ir_version"`
	// CoverageVersion is the coverage table the per-node verdicts below were computed against.
	//
	// `omitempty`, and that is the contract rather than tidiness: a CLI built before P29 sends no such
	// key, the platform stores its ABSENCE, and every projection cell for that structure reads
	// `not-reported`. Substituting the platform's own table version would be the exact fabrication D2
	// forbids — it would date somebody else's verdicts to a table they were never computed against.
	CoverageVersion string       `json:"coverage_version,omitempty"`
	Nodes           []WireIRNode `json:"nodes"`
	Edges           []WireIREdge `json:"edges"`

	// AgentConfigHash names the agent definition that produced the `heros`-authored edges below (P30
	// task 7.3).
	//
	// 🔴 `omitempty`, and the ABSENCE is load-bearing in two directions. A CLI that predates P30 sends
	// no such key and no inferred edges, and its payload is byte-identical to the one it always sent —
	// which matters more than usual here, because Q2 makes `disabled` the default placement, so on the
	// day this ships EVERY existing customer is still sending exactly the pre-P30 shape. And in the
	// other direction, a payload carrying a `heros`-authored edge and NO hash is refused: a fact the
	// agent wrote whose definition cannot be named is a fact nothing can ever re-derive or re-run.
	AgentConfigHash string `json:"agent_config_hash,omitempty"`

	// Abstentions are what the agent declined to answer about. Empty and absent for a submission with
	// no inferred facts.
	//
	// Transmitted so a customer-placed tenant's stored inference has the SAME SHAPE as a platform-placed
	// one (D6). An asymmetry here would be invisible and would read as an agent that abstains less on
	// customer hardware — a conclusion about the model drawn from a gap in a wire format.
	Abstentions []WireAbstention `json:"abstentions,omitempty"`
}

// WireAbstention is one subject the agent declined. FR3.4: not knowing is an OUTPUT.
type WireAbstention struct {
	// Subject is a node id, or an ordered pair written `a→b`. The identifiers already on this wire.
	Subject string `json:"subject"`
	// Cause is one of the five closed AbstentionReason values. 🚫 Never prose.
	//
	// `cause`, not `reason`, exactly as `WireAxisVerdict.Cause` is: on this boundary a cause is a stable
	// class identifier and a reason is a sentence. TestNoOptInFieldIsShapedLikeContent refuses the
	// second name outright, which is how it caught this field being called the wrong thing.
	Cause string `json:"cause"`
	// Confidence is the value that fell below the floor, when there was one. A POINTER: a decline with
	// no candidate at all is a different fact from one at 0.0, and a float cannot say which.
	Confidence *float64 `json:"confidence,omitempty"`
}

// AbstentionCauses is the closed set this boundary accepts, spelled here rather than imported.
//
// 🔴 `internal/runlink` must not import `internal/herosagent` — the egress package does not import the
// thing whose output it constrains, exactly as it does not import the transform engine whose cause
// classes it lists. TestAbstentionReasonsMatchTheAgent in internal/herosagent, which CAN see both,
// catches a drift between the two.
func AbstentionCauses() []string {
	return []string{
		"below_confidence_floor",
		"no_candidate_offered",
		"out_of_vocabulary",
		"unknown_node",
		"frontend_already_established",
	}
}

// Edge authors. A closed set of two ON THIS BOUNDARY — `detector` and `operator` exist in
// `discovery.FactAuthor` and neither can author an EDGE a CLI submits, so neither is accepted here.
// Widening a wire vocabulary to match an internal one "just in case" is how a boundary stops describing
// what actually crosses it.
const (
	// AuthorFrontend — a language frontend established this edge by parsing the source.
	AuthorFrontend = "frontend"
	// AuthorHEROS — the analysis agent inferred it. Carries a confidence, and requires an
	// `agent_config_hash` on the payload.
	AuthorHEROS = "heros"
)

// EdgeAuthors returns the closed set, so a consumer's switch can be proved exhaustive.
func EdgeAuthors() []string { return []string{AuthorFrontend, AuthorHEROS} }

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

	// Language is what the discovery FRONTEND that found this node reports — never the file's extension.
	//
	// Per node, not per workflow, because a polyglot repository is the normal case and a workflow-wide
	// label taken from the majority would attribute a Python node's answer to a Go one. Absent when no
	// frontend reported one, and `omitempty` makes that absence the wire's own state rather than an
	// empty string somebody has to remember means "unknown".
	Language string `json:"language,omitempty"`

	// AxisVerdicts is, per optimization axis, what the transform engine answered for THIS call site when
	// it was run against the real source on the customer's machine.
	//
	// 🔴 This is the field the whole projection rests on, and the reason the platform is forbidden from
	// computing one. The coverage table answers (axis, language, form). It does NOT answer a call site's
	// own shape — arguments unpacked from a mapping, a tool list assembled at run time, a registry row
	// with no locator — and that information lives only in the source, which does not cross this boundary
	// and must not. A platform that computed the (axis, language) cell would be right most of the time
	// and would claim `applies` for exactly the call sites that refuse for their own shape. A projection
	// that is right most of the time is worse than none, because it is the input to a customer's decision
	// about what to author.
	//
	// A node with no entry for an axis renders `not-reported`. That is a FOURTH state, and it is not a
	// polite way of saying "no": `not-applicable` is a claim about the customer's code and is the one
	// thing this data must never say by accident.
	AxisVerdicts []WireAxisVerdict `json:"axis_verdicts,omitempty"`
}

// WireAxisVerdict is one axis's answer for one node. Both fields are identifiers.
type WireAxisVerdict struct {
	// Axis is a member of transform.CoverageAxes().
	Axis string `json:"axis"`
	// Status is exactly `applies` or `refused`. There is no third value and there must not be: a verdict
	// the CLI could not compute is ABSENT from the list, not present carrying an "unknown". An absent
	// verdict and an uncertain one render differently, and only one of them is honest.
	Status string `json:"status"`
	// Cause is the stable refusal-class identifier, present only when Status is `refused`.
	//
	// 🚫 The engine's `Detail` sentence is NOT here and has no field to occupy. It names arguments,
	// symbols, policies and registry rows lifted out of the customer's source — it is exactly the kind of
	// prose this boundary exists to keep on their machine — and the console renders its own sentence from
	// its own catalogue anyway.
	Cause string `json:"cause,omitempty"`
}

// Verdict statuses. A closed set of two, checked at both ends.
const (
	// VerdictApplies — the engine produced an edit for this node on this axis, against the real source.
	VerdictApplies = "applies"
	// VerdictRefused — the engine refused, and Cause names which of the three classes.
	VerdictRefused = "refused"
)

// VerdictStatuses returns the closed set, so a consumer's switch can be proved exhaustive.
func VerdictStatuses() []string { return []string{VerdictApplies, VerdictRefused} }

// WireIREdge is one edge.
type WireIREdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`

	// Author is WHO wrote this edge — `frontend` or `heros` (P30 D4, task 7.3).
	//
	// 🔴 P29's projection deliberately did not send this, with the argument that "an inferred edge's
	// confidence is a fact about our analysis, not about the customer's workflow, and the graph draws
	// neither". The graph draws BOTH now, and that is what changed: task 8.3 renders inferred edges
	// visually distinct and task 8.4 reports the inferred portion of a mixed count, neither of which is
	// possible from an undifferentiated edge list. The P29 argument was right about the graph P29 drew.
	//
	// ABSENT is not `frontend`. It reads as `legacy` — "we were not told who wrote this" — for the same
	// reason `coverage_version` is never substituted with the platform's own: a pre-P30 CLI made no
	// authorship claim, and answering on its behalf would make its rows indistinguishable from stamped
	// ones, which is precisely the distinction the field exists to create.
	Author string `json:"author,omitempty"`

	// Confidence is how sure the agent was, 0–1. Present only on a `heros`-authored edge.
	//
	// The platform applies its OWN floor to this at ingest (task 7.4) rather than trusting that the
	// submitter applied one. A floor enforced only on the machine that produced the number is not a
	// floor; it is a convention, and the one participant it does not bind is the one that would benefit
	// from ignoring it.
	Confidence float64 `json:"confidence,omitempty"`
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
