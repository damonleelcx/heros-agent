package worktree

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/transform"
)

// BuildStatus is a transform's terminal build outcome. These are the values the `transform` row and
// the UI read (PRD §6 FR18).
//
// It answers "did the gate pass?". It deliberately does NOT answer "what did the gate prove?" — that
// is Strength's job, and the two are orthogonal facts that ADR-003 exists to stop collapsing into one
// word. `built` + `syntax-checked` and `built` + `type-checked` are both passes, and a reviewer must
// never have to guess which one they are looking at.
type BuildStatus string

const (
	// StatusBuilt: the transformed copy compiles and may be proposed and run.
	StatusBuilt BuildStatus = "built"
	// StatusBuildRejected: it does not compile. It is never proposed and never run (FR5b).
	StatusBuildRejected BuildStatus = "build-rejected"
)

// ErrBuildRejected is returned when a transform fails the build gate. The typed error is what lets
// the Runtime record `build-rejected` and the UI render it as its own state rather than as a generic
// failure — an unbuildable transform and a crashed run are different things to a user.
var ErrBuildRejected = errors.New("worktree: transform rejected: it does not build")

// BuildRejection names what broke, and — where the compiler said enough — which node and dimension.
type BuildRejection struct {
	ConfigHash string
	// NodeID and Dim are the rewrite the compiler blamed, when the error's file:line falls inside a
	// rewritten call site. Empty when the build failed somewhere we did not touch.
	NodeID string
	Dim    string
	Log    string
}

func (e *BuildRejection) Error() string {
	s := ErrBuildRejected.Error()
	if e.NodeID != "" {
		s += fmt.Sprintf(": the rewrite of node %q dimension %q does not compile", e.NodeID, e.Dim)
	}
	if e.Log != "" {
		s += "\n" + e.Log
	}
	return s
}

func (e *BuildRejection) Unwrap() error { return ErrBuildRejected }

// diagLines are the shapes in which a compiler or checker names a file and a line.
//
// One table, not one regexp per verifier: the seven gates print four distinct shapes between them, and
// which shape a given tool uses is a fact about the tool, not about our design. Listing them together
// is what makes "does the new frontend's tool print something we can read?" a question with a visible
// answer.
//
// Widened from a Go-only pattern when the gate stopped being Go-only. Left as-is, FR5b's attribution
// would have silently worked for one of seven languages while appearing to work for all of them —
// every non-Go rejection would arrive with no node and no dimension and no reason given.
var diagLines = []*regexp.Regexp{
	// go / javac / node --check / mypy:   ./pipeline.go:10:42: msg   Foo.java:10: error:   x.py:10: note:
	regexp.MustCompile(`(?m)^\s*\.?/?([^\s:]+\.[A-Za-z]+):(\d+)[:\s]`),
	// rustc / cargo:                      --> src/pipeline.rs:10:42
	regexp.MustCompile(`-->\s+([^\s:]+\.[A-Za-z]+):(\d+):`),
	// tsc:                                pipeline.ts(10,42): error TS2345: msg
	regexp.MustCompile(`(?m)^\s*([^\s(]+\.[A-Za-z]+)\((\d+),\d+\)`),
	// py_compile / any Python traceback:  File "pipeline.py", line 10
	regexp.MustCompile(`File "([^"]+)", line (\d+)`),
}

// attribute maps a verification failure back to the rewrite that caused it (FR5b: "surface a typed
// error naming the node and dimension whose rewrite failed to build").
//
// It matches the tool's file:line against the lines the patch actually edited. This is the honest
// version of that requirement: when the compiler points at a line we rewrote, we can name the node
// and dimension with certainty; when it points somewhere we never touched, we say so rather than
// blaming the only override in the spec because it is the only candidate. A confidently wrong
// attribution is worse than none — it sends the user to edit a dimension that was never the problem.
func attribute(patch *transform.Patch, log string) (nodeID, dim string) {
	for _, re := range diagLines {
		for _, m := range re.FindAllStringSubmatch(log, -1) {
			file, lineStr := m[1], m[2]
			line, err := strconv.Atoi(lineStr)
			if err != nil {
				continue
			}
			for _, t := range patch.Touched {
				if filepath.Base(t.File) == filepath.Base(file) && t.Line == line {
					return t.NodeID, t.Dim
				}
			}
		}
	}
	return "", ""
}

// Cache is the per-config_hash build cache (task 3.6).
//
// # Why it is keyed by config_hash + source_revision, not config_hash alone
//
// The same configuration can target two commits (source_revision is deliberately not in the hash —
// see internal/variantspec), and the two produce different diffs and different builds. Keying on the
// hash alone would serve one commit's build for the other's variant, which is the worst kind of cache
// bug: silently correct-looking, and it would make P4 score a build that never came from the code it
// claims to.
//
// # Why it is on disk
//
// A cache in a map dies with the process, and P4's fan-out is exactly where that matters: the point
// of caching is that scoring hundreds of variants of one base rebuilds each distinct one once, across
// however many runs and restarts it takes.
type Cache struct{ root string }

// CacheEntry records a completed transform+verification.
type CacheEntry struct {
	ConfigHash     string      `json:"config_hash"`
	SourceRevision string      `json:"source_revision"`
	Dir            string      `json:"dir"`
	Branch         string      `json:"branch"`
	Commit         string      `json:"commit"`
	DiffHash       string      `json:"diff_hash"`
	Status         BuildStatus `json:"status"`
	// Strength is what the gate PROVED, and it is cached with the verdict because a hit must be able to
	// answer that too. Serving a cached `built` with no strength would make a repeat variant look
	// weaker than the first run of the same variant — or, if anything defaulted, stronger. The cache
	// exists to avoid rework, never to change the answer.
	Strength Strength `json:"strength"`
	// VerifierTool names what ran, so a cache hit carries the same evidence the first run did.
	VerifierTool string `json:"verifier_tool"`
}

func NewCache(root string) (*Cache, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("worktree: create cache root: %w", err)
	}
	return &Cache{root: root}, nil
}

func (c *Cache) path(configHash, rev string) string {
	return filepath.Join(c.root, fmt.Sprintf("%s.%s.json", configHash, sanitize(rev)))
}

// Get returns a cached entry, or nil for a miss.
//
// A hit is only a hit if the worktree it names is still on disk. The cache is evictable (PRD §7) and
// a directory can vanish — to a cleanup job, to a full disk, to an operator. Trusting the record
// without checking would hand the executor a path that is not there.
func (c *Cache) Get(configHash, rev string) *CacheEntry {
	raw, err := os.ReadFile(c.path(configHash, rev))
	if err != nil {
		return nil
	}
	var e CacheEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil // a corrupt record is a miss, not an error: the work is always reproducible
	}
	if e.Status == StatusBuilt {
		if _, err := os.Stat(e.Dir); err != nil {
			return nil
		}
	}
	c.touch(configHash, rev) // a hit is a use: Prune's LRU must reflect it
	return &e
}

// Put records a completed transform+build.
func (c *Cache) Put(e *CacheEntry) error {
	raw, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	// Write-then-rename: a reader must never see a half-written record and treat it as a miss for a
	// build that actually succeeded, or worse, parse a truncated one.
	tmp := c.path(e.ConfigHash, e.SourceRevision) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("worktree: write cache entry: %w", err)
	}
	if err := os.Rename(tmp, c.path(e.ConfigHash, e.SourceRevision)); err != nil {
		return fmt.Errorf("worktree: commit cache entry: %w", err)
	}
	return nil
}

// Evict drops a cache record (the worktree itself is the pool's to remove).
func (c *Cache) Evict(configHash, rev string) error {
	err := os.Remove(c.path(configHash, rev))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func trimLog(s string) string {
	s = strings.TrimSpace(s)
	const max = 4000
	if len(s) > max {
		return s[:max] + "\n…(truncated)"
	}
	return s
}

// Prune bounds the cache to maxEntries, evicting least-recently-used first (task 6.2's "evict";
// PRD §7's "evictable cache"; OQ6 asks what the policy should be).
//
// # Why a bound is not optional
//
// Every cached variant holds a WORKTREE — a full checkout plus its build output. P4's fan-out scores
// hundreds of variants of one base (storage-decision-record A3: 20 variants × 200 cases × 5 seeds),
// so an unbounded cache is a slow disk-full, and a disk-full during a fan-out fails runs that have
// nothing wrong with them. The cache exists to make a repeat variant cheap, not to keep every variant
// forever.
//
// # Why LRU, and what it costs
//
// LRU rather than FIFO because P4 re-reads a base's variants while it scores them: evicting by age
// would drop the variant it is about to ask for again. Nothing is lost by evicting — every entry is
// reproducible from {config_hash, source_revision}, which is the property the whole phase is built
// on. An eviction is a rebuild, never a wrong answer.
//
// Returns the evicted worktree directories. Removing them is the caller's (Applier.Release), because
// only the pool knows how to unregister a worktree from its mirror — deleting the directory here
// would leave git's administrative record dangling.
func (c *Cache) Prune(maxEntries int) ([]CacheEntry, error) {
	if maxEntries < 0 {
		return nil, fmt.Errorf("worktree: Prune requires a non-negative bound")
	}
	entries, err := c.list()
	if err != nil {
		return nil, err
	}
	if len(entries) <= maxEntries {
		return nil, nil
	}
	// Oldest access first. A stable tiebreak on the key keeps Prune deterministic when two entries
	// share a timestamp — otherwise which one survives depends on directory order.
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].atime.Equal(entries[j].atime) {
			return entries[i].atime.Before(entries[j].atime)
		}
		return entries[i].ConfigHash < entries[j].ConfigHash
	})

	var evicted []CacheEntry
	for _, e := range entries[:len(entries)-maxEntries] {
		if err := c.Evict(e.ConfigHash, e.SourceRevision); err != nil {
			return evicted, err
		}
		evicted = append(evicted, e.CacheEntry)
	}
	return evicted, nil
}

type datedEntry struct {
	CacheEntry
	atime time.Time
}

// list reads every cache record with its last-access time.
//
// Access time comes from the record file's mtime, which Get touches on a hit — the filesystem's own
// atime is unusable (relatime, noatime, and every container runtime differ on whether it updates at
// all, so an LRU built on it would evict at random on some hosts and never on others).
func (c *Cache) list() ([]datedEntry, error) {
	des, err := os.ReadDir(c.root)
	if err != nil {
		return nil, fmt.Errorf("worktree: read cache root: %w", err)
	}
	var out []datedEntry
	for _, de := range des {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(c.root, de.Name()))
		if err != nil {
			continue // a record we cannot read is a miss, not a failure: the work is reproducible
		}
		var e CacheEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		out = append(out, datedEntry{CacheEntry: e, atime: info.ModTime()})
	}
	return out, nil
}

// touch records a cache hit, so Prune's LRU reflects use rather than creation.
func (c *Cache) touch(configHash, rev string) {
	now := time.Now()
	_ = os.Chtimes(c.path(configHash, rev), now, now) // best effort: a failed touch costs an eviction, not correctness
}
