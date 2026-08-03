// Package evalboard is the read model the P4 UI renders: leaderboard, Pareto frontier, coverage
// report, spend, and the board-level states.
//
// It is built SERVER-SIDE, for the same reason patternclassifier.GraphView is: "are these two
// variants tied", "is this gate refused", "did coverage actually reach its threshold" are
// measurement questions with exactly one correct answer, and a second implementation of them in
// JavaScript would drift from this one. The browser renders what it is handed; it derives nothing.
//
// The states are DATA, not inferences from missing keys. A tie, a disqualification, a refused gate,
// a residual coverage obligation and a weak-labeled score each arrive as an explicit field, because
// a UI that infers "no winner" from the absence of something will eventually infer it wrongly.
package evalboard

import (
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/evalgen"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalrun"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/scoring"
)

// State is the board's aggregate state. It is READ from the persisted run counters, never derived
// from a client-side notion of "probably done by now" — derived state drifts, and the drift shows up
// as a board that says complete while runs are still landing.
type State string

const (
	// StateEmpty: no variants configured. The action is "add a variant", not "wait".
	StateEmpty State = "empty"
	// StatePartial: the fan-out is in progress. Real rows exist and are readable; intervals computed
	// from fewer than the seed floor are marked provisional.
	StatePartial State = "partial"
	// StateComplete: every planned unit landed.
	StateComplete State = "complete"
	// StateError: the board could not be computed. It NEVER renders a partial board as if whole.
	StateError State = "error"
)

// View is the whole board.
type View struct {
	State       State  `json:"state"`
	Error       string `json:"error,omitempty"`
	WorkflowID  string `json:"workflow_id"`
	EvalSetHash string `json:"eval_set_hash"`
	Profile     string `json:"profile"`
	GateSet     string `json:"gate_set"`
	// Profiles are the switchable named profiles. Switching one re-ranks from the cache and enqueues
	// zero runs; the UI is handed the list so it cannot invent a profile the server does not have.
	Profiles []string `json:"profiles"`

	Progress Progress `json:"progress"`

	Ranked       []Row `json:"ranked"`
	Disqualified []Row `json:"disqualified"`

	Pareto   []ParetoPoint       `json:"pareto"`
	Coverage CoverageView        `json:"coverage"`
	Spend    evalrun.SpendReport `json:"spend"`

	// AllTie is the banner condition: every gate-passing variant overlaps every other. It is a
	// computed FACT on the board, not something the UI counts up itself, because the all-tie board
	// is exactly where a well-meaning UI does the most damage by rendering rank 1 as a victory.
	AllTie bool `json:"all_tie"`
	// TieAnalysis says whether AllTie/TiedWith MEAN anything on this board.
	//
	// 🔴 `AllTie: false` is a claim — "these variants are distinguishable" — and a board that cannot
	// perform the overlap test would be making it by default. That test needs the bootstrap REPLICATES,
	// which exist only where the eval ran; a board assembled from linked runs receives intervals and no
	// replicates, so it cannot compute ties and must not imply it did. Hence a field rather than a
	// convention: this package's opening comment says the states are DATA, never inferences from a
	// missing key, and "we did not test for ties" is a state.
	TieAnalysis TieAnalysis `json:"tie_analysis"`
	// Notes are board-level caveats: refused gates, low-confidence eval set. They change what the
	// WHOLE board means, which is why they are not per-row badges.
	Notes []string `json:"notes,omitempty"`
	// Unmeasured are the variants excluded from the ranking because their runs did not complete.
	// A separate list rather than notes: 58 of them must not bury the one banner that explains why.
	Unmeasured []UnmeasuredView `json:"unmeasured,omitempty"`
	// RunsEnqueued is how many runs rendering this board cost. Always 0: ranking is a pure function
	// of the score cache. The field exists so the UI (and a test) can assert it rather than assume.
	RunsEnqueued int `json:"runs_enqueued"`
}

// TieAnalysis reports whether statistical tie detection was performed for this board.
type TieAnalysis string

const (
	// TieComputed: the overlap test ran over bootstrap replicates. AllTie and TiedWith are meaningful.
	TieComputed TieAnalysis = "computed"
	// TieUnavailable: the board was assembled from reported intervals with no replicates behind them.
	// AllTie is false and TiedWith is empty because nothing was TESTED, not because nothing tied.
	TieUnavailable TieAnalysis = "unavailable"
)

// UnmeasuredView is one variant excluded from the ranking, with the reason.
type UnmeasuredView struct {
	VariantID string `json:"variant_id"`
	Label     string `json:"label"`
	Reason    string `json:"reason"`
}

// Progress is the fan-out's read-from-storage completion state.
type Progress struct {
	UnitsPlanned   int `json:"units_planned"`
	UnitsCompleted int `json:"units_completed"`
	SeedFloor      int `json:"seed_floor"`
}

// Done reports terminal completion from the persisted counters.
func (p Progress) Done() bool { return p.UnitsPlanned > 0 && p.UnitsCompleted >= p.UnitsPlanned }

// Row is one leaderboard row, flattened for rendering.
type Row struct {
	Rank       int    `json:"rank"`
	VariantID  string `json:"variant_id"`
	Label      string `json:"label"`
	ConfigHash string `json:"config_hash"`
	// ConfigHashShort is the 12-char display prefix. Rendered short, carried full — a board row must
	// stay attributable to an exact configuration.
	ConfigHashShort string `json:"config_hash_short"`

	Score  float64 `json:"score"`
	CILow  float64 `json:"ci_low"`
	CIHigh float64 `json:"ci_high"`
	NSeeds int     `json:"n_seeds"`
	NCases int     `json:"n_cases"`
	Method string  `json:"method"`

	Components []ComponentView `json:"components"`
	Penalties  []PenaltyView   `json:"penalties,omitempty"`

	GatePass    bool     `json:"gate_pass"`
	FailedGates []string `json:"failed_gates,omitempty"`
	GateReasons []string `json:"gate_reasons,omitempty"`

	// Flags are the row-level states, as TEXT. Identity is never carried by color alone, so every
	// state ships a chip the screen reader and the printout both get.
	Flags []string `json:"flags,omitempty"`
	// TiedWith names the variants this row is statistically indistinguishable from.
	TiedWith []string `json:"tied_with,omitempty"`
	// Provisional marks an interval computed from fewer than the configured seed floor: wide on
	// purpose, and not yet a ranking.
	Provisional bool `json:"provisional"`
}

// ComponentView is one metric's contribution, raw AND normalized. Both, because a user acts on
// "$0.021 / 900 ms" and understands the ranking through "0.72 / 0.81"; showing one alone makes the
// board either unactionable or unexplainable.
type ComponentView struct {
	Metric       string  `json:"metric"`
	Role         string  `json:"role"`
	Raw          float64 `json:"raw"`
	RawLow       float64 `json:"raw_ci_low"`
	RawHigh      float64 `json:"raw_ci_high"`
	Normalized   float64 `json:"normalized"`
	Contribution float64 `json:"contribution"`
	Unit         string  `json:"unit"`
	// Judge is the calibration standing when this metric came from a judge — a judge number never
	// renders without its agreement.
	Judge *evalharness.JudgeStanding `json:"judge,omitempty"`
}

// PenaltyView is one subtractive term.
type PenaltyView struct {
	Term   string  `json:"term"`
	Amount float64 `json:"amount"`
}

// ParetoPoint is one point on the quality/cost/latency frontier, in RAW units.
type ParetoPoint struct {
	VariantID    string  `json:"variant_id"`
	Label        string  `json:"label"`
	Quality      float64 `json:"quality"`
	CostUSD      float64 `json:"cost_usd"`
	LatencyMS    float64 `json:"latency_ms"`
	NonDominated bool    `json:"non_dominated"`
	Composite    float64 `json:"composite"`
}

// CoverageView is the "is this eval set good enough?" screen's data.
type CoverageView struct {
	Measured   bool            `json:"measured"`
	Dimensions []DimensionView `json:"dimensions"`
	// Residual is every obligation still uncovered, each with its reason. It is a first-class field
	// rather than an absence, because "83% and here is the missing edge" is the honest answer and
	// "100%" would be the damaging one.
	Residual       []ResidualItem `json:"residual"`
	Met            bool           `json:"met"`
	Iterations     int            `json:"iterations"`
	StoppedBecause string         `json:"stopped_because,omitempty"`

	NCases             int     `json:"n_cases"`
	Difficulty         float64 `json:"difficulty"`
	DifficultyMeasured bool    `json:"difficulty_measured"`
	Diversity          float64 `json:"diversity"`
	OracleCoverage     float64 `json:"oracle_coverage"`
	NOracle            int     `json:"n_oracle"`
	// NIndecisive counts cases carrying an oracle that can never fail — the most misleading cases in
	// a set, because they look measured and decide nothing.
	NIndecisive   int      `json:"n_indecisive"`
	NGold         int      `json:"n_gold"`
	NWeak         int      `json:"n_weak"`
	NNone         int      `json:"n_none"`
	LowConfidence bool     `json:"low_confidence"`
	Reasons       []string `json:"reasons,omitempty"`
}

// DimensionView is one coverage axis rendered as achieved vs. target.
type DimensionView struct {
	Name      string   `json:"name"`
	Achieved  float64  `json:"achieved"`
	Target    float64  `json:"target"`
	Covered   int      `json:"covered"`
	Total     int      `json:"total"`
	Met       bool     `json:"met"`
	Uncovered []string `json:"uncovered,omitempty"`
	// Vacuous: this axis had NO obligations. "Nothing to measure" is not "everything covered", and
	// the UI must never render them the same.
	Vacuous bool `json:"vacuous,omitempty"`
}

// ResidualItem is one obligation the generator could not discharge.
type ResidualItem struct {
	ID          string `json:"id"`
	Dimension   string `json:"dimension"`
	Kind        string `json:"kind"`
	Unreachable bool   `json:"unreachable"`
	Reason      string `json:"reason"`
}

// Input is everything Build needs. It is a struct rather than eight parameters because the caller
// assembles it from four packages and a positional argument list would be a bug waiting to happen.
type Input struct {
	WorkflowID     string
	Cache          *scoring.Cache
	Profile        scoring.Profile
	Gates          scoring.GateSet
	Coverage       *evalgen.CoverageReport
	Quality        *evalgen.SetQuality
	StoppedBecause string
	Spend          evalrun.SpendReport
	Progress       Progress
	Labels         map[string]string // variant_id -> display label
}

// Build assembles the view.
func Build(in Input) View {
	v := View{
		WorkflowID: in.WorkflowID,
		Profile:    in.Profile.Name,
		GateSet:    in.Gates.Name,
		Profiles:   scoring.ProfileNames(),
		Progress:   in.Progress,
		Spend:      in.Spend,
	}
	if in.Cache == nil || len(in.Cache.Order) == 0 {
		v.State = StateEmpty
		v.Coverage = coverageView(in)
		return v
	}
	v.EvalSetHash = in.Cache.EvalSetHash

	lb := in.Cache.Rank(in.Profile, in.Gates)
	v.RunsEnqueued = lb.RunsEnqueued
	v.Notes = lb.Notes

	seedFloor := in.Progress.SeedFloor
	if seedFloor <= 0 {
		seedFloor = evalstats.DefaultMinSeeds
	}
	for _, r := range lb.Ranked {
		v.Ranked = append(v.Ranked, row(r, in.Labels, seedFloor))
	}
	for _, r := range lb.Disqualified {
		v.Disqualified = append(v.Disqualified, row(r, in.Labels, seedFloor))
	}

	// AllTie: every gate-passer overlaps every other. Computed here so the UI never has to.
	//
	// Build always has replicates — it works from a scoring.Cache, which is built by bootstrapping the
	// raw series — so this path is unconditionally `computed`. The other value exists for assemblers
	// that do not (internal/hostedboard).
	v.TieAnalysis = TieComputed
	if len(v.Ranked) > 1 {
		v.AllTie = true
		for _, r := range v.Ranked {
			if len(r.TiedWith) != len(v.Ranked)-1 {
				v.AllTie = false
				break
			}
		}
	}
	// AllTie is deliberately NOT also appended to Notes. The UI renders it as its own banner, and
	// emitting the same sentence twice reads as two separate findings — the duplication was visible
	// on the first live render.

	for _, p := range in.Cache.Pareto(lb) {
		v.Pareto = append(v.Pareto, ParetoPoint{
			VariantID:    p.VariantID,
			Label:        labelFor(in.Labels, p.VariantID),
			Quality:      p.Quality,
			CostUSD:      p.CostUSD,
			LatencyMS:    p.LatencyMS,
			NonDominated: p.NonDominated,
			Composite:    p.Composite,
		})
	}

	// Variants the cache could not score are surfaced, not dropped: "the budget ran out before 58 of
	// these were measured" is a finding a reader must see, and a board that silently shows 26 rows
	// where 84 variants were submitted is the quietest possible lie.
	//
	// They are ONE summary note plus a separate list, not one note each. The first version emitted a
	// banner per variant and put 58 banners above the board — technically complete, practically
	// unreadable, and it buried the fan-out-in-progress banner that actually explained why.
	if n := len(in.Cache.Unmeasured); n > 0 {
		v.State = StatePartial
		v.Notes = append(v.Notes, fmt.Sprintf(
			"%d of %d variants could not be scored — their runs did not complete. The ranking below covers only the measured ones.",
			n, n+len(in.Cache.Order)))
		for _, u := range in.Cache.Unmeasured {
			v.Unmeasured = append(v.Unmeasured, UnmeasuredView{VariantID: u.VariantID, Label: labelFor(in.Labels, u.VariantID), Reason: u.Reason})
		}
	}

	v.Coverage = coverageView(in)
	// No board-level note here: the leaderboard already emits ONE low-confidence summary derived
	// from the same verdict (every variant carries the flag because they share the eval set), and
	// the coverage panel states the specific reasons. A second sentence saying the same thing in
	// different words reads as a second, independent finding.

	if v.State != StatePartial { // an unmeasured variant already made this board partial
		switch {
		case in.Progress.UnitsPlanned == 0, in.Progress.Done():
			v.State = StateComplete
		default:
			v.State = StatePartial
		}
	}
	sort.Strings(v.Notes)
	return v
}

// Error builds the error state. It carries the message and NOTHING else — a board that renders half
// its rows beside an error banner reads as "mostly fine", which is the opposite of what an error
// means.
func Error(workflowID string, err error) View {
	return View{State: StateError, WorkflowID: workflowID, Error: err.Error(), Profiles: scoring.ProfileNames()}
}

func row(r scoring.Row, labels map[string]string, seedFloor int) Row {
	out := Row{
		Rank:            r.Rank,
		VariantID:       r.VariantID,
		Label:           labelFor(labels, r.VariantID),
		ConfigHash:      r.ConfigHash,
		ConfigHashShort: shortHash(r.ConfigHash),
		Score:           r.Composite.Mean,
		CILow:           r.Composite.Low,
		CIHigh:          r.Composite.High,
		NSeeds:          r.Composite.NSeeds,
		NCases:          r.Composite.NCases,
		Method:          r.Composite.Method,
		GatePass:        r.Gate.Passed,
		FailedGates:     r.Gate.Failed,
		GateReasons:     r.Gate.Reasons,
		TiedWith:        r.TiedWith,
	}
	if r.Label != "" && out.Label == r.VariantID {
		out.Label = r.Label
	}
	for _, name := range sortedKeys(r.Components) {
		c := r.Components[name]
		out.Components = append(out.Components, ComponentView{
			Metric:       c.Metric,
			Role:         string(c.Role),
			Raw:          c.Raw.Mean,
			RawLow:       c.Raw.Low,
			RawHigh:      c.Raw.High,
			Normalized:   c.Normalized,
			Contribution: c.Contribution,
			Unit:         c.Unit,
			Judge:        c.JudgeStanding,
		})
	}
	for term, amount := range r.Penalties {
		out.Penalties = append(out.Penalties, PenaltyView{Term: term, Amount: amount})
	}
	sort.Slice(out.Penalties, func(i, j int) bool { return out.Penalties[i].Term < out.Penalties[j].Term })

	out.Provisional = r.Composite.NSeeds > 0 && r.Composite.NSeeds < seedFloor

	if len(r.TiedWith) > 0 {
		out.Flags = append(out.Flags, "tie")
	}
	if !r.Gate.Passed {
		out.Flags = append(out.Flags, "disqualified")
	}
	if r.WeakLabeled {
		out.Flags = append(out.Flags, "weak-labeled")
	}
	if r.LowConfidence {
		out.Flags = append(out.Flags, "low-confidence")
	}
	if out.Provisional {
		out.Flags = append(out.Flags, "provisional")
	}
	sort.Strings(out.Flags)
	return out
}

func coverageView(in Input) CoverageView {
	cv := CoverageView{StoppedBecause: in.StoppedBecause}
	if in.Coverage != nil {
		rep := *in.Coverage
		cv.Measured = true
		cv.Met = rep.Met()
		cv.Iterations = rep.Iterations
		cv.NCases = rep.NCases
		for _, d := range rep.Dimensions() {
			covered := 0
			for _, it := range d.Items {
				if it.Covered {
					covered++
				}
			}
			cv.Dimensions = append(cv.Dimensions, DimensionView{
				Name:      d.Name,
				Achieved:  d.Achieved,
				Target:    d.Target,
				Covered:   covered,
				Total:     len(d.Items),
				Met:       d.Met(),
				Uncovered: d.Uncovered(),
				Vacuous:   d.Vacuous,
			})
			for _, it := range d.Items {
				if it.Covered {
					continue
				}
				reason := "not yet exercised by any case"
				if it.Unreachable {
					reason = fmt.Sprintf("unreachable after %d round(s): no generator produced an input that reaches it", rep.Iterations)
				}
				cv.Residual = append(cv.Residual, ResidualItem{
					ID: it.ID, Dimension: d.Name, Kind: it.Kind, Unreachable: it.Unreachable, Reason: reason,
				})
			}
		}
	}
	if in.Coverage != nil {
		for _, name := range in.Coverage.Vacuous() {
			cv.Reasons = append(cv.Reasons, fmt.Sprintf(
				"%s coverage could not be measured: the IR carries no obligations on that axis. "+
					"For path coverage this normally means the workflow's inter-node flow has not been "+
					"observed yet — P1 static discovery finds call sites, edges arrive with P5 tracing.",
				name))
			cv.LowConfidence = true
		}
		// A ZERO-covered axis is as damning as a vacuous one, and less obvious: the obligations
		// exist and not one of them was discharged. That has two very different causes — an eval set
		// that never reaches that part of the workflow, or a runner that cannot execute it at all —
		// and the board cannot tell them apart, which is exactly why it must not rank as if it could.
		// Either way, what it measures is the harness, not the variants.
		for _, d := range in.Coverage.Dimensions() {
			if d.Vacuous || len(d.Items) == 0 || d.Achieved > 0 {
				continue
			}
			cv.Reasons = append(cv.Reasons, fmt.Sprintf(
				"%s coverage is 0 of %d: no case in this eval set exercised any %s of the workflow, "+
					"so nothing on this board is evidence about it",
				d.Name, len(d.Items), d.Name))
			cv.LowConfidence = true
		}
	}
	if in.Quality != nil {
		q := *in.Quality
		cv.Difficulty = q.Difficulty
		cv.DifficultyMeasured = q.DifficultyMeasured
		cv.Diversity = q.Diversity
		cv.NGold, cv.NWeak, cv.NNone = q.NGold, q.NWeak, q.NNone
		cv.OracleCoverage, cv.NOracle, cv.NIndecisive = q.OracleCoverage, q.NOracle, q.NIndecisive
		// OR, never assign: the coverage pass above may already have set LowConfidence (a vacuous or
		// zero-covered axis), and assigning here silently threw that away — the board reported
		// low_confidence:false for a workflow whose path coverage was NOT MEASURABLE and whose node
		// coverage was 0%. Reasons are appended for the same reason.
		cv.LowConfidence = cv.LowConfidence || q.LowConfidence
		cv.Reasons = append(cv.Reasons, q.Reasons...)
		if cv.NCases == 0 {
			cv.NCases = q.NCases
		}
	}
	return cv
}

func labelFor(labels map[string]string, id string) string {
	if l, ok := labels[id]; ok && l != "" {
		return l
	}
	return id
}

func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

func sortedKeys(m map[string]scoring.Component) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
