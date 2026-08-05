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
	//
	// EMPTY means "no billing-provider customer yet", which is what a Free account looks like before its
	// first upgrade. It was mandatory until P27, correctly for a BILLABLE account and wrongly for a free
	// one: sign-up would have had to either fail or register a customer object at a payment provider for
	// every person who ever tried the free tier and never came back. The guarantee is preserved by
	// stating the condition it actually was — see PlanCharges.
	ProviderCustomerHandle string `json:"provider_customer_handle,omitempty"`
	ActivePlanID           string `json:"active_plan_id"`
	// PlanConfigVersion is the config-store version the active plan was resolved under.
	PlanConfigVersion string `json:"plan_config_version"`
	// PlanCharges is whether the active plan costs money, and it is the third thing that moves with the
	// plan id and its version rather than a fourth thing somebody remembers to update.
	//
	// 🔴 It exists because the DATABASE has to hold P27's invariant — *a provider handle may be absent
	// only while the plan charges nothing* — and the database cannot read plan configuration. Without a
	// column the CHECK cannot be written, and "paid plan with no billing customer" goes back to being a
	// state something has to detect rather than a row that cannot exist.
	//
	// It defaults TRUE for the same reason the column does: every account that existed before P27 has a
	// handle, so the strict reading is the safe one and no existing row becomes invalid.
	PlanCharges bool `json:"plan_charges"`
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
	ErrNotFound    = errors.New("account: no such customer")
	ErrExists      = errors.New("account: customer already exists")
	ErrCardData    = errors.New("account: refusing to store a value that looks like card data — the platform holds provider handles only")
	ErrEmptyHandle = errors.New("account: provider customer handle is required")
	// ErrHandleRequired is the P27 invariant, named: an account may hold no provider handle only while
	// its plan charges nothing. It is distinct from ErrEmptyHandle because the two have different
	// remedies — one says "you passed nothing", the other says "mint the customer first".
	ErrHandleRequired = errors.New("account: a plan that charges requires a billing-provider customer")
	ErrEmptyCustomer  = errors.New("account: customer_id is required")
)

// digitsOnly matches a run of 12–19 digits after separators are stripped: the shape of every card PAN
// in circulation. Deliberately WIDER than "16 digits" — the point is to reject the family, not to
// validate one issuer.
var digitsOnly = regexp.MustCompile(`^\d{12,19}$`)

// sepStripper removes the separators a pasted card number carries.
var sepStripper = strings.NewReplacer(" ", "", "-", "", ".", "")

// ValidateHandle checks a provider handle against the plan's charging state.
//
// 🔴 This is the whole of P27's D3, in one function. Before P27 an empty handle was always refused,
// which is correct for a BILLABLE account and makes a free one inexpressible. The guarantee — a customer
// who cannot be billed must not look billable — survives, stated as the condition it actually was.
//
// The database holds the same rule in `account_handle_required_when_plan_charges`. Both, deliberately:
// the database is the last line and cannot be bypassed, and this one gives the caller a named error
// instead of a constraint violation.
func ValidateHandle(s string, planCharges bool) (string, error) {
	if strings.TrimSpace(s) == "" {
		if planCharges {
			return "", ErrHandleRequired
		}
		return "", nil
	}
	return NewHandle(s)
}

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
	// SetPlan repoints the account at a plan, the config version that plan was resolved under, AND
	// whether that plan charges. All three move together on purpose: a plan id without its version
	// cannot explain a closed period, and a plan without its charging flag lets an account sit on a paid
	// plan with no billing customer — which P27's database CHECK refuses, so a caller that forgot would
	// discover it as a write failure rather than as a wrong bill.
	SetPlan(customerID, planID, planConfigVersion string, charges bool) (Account, error)
	// SetProviderHandle records the billing-provider customer minted at first checkout. It is a separate
	// method from SetPlan because the handle is created BEFORE the plan moves, so a failed provider call
	// never leaves a plan change to undo.
	SetProviderHandle(customerID, handle string) (Account, error)
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
	// List returns every account.
	//
	// 🔴 It returns an error for the reason billing.Ledger.Events does: written against a map, which
	// cannot fail, so a durable store had nowhere to report a failed read. Returning nil is what one
	// would naturally do — and billing/webhook.go scans this list to match a provider event to a
	// customer, so an empty read means the webhook silently matches nothing and the charge is never
	// attributed. An outage must not be spellable as "this platform has no customers".
	List() ([]Account, error)
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
	h, err := ValidateHandle(a.ProviderCustomerHandle, a.PlanCharges)
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

// SetPlan repoints the account's plan, pins the config version it resolved under, and records whether
// the plan charges.
//
// 🔴 It refuses to move an account onto a CHARGING plan while it holds no provider handle. The database
// refuses the same write; doing it here too means the caller gets a named error instead of a constraint
// violation, and the in-memory store does not quietly permit what the durable one forbids.
func (s *MemStore) SetPlan(customerID, planID, planConfigVersion string, charges bool) (Account, error) {
	if planConfigVersion == "" {
		return Account{}, errors.New("account: a plan change must pin the plan_config_version it resolved under")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.by[customerID]
	if !ok {
		return Account{}, fmt.Errorf("%w: %s", ErrNotFound, customerID)
	}
	if charges && strings.TrimSpace(a.ProviderCustomerHandle) == "" {
		return Account{}, ErrHandleRequired
	}
	a.ActivePlanID, a.PlanConfigVersion, a.PlanCharges = planID, planConfigVersion, charges
	s.by[customerID] = a
	return a, nil
}

// SetProviderHandle records the billing-provider customer minted at first checkout.
//
// Separate from SetPlan because the two happen at different moments and in that order: the handle is
// created BEFORE the plan moves, so the plan change never has to be undone when the provider call fails.
func (s *MemStore) SetProviderHandle(customerID, handle string) (Account, error) {
	h, err := NewHandle(handle)
	if err != nil {
		return Account{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.by[customerID]
	if !ok {
		return Account{}, fmt.Errorf("%w: %s", ErrNotFound, customerID)
	}
	a.ProviderCustomerHandle = h
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
func (s *MemStore) List() ([]Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Account, 0, len(s.by))
	for _, a := range s.by {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CustomerID < out[j].CustomerID })
	// nil error always: a map cannot fail. The signature exists for the durable store, and MemStore
	// satisfying it is what keeps the two interchangeable.
	return out, nil
}
