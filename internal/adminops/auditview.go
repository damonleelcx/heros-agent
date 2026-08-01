package adminops

import (
	"context"
	"errors"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// auditview.go is the read side of the audit chain — what the console's audit-log viewer renders
// (task 12.11) and what an auditor queries.
//
// It returns the chain's VERIFICATION alongside the entries, always. A viewer that shows rows without
// saying whether the chain they came from is intact is a viewer that would keep looking perfectly
// normal after somebody rewrote history, which defeats the point of hash-chaining it.

// AuditView is a page of audit entries plus the chain's integrity verdict.
type AuditView struct {
	Entries []adminaudit.Entry `json:"entries"`
	// Verification is the whole chain's integrity, recomputed on every read. Not cached: a cached
	// "intact" is a claim about the past.
	Verification adminaudit.Verification `json:"verification"`
	// Total is how many entries matched before the page limit, so the viewer can say "showing N of M"
	// rather than implying it showed everything.
	Total int `json:"total"`
	// MergeCoverage states which merge paths the chain records and which it does not (P26 task 2.7).
	MergeCoverage MergePathCoverage `json:"merge_coverage"`
}

// MergePathCoverage is the honest statement of what the hash chain holds about merges.
//
// # Why this had to be said out loud
//
// The chain mirrors every P6 AUTONOMOUS merge, via `mergeaudit.go`, on the same code path as the
// ledger append and with the same fail-closed consequence. That was complete when it was built,
// because autonomous merges were the only merges the platform was involved in. P12 then added
// customer-CI-mediated delivery: the pull request is opened by the platform and MERGED IN THE
// CUSTOMER'S CI, by the customer's own process, using a credential the platform does not hold.
//
// Nothing broke. What changed is what the surface IMPLIES: an audit log described as the record of
// "every action and every merge" now covers one of two merge paths, and the gap is invisible to the
// reader — which is the worst shape for a gap, because an auditor concluding "no record, so it did not
// happen" is drawing a false conclusion from a true page.
type MergePathCoverage struct {
	// Covered names the merge paths whose merges are mirrored into the chain, and by what.
	Covered []MergePath `json:"covered"`
	// NotCovered names the merge paths that are NOT in the chain, and says where they ARE readable —
	// a gap with a destination is a different thing from a gap.
	NotCovered []MergePath `json:"not_covered"`
	// Statement is the sentence the surface renders. One place, so the audit surface and the delivery
	// surface cannot describe the same boundary two ways.
	Statement string `json:"statement"`
}

// MergePath is one way a merge reaches a customer's repository.
type MergePath struct {
	// ID is the stable identifier. Prose changes; this does not.
	ID string `json:"id"`
	// Name is the operator-facing name.
	Name string `json:"name"`
	// Mechanism names the code that records it, or the reason it is not recorded here.
	Mechanism string `json:"mechanism"`
	// ReadableAt is the operator destination that DOES show this path, empty when the chain shows it.
	ReadableAt string `json:"readable_at,omitempty"`
}

// MergeCoverage reports the chain's merge-path coverage.
//
// A function rather than a variable so a caller cannot mutate the shared statement, and exported so
// the delivery surface links back to exactly the paths this names rather than restating them.
func MergeCoverage() MergePathCoverage {
	return MergePathCoverage{
		Covered: []MergePath{{
			ID:        "p6-autonomous-merge",
			Name:      "Autonomous merges by the P6 optimizer loop",
			Mechanism: "mirrored into the chain by internal/adminops/mergeaudit.go, on the same code path as the change-ledger append and fail-closed with it",
		}},
		NotCovered: []MergePath{{
			ID:         "p12-ci-mediated-delivery",
			Name:       "Customer-CI-mediated deliveries",
			Mechanism:  "the pull request is opened by the platform and merged by the CUSTOMER'S CI, with a forge credential the platform does not hold — so the merge is not an event this platform observes at the moment it happens",
			ReadableAt: "/delivery",
		}},
		Statement: "This chain records every ADMIN ACTION, and the merges the P6 loop performs itself. " +
			"It does NOT record customer-CI-mediated deliveries: those merge in the customer's own CI, " +
			"under a credential the platform does not hold. The absence of a delivery here is not " +
			"evidence that it did not happen — the delivery surface is where that path is read.",
	}
}

// AuditService serves the audit chain to authorized operators.
type AuditService struct {
	exec *Executor
}

// NewAuditService wires the viewer.
func NewAuditService(exec *Executor) (*AuditService, error) {
	if exec == nil {
		return nil, errors.New("adminops: the audit viewer needs the command path")
	}
	return &AuditService{exec: exec}, nil
}

// Entries returns the newest matching entries, most recent first, with the chain's verification.
func (s *AuditService) Entries(ctx context.Context, filter adminaudit.Filter, limit int) (AuditView, error) {
	if _, _, err := s.exec.Authorize(ctx, adminrbac.CapAuditRead, TargetGlobal); err != nil {
		return AuditView{}, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	matched := adminaudit.Select(s.exec.Audit(), filter)
	view := AuditView{Verification: s.exec.Audit().Verify(), Total: len(matched), MergeCoverage: MergeCoverage()}
	// Newest first: an operator opening the log is looking at what just happened.
	for i := len(matched) - 1; i >= 0 && len(view.Entries) < limit; i-- {
		view.Entries = append(view.Entries, matched[i])
	}
	return view, nil
}

// Verify recomputes the chain's integrity on demand — the console's "verify chain" control and the
// scheduled integrity check both call it.
func (s *AuditService) Verify(ctx context.Context) (adminaudit.Verification, error) {
	if _, _, err := s.exec.Authorize(ctx, adminrbac.CapAuditRead, TargetGlobal); err != nil {
		return adminaudit.Verification{}, err
	}
	return s.exec.Audit().Verify(), nil
}
