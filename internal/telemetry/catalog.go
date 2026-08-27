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
	// Scope: "call" (per provider call), "node" (per node event, inside a run, not tied to a call),
	// "run" (per run window) or "billing" (per customer period).
	//
	// 🔴 `node` exists because a sandbox denial is not a provider call. It is emitted with a REAL node id
	// (unlike a run-scoped metric, which carries the `__run__` sentinel) and it can happen with no
	// provider call in flight at all — a filesystem denial, an egress denial, a lifecycle transition.
	// Labelling one `call` would put a false statement in the field whose only job is to say what the
	// number is per, which is the whole reason this field is not a comment.
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

		// ── Context assembly (P3 task 1.9) ───────────────────────────────────────────────────────
		//
		// One event per axis, so a policy's assembled size, how much of the source it kept and — for a
		// lossy policy — what it discarded are sliceable separately. Emitted host-side by the
		// context-policy layer, never from a sandbox.
		{
			Name: MetricContextAssembledTokens, Unit: UnitTokens, Scope: "call",
			Computation: "Tokens in the message list the policy actually assembled for this node invocation. It is what the model was given, not what was available to give.",
			ComputedIn:  "internal/telemetry/context_assembly.go:EmitContextAssembly",
		},
		{
			Name: MetricContextSourceMessages, Unit: UnitCount, Scope: "call",
			Computation: "How many messages the policy had to select from, before assembly. The denominator the drop ratio is taken against.",
			ComputedIn:  "internal/telemetry/context_assembly.go:EmitContextAssembly",
		},
		{
			Name: MetricContextDropRatio, Unit: UnitRatio, Scope: "call",
			// 🔴 The conditional emission is the definition, not an implementation detail. A zero from a
			// summariser means "this run happened to drop nothing"; a zero from a window would mean "this
			// policy never drops". Publishing both as 0 would make the two unreadable, so only the first
			// is published and a lossless policy emits nothing on this axis at all.
			Computation: "The fraction of the source context a LOSSY policy discarded. Emitted only for a lossy policy (summarization, semantic-compaction); a lossless policy emits nothing on this axis rather than a zero, because a zero from a summariser and a zero from a window mean opposite things.",
			ComputedIn:  "internal/telemetry/context_assembly.go:EmitContextAssembly",
		},
		{
			Name: MetricContextRetrievedChunks, Unit: UnitCount, Scope: "call",
			Computation: "Passages a retrieval policy added. Emitted only when the count is above zero, and recorded as retrieval rather than as loss — augmentation counted as loss would make the drop-tolerance gate reject retrieval for doing exactly what it is for.",
			ComputedIn:  "internal/telemetry/context_assembly.go:EmitContextAssembly",
		},

		// ── Sandbox and tools (P3 §5) ────────────────────────────────────────────────────────────
		//
		// Node-scoped rather than call-scoped: each carries a REAL node id and none of them needs a
		// provider call in flight. See the `Scope` field's own comment for why that is not "call".
		{
			Name: MetricSandboxDenial, Unit: UnitCount, Scope: "node",
			Computation: "1 per action the sandbox denied — egress, credential, filesystem, resource, or a brokered call that was refused. The class rides as the `denial_kind` dimension so a consumer can filter without the value becoming a series label. An ALLOWED brokered call is not counted: it is not an anomaly and would inflate the denial rate.",
			ComputedIn:  "internal/sandboxaudit/adapter.go",
		},
		{
			Name: MetricSandboxLifecycle, Unit: UnitCount, Scope: "node",
			Computation: "1 per isolate lifecycle transition, with the transition as the `phase` dimension.",
			ComputedIn:  "internal/sandboxaudit/adapter.go",
		},
		{
			Name: MetricToolError, Unit: UnitCount, Scope: "node",
			Computation: "1 when a skill or tool call fails closed, with the contract-error class as `reason` and the skill as `skill_name`. It counts failures that were REFUSED, not failures that were retried.",
			ComputedIn:  "internal/sandboxaudit/adapter.go:RecordSkillFailure",
		},

		// ── Revenue (P7) ─────────────────────────────────────────────────────────────────────────
		//
		// BILLING-scoped: a customer's spend for a period is not attributable to a run, a node or a case,
		// so these carry the reserved tag sentinels rather than weakening the seven-tag contract. The
		// customer and the period ride as DIMENSIONS, never as labels — a label per customer is the
		// cardinality explosion Decision 4 exists to prevent.
		{
			Name: MetricRevenueSUM, Unit: UnitUSD, Scope: "billing",
			Computation: "A customer's derived spend under management for a period.",
			ComputedIn:  "internal/metering/observe.go:SUMRecorded",
		},
		{
			Name: MetricRevenueMetered, Unit: UnitCount, Scope: "billing",
			Computation: "A metered quantity as handed to the billing provider, with the meter as the `meter_name` dimension. It is what we REPORTED, which is the half of a reconciliation that is ours.",
			ComputedIn:  "internal/metering/observe.go:UsageReported",
		},
		{
			Name: MetricRevenueInvoiceState, Unit: UnitCount, Scope: "billing",
			// 🔴 The value is 1 and the state is a dimension, deliberately. A state is not a quantity, and
			// encoding it as one (paid=1, failed=0) is how a dashboard ends up averaging statuses.
			Computation: "1 per observed invoice or subscription state transition, with the state as the `billing_state` dimension. The value is 1 rather than an encoded state, because a state is not a quantity and averaging one is meaningless.",
			ComputedIn:  "internal/metering/observe.go:InvoiceState",
		},
		{
			Name: MetricRevenueChargeFailed, Unit: UnitCount, Scope: "billing",
			Computation: "1 per charge that did not settle, with the kind as the `charge_kind` dimension. It also raises an alert: a failed charge is revenue that silently did not happen.",
			ComputedIn:  "internal/metering/observe.go:ChargeFailed",
		},
		{
			Name: MetricRevenueGainshareBilled, Unit: UnitUSD, Scope: "billing",
			Computation: "A gainshare charge's billed quantity, over VERIFIED savings only. An unverified change contributes nothing to it.",
			ComputedIn:  "internal/metering/observe.go:GainshareBilled",
		},
		{
			Name: MetricRevenueReconcileDrift, Unit: UnitCount, Scope: "billing",
			Computation: "1 per drift finding, with the kind as `drift_kind` and the meter as `meter_name`. It also raises an alert: drift is the meter and the provider disagreeing, which is how month-end becomes a reconstruction.",
			ComputedIn:  "internal/metering/observe.go:DriftDetected",
		},
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
