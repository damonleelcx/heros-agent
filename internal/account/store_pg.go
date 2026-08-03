package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// store_pg.go is the durable account Store, over the `account` table migration 0013 created when P7
// landed — plus the four columns migration 0024 adds.
//
// 🔴 The table was already there; only Go code that used it was missing. What genuinely did NOT exist
// were P8's operator fields: `account.Account` grew Status, SuspensionReason, SuspendedAt and
// QuotaOverrides for the operator console, and 0013's schema has none of them. The in-memory store held
// them, so nothing noticed. A durable store that silently dropped them would let an operator suspend a
// tenant, see it applied, and find the account active again after the next restart.
//
// 0013's CHECKs are stricter than this code would have written, and are respected rather than
// re-invented: `active_plan_id <> ''`, `plan_config_version <> ''`, a handle that cannot look like a
// card number, and `account_consent_timestamped` (gainshare_consent = (consented_at IS NOT NULL)) —
// which is why SetGainshareConsent sends NULL on revocation instead of leaving a stale timestamp.
//
// Billing was unmounted because its only Ledger and its only account Store were in-memory. This is the
// second half: the plan an account is on, and the gainshare consent that authorises charging it, must
// survive a restart or the platform bills against state it invented after the last deploy.
//
// # Read-modify-write is done in SQL, not in Go
//
// MemStore's mutators read the map, edit the struct and write it back under a mutex. The same shape here
// would be a lost update: two operators changing different fields of one account concurrently, each
// writing back a whole row built from what they read, and the second silently reverting the first. So
// every mutator is a single UPDATE naming ONLY the columns it owns, with `RETURNING` to hand back the
// row as it now stands. The database serialises them; nothing is read back and rewritten.
//
// 🔴 No card data. `provider_customer_handle` is the provider's opaque reference and nothing else in
// this file touches an instrument. A column here holding one is a PCI surface this system has never had.

// PGStore is the durable account Store.
type PGStore struct{ db *sql.DB }

// NewPGStore returns a durable Store over an open Postgres handle.
func NewPGStore(db *sql.DB) (*PGStore, error) {
	if db == nil {
		return nil, errors.New("account: nil database")
	}
	return &PGStore{db: db}, nil
}

func (p *PGStore) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

const accountColumns = `customer_id, provider_customer_handle, active_plan_id, plan_config_version,
	gainshare_consent, consented_at, created_at, status, suspension_reason, suspended_at, quota_overrides`

// Create records a new account. The provider handle is validated first, exactly as MemStore does — the
// rule belongs to the type, not to one implementation.
func (p *PGStore) Create(a Account) (Account, error) {
	if strings.TrimSpace(a.CustomerID) == "" {
		return Account{}, ErrEmptyCustomer
	}
	h, err := NewHandle(a.ProviderCustomerHandle)
	if err != nil {
		return Account{}, err
	}
	a.ProviderCustomerHandle = h
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Unix(0, 0).UTC()
	}
	overrides, err := json.Marshal(a.QuotaOverrides)
	if err != nil {
		return Account{}, fmt.Errorf("account: encode quota overrides for %s: %w", a.CustomerID, err)
	}
	if len(a.QuotaOverrides) == 0 {
		overrides = []byte("{}")
	}

	ctx, cancel := p.ctx()
	defer cancel()

	// ON CONFLICT DO NOTHING plus a zero row count, rather than a prior SELECT: the existence check and
	// the insert must be one operation or two concurrent signups for one customer both succeed.
	res, err := p.db.ExecContext(ctx,
		`INSERT INTO account (`+accountColumns+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		 ON CONFLICT (customer_id) DO NOTHING`,
		a.CustomerID, a.ProviderCustomerHandle, a.ActivePlanID, a.PlanConfigVersion,
		a.GainshareConsent, nullTime(a.ConsentedAt), a.CreatedAt.UTC(), string(a.Status),
		a.SuspensionReason, nullTime(a.SuspendedAt), overrides)
	if err != nil {
		return Account{}, fmt.Errorf("account: create %s: %w", a.CustomerID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Account{}, fmt.Errorf("account: create %s: %w", a.CustomerID, err)
	}
	if n == 0 {
		return Account{}, fmt.Errorf("%w: %s", ErrExists, a.CustomerID)
	}
	return a, nil
}

// Get returns the account, or ErrNotFound.
func (p *PGStore) Get(customerID string) (Account, error) {
	ctx, cancel := p.ctx()
	defer cancel()

	row := p.db.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM account WHERE customer_id = $1`, customerID)
	a, err := scanAccount(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Account{}, fmt.Errorf("%w: %s", ErrNotFound, customerID)
	case err != nil:
		return Account{}, fmt.Errorf("account: get %s: %w", customerID, err)
	}
	return a, nil
}

// update runs one column-scoped UPDATE ... RETURNING and maps a missing row onto ErrNotFound.
func (p *PGStore) update(customerID, set string, args ...any) (Account, error) {
	ctx, cancel := p.ctx()
	defer cancel()

	full := append([]any{customerID}, args...)
	row := p.db.QueryRowContext(ctx,
		`UPDATE account SET `+set+` WHERE customer_id = $1 RETURNING `+accountColumns, full...)
	a, err := scanAccount(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Account{}, fmt.Errorf("%w: %s", ErrNotFound, customerID)
	case err != nil:
		return Account{}, fmt.Errorf("account: update %s: %w", customerID, err)
	}
	return a, nil
}

// SetPlan repoints the plan and pins the config version it resolved under. Both columns move in one
// statement: a plan id without its version cannot explain a closed period.
func (p *PGStore) SetPlan(customerID, planID, planConfigVersion string) (Account, error) {
	if planConfigVersion == "" {
		return Account{}, errors.New("account: a plan change must pin the plan_config_version it resolved under")
	}
	return p.update(customerID, `active_plan_id = $2, plan_config_version = $3`, planID, planConfigVersion)
}

// SetGainshareConsent records consent or its revocation. Revocation clears consented_at, which the
// table's CHECK also enforces — consent without a timestamp is a state nobody can audit.
func (p *PGStore) SetGainshareConsent(customerID string, consented bool, at time.Time) (Account, error) {
	var ts any
	if consented {
		ts = at.UTC()
	}
	return p.update(customerID, `gainshare_consent = $2, consented_at = $3`, consented, ts)
}

// SetStatus suspends or reactivates. A suspension REQUIRES a reason — an unexplained one is
// indistinguishable from a mistake when the customer calls.
func (p *PGStore) SetStatus(customerID string, status Status, reason string, at time.Time) (Account, error) {
	switch status {
	case StatusActive, StatusSuspended:
	default:
		return Account{}, fmt.Errorf("account: unknown status %q", status)
	}
	if status == StatusSuspended && strings.TrimSpace(reason) == "" {
		return Account{}, errors.New("account: a suspension requires a recorded reason")
	}
	if status == StatusSuspended {
		return p.update(customerID, `status = $2, suspension_reason = $3, suspended_at = $4`,
			string(status), reason, at.UTC())
	}
	// Reactivation clears both, restoring the prior state rather than leaving a stale reason attached
	// to an active account.
	return p.update(customerID, `status = $2, suspension_reason = '', suspended_at = NULL`, string(status))
}

// SetQuota sets or clears one per-tenant allowance override. A NaN value CLEARS it.
//
// The override is deleted from the document rather than written as zero, because an absent key means
// "resolve from the plan" and a zero means "an allowance of nothing". Those are opposite instructions,
// and jsonb_set/`-` is what keeps the other limits untouched by a single-key edit.
func (p *PGStore) SetQuota(customerID, limit string, value float64) (Account, error) {
	if strings.TrimSpace(limit) == "" {
		return Account{}, errors.New("account: a quota override must name the limit it overrides")
	}
	if math.IsNaN(value) {
		return p.update(customerID, `quota_overrides = quota_overrides - $2::text`, limit)
	}
	return p.update(customerID, `quota_overrides = jsonb_set(quota_overrides, ARRAY[$2::text], to_jsonb($3::double precision), true)`,
		limit, value)
}

// List returns every account, ordered by customer id.
func (p *PGStore) List() ([]Account, error) {
	ctx, cancel := p.ctx()
	defer cancel()

	rows, err := p.db.QueryContext(ctx, `SELECT `+accountColumns+` FROM account ORDER BY customer_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("account: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Account, 0)
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("account: list: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("account: list: %w", err)
	}
	return out, nil
}

type scanner interface{ Scan(...any) error }

func scanAccount(sc scanner) (Account, error) {
	var a Account
	var status string
	var overrides []byte
	var consented, suspended sql.NullTime
	if err := sc.Scan(&a.CustomerID, &a.ProviderCustomerHandle, &a.ActivePlanID, &a.PlanConfigVersion,
		&a.GainshareConsent, &consented, &a.CreatedAt, &status, &a.SuspensionReason, &suspended,
		&overrides); err != nil {
		return Account{}, err
	}
	a.Status = Status(status)
	if consented.Valid {
		t := consented.Time.UTC()
		a.ConsentedAt = &t
	}
	if suspended.Valid {
		t := suspended.Time.UTC()
		a.SuspendedAt = &t
	}
	if len(overrides) > 0 {
		if err := json.Unmarshal(overrides, &a.QuotaOverrides); err != nil {
			return Account{}, fmt.Errorf("decode quota overrides for %s: %w", a.CustomerID, err)
		}
		// An empty document becomes a nil map rather than an empty one, so `QuotaOverride` reports
		// "no override" identically whichever store served the row.
		if len(a.QuotaOverrides) == 0 {
			a.QuotaOverrides = nil
		}
	}
	return a, nil
}

// nullTime renders an optional timestamp for the driver. NULL rather than the zero time: "never
// consented" and "consented at year zero" must not be the same value on a consent record.
func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}
