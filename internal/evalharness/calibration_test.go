package evalharness

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
)

// agreeingJudge answers exactly what a lookup table says, so a calibration test asserts the
// AGREEMENT ARITHMETIC rather than a model's behaviour.
type tableJudge struct {
	byCase map[string]float64
	scale  float64
}

func (j *tableJudge) Judge(_ context.Context, req JudgeRequest) (RawVerdict, error) {
	// The rendered prompt carries the case input; the fixture keys off the case id embedded in it.
	for id, v := range j.byCase {
		if containsCaseMarker(req.Prompt, id) {
			s := v * j.scale
			return RawVerdict{Score: &s, Rationale: "table"}, nil
		}
	}
	zero := 0.0
	return RawVerdict{Score: &zero, Rationale: "unknown case"}, nil
}

func containsCaseMarker(prompt, caseID string) bool {
	return len(caseID) > 0 && indexOf(prompt, `"case":"`+caseID+`"`) >= 0
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// calibrationFixture builds n cases whose inputs embed the case id, plus the traces for them.
func calibrationFixture(n int) (map[string]Case, map[string]Trace) {
	cases := map[string]Case{}
	traces := map[string]Trace{}
	for i := 0; i < n; i++ {
		id := "cal-" + string(rune('a'+i))
		cases[id] = Case{
			CaseID:     id,
			WorkflowID: "wf",
			Input:      json.RawMessage(`{"case":"` + id + `"}`),
			Label:      LabelNone,
			Rubric:     "Is the answer correct?",
			Origin:     OriginHandAuthored,
		}
		traces[id] = newTrace("run-"+id).node("n", 0.001, 10, 1, 1, false).output(`{"a":"x"}`).build()
	}
	return cases, traces
}

func newJudge(t *testing.T, model JudgeModel, standing JudgeStanding) *LLMJudge {
	t.Helper()
	j, err := NewLLMJudge(EvaluatorLLMJudge, MetricJudgeScore, model,
		JudgeConfig{Model: "stub", ScaleMax: 1}, standing)
	if err != nil {
		t.Fatalf("new judge: %v", err)
	}
	return j
}

// Task 3.1 / 3.2 — a human-labeled subset is accepted and the judge's agreement is MEASURED against
// it, persisted with n_human.
func TestCalibrateMeasuresAgreementAgainstHumanLabels(t *testing.T) {
	cases, traces := calibrationFixture(10)

	// A judge that matches the human on 9 of 10 cases (the humans split 5 pass / 5 fail).
	judgeScores := map[string]float64{}
	var labels []HumanLabel
	for i := 0; i < 10; i++ {
		id := "cal-" + string(rune('a'+i))
		human := 0.0
		if i < 5 {
			human = 1.0
		}
		judged := human
		if i == 9 {
			judged = 1.0 // the one disagreement
		}
		judgeScores[id] = judged
		labels = append(labels, HumanLabel{CaseID: id, Score: human, Labeler: "reviewer-1"})
	}

	j := newJudge(t, &tableJudge{byCase: judgeScores, scale: 1}, uncalibratedStanding(MetricJudgeScore))
	st, err := Calibrate(context.Background(), j, CalibrationSubset{
		Metric: MetricJudgeScore, Floor: DefaultAgreementFloor, Labels: labels,
	}, cases, traces)
	if err != nil {
		t.Fatalf("calibrate: %v", err)
	}

	if st.NHuman != 10 {
		t.Fatalf("n_human: want 10 got %d", st.NHuman)
	}
	if math.Abs(st.PercentAgreement-0.9) > 1e-9 {
		t.Fatalf("percent agreement: want 0.9 got %v", st.PercentAgreement)
	}
	// po=0.9, pe = (0.6*0.5)+(0.4*0.5) = 0.5 -> kappa = 0.8
	if math.Abs(st.Agreement-0.8) > 1e-9 {
		t.Fatalf("kappa: want 0.8 got %v", st.Agreement)
	}
	if !st.Calibrated {
		t.Fatal("a measured subset must mark the judge calibrated")
	}
	if !st.GateEligible() {
		t.Fatalf("kappa 0.8 over the 0.6 floor must be gate-eligible: %+v", st)
	}
}

// A skewed subset (every human label the same) yields kappa 0, NOT a flattering 1.0 — chance
// agreement is total there and the subset proves nothing.
func TestSkewedSubsetDoesNotEarnPerfectAgreement(t *testing.T) {
	cases, traces := calibrationFixture(8)
	judgeScores := map[string]float64{}
	var labels []HumanLabel
	for i := 0; i < 8; i++ {
		id := "cal-" + string(rune('a'+i))
		judgeScores[id] = 1
		labels = append(labels, HumanLabel{CaseID: id, Score: 1})
	}
	j := newJudge(t, &tableJudge{byCase: judgeScores, scale: 1}, uncalibratedStanding(MetricJudgeScore))
	st, err := Calibrate(context.Background(), j, CalibrationSubset{Metric: MetricJudgeScore, Labels: labels}, cases, traces)
	if err != nil {
		t.Fatalf("calibrate: %v", err)
	}
	if st.Agreement != 0 {
		t.Fatalf("a fully-skewed subset must not earn a positive kappa, got %v", st.Agreement)
	}
	if st.PercentAgreement != 1 {
		t.Fatalf("raw percent agreement is still 1.0, got %v", st.PercentAgreement)
	}
	if st.GateEligible() {
		t.Fatal("a judge whose kappa is 0 must not gate, however high its raw agreement")
	}
}

// Task 3.3 — the standing rides on EVERY score the judge produces, before and after calibration.
func TestAgreementIsReportedAlongsideEveryScore(t *testing.T) {
	cases, traces := calibrationFixture(6)
	scores := map[string]float64{}
	var labels []HumanLabel
	for i := 0; i < 6; i++ {
		id := "cal-" + string(rune('a'+i))
		v := float64(i % 2)
		scores[id] = v
		labels = append(labels, HumanLabel{CaseID: id, Score: v})
	}
	j := newJudge(t, &tableJudge{byCase: scores, scale: 1}, uncalibratedStanding(MetricJudgeScore))

	// Before calibration the standing is present and says "uncalibrated".
	c := cases["cal-a"]
	mv, err := Compute(context.Background(), j, traces["cal-a"], c, RunTarget())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if mv.Judge == nil || mv.Judge.Calibrated {
		t.Fatalf("an uncalibrated judge must say so on every score: %+v", mv.Judge)
	}

	st, err := Calibrate(context.Background(), j, CalibrationSubset{Metric: MetricJudgeScore, Labels: labels}, cases, traces)
	if err != nil {
		t.Fatalf("calibrate: %v", err)
	}
	j.SetStanding(st)

	mv, err = Compute(context.Background(), j, traces["cal-a"], c, RunTarget())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if mv.Judge == nil || !mv.Judge.Calibrated || mv.Judge.NHuman != 6 {
		t.Fatalf("the measured standing must ride on every subsequent score: %+v", mv.Judge)
	}
}

// Task 3.4 — a below-floor judge is flagged AND barred from gating; the refusal names the reason.
func TestBelowFloorJudgeCannotGate(t *testing.T) {
	cases, traces := calibrationFixture(10)
	// A judge that disagrees with the human half the time: kappa near zero.
	scores := map[string]float64{}
	var labels []HumanLabel
	for i := 0; i < 10; i++ {
		id := "cal-" + string(rune('a'+i))
		human := float64(i % 2)
		judged := float64((i / 2) % 2)
		scores[id] = judged
		labels = append(labels, HumanLabel{CaseID: id, Score: human})
	}
	j := newJudge(t, &tableJudge{byCase: scores, scale: 1}, uncalibratedStanding(MetricJudgeScore))
	st, err := Calibrate(context.Background(), j, CalibrationSubset{
		Metric: MetricJudgeScore, Floor: DefaultAgreementFloor, Labels: labels}, cases, traces)
	if err != nil {
		t.Fatalf("calibrate: %v", err)
	}
	if st.Agreement >= DefaultAgreementFloor {
		t.Fatalf("fixture is not below floor (kappa %v); the test would prove nothing", st.Agreement)
	}
	if st.GateEligible() {
		t.Fatal("a below-floor judge must not be gate-eligible")
	}
	err = EnsureGateEligible(st)
	if !errors.Is(err, ErrJudgeNotGateEligible) {
		t.Fatalf("want ErrJudgeNotGateEligible, got %v", err)
	}
	if err.Error() == "" || st.Reason() == "" {
		t.Fatal("the refusal must name the reason so a UI can render it")
	}
}

// An uncalibrated judge is barred from gating too — there is no "no evidence means fine" path.
func TestUncalibratedJudgeCannotGate(t *testing.T) {
	st := uncalibratedStanding(MetricJudgeScore)
	if st.GateEligible() {
		t.Fatal("an uncalibrated judge must never be gate-eligible")
	}
	if err := EnsureGateEligible(st); !errors.Is(err, ErrJudgeNotGateEligible) {
		t.Fatalf("want ErrJudgeNotGateEligible, got %v", err)
	}
}

// Calibration with no labels is refused; a standing of "calibrated, n=0" is the worst answer
// available and must be unreachable.
func TestCalibrationWithoutLabelsIsRefused(t *testing.T) {
	cases, traces := calibrationFixture(3)
	j := newJudge(t, &tableJudge{byCase: map[string]float64{}, scale: 1}, uncalibratedStanding(MetricJudgeScore))
	_, err := Calibrate(context.Background(), j, CalibrationSubset{Metric: MetricJudgeScore}, cases, traces)
	if !errors.Is(err, ErrNoHumanLabels) {
		t.Fatalf("want ErrNoHumanLabels, got %v", err)
	}
}

// A case the judge declines is EXCLUDED from n_human, never counted as agreement — inventing a
// verdict for a declined case is the most direct way to fake a calibration.
func TestDeclinedCasesAreExcludedFromNHuman(t *testing.T) {
	cases, traces := calibrationFixture(4)
	// Strip the rubric from two cases: the judge declines them (ErrNotApplicable).
	for _, id := range []string{"cal-c", "cal-d"} {
		c := cases[id]
		c.Rubric = ""
		cases[id] = c
	}
	scores := map[string]float64{"cal-a": 1, "cal-b": 1, "cal-c": 1, "cal-d": 1}
	labels := []HumanLabel{
		{CaseID: "cal-a", Score: 1}, {CaseID: "cal-b", Score: 0},
		{CaseID: "cal-c", Score: 1}, {CaseID: "cal-d", Score: 1},
	}
	j := newJudge(t, &tableJudge{byCase: scores, scale: 1}, uncalibratedStanding(MetricJudgeScore))
	st, err := Calibrate(context.Background(), j, CalibrationSubset{Metric: MetricJudgeScore, Labels: labels}, cases, traces)
	if err != nil {
		t.Fatalf("calibrate: %v", err)
	}
	if st.NHuman != 2 {
		t.Fatalf("only the 2 scorable cases count toward n_human, got %d", st.NHuman)
	}
}

// The standing persists with its n_human.
func TestCalibrationStoreRoundTrip(t *testing.T) {
	s := NewMemCalibrationStore()
	want := calibratedStanding(MetricJudgeScore)
	if err := s.PutStanding(context.Background(), want); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, ok, err := s.Standing(context.Background(), MetricJudgeScore)
	if err != nil || !ok {
		t.Fatalf("standing: ok=%v err=%v", ok, err)
	}
	if got != want {
		t.Fatalf("round trip lost data:\n got %+v\nwant %+v", got, want)
	}
	if _, ok, _ := s.Standing(context.Background(), "no_such_metric"); ok {
		t.Fatal("an unknown metric must not resolve to a standing")
	}
}

// A standing with no metric name is refused: an unattributed standing cannot gate anything safely.
func TestStandingWithoutMetricIsRefused(t *testing.T) {
	s := NewMemCalibrationStore()
	if err := s.PutStanding(context.Background(), JudgeStanding{Calibrated: true, NHuman: 10}); err == nil {
		t.Fatal("a standing with no metric must be refused")
	}
}
