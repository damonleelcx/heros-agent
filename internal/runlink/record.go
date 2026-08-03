package runlink

// record.go defines RunRecord — the local, on-disk representation of a completed eval run that `link`
// later transmits (task 1.4 / contracts doc Q2).
//
// 🔴 The decisive property: RunRecord holds ONLY allowlisted fields. The data at rest is exactly the
// data on the wire. A customer who opens `.heros/runs/<run_id>.json` sees the entire egress surface —
// there is no richer object behind it from which sensitive fields are later stripped. This is what
// makes "the file makes retroactive linking possible AND customers can audit what would be sent" both
// true at once. Prompts, source, diffs, env values and provider credentials are not fields here and
// never reach this struct.
//
// BuildPayload (payload.go) still CONSTRUCTS the wire shape field by field from this record rather than
// serializing it, so the FR11 guarantee is testable: a field added to RunRecord is absent from a
// transmitted payload until BuildPayload is taught to copy it AND the allowlist admits its key.

// RunRecord is one completed run, allowlist-shaped. It is written by `eval` and read by `link`.
type RunRecord struct {
	// Identity + provenance.
	RunID          string  `json:"run_id"`
	WorkflowID     string  `json:"workflow_id"`
	ConfigHash     string  `json:"config_hash"`
	SourceRevision string  `json:"source_revision"`
	Timestamp      string  `json:"timestamp"` // RFC3339
	Seeds          []int64 `json:"seeds"`
	ToolVersion    string  `json:"tool_version"`

	// Metrics — quantities only.
	Metrics Metrics `json:"metrics"`

	// IR structure — shape only.
	IR IRStructure `json:"ir_structure"`

	// Scores with intervals (from the P4 harness / evalstats).
	Scores []Score `json:"scores"`

	// RunsReported is the coverage denominator this session observed (task 1.7). A count, never a list.
	RunsReported int `json:"runs_reported"`

	// Eval is the summary that QUALIFIES the scores above: how much evidence there was, and what the
	// customer's own gate said about it. See the eval section of Allowlist for why each field is
	// permitted and what is still refused.
	//
	// It is a separate struct rather than four loose fields so that "the evidence behind the number"
	// is one reviewable thing — and so a reader of the on-disk record sees it grouped exactly as the
	// wire groups it.
	Eval EvalSummary `json:"eval"`

	// LocalNote is a LOCAL-ONLY annotation field. It is the FR11 canary (task 3.10): it lives on the
	// source struct, it is written to the on-disk record, and it is asserted ABSENT from every
	// transmitted payload. BuildPayload never copies it — because the payload is CONSTRUCTED from named
	// fields, not serialized from this struct. If a future edit ever makes the payload a serialization of
	// RunRecord (a denylist), this field leaks and egress_test.go fails loudly. That failure is the
	// guarantee being real rather than decoration.
	LocalNote string `json:"local_note,omitempty"`
}

// GateOutcome is a run's verdict against the customer's own configured gate.
//
// Three values, and the third is not a synonym for either other one: a deployment with no gate
// configured has not passed and has not failed, and collapsing that into "pass" would let an unmeasured
// run render on the board identically to one that cleared a threshold. That is the specific lie this
// type exists to make unrepresentable.
type GateOutcome string

const (
	// GateNotConfigured: no gate was set, so there is no verdict. NOT a pass.
	GateNotConfigured GateOutcome = "not-configured"
	GatePass          GateOutcome = "pass"
	GateFail          GateOutcome = "fail"
)

// EvalSummary is the evidence behind a run's scores.
type EvalSummary struct {
	// CaseCount is how many eval cases the scores are computed over. A count, never the cases.
	CaseCount int `json:"case_count"`
	// SeedCount is how many seeds ran. Derived from Seeds, which already crosses — carried here so the
	// board can render "n=5 seeds x 8 cases" without the reader having to count a list.
	SeedCount int `json:"seed_count"`
	// GateOutcome is the verdict the CLI already printed on the developer's own terminal.
	GateOutcome GateOutcome `json:"gate_outcome"`
	// GateFailures names the metrics that failed. Empty on a pass and on not-configured — and those two
	// are still told apart by GateOutcome, never by this being empty.
	GateFailures []string `json:"gate_failures,omitempty"`
	// SingleSeed marks a run below the multi-seed floor as provisional.
	SingleSeed bool `json:"single_seed"`
}

// Metrics are the run's cost/latency/token quantities: an aggregate plus an optional per-node breakdown
// keyed by node id. Counts and amounts only — never the tokens or the prompts they came from.
type Metrics struct {
	CostUSD   float64               `json:"cost_usd"`
	LatencyMS float64               `json:"latency_ms"`
	TokensIn  int64                 `json:"tokens_in"`
	TokensOut int64                 `json:"tokens_out"`
	PerNode   map[string]NodeMetric `json:"per_node,omitempty"`
}

// NodeMetric is one node's contribution to the run's metrics.
type NodeMetric struct {
	CostUSD   float64 `json:"cost_usd"`
	LatencyMS float64 `json:"latency_ms"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
}

// IRStructure is the workflow's shape: which nodes exist, how they connect, what model each used, and
// its pattern label. No source, no prompt bodies — a graph the console can draw.
type IRStructure struct {
	NodeIDs       []string          `json:"node_ids"`
	Edges         []Edge            `json:"edges"`
	ModelRefs     map[string]string `json:"model_refs,omitempty"`     // node_id -> "provider/model"
	PatternLabels map[string]string `json:"pattern_labels,omitempty"` // node_id -> pattern label
}

// Edge is one directed connection in the workflow graph.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind,omitempty"`
}

// Score is one metric's value with its confidence interval, exactly as evalstats computes it.
type Score struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
	CILow  float64 `json:"ci_low"`
	CIHigh float64 `json:"ci_high"`
}
