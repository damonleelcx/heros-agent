package herosagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// store.go holds the VersionStore implementations: Postgres over migration 0046, and in-memory.

// PGVersionStore is the durable store.
type PGVersionStore struct{ db *sql.DB }

// NewPGVersionStore returns a store over an open Postgres handle.
func NewPGVersionStore(db *sql.DB) (*PGVersionStore, error) {
	if db == nil {
		return nil, errors.New("herosagent: nil database")
	}
	return &PGVersionStore{db: db}, nil
}

func (s *PGVersionStore) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 10*time.Second)
}

// Put records a published version. Re-putting the same hash is a no-op on content: the row is
// immutable and the definition is a pure function of the hash, so there is nothing to update.
func (s *PGVersionStore) Put(parent context.Context, v Version) error {
	spec, err := json.Marshal(v.Definition)
	if err != nil {
		return fmt.Errorf("herosagent: encoding the definition: %w", err)
	}
	ctx, cancel := s.ctx(parent)
	defer cancel()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO heros_agent_version
		   (config_hash, spec_json, model_ref, credential_ref, rehearsal_state, rehearsal_report, created_at_ms)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (config_hash) DO NOTHING`,
		v.ConfigHash, string(spec), v.ModelRef, v.CredentialRef,
		string(v.RehearsalState), nullString(v.RehearsalReport), v.CreatedAtMS)
	if err != nil {
		return fmt.Errorf("herosagent: recording version %s: %w", v.ConfigHash, err)
	}
	return nil
}

const versionColumns = `config_hash, spec_json, model_ref, credential_ref, rehearsal_state,
	rehearsal_report, activated_at_ms, created_at_ms`

func scanVersion(sc interface{ Scan(...any) error }) (Version, error) {
	var v Version
	var spec string
	var report sql.NullString
	var activated sql.NullInt64
	var state string
	if err := sc.Scan(&v.ConfigHash, &spec, &v.ModelRef, &v.CredentialRef, &state,
		&report, &activated, &v.CreatedAtMS); err != nil {
		return Version{}, err
	}
	if err := json.Unmarshal([]byte(spec), &v.Definition); err != nil {
		return Version{}, fmt.Errorf("herosagent: decoding definition %s: %w", v.ConfigHash, err)
	}
	v.RehearsalState = RehearsalState(state)
	v.RehearsalReport = report.String
	// 🔴 `.Valid` IS the SQL predicate. `PGVersionStore.Active` selects `WHERE activated_at_ms IS NOT
	// NULL`, so carrying validity here is what makes the Go answer the same question the query asked.
	// Reading `.Int64` alone collapsed NULL and 0 into one value, and a row stamped 0 then satisfied
	// the query while failing `Active()`.
	if activated.Valid {
		at := activated.Int64
		v.ActivatedAtMS = &at
	}
	return v, nil
}

// Get returns one version. ok=false is NOT PUBLISHED — never a read failure, which is the error.
func (s *PGVersionStore) Get(parent context.Context, configHash string) (Version, bool, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	v, err := scanVersion(s.db.QueryRowContext(ctx,
		`SELECT `+versionColumns+` FROM heros_agent_version WHERE config_hash = $1`, configHash))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Version{}, false, nil
	case err != nil:
		return Version{}, false, fmt.Errorf("herosagent: reading version %s: %w", configHash, err)
	}
	return v, true, nil
}

// Active returns the one activated version. ok=false means NONE IS ACTIVE — a real state on a fresh
// deployment, and distinct from a read failure.
func (s *PGVersionStore) Active(parent context.Context) (Version, bool, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	v, err := scanVersion(s.db.QueryRowContext(ctx,
		`SELECT `+versionColumns+` FROM heros_agent_version WHERE activated_at_ms IS NOT NULL`))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Version{}, false, nil
	case err != nil:
		return Version{}, false, fmt.Errorf("herosagent: reading the active version: %w", err)
	}
	return v, true, nil
}

// Activate makes one version active IN ONE TRANSACTION that first clears any other (task 3.7).
//
// 🔴 The clear and the set must be one transaction, and the partial unique index in migration 0046 is
// what makes that true under contention rather than merely intended: two concurrent activations cannot
// both commit, so "which definition is serving inference" never depends on which transaction finished
// last.
func (s *PGVersionStore) Activate(parent context.Context, configHash string, atMS int64) error {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("herosagent: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`UPDATE heros_agent_version SET activated_at_ms = NULL
		  WHERE activated_at_ms IS NOT NULL AND config_hash <> $1`, configHash); err != nil {
		return fmt.Errorf("herosagent: standing down the previous active version: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE heros_agent_version SET activated_at_ms = $1
		  WHERE config_hash = $2 AND rehearsal_state = 'passed'`, atMS, configHash)
	if err != nil {
		return fmt.Errorf("herosagent: activating %s: %w", configHash, err)
	}
	// 🔴 Zero rows means the WHERE did not match — the definition is missing or has not passed. Reported
	// rather than committed silently: an activation that changed nothing and returned success is the
	// shape that leaves an operator believing an agent is armed.
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s did not activate — it is either not published or not `passed`",
			ErrRehearsalNotPassed, confighashDisplay(configHash))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("herosagent: commit: %w", err)
	}
	return nil
}

// List returns every version, newest first.
func (s *PGVersionStore) List(parent context.Context) ([]Version, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+versionColumns+` FROM heros_agent_version ORDER BY created_at_ms DESC`)
	if err != nil {
		return nil, fmt.Errorf("herosagent: listing versions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Version{}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// SetRehearsal records a gate verdict and its per-fixture report (task 5.6).
func (s *PGVersionStore) SetRehearsal(parent context.Context, configHash string, state RehearsalState, report string) error {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	_, err := s.db.ExecContext(ctx,
		`UPDATE heros_agent_version SET rehearsal_state = $1, rehearsal_report = $2 WHERE config_hash = $3`,
		string(state), nullString(report), configHash)
	if err != nil {
		return fmt.Errorf("herosagent: recording the rehearsal for %s: %w", configHash, err)
	}
	return nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// MemVersionStore is the in-memory store, for tests and for a deployment with no platform database.
//
// Concurrency-safe, unlike some of its neighbours: publishing is reached from HTTP handlers, and a
// store that races only under load is a store that passes every test and fails in production.
type MemVersionStore struct {
	mu sync.RWMutex
	m  map[string]Version
}

// NewMemVersionStore returns an empty in-memory store.
func NewMemVersionStore() *MemVersionStore { return &MemVersionStore{m: map[string]Version{}} }

func (s *MemVersionStore) Put(_ context.Context, v Version) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.m[v.ConfigHash]; exists {
		return nil // immutable: content determines identity
	}
	s.m[v.ConfigHash] = v
	return nil
}

func (s *MemVersionStore) Get(_ context.Context, h string) (Version, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[h]
	return v, ok, nil
}

func (s *MemVersionStore) Active(_ context.Context) (Version, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.m {
		if v.Active() {
			return v, true, nil
		}
	}
	return Version{}, false, nil
}

func (s *MemVersionStore) Activate(_ context.Context, h string, atMS int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[h]
	if !ok {
		return fmt.Errorf("%w: %s is not published", ErrInvalidDefinition, confighashDisplay(h))
	}
	if v.RehearsalState != RehearsalPassed {
		return fmt.Errorf("%w: %s is %q", ErrRehearsalNotPassed, confighashDisplay(h), v.RehearsalState)
	}
	// Exactly one active, here too. The in-memory store is what tests run against, and a store that
	// permitted two would let a test pass on behaviour the database refuses.
	for k, other := range s.m {
		if other.Active() && k != h {
			// 🔴 nil, which is the `SET activated_at_ms = NULL` the Postgres store issues. Writing a 0
			// here would leave the row satisfying the database's own `IS NOT NULL` predicate — two rows
			// in the state the partial unique index forbids, in the store most tests run against.
			other.ActivatedAtMS = nil
			s.m[k] = other
		}
	}
	at := atMS
	v.ActivatedAtMS = &at
	s.m[h] = v
	return nil
}

func (s *MemVersionStore) List(_ context.Context) ([]Version, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Version, 0, len(s.m))
	for _, v := range s.m {
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAtMS > out[j].CreatedAtMS })
	return out, nil
}

// SetRehearsal records a gate verdict.
func (s *MemVersionStore) SetRehearsal(_ context.Context, h string, state RehearsalState, report string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[h]
	if !ok {
		return fmt.Errorf("%w: %s is not published", ErrInvalidDefinition, confighashDisplay(h))
	}
	v.RehearsalState, v.RehearsalReport = state, report
	s.m[h] = v
	return nil
}
