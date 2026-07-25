// Package studio meters and executes P10 studio test-runs (tasks 5.1–5.4).
//
// # Why studio spend is its own kind, in its own meter
//
// Studio traffic is exploration: a user trying a model, previewing a prompt, comparing two versions.
// P4 eval traffic is measurement: the numbers optimisation decisions are made on. If exploratory
// spend were folded into eval cost, it would corrupt exactly those numbers (design.md Decision 8). So
// studio has its OWN spend kind and its OWN meter — the separation is structural, not a convention a
// reader has to trust: a studio charge cannot reach an eval report because the two are different
// objects. This mirrors evalrun's per-kind SpendReport shape (task 5.2) without sharing its ledger.
package studio

import (
	"errors"
	"sort"
	"sync"
)

// SpendKind names what a studio charge was for. One kind today — the point is that it is DISTINCT
// from evalrun's judge/generation/execution kinds, so studio cost never appears within eval cost.
type SpendKind string

const SpendStudio SpendKind = "studio"

// ErrCapExhausted is returned when a charge (or a pre-check before one) would breach a configured cap.
// It is a first-class, typed signal — NOT an error condition — because exhausting a studio budget is
// configured behaviour, not a failure: the studio stops and says so (task 5.3).
var ErrCapExhausted = errors.New("studio: spend cap reached")

// Caller is who a studio execution is billed to: a user within a tenant. Both are capped independently
// (task 5.3) — a single user cannot exhaust the tenant's budget alone, and the tenant total bounds the
// sum of its users.
type Caller struct {
	TenantID string
	UserID   string
}

func (c Caller) userKey() string { return c.TenantID + "\x1f" + c.UserID }

// Cap is the studio spend limit. A nil pointer means "no cap for this scope" — distinguishable from a
// zero cap, which would refuse every execution (the same pointer convention evalrun.Budget uses).
type Cap struct {
	PerUserUSD   *float64 `json:"per_user_usd,omitempty"`
	PerTenantUSD *float64 `json:"per_tenant_usd,omitempty"`
}

// SpendMeter accumulates studio spend per user and per tenant and enforces the caps. Safe for
// concurrent use: a user may drive several studio panels at once.
type SpendMeter struct {
	cap Cap

	mu        sync.Mutex
	byUser    map[string]float64
	byTenant  map[string]float64
	callCount map[string]int
}

// NewSpendMeter builds a studio spend meter with the given caps.
func NewSpendMeter(cap Cap) *SpendMeter {
	return &SpendMeter{
		cap:       cap,
		byUser:    map[string]float64{},
		byTenant:  map[string]float64{},
		callCount: map[string]int{},
	}
}

// Allow reports whether a caller has budget left for at least one more execution, WITHOUT charging.
// The runner calls this BEFORE contacting a provider, so a caller already at cap is stopped before any
// money is spent — the cap stops execution rather than overspending (task 5.3/5.4). It returns the
// scope that is exhausted ("user" or "tenant") for a message that says which limit stopped the run.
func (m *SpendMeter) Allow(c Caller) (ok bool, exhausted string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cap.PerUserUSD != nil && m.byUser[c.userKey()] >= *m.cap.PerUserUSD {
		return false, "user"
	}
	if m.cap.PerTenantUSD != nil && m.byTenant[c.TenantID] >= *m.cap.PerTenantUSD {
		return false, "tenant"
	}
	return true, ""
}

// Charge records actual spend after an execution. It never rejects — the pre-flight Allow is the gate,
// and a single in-flight call is allowed to complete and be recorded even if it lands slightly over,
// which is why Allow is checked first and Charge only accounts. Recording after the fact keeps the
// running total honest for the NEXT Allow.
func (m *SpendMeter) Charge(c Caller, usd float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byUser[c.userKey()] += usd
	m.byTenant[c.TenantID] += usd
	m.callCount[c.userKey()]++
}

// SpendReport is the studio spend readout for one caller — the same by-kind shape evalrun.SpendReport
// uses (task 5.2), but sourced from the studio ledger so the two never mix.
type SpendReport struct {
	TenantID  string                `json:"tenant_id"`
	UserID    string                `json:"user_id"`
	ByKind    map[SpendKind]float64 `json:"by_kind"`
	Calls     int                   `json:"calls"`
	TotalUSD  float64               `json:"total_usd"`
	Cap       Cap                   `json:"cap"`
	Exhausted []string              `json:"exhausted,omitempty"`
}

// Report returns the studio spend for a caller.
func (m *SpendMeter) Report(c Caller) SpendReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := m.byUser[c.userKey()]
	var exhausted []string
	if m.cap.PerUserUSD != nil && total >= *m.cap.PerUserUSD {
		exhausted = append(exhausted, "user")
	}
	if m.cap.PerTenantUSD != nil && m.byTenant[c.TenantID] >= *m.cap.PerTenantUSD {
		exhausted = append(exhausted, "tenant")
	}
	sort.Strings(exhausted)
	return SpendReport{
		TenantID: c.TenantID, UserID: c.UserID,
		ByKind:    map[SpendKind]float64{SpendStudio: total},
		Calls:     m.callCount[c.userKey()],
		TotalUSD:  total,
		Cap:       m.cap,
		Exhausted: exhausted,
	}
}
