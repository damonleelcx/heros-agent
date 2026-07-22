package evalharness

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// calibration.go is Decision 3: judge calibration gates judge trust.
//
// An uncalibrated judge metric is decoration. Letting one drive a disqualifying gate would let an
// unverified LLM opinion silently kill a variant — the same failure P4.5 diagnosis and P5.5
// verification forbid ("no single unverified LLM opinion drives a change"), applied here at the
// measurement source. The discipline costs a small human-labeled subset up front; that subset is the
// only thing that makes the judge's thousands of automated scores worth reading.

// DefaultAgreementFloor is the kappa a judge must reach to be gate-eligible. 0.6 is the conventional
// "substantial agreement" boundary; it is a value here rather than a literal at a call site so
// raising or lowering it is one visible edit.
const DefaultAgreementFloor = 0.6

// DefaultCalibrationBins is how many buckets a continuous score is quantized into before kappa is
// computed. Two (pass/fail at the midpoint) is the default because that is the decision a gate
// actually makes; a kappa over 20 buckets would punish a judge for disagreeing about 0.71 vs 0.73,
// which nobody acts on.
const DefaultCalibrationBins = 2

var (
	// ErrNoHumanLabels means calibration was asked for with an empty subset. Returning a standing of
	// "calibrated, agreement 1.0, n=0" would be the worst possible answer.
	ErrNoHumanLabels = errors.New("evalharness: calibration subset carries no human labels")
	// ErrJudgeNotGateEligible is the refusal a gate configured on an uncalibrated or below-floor
	// judge produces.
	ErrJudgeNotGateEligible = errors.New("evalharness: judge metric is not eligible to be a gate input")
)

// HumanLabel is one human's score for one case, on the SAME [0,1] normalized scale the judge
// reports. Normalizing at the source means the comparison is not quietly comparing a 1-5 human
// rating against a 0-1 judge score.
type HumanLabel struct {
	CaseID string  `json:"case_id"`
	Score  float64 `json:"score"`
	// Labeler identifies who labeled it, so a subset labeled entirely by the judge's own author is
	// visible as such rather than passing as independent evidence.
	Labeler string `json:"labeler,omitempty"`
}

// CalibrationSubset is the human-labeled evidence for one judge metric (task 3.1).
type CalibrationSubset struct {
	Metric string       `json:"metric"`
	Floor  float64      `json:"floor"`
	Bins   int          `json:"bins"`
	Labels []HumanLabel `json:"labels"`
}

// Validate rejects a subset that cannot produce an honest agreement.
func (s CalibrationSubset) Validate() error {
	if s.Metric == "" {
		return fmt.Errorf("%w: subset names no metric", ErrInvalidEvaluator)
	}
	if len(s.Labels) == 0 {
		return fmt.Errorf("%w: metric %q", ErrNoHumanLabels, s.Metric)
	}
	for _, l := range s.Labels {
		if l.CaseID == "" {
			return fmt.Errorf("%w: metric %q has a label with no case_id", ErrInvalidEvaluator, s.Metric)
		}
		if l.Score < 0 || l.Score > 1 {
			return fmt.Errorf("%w: metric %q label for case %q scores %v, which is outside [0,1]",
				ErrInvalidEvaluator, s.Metric, l.CaseID, l.Score)
		}
	}
	return nil
}

func (s CalibrationSubset) floor() float64 {
	if s.Floor <= 0 {
		return DefaultAgreementFloor
	}
	return s.Floor
}

func (s CalibrationSubset) bins() int {
	if s.Bins < 2 {
		return DefaultCalibrationBins
	}
	return s.Bins
}

// Calibrate runs the judge over the human-labeled cases and computes its agreement (task 3.2).
//
// It runs the REAL judge over the REAL cases — there is no path where a standing is asserted rather
// than measured. A case the judge cannot score (no rubric, no output) is EXCLUDED from n_human
// rather than counted as agreement or disagreement: inventing a verdict for a case the judge
// declined would be the most direct way to fake a calibration.
func Calibrate(ctx context.Context, j JudgeEvaluator, subset CalibrationSubset,
	cases map[string]Case, traces map[string]Trace) (JudgeStanding, error) {

	if err := subset.Validate(); err != nil {
		return JudgeStanding{}, err
	}
	labels := append([]HumanLabel(nil), subset.Labels...)
	sort.Slice(labels, func(i, k int) bool { return labels[i].CaseID < labels[k].CaseID })

	bins := subset.bins()
	var judgeBin, humanBin []int
	for _, l := range labels {
		c, okCase := cases[l.CaseID]
		tr, okTrace := traces[l.CaseID]
		if !okCase || !okTrace {
			continue
		}
		v, err := j.Evaluate(ctx, tr, c, RunTarget())
		if err != nil {
			continue
		}
		judgeBin = append(judgeBin, quantize(v, bins))
		humanBin = append(humanBin, quantize(l.Score, bins))
	}
	if len(judgeBin) == 0 {
		return JudgeStanding{}, fmt.Errorf("%w: the judge scored none of the %d labeled cases",
			ErrNoHumanLabels, len(labels))
	}

	kappa, pct := cohensKappa(judgeBin, humanBin, bins)
	return JudgeStanding{
		Metric:           subset.Metric,
		Agreement:        kappa,
		PercentAgreement: pct,
		NHuman:           len(judgeBin),
		Floor:            subset.floor(),
		Calibrated:       true,
	}, nil
}

// quantize buckets a [0,1] score into one of bins categories.
func quantize(v float64, bins int) int {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	b := int(v * float64(bins))
	if b >= bins {
		b = bins - 1
	}
	return b
}

// cohensKappa returns kappa and raw percent agreement over two equal-length categorical vectors.
//
// Both numbers are reported because each is unreadable alone: kappa collapses when the label
// distribution is skewed (a judge agreeing with a human on 95 of 100 cases that are all "pass" earns
// a kappa near zero), and percent agreement flatters a judge that always answers "pass".
func cohensKappa(a, b []int, bins int) (kappa, percent float64) {
	n := len(a)
	if n == 0 || len(b) != n {
		return 0, 0
	}
	agree := 0
	countA := make([]float64, bins)
	countB := make([]float64, bins)
	for i := 0; i < n; i++ {
		if a[i] == b[i] {
			agree++
		}
		countA[a[i]]++
		countB[b[i]]++
	}
	po := float64(agree) / float64(n)
	var pe float64
	for k := 0; k < bins; k++ {
		pe += (countA[k] / float64(n)) * (countB[k] / float64(n))
	}
	if pe >= 1 {
		// Every label in one category: chance agreement is total, so kappa is undefined. Reporting
		// 1.0 would claim perfect agreement from a subset that proves nothing.
		return 0, po
	}
	return (po - pe) / (1 - pe), po
}

// EnsureGateEligible is the ONE predicate the gate layer consults before admitting a judge metric as
// a hard-constraint input (task 3.4). A gate configured on an ineligible judge does not "score
// lower" — it is REFUSED, and the refusal names the reason, so no variant is disqualified by an
// unverified opinion.
func EnsureGateEligible(st JudgeStanding) error {
	if st.GateEligible() {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrJudgeNotGateEligible, st.Reason())
}

// ─────────────────────────────────────────────────────────────────────────────
// Calibration store
// ─────────────────────────────────────────────────────────────────────────────

// CalibrationStore persists a judge metric's standing with its n_human (task 3.2). An interface so
// the harness does not depend on a concrete database; the Postgres implementation lands with the
// rest of the eval schema.
type CalibrationStore interface {
	PutStanding(ctx context.Context, st JudgeStanding) error
	Standing(ctx context.Context, metric string) (JudgeStanding, bool, error)
}

// MemCalibrationStore is the in-memory store used by the harness in tests and by the demo driver.
type MemCalibrationStore struct {
	mu sync.RWMutex
	by map[string]JudgeStanding
}

// NewMemCalibrationStore builds an empty in-memory calibration store.
func NewMemCalibrationStore() *MemCalibrationStore {
	return &MemCalibrationStore{by: map[string]JudgeStanding{}}
}

func (s *MemCalibrationStore) PutStanding(_ context.Context, st JudgeStanding) error {
	if st.Metric == "" {
		return fmt.Errorf("%w: a standing must name its metric", ErrInvalidEvaluator)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.by[st.Metric] = st
	return nil
}

func (s *MemCalibrationStore) Standing(_ context.Context, metric string) (JudgeStanding, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.by[metric]
	return st, ok, nil
}
