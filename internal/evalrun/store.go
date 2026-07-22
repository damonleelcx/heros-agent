package evalrun

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// store.go is task 6.2: persist eval results with the FULL P0 tag set, so every leaderboard slice —
// per pattern, per failure cluster, per node, per seed — is a query rather than a re-run.
//
// The tag set is checked at THIS boundary as well as by the database's NOT NULL columns, which is
// deliberate duplication: the DB is the last line and the emission boundary is the first, and a
// violation caught here names the offending field instead of surfacing as a constraint error three
// layers down. It is the same defence-in-depth P0 established for metricevent.

// ErrIncompleteTags is returned when a result would be persisted without the full seven-tag set.
var ErrIncompleteTags = errors.New("evalrun: result is missing required P0 tags")

// Result is one persisted eval measurement, fully tagged.
type Result struct {
	// The seven P0 tags. Every one is required; there is no path that defaults one.
	VariantID  string    `json:"variant_id"`
	RunID      string    `json:"run_id"`
	NodeID     string    `json:"node_id"`
	CaseID     string    `json:"case_id"`
	Seed       int64     `json:"seed"`
	Timestamp  time.Time `json:"timestamp"`
	ConfigHash string    `json:"config_hash"`

	// Structural helper so the (workflow_id, node_id) FK resolves.
	WorkflowID string `json:"workflow_id"`

	// Payload.
	MetricName string  `json:"metric_name"`
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`

	// Attribution and provenance.
	EvaluatorName string `json:"evaluator_name"`
	EvalSetHash   string `json:"eval_set_hash"`
	// ReferenceLabel rides on the row so a slice can exclude weak-labeled evidence in SQL rather
	// than by re-deriving it from the case table at read time.
	ReferenceLabel evalharness.ReferenceLabel `json:"reference_label"`
	// Pattern is the node's P3.5 label, carried so "slice the board by pattern" is a WHERE clause.
	Pattern string `json:"pattern,omitempty"`

	// Content-hashed blob references. Payloads are NEVER inlined here (task 6.5).
	InputBlobHash  string `json:"input_blob_hash,omitempty"`
	OutputBlobHash string `json:"output_blob_hash,omitempty"`
	JudgePromptRef string `json:"judge_prompt_ref,omitempty"`
	ReferenceRef   string `json:"reference_ref,omitempty"`
}

// Validate enforces tag completeness. It returns EVERY problem at once, sorted, for the same reason
// metricevent.Validate does: a caller fixing one missing tag at a time learns about the next one on
// the next round trip.
func (r Result) Validate() error {
	var problems []string
	if strings.TrimSpace(r.VariantID) == "" {
		problems = append(problems, "variant_id must not be empty")
	}
	if strings.TrimSpace(r.RunID) == "" {
		problems = append(problems, "run_id must not be empty")
	}
	if strings.TrimSpace(r.NodeID) == "" {
		problems = append(problems, "node_id must not be empty (run-scoped rows use the "+telemetry.NodeIDRun+" sentinel)")
	}
	if strings.TrimSpace(r.CaseID) == "" {
		problems = append(problems, "case_id must not be empty")
	}
	if r.Seed < 0 {
		problems = append(problems, "seed must be >= 0")
	}
	if r.Timestamp.IsZero() {
		problems = append(problems, "timestamp must be set")
	}
	if !isHash64(r.ConfigHash) {
		problems = append(problems, "config_hash must be 64 lowercase hex characters")
	}
	if strings.TrimSpace(r.WorkflowID) == "" {
		problems = append(problems, "workflow_id must not be empty")
	}
	if strings.TrimSpace(r.MetricName) == "" {
		problems = append(problems, "metric_name must not be empty")
	}
	if strings.TrimSpace(r.Unit) == "" {
		problems = append(problems, "unit must not be empty")
	}
	if strings.TrimSpace(r.EvaluatorName) == "" {
		problems = append(problems, "evaluator_name must not be empty: every row must be attributable to a producer")
	}
	if !isHash64(r.EvalSetHash) {
		problems = append(problems, "eval_set_hash must be 64 lowercase hex characters: a board must be attributable to an exact eval set")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%w: %s", ErrIncompleteTags, strings.Join(problems, "; "))
	}
	return nil
}

func isHash64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// AsMetricEvent projects the result onto the P0 metric-event contract, so the SAME validator P2.5
// emission goes through also sees an eval result. Two tag checks that could disagree would be worse
// than one; this makes them the same check.
func (r Result) AsMetricEvent() metricevent.Event {
	seed := r.Seed
	value := r.Value
	return metricevent.Event{
		SchemaVersion: metricevent.SchemaVersion,
		VariantID:     r.VariantID,
		RunID:         r.RunID,
		NodeID:        r.NodeID,
		CaseID:        r.CaseID,
		Seed:          &seed,
		Timestamp:     r.Timestamp.UTC().Format(time.RFC3339Nano),
		ConfigHash:    r.ConfigHash,
		MetricName:    r.MetricName,
		Value:         &value,
		Unit:          r.Unit,
	}
}

// ResultsFrom projects a harness measurement onto fully-tagged rows.
//
// The tags come from the RUN CONTEXT and the case, never from the evaluator's arguments — the same
// property telemetry's evaluator seam froze. An evaluator therefore cannot produce an under-tagged
// row even by trying.
func ResultsFrom(rc telemetry.RunContext, workflowID, evalSetHash string, c evalharness.Case,
	pattern string, values []evalharness.MetricValue, now time.Time) []Result {

	out := make([]Result, 0, len(values))
	for _, v := range values {
		nodeID := v.NodeID
		if nodeID == "" {
			nodeID = telemetry.NodeIDRun
		}
		out = append(out, Result{
			VariantID:     rc.VariantID,
			RunID:         rc.RunID,
			NodeID:        nodeID,
			CaseID:        c.CaseID,
			Seed:          rc.Seed,
			Timestamp:     now,
			ConfigHash:    rc.ConfigHash,
			WorkflowID:    workflowID,
			MetricName:    v.Metric,
			Value:         v.Value,
			Unit:          v.Unit,
			EvaluatorName: v.Evaluator,
			EvalSetHash:   evalSetHash,
			// The CASE is the authority on its own reference label, not the copy the evaluator
			// happened to carry. Two sources for one fact eventually disagree, and the one that
			// would win here is the one furthest from the provenance.
			ReferenceLabel: c.Label,
			Pattern:        pattern,
		})
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Store
// ─────────────────────────────────────────────────────────────────────────────

// Store persists eval results and reads them back as the statistics layer's series.
type Store interface {
	PutResults(ctx context.Context, rows []Result) error
	// SeriesFor returns one variant's observations for one metric, which is exactly the shape
	// evalstats.Aggregate consumes. The read model is the statistics layer's input type on purpose:
	// a translation step between them is a place for the tags to get lost.
	SeriesFor(ctx context.Context, evalSetHash, variantID, metric string) (evalstats.Series, error)
}

// MemStore is the in-memory result store used by the demo driver and by tests that are about
// measurement rather than about Postgres.
type MemStore struct {
	mu   sync.RWMutex
	rows []Result
}

// NewMemStore builds an empty in-memory store.
func NewMemStore() *MemStore { return &MemStore{} }

// PutResults validates every row BEFORE writing any of them. All-or-nothing because a partial write
// of a fan-out's results is a silently incomplete leaderboard.
func (s *MemStore) PutResults(_ context.Context, rows []Result) error {
	for _, r := range rows {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range rows {
		if s.duplicate(r) {
			// The natural key already holds this measurement. A redelivered unit therefore
			// overwrites nothing and adds nothing — the storage half of "no double-charge".
			continue
		}
		s.rows = append(s.rows, r)
	}
	return nil
}

// duplicate reports whether the natural key is already present. Caller holds the lock.
func (s *MemStore) duplicate(r Result) bool {
	for _, e := range s.rows {
		if e.ConfigHash == r.ConfigHash && e.RunID == r.RunID && e.NodeID == r.NodeID &&
			e.CaseID == r.CaseID && e.Seed == r.Seed && e.EvaluatorName == r.EvaluatorName &&
			e.MetricName == r.MetricName {
			return true
		}
	}
	return false
}

// SeriesFor reads a variant's observations for a metric.
func (s *MemStore) SeriesFor(_ context.Context, evalSetHash, variantID, metric string) (evalstats.Series, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := evalstats.Series{VariantID: variantID, Metric: metric}
	for _, r := range s.rows {
		if r.EvalSetHash != evalSetHash || r.VariantID != variantID || r.MetricName != metric {
			continue
		}
		if r.NodeID != telemetry.NodeIDRun {
			continue // run-scoped series only; per-node rows are read by SliceByNode
		}
		out.ConfigHash = r.ConfigHash
		out.Obs = append(out.Obs, evalstats.Observation{CaseID: r.CaseID, Seed: r.Seed, Value: r.Value})
	}
	sort.Slice(out.Obs, func(i, j int) bool {
		if out.Obs[i].CaseID != out.Obs[j].CaseID {
			return out.Obs[i].CaseID < out.Obs[j].CaseID
		}
		return out.Obs[i].Seed < out.Obs[j].Seed
	})
	return out, nil
}

// Slice is a leaderboard slice predicate. Every field is a tag, which is the point: the tags were
// persisted so that slicing is a WHERE clause rather than a re-run.
type Slice struct {
	EvalSetHash string
	VariantID   string
	Metric      string
	NodeID      string
	CaseID      string
	Pattern     string
	Seed        *int64
	Evaluator   string
	// ExcludeWeak drops rows whose reference is weak-labeled — the query-level enforcement of
	// "weak references never silently drive scoring".
	ExcludeWeak bool
}

// Query returns the rows matching a slice.
func (s *MemStore) Query(sl Slice) []Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Result
	for _, r := range s.rows {
		if sl.EvalSetHash != "" && r.EvalSetHash != sl.EvalSetHash {
			continue
		}
		if sl.VariantID != "" && r.VariantID != sl.VariantID {
			continue
		}
		if sl.Metric != "" && r.MetricName != sl.Metric {
			continue
		}
		if sl.NodeID != "" && r.NodeID != sl.NodeID {
			continue
		}
		if sl.CaseID != "" && r.CaseID != sl.CaseID {
			continue
		}
		if sl.Pattern != "" && r.Pattern != sl.Pattern {
			continue
		}
		if sl.Seed != nil && r.Seed != *sl.Seed {
			continue
		}
		if sl.Evaluator != "" && r.EvaluatorName != sl.Evaluator {
			continue
		}
		if sl.ExcludeWeak && r.ReferenceLabel == evalharness.LabelWeak {
			continue
		}
		out = append(out, r)
	}
	return out
}

// Len reports how many rows are stored.
func (s *MemStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rows)
}

// ─────────────────────────────────────────────────────────────────────────────
// Blob store — task 6.5
// ─────────────────────────────────────────────────────────────────────────────

// BlobStore holds case inputs, reference outputs and rendered judge prompts by content hash.
//
// They live here and NOT in a log line or a database column for two reasons: a rendered judge prompt
// carries the case input verbatim (so inlining it in logs scatters user data across a log pipeline
// with different retention than the database), and content-addressing means an identical prompt
// rendered a thousand times is stored once and is comparable by hash.
type BlobStore interface {
	Put(ctx context.Context, data []byte, mediaType string) (hash string, err error)
	Get(ctx context.Context, hash string) ([]byte, bool, error)
}

// MemBlobStore is the in-memory content-addressed blob store.
type MemBlobStore struct {
	mu   sync.RWMutex
	blob map[string][]byte
	kind map[string]string
}

// NewMemBlobStore builds an empty blob store.
func NewMemBlobStore() *MemBlobStore {
	return &MemBlobStore{blob: map[string][]byte{}, kind: map[string]string{}}
}

func (s *MemBlobStore) Put(_ context.Context, data []byte, mediaType string) (string, error) {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blob[h] = append([]byte(nil), data...)
	s.kind[h] = mediaType
	return h, nil
}

func (s *MemBlobStore) Get(_ context.Context, hash string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.blob[hash]
	return b, ok, nil
}

// Len reports how many distinct blobs are stored — the content-addressing dividend is visible here:
// a thousand identical prompts are one blob.
func (s *MemBlobStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.blob)
}

// StoreJudgePrompt content-hashes a rendered judge prompt and returns its reference. Callers persist
// the REFERENCE on the result row; the prompt text itself never becomes a column value or a log
// field.
func StoreJudgePrompt(ctx context.Context, bs BlobStore, prompt string) (string, error) {
	if bs == nil {
		return "", errors.New("evalrun: a judge prompt needs a blob store; it must never be inlined")
	}
	return bs.Put(ctx, []byte(prompt), "text/plain; charset=utf-8")
}

// ─────────────────────────────────────────────────────────────────────────────
// Postgres store
// ─────────────────────────────────────────────────────────────────────────────

// PGStore persists eval results into the Postgres schema (migration 0009). The DB's NOT NULL columns
// and FKs are the last line of the tag contract; Validate above is the first.
type PGStore struct{ db *sql.DB }

// NewPGStore builds the Postgres-backed store.
func NewPGStore(db *sql.DB) *PGStore { return &PGStore{db: db} }

// PutResults writes rows in ONE transaction, so a fan-out's batch is all-or-nothing.
func (s *PGStore) PutResults(ctx context.Context, rows []Result) error {
	for _, r := range rows {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, r := range rows {
		// ON CONFLICT DO NOTHING against the natural key: a redelivered unit re-writes the same
		// measurement rather than duplicating it. The uniqueness is enforced by the schema, so this
		// holds even for a writer that forgets.
		_, err := tx.ExecContext(ctx,
			`INSERT INTO eval_result
			   (config_hash, variant_id, run_id, node_id, case_id, seed, ts, workflow_id,
			    metric_name, value, unit, evaluator_name, eval_set_hash, reference_label, pattern)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			 ON CONFLICT ON CONSTRAINT eval_result_natural_key DO NOTHING`,
			r.ConfigHash, r.VariantID, r.RunID, r.NodeID, r.CaseID, r.Seed, r.Timestamp.UTC(),
			r.WorkflowID, r.MetricName, r.Value, r.Unit, r.EvaluatorName, r.EvalSetHash,
			string(r.ReferenceLabel), nullable(r.Pattern))
		if err != nil {
			return fmt.Errorf("evalrun: insert result %s/%s: %w", r.RunID, r.MetricName, err)
		}
	}
	return tx.Commit()
}

// SeriesFor reads a run-scoped series back for the statistics layer.
func (s *PGStore) SeriesFor(ctx context.Context, evalSetHash, variantID, metric string) (evalstats.Series, error) {
	out := evalstats.Series{VariantID: variantID, Metric: metric}
	rows, err := s.db.QueryContext(ctx,
		`SELECT case_id, seed, value, config_hash
		   FROM eval_result
		  WHERE eval_set_hash = $1 AND variant_id = $2 AND metric_name = $3 AND node_id = $4
		  ORDER BY case_id, seed`,
		evalSetHash, variantID, metric, telemetry.NodeIDRun)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var o evalstats.Observation
		var configHash string
		if err := rows.Scan(&o.CaseID, &o.Seed, &o.Value, &configHash); err != nil {
			return out, err
		}
		out.ConfigHash = configHash
		out.Obs = append(out.Obs, o)
	}
	return out, rows.Err()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
