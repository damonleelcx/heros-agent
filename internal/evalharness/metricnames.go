package evalharness

// This file is the ONE place P4 quality/eval metric names are spelled — the continuation of
// telemetry/attributes.go, which deliberately stops at the operational taxonomy and says
// "Quality/agent/safety metric names are P4/P5 and are NOT here". A literal "task_success" typed at
// a call site is exactly the drift that makes one producer's label miss another consumer's query.

const (
	// MetricTaskSuccess is the headline end-to-end metric: did the run produce the right answer for
	// this case. Range [0,1]; several evaluators can produce it (exact-match, regex, schema, judge),
	// which is why evaluator_name is part of the eval_result natural key.
	MetricTaskSuccess = "task_success"

	// MetricExactMatch, MetricSchemaValid and MetricRegexMatch are the built-in oracles' own
	// metrics, recorded alongside task_success so a board can show WHICH oracle passed.
	MetricExactMatch  = "exact_match"
	MetricSchemaValid = "schema_valid"
	MetricRegexMatch  = "regex_match"

	// MetricJudgeScore is the rubric-driven LLM judge's score, always reported with its calibration
	// standing attached (see JudgeStanding).
	MetricJudgeScore = "judge_score"

	// The standard family the harness computes from every trace without any user configuration.
	MetricRunCostUSD    = "eval_cost_usd"
	MetricRunLatencyMS  = "eval_latency_ms"
	MetricRunTokens     = "eval_tokens_total"
	MetricToolErrorRate = "tool_error_rate"
	// MetricReliability is 1 for a run that completed without error/timeout/contract violation,
	// 0 otherwise. It is the composite score's reliability term.
	MetricReliability = "reliability"

	// Per-node contribution decomposition (task 1.6) — the substrate P4.5 attribution consumes.
	// Each is a SHARE in [0,1] of the run's end-to-end figure, so the per-node values of one run sum
	// to 1 (cost/latency) or to the count of failing nodes (success).
	MetricNodeCostShare      = "node_cost_share"
	MetricNodeLatencyShare   = "node_latency_share"
	MetricNodeSuccessImpact  = "node_success_impact"
	MetricNodeToolErrorCount = "node_tool_error_count"
)

// StandardFamily is the metric family the harness computes for every run from the traces alone, with
// no per-workflow configuration. Tests assert against this value, so "the standard family" is a
// value in code rather than a description in a document that can drift from it.
var StandardFamily = []string{
	MetricTaskSuccess,
	MetricRunCostUSD,
	MetricRunLatencyMS,
	MetricRunTokens,
	MetricToolErrorRate,
	MetricReliability,
}

// ContributionFamily is the per-node decomposition the harness emits per node per case.
var ContributionFamily = []string{
	MetricNodeCostShare,
	MetricNodeLatencyShare,
	MetricNodeSuccessImpact,
	MetricNodeToolErrorCount,
}
