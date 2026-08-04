package adminstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/adminops"
)

// modelregistry.go is the durable backing for the operator model registry (migration 0036).
//
// See the migration header for why `model_entry` could not be reused: it is the P10 prompt/model
// ENVELOPE store, an opaque blob keyed by version, with no column for a provider, a price reference or
// a deprecation. This is a different record that shares a noun.

// ModelRegistry is the Postgres-backed model registry store.
type ModelRegistry struct{ db *sql.DB }

// NewModelRegistry wraps a live platform pool.
func NewModelRegistry(db *sql.DB) (*ModelRegistry, error) {
	if db == nil {
		return nil, errors.New("adminstore: the model registry needs the platform database — held in memory, an added model is lost on restart and SUM silently re-derives without it")
	}
	return &ModelRegistry{db: db}, nil
}

func (m *ModelRegistry) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), writeTimeout)
}

// PutModel inserts or replaces one model record.
func (m *ModelRegistry) PutModel(rec adminops.ModelRecord) error {
	ctx, cancel := m.ctx()
	defer cancel()
	updated := rec.UpdatedAt
	if updated.IsZero() {
		updated = time.Now().UTC()
	}
	// NULL rather than the zero time when not deprecated: the schema's CHECK pairs the flag with the
	// timestamp, and a zero time would both violate it and read as "deprecated in year 1".
	var deprecatedAt any
	if rec.Deprecated {
		at := rec.DeprecatedAt
		if at.IsZero() {
			at = updated
		}
		deprecatedAt = at
	}
	revision := rec.Revision
	if revision < 1 {
		revision = 1
	}
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO admin_model (model_id, provider, price_ref, deprecated, deprecated_at, updated_at, revision)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (model_id) DO UPDATE SET
			provider      = EXCLUDED.provider,
			price_ref     = EXCLUDED.price_ref,
			deprecated    = EXCLUDED.deprecated,
			deprecated_at = EXCLUDED.deprecated_at,
			updated_at    = EXCLUDED.updated_at,
			revision      = EXCLUDED.revision`,
		rec.ModelID, rec.Provider, rec.PriceRef, rec.Deprecated, deprecatedAt, updated, revision)
	if err != nil {
		return fmt.Errorf("adminstore: put model %s: %w", rec.ModelID, err)
	}
	return nil
}

// ClosePeriod records the price references in force at the moment a period closed.
//
// 🔴 `DO NOTHING` on conflict, never an update. Closing a period twice must not re-snapshot today's
// references onto a period that already closed — that is retroactive repricing, and it is the exact
// thing the in-memory registry's "closing twice is a no-op" comment protects against. Enforcing it here
// too means a caller that loses the in-process guard cannot rewrite history through this door.
func (m *ModelRegistry) ClosePeriod(periodID string, priceRefs map[string]string) error {
	ctx, cancel := m.ctx()
	defer cancel()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("adminstore: close period %s: %w", periodID, err)
	}
	defer func() { _ = tx.Rollback() }()
	// One transaction: a period whose snapshot is half-written would resolve some models at their
	// closing reference and the rest at today's, which is worse than either.
	for modelID, ref := range priceRefs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO admin_model_closed_price (period_id, model_id, price_ref)
			VALUES ($1, $2, $3) ON CONFLICT (period_id, model_id) DO NOTHING`,
			periodID, modelID, ref); err != nil {
			return fmt.Errorf("adminstore: close period %s (model %s): %w", periodID, modelID, err)
		}
	}
	// A period with NO models still has to be recorded as closed, or `PeriodClosed` answers false and the
	// next call re-snapshots. The sentinel row carries an empty model id, which the CHECK forbids on a
	// real row, so it can never be mistaken for one.
	if len(priceRefs) == 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO admin_model_closed_price (period_id, model_id, price_ref)
			VALUES ($1, '-', '') ON CONFLICT (period_id, model_id) DO NOTHING`, periodID); err != nil {
			return fmt.Errorf("adminstore: close empty period %s: %w", periodID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("adminstore: close period %s: %w", periodID, err)
	}
	return nil
}

// Models reads every model back, for replay at boot.
func (m *ModelRegistry) Models(ctx context.Context) ([]adminops.ModelRecord, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT model_id, provider, price_ref, deprecated, deprecated_at, updated_at, revision
		FROM admin_model ORDER BY model_id`)
	if err != nil {
		return nil, fmt.Errorf("adminstore: read models: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []adminops.ModelRecord
	for rows.Next() {
		var rec adminops.ModelRecord
		var deprecatedAt sql.NullTime
		if err := rows.Scan(&rec.ModelID, &rec.Provider, &rec.PriceRef, &rec.Deprecated,
			&deprecatedAt, &rec.UpdatedAt, &rec.Revision); err != nil {
			return nil, fmt.Errorf("adminstore: scan model: %w", err)
		}
		if deprecatedAt.Valid {
			rec.DeprecatedAt = deprecatedAt.Time
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminstore: read models: %w", err)
	}
	return out, nil
}

// ClosedPeriods reads every closed period's snapshot back, for replay at boot.
func (m *ModelRegistry) ClosedPeriods(ctx context.Context) (map[string]map[string]string, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT period_id, model_id, price_ref FROM admin_model_closed_price ORDER BY period_id, model_id`)
	if err != nil {
		return nil, fmt.Errorf("adminstore: read closed periods: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]map[string]string{}
	for rows.Next() {
		var periodID, modelID, ref string
		if err := rows.Scan(&periodID, &modelID, &ref); err != nil {
			return nil, fmt.Errorf("adminstore: scan closed period: %w", err)
		}
		if out[periodID] == nil {
			out[periodID] = map[string]string{}
		}
		// The empty-period sentinel marks the period closed and is not a model.
		if modelID == "-" && ref == "" {
			continue
		}
		out[periodID][modelID] = ref
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminstore: read closed periods: %w", err)
	}
	return out, nil
}
