package evalharness

import (
	"context"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/telemetry"
)

// family.go is task 1.5: the standard metric family — task success, cost, latency, tokens,
// tool-error rate — computed from the P2.5 traces with NO per-workflow configuration. Every one of
// these numbers is derived from what the substrate already emits; none of them requires a workflow
// author to instrument anything, which is why the family is a function of the trace rather than a
// set of registered evaluators the user could forget to add.

// SuccessOracleFor picks the evaluator that decides task_success for a case.
//
// Precedence when the case does not name one explicitly: exact-match, then schema, then regex, then
// judge. Deterministic oracles come first on purpose — they are gold (decidable, reviewable, free)
// and a judge call that a free oracle could have answered is spend with no added information. A case
// that genuinely wants the judge says so via Case.SuccessOracle.
func SuccessOracleFor(reg *Registry, c Case) (Evaluator, error) {
	if reg == nil {
		return nil, fmt.Errorf("%w: no registry", ErrNotApplicable)
	}
	if c.SuccessOracle != "" {
		e, ok := reg.Get(c.SuccessOracle)
		if !ok {
			return nil, fmt.Errorf("%w: case %q names oracle %q which is not registered",
				ErrNotApplicable, c.CaseID, c.SuccessOracle)
		}
		return e, nil
	}
	type candidate struct {
		name string
		has  bool
	}
	for _, cand := range []candidate{
		{EvaluatorExactMatch, len(c.Reference) > 0},
		{EvaluatorJSONSchema, len(c.OutputSchema) > 0},
		{EvaluatorRegex, c.Pattern != ""},
		{EvaluatorLLMJudge, c.Rubric != ""},
	} {
		if !cand.has {
			continue
		}
		if e, ok := reg.Get(cand.name); ok {
			return e, nil
		}
	}
	return nil, fmt.Errorf("%w: case %q has no usable success oracle", ErrNotApplicable, c.CaseID)
}

// RunMetrics computes the standard family for one completed run of one case.
//
// A skipped success oracle yields NO task_success row rather than a zero: "we could not measure
// success here" and "success was measured and it failed" are different facts, and a leaderboard that
// conflates them reports missing measurement as poor quality.
func RunMetrics(ctx context.Context, reg *Registry, tr Trace, c Case) ([]MetricValue, error) {
	var out []MetricValue

	oracle, err := SuccessOracleFor(reg, c)
	switch {
	case err == nil:
		mv, cerr := Compute(ctx, oracle, tr, c, RunTarget())
		if cerr == nil {
			// The oracle's own metric (exact_match / schema_valid / ...) AND task_success, so a board
			// can show which oracle decided it without losing the headline number.
			out = append(out, mv)
			success := mv
			success.Metric = MetricTaskSuccess
			out = append(out, success)
		} else if !errors.Is(cerr, ErrNotApplicable) && !errors.Is(cerr, ErrInadmissible) {
			return nil, cerr
		}
	case errors.Is(err, ErrNotApplicable):
		// no oracle: reference-free case, nothing to assert about success
	default:
		return nil, err
	}

	agg := aggregate(tr)
	out = append(out,
		runValue(MetricRunCostUSD, agg.costUSD, telemetry.UnitUSD, c),
		runValue(MetricRunLatencyMS, agg.latencyMS, telemetry.UnitMS, c),
		runValue(MetricRunTokens, agg.tokens, telemetry.UnitTokens, c),
		runValue(MetricToolErrorRate, agg.toolErrorRate, telemetry.UnitRatio, c),
		runValue(MetricReliability, agg.reliability, telemetry.UnitRatio, c),
	)
	return out, nil
}

func runValue(metric string, v float64, unit string, c Case) MetricValue {
	return MetricValue{
		Metric:         metric,
		Value:          v,
		Unit:           unit,
		NodeID:         telemetry.NodeIDRun,
		Evaluator:      standardFamilyEvaluator,
		ReferenceLabel: c.Label,
	}
}

// standardFamilyEvaluator is the evaluator_name the trace-derived family is attributed to. It is a
// real name (not an empty string) so every eval_result row is attributable to a producer.
const standardFamilyEvaluator = "standard_family"

// runAggregate is the per-run rollup of what the spans carry.
type runAggregate struct {
	costUSD       float64
	latencyMS     float64
	tokens        float64
	toolErrorRate float64
	reliability   float64
	toolCalls     int
	toolErrors    int
}

// aggregate rolls a trace up. Cost and tokens sum over NODE spans (one per provider call); latency
// is the RUN span's wall-clock where present, falling back to the node-span envelope — summing node
// latencies would over-count a parallel fan-out, reporting a workflow as slower than a stopwatch
// says it is.
func aggregate(tr Trace) runAggregate {
	var a runAggregate
	var earliest, latest = int64(0), int64(0)
	haveEnvelope := false

	for _, sp := range tr.Spans {
		switch sp.Kind {
		case telemetry.SpanKindNode:
			a.costUSD += attrFloat(sp.Attributes, telemetry.AttrCostUSD)
			a.tokens += attrFloat(sp.Attributes, telemetry.AttrGenAIUsageInput)
			a.tokens += attrFloat(sp.Attributes, telemetry.AttrGenAIUsageOutput)
			s, e := sp.StartTime.UnixNano(), sp.EndTime.UnixNano()
			if !haveEnvelope || s < earliest {
				earliest = s
			}
			if !haveEnvelope || e > latest {
				latest = e
			}
			haveEnvelope = true
		case telemetry.SpanKindTool:
			a.toolCalls++
			if sp.Status == telemetry.SpanStatusError {
				a.toolErrors++
			}
		case telemetry.SpanKindRun:
			if d := sp.Duration(); d > 0 {
				a.latencyMS = float64(d.Milliseconds())
			}
		}
	}
	if a.latencyMS == 0 && haveEnvelope && latest > earliest {
		a.latencyMS = float64((latest - earliest) / 1e6)
	}
	if a.toolCalls > 0 {
		a.toolErrorRate = float64(a.toolErrors) / float64(a.toolCalls)
	}
	a.reliability = 1
	if tr.Failed || a.toolErrors > 0 || anyNodeFailed(tr) {
		a.reliability = 0
	}
	return a
}

func anyNodeFailed(tr Trace) bool {
	for _, sp := range tr.NodeSpans() {
		if sp.Status == telemetry.SpanStatusError {
			return true
		}
		if failed, ok := sp.Attributes[telemetry.AttrNodeFailed].(bool); ok && failed {
			return true
		}
	}
	return false
}

// attrFloat reads a numeric span attribute regardless of which numeric type the emitter used.
// Attributes are map[string]any and an int emitted here would be a float64 after a JSON round-trip;
// a type switch that only handled float64 would silently read every in-process token count as zero.
func attrFloat(attrs map[string]any, key string) float64 {
	switch v := attrs[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func attrString(attrs map[string]any, key string) string {
	s, _ := attrs[key].(string)
	return s
}
