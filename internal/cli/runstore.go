package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/heros-foreal/agentd/internal/runlink"
)

// runstore.go is the local, on-disk run store (contracts doc Q2 / task 1.4). `eval` writes an
// allowlist-shaped RunRecord here; `link` reads it later. Because the record holds only allowlisted
// fields, the file at rest is exactly the egress surface — a customer can `cat` it and see everything
// `link` could ever send. It lives in the customer's environment and is read by nothing but an explicit
// `link`.

// RunStoreDir is the directory, relative to the working root, where run records live. Git-ignored.
const RunStoreDir = ".heros/runs"

// RunStore is a directory of run records.
type RunStore struct {
	dir string
}

// OpenRunStore returns the run store rooted at root/.heros/runs, creating the directory on first write.
func OpenRunStore(root string) *RunStore {
	return &RunStore{dir: filepath.Join(root, RunStoreDir)}
}

// Put writes a run record as <run_id>.json (0600 — it is the customer's data, on the customer's disk).
func (s *RunStore) Put(r runlink.RunRecord) error {
	if r.RunID == "" {
		return fmt.Errorf("run store: record has no run_id")
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("run store: mkdir: %w", err)
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("run store: marshal %s: %w", r.RunID, err)
	}
	path := filepath.Join(s.dir, safeName(r.RunID)+".json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("run store: write %s: %w", path, err)
	}
	return nil
}

// Get reads one run record by id. A missing record is an invalid-config error naming the id (the user
// asked to link a run that was never evaluated).
func (s *RunStore) Get(runID string) (runlink.RunRecord, error) {
	path := filepath.Join(s.dir, safeName(runID)+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return runlink.RunRecord{}, invalidConfig(fmt.Sprintf("no local run %q found under %s — run `eval` first, or check the run id", runID, s.dir))
		}
		return runlink.RunRecord{}, operational(fmt.Sprintf("read run %s", runID), err)
	}
	var r runlink.RunRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return runlink.RunRecord{}, operational(fmt.Sprintf("run %s is corrupt", runID), err)
	}
	return r, nil
}

// List returns the run ids present, sorted. Used by `status` to report how many local runs exist.
func (s *RunStore) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, operational("list run store", err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		ids = append(ids, e.Name()[:len(e.Name())-len(".json")])
	}
	sort.Strings(ids)
	return ids, nil
}

// safeName keeps a run id from escaping the store directory. Run ids are derived hashes, but a store
// path is a place path traversal likes to hide, so it is refused structurally rather than trusted.
func safeName(id string) string {
	out := make([]rune, 0, len(id))
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
