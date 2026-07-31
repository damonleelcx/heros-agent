package telemetry

// catalog.go pairs every operational metric with its UNIT and the SITE THAT COMPUTES IT.
//
// # Why the computation site is a field rather than a comment
//
// P23 task 4.9: a documented metric must match the harness on name, unit **and computation**, and must
// **cite where it is computed**. The first two are checkable against a constant; the third is the one
// that makes a metric definition worth reading.
//
// The failure this prevents is specific. "Latency" is not a definition — total wall time, time to first
// token, and time excluding retries are three different numbers, and a customer comparing our figure to
// theirs needs to know which. A documentation page that says "latency in milliseconds" and a metric that
// measures something else agree on every checkable field and disagree about the only thing that matters.
// Naming the function that computes it makes the disagreement findable.
//
// `MetricCatalog` is the artifact `cmd/docsfacts` exports and `scan-metric.mjs` checks documentation
// against. `TestCatalogCoversEveryEmittedMetric` asserts it covers exactly the emitted taxonomy, so a
// new metric with no catalogue entry is a red build rather than an undocumented number.

// MetricDefinition is one metric, as a reader needs it.
type MetricDefinition struct {
	Name string `json:"name"`
	Unit string `json:"unit"`
	// Scope: "call" (per provider call), "run" (per run window) or "billing" (per customer period).
	Scope string `json:"scope"`
	// Computation is the definition in one sentence — what is measured, and what is excluded.
	Computation string `json:"computation"`
	// ComputedIn cites the site. `file.go:function`, so a reader can open it.
	ComputedIn string `json:"computed_in"`
}

// MetricCatalog is the documented metric surface.
func MetricCatalog() []MetricDefinition {
	return []MetricDefinition{
		{
			Name: MetricLatencyTotalMS, Unit: UnitMS, Scope: "call",
			Computation: "Wall-clock duration of the whole provider call, including every retry attempt. It is not the time of the successful attempt alone.",
			ComputedIn:  "internal/telemetry/metrics.go:derive",
		},
		{
			Name: MetricTTFTMS, Unit: UnitMS, Scope: "call",
			Computation: "Time from the request being sent to the first streamed token arriving. Emitted only for a streaming call; a non-streaming call has no first token to time.",
			ComputedIn:  "internal/telemetry/metrics.go:derive",
		},
		{
			Name: MetricTokensPerSec, Unit: UnitTokensPS, Scope: "call",
			Computation: "Completion tokens divided by the generation duration. It measures output throughput, not the total call.",
			ComputedIn:  "internal/telemetry/metrics.go:derive",
		},
		{
			Name: MetricCostUSD, Unit: UnitUSD, Scope: "call",
			Computation: "Token counts priced by the provider's published rate for the model actually used. A model with no pricing entry emits no cost rather than a zero — a zero cost is a claim, and an absent one is a gap.",
			ComputedIn:  "internal/telemetry/pricing.go, applied in internal/telemetry/metrics.go:derive",
		},
		{Name: MetricTokensPrompt, Unit: UnitTokens, Scope: "call", Computation: "Input tokens the provider reported for this call.", ComputedIn: "internal/telemetry/metrics.go:derive"},
		{Name: MetricTokensCompletion, Unit: UnitTokens, Scope: "call", Computation: "Output tokens the provider reported for this call.", ComputedIn: "internal/telemetry/metrics.go:derive"},
		{Name: MetricTokensThinking, Unit: UnitTokens, Scope: "call", Computation: "Reasoning tokens the provider reported, where the provider reports them separately from completion.", ComputedIn: "internal/telemetry/metrics.go:derive"},
		{Name: MetricTokensCacheRead, Unit: UnitTokens, Scope: "call", Computation: "Prompt tokens served from the provider's cache.", ComputedIn: "internal/telemetry/metrics.go:derive"},
		{Name: MetricTokensCacheWrite, Unit: UnitTokens, Scope: "call", Computation: "Prompt tokens written into the provider's cache.", ComputedIn: "internal/telemetry/metrics.go:derive"},
		{
			Name: MetricContextWindowUtil, Unit: UnitRatio, Scope: "call",
			Computation: "Total tokens divided by the model's context window. Emitted only when the window is known; an unknown window emits nothing rather than assuming one.",
			ComputedIn:  "internal/telemetry/metrics.go:derive",
		},
		{Name: MetricError, Unit: UnitCount, Scope: "call", Computation: "1 when the call ended in an error, 0 otherwise. It counts terminal outcomes, not attempts.", ComputedIn: "internal/telemetry/metrics.go:derive"},
		{Name: MetricTimeout, Unit: UnitCount, Scope: "call", Computation: "1 when the terminal error was a timeout, 0 otherwise. A subset of reliability_error, not a separate failure.", ComputedIn: "internal/telemetry/metrics.go:derive"},
		{Name: MetricRetryCount, Unit: UnitCount, Scope: "call", Computation: "HTTP attempts beyond the first. A call that succeeded first time is 0, not 1.", ComputedIn: "internal/telemetry/metrics.go:derive"},
		{Name: MetricRateLimitHit, Unit: UnitCount, Scope: "call", Computation: "1 when the provider returned a rate-limit response at any point during the call, 0 otherwise.", ComputedIn: "internal/telemetry/metrics.go:derive"},
		{Name: MetricCallsInFlight, Unit: UnitCalls, Scope: "run", Computation: "Concurrent provider calls observed during the run window. Run-scoped, so it carries the reserved node_id sentinel rather than a node's.", ComputedIn: "internal/telemetry/monitor.go"},
		{Name: MetricCallsPerSec, Unit: UnitCallsPS, Scope: "run", Computation: "Provider calls per second over the run window.", ComputedIn: "internal/telemetry/monitor.go"},
	}
}

// MetricByName looks a definition up. The boolean distinguishes "no such metric" from a zero value,
// because a caller that silently got an empty definition would document a metric with no unit.
func MetricByName(name string) (MetricDefinition, bool) {
	for _, d := range MetricCatalog() {
		if d.Name == name {
			return d, true
		}
	}
	return MetricDefinition{}, false
}
