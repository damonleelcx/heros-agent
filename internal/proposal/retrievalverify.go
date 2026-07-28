package proposal

import (
	"errors"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/evalstats"
)

// Held-out verification for a retrieval change (P16 task 6.3, design.md Decision 4, FR11/NFR7)
// ────────────────────────────────────────────────────────────────────────────────────────────
//
// Retrieval is where a "win" is easiest to fake. Top-k, chunk size and embedding model are knobs a
// search can be run over, and a search run against an eval set will find the setting that scores best
// ON THAT SET. Reporting that number as a verified delta is overfit sold as a result — indefensible the
// first time it regresses on a customer's real traffic, and "we tuned and verified on the same data" is
// not a defensible sentence.
//
// So the verdict is computed on a HELD-OUT set, disjoint from the set the parameters were selected on.
//
// # Disjoint BY CONSTRUCTION, and refused when it is not
//
// `DeriveRetrievalSplit` builds the partition from `config_hash` + case ids, so every case lands in
// exactly one bucket and the split re-derives identically months later. That is the intended path, and
// it makes overlap impossible.
//
// But a caller may bring its own split — an authored suite, a customer's own tuning/holdout files — and
// that is where the guarantee has to bite: an intersecting split is REFUSED, with the offending case
// ids named. Refusing is the whole point. A verification that quietly de-duplicated the overlap would
// still report a number, and the number would be exactly the overfit one.
//
// # Why this is not the P13 downgrade guardrail
//
// Both judge on held-out cases; they answer different questions. The downgrade guardrail asks "can the
// platform tell these two models apart?" and admits on OVERLAP (an equal-quality-cheaper tie). This
// asks "did retrieval actually get better?" and requires SEPARATION — an overlap here is a tie, and a
// tie is not a win. Sharing one function would mean one predicate answering two questions, and the
// answer would be wrong for one of them.

// ErrOverlappingSplit is returned when a tuning set and a held-out set intersect. It is a refusal, not
// a degraded verdict: there is no honest number to report from an overlapping split.
var ErrOverlappingSplit = errors.New("proposal: the tuning set and the held-out set intersect")

// RetrievalSplit is the partition a retrieval change is judged on: the cases its parameters were
// SELECTED on, and the disjoint cases the verdict is computed on.
type RetrievalSplit struct {
	Tuning  []string
	HeldOut []string
}

// DeriveRetrievalSplit builds a disjoint tuning/held-out partition from the configuration hash and the
// case ids. Disjoint by construction, reproducible, and independent of input order — it reuses the same
// per-case derivation the P13 guardrail uses, so a platform that already re-derives one split does not
// acquire a second, differently-behaving one.
func DeriveRetrievalSplit(configHash string, caseIDs []string) RetrievalSplit {
	s := HeldOutSplit(configHash, caseIDs)
	return RetrievalSplit{Tuning: s.Motivating, HeldOut: s.HeldOut}
}

// Overlap returns the case ids present in BOTH halves, sorted. Empty means the split is disjoint.
func (s RetrievalSplit) Overlap() []string {
	in := map[string]bool{}
	for _, id := range s.Tuning {
		in[id] = true
	}
	var both []string
	seen := map[string]bool{}
	for _, id := range s.HeldOut {
		if in[id] && !seen[id] {
			seen[id] = true
			both = append(both, id)
		}
	}
	sort.Strings(both)
	return both
}

// RetrievalVerdict is the outcome of verifying one retrieval change.
type RetrievalVerdict struct {
	// Verified is true ONLY for a separated win on the held-out set. A tie is not a win and an
	// insufficient held-out set is not a verdict.
	Verified bool
	// HeldOut is the case set the verdict was computed over — carried so a reader can check that the
	// number came from cases the tuning never saw.
	HeldOut []string
	// Base and Candidate are the task-success intervals on the HELD-OUT cases. Zero-valued when the
	// verdict could not be computed.
	Base      evalstats.Interval
	Candidate evalstats.Interval
	// Reason states, in words a UI can render verbatim, why the verdict is what it is.
	Reason string
}

// VerifyRetrievalChange computes the held-out verdict for a retrieval change.
//
// `base` and `candidate` are task_success series over the same case universe under multiple seeds. Only
// the held-out slice is judged: a win measured on the tuning set is not reported at all, because it is
// not evidence about anything the tuning did not already see.
func VerifyRetrievalChange(split RetrievalSplit, base, candidate evalstats.Series, cfg evalstats.Config) (RetrievalVerdict, error) {
	if overlap := split.Overlap(); len(overlap) > 0 {
		// 🔴 The refusal. Not a warning, not a de-duplication — there is no honest number to compute from
		// a split whose halves share cases, and computing one anyway is precisely how overfit is sold as
		// a result.
		return RetrievalVerdict{}, fmt.Errorf("%w: %v appear in both halves, so a verdict computed here "+
			"would be measured partly on the cases the parameters were selected on — that is not a verified "+
			"delta and is refused rather than reported", ErrOverlappingSplit, overlap)
	}
	if len(split.HeldOut) < MinHeldOutCases {
		return RetrievalVerdict{
			HeldOut: split.HeldOut,
			Reason: fmt.Sprintf("the held-out set has %d case(s), below the floor of %d; there is not enough "+
				"unseen data to support a verdict, and 'not enough data' is reported rather than treated as a pass",
				len(split.HeldOut), MinHeldOutCases),
		}, nil
	}

	keep := map[string]bool{}
	for _, id := range split.HeldOut {
		keep[id] = true
	}
	bi, errB := evalstats.Aggregate(filterSeries(base, keep), cfg)
	ci, errC := evalstats.Aggregate(filterSeries(candidate, keep), cfg)
	if errB != nil || errC != nil {
		return RetrievalVerdict{
			HeldOut: split.HeldOut,
			Reason: "held-out task-success could not be aggregated to a discriminating interval " +
				"(under-seeded or empty); no verdict is reported",
		}, nil
	}

	v := RetrievalVerdict{HeldOut: split.HeldOut, Base: bi, Candidate: ci}
	switch {
	case ci.Low > bi.High:
		v.Verified = true
		v.Reason = fmt.Sprintf("held-out task-success improved with separated intervals (%s → %s); the "+
			"change is a verified delta on cases its parameters never saw", fmtCI(bi), fmtCI(ci))
	case ci.Overlaps(bi):
		v.Reason = fmt.Sprintf("held-out task-success intervals overlap (%s vs %s); the platform cannot "+
			"tell the two apart on unseen cases, so this is a tie and a tie is not a win", fmtCI(bi), fmtCI(ci))
	default:
		v.Reason = fmt.Sprintf("held-out task-success REGRESSED (%s → %s); the change is rejected",
			fmtCI(bi), fmtCI(ci))
	}
	return v, nil
}

// RequiresHeldOutVerification reports whether an operator's candidates must clear held-out verification
// before their delta may be presented as verified (task 6.3).
//
// Retrieval tuning is the case: its knobs are searchable, so a win on the set they were searched over
// carries no information about anything else. It is stated as a predicate, next to
// RequiresHeldOutGuardrail, so the two held-out rules live side by side and neither can be mistaken for
// the other.
func RequiresHeldOutVerification(op OperatorKind) bool { return op == OpRAGTune }
