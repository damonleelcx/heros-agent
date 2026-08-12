package evalboard

import (
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/evalgen"
	"github.com/heros-foreal/agentd/internal/evalharness"
)

// evalset.go is the read model behind `/app/workflows/{id}/evalset` (P30 task 1.12).
//
// # The question it answers, which the board could not
//
// The board reports `n_cases` as a number and nothing else. A reader who wants to know WHICH cases —
// which families are represented, how many carry an oracle that can decide anything, which ones look
// measured and decide nothing — has no way to ask. Worse, the number is the denominator under every
// score on the board, so "8 cases" is doing load-bearing work while being completely opaque.
//
// # 🔴 What a hosted deployment can and cannot list, stated as data
//
// `internal/runlink/allowlist.go` permits `eval.case_count` — "a count, never the cases" — and says in
// terms that the eval set itself never crosses and that there is no field on the wire it could occupy.
// That is a security boundary somebody argued for, and it is not weakened here.
//
// So on a hosted deployment this surface's state is `counts_only`: it reports the denominator, the
// per-family split, the decisive-oracle count and the indecisive count, all of which DO cross as
// counts — and it says plainly that the cases stay on the customer's machine, naming the rule rather
// than rendering an empty table that reads as "you have no cases".
//
// A `CaseSource` fills the list where one genuinely exists (a deployment that runs the harness itself,
// or a future wire change made deliberately). The surface then reports `listed`, and task 1.15's fence
// applies: the list length must equal the denominator, and a mismatch is an ERROR STATE, never a
// shorter table under an unchanged number.

// EvalSetState is the closed vocabulary for what this surface can say.
type EvalSetState string

const (
	// EvalSetNeverLinked: no run has been linked, so there is no eval set to describe. Distinct from
	// `counts_only` with zero cases, which would assert a measurement of an empty set.
	EvalSetNeverLinked EvalSetState = "never_linked"
	// EvalSetCountsOnly: the platform holds the denominator and the quality counts, and not the cases.
	// The state a hosted deployment is in by construction — see this file's header.
	EvalSetCountsOnly EvalSetState = "counts_only"
	// EvalSetListed: the cases are enumerated and their count agrees with the denominator.
	EvalSetListed EvalSetState = "listed"
	// 🔴 EvalSetInconsistent: a list was supplied and it does not have `n_cases` entries. It is an
	// ERROR, not a rendering: every score on the board is computed over `n_cases`, so a list that
	// disagrees means one of the two numbers describes a different eval set, and showing the shorter
	// table under the larger number would silently pick the wrong one.
	EvalSetInconsistent EvalSetState = "inconsistent"
)

// EvalSetView is one workflow's eval set as the console reads it.
type EvalSetView struct {
	WorkflowID string       `json:"workflow_id"`
	State      EvalSetState `json:"state"`
	// Sentence explains the state. Always populated — a state with no sentence is a chip a reader has
	// to interpret.
	Sentence string `json:"sentence"`
	// NCases is the denominator every score on the board is computed over.
	NCases int `json:"n_cases"`
	// Cases are the cases the platform can name. Empty in `counts_only`, which the sentence explains.
	Cases []EvalCaseView `json:"cases"`
	// Families is the per-family split the platform DOES hold, so a `counts_only` surface still
	// answers "what is in this set" at the resolution it has.
	Families []FamilyCount `json:"families"`
	// NOracle is how many cases carry an oracle that can actually return NO.
	NOracle int `json:"n_oracle"`
	// NIndecisive counts cases whose oracle can never fail — the most misleading cases in a set,
	// because they look measured and decide nothing.
	NIndecisive int `json:"n_indecisive"`
	// IndecisiveReasons are the distinct explanations, so a reader learns WHY.
	IndecisiveReasons []string `json:"indecisive_reasons"`
	// 🔴 VacuousDimensions names the coverage axes that had NO obligations — BY NAME, never as a count
	// (P30 task 1.13). "1 axis not measurable" tells a reader nothing they can act on; "path coverage
	// was not measurable" tells them the workflow's inter-node flow has not been observed.
	VacuousDimensions []string `json:"vacuous_dimensions"`
	// UncoveredNodes are graph nodes no case exercises, by id (P30 task 8.9).
	//
	// 🔴 EMPTY IS AMBIGUOUS ON ITS OWN and must never be read as "every node is exercised". When this
	// deployment holds no coverage report, `Unattributed` carries UnattributedUncoveredNodes and the
	// console says it cannot tell — see BuildEvalSet.
	UncoveredNodes []string `json:"uncovered_nodes"`
	// Unattributed names the per-case columns the platform does not hold on this deployment. It is a
	// list of column names rather than a boolean so the console can mark the exact cells that are
	// unknown, instead of showing an em-dash that looks like legitimately absent data.
	Unattributed []string `json:"unattributed"`
}

// UnattributedUncoveredNodes is the `Unattributed` member meaning "this deployment cannot say which
// graph nodes no case exercises". A named constant rather than a literal at two call sites, because the
// console branches on the exact string and a typo would silently restore the ambiguity it removes.
const UnattributedUncoveredNodes = "uncovered_nodes"

// EvalCaseView is one case, at the resolution this surface is allowed to show.
//
// 🔴 Identifiers and closed-set members only. There is no field here an input, a reference, a rubric or
// a judge prompt could occupy — the same construction argument `linked_transform` rests on, applied to
// a read model instead of a table.
type EvalCaseView struct {
	CaseID string `json:"case_id"`
	// Family is the failure-taxonomy slot the case occupies. Empty means the platform does not hold
	// it, which `Unattributed` names — never "this case has no family".
	Family string `json:"family"`
	// Oracle names the evaluator that decides task_success. Empty means unattributed, as above.
	Oracle string `json:"oracle"`
	// Indecisive marks a case whose oracle can never fail.
	Indecisive bool `json:"indecisive"`
}

// FamilyCount is one failure-taxonomy family and how many cases occupy it.
type FamilyCount struct {
	Family string `json:"family"`
	Cases  int    `json:"cases"`
}

// EvalSetInput is everything BuildEvalSet needs.
type EvalSetInput struct {
	WorkflowID string
	// NCases is the board's denominator. It is passed in rather than derived from Cases, deliberately:
	// deriving it would make the two agree by construction and delete the fence in task 1.15, which
	// exists precisely because they can come from different places.
	NCases int
	// Linked reports whether ANY run has been linked. Without it, "no runs" and "a run with an empty
	// eval set" are one state, and they are different facts.
	Linked bool
	// Cases is the enumeration, when a source has one. nil is not "zero cases" — see the states.
	Cases []evalharness.Case
	// CasesAvailable distinguishes "this deployment can enumerate cases and there are none" from
	// "this deployment cannot enumerate cases". A nil slice cannot say which.
	CasesAvailable bool
	Quality        *evalgen.SetQuality
	Coverage       *evalgen.CoverageReport
}

// BuildEvalSet assembles the eval-set surface.
func BuildEvalSet(in EvalSetInput) EvalSetView {
	v := EvalSetView{
		WorkflowID: in.WorkflowID,
		NCases:     in.NCases,
		// Every collection empty, never nil: a consumer handed `null` has to treat an absence as a
		// state, which is the failure this whole workstream is about.
		Cases:             []EvalCaseView{},
		Families:          []FamilyCount{},
		IndecisiveReasons: []string{},
		VacuousDimensions: []string{},
		UncoveredNodes:    []string{},
		Unattributed:      []string{},
	}
	if in.Quality != nil {
		v.NOracle = in.Quality.NOracle
		v.NIndecisive = in.Quality.NIndecisive
		v.IndecisiveReasons = append(v.IndecisiveReasons, in.Quality.IndecisiveReasons...)
		if v.NCases == 0 {
			v.NCases = in.Quality.NCases
		}
		// The reference-label split. It is the family axis the platform holds today as counts, and it
		// is what makes `counts_only` an answer rather than a shrug.
		for _, fc := range []FamilyCount{
			{Family: "gold reference", Cases: in.Quality.NGold},
			{Family: "weak reference", Cases: in.Quality.NWeak},
			{Family: "no reference", Cases: in.Quality.NNone},
		} {
			if fc.Cases > 0 {
				v.Families = append(v.Families, fc)
			}
		}
	}
	if in.Coverage != nil {
		// 🔴 BY NAME (task 1.13). evalgen.CoverageReport.Vacuous() already returns the names; the board
		// folded them into a sentence and a `low_confidence` boolean, and a reader could not tell WHICH
		// axis was unmeasurable without reading prose.
		v.VacuousDimensions = append(v.VacuousDimensions, in.Coverage.Vacuous()...)
		v.UncoveredNodes = append(v.UncoveredNodes, in.Coverage.Node.Uncovered()...)
	} else {
		// 🔴 TASK 8.9's SECOND HALF, and the half that was missing. With no coverage report,
		// `UncoveredNodes` stays empty — and an empty list of unexercised nodes is INDISTINGUISHABLE
		// from every node being exercised, which is the most reassuring possible reading and is not
		// something this deployment knows.
		//
		// It joins `Unattributed` rather than gaining a boolean of its own, because that list is
		// already the mechanism for "the platform does not hold this" and the console already renders
		// its members as unknown rather than as absent. A second mechanism for one idea is how two
		// surfaces come to disagree about what an empty list means.
		v.Unattributed = append(v.Unattributed, UnattributedUncoveredNodes)
	}

	for _, c := range in.Cases {
		// The ORACLE KIND, from the same probe `evalgen.MeasureQuality` counts with — so the per-case
		// column and the NIndecisive total cannot disagree. Deriving the kind a second way here is how
		// a table comes to show three decisive oracles under a headline that says two.
		verdict := c.DecisiveOracle()
		v.Cases = append(v.Cases, EvalCaseView{
			CaseID: c.CaseID,
			Family: string(c.EdgeCase),
			Oracle: verdict.Kind,
			// Indecisive means the case HAS an oracle that can never fail. A case with no oracle at all
			// is a different and more obvious problem, and folding the two would hide the subtle one.
			Indecisive: !verdict.Decisive && c.HasOracle(),
		})
	}
	sort.SliceStable(v.Cases, func(i, j int) bool { return v.Cases[i].CaseID < v.Cases[j].CaseID })

	v.State, v.Sentence, v.Unattributed = evalSetState(in, len(v.Cases))
	return v
}

// evalSetState resolves the state, its sentence, and which per-case columns are unattributed.
func evalSetState(in EvalSetInput, listed int) (EvalSetState, string, []string) {
	switch {
	case !in.Linked && !in.CasesAvailable:
		return EvalSetNeverLinked,
			"No run has been linked for this workflow, so there is no eval set to describe. This is not " +
				"an empty eval set — it is the absence of any measurement to have one.",
			[]string{}

	case !in.CasesAvailable:
		// The hosted reality, and the honest name for it.
		return EvalSetCountsOnly,
			fmt.Sprintf("This deployment holds the eval set's SIZE and its quality counts, and not the "+
				"cases. The wire contract permits `eval.case_count` — a count, never the cases — so the "+
				"%d case(s) behind every score on the board stay on your machine. The split below is "+
				"everything that does cross.", in.NCases),
			// Named per column, so a console marks the exact cells it cannot fill rather than rendering
			// an em-dash a reader would read as "this case has no family".
			[]string{"case_id", "family", "oracle", "indecisive"}

	case listed != in.NCases:
		// 🔴 An error, not a rendering. See EvalSetInconsistent.
		//
		// Proved red: defeating this comparison (`case false:`) makes
		// TestAListShorterThanTheDenominatorIsAnErrorNotATable and its longer-list twin report `listed`
		// over a table that does not match the number above it — which is the exact silent disagreement
		// the fence exists to catch.
		return EvalSetInconsistent,
			fmt.Sprintf("The eval set lists %d case(s) and every score on the board is computed over %d. "+
				"One of those two numbers describes a different eval set, and this surface will not pick "+
				"which — showing the shorter table under the larger number would make the disagreement "+
				"invisible.", listed, in.NCases),
			[]string{}

	default:
		return EvalSetListed,
			fmt.Sprintf("%d case(s), which is the denominator every score on the board is computed over.",
				listed),
			[]string{}
	}
}
