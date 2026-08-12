package herosagent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// capstore.go is the durable half of caps, spend and the stale mark, over migration 0048.

// PGCapStore is the durable ceiling store.
type PGCapStore struct{ db *sql.DB }

// NewPGCapStore returns a cap store over an open Postgres handle.
func NewPGCapStore(db *sql.DB) (*PGCapStore, error) {
	if db == nil {
		return nil, errors.New("herosagent: nil database")
	}
	return &PGCapStore{db: db}, nil
}

func (s *PGCapStore) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 10*time.Second)
}

// Get returns one ceiling. 🔴 ok=false is NO CAP — unbounded — never a zero the caller could compare.
func (s *PGCapStore) Get(parent context.Context, tenantID string) (Cap, bool, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	var c Cap
	c.TenantID = tenantID
	err := s.db.QueryRowContext(ctx,
		`SELECT max_tokens, reason, set_by, updated_at_ms FROM heros_cap WHERE tenant_id = $1`,
		tenantID).Scan(&c.MaxTokens, &c.Reason, &c.SetBy, &c.UpdatedAtMS)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Cap{}, false, nil
	case err != nil:
		return Cap{}, false, fmt.Errorf("herosagent: reading the cap for %q: %w", tenantID, err)
	}
	return c, true, nil
}

// Set writes a ceiling. The schema refuses a zero; this refuses it earlier so the message is ours.
func (s *PGCapStore) Set(parent context.Context, c Cap) error {
	if c.MaxTokens <= 0 {
		return fmt.Errorf("%w: a ceiling of %d is not a ceiling. `0` is ambiguous between `spend "+
			"nothing` and `no limit`; removing a cap is a delete", ErrInvalidDefinition, c.MaxTokens)
	}
	ctx, cancel := s.ctx(parent)
	defer cancel()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO heros_cap (tenant_id, max_tokens, reason, set_by, updated_at_ms)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (tenant_id) DO UPDATE
		    SET max_tokens = EXCLUDED.max_tokens, reason = EXCLUDED.reason,
		        set_by = EXCLUDED.set_by, updated_at_ms = EXCLUDED.updated_at_ms`,
		c.TenantID, c.MaxTokens, c.Reason, c.SetBy, c.UpdatedAtMS)
	if err != nil {
		return fmt.Errorf("herosagent: setting the cap for %q: %w", c.TenantID, err)
	}
	return nil
}

// Delete removes a ceiling, which is how a cap is removed — see Cap.MaxTokens.
func (s *PGCapStore) Delete(parent context.Context, tenantID string) error {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM heros_cap WHERE tenant_id = $1`, tenantID); err != nil {
		return fmt.Errorf("herosagent: removing the cap for %q: %w", tenantID, err)
	}
	return nil
}

// List returns every ceiling, fleet first.
func (s *PGCapStore) List(parent context.Context) ([]Cap, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	rows, err := s.db.QueryContext(ctx,
		`SELECT tenant_id, max_tokens, reason, set_by, updated_at_ms FROM heros_cap ORDER BY tenant_id`)
	if err != nil {
		return nil, fmt.Errorf("herosagent: listing caps: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Cap{}
	for rows.Next() {
		var c Cap
		if err := rows.Scan(&c.TenantID, &c.MaxTokens, &c.Reason, &c.SetBy, &c.UpdatedAtMS); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PGSpendStore is the durable meter over `heros_spend`.
type PGSpendStore struct{ db *sql.DB }

// NewPGSpendStore returns a meter over an open Postgres handle.
func NewPGSpendStore(db *sql.DB) (*PGSpendStore, error) {
	if db == nil {
		return nil, errors.New("herosagent: nil database")
	}
	return &PGSpendStore{db: db}, nil
}

func (s *PGSpendStore) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 10*time.Second)
}

// Record writes one inference's meter reading.
//
// Idempotent on `(tenant_id, inference_id)`, which is the primary key: a retried write after a lost
// response must not double-count spend, and double-counting is the direction that makes a cap bind
// early — a customer's analysis stopping for tokens nobody spent.
func (s *PGSpendStore) Record(parent context.Context, sp Spend) error {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO heros_spend
		   (tenant_id, inference_id, tokens_in, tokens_out, estimated_cost, priced, created_at_ms)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (tenant_id, inference_id) DO NOTHING`,
		sp.TenantID, sp.InferenceID, sp.TokensIn, sp.TokensOut, sp.EstimatedCost, sp.Priced, sp.CreatedAtMS)
	if err != nil {
		return fmt.Errorf("herosagent: recording spend for %s: %w", sp.InferenceID, err)
	}
	return nil
}

// SpentSince sums tokens over the window, for one tenant or the whole fleet.
//
// 🔴 It sums TOKENS and not cost. A cap on cost would be unenforceable for an unpriced model — the
// exact case task 6.5's `unpriced` word exists for — so a tenant on a model with no published price
// would be uncapped, which is the tenant most likely to be on something new and expensive.
func (s *PGSpendStore) SpentSince(parent context.Context, tenantID string, sinceMS int64) (int64, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	q := `SELECT COALESCE(SUM(tokens_in + tokens_out), 0) FROM heros_spend WHERE created_at_ms >= $1`
	args := []any{sinceMS}
	if tenantID != FleetTenantID {
		q += ` AND tenant_id = $2`
		args = append(args, tenantID)
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("herosagent: reading spend since %d: %w", sinceMS, err)
	}
	return total, nil
}

// MarkStale marks every stored inference for one tenant (task 9.5).
//
// 🔴 It does NOT overwrite an existing mark. A row already stale for `analysis_disabled` that gets
// re-marked when a definition retires would lose the earlier, more specific reason and its timestamp —
// and "since when did this stop being maintained" is the question the timestamp exists to answer.
// First cause wins, because the first cause is when maintenance actually stopped.
func (s *PGSpendStore) MarkStale(parent context.Context, tenantID string, reason StaleReason, atMS int64) (int64, error) {
	if SentenceForStale(reason) == "" {
		return 0, fmt.Errorf("%w: %q is not a stale reason", ErrInvalidDefinition, reason)
	}
	ctx, cancel := s.ctx(parent)
	defer cancel()
	res, err := s.db.ExecContext(ctx,
		`UPDATE heros_inference SET stale_reason = $1, stale_at_ms = $2
		  WHERE tenant_id = $3 AND stale_reason IS NULL`,
		string(reason), atMS, tenantID)
	if err != nil {
		return 0, fmt.Errorf("herosagent: marking %q's inferences stale: %w", tenantID, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ClearStale removes the mark for one tenant, for when analysis is re-enabled.
//
// 🚫 It does NOT re-run anything, and the sentence on the surface says so. Clearing the mark says
// maintenance has resumed; the stored facts are still the ones the agent wrote before the gap, and a
// caller that wanted them refreshed asks for a re-inference, which is a diff somebody confirms.
func (s *PGSpendStore) ClearStale(parent context.Context, tenantID string) (int64, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	res, err := s.db.ExecContext(ctx,
		`UPDATE heros_inference SET stale_reason = NULL, stale_at_ms = NULL
		  WHERE tenant_id = $1 AND stale_reason = $2`,
		tenantID, string(StaleDisabled))
	if err != nil {
		return 0, fmt.Errorf("herosagent: clearing %q's stale marks: %w", tenantID, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
