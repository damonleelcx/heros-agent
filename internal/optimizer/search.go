package optimizer

import "sort"

// search.go is the diagnosis-guided search (design Decision 2): it enumerates candidate changes at the
// P4.5-attributed node+dimension — through the P5.5 operator catalog — BEFORE any blind grid/Bayesian
// expansion over the wider model×prompt×context space, and it records the motivating diagnosis on
// EVERY candidate so the sample-efficiency claim is auditable, not asserted. Blind expansion is a
// bounded fallback: it fires only when the targeted candidates are exhausted AND budget remains, and it
// draws from a SEPARATE blind sub-budget so it can never consume the whole ceiling.

// Target is one attributed failure to propose against: the P4.5 diagnosis id, the localized node and
// dimension, and a priority (higher attribution first). The optimizer keeps this thin, framework-
// agnostic type so the search's ORDERING logic — the load-bearing "diagnosis-guided first" property —
// is testable without the full P4.5/P5.5 wiring; production assembles Targets from the read-only P4.5
// scorecard.
type Target struct {
	DiagnosisID string  `json:"diagnosis_id"`
	Node        string  `json:"node_id"`
	Dimension   string  `json:"dimension"`
	Priority    float64 `json:"priority"`
	// Payload is the opaque per-node bundle the production enumerator needs (the proposal.Target). The
	// search itself never inspects it.
	Payload any `json:"-"`
}

// SearchCandidate is one enumerated candidate the loop will verify. It carries the motivating diagnosis
// (task 1.3), the node+dimension it changes, whether it came from the diagnosis-guided phase or the
// blind fallback (task 1.4), and the candidate spec bytes the merge path opens a PR for.
type SearchCandidate struct {
	DiagnosisID string          `json:"diagnosis_id"`
	Node        string          `json:"node_id"`
	Dimension   string          `json:"dimension"`
	Source      CandidateSource `json:"source"`
	ConfigHash  string          `json:"config_hash"`
	SpecBytes   []byte          `json:"-"`
	Operator    string          `json:"operator"`
	Rationale   string          `json:"rationale"`
	Providers   []string        `json:"providers,omitempty"`
	// ExpectedGain is a cheap pre-verification estimate (operator-prior × severity) used ONLY to order
	// verification cheapest/highest-yield first. The measured verdict replaces it (design Q2).
	ExpectedGain float64 `json:"expected_gain"`
}

// Enumerator turns one attributed Target into diagnosis-guided candidates through the P5.5 operator
// catalog. Making it an interface keeps the search's ordering logic pure; production wires an adapter
// over proposal.Engine.Propose, whose candidates already carry their diagnosis + node + dimension.
type Enumerator interface {
	Enumerate(t Target) []SearchCandidate
}

// BlindExpander produces blind grid/Bayesian candidates over the wider space, given the current live
// config and the spend still available under the blind sub-budget. It exists so the search can widen
// when the diagnosis is incomplete, but only within its carved-out sub-budget.
type BlindExpander interface {
	Expand(currentConfigHash string, blindBudgetRemaining float64) []SearchCandidate
}

// Policy is the search's per-call budget/enablement state. It is how the loop tells the search "the
// targeted candidates are exhausted" and "this much blind budget remains", so the blind-fallback
// trigger (task 1.4) is a pure function of inputs rather than hidden search state.
type Policy struct {
	// TargetedExhausted is set by the loop once every diagnosis-guided candidate has been tried; only
	// then may blind expansion fire (design Decision 2: diagnosis-guided first).
	TargetedExhausted bool
	// BudgetRemaining is the run's remaining cumulative budget. Blind expansion needs budget to remain.
	BudgetRemaining float64
	// BlindBudgetRemaining is the remaining blind SUB-budget. Blind expansion draws only from this, so it
	// can never consume the whole ceiling (design Q1).
	BlindBudgetRemaining float64
}

// Search enumerates candidates diagnosis-guided-first with a bounded blind fallback.
type Search struct {
	Enum  Enumerator
	Blind BlindExpander
}

// NextCandidates returns the ordered candidate plan for the current state: ALL diagnosis-guided
// candidates (at the attributed node+dimension, in target-priority then cheapest-verify order) come
// first; blind candidates are appended ONLY when the loop reports the targeted set exhausted AND both
// the run budget and the blind sub-budget still have room. Every returned candidate carries its
// motivating diagnosis and its source — the two facts the audit needs to check that guidance actually
// preceded the sweep.
func (s Search) NextCandidates(targets []Target, currentConfigHash string, policy Policy) []SearchCandidate {
	// Diagnosis-guided phase: enumerate in priority order (higher attribution first, node tiebreak).
	ordered := append([]Target(nil), targets...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Priority != ordered[j].Priority {
			return ordered[i].Priority > ordered[j].Priority
		}
		if ordered[i].Node != ordered[j].Node {
			return ordered[i].Node < ordered[j].Node
		}
		return ordered[i].Dimension < ordered[j].Dimension
	})

	var guided []SearchCandidate
	if s.Enum != nil {
		for _, t := range ordered {
			for _, c := range s.Enum.Enumerate(t) {
				c.Source = SourceDiagnosisGuided
				if c.DiagnosisID == "" {
					c.DiagnosisID = t.DiagnosisID // guarantee the motivating diagnosis is recorded (task 1.3)
				}
				if c.Node == "" {
					c.Node = t.Node
				}
				if c.Dimension == "" {
					c.Dimension = t.Dimension
				}
				guided = append(guided, c)
			}
		}
	}
	// Within the guided set, order by expected gain (cheapest/highest-yield verified first), stable.
	sort.SliceStable(guided, func(i, j int) bool {
		if guided[i].ExpectedGain != guided[j].ExpectedGain {
			return guided[i].ExpectedGain > guided[j].ExpectedGain
		}
		if guided[i].Node != guided[j].Node {
			return guided[i].Node < guided[j].Node
		}
		return guided[i].ConfigHash < guided[j].ConfigHash
	})

	// Blind fallback (design Decision 2 / task 1.4): ONLY after the targeted set is exhausted, and only
	// while both the run budget and the separate blind sub-budget remain. Absent any of those, no blind
	// candidate is produced — the diagnosis-guided candidates are the whole plan.
	if !policy.TargetedExhausted || s.Blind == nil {
		return guided
	}
	if policy.BudgetRemaining <= 0 || policy.BlindBudgetRemaining <= 0 {
		return guided
	}
	blind := s.Blind.Expand(currentConfigHash, policy.BlindBudgetRemaining)
	for i := range blind {
		blind[i].Source = SourceBlind
		// A blind candidate's "motivating diagnosis" is the residual failure whose targeted candidates
		// were exhausted; the loop passes it through when it constructs the expander, but if unset we
		// leave it empty and the audit reads it as an un-attributed blind widen.
	}
	return append(guided, blind...)
}
