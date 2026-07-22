package evalharness

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Task 1.2 — the four built-in evaluators, each over the same trace.

func TestExactMatch(t *testing.T) {
	e := NewExactMatch()
	for _, tc := range []struct {
		name   string
		output string
		want   float64
	}{
		{"identical", `{"a":"world"}`, 1},
		// Key order is a formatting difference, not a quality regression: canonical-JSON equality.
		{"reordered keys", `{"b":1,"a":"world"}`, 0}, // extra key IS a difference
		{"different", `{"a":"mars"}`, 0},
		{"unparseable", `not json`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTrace("r").node("n", 0, 1, 0, 0, false).output(tc.output).build()
			got, err := e.Evaluate(context.Background(), tr, baseCase(), RunTarget())
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if got != tc.want {
				t.Fatalf("output %s: want %v got %v", tc.output, tc.want, got)
			}
		})
	}
}

func TestExactMatchIsCanonicalNotByteEqual(t *testing.T) {
	e := NewExactMatch()
	c := baseCase()
	c.Reference = json.RawMessage(`{"a":"world","b":1}`)
	tr := newTrace("r").node("n", 0, 1, 0, 0, false).output(`{"b":1,   "a":"world"}`).build()
	got, err := e.Evaluate(context.Background(), tr, c, RunTarget())
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if got != 1 {
		t.Fatal("two objects differing only in key order and whitespace are the same answer")
	}
}

// A case with no reference is a SKIP, not a zero — "could not measure" is not "measured and failed".
func TestExactMatchWithoutReferenceIsNotApplicable(t *testing.T) {
	e := NewExactMatch()
	c := baseCase()
	c.Reference = nil
	c.Label = LabelNone
	tr := newTrace("r").node("n", 0, 1, 0, 0, false).output(`{"a":"world"}`).build()
	if _, err := e.Evaluate(context.Background(), tr, c, RunTarget()); !errors.Is(err, ErrNotApplicable) {
		t.Fatalf("want ErrNotApplicable, got %v", err)
	}
}

func TestJSONSchemaValidity(t *testing.T) {
	e := NewJSONSchemaValidity()
	c := baseCase()
	c.OutputSchema = json.RawMessage(`{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}}`)

	for _, tc := range []struct {
		name   string
		output string
		want   float64
	}{
		{"valid", `{"a":"world"}`, 1},
		{"wrong type", `{"a":42}`, 0},
		{"missing required", `{"b":1}`, 0},
		{"not json", `<html>`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTrace("r").node("n", 0, 1, 0, 0, false).output(tc.output).build()
			got, err := e.Evaluate(context.Background(), tr, c, RunTarget())
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if got != tc.want {
				t.Fatalf("output %s: want %v got %v", tc.output, tc.want, got)
			}
		})
	}
}

// A broken schema is the CASE's defect. Scoring 0 would blame the variant for a broken eval set.
func TestBrokenSchemaIsNotApplicableNotAFailure(t *testing.T) {
	e := NewJSONSchemaValidity()
	c := baseCase()
	c.OutputSchema = json.RawMessage(`{"type": 12345}`)
	tr := newTrace("r").node("n", 0, 1, 0, 0, false).output(`{"a":"world"}`).build()
	if _, err := e.Evaluate(context.Background(), tr, c, RunTarget()); !errors.Is(err, ErrNotApplicable) {
		t.Fatalf("want ErrNotApplicable for an unusable schema, got %v", err)
	}
}

// A schema reaching out over the network is not reproducible; the compiler refuses it.
func TestRemoteRefIsRefused(t *testing.T) {
	_, err := CompileSchema(json.RawMessage(`{"$ref":"https://example.com/schema.json"}`))
	if err == nil {
		t.Fatal("a remote $ref must be refused so an oracle stays self-contained")
	}
}

func TestRegex(t *testing.T) {
	e := NewRegex()
	c := baseCase()
	c.Pattern = `"a"\s*:\s*"wor`

	tr := newTrace("r").node("n", 0, 1, 0, 0, false).output(`{"a":"world"}`).build()
	got, err := e.Evaluate(context.Background(), tr, c, RunTarget())
	if err != nil || got != 1 {
		t.Fatalf("want match, got %v err %v", got, err)
	}

	tr2 := newTrace("r").node("n", 0, 1, 0, 0, false).output(`{"a":"mars"}`).build()
	got, err = e.Evaluate(context.Background(), tr2, c, RunTarget())
	if err != nil || got != 0 {
		t.Fatalf("want no match, got %v err %v", got, err)
	}

	c.Pattern = `([`
	if _, err := e.Evaluate(context.Background(), tr, c, RunTarget()); !errors.Is(err, ErrNotApplicable) {
		t.Fatalf("an uncompilable pattern is the case's defect, want ErrNotApplicable got %v", err)
	}
}

func TestLLMJudgeNormalizesOntoUnitRange(t *testing.T) {
	model := &stubJudge{score: 4}
	j, err := NewLLMJudge(EvaluatorLLMJudge, MetricJudgeScore, model,
		JudgeConfig{Model: "stub", ScaleMax: 5}, calibratedStanding(MetricJudgeScore))
	if err != nil {
		t.Fatalf("new judge: %v", err)
	}
	c := baseCase()
	c.Rubric = "Is the answer correct?"
	tr := newTrace("r").node("n", 0, 1, 0, 0, false).output(`{"a":"world"}`).build()

	got, err := j.Evaluate(context.Background(), tr, c, RunTarget())
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if got != 0.8 {
		t.Fatalf("4 on a 1-5 scale must normalize to 0.8, got %v", got)
	}
	if model.calls != 1 {
		t.Fatalf("want exactly one judge call, got %d", model.calls)
	}
}

// A judge answering off-scale is a broken contract, surfaced — never clamped into a perfect score.
func TestLLMJudgeOffScaleIsOutOfRange(t *testing.T) {
	j, _ := NewLLMJudge(EvaluatorLLMJudge, MetricJudgeScore, &stubJudge{score: 7},
		JudgeConfig{Model: "stub", ScaleMax: 5}, calibratedStanding(MetricJudgeScore))
	c := baseCase()
	c.Rubric = "rubric"
	tr := newTrace("r").node("n", 0, 1, 0, 0, false).output(`{"a":"world"}`).build()
	if _, err := j.Evaluate(context.Background(), tr, c, RunTarget()); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("want ErrOutOfRange for a 7 on a 0-5 scale, got %v", err)
	}
}

// A model that answers without a score has not met the contract; it must not read as a zero.
func TestLLMJudgeMissingScoreIsAnError(t *testing.T) {
	j, _ := NewLLMJudge(EvaluatorLLMJudge, MetricJudgeScore, &stubJudge{omitScore: true},
		JudgeConfig{Model: "stub", ScaleMax: 5}, calibratedStanding(MetricJudgeScore))
	c := baseCase()
	c.Rubric = "rubric"
	tr := newTrace("r").node("n", 0, 1, 0, 0, false).output(`{"a":"world"}`).build()
	if _, err := j.Evaluate(context.Background(), tr, c, RunTarget()); err == nil {
		t.Fatal("a missing score must be an error, not a zero")
	}
}

// Every judge value carries its calibration standing — a judge score without its agreement is
// decoration presented as measurement.
func TestJudgeValueCarriesStanding(t *testing.T) {
	j, _ := NewLLMJudge(EvaluatorLLMJudge, MetricJudgeScore, &stubJudge{score: 5},
		JudgeConfig{Model: "stub", ScaleMax: 5}, calibratedStanding(MetricJudgeScore))
	c := baseCase()
	c.Rubric = "rubric"
	tr := newTrace("r").node("n", 0, 1, 0, 0, false).output(`{"a":"world"}`).build()

	mv, err := Compute(context.Background(), j, tr, c, RunTarget())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if mv.Judge == nil {
		t.Fatal("a judge score must carry its calibration standing")
	}
	if mv.Judge.NHuman != 40 || !mv.Judge.Calibrated {
		t.Fatalf("standing not propagated: %+v", mv.Judge)
	}
}

// A judge with no model or a nonsensical scale cannot be constructed at all.
func TestJudgeConstructionRefusesNonsense(t *testing.T) {
	if _, err := NewLLMJudge("j", MetricJudgeScore, nil, JudgeConfig{ScaleMax: 5}, JudgeStanding{}); err == nil {
		t.Fatal("a judge with no model must be refused")
	}
	if _, err := NewLLMJudge("j", MetricJudgeScore, &stubJudge{}, JudgeConfig{ScaleMax: 0}, JudgeStanding{}); err == nil {
		t.Fatal("a judge with a zero scale must be refused")
	}
}
