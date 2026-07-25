package studio

import "sync"

// bindings.go is the matrix's in-force selection: per (tenant, workflow), which cell each node is bound
// to (task M3.4). "Save and inject into runtime" writes here.
//
// # Why this is not a new table
//
// A binding is `bound` apply mode: it belongs, durably, in the workflow's Variant Spec and the emitted
// binding document (P10 §7). This store is the demo/read-model projection of the current selection so
// the matrix can render the in-force cell; a production deployment persists it as spec + document. It
// carries NO verified state that could imply a proof — a studio bind is always unverified (PRD FR37).

// Binding is one node's in-force selection.
type Binding struct {
	NodeID          string `json:"node_id"`
	ModelVersionID  string `json:"model_version_id"`
	ModelID         string `json:"model_id"`
	PromptName      string `json:"prompt_name"`
	PromptVersionID string `json:"prompt_version_id"`
	// Verified is ALWAYS false for a studio bind — a selection is not a proof. Kept as a field, not a
	// constant, so a future path that binds a verified configuration can set it, and the renderer reads
	// one place. The matrix never sets it true.
	Verified bool `json:"verified"`
}

// BindStore holds the current bindings. One binding per node (a node runs one model + one prompt at
// runtime), so Bind REPLACES a node's prior selection (PRD FR38, one bound cell per column).
type BindStore struct {
	mu sync.RWMutex
	// key: tenant\x1fworkflow -> nodeID -> Binding
	m map[string]map[string]Binding
}

// NewBindStore returns an empty bind store.
func NewBindStore() *BindStore { return &BindStore{m: map[string]map[string]Binding{}} }

func key(tenant, workflow string) string { return tenant + "\x1f" + workflow }

// Bind records a node's in-force selection, replacing any prior one for that node.
func (s *BindStore) Bind(tenant, workflow string, b Binding) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(tenant, workflow)
	if s.m[k] == nil {
		s.m[k] = map[string]Binding{}
	}
	s.m[k][b.NodeID] = b // replace — at most one bound cell per node
}

// Bindings returns the current in-force selections for a (tenant, workflow), keyed by node id.
func (s *BindStore) Bindings(tenant, workflow string) map[string]Binding {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]Binding{}
	for id, b := range s.m[key(tenant, workflow)] {
		out[id] = b
	}
	return out
}
