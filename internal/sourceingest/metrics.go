package sourceingest

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

// metrics.go publishes ingest outcomes BROKEN OUT PER FORGE and per failure cause.
//
// # Why the aggregate is refused rather than merely supplemented
//
// PRD §9.5: *"an aggregate hides the single-sample defect."* Three forges behind one success rate is
// the shape where a completely broken Bitbucket adapter — 0% success, every read failing — reads as a
// 96% overall rate, because Bitbucket is 4% of the traffic. Nobody investigates 96%. The aggregate is
// still computed (`Total`), because an operator does want one number for a dashboard tile, but it is
// never the ONLY figure available and §7.7's fence asserts the breakdown exists rather than asserting
// the aggregate is present.
//
// # Why counters and not a histogram
//
// Duration is reported as count/total/max per forge rather than as buckets. A histogram is the right
// shape when the question is "what does the tail look like", and the question this phase actually has
// is §5.5's: does the 30,500-file benchmark ingest within budget, per mode. That is answered by a max
// and a mean. Buckets can be added when somebody has a question that needs them; adding them now
// would be building for a load nobody has.

// ForgeStats is one forge's ingest outcomes.
type ForgeStats struct {
	Forge string `json:"forge"`
	// Succeeded and Failed are counts of completed reads.
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
	// ByCause counts failures per cause. Every one of the four causes is present as a key even at
	// zero — an absent key and a zero are indistinguishable to a dashboard that renders whatever it
	// finds, and "we have never seen this failure" is a different fact from "this adapter cannot
	// produce this failure because it is broken in a way that fails earlier".
	ByCause map[string]int64 `json:"by_cause"`
	// Bytes is total snapshot bytes stored from this forge.
	Bytes int64 `json:"bytes"`
	// DurationTotalMS and DurationMaxMS answer §5.5's benchmark question.
	DurationTotalMS int64 `json:"duration_total_ms"`
	DurationMaxMS   int64 `json:"duration_max_ms"`
	// ConsecutiveFailures is the escalation input (§7.3, task 5.3): a connection failing forever at
	// WARN is a connection nobody fixes.
	ConsecutiveFailures int64 `json:"consecutive_failures"`
}

// IngestHealth is what the health endpoint publishes.
type IngestHealth struct {
	// PerForge is the breakdown, sorted by forge. 🔴 It is a LIST rather than a map so its ordering is
	// stable in a diff and a test can assert "three entries, in this order" without sorting keys.
	PerForge []ForgeStats `json:"per_forge"`
	// Total is the aggregate, present but never alone.
	Total ForgeStats `json:"total"`
	// Escalated names the forges whose consecutive-failure count has crossed the threshold. Named
	// rather than counted, because "something is escalated" without a subject sends an operator to
	// read three dashboards to learn what this response already knew.
	Escalated []string `json:"escalated,omitempty"`
	// EscalateAfter is the threshold, published so a monitor does not have to hard-code it.
	EscalateAfter int64 `json:"escalate_after"`
}

// EscalateAfterConsecutiveFailures is when a forge stops being a WARN and becomes a named problem.
//
// Three, because one failure is a rotated token somebody is already fixing and two is a bad afternoon;
// three consecutive failures on one forge with no success in between is an adapter or a credential
// that is not coming back on its own.
const EscalateAfterConsecutiveFailures = 3

// IngestMetrics accumulates per-forge ingest outcomes.
//
// Safe for concurrent use, and safe to call on a NIL receiver: `Observe` on a nil *IngestMetrics is a
// no-op, so a deployment wired without metrics still clones rather than panicking on the one code
// path that runs while nobody is watching.
type IngestMetrics struct {
	mu    sync.Mutex
	stats map[Forge]*ForgeStats
}

// NewIngestMetrics builds an empty collector with every forge pre-registered.
//
// 🔴 Pre-registered, at zero. A forge that has never been used must appear in the health document, or
// "GitHub is at 100% and Bitbucket is absent" reads as "everyone uses GitHub" when it actually means
// the Bitbucket adapter has never once been reached.
func NewIngestMetrics() *IngestMetrics {
	m := &IngestMetrics{stats: map[Forge]*ForgeStats{}}
	for _, f := range Forges() {
		m.stats[f] = newForgeStats(f)
	}
	return m
}

func newForgeStats(f Forge) *ForgeStats {
	s := &ForgeStats{Forge: f.String(), ByCause: map[string]int64{}}
	for _, c := range CloneCauses() {
		s.ByCause[c.String()] = 0
	}
	return s
}

// Observe records one completed read. An empty cause means it succeeded.
func (m *IngestMetrics) Observe(f Forge, cause CloneCause, bytes int64, d time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.stats[f]
	if !ok {
		s = newForgeStats(f)
		m.stats[f] = s
	}
	ms := d.Milliseconds()
	s.DurationTotalMS += ms
	if ms > s.DurationMaxMS {
		s.DurationMaxMS = ms
	}
	if cause == "" {
		s.Succeeded++
		s.Bytes += bytes
		s.ConsecutiveFailures = 0
		return
	}
	s.Failed++
	s.ConsecutiveFailures++
	key := cause.String()
	if !cause.Valid() {
		// An unclassified cause is counted under `network`, which is where classifyGitFailure's
		// default sends it too. Inventing a fifth key here would make the console's four-message
		// switch fall through to nothing, which renders as a blank card.
		key = CauseNetwork.String()
	}
	s.ByCause[key]++
}

// Health renders the current breakdown.
func (m *IngestMetrics) Health() IngestHealth {
	if m == nil {
		return IngestHealth{PerForge: []ForgeStats{}, EscalateAfter: EscalateAfterConsecutiveFailures}
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	out := IngestHealth{EscalateAfter: EscalateAfterConsecutiveFailures}
	total := ForgeStats{Forge: "total", ByCause: map[string]int64{}}
	for _, c := range CloneCauses() {
		total.ByCause[c.String()] = 0
	}
	forges := make([]Forge, 0, len(m.stats))
	for f := range m.stats {
		forges = append(forges, f)
	}
	sort.Slice(forges, func(i, j int) bool { return forges[i] < forges[j] })

	for _, f := range forges {
		s := *m.stats[f]
		s.ByCause = copyCounts(m.stats[f].ByCause)
		out.PerForge = append(out.PerForge, s)
		total.Succeeded += s.Succeeded
		total.Failed += s.Failed
		total.Bytes += s.Bytes
		total.DurationTotalMS += s.DurationTotalMS
		if s.DurationMaxMS > total.DurationMaxMS {
			total.DurationMaxMS = s.DurationMaxMS
		}
		for k, v := range s.ByCause {
			total.ByCause[k] += v
		}
		if s.ConsecutiveFailures >= EscalateAfterConsecutiveFailures {
			out.Escalated = append(out.Escalated, s.Forge)
		}
	}
	out.Total = total
	if out.PerForge == nil {
		out.PerForge = []ForgeStats{}
	}
	return out
}

func copyCounts(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// randomHex returns n random bytes, hex-encoded. Used for identifiers.
//
// It reads `crypto/rand` and does not fall back to a weaker source on error: a clone-record id
// colliding because a math/rand fallback was seeded identically in two replicas would silently merge
// two tenants' ledger entries, and a panic here is a process that never started rather than a ledger
// nobody can trust.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("sourceingest: the system random source is unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
