// Package account is the P7 customer model: who is billed, on which plan, under which published plan
// configuration, and whether they consented to gainshare.
//
// THE LOAD-BEARING RULE: the platform holds provider HANDLES only. Card data stays with the
// PCI-compliant billing provider, which keeps PCI scope out of this system entirely (design
// Decision 10). That is not a comment — NewHandle rejects anything that looks like a primary account
// number, so a handle field can never quietly become a card number because one integration passed the
// wrong string.
//
// The account pins `plan_config_version` alongside `active_plan_id`. Both are needed: the plan id says
// WHICH plan, the version says which PUBLISHED DEFINITION of it was in force. Without the version, a
// closed period can no longer be explained after the next config publish — the account would resolve
// against today's limits and disagree with the invoice it already produced.
package account

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status is an account's lifecycle state, administered by the P8 operator console (P8 FR7).
//
// It lives on the account rather than in the operator console's own store because the account IS the
// tenant's system of record — a second "is this tenant suspended" table would be a place for the two
// answers to disagree, and the one that matters (may this tenant's loop merge) would be read from
// whichever the caller happened to know about.
type Status string

const (
	// StatusActive is the normal state. An account with no explicit status is treated as active, so
	// existing rows and fixtures keep their behaviour.
	StatusActive Status = "active"
	// StatusSuspended halts the tenant: its autonomous optimizer merges no further while suspended
	// (P8 FR7). Reversible by reactivation, which restores the prior state.
	StatusSuspended Status = "suspended"
)

// Suspended reports whether s halts the tenant. Unset is active — the expand-only default.
func (s Status) Suspended() bool { return s == StatusSuspended }

// Account is one billable customer.
type Account struct {
	CustomerID string `json:"customer_id"`
	// ProviderCustomerHandle is the billing provider's OPAQUE customer reference. No card data — ever.
	ProviderCustomerHandle string `json:"provider_customer_handle"`
	ActivePlanID           string `json:"active_plan_id"`
	// PlanConfigVersion is the config-store version the active plan was resolved under.
	PlanConfigVersion string `json:"plan_config_version"`
	// GainshareConsent is the informed, recorded, REVOCABLE consent to verified-savings billing. It is a
	// contract state, not a preference: gainshare may not be charged without it.
	GainshareConsent bool       `json:"gainshare_consent"`
	ConsentedAt      *time.Time `json:"consented_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`

	// Status is the lifecycle state the operator console administers. Empty means active.
	Status Status `json:"status,omitempty"`
	// SuspensionReason is the operator's recorded reason for the current suspension, and
	// SuspendedAt is when it was applied. Both are cleared on reactivation. They are the account's own
	// copy of what the audit chain records; the chain is the evidence, these are what the tenant view
	// shows without a chain query.
	SuspensionReason string     `json:"suspension_reason,omitempty"`
	SuspendedAt      *time.Time `json:"suspended_at,omitempty"`
	// QuotaOverrides are per-tenant allowance overrides an operator set (P8 FR7's SetQuota), keyed by
	// the plan limit's name. An override REPLACES the plan's allowance for that one limit and leaves
	// every other limit resolving from plan configuration.
	//
	// Keyed by string rather than by plancfg.Limit so the account model does not depend on the plan
	// package; entitlement, which owns limit semantics, does the conversion. An absent key means "no
	// override" — never zero, which would read as an allowance of nothing.
	QuotaOverrides map[string]float64 `json:"quota_overrides,omitempty"`
}

// Active reports whether the account is not suspended.
func (a Account) Active() bool { return !a.Status.Suspended() }

// QuotaOverride returns the operator-set allowance for a limit, if one is set.
func (a Account) QuotaOverride(limit string) (float64, bool) {
	v, ok := a.QuotaOverrides[limit]
	return v, ok
}

// Errors the store returns. They are distinguishable because callers act differently on each: a missing
// account is a setup problem, a bad handle is a security problem.
var (
	ErrNotFound      = errors.New("account: no such customer")
	ErrExists        = errors.New("account: customer already exists")
	ErrCardData      = errors.New("account: refusing to store a value that looks like card data — the platform holds provider handles only")
	ErrEmptyHandle   = errors.New("account: provider customer handle is required")
	ErrEmptyCustomer = errors.New("account: customer_id is required")
)

// digitsOnly matches a run of 12–19 digits after separators are stripped: the shape of every card PAN
// in circulation. Deliberately WIDER than "16 digits" — the point is to reject the family, not to
// validate one issuer.
var digitsOnly = regexp.MustCompile(`^\d{12,19}$`)

// sepStripper removes the separators a pasted card number carries.
var sepStripper = strings.NewReplacer(" ", "", "-", "", ".", "")

// NewHandle validates an opaque provider customer handle before it can be stored.
//
// It refuses two things: an empty handle (a customer with no provider identity cannot be billed, and
// storing the empty string would defer that discovery to the first charge) and anything PAN-shaped.
// The PAN check is a Luhn-validated digit run, so a legitimate all-digit provider id is not rejected by
// accident while an actual card number is.
func NewHandle(s string) (string, error) {
	h := strings.TrimSpace(s)
	if h == "" {
		return "", ErrEmptyHandle
	}
	if bare := sepStripper.Replace(h); digitsOnly.MatchString(bare) && luhn(bare) {
		return "", fmt.Errorf("%w (got a %d-digit Luhn-valid number)", ErrCardData, len(bare))
	}
	return h, nil
}

// luhn reports whether a digit string passes the Luhn checksum every card PAN satisfies.
func luhn(s string) bool {
	sum, alt := 0, false
	for i := len(s) - 1; i >= 0; i-- {
		d := int(s[i] - '0')
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}

// Store is the account system of record.
type Store interface {
	Create(a Account) (Account, error)
	Get(customerID string) (Account, error)
	// SetPlan repoints the account at a plan AND the config version that plan was resolved under. The
	// two move together on purpose: a plan id without its version cannot explain a closed period.
	SetPlan(customerID, planID, planConfigVersion string) (Account, error)
	// SetGainshareConsent records consent or its REVOCATION. Revocation clears consented_at; consent
	// stamps it. Both are ordinary updates — the audit of the change lives in the billing ledger.
	SetGainshareConsent(customerID string, consented bool, at time.Time) (Account, error)
	// SetStatus suspends or reactivates the account (P8 FR7). The reason is stored with the
	// suspension and cleared on reactivation; the AUDIT of the change lives in the P8 audit chain,
	// which this store neither owns nor can write.
	SetStatus(customerID string, status Status, reason string, at time.Time) (Account, error)
	// SetQuota sets or clears one per-tenant allowance override (P8 FR7). A NaN value clears the
	// override, so "back to the plan's allowance" is expressible without a second method.
	SetQuota(customerID, limit string, value float64) (Account, error)
	List() []Account
}

// MemStore is an in-memory Store for the default path, the demo, and hermetic tests.
type MemStore struct {
	mu sync.RWMutex
	by map[string]Account
}

// NewMemStore builds an empty account store.
func NewMemStore() *MemStore { return &MemStore{by: map[string]Account{}} }

// Create records a new account, validating the provider handle first.
func (s *MemStore) Create(a Account) (Account, error) {
	if strings.TrimSpace(a.CustomerID) == "" {
		return Account{}, ErrEmptyCustomer
	}
	h, err := NewHandle(a.ProviderCustomerHandle)
	if err != nil {
		return Account{}, err
	}
	a.ProviderCustomerHandle = h
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.by[a.CustomerID]; ok {
		return Account{}, fmt.Errorf("%w: %s", ErrExists, a.CustomerID)
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Unix(0, 0).UTC()
	}
	s.by[a.CustomerID] = a
	return a, nil
}

// Get returns the account, or ErrNotFound.
func (s *MemStore) Get(customerID string) (Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.by[customerID]
	if !ok {
		return Account{}, fmt.Errorf("%w: %s", ErrNotFound, customerID)
	}
	return a, nil
}

// SetPlan repoints the account's plan and pins the config version it resolved under.
func (s *MemStore) SetPlan(customerID, planID, planConfigVersion string) (Account, error) {
	if planConfigVersion == "" {
		return Account{}, errors.New("account: a plan change must pin the plan_config_version it resolved under")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.by[customerID]
	if !ok {
		return Account{}, fmt.Errorf("%w: %s", ErrNotFound, customerID)
	}
	a.ActivePlanID, a.PlanConfigVersion = planID, planConfigVersion
	s.by[customerID] = a
	return a, nil
}

// SetGainshareConsent records consent or revocation.
func (s *MemStore) SetGainshareConsent(customerID string, consented bool, at time.Time) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.by[customerID]
	if !ok {
		return Account{}, fmt.Errorf("%w: %s", ErrNotFound, customerID)
	}
	a.GainshareConsent = consented
	if consented {
		t := at.UTC()
		a.ConsentedAt = &t
	} else {
		a.ConsentedAt = nil
	}
	s.by[customerID] = a
	return a, nil
}

// SetStatus suspends or reactivates an account.
//
// A suspension REQUIRES a reason: "why is this tenant halted" is the first question asked when a
// customer calls, and an unexplained suspension is indistinguishable from a mistake. Reactivation
// clears the reason and the timestamp, restoring the prior state.
func (s *MemStore) SetStatus(customerID string, status Status, reason string, at time.Time) (Account, error) {
	switch status {
	case StatusActive, StatusSuspended:
	default:
		return Account{}, fmt.Errorf("account: unknown status %q", status)
	}
	if status == StatusSuspended && strings.TrimSpace(reason) == "" {
		return Account{}, errors.New("account: a suspension requires a recorded reason")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.by[customerID]
	if !ok {
		return Account{}, fmt.Errorf("%w: %s", ErrNotFound, customerID)
	}
	a.Status = status
	if status == StatusSuspended {
		t := at.UTC()
		a.SuspensionReason, a.SuspendedAt = reason, &t
	} else {
		a.SuspensionReason, a.SuspendedAt = "", nil
	}
	s.by[customerID] = a
	return a, nil
}

// SetQuota sets or clears one per-tenant allowance override. A NaN value clears it.
func (s *MemStore) SetQuota(customerID, limit string, value float64) (Account, error) {
	if strings.TrimSpace(limit) == "" {
		return Account{}, errors.New("account: a quota override must name the limit it overrides")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.by[customerID]
	if !ok {
		return Account{}, fmt.Errorf("%w: %s", ErrNotFound, customerID)
	}
	if math.IsNaN(value) {
		delete(a.QuotaOverrides, limit)
	} else {
		if value < 0 {
			return Account{}, fmt.Errorf("account: quota override for %s is negative", limit)
		}
		if a.QuotaOverrides == nil {
			a.QuotaOverrides = map[string]float64{}
		}
		a.QuotaOverrides[limit] = value
	}
	s.by[customerID] = a
	return a, nil
}

// List returns every account in customer-id order.
func (s *MemStore) List() []Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Account, 0, len(s.by))
	for _, a := range s.by {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CustomerID < out[j].CustomerID })
	return out
}
