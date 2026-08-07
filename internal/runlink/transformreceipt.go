package runlink

// transformreceipt.go is the THIRD opt-in payload: what a transform DID, transmitted only when a
// developer asks for it by name.
//
// # What it is for, in one sentence
//
// `/app/transforms/{config_hash}/{source_revision}` is a console surface that resolves for nothing,
// because the transform that would fill it runs on the customer's machine and the platform is never told
// it happened. This is the telling.
//
// # 🔴 What it deliberately cannot carry, and why the impossibility is structural
//
// THE DIFF. Not a hunk, not a line, not a path, not a file's before or after. The whole product's output
// under ADR-001 is a diff a human reviews, so "send the transform" reads naturally as "send the diff" —
// and it is precisely the thing that must not cross. What crosses instead is three integers where a diff
// would go: files changed, lines added, lines removed. There is no field a diff could occupy, so it
// cannot arrive by being forgotten, and a reviewer asking "could source get in here" finds three `int`s.
//
// The same is true of the per-node outcomes: a `cause` is one of three stable class identifiers, never
// the engine's `Detail`, which names arguments and symbols lifted out of the customer's source.
//
// # Why it is its own contract version
//
// It moves for different reasons than the run link and the structure. A deployment can accept a run and
// refuse a receipt, and the CLI must be told WHICH of the three a platform declined rather than
// discovering it as a generic rejection.

// TransformReceiptPath is the authenticated ingest path a receipt is POSTed to, under PlatformBaseURL.
// Flat, for the reason WorkflowIRPath is flat.
const TransformReceiptPath = "/api/v1/transform-receipts"

// TransformReceiptContractVersion versions this payload independently of the other three.
const TransformReceiptContractVersion = "p29.transform-receipt.v1"

// Node outcomes. A closed set of two, mirroring the verdict statuses: an outcome the engine did not
// produce is ABSENT, never a third value meaning "we are not sure".
const (
	// OutcomeApplied — the engine wrote an edit for this node.
	OutcomeApplied = "applied"
	// OutcomeRefused — the engine refused this node, and Cause names which class.
	OutcomeRefused = "refused"
)

// OutcomeStatuses returns the closed set.
func OutcomeStatuses() []string { return []string{OutcomeApplied, OutcomeRefused} }

// TransformReceipt is the exact bytes on the wire. Built field by field by BuildTransformReceipt.
type TransformReceipt struct {
	ContractVersion string `json:"contract_version"`
	ConfigHash      string `json:"config_hash"`
	SourceRevision  string `json:"source_revision"`
	WorkflowID      string `json:"workflow_id"`
	ToolVersion     string `json:"tool_version"`
	CoverageVersion string `json:"coverage_version,omitempty"`
	// Status is the transform's own terminal state, verbatim from the engine. A closed value, not a
	// sentence: the console renders its own copy.
	Status string `json:"status"`
	// NodeOutcomes is per node. Absent for a node the engine said nothing about — the same discipline
	// the structure payload's verdicts follow, and for the same reason.
	NodeOutcomes []WireNodeOutcome `json:"node_outcomes,omitempty"`
	// FilesChanged, LinesAdded and LinesRemoved are STATISTICS. This is the entire diff that crosses.
	FilesChanged int `json:"files_changed"`
	LinesAdded   int `json:"lines_added"`
	LinesRemoved int `json:"lines_removed"`
}

// WireNodeOutcome is one node's outcome under one transform.
type WireNodeOutcome struct {
	NodeID  string `json:"node_id"`
	Outcome string `json:"outcome"`
	Cause   string `json:"cause,omitempty"`
}

// TransformReceiptAllowlistKeys returns the permitted wire keys for the receipt.
func TransformReceiptAllowlistKeys() []string {
	out := make([]string, 0, len(TransformReceiptAllowlist))
	for _, f := range TransformReceiptAllowlist {
		out = append(out, f.Name)
	}
	return out
}

// TransformReceiptPermitted reports whether a dotted wire key is on the receipt's allowlist.
func TransformReceiptPermitted(key string) bool {
	for _, f := range TransformReceiptAllowlist {
		if f.Name == key {
			return true
		}
	}
	return false
}
