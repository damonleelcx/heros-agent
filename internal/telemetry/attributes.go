package telemetry

// This file is the ONE place metric names and attribute keys are spelled. Every emitter, store, and
// test reads them from here — a literal "gen_ai.usage.input_tokens" or "cost_usd" typed at a call site
// is exactly the drift that makes one producer's label silently miss another consumer's query (backend
// rule: no string literals for event/attribute names; central enum only).

// ─────────────────────────────────────────────────────────────────────────────
// OTel GenAI semantic-convention attribute keys.
//
// These are the real convention keys, verbatim, so a real OTLP exporter carries the same names a
// GenAI-aware backend already understands. The seven-tag keys ride alongside them as span attributes.
// ─────────────────────────────────────────────────────────────────────────────
const (
	AttrGenAISystem         = "gen_ai.system"         // provider: "openai" | "anthropic" | "bedrock"
	AttrGenAIOperationName  = "gen_ai.operation.name" // "chat"
	AttrGenAIRequestModel   = "gen_ai.request.model"  // the model_id asked for
	AttrGenAIResponseModel  = "gen_ai.response.model" // the model_id that answered
	AttrGenAIUsageInput     = "gen_ai.usage.input_tokens"
	AttrGenAIUsageOutput    = "gen_ai.usage.output_tokens"
	AttrGenAIFinishReasons  = "gen_ai.response.finish_reasons"
	AttrGenAIModelVersionID = "gen_ai.request.model_version_id" // registry content address of the model version
)

// GenAIOperationChat is the single operation P2.5 instruments (chat/completions). Named, not inlined,
// so a future operation (embeddings) is a new constant, not a scattered literal.
const GenAIOperationChat = "chat"

// ─────────────────────────────────────────────────────────────────────────────
// The seven-tag set as OTel attribute keys.
//
// metricevent.Tags is the schema-order source of truth for the tag NAMES; these are the keys under
// which the same tags appear as OTel span attributes (Requirement: "the seven tags SHALL be carried as
// OTel attributes"). They intentionally match the metric-event field names so a span attribute and a
// metric tag are joinable without translation.
// ─────────────────────────────────────────────────────────────────────────────
const (
	AttrVariantID  = "variant_id"
	AttrRunID      = "run_id"
	AttrNodeID     = "node_id"
	AttrCaseID     = "case_id"
	AttrSeed       = "seed"
	AttrTimestamp  = "timestamp"
	AttrConfigHash = "config_hash"
)

// High-cardinality attributes that live on spans / Postgres / exemplars but NEVER as TSDB series labels
// (Decision 4). Kept here so the cardinality filter (cardinality.go) and the span emitter agree on the
// exact key set.
const (
	AttrInvocationID = "invocation_id"
	AttrAttemptGroup = "attempt_group"
	AttrNodeKind     = "node_kind"
	AttrToolName     = "tool_name"
	AttrPriceBookVer = "pricebook_version" // which price source produced a cost figure (attributability)
	// Convenience attributes carried on the node span so the live run-monitor (§8) reads a run's per-node
	// latency/cost/tokens/reliability from ONE store (the span store — the per-run drill-down store,
	// Decision 5), rather than joining the TSDB by a run_id it deliberately does not label (Decision 4).
	AttrCostUSD    = "cost_usd"
	AttrLatencyMS  = "latency_ms"
	AttrTimedOut   = "timed_out"
	AttrNodeFailed = "failed"
)

// ─────────────────────────────────────────────────────────────────────────────
// The operational metric taxonomy — the ~15 metric_name values P2.5 emits (PRD §6; proposal "What
// Changes"). Quality/agent/safety metric names are P4/P5 and are NOT here.
// ─────────────────────────────────────────────────────────────────────────────
const (
	// Latency (task 1.2).
	MetricLatencyTotalMS = "latency_total_ms"
	MetricTTFTMS         = "latency_ttft_ms"
	MetricTokensPerSec   = "throughput_tokens_per_sec"

	// Cost (task 1.3) — one figure per call, attributable per node/run/cumulative by summing over tags.
	MetricCostUSD = "cost_usd"

	// Tokens (task 1.4).
	MetricTokensPrompt      = "tokens_prompt"
	MetricTokensCompletion  = "tokens_completion"
	MetricTokensThinking    = "tokens_thinking"
	MetricTokensCacheRead   = "tokens_cache_read"
	MetricTokensCacheWrite  = "tokens_cache_write"
	MetricContextWindowUtil = "context_window_utilization"

	// Reliability (task 1.5).
	MetricError        = "reliability_error"       // 1 if the call ended in error, else 0
	MetricTimeout      = "reliability_timeout"     // 1 if the terminal error was a timeout, else 0
	MetricRetryCount   = "reliability_retry_count" // HTTP attempts beyond the first
	MetricRateLimitHit = "reliability_rate_limit_hit"

	// Throughput / concurrency for the run (task 1.6).
	MetricCallsInFlight = "concurrency_calls_in_flight"
	MetricCallsPerSec   = "throughput_calls_per_sec"

	// Context-assembly (P3 task 1.9). One event per axis so P4 can slice a policy's assembled size,
	// how much of the source it kept, and — for lossy policies — what it dropped. Emitted host-side by
	// the context-policy layer, never from a sandbox.
	MetricContextAssembledTokens = "context_assembled_tokens"
	MetricContextSourceMessages  = "context_source_messages"
	MetricContextDropRatio       = "context_drop_ratio"       // lossy policies only (summarization/compaction)
	MetricContextRetrievedChunks = "context_retrieved_chunks" // rag-retrieval only

	// Sandbox (P3 §5). Denial-rate and lifecycle are counters; each carries the P0 tag set for audit.
	MetricSandboxDenial    = "sandbox_denial"    // 1 per denied action (egress/cred/fs/resource); dimension `denial_kind`
	MetricSandboxLifecycle = "sandbox_lifecycle" // 1 per isolate lifecycle transition; dimension `phase`
	MetricToolError        = "tool_error"        // 1 when a skill/tool call fails closed; dimension `reason`
)

// AttrContextPolicy tags a context-assembly event with the policy that produced it (high-cardinality
// dimension, span/exemplar only). AttrDenialKind / AttrPhase / AttrToolReason likewise ride as
// dimensions on the sandbox/tool events so a consumer can filter by what was denied without those
// values ever becoming TSDB series labels.
const (
	AttrContextPolicy = "context_policy"
	AttrDenialKind    = "denial_kind"
	AttrPhase         = "phase"
	AttrToolReason    = "reason"
	AttrSkillName     = "skill_name"
)

// Units (P0 payload `unit`). Central, for the same reason the names are.
const (
	UnitMS       = "ms"
	UnitUSD      = "usd"
	UnitTokens   = "tokens"
	UnitRatio    = "ratio"
	UnitCount    = "count"
	UnitTokensPS = "tokens/s"
	UnitCallsPS  = "calls/s"
	UnitCalls    = "calls"
)

// OperationalMetricNames is the full set an un-instrumented workflow MUST yield per provider call
// (per-call metrics only; the two run-scoped throughput/concurrency metrics are emitted per run, not
// per call, so they are listed separately). The zero-user-code fixture (tasks 1.7 / 9.1) asserts
// against exactly this set, so "the taxonomy" is a value, not a description that can drift from code.
var OperationalMetricNames = []string{
	MetricLatencyTotalMS,
	MetricTTFTMS,
	MetricTokensPerSec,
	MetricCostUSD,
	MetricTokensPrompt,
	MetricTokensCompletion,
	MetricTokensThinking,
	MetricTokensCacheRead,
	MetricTokensCacheWrite,
	MetricContextWindowUtil,
	MetricError,
	MetricTimeout,
	MetricRetryCount,
	MetricRateLimitHit,
}

// RunScopedMetricNames are emitted once per run window rather than per call (throughput/concurrency).
var RunScopedMetricNames = []string{
	MetricCallsInFlight,
	MetricCallsPerSec,
}

// NodeIDRun is the reserved node_id a run-scoped metric (throughput/concurrency) carries. The seven-tag
// contract requires node_id on EVERY event (minLength 1), but concurrency-for-the-run is not
// attributable to a single node. Rather than weaken the contract, a run-scoped metric tags node_id
// with this sentinel: it is non-empty (the gate passes), greppable, and lets a consumer filter
// run-scoped series in or out (`node_id = "__run__"`). Distinct from any real node_id, which references
// a static IR definition.
const NodeIDRun = "__run__"
