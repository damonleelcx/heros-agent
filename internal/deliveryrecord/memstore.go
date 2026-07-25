// Package deliveryrecord is the append-only P12 delivery record: what was delivered where, in which
// mode, and its lifecycle state (opened → updated → superseded / closed / merged → possibly reverted).
//
// It implements forgedelivery.Recorder. Two backings share ONE set of invariants:
//
//   - MemStore — the in-memory default/dev/demo path (this file). Faithful to the durable schema's two
//     load-bearing rules: APPEND-ONLY (no mutate, no delete) and AT MOST ONE 'opened' row per delivery
//     id (the partial unique index). A test double that did not enforce these would prove nothing.
//   - the Postgres `delivery` table (0015_p12_delivery), whose constraints are proven live in
//     schema_pgproof_test.go and enforce the SAME two rules BY CONSTRUCTION.
//
// `transform` is never touched by either — a delivery is a different, lifecycle-bearing fact (ADR-005).
package deliveryrecord

import (
	"context"
	"sort"
	"sync"

	"github.com/heros-foreal/agentd/internal/forgedelivery"
)

// MemStore is the in-memory delivery record. It appends entries and never mutates or deletes one, so it
// behaves exactly as the durable table's append-only trigger requires.
type MemStore struct {
	mu      sync.Mutex
	entries []forgedelivery.Entry
	nextSeq int64
	// openByDelivery tracks which delivery ids currently hold an 'opened' row, so a second 'opened'
	// append is rejected exactly as the partial unique index rejects it.
	openByDelivery map[string]bool
	// unreachable makes every operation fail, to exercise the store-unavailable path (isolation tests).
	unreachable bool
}

// NewMemStore builds an empty store.
func NewMemStore() *MemStore {
	return &MemStore{nextSeq: 1, openByDelivery: map[string]bool{}}
}

// SetUnreachable takes the store offline (or brings it back), for isolation/fault tests.
func (s *MemStore) SetUnreachable(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unreachable = down
}

// Append implements forgedelivery.Recorder. It enforces the partial-unique 'opened' rule and returns
// forgedelivery.ErrOpenConflict when a second 'opened' collides — the same signal the database gives.
func (s *MemStore) Append(ctx context.Context, e forgedelivery.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unreachable {
		return errUnreachable
	}
	if e.State == forgedelivery.StateOpened && s.openByDelivery[e.DeliveryID] {
		return forgedelivery.ErrOpenConflict
	}
	e.Seq = s.nextSeq
	s.nextSeq++
	s.entries = append(s.entries, e)
	if e.State == forgedelivery.StateOpened {
		s.openByDelivery[e.DeliveryID] = true
	}
	return nil
}

// Head implements forgedelivery.Recorder: the latest entry for a delivery, projected to a head.
func (s *MemStore) Head(ctx context.Context, deliveryID string) (forgedelivery.DeliveryHead, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unreachable {
		return forgedelivery.DeliveryHead{}, false, errUnreachable
	}
	return s.headLocked(deliveryID)
}

func (s *MemStore) headLocked(deliveryID string) (forgedelivery.DeliveryHead, bool, error) {
	var latest forgedelivery.Entry
	found := false
	for _, e := range s.entries {
		if e.DeliveryID == deliveryID && (!found || e.Seq > latest.Seq) {
			latest = e
			found = true
		}
	}
	if !found {
		return forgedelivery.DeliveryHead{}, false, nil
	}
	return headOf(latest), true, nil
}

// OpenForTarget implements forgedelivery.Recorder: the heads of deliveries whose latest state is still
// open, for a (tenant, target).
func (s *MemStore) OpenForTarget(ctx context.Context, tenantID, target string) ([]forgedelivery.DeliveryHead, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unreachable {
		return nil, errUnreachable
	}
	ids := s.deliveryIDsLocked(func(e forgedelivery.Entry) bool {
		return e.TenantID == tenantID && e.Target == target
	})
	var out []forgedelivery.DeliveryHead
	for _, id := range ids {
		h, ok, _ := s.headLocked(id)
		if ok && h.Open() {
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeliveryID < out[j].DeliveryID })
	return out, nil
}

// History implements forgedelivery.Recorder: every entry for a delivery in append order.
func (s *MemStore) History(ctx context.Context, deliveryID string) ([]forgedelivery.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unreachable {
		return nil, errUnreachable
	}
	var out []forgedelivery.Entry
	for _, e := range s.entries {
		if e.DeliveryID == deliveryID {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// ListForTenant implements forgedelivery.Recorder: the head of every delivery for a tenant, newest
// first.
func (s *MemStore) ListForTenant(ctx context.Context, tenantID string) ([]forgedelivery.DeliveryHead, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unreachable {
		return nil, errUnreachable
	}
	ids := s.deliveryIDsLocked(func(e forgedelivery.Entry) bool { return e.TenantID == tenantID })
	type headSeq struct {
		head forgedelivery.DeliveryHead
		seq  int64
	}
	var hs []headSeq
	for _, id := range ids {
		h, ok, _ := s.headLocked(id)
		if !ok {
			continue
		}
		var maxSeq int64
		for _, e := range s.entries {
			if e.DeliveryID == id && e.Seq > maxSeq {
				maxSeq = e.Seq
			}
		}
		hs = append(hs, headSeq{head: h, seq: maxSeq})
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].seq > hs[j].seq })
	out := make([]forgedelivery.DeliveryHead, 0, len(hs))
	for _, x := range hs {
		out = append(out, x.head)
	}
	return out, nil
}

// deliveryIDsLocked returns the distinct delivery ids among entries matching pred, in first-seen order.
func (s *MemStore) deliveryIDsLocked(pred func(forgedelivery.Entry) bool) []string {
	seen := map[string]bool{}
	var ids []string
	for _, e := range s.entries {
		if pred(e) && !seen[e.DeliveryID] {
			seen[e.DeliveryID] = true
			ids = append(ids, e.DeliveryID)
		}
	}
	return ids
}

func headOf(e forgedelivery.Entry) forgedelivery.DeliveryHead {
	return forgedelivery.DeliveryHead{
		DeliveryID: e.DeliveryID, TenantID: e.TenantID, ConfigHash: e.ConfigHash,
		SourceRevision: e.SourceRevision, Target: e.Target, ForgeRef: e.ForgeRef, Mode: e.Mode,
		State: e.State, Reason: e.Reason, MergeCommit: e.MergeCommit,
	}
}
