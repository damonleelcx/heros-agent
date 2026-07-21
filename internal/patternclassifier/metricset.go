package patternclassifier

import "sort"

// MetricSet is the set of metrics in scope for a subgraph carrying a given pattern label, plus the
// failure modes that scoping implies. It is the payoff of the whole phase: the label SELECTS what
// gets measured on that region, so the harness measures each region correctly instead of computing
// every metric everywhere (and reporting retrieval relevance@k on a node with no retriever).
type MetricSet struct {
	Pattern Pattern `json:"pattern"`
	// Metrics are metric identifiers, in a stable order. P4's metric-set selection keys off these.
	Metrics []string `json:"metrics"`
	// Primary is the headline metric — the one a regression on this pattern shows up in first.
	// Routing's is misroute-rate; RAG's is relevance@k. Naming it keeps a reader from having to
	// guess which of six numbers matters.
	Primary string `json:"primary"`
	// FailureModes are the signature ways this pattern goes wrong. Carried now so P4.5's failure
	// taxonomy is scoped by the same table rather than re-deriving the mapping and drifting.
	FailureModes []string `json:"failure_modes"`
	// Available is false for a pattern whose label cannot yet be produced (behavioral: P5). The row
	// is authored now so P5 wires a label source into an EXISTING table instead of redesigning
	// dispatch — the mapping is the durable artifact, the label source is what grows.
	Available bool `json:"available"`
}

// metricSets is the SINGLE SOURCE OF TRUTH for pattern → metric-set (PRD §8.4). Every consumer —
// P4 metric selection, P4.5 failure scoping, the UI, the dump — reads this one table. Letting each
// consumer re-derive metric relevance from the label is exactly how four subsystems come to disagree
// about what a Routing subgraph should be scored on.
var metricSets = map[Pattern]MetricSet{
	PromptChaining: {
		Metrics:      []string{"cumulative_cost", "cumulative_latency", "handoff_validity", "output_contract_adherence"},
		Primary:      "handoff_validity",
		FailureModes: []string{"broken_handoff", "format_drift_breaks_next_node"},
		Available:    true,
	},
	Routing: {
		Metrics:      []string{"branch_load_balance", "misroute_rate", "per_branch_coverage", "routing_accuracy"},
		Primary:      "misroute_rate",
		FailureModes: []string{"everything_to_one_branch", "misroute"},
		Available:    true,
	},
	Parallelization: {
		Metrics:      []string{"branch_agreement", "fanout_cost", "fanout_latency", "merge_consistency", "redundant_branch_rate"},
		Primary:      "merge_consistency",
		FailureModes: []string{"merge_conflict", "no_real_independence"},
		Available:    true,
	},
	Reflection: {
		Metrics:      []string{"convergence", "iteration_count", "quality_gain_per_revision"},
		Primary:      "quality_gain_per_revision",
		FailureModes: []string{"degradation_on_revision", "non_convergence"},
		// Available: the LABEL ships in P3.5 as a structural candidate, so P4 can select the set.
		// What P5 adds is CONFIRMATION that the loop iterates, not the mapping.
		Available: true,
	},
	ToolUse: {
		Metrics:      []string{"arg_validity", "schema_mismatch_rate", "tool_call_success_rate", "wrong_tool_rate"},
		Primary:      "tool_call_success_rate",
		FailureModes: []string{"repeated_tool_failure", "schema_error", "wrong_tool"},
		Available:    true,
	},
	RetrievalRAG: {
		Metrics:      []string{"faithfulness", "recall", "relevance_at_k", "rerank_gain"},
		Primary:      "relevance_at_k",
		FailureModes: []string{"hallucination", "low_relevance", "retrieval_miss"},
		Available:    true,
	},
	MultiAgentCollaboration: {
		Metrics:      []string{"coordination_overhead", "inter_agent_message_validity", "per_role_contribution"},
		Primary:      "inter_agent_message_validity",
		FailureModes: []string{"dropped_or_invalid_message", "role_starvation"},
		Available:    true,
	},
	ResourceAwareOptimization: {
		Metrics:      []string{"cost_per_case", "cost_quality_pareto_position", "downgrade_safety", "tier_selection_accuracy"},
		Primary:      "cost_per_case",
		FailureModes: []string{"over_spend", "quality_loss_from_wrong_tier"},
		Available:    true,
	},

	// --- Authored now, activated when P5 behavioral classification supplies the label -------------
	Planning: {
		Metrics:      []string{"plan_adherence", "plan_completeness", "replanning_rate", "step_validity"},
		Primary:      "plan_adherence",
		FailureModes: []string{"abandoned_plan", "unexecutable_step"},
	},
	Prioritization: {
		Metrics:      []string{"ordering_quality", "starvation_rate", "top_k_precision"},
		Primary:      "ordering_quality",
		FailureModes: []string{"inversion", "starvation"},
	},
	ExplorationDiscovery: {
		Metrics:      []string{"branch_diversity", "cost_per_discovery", "coverage", "dead_end_rate"},
		Primary:      "coverage",
		FailureModes: []string{"premature_convergence", "unbounded_search"},
	},
	MemoryManagement: {
		Metrics:      []string{"memory_hit_rate", "recall_precision", "staleness", "write_amplification"},
		Primary:      "memory_hit_rate",
		FailureModes: []string{"contradictory_memory", "stale_read"},
	},
	ReasoningTechniques: {
		Metrics:      []string{"path_agreement", "reasoning_validity", "self_consistency", "vote_margin"},
		Primary:      "self_consistency",
		FailureModes: []string{"confident_wrong_consensus", "unfaithful_reasoning"},
	},
	InterAgentCommunication: {
		Metrics:      []string{"message_delivery_rate", "message_schema_validity", "protocol_adherence"},
		Primary:      "message_schema_validity",
		FailureModes: []string{"dropped_message", "protocol_violation"},
	},
	GoalSettingMonitoring: {
		Metrics:      []string{"goal_attainment", "goal_drift", "monitor_trigger_accuracy"},
		Primary:      "goal_attainment",
		FailureModes: []string{"goal_drift", "unmonitored_failure"},
	},
	ExceptionHandlingRecovery: {
		Metrics:      []string{"error_detection_rate", "recovery_cost", "recovery_success_rate", "unhandled_error_rate"},
		Primary:      "recovery_success_rate",
		FailureModes: []string{"retry_storm", "silent_swallow"},
	},
	HumanInTheLoop: {
		Metrics:      []string{"approval_latency", "escalation_precision", "human_override_rate"},
		Primary:      "escalation_precision",
		FailureModes: []string{"escalation_flood", "rubber_stamping"},
	},
	EvaluationMonitoring: {
		Metrics:      []string{"evaluator_agreement", "judge_calibration", "score_stability"},
		Primary:      "evaluator_agreement",
		FailureModes: []string{"judge_drift", "uncalibrated_scores"},
	},
	GuardrailsSafety: {
		Metrics:      []string{"block_precision", "block_recall", "bypass_rate", "false_block_rate"},
		Primary:      "block_recall",
		FailureModes: []string{"over_blocking", "silent_bypass"},
	},
	LearningAdaptation: {
		Metrics:      []string{"adaptation_gain", "feedback_incorporation_rate", "regression_after_update"},
		Primary:      "adaptation_gain",
		FailureModes: []string{"catastrophic_forgetting", "feedback_loop_amplification"},
	},
}

// MetricSetFor is THE dispatch seam. P4's metric-set selection keys off it, so a subgraph's pattern
// mechanically determines what is computed on it — a RAG subgraph gets retrieval metrics, a router
// gets misroute-rate, and neither gets the other's.
//
// An unknown pattern returns an EMPTY set with ok=false. It never returns a plausible default: a
// caller that silently received "some metrics" for a pattern this table does not know would compute
// numbers nobody can interpret, which is worse than computing none.
func MetricSetFor(p Pattern) (MetricSet, bool) {
	ms, ok := metricSets[p]
	if !ok {
		return MetricSet{}, false
	}
	ms.Pattern = p
	ms.Metrics = append([]string(nil), ms.Metrics...) // defensive copy: the table is not mutable by a caller
	ms.FailureModes = append([]string(nil), ms.FailureModes...)
	sort.Strings(ms.Metrics)
	sort.Strings(ms.FailureModes)
	return ms, true
}

// MetricSetsForLabels resolves the in-scope metric-sets for a whole classification, keyed by
// subgraph_ref. This is the shape a consumer actually wants: "for this region, measure these".
func MetricSetsForLabels(labels []Label) map[string][]MetricSet {
	out := map[string][]MetricSet{}
	for _, l := range labels {
		if ms, ok := MetricSetFor(l.Pattern); ok {
			out[l.SubgraphRef] = append(out[l.SubgraphRef], ms)
		}
	}
	for ref := range out {
		sets := out[ref]
		sort.SliceStable(sets, func(i, j int) bool { return sets[i].Pattern < sets[j].Pattern })
	}
	return out
}
