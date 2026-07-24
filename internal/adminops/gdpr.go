package adminops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// gdpr.go is compliance data deletion (FR17, design Decision 10): actionable from the console,
// verifiable afterwards, audited — and it does NOT break the append-only audit chain.
//
// # The tension, and how tombstoning resolves it
//
// "Erase this subject's data" and "the audit log is append-only and nothing may be removed from it"
// are in direct conflict if the audit log contains the subject's data. It does not, and that is by
// construction: an audit entry holds identifiers, a reason and a params DIGEST, never a parameter
// body (see adminaudit.Entry). So the erasure removes or tombstones the CONTENT in the stores that
// hold it, and the chain keeps a NON-PII tombstone reference recording that the erasure happened.
//
// The result: the content is gone, the fact of deletion is auditable, no audit entry is removed, and
// the chain still verifies. Any design that satisfied the erasure by deleting audit rows would make
// the compliance path the one route that can break tamper-evidence — which would defeat Decision 6.
//
// # Why the completion record is a recomputable digest
//
// "Verifiable" has to mean an auditor can check it, not that the platform asserts it. The completion
// record is a hash over the request's own facts, so anyone holding the request can recompute it and
// compare. A boolean "completed: true" would be a claim; this is evidence.

// GDPRStatus is a request's lifecycle state.
type GDPRStatus string

const (
	// GDPRReceived is a recorded request that has not been actioned.
	GDPRReceived GDPRStatus = "received"
	// GDPRExecuting is a request whose erasure is under way.
	GDPRExecuting GDPRStatus = "executing"
	// GDPRCompleted is an erasure that finished and produced a verification reference.
	GDPRCompleted GDPRStatus = "completed"
)

// GDPRRequest is one data-deletion request — the design's `gdpr_request`.
type GDPRRequest struct {
	RequestID string `json:"request_id"`
	// SubjectRef identifies the data subject. It is the ONE field that may carry a subject identifier,
	// and it never enters the audit chain — the chain carries TombstoneRef instead.
	SubjectRef string     `json:"subject_ref"`
	Status     GDPRStatus `json:"status"`
	Actor      string     `json:"actor"`
	Reason     string     `json:"reason"`
	// VerificationRef is the recomputable completion record.
	VerificationRef string `json:"verification_ref,omitempty"`
	// TombstoneRef is the NON-PII reference kept in the audit chain. It is derived from the subject
	// reference by a one-way hash, so the chain can prove which request it belongs to without
	// containing anything that identifies the subject.
	TombstoneRef string     `json:"tombstone_ref,omitempty"`
	RemovedCount int        `json:"removed_count"`
	CreatedAt    time.Time  `json:"created_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

// SubjectContentStore is the erasure surface over whatever holds a subject's content.
//
// It is an interface because P8 owns no tenant content: the stores that do (traces, memories, eval
// cases) implement this, and the console commands them. A deployment wires one implementation per
// store, or one facade over all of them.
type SubjectContentStore interface {
	// Tombstone removes or tombstones every record belonging to subjectRef and returns how many it
	// acted on. It must be idempotent: a re-run after a partial failure completes the erasure rather
	// than failing because some records are already gone.
	Tombstone(ctx context.Context, subjectRef string) (removed int, err error)
	// Remaining reports how many records still hold the subject's content. This is what makes the
	// completion VERIFIABLE rather than asserted — an auditor asks the store, not the console.
	Remaining(ctx context.Context, subjectRef string) (int, error)
	// Describe names the store for the completion record.
	Describe() string
}

// GDPR errors.
var (
	// ErrNoSuchGDPRRequest is returned for an unknown request id.
	ErrNoSuchGDPRRequest = errors.New("adminops: no such data-deletion request")
	// ErrErasureIncomplete means content remains after an erasure ran. The request is NOT marked
	// completed: reporting a partial erasure as done is the failure mode compliance cannot have.
	ErrErasureIncomplete = errors.New("adminops: content remains after the erasure — the request is not complete")
)

// TombstoneRef derives the NON-PII chain reference for a subject.
//
// A one-way hash, so an audit entry can name WHICH erasure it records without carrying anything that
// identifies the subject. Exported because an auditor holding the subject reference must be able to
// recompute it and find the entry.
func TombstoneRef(requestID, subjectRef string) string {
	h := sha256.New()
	h.Write([]byte("gdpr-tombstone-v1\n"))
	h.Write([]byte(requestID))
	h.Write([]byte{0})
	h.Write([]byte(subjectRef))
	return "tombstone:" + hex.EncodeToString(h.Sum(nil))[:32]
}

// VerificationRef derives the recomputable completion record.
func VerificationRef(req GDPRRequest, storeDescription string, completedAt time.Time) string {
	h := sha256.New()
	for _, part := range []string{
		"gdpr-completion-v1", req.RequestID, req.TombstoneRef, storeDescription,
		strconv.Itoa(req.RemovedCount), completedAt.UTC().Format(time.RFC3339Nano),
	} {
		h.Write([]byte(strconv.Itoa(len(part))))
		h.Write([]byte{':'})
		h.Write([]byte(part))
	}
	return "verification:" + hex.EncodeToString(h.Sum(nil))[:32]
}

// GDPRService actions data-deletion requests.
type GDPRService struct {
	exec    *Executor
	content SubjectContentStore
	now     func() time.Time

	mu       sync.RWMutex
	requests map[string]GDPRRequest
	seq      int
}

// NewGDPRService wires the service.
func NewGDPRService(exec *Executor, content SubjectContentStore) (*GDPRService, error) {
	if exec == nil || content == nil {
		return nil, errors.New("adminops: the compliance service needs the command path and a subject content store")
	}
	return &GDPRService{exec: exec, content: content, now: exec.Now, requests: map[string]GDPRRequest{}}, nil
}

// Record files a deletion request without actioning it — the intake step, so a request that arrives
// on a Friday is on the record before anybody executes it.
func (s *GDPRService) Record(ctx context.Context, subjectRef, reason string) (GDPRRequest, error) {
	if _, _, err := s.exec.Authorize(ctx, adminrbac.CapGDPRExecute, SubjectTarget(subjectRef)); err != nil {
		return GDPRRequest{}, err
	}
	if strings.TrimSpace(subjectRef) == "" {
		return GDPRRequest{}, errors.New("adminops: a deletion request must name its subject")
	}
	actor, _ := adminSessionFrom(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	req := GDPRRequest{
		RequestID: fmt.Sprintf("dsr-%04d", s.seq), SubjectRef: subjectRef, Status: GDPRReceived,
		Actor: actor, Reason: reason, CreatedAt: s.now(),
	}
	s.requests[req.RequestID] = req
	return req, nil
}

// List returns filed requests, newest first.
func (s *GDPRService) List(ctx context.Context) ([]GDPRRequest, error) {
	if _, _, err := s.exec.Authorize(ctx, adminrbac.CapGDPRExecute, TargetGlobal); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]GDPRRequest, 0, len(s.requests))
	for _, r := range s.requests {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequestID > out[j].RequestID })
	return out, nil
}

// Execute performs the erasure.
//
// Superadmin-only and irreversible, so the command path requires the operator to TYPE the target as a
// second confirmation. Everything after that is ordinary: write-ahead audit, effect, outcome entry —
// plus the non-PII tombstone entry that keeps the chain complete.
func (s *GDPRService) Execute(ctx context.Context, subjectRef, reason string, confirm Confirmation) (GDPRRequest, Receipt, error) {
	req, err := s.requestFor(ctx, subjectRef, reason)
	if err != nil {
		return GDPRRequest{}, Receipt{}, err
	}
	req.TombstoneRef = TombstoneRef(req.RequestID, req.SubjectRef)

	var completed GDPRRequest
	receipt, err := s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapGDPRExecute,
		Action:     adminaudit.ActionGDPRExecute,
		Target:     SubjectTarget(subjectRef),
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{req.RequestID},
		// The evidence carries the request id and the TOMBSTONE reference — never the subject
		// reference, which is the whole point: the chain is never deleted, so nothing identifying may
		// enter it.
		Evidence: map[string]string{
			"request_id": req.RequestID, "tombstone_ref": req.TombstoneRef, "store": s.content.Describe(),
		},
	}, func(ctx context.Context) (map[string]string, error) {
		s.setStatus(req.RequestID, GDPRExecuting)
		removed, err := s.content.Tombstone(ctx, req.SubjectRef)
		if err != nil {
			return nil, err
		}
		remaining, err := s.content.Remaining(ctx, req.SubjectRef)
		if err != nil {
			return nil, err
		}
		if remaining != 0 {
			// Not completed. A partial erasure reported as done is the compliance failure that matters.
			return map[string]string{"removed": strconv.Itoa(removed), "remaining": strconv.Itoa(remaining)},
				fmt.Errorf("%w: %d records remain", ErrErasureIncomplete, remaining)
		}
		now := s.now()
		req.RemovedCount = removed
		req.Status = GDPRCompleted
		req.CompletedAt = &now
		req.VerificationRef = VerificationRef(req, s.content.Describe(), now)

		s.mu.Lock()
		s.requests[req.RequestID] = req
		s.mu.Unlock()
		completed = req

		// The NON-PII tombstone entry: appended, never replacing anything, so the chain stays complete
		// and verifiable after the subject's content is gone (FR17).
		if _, terr := s.exec.Audit().Append(adminaudit.Entry{
			ActorAdminID: entryActor(ctx),
			Target:       req.TombstoneRef,
			Action:       adminaudit.ActionGDPRTombstone,
			Reason:       "data-subject erasure completed",
			Result:       ResultApplied,
			Evidence: map[string]string{
				"request_id": req.RequestID, "tombstone_ref": req.TombstoneRef,
				"verification_ref": req.VerificationRef, "removed": strconv.Itoa(removed),
			},
			CreatedAt: now,
		}); terr != nil {
			return nil, fmt.Errorf("adminops: erasure completed but its tombstone could not be recorded: %w", terr)
		}
		return map[string]string{
			"request_id": req.RequestID, "removed": strconv.Itoa(removed),
			"verification_ref": req.VerificationRef, "tombstone_ref": req.TombstoneRef,
		}, nil
	})
	if err != nil {
		return req, receipt, err
	}
	return completed, receipt, nil
}

// VerificationReport is the recomputed proof that an erasure completed.
type VerificationReport struct {
	RequestID string `json:"request_id"`
	// Completed is the request's recorded status.
	Completed bool `json:"completed"`
	// ContentRemaining is what the content store says RIGHT NOW. Zero is the only acceptable answer
	// for a completed request, and it is asked of the store rather than read from the request.
	ContentRemaining int `json:"content_remaining"`
	// VerificationRefMatches is true when the stored completion record recomputes to the same value —
	// evidence rather than assertion.
	VerificationRefMatches bool `json:"verification_ref_matches"`
	// TombstoneInChain is true when the non-PII tombstone entry is present in the audit chain.
	TombstoneInChain bool `json:"tombstone_in_chain"`
	// ChainIntact is the whole audit chain's integrity after the erasure.
	ChainIntact bool   `json:"chain_intact"`
	Detail      string `json:"detail,omitempty"`
}

// Verified reports whether every check passed.
func (r VerificationReport) Verified() bool {
	return r.Completed && r.ContentRemaining == 0 && r.VerificationRefMatches &&
		r.TombstoneInChain && r.ChainIntact
}

// Verify recomputes an erasure's completion evidence.
func (s *GDPRService) Verify(ctx context.Context, requestID string) (VerificationReport, error) {
	if _, _, err := s.exec.Authorize(ctx, adminrbac.CapGDPRExecute, TargetGlobal); err != nil {
		return VerificationReport{}, err
	}
	s.mu.RLock()
	req, ok := s.requests[requestID]
	s.mu.RUnlock()
	if !ok {
		return VerificationReport{}, fmt.Errorf("%w: %s", ErrNoSuchGDPRRequest, requestID)
	}
	rep := VerificationReport{RequestID: requestID, Completed: req.Status == GDPRCompleted}

	remaining, err := s.content.Remaining(ctx, req.SubjectRef)
	if err != nil {
		rep.Detail = "the content store could not be read: " + err.Error()
		return rep, nil
	}
	rep.ContentRemaining = remaining

	if req.CompletedAt != nil {
		rep.VerificationRefMatches = req.VerificationRef == VerificationRef(req, s.content.Describe(), *req.CompletedAt)
	}
	for _, e := range s.exec.Audit().Entries() {
		if e.Action == adminaudit.ActionGDPRTombstone && e.Evidence["request_id"] == requestID {
			rep.TombstoneInChain = true
			break
		}
	}
	rep.ChainIntact = s.exec.Audit().Verify().Intact
	return rep, nil
}

// requestFor finds an existing open request for the subject, or files one.
func (s *GDPRService) requestFor(ctx context.Context, subjectRef, reason string) (GDPRRequest, error) {
	s.mu.RLock()
	for _, r := range s.requests {
		if r.SubjectRef == subjectRef && r.Status != GDPRCompleted {
			s.mu.RUnlock()
			return r, nil
		}
	}
	s.mu.RUnlock()
	return s.Record(ctx, subjectRef, reason)
}

func (s *GDPRService) setStatus(requestID string, status GDPRStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.requests[requestID]; ok {
		r.Status = status
		s.requests[requestID] = r
	}
}

// entryActor reads the acting admin for an audit entry written outside the command path.
func entryActor(ctx context.Context) string {
	if id, ok := adminSessionFrom(ctx); ok {
		return id
	}
	return adminaudit.ActorSystem
}

// ── Reference content store ─────────────────────────────────────────────────────────────────────

// MemSubjectStore is the reference SubjectContentStore: records keyed by subject, tombstoned in place.
//
// It tombstones rather than dropping rows, which is the shape a real store takes — a foreign key from
// elsewhere must still resolve, so the row survives with its content removed and a marker in its
// place. Remaining counts records that still HOLD content, which is what an auditor asks about.
type MemSubjectStore struct {
	mu sync.Mutex
	// records maps subject → the records holding their content. A tombstoned record stays in the map
	// with an empty body.
	records map[string][]subjectRecord
}

type subjectRecord struct {
	id         string
	body       string
	tombstoned bool
}

// NewMemSubjectStore builds an empty store.
func NewMemSubjectStore() *MemSubjectStore {
	return &MemSubjectStore{records: map[string][]subjectRecord{}}
}

// Put adds one content record for a subject.
func (s *MemSubjectStore) Put(subjectRef, recordID, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[subjectRef] = append(s.records[subjectRef], subjectRecord{id: recordID, body: body})
}

// Tombstone implements SubjectContentStore. Idempotent: a re-run tombstones what remains.
func (s *MemSubjectStore) Tombstone(_ context.Context, subjectRef string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	recs := s.records[subjectRef]
	for i := range recs {
		if !recs[i].tombstoned {
			recs[i].body = ""
			recs[i].tombstoned = true
			n++
		}
	}
	s.records[subjectRef] = recs
	return n, nil
}

// Remaining implements SubjectContentStore.
func (s *MemSubjectStore) Remaining(_ context.Context, subjectRef string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.records[subjectRef] {
		if !r.tombstoned {
			n++
		}
	}
	return n, nil
}

// Body returns a record's content, so a test can assert the content is genuinely gone rather than
// trusting a counter.
func (s *MemSubjectStore) Body(subjectRef, recordID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.records[subjectRef] {
		if r.id == recordID {
			return r.body, true
		}
	}
	return "", false
}

// Describe implements SubjectContentStore.
func (s *MemSubjectStore) Describe() string { return "memory:subject-content" }
