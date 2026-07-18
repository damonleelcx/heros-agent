package telemetry

import "sort"

// monitor.go is the read model behind the live run-monitoring view (§8). It builds a per-run snapshot
// from the SPAN store — the store built for per-run drill-down (Decision 5) — joined with the run's
// status read from the RUN RECORD. It deliberately reads status from the record, never derives it from
// the node list: a run whose nodes all succeeded but which was halted is exactly the case a
// client-derived status gets wrong (the same rule the P2 inspect view follows).

// RunStatusSource reads a run's authoritative status from the run record (executor.Store in production,
// an in-memory record in the demo). Returning ok=false means "no such run", which the view renders as
// its empty state — distinct from a run that exists but has produced nothing yet.
type RunStatusSource interface {
	RunStatus(runID string) (status RunStatusInfo, ok bool)
}

// RunStatusInfo is the run record's own fields the monitor needs. Status is verbatim from the record.
type RunStatusInfo struct {
	Status       string
	ConfigHash   string
	HaltedNodeID string
	HaltedReason string
}

// RunMonitor is the live snapshot the view renders. JSON-tagged because it is serialized straight to
// the browser.
type RunMonitor struct {
	RunID      string            `json:"run_id"`
	ConfigHash string            `json:"config_hash"`
	Status     string            `json:"status"` // verbatim from the run record (running|succeeded|failed|halted|...)
	Terminal   bool              `json:"terminal"`
	Nodes      []RunMonitorNode  `json:"nodes"`
	Halted     *RunMonitorHalted `json:"halted,omitempty"`
}

// RunMonitorHalted names the node and reason a run was halted (the unhappy path the view renders).
type RunMonitorHalted struct {
	NodeID string `json:"node_id"`
	Reason string `json:"reason"`
}

// RunMonitorNode is one node's live metrics — latency, cost, tokens, and the reliability-derived state
// that makes a failed/timed-out node visually distinct from a slow-but-healthy one (task 8.2).
type RunMonitorNode struct {
	NodeID           string  `json:"node_id"`
	State            string  `json:"state"` // "ok" | "failed" | "timed_out" (driven by the reliability metric)
	LatencyMS        float64 `json:"latency_ms"`
	CostUSD          float64 `json:"cost_usd"`
	TokensPrompt     int     `json:"tokens_prompt"`
	TokensCompletion int     `json:"tokens_completion"`
}

// Node states, driven by the reliability signal on the span (task 8.2). Central, not string literals.
const (
	NodeStateOK       = "ok"
	NodeStateFailed   = "failed"
	NodeStateTimedOut = "timed_out"
)

// Monitor builds live snapshots from a run-status source and the span store.
type Monitor struct {
	status RunStatusSource
	spans  SpanStore
}

// NewMonitor builds the live-monitor read model.
func NewMonitor(status RunStatusSource, spans SpanStore) *Monitor {
	return &Monitor{status: status, spans: spans}
}

// Snapshot returns the current live view of a run, or ok=false if the run record does not exist (the
// view's empty state). A run that exists but has emitted no node spans yet returns an empty Nodes slice
// with Status="running" — the view's loading/streaming state, distinct from empty.
func (m *Monitor) Snapshot(runID string) (RunMonitor, bool) {
	info, ok := m.status.RunStatus(runID)
	if !ok {
		return RunMonitor{}, false
	}
	rm := RunMonitor{
		RunID:      runID,
		ConfigHash: info.ConfigHash,
		Status:     info.Status,
		Terminal:   isTerminal(info.Status),
		Nodes:      []RunMonitorNode{}, // never nil: the view distinguishes "no nodes yet" from a decode failure
	}
	if info.HaltedNodeID != "" {
		rm.Halted = &RunMonitorHalted{NodeID: info.HaltedNodeID, Reason: info.HaltedReason}
	}

	// Per-node metrics from the node spans of this run (the span store is the per-run drill-down store).
	for _, sp := range m.spans.Trace(runID) {
		if sp.Kind != SpanKindNode {
			continue
		}
		rm.Nodes = append(rm.Nodes, RunMonitorNode{
			NodeID:           attrStr(sp.Attributes, AttrNodeID),
			State:            nodeState(sp),
			LatencyMS:        attrFloat(sp.Attributes, AttrLatencyMS),
			CostUSD:          attrFloat(sp.Attributes, AttrCostUSD),
			TokensPrompt:     attrInt(sp.Attributes, AttrGenAIUsageInput),
			TokensCompletion: attrInt(sp.Attributes, AttrGenAIUsageOutput),
		})
	}
	// Stable order so the streaming view does not reshuffle rows as it polls.
	sort.Slice(rm.Nodes, func(i, j int) bool { return rm.Nodes[i].NodeID < rm.Nodes[j].NodeID })
	return rm, true
}

// nodeState maps a node span's reliability attributes to the view's visual state. Timed-out is distinct
// from failed (task 8.2): a timeout is an infrastructure signal an operator acts on differently than a
// provider rejection.
func nodeState(sp Span) string {
	if b, _ := sp.Attributes[AttrTimedOut].(bool); b {
		return NodeStateTimedOut
	}
	if b, _ := sp.Attributes[AttrNodeFailed].(bool); b {
		return NodeStateFailed
	}
	if sp.Status == SpanStatusError {
		return NodeStateFailed
	}
	return NodeStateOK
}

func isTerminal(status string) bool {
	switch status {
	case "succeeded", "failed", "halted", "build-rejected":
		return true
	default:
		return false
	}
}

func attrFloat(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return 0
	}
}

func attrInt(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}
