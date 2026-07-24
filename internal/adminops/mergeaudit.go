package adminops

import (
	"errors"
	"fmt"
	"sync"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/optimizer"
)

// mergeaudit.go mirrors every P6 autonomous merge into the tamper-evident audit chain (FR16).
//
// # Why the audit log has to see merges at all
//
// P8's audit log answers "who changed what across this platform". The most consequential actor on the
// platform is not a human operator — it is the P6 loop, which opens and merges pull requests into
// customer repositories without anyone clicking anything. An oversight record that captured every
// admin click and none of the fleet's merges would be an audit of the least dangerous actor.
//
// # Why this decorates the change ledger rather than polling it
//
// The P6 change ledger is already the write-ahead record the loop cannot merge without. Wrapping it
// means the audit entry is written on the SAME code path, in the same order, with the same fail-closed
// consequence: if the audit chain cannot take the entry, the ledger append fails, and the loop treats
// that exactly as it treats its own ledger being down — it does not merge. A poller would produce a
// second, lagging copy that is silently incomplete after a crash.

// AuditingLedger wraps a P6 change ledger and mirrors its merge events into the audit chain.
type AuditingLedger struct {
	inner optimizer.ChangeLedger
	audit adminaudit.Store
	// tenantOf resolves a run to the tenant it belongs to, so a merge entry is attributable. Nil
	// yields the global target, which is honest for a single-tenant deployment.
	tenantOf func(runID string) string

	mu sync.Mutex
	// pending remembers the apply events awaiting their merge commit, keyed by {run, seq}, so the
	// completion entry can carry the diagnosis and the verified delta the apply event named.
	pending map[string]optimizer.LedgerEvent
}

// NewAuditingLedger wraps inner.
func NewAuditingLedger(inner optimizer.ChangeLedger, audit adminaudit.Store, tenantOf func(runID string) string) (*AuditingLedger, error) {
	if inner == nil || audit == nil {
		return nil, errors.New("adminops: the auditing ledger needs the P6 change ledger and the audit store")
	}
	return &AuditingLedger{inner: inner, audit: audit, tenantOf: tenantOf, pending: map[string]optimizer.LedgerEvent{}}, nil
}

func pendingKey(runID string, seq int) string { return fmt.Sprintf("%s#%d", runID, seq) }

// target resolves the audit target for a run.
func (l *AuditingLedger) target(runID string) string {
	if l.tenantOf == nil {
		return TargetGlobal
	}
	if tenant := l.tenantOf(runID); tenant != "" {
		return TenantTarget(tenant)
	}
	return TargetGlobal
}

// Append records the event in the P6 ledger and, for a merge, in the audit chain.
//
// The AUDIT entry goes first. If the chain refuses it, nothing is written to the P6 ledger either and
// the error the loop sees is ErrLedgerUnavailable — which it already handles by not merging. That
// ordering means an unauditable merge cannot happen, rather than happening and being unrecorded.
func (l *AuditingLedger) Append(ev optimizer.LedgerEvent) (int, error) {
	if ev.Type != optimizer.EventApply {
		return l.inner.Append(ev)
	}
	entry := adminaudit.Entry{
		ActorAdminID: adminaudit.ActorSystem,
		Target:       l.target(ev.RunID),
		Action:       adminaudit.ActionAutonomousMerge,
		Reason:       ev.Summary,
		Result:       ResultAttempted,
		Evidence: map[string]string{
			"run_id": ev.RunID, "diagnosis_id": ev.DiagnosisID, "verified_delta": ev.Summary,
			"from_config_hash": ev.FromConfigHash, "to_config_hash": ev.ToConfigHash, "pr_ref": ev.PRRef,
			"loop_actor": ev.Actor,
		},
		CreatedAt: ev.TS,
	}
	if _, err := l.audit.Append(entry); err != nil {
		// Reported as the ledger being unavailable, because that is the condition the loop knows how to
		// fail closed on — and it is true: the write-ahead record could not be completed.
		return 0, fmt.Errorf("%w: audit chain refused the merge record: %v", optimizer.ErrLedgerUnavailable, err)
	}
	seq, err := l.inner.Append(ev)
	if err != nil {
		return seq, err
	}
	l.mu.Lock()
	l.pending[pendingKey(ev.RunID, seq)] = ev
	l.mu.Unlock()
	return seq, nil
}

// Backfill records the merge commit on the ledger event and appends the COMPLETE audit entry — the one
// carrying the motivating diagnosis, the verified delta and the merge commit together (FR16).
func (l *AuditingLedger) Backfill(runID string, seq int, mergeCommit string) error {
	if err := l.inner.Backfill(runID, seq, mergeCommit); err != nil {
		return err
	}
	l.mu.Lock()
	ev, ok := l.pending[pendingKey(runID, seq)]
	delete(l.pending, pendingKey(runID, seq))
	l.mu.Unlock()
	if !ok {
		// A backfill for an apply this wrapper never saw. Recorded anyway with what is known: an
		// unattributable merge is still a merge, and dropping it would be the silent omission FR16
		// forbids.
		ev = optimizer.LedgerEvent{RunID: runID}
	}
	_, err := l.audit.Append(adminaudit.Entry{
		ActorAdminID: adminaudit.ActorSystem,
		Target:       l.target(runID),
		Action:       adminaudit.ActionAutonomousMerge,
		Reason:       ev.Summary,
		Result:       ResultApplied,
		Evidence: map[string]string{
			"run_id": runID, "diagnosis_id": ev.DiagnosisID, "verified_delta": ev.Summary,
			"from_config_hash": ev.FromConfigHash, "to_config_hash": ev.ToConfigHash,
			"pr_ref": ev.PRRef, "merge_commit": mergeCommit,
		},
		CreatedAt: ev.TS,
	})
	if err != nil {
		// The merge has already happened; the chain still holds the write-ahead entry, so it is not
		// invisible. Returning the error surfaces the integrity degradation rather than hiding it.
		return fmt.Errorf("adminops: merge %s was performed but its completion could not be audited: %w", mergeCommit, err)
	}
	return nil
}

// Events delegates to the wrapped ledger.
func (l *AuditingLedger) Events(runID string) []optimizer.LedgerEvent { return l.inner.Events(runID) }
