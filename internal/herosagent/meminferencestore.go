package herosagent

import (
	"context"
	"fmt"
	"sync"
)

// meminferencestore.go is the InferenceStore for a runner with no platform database — the customer-side
// one (task 7.1), and tests.
//
// # Why the customer side gets a store at all, when its answer is submitted rather than kept
//
// `NewRunner` refuses a nil store on purpose: "D2's guarantee is a property of the store, and a runner
// without one re-infers on every read". That argument does not weaken on the customer's machine — it is
// the same runner, and a nil store there would mean the ONE runner type had two behaviours depending on
// which constructor was used, which is the divergence D6 is about.
//
// 🔴 What it deliberately does NOT do is persist across CLI invocations. A second `heros analyse` on the
// same revision runs the model again and pays for it. That is a real cost, and it is the honest one: a
// durable local cache would make the customer's machine a second source of truth for a key whose whole
// promise is that the PLATFORM's stored answer is what a revision resolves to. Two caches for one key,
// one of them invisible to the platform, is how "the same revision always shows you the same graph"
// becomes false without anything failing.

// MemInferenceStore keeps inferences for the lifetime of a process.
//
// Concurrency-safe like MemVersionStore, and for the same reason: a store that races only under load is
// a store that passes every test.
type MemInferenceStore struct {
	mu sync.RWMutex
	m  map[[3]string]Stored
}

// NewMemInferenceStore returns an empty in-memory inference store.
func NewMemInferenceStore() *MemInferenceStore {
	return &MemInferenceStore{m: map[[3]string]Stored{}}
}

func key(workflowID, sourceRevision, hash string) [3]string {
	return [3]string{workflowID, sourceRevision, hash}
}

// Get reads by the three-part key. ok=false is NOT INFERRED, never an error.
func (s *MemInferenceStore) Get(_ context.Context, workflowID, sourceRevision, hash string) (Stored, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.m[key(workflowID, sourceRevision, hash)]
	return st, ok, nil
}

// Put is idempotent on the key and 🚫 never overwrites — the same contract the Postgres store's
// `ON CONFLICT DO NOTHING` gives, so a test cannot pass here on behaviour the database refuses.
func (s *MemInferenceStore) Put(_ context.Context, st Stored) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(st.WorkflowID, st.SourceRevision, st.AgentConfigHash)
	if _, exists := s.m[k]; exists {
		return nil
	}
	s.m[k] = st
	return nil
}

// Replace overwrites after a confirmed re-inference.
func (s *MemInferenceStore) Replace(_ context.Context, st Stored) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key(st.WorkflowID, st.SourceRevision, st.AgentConfigHash)] = st
	return nil
}

// MarkStale marks every inference for one tenant, and returns how many it marked (task 9.5).
//
// 🔴 IT MARKS. It does not delete, and the count it returns is how a caller proves that — a version
// that removed rows would return the same number while leaving nothing to read.
//
// An already-stale row keeps its FIRST reason and timestamp. "Since when did this stop being
// maintained" is the question the timestamp answers, and the first cause is when maintenance actually
// stopped; re-marking would move the answer forward every time something else changed.
func (s *MemInferenceStore) MarkStale(_ context.Context, tenantID string, reason StaleReason, atMS int64) (int64, error) {
	if SentenceForStale(reason) == "" {
		return 0, fmt.Errorf("%w: %q is not a stale reason", ErrInvalidDefinition, reason)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for k, st := range s.m {
		if st.TenantID != tenantID || st.StaleReason != "" {
			continue
		}
		st.StaleReason, st.StaleAtMS = reason, atMS
		s.m[k] = st
		n++
	}
	return n, nil
}

// ClearStale removes the disabled-mark for one tenant, for when analysis is re-enabled.
//
// 🚫 It re-runs nothing. The stored facts are still the ones written before the gap.
func (s *MemInferenceStore) ClearStale(_ context.Context, tenantID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for k, st := range s.m {
		if st.TenantID != tenantID || st.StaleReason != StaleDisabled {
			continue
		}
		st.StaleReason, st.StaleAtMS = "", 0
		s.m[k] = st
		n++
	}
	return n, nil
}

// Len reports how many inferences are held. For a caller narrating what it did, and for tests.
func (s *MemInferenceStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}

// LatestFor returns the most recently created inference for one workflow (task 8.2's narrative read).
//
// "Most recent" by `CreatedAtMS`, with the inference id as the tie-break so a store holding two rows
// written in the same millisecond answers the same way twice. A map iteration order left as the
// tie-break would make the narrative on a graph page change between reloads — which is exactly the kind
// of instability D2's pinning exists to remove.
func (s *MemInferenceStore) LatestFor(_ context.Context, tenantID, workflowID string) (Stored, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best Stored
	var found bool
	for _, st := range s.m {
		if st.TenantID != tenantID || st.WorkflowID != workflowID {
			continue
		}
		if !found || st.CreatedAtMS > best.CreatedAtMS ||
			(st.CreatedAtMS == best.CreatedAtMS && st.InferenceID > best.InferenceID) {
			best, found = st, true
		}
	}
	return best, found, nil
}
