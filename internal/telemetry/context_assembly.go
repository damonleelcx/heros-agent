package telemetry

import (
	"context"
	"time"

	"github.com/heros-foreal/agentd/internal/metricevent"
)

// This file emits context-assembly telemetry (P3 task 1.9). Each context policy, after it assembles a
// node's context on the trusted host, reports assembled tokens, source-message count, and — for lossy
// or retrieval policies — the drop/compaction ratio and retrieved-chunk count. Every event carries the
// full P0 tag set so P4 can slice a run by context policy. The policy layer (internal/registry) never
// imports telemetry; it returns the numbers and the host calls this emitter, keeping the dependency
// one-directional (the same shape as the gateway's Usage → observer path).

// P0Tags is the seven-tag set every telemetry event must carry. Held here as a value so a caller
// assembles it once and threads it into each emit, rather than passing seven strings around.
type P0Tags struct {
	VariantID  string
	RunID      string
	NodeID     string
	CaseID     string
	Seed       int64
	ConfigHash string
	// Timestamp is optional; when empty the emitter stamps RFC 3339 UTC now.
	Timestamp string
}

// ContextAssembly is the outcome a context policy reports for one node assembly.
type ContextAssembly struct {
	Policy          string
	AssembledTokens int
	SourceMessages  int
	// Lossy policies (summarization, semantic-compaction) set Lossy so a drop-ratio event is emitted;
	// a lossless policy's implicit 0.0 is not published as if it were measured.
	Lossy           bool
	DropRatio       float64
	RetrievedChunks int // > 0 only for retrieval policies
}

// EmitContextAssembly publishes the context-assembly metrics for one node. It is a no-op on a nil
// collector so a caller without telemetry wired does not have to branch. Each event routes through the
// collector's tag-gate → scrub → TSDB pipeline like every other metric.
func EmitContextAssembly(c *Collector, tags P0Tags, a ContextAssembly) {
	if c == nil {
		return
	}
	ts := tags.Timestamp
	if ts == "" {
		ts = time.Now().UTC().Format(time.RFC3339Nano)
	}
	dims := map[string]any{AttrContextPolicy: a.Policy}
	emit := func(name string, value float64, unit string) {
		seed := tags.Seed
		ev := metricevent.Event{
			SchemaVersion: metricevent.SchemaVersion,
			VariantID:     tags.VariantID,
			RunID:         tags.RunID,
			NodeID:        tags.NodeID,
			CaseID:        tags.CaseID,
			Seed:          &seed,
			Timestamp:     ts,
			ConfigHash:    tags.ConfigHash,
			MetricName:    name,
			Value:         &value,
			Unit:          unit,
			Dimensions:    dims,
		}
		c.EmitMetric(context.Background(), ev)
	}

	emit(MetricContextAssembledTokens, float64(a.AssembledTokens), UnitTokens)
	emit(MetricContextSourceMessages, float64(a.SourceMessages), UnitCount)
	// A lossy policy MUST report what it dropped so a "compaction dropped the answer" defect is
	// measurable in P4 (Decision 7). A lossless policy does not emit this axis at all.
	if a.Lossy {
		emit(MetricContextDropRatio, a.DropRatio, UnitRatio)
	}
	if a.RetrievedChunks > 0 {
		emit(MetricContextRetrievedChunks, float64(a.RetrievedChunks), UnitCount)
	}
}
