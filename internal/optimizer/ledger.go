package optimizer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// EventType is one kind of change-ledger event (design's change_ledger_event.type). Every decision the
// loop makes is one of these — the append-only sequence IS the audit trail's "why", complementing git
// history's "what" (spec Requirement "The audit trail SHALL be git history plus an append-only change
// ledger"). Stable strings: a ledger is replayed years later.
type EventType string

const (
	EventGrant    EventType = "grant"    // authority granted with its constraints
	EventConsider EventType = "consider" // a candidate was considered (+ its motivating diagnosis)
	EventVerify   EventType = "verify"   // a verification verdict (delta, CI, significance, regression)
	EventApply    EventType = "apply"    // a PR was opened AND merged (the write-ahead record)
	EventHalt     EventType = "halt"     // a regression/budget halt disarmed the merge step
	EventStop     EventType = "stop"     // the run stopped (kill switch / converged / max_iter / stalled)
	EventRevert   EventType = "revert"   // a merged change was reverted via git revert
	EventRearm    EventType = "rearm"    // a human re-armed the merge step after a halt
	EventIngest   EventType = "ingest"   // a production-failure trace became a new eval case
	// EventEntitlementDenied records that the P7 entitlement gate refused a merge and the loop fell back
	// to opening a pull request for a human. It is a first-class ledger event rather than a log line
	// because it changed what the loop DID to the customer's repository, and "why was this verified
	// candidate not merged" must be answerable from the trail years later.
	EventEntitlementDenied EventType = "entitlement_denied"
)

// LedgerEvent is one append-only row. It is tagged with the P0 tag set (config_hash, variant_id,
// run_id, timestamp) and carries only HASHES and git refs — never inline prompts, keys, or PII
// (task 5.5: the loop writes no secrets into the ledger; large payloads are content-addressed to the
// object store and referenced by PayloadHash).
type LedgerEvent struct {
	RunID     string    `json:"run_id"`
	Seq       int       `json:"seq"`
	Type      EventType `json:"type"`
	Actor     string    `json:"actor"`
	VariantID string    `json:"variant_id,omitempty"`
	// FromConfigHash / ToConfigHash bound a change: an apply moves from→to, a revert moves back.
	FromConfigHash string `json:"from_config_hash,omitempty"`
	ToConfigHash   string `json:"to_config_hash,omitempty"`
	DiagnosisID    string `json:"diagnosis_id,omitempty"`
	PRRef          string `json:"pr_ref,omitempty"`
	MergeCommit    string `json:"merge_commit,omitempty"`
	// PayloadHash is the content hash of the full decision payload (verdict blob, candidate spec, trace)
	// in the object store. The ledger row stays small and secret-free; the payload is fetched by hash.
	PayloadHash string `json:"payload_hash,omitempty"`
	// Summary is a short, secret-free human label ("held-out gain +0.42, regression-clean"). It never
	// carries a prompt body, a key, or case content — only measured numbers and stable identifiers.
	Summary string    `json:"summary,omitempty"`
	TS      time.Time `json:"ts"`
}

// ErrLedgerUnavailable is what a fail-closed ledger returns. The loop treats it as "cannot merge":
// with the write-ahead ledger down, a merge would be unauditable, so it must not happen (design
// Decision 4, spec Requirement "The change-ledger store being unavailable prevents the merge").
var ErrLedgerUnavailable = errors.New("optimizer: change-ledger store unavailable")

// ChangeLedger is the append-only audit store. Append MUST be durable before it returns (the
// write-ahead guarantee depends on it): a merge only proceeds after its apply event has been committed
// here. A store that cannot append returns an error, and the loop fails closed.
type ChangeLedger interface {
	// Append durably records ev (assigning its Seq) and returns the assigned sequence number. It returns
	// an error if the store is unavailable — the loop must treat that as "do not merge".
	Append(ev LedgerEvent) (seq int, err error)
	// Backfill records the merge commit onto an already-committed apply event (design Decision 4's "the
	// merge commit ref is written back to the ledger"). It never creates a merge or a new decision — the
	// write-ahead apply event already exists; this only completes it with the git ref once the merge
	// succeeds. A no-op miss (unknown seq) is not an error.
	Backfill(runID string, seq int, mergeCommit string) error
	// Events returns every event for a run in sequence order (a copy — the caller may not mutate it).
	Events(runID string) []LedgerEvent
}

// MemLedger is an in-memory append-only ChangeLedger for the loop's default path and the tests. It is
// concurrency-safe and content-addresses payloads through Put/PayloadHash. It never stores a payload
// inline in an event — only the hash — which is what keeps the audit trail secret-free (task 5.5).
type MemLedger struct {
	mu       sync.Mutex
	byRun    map[string][]LedgerEvent
	seq      int
	payloads map[string][]byte
	// down, when true, makes Append fail — the fail-closed test seam (task 6.3).
	down bool
}

// NewMemLedger builds an empty in-memory ledger.
func NewMemLedger() *MemLedger {
	return &MemLedger{byRun: map[string][]LedgerEvent{}, payloads: map[string][]byte{}}
}

// SetDown flips the store between available and unavailable, for the fail-closed degradation test
// (task 6.3 / 8.7): kill the change-ledger store mid-run and the loop must stop merging.
func (l *MemLedger) SetDown(down bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.down = down
}

// Append records ev with the next sequence number, or fails if the store is down.
func (l *MemLedger) Append(ev LedgerEvent) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.down {
		return 0, ErrLedgerUnavailable
	}
	l.seq++
	ev.Seq = l.seq
	if ev.TS.IsZero() {
		// The loop always stamps TS from its injected clock; this is a defensive fallback only.
		ev.TS = time.Unix(0, 0).UTC()
	}
	l.byRun[ev.RunID] = append(l.byRun[ev.RunID], ev)
	return ev.Seq, nil
}

// Backfill sets the merge commit on the apply event with the given seq, once the merge has succeeded.
func (l *MemLedger) Backfill(runID string, seq int, mergeCommit string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.down {
		return ErrLedgerUnavailable
	}
	for i := range l.byRun[runID] {
		if l.byRun[runID][i].Seq == seq {
			l.byRun[runID][i].MergeCommit = mergeCommit
			return nil
		}
	}
	return nil
}

// Events returns a copy of a run's events in sequence order.
func (l *MemLedger) Events(runID string) []LedgerEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := append([]LedgerEvent(nil), l.byRun[runID]...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// Put content-addresses a payload (a verdict blob, a candidate spec, a production trace) and returns
// its hash, so a ledger event can reference it without inlining it. Idempotent by content.
func (l *MemLedger) Put(payload []byte) string {
	h := ContentHash(payload)
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.payloads[h]; !ok {
		l.payloads[h] = append([]byte(nil), payload...)
	}
	return h
}

// Get returns a content-addressed payload by hash.
func (l *MemLedger) Get(hash string) ([]byte, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.payloads[hash]
	return append([]byte(nil), p...), ok
}

// ContentHash is the content-addressing primitive: SHA-256 hex of the bytes. Payloads are referenced by
// this hash so the ledger never inlines prompts/keys/PII (task 5.5).
func ContentHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
