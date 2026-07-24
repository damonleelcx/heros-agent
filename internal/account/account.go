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
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

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
