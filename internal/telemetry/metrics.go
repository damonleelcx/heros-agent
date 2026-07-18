package telemetry

import (
	"time"

	"github.com/heros-foreal/agentd/internal/metricevent"
)

// callDetail is everything the metric builder needs about one completed provider call, restated
// independent of the gateway so the taxonomy is testable without standing up an HTTP round trip. The
// instrument (instrument.go) bridges providergateway.CallInfo into this.
type callDetail struct {
	provider       string
	modelID        string
	modelVersionID string
	usage          tokenUsage
	duration       time.Duration
	// ttft is time-to-first-token when measured (streaming). Zero when not measured, in which case the
	// builder falls back to total duration: for a NON-streamed call the response is atomic, so first
	// token and last token genuinely arrive together and ttft == total latency is the true value, not a
	// placeholder.
	ttft        time.Duration
	attempts    int
	rateLimited bool
	isError     bool
	isTimeout   bool
	// idempotencyKey is the {run_id, node_id, attempt_group} identity. Stamped onto every per-call event
	// as the invocation_id dimension so the emission gate can measure a retried invocation exactly once
	// (Decision 8): the dedup key is the SAME string the gateway de-dupes the charge on.
	idempotencyKey string
}

// event stamps the seven tags onto one metric event from a RunContext. This is the single place tags
// are populated, which is what makes "tags complete by construction" true: a metric cannot be built
// through this helper without all seven present, and the boundary gate (§2) rejects it if some code
// path ever bypasses it.
func (rc RunContext) event(name string, value float64, unit string, ts time.Time, dims map[string]any) metricevent.Event {
	seed := rc.Seed
	v := value
	return metricevent.Event{
		SchemaVersion: metricevent.SchemaVersion,
		VariantID:     rc.VariantID,
		RunID:         rc.RunID,
		NodeID:        rc.NodeID,
		CaseID:        rc.CaseID,
		Seed:          &seed,
		Timestamp:     ts.UTC().Format(time.RFC3339Nano),
		ConfigHash:    rc.ConfigHash,
		MetricName:    name,
		Value:         &v,
		Unit:          unit,
		Dimensions:    dims,
	}
}

// MetricSet derives the full per-call operational metric taxonomy (tasks 1.2–1.5) from one call.
//
// It returns the events plus a list of GAPS — metric names it could not compute honestly (an unpriced
// model, an unknown context window). A gap is surfaced, never papered over with a fake 0: emitting
// cost=$0 for an unpriced model would understate spend exactly where a new model is in play. The
// instrument logs gaps; the fixture prices its model so the set is complete.
func MetricSet(rc RunContext, d callDetail, now time.Time, pb *PriceBook) (events []metricevent.Event, gaps []string) {
	add := func(name string, value float64, unit string, dims map[string]any) {
		// Every per-call event carries the invocation identity so the gate can dedup a retried
		// invocation. Merged in one place so no metric can forget it.
		if d.idempotencyKey != "" {
			if dims == nil {
				dims = map[string]any{}
			}
			dims[AttrInvocationID] = d.idempotencyKey
		}
		events = append(events, rc.event(name, value, unit, now, dims))
	}

	// ── Latency (1.2) ──
	add(MetricLatencyTotalMS, ms(d.duration), UnitMS, nil)
	ttft := d.ttft
	if ttft <= 0 {
		ttft = d.duration // atomic (non-streamed) response: first token == last token
	}
	add(MetricTTFTMS, ms(ttft), UnitMS, nil)
	tps := 0.0
	if secs := d.duration.Seconds(); secs > 0 {
		tps = float64(d.usage.output) / secs
	}
	add(MetricTokensPerSec, tps, UnitTokensPS, nil)

	// ── Cost (1.3) — attributable to the pinned price source ──
	cost, ctxWindow, priced := pb.costUSD(d.provider, d.modelID, d.usage)
	if priced {
		add(MetricCostUSD, cost, UnitUSD, map[string]any{
			AttrPriceBookVer:        pb.Version(),
			AttrGenAIModelVersionID: d.modelVersionID,
		})
	} else {
		gaps = append(gaps, MetricCostUSD)
	}

	// ── Tokens (1.4) ──
	add(MetricTokensPrompt, float64(d.usage.input), UnitTokens, nil)
	add(MetricTokensCompletion, float64(d.usage.output), UnitTokens, nil)
	add(MetricTokensThinking, float64(d.usage.thinking), UnitTokens, nil)
	add(MetricTokensCacheRead, float64(d.usage.cacheRead), UnitTokens, nil)
	add(MetricTokensCacheWrite, float64(d.usage.cacheWrite), UnitTokens, nil)
	if ctxWindow > 0 {
		total := d.usage.input + d.usage.output + d.usage.thinking + d.usage.cacheRead + d.usage.cacheWrite
		add(MetricContextWindowUtil, float64(total)/float64(ctxWindow), UnitRatio, nil)
	} else {
		gaps = append(gaps, MetricContextWindowUtil)
	}

	// ── Reliability (1.5) ──
	add(MetricError, b2f(d.isError), UnitCount, nil)
	add(MetricTimeout, b2f(d.isTimeout), UnitCount, nil)
	retries := d.attempts - 1
	if retries < 0 {
		retries = 0
	}
	add(MetricRetryCount, float64(retries), UnitCount, nil)
	add(MetricRateLimitHit, b2f(d.rateLimited), UnitCount, nil)

	return events, gaps
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
