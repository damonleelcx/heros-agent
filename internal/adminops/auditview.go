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
	view := AuditView{Verification: s.exec.Audit().Verify(), Total: len(matched)}
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
