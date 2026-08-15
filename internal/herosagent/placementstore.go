package herosagent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// placementstore.go holds per-tenant placement over migration 0047.
//
// 🔴 THE ABSENCE OF A ROW IS A VALUE. `Get` returns `(disabled, defaulted=false)` when nobody has
// written one, and a caller that needs the difference gets it in the second return rather than having
// to compare against a constant. Q2 made `disabled` the default precisely so nothing analyses anything
// on deploy; `adminops` renders "defaulted" and "explicitly disabled" differently, and it can only do
// that if the store never invents a row.

// TenantPlacement is one tenant's setting, as an operator left it.
type TenantPlacement struct {
	TenantID  string
	Placement Placement
	// Explicit is false when NOBODY HAS DECIDED — the effective placement is `disabled` because that is
	// the default, not because anybody chose it.
	Explicit    bool
	Reason      string
	SetBy       string
	UpdatedAtMS int64
}

// PGPlacementStore is the durable store.
type PGPlacementStore struct{ db *sql.DB }

// NewPGPlacementStore returns a store over an open Postgres handle.
func NewPGPlacementStore(db *sql.DB) (*PGPlacementStore, error) {
	if db == nil {
		return nil, errors.New("herosagent: nil database")
	}
	return &PGPlacementStore{db: db}, nil
}

func (s *PGPlacementStore) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 10*time.Second)
}

// Get reads one tenant's placement. 🔴 A missing row is `disabled`, NOT explicit, and NOT an error.
func (s *PGPlacementStore) Get(parent context.Context, tenantID string) (TenantPlacement, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()

	out := TenantPlacement{TenantID: tenantID, Placement: PlacementDisabled}
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT placement, reason, set_by, updated_at_ms FROM heros_tenant_placement WHERE tenant_id = $1`,
		tenantID).Scan(&value, &out.Reason, &out.SetBy, &out.UpdatedAtMS)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return out, nil
	case err != nil:
		return TenantPlacement{}, fmt.Errorf("herosagent: reading the placement for %s: %w", tenantID, err)
	}
	p, perr := ParsePlacement(value)
	if perr != nil {
		// 🔴 A stored value outside the vocabulary is an ERROR, never coerced to `disabled`. Coercing
		// would turn a corrupt row into a silently-safe one — and the row a corruption is most likely to
		// have been is `platform`, which is the one whose loss an operator most needs to hear about.
		return TenantPlacement{}, fmt.Errorf("herosagent: tenant %s carries stored placement %q: %w",
			tenantID, value, perr)
	}
	out.Placement, out.Explicit = p, true
	return out, nil
}

// Set records a decision. A reason is required — see migration 0047's CHECK, which is the half that
// holds when a row is written by something other than the service.
func (s *PGPlacementStore) Set(parent context.Context, tp TenantPlacement) error {
	if _, err := ParsePlacement(string(tp.Placement)); err != nil {
		return err
	}
	if strings.TrimSpace(tp.Reason) == "" {
		return fmt.Errorf("%w: setting a placement requires a reason — `%s` is what makes this platform "+
			"read that tenant's source under a platform-held credential", ErrInvalidDefinition, PlacementPlatform)
	}
	ctx, cancel := s.ctx(parent)
	defer cancel()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO heros_tenant_placement (tenant_id, placement, reason, set_by, updated_at_ms)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (tenant_id) DO UPDATE
		    SET placement = EXCLUDED.placement, reason = EXCLUDED.reason,
		        set_by = EXCLUDED.set_by, updated_at_ms = EXCLUDED.updated_at_ms`,
		tp.TenantID, string(tp.Placement), tp.Reason, tp.SetBy, tp.UpdatedAtMS)
	if err != nil {
		return fmt.Errorf("herosagent: setting the placement for %s: %w", tp.TenantID, err)
	}
	return nil
}

// List returns every EXPLICIT placement. A tenant with no row is absent, which is what lets a caller
// count how much of the fleet anybody has actually reviewed.
func (s *PGPlacementStore) List(parent context.Context) ([]TenantPlacement, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	rows, err := s.db.QueryContext(ctx,
		`SELECT tenant_id, placement, reason, set_by, updated_at_ms
		   FROM heros_tenant_placement ORDER BY tenant_id`)
	if err != nil {
		return nil, fmt.Errorf("herosagent: listing placements: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []TenantPlacement{}
	for rows.Next() {
		var tp TenantPlacement
		var value string
		if err := rows.Scan(&tp.TenantID, &value, &tp.Reason, &tp.SetBy, &tp.UpdatedAtMS); err != nil {
			return nil, err
		}
		p, perr := ParsePlacement(value)
		if perr != nil {
			return nil, fmt.Errorf("herosagent: tenant %s carries stored placement %q: %w",
				tp.TenantID, value, perr)
		}
		tp.Placement, tp.Explicit = p, true
		out = append(out, tp)
	}
	return out, rows.Err()
}

// MemPlacementStore is the in-memory placement store, for tests and for a deployment with no platform
// database. Concurrency-safe, like its neighbours.
type MemPlacementStore struct {
	mu sync.RWMutex
	m  map[string]TenantPlacement
}

// NewMemPlacementStore returns an empty in-memory placement store.
func NewMemPlacementStore() *MemPlacementStore {
	return &MemPlacementStore{m: map[string]TenantPlacement{}}
}

// Get reads one tenant's placement, with the same "absence is a value" contract the durable store has.
func (s *MemPlacementStore) Get(_ context.Context, tenantID string) (TenantPlacement, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if tp, ok := s.m[tenantID]; ok {
		return tp, nil
	}
	return TenantPlacement{TenantID: tenantID, Placement: PlacementDisabled}, nil
}

// Set records a decision.
func (s *MemPlacementStore) Set(_ context.Context, tp TenantPlacement) error {
	if _, err := ParsePlacement(string(tp.Placement)); err != nil {
		return err
	}
	if strings.TrimSpace(tp.Reason) == "" {
		return fmt.Errorf("%w: setting a placement requires a reason", ErrInvalidDefinition)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tp.Explicit = true
	s.m[tp.TenantID] = tp
	return nil
}

// List returns every explicit placement.
func (s *MemPlacementStore) List(_ context.Context) ([]TenantPlacement, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]TenantPlacement, 0, len(s.m))
	for _, tp := range s.m {
		out = append(out, tp)
	}
	return out, nil
}

// PlacementExecer is the transaction handle SetPlacementWithin writes through. Structural, so
// `tenancy.Execer` and `*sql.Tx` both satisfy it without this package importing either.
type PlacementExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// SetPlacementWithin writes a placement inside a transaction the CALLER owns.
//
// # Why this exists beside Set
//
// Sign-up creates the tenant, the user, the membership and the account in ONE transaction, so that a
// half-created organization is not a state the system can be left in. A placement seeded after that
// transaction commits would be a fifth write outside it — and the window between them is exactly long
// enough for a crash to leave an organization whose placement says nobody ever decided, on a
// deployment whose policy is that every new organization is decided at creation.
//
// 🔴 It writes an EXPLICIT row rather than changing what an absent row means. The default stays
// `disabled` (Q2) and `TenantPlacement.Explicit` keeps meaning "somebody decided": a seeded org has a
// row, a reason and a set_by, and can be individually disabled afterwards. Flipping the read default
// instead would enable every tenant at once, leave no record of the choice for any of them, and remove
// the per-tenant off switch — three losses for the same effect.
func SetPlacementWithin(ctx context.Context, ex PlacementExecer, tp TenantPlacement) error {
	if ex == nil {
		return fmt.Errorf("herosagent: SetPlacementWithin needs a transaction")
	}
	if _, err := ParsePlacement(string(tp.Placement)); err != nil {
		return err
	}
	if strings.TrimSpace(tp.Reason) == "" {
		return fmt.Errorf("%w: seeding a placement requires a reason — `%s` is what makes this platform "+
			"read that tenant's source under a platform-held credential", ErrInvalidDefinition, PlacementPlatform)
	}
	_, err := ex.ExecContext(ctx,
		`INSERT INTO heros_tenant_placement (tenant_id, placement, reason, set_by, updated_at_ms)
		 VALUES ($1,$2,$3,$4,$5)
		 -- DO NOTHING, never DO UPDATE. A seed is the value for an organization nobody has decided
		 -- about yet; if a row already exists then somebody DID decide, and a creation-time default
		 -- must never overwrite an operator's decision.
		 ON CONFLICT (tenant_id) DO NOTHING`,
		tp.TenantID, string(tp.Placement), tp.Reason, tp.SetBy, tp.UpdatedAtMS)
	if err != nil {
		return fmt.Errorf("herosagent: seeding the placement for %s: %w", tp.TenantID, err)
	}
	return nil
}
