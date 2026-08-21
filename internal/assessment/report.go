package assessment

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// report.go is the whole assessment: exactly nine findings, ordered by evidence strength, carrying no
// composite of any kind.
//
// # 🚫 Read this before adding a field
//
// There is no `score`, no `grade`, no `level`, no `summary_number`, no `health`, no `percentage`. That
// is program ruling R4 and design D3, and it is the single most likely thing to be added later by
// request, in a hurry, by someone who did not read this file. `composite_fence_test.go` reads this
// package's declarations and fails on a field or a method that spans axes numerically, so the refusal
// is reviewable rather than merely intended.
//
// The honest summary a reader is owed instead is `Tally` — how many axes are in each state. It is a
// distribution, not a reduction: nine numbers that sum to nine, from which no ordering of one
// repository against another can be computed.

// ErrIncompleteAssessment is returned when a report does not cover all nine axes.
var ErrIncompleteAssessment = errors.New("assessment: incomplete report")

// Assessment is one run's result.
//
// Its identity fields ARE exported. Unlike Finding they carry no conditional requirements — every one
// is required, `Validate` is a total check, and hiding them would force an accessor per field for no
// guarantee in return.
type Assessment struct {
	AssessmentID string `json:"assessment_id"`
	TenantID     string `json:"tenant_id"`
	WorkflowID   string `json:"workflow_id"`
	// SourceRevision is half of the pin key. An assessment is OF a revision — which is why nothing in
	// it can be stale with respect to itself, and why P31's `stale` state has no member here.
	SourceRevision string `json:"source_revision"`
	// AgentConfigHash is the other half (design D7, FR16).
	AgentConfigHash string `json:"agent_config_hash"`

	StartedAtMS   int64 `json:"started_at_ms"`
	CompletedAtMS int64 `json:"completed_at_ms"`

	// SpendUSD and SpendCapUSD are §7.3. The cap is carried on the report, not only enforced during
	// it, so a reader of a report that degraded to `budget_exhausted` can see what the cap WAS
	// without going to look up a configuration that may since have changed.
	SpendUSD    float64 `json:"spend_usd"`
	SpendCapUSD float64 `json:"spend_cap_usd"`

	// Findings are the nine. Unexported would buy nothing here — a Finding is already opaque, and the
	// slice's own invariant (exactly nine, one per axis) is checked by Validate at the write boundary
	// where it can actually stop a bad row.
	Findings []Finding `json:"findings"`
}

// Validate enforces FR1: a finding for each of the nine axes, none omitted, none duplicated.
//
// 🔴 "None omitted" is the requirement with teeth. A report that silently drops the axes it could not
// assess is shorter, prettier, and lies by construction — §9.1. So the check is set EQUALITY against
// `Axes()`, not a length check: nine findings that happen to include `model` twice and never `memory`
// would pass a count and fail here.
func (a Assessment) Validate() error {
	if a.AssessmentID == "" || a.TenantID == "" || a.WorkflowID == "" {
		return fmt.Errorf("%w: an assessment must name itself, its tenant and its workflow", ErrIncompleteAssessment)
	}
	if a.SourceRevision == "" {
		return fmt.Errorf("%w: %s names no source revision, so it is a report about no particular code",
			ErrIncompleteAssessment, a.AssessmentID)
	}
	if a.AgentConfigHash == "" {
		return fmt.Errorf("%w: %s records no agent config hash, so its findings cannot be attributed "+
			"to the configuration that produced them (FR16)", ErrIncompleteAssessment, a.AssessmentID)
	}

	seen := map[Axis]bool{}
	for _, f := range a.Findings {
		if err := f.Validate(); err != nil {
			return err
		}
		if seen[f.Axis()] {
			return fmt.Errorf("%w: %s reports on %s twice", ErrIncompleteAssessment, a.AssessmentID, f.Axis())
		}
		seen[f.Axis()] = true
	}
	var missing []string
	for _, axis := range Axes() {
		if !seen[axis] {
			missing = append(missing, string(axis))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s omits %v — an axis with nothing to say reports not_measured with its "+
			"missing input, it is never dropped (FR1)", ErrIncompleteAssessment, a.AssessmentID, missing)
	}
	return nil
}

// ── The wire form is CANONICAL (FR15) ────────────────────────────────────────────────────────────

// assessmentWire is the JSON shape. A private mirror, so `MarshalJSON` can impose an order without
// re-declaring every field at the call site.
type assessmentWire struct {
	AssessmentID    string    `json:"assessment_id"`
	TenantID        string    `json:"tenant_id"`
	WorkflowID      string    `json:"workflow_id"`
	SourceRevision  string    `json:"source_revision"`
	AgentConfigHash string    `json:"agent_config_hash"`
	StartedAtMS     int64     `json:"started_at_ms"`
	CompletedAtMS   int64     `json:"completed_at_ms"`
	SpendUSD        float64   `json:"spend_usd"`
	SpendCapUSD     float64   `json:"spend_cap_usd"`
	Findings        []Finding `json:"findings"`
}

// MarshalJSON emits the findings in AXIS REPORT ORDER, always.
//
// # 🔴 The defect this closes, found by the live proof and by nothing else
//
// FR15 says two assessments of the same `(revision, config)` are identical, and QA task 7.5 makes that
// BYTE-IDENTICAL. It was not. A freshly produced assessment carries its findings in the order the
// extractors ran (report order); one read back out of Postgres carries them in `ORDER BY axis`
// (alphabetical). Every field matched, every test over the struct passed, and the two DOCUMENTS
// differed — so the guarantee held for a reader comparing fields and failed for anyone diffing the
// export, which is the form the guarantee is actually consumed in.
//
// The fix is in the TYPE rather than in the two producers. Making the store's SQL emit report order
// would be a second copy of `Axes()`, in a language that cannot see it, and making the runner sort
// would leave the store's path unfixed. Here there is one order, imposed at the one boundary where
// "identical" is measured.
//
// 🚫 This is NOT `Ordered()`. That is EVIDENCE-STRENGTH order, which is how the report is RENDERED and
// which changes as findings change state. Serialising in it would make the wire form depend on the
// content, and a re-run that upgraded one axis would reorder the document for reasons unrelated to
// that axis.
func (a Assessment) MarshalJSON() ([]byte, error) {
	index := map[Axis]int{}
	for i, axis := range Axes() {
		index[axis] = i
	}
	ordered := append([]Finding(nil), a.Findings...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return index[ordered[i].Axis()] < index[ordered[j].Axis()]
	})
	return json.Marshal(assessmentWire{
		AssessmentID: a.AssessmentID, TenantID: a.TenantID, WorkflowID: a.WorkflowID,
		SourceRevision: a.SourceRevision, AgentConfigHash: a.AgentConfigHash,
		StartedAtMS: a.StartedAtMS, CompletedAtMS: a.CompletedAtMS,
		SpendUSD: a.SpendUSD, SpendCapUSD: a.SpendCapUSD,
		Findings: ordered,
	})
}

// UnmarshalJSON reads the wire form. It does NOT re-sort: the document is already canonical, and a
// decoder that re-imposed the order would hide a producer that had stopped doing so.
func (a *Assessment) UnmarshalJSON(b []byte) error {
	var w assessmentWire
	if err := json.Unmarshal(b, &w); err != nil {
		return fmt.Errorf("assessment: decoding an assessment: %w", err)
	}
	*a = Assessment{
		AssessmentID: w.AssessmentID, TenantID: w.TenantID, WorkflowID: w.WorkflowID,
		SourceRevision: w.SourceRevision, AgentConfigHash: w.AgentConfigHash,
		StartedAtMS: w.StartedAtMS, CompletedAtMS: w.CompletedAtMS,
		SpendUSD: w.SpendUSD, SpendCapUSD: w.SpendCapUSD,
		Findings: w.Findings,
	}
	return nil
}

// ── Ordering (FR5) ───────────────────────────────────────────────────────────────────────────────

// rank is a finding's position on the evidence-strength ladder. Lower is stronger.
//
// # Why this ladder and not severity
//
// A reader treats an ordered list as a priority queue whether or not it is one. Ordering by severity
// would mean ordering by a judgement that is itself, for most axes, an inference — so the top of the
// list would be the model's opinion wearing the authority of a ranking. Ordering by evidence strength
// makes the list's meaning checkable: the top is what we know best.
//
// The ladder is stated once here and nowhere else. The console renders `Ordered`'s output.
func rank(f Finding) int {
	switch {
	case f.State() == StateMeasured:
		return 0
	case f.State() == StateObserved && f.Origin() == OriginStructural:
		return 1
	case f.State() == StateObserved && f.Origin() == OriginInferred:
		// The spec's ladder names this rung "inferred" rather than a state, and that is the point:
		// an inference that concluded is a weaker claim than a parse, and a stronger one than silence.
		return 2
	case f.State() == StateNotMeasured:
		return 3
	default:
		// `refused` last. It is not a weaker claim than `not_measured` — it is the SAME absence of
		// evidence with a different owner, and it sorts last because it is the only rung the reader
		// can do nothing about. Everything above it is either a fact or a task for them; this rung is
		// a task for us.
		return 4
	}
}

// Ordered returns the findings sorted by evidence strength, then by the axis order in `Axes()`.
//
// The secondary key is the axis's REPORT order rather than its name, so two reports of the same
// repository list the same rung in the same sequence — FR15's identical findings has to mean
// identical output, and a map iteration or an alphabetical tiebreak inside a rung is exactly the kind
// of instability that makes a byte-comparison fence flap.
func (a Assessment) Ordered() []Finding {
	index := map[Axis]int{}
	for i, axis := range Axes() {
		index[axis] = i
	}
	out := append([]Finding(nil), a.Findings...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		return index[out[i].Axis()] < index[out[j].Axis()]
	})
	return out
}

// ── The honest summary ───────────────────────────────────────────────────────────────────────────

// Tally counts the axes in each state. Nine numbers that sum to nine.
//
// 🔴 This is what the manager in PRD §4 is given INSTEAD of a score, and it is deliberately not
// reducible to one: "4 observed · 3 not_measured · 2 refused" is a shape, and no arithmetic over it
// produces an ordering of one repository against another. That is the difference between a summary
// and a composite, and it is the whole of §8.2's answer.
type Tally struct {
	Measured    int `json:"measured"`
	Observed    int `json:"observed"`
	NotMeasured int `json:"not_measured"`
	Refused     int `json:"refused"`
	// Inferred cuts across the states: how many of the nine rest on a model. It is the ratio design
	// D2 exists to make visible, and it is reported as a COUNT of axes rather than as a percentage,
	// because a percentage of nine invites a reader to compare it with a percentage of something else.
	Inferred int `json:"inferred"`
}

// Tally computes the distribution.
func (a Assessment) Tally() Tally {
	var t Tally
	for _, f := range a.Findings {
		switch f.State() {
		case StateMeasured:
			t.Measured++
		case StateObserved:
			t.Observed++
		case StateNotMeasured:
			t.NotMeasured++
		case StateRefused:
			t.Refused++
		}
		if f.Origin() == OriginInferred {
			t.Inferred++
		}
	}
	return t
}

// AllNotMeasured reports whether every one of the nine axes came back `not_measured`.
//
// 🔴 This is DevOps task 6.2's signal, computed here rather than in a dashboard query. An assessment
// that returns nine `not_measured` findings is a successful run by every aggregate measure — it
// completed, it returned 200, it persisted nine rows — and it is also the earliest evidence that a
// language frontend or the sandbox has broken. A rate that is invisible in a success rate has to be
// its own named quantity or nobody will ever look at it.
func (a Assessment) AllNotMeasured() bool {
	if len(a.Findings) != len(Axes()) {
		return false
	}
	for _, f := range a.Findings {
		if f.State() != StateNotMeasured {
			return false
		}
	}
	return true
}

// Partial reports whether any axis degraded because the assessment ran out of money (§7.3).
//
// The console states it above the report. A partial report presented as complete is the specific
// failure the budget requirement names, and the only defence is that "some of this is missing because
// we stopped paying" is a fact the report carries rather than one a reader has to notice.
func (a Assessment) Partial() bool {
	for _, f := range a.Findings {
		if f.MissingInput() == MissingBudgetExhausted {
			return true
		}
	}
	return false
}
