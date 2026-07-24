package plancfg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// source.go ships the two Source implementations P7 needs: a file-backed config store (the deployed
// shape — a mounted volume or a config-map the control plane publishes into) and an in-memory one (the
// hermetic-test and demo shape).
//
// Neither is a git-tracked file, and that is not a convention — TestNoPricedValueInGitTrackedFile
// enumerates the git index and fails if a plan catalog is committed. The runtime reads whatever path it
// is pointed at; the fence that keeps prices out of history lives in CI, where it can actually see the
// index.

// PlanConfigPathEnv names the environment variable that points at the config store. Named here so the
// deployment, the demo, and the tests cannot spell it three ways.
const PlanConfigPathEnv = "HEROS_PLAN_CONFIG_PATH"

// catalogFile is the on-disk shape of a published catalog. It is a distinct type from Snapshot so the
// wire format can gain fields without the in-memory model inheriting them.
type catalogFile struct {
	Version string `json:"version"`
	Plans   []struct {
		PlanID      string             `json:"plan_id"`
		DisplayName string             `json:"display_name"`
		Rank        int                `json:"rank"`
		Features    []string           `json:"features"`
		Limits      map[string]float64 `json:"limits"`
		PriceRefs   map[string]string  `json:"price_refs"`
	} `json:"plans"`
}

// FileSource loads the plan catalog from a JSON file the config store publishes.
//
// The file is re-read on every Load — that is what makes the catalog hot-reloadable: publishing is
// "write the file, signal a reload", with no process restart and no deploy.
type FileSource struct{ Path string }

// NewFileSource points a source at path.
func NewFileSource(path string) *FileSource { return &FileSource{Path: path} }

// FileSourceFromEnv builds a FileSource from PlanConfigPathEnv. It returns a nil source and false when
// the variable is unset, so a caller can say so explicitly rather than defaulting to a path nobody
// configured.
func FileSourceFromEnv() (*FileSource, bool) {
	p := os.Getenv(PlanConfigPathEnv)
	if p == "" {
		return nil, false
	}
	return NewFileSource(p), true
}

// Describe names the store — the path, never its contents.
func (f *FileSource) Describe() string { return "file:" + filepath.Clean(f.Path) }

// Load reads and validates the published catalog.
func (f *FileSource) Load() (Snapshot, error) {
	b, err := os.ReadFile(f.Path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read plan config %q: %w", f.Path, err)
	}
	var cf catalogFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return Snapshot{}, fmt.Errorf("parse plan config %q: %w", f.Path, err)
	}
	st, err := os.Stat(f.Path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("stat plan config %q: %w", f.Path, err)
	}
	return toSnapshot(cf, st.ModTime())
}

func toSnapshot(cf catalogFile, published time.Time) (Snapshot, error) {
	snap := Snapshot{Version: cf.Version, Plans: map[string]PlanConfig{}, Published: published.UTC()}
	for _, p := range cf.Plans {
		id := NormalizePlanID(p.PlanID)
		if id == "" {
			return Snapshot{}, fmt.Errorf("plan config: a plan entry has no plan_id")
		}
		if _, dup := snap.Plans[id]; dup {
			return Snapshot{}, fmt.Errorf("plan config: duplicate plan_id %q", id)
		}
		pc := PlanConfig{
			PlanID:      id,
			DisplayName: p.DisplayName,
			Rank:        p.Rank,
			Features:    map[Feature]bool{},
			Limits:      map[Limit]float64{},
			PriceRefs:   map[string]string{},
			Version:     cf.Version,
		}
		if pc.DisplayName == "" {
			// Plans are referred to by NAME everywhere in the product; an unnamed plan would surface as a
			// blank in a denial message ("upgrade to ''"), which reads as a broken product.
			return Snapshot{}, fmt.Errorf("plan config: plan %q has no display_name", id)
		}
		for _, fs := range p.Features {
			feat := Feature(fs)
			if !knownFeature(feat) {
				return Snapshot{}, fmt.Errorf("plan config: plan %q lists unknown feature %q", id, fs)
			}
			pc.Features[feat] = true
		}
		for k, v := range p.Limits {
			lim := Limit(k)
			if !knownLimit(lim) {
				return Snapshot{}, fmt.Errorf("plan config: plan %q sets unknown limit %q", id, k)
			}
			if v < 0 {
				return Snapshot{}, fmt.Errorf("plan config: plan %q sets negative limit %q", id, k)
			}
			pc.Limits[lim] = v
		}
		for k, v := range p.PriceRefs {
			if v == "" {
				return Snapshot{}, fmt.Errorf("plan config: plan %q has an empty price reference for %q", id, k)
			}
			pc.PriceRefs[k] = v
		}
		snap.Plans[id] = pc
	}
	return snap, nil
}

func knownFeature(f Feature) bool {
	for _, k := range Features {
		if k == f {
			return true
		}
	}
	return false
}

func knownLimit(l Limit) bool {
	for _, k := range Limits {
		if k == l {
			return true
		}
	}
	return false
}

// MemSource is an in-memory config store for hermetic tests and the demo. Publish swaps the catalog the
// way a config publish does in production; the resolver reads it through the same Source contract, so
// the hot-reload path under test is the shipped one.
type MemSource struct {
	mu   sync.RWMutex
	snap Snapshot
	err  error
}

// NewMemSource builds an empty in-memory store.
func NewMemSource() *MemSource { return &MemSource{} }

// Publish replaces the catalog the store serves.
func (m *MemSource) Publish(snap Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap, m.err = snap, nil
}

// PublishJSON parses the same on-disk catalog format the FileSource reads, so a fixture written once
// exercises both stores.
func (m *MemSource) PublishJSON(b []byte) error {
	var cf catalogFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return err
	}
	snap, err := toSnapshot(cf, time.Unix(0, 0))
	if err != nil {
		return err
	}
	m.Publish(snap)
	return nil
}

// SetErr makes Load fail — the config-store-unavailable seam.
func (m *MemSource) SetErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// Load returns the published catalog.
func (m *MemSource) Load() (Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.err != nil {
		return Snapshot{}, m.err
	}
	// Copy the map so a caller cannot mutate the store's catalog through the returned snapshot.
	out := Snapshot{Version: m.snap.Version, Published: m.snap.Published, Plans: map[string]PlanConfig{}}
	for k, v := range m.snap.Plans {
		out.Plans[k] = v
	}
	return out, nil
}

// Describe names the store.
func (m *MemSource) Describe() string { return "memory" }
