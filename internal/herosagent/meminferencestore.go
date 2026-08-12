package herosagent

import (
	"context"
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

// Len reports how many inferences are held. For a caller narrating what it did, and for tests.
func (s *MemInferenceStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}
