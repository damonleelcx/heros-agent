package telemetry

import (
	"context"
	"testing"
	"time"
)

type fakeStatus struct {
	info map[string]RunStatusInfo
}

func (f fakeStatus) RunStatus(runID string) (RunStatusInfo, bool) {
	i, ok := f.info[runID]
	return i, ok
}

// Task 8.1 / 8.2: the monitor streams per-node latency/cost/token metrics and marks a failed/timed-out
// node distinctly from a healthy one, reading run status from the record (not derived).
func TestSection8_MonitorSnapshotReadsMetricsAndStates(t *testing.T) {
	gw, inst, col, spans, _, _, entry := collectorRig(t)
	// Two real, healthy node spans from a run through the gateway.
	rc := runFixture(t, gw, inst, entry, []string{"n_a", "n_b"})
	col.Flush()
	_ = gw

	// Inject a failed node and a timed-out node into the same run (states the fixture cannot produce
	// without a failing provider) so all four visual states are covered.
	base := map[string]any{AttrConfigHash: rc.ConfigHash, AttrRunID: rc.RunID, AttrVariantID: rc.VariantID}
	failed := Span{SpanID: "sf", TraceID: TraceID(rc.RunID), Kind: SpanKindNode, Status: SpanStatusError,
		Attributes: mergeAttrs(base, map[string]any{AttrNodeID: "n_fail", AttrNodeFailed: true, AttrLatencyMS: 120.0, AttrCostUSD: 0.002})}
	timedOut := Span{SpanID: "st", TraceID: TraceID(rc.RunID), Kind: SpanKindNode, Status: SpanStatusError,
		Attributes: mergeAttrs(base, map[string]any{AttrNodeID: "n_timeout", AttrTimedOut: true, AttrNodeFailed: true, AttrLatencyMS: 60000.0})}
	spans.PutSpan(context.Background(), failed)
	spans.PutSpan(context.Background(), timedOut)

	status := fakeStatus{info: map[string]RunStatusInfo{
		rc.RunID: {Status: "running", ConfigHash: rc.ConfigHash},
	}}
	mon := NewMonitor(status, spans)

	snap, ok := mon.Snapshot(rc.RunID)
	if !ok {
		t.Fatal("snapshot for an existing run returned ok=false")
	}
	if snap.Status != "running" {
		t.Errorf("status = %q, want the record's verbatim 'running'", snap.Status)
	}
	if snap.Terminal {
		t.Error("a running run should not be terminal")
	}

	states := map[string]string{}
	for _, n := range snap.Nodes {
		states[n.NodeID] = n.State
	}
	if states["n_a"] != NodeStateOK || states["n_b"] != NodeStateOK {
		t.Errorf("healthy nodes not marked ok: %v", states)
	}
	if states["n_fail"] != NodeStateFailed {
		t.Errorf("failed node state = %q, want failed", states["n_fail"])
	}
	if states["n_timeout"] != NodeStateTimedOut {
		t.Errorf("timed-out node state = %q, want timed_out (distinct from failed)", states["n_timeout"])
	}
	// The healthy nodes carry real latency/cost/token metrics streamed from the span.
	for _, n := range snap.Nodes {
		if n.NodeID == "n_a" {
			if n.LatencyMS <= 0 || n.CostUSD <= 0 || n.TokensPrompt != 100 || n.TokensCompletion != 40 {
				t.Errorf("node n_a metrics not populated: %+v", n)
			}
		}
	}
}

// The empty state: a run the record does not know is ok=false (distinct from a known run with no nodes).
func TestSection8_MonitorEmptyStateForUnknownRun(t *testing.T) {
	mon := NewMonitor(fakeStatus{info: map[string]RunStatusInfo{}}, NewMemSpanStore(0))
	if _, ok := mon.Snapshot("nope"); ok {
		t.Error("snapshot for an unknown run should return ok=false (the empty state)")
	}
}

// Terminal status is read from the record verbatim, so a halted run is never shown as succeeded.
func TestSection8_MonitorReadsTerminalStatusFromRecord(t *testing.T) {
	spans := NewMemSpanStore(0)
	status := fakeStatus{info: map[string]RunStatusInfo{
		"r1": {Status: "halted", ConfigHash: testConfigHash, HaltedNodeID: "n_b", HaltedReason: "output violates io_contract"},
	}}
	snap, ok := NewMonitor(status, spans).Snapshot("r1")
	if !ok || snap.Status != "halted" || !snap.Terminal {
		t.Fatalf("halted run not read from record: %+v", snap)
	}
	if snap.Halted == nil || snap.Halted.NodeID != "n_b" {
		t.Errorf("halt attribution not surfaced: %+v", snap.Halted)
	}
}

func mergeAttrs(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

var _ = time.Now
