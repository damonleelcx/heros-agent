// Package signup is the one place the identity domain and the billing domain are composed.
//
// # Why it is its own package
//
// `tenancy` owns organizations, people and memberships. `account` owns who is billed. Neither imports
// the other, deliberately: a compile-time dependency between them means an identity migration cannot
// run without a billing outage, and it is the kind of edge that is easy to add and very hard to remove.
//
// But sign-up has to write across both, atomically — every partial state is unrecoverable through the
// product (see `tenancy/orgcreate.go`'s header). So the composition lives here, where knowing about both
// is the job rather than a leak, and the transaction is lent from the identity side through a closure.
//
// # What sign-up is, in one line
//
// A verified identity that maps to no organization creates one, becomes its owner, and gets a Free
// account with no payment method — so the first *Upgrade* click finds an account instead of the
// `ErrNotFound` that `StartCheckout` returns today for every real customer.
//
// # 🔴 What this package refuses to do
//
// It does not decide whether sign-up is ALLOWED. That is a declared deployment posture (`HEROS_SELF_SERVE_SIGNUP`,
// off by default) read by the caller, because the three deployment shapes this product ships in want
// three different answers and an air-gapped install must not grow a registration form by upgrading.
// This package answers "how", never "whether".
package signup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

// Plans is the slice of plan configuration sign-up needs: which plan a new organization starts on.
type Plans interface {
	// Resolve returns a plan by id, and the config version it resolved under.
	Resolve(planID string) (plancfg.PlanConfig, error)
	// FreePlanID names the plan a new organization starts on.
	FreePlanID() string
}

// Errors callers branch on.
var (
	// ErrDisabled is what a deployment with self-serve off returns. It is distinct from a refusal to
	// authenticate: the identity was verified, and the platform is declining to create anything.
	ErrDisabled = errors.New("signup: self-serve organization creation is not enabled on this deployment")
	// ErrNameRequired is the one field the person types.
	ErrNameRequired = errors.New("signup: an organization needs a name")
	// ErrNoFreePlan means the deployment's plan configuration has no plan that charges nothing. Sign-up
	// cannot invent one: putting a new customer straight onto a charging plan would demand a payment
	// method before they have seen the product.
	ErrNoFreePlan = errors.New("signup: this deployment's plan configuration has no free plan to start on")
)

// Service creates organizations.
type Service struct {
	tenants  tenancy.OrganizationCreator
	accounts account.Store
	plans    Plans
	now      func() time.Time
	// seed runs inside the creation transaction, after the account, for deployments that decide a new
	// organization's analysis placement at creation rather than leaving it to an operator. Nil on every
	// deployment that does not — which is the default, and is why this is an option rather than a
	// parameter every caller would pass nil to.
	seed PlacementSeed
}

// PlacementSeed writes a newly created organization's analysis placement inside the SAME transaction
// that created it.
//
// 🔴 A function rather than a store, so this package stays ignorant of what a placement IS. Sign-up
// creates organizations; `platform` versus `customer` is a P30 policy, and a signup path that imported
// the agent's vocabulary would be a signup path that has to change when that vocabulary does.
//
// `ex` is nil on the in-memory path, where there is no transaction to join.
type PlacementSeed func(ctx context.Context, ex tenancy.Execer, tenantID string, at time.Time) error

// WithPlacementSeed returns the service with creation-time placement seeding wired.
//
// 🔴 It seeds an EXPLICIT row; it does not change what an absent row means. A deployment that wants
// every new organization analysed platform-side gets exactly that, while `disabled` stays the default
// for anything unseeded, every seeded org carries a reason, and any one of them can still be turned
// off individually. Flipping the default instead would enable every tenant at once with no record of
// the choice and no per-tenant off switch.
func (s *Service) WithPlacementSeed(seed PlacementSeed) *Service {
	s.seed = seed
	return s
}

// New builds the service. `accounts` is used only on the in-memory path; on the durable path the
// account is written inside the identity transaction (see `create`).
func New(tenants tenancy.OrganizationCreator, accounts account.Store, plans Plans, now func() time.Time) (*Service, error) {
	if tenants == nil {
		return nil, errors.New("signup: no organization creator")
	}
	if accounts == nil {
		return nil, errors.New("signup: no account store")
	}
	if plans == nil {
		return nil, errors.New("signup: no plan configuration")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{tenants: tenants, accounts: accounts, plans: plans, now: now}, nil
}

// Request is a sign-up. Everything except `Name` comes from a VERIFIED assertion — the caller has
// already proved the identity, and this package does not re-derive it from anything a client sent.
type Request struct {
	// Name is the ONLY client-supplied field, and it is a display string.
	//
	// It is asked for rather than derived. Deriving it from the email domain produces "Gmail" for every
	// independent developer and the wrong legal entity for half of everyone else — and an editable wrong
	// name is a name most people never edit.
	Name string
	// Issuer, Subject and Email come from the verified assertion, server-side.
	Issuer  string
	Subject string
	Email   string
}

// Result is what sign-up produced. The account is included because the caller's next screen shows the
// plan, and re-reading it would be a second query for something this call already knows.
type Result struct {
	Organization tenancy.Organization `json:"organization"`
	Account      account.Account      `json:"account"`
}

// Create writes the organization, its owner, their membership and a Free account — all of them or none.
func (s *Service) Create(ctx context.Context, req Request) (Result, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return Result{}, ErrNameRequired
	}
	if strings.TrimSpace(req.Issuer) == "" || strings.TrimSpace(req.Subject) == "" {
		return Result{}, errors.New("signup: sign-up needs a verified identity")
	}

	freeID := s.plans.FreePlanID()
	if strings.TrimSpace(freeID) == "" {
		return Result{}, ErrNoFreePlan
	}
	plan, err := s.plans.Resolve(freeID)
	if err != nil {
		return Result{}, fmt.Errorf("signup: resolve the free plan: %w", err)
	}
	if plan.Charges() {
		// A deployment whose "free" plan carries a price reference has a configuration mistake, and
		// starting somebody on it would demand a payment method before they have seen the product.
		// Refusing here is louder than the alternative, which is a sign-up that succeeds and a first
		// screen that asks for a card.
		return Result{}, fmt.Errorf("%w: %q carries price references", ErrNoFreePlan, freeID)
	}

	at := s.now().UTC()

	// 🔴 The tenant id is minted HERE, not inside CreateOrganization.
	//
	// The account row has to name it, and the account row is written by the hook — which runs inside the
	// identity transaction and therefore before CreateOrganization has returned anything. Letting the
	// store mint it would mean either passing the id back out of a running transaction or writing the
	// account afterwards, and "afterwards" is exactly the non-atomic shape this package exists to avoid.
	tenantID := tenancy.NewID("org")

	// The account's customer id IS the tenant id. One organization, one billing relationship — and it is
	// what `scope.ts` already assumes when it builds `/customers/{tenant}/billing`.
	acct := account.Account{
		CustomerID:        tenantID,
		ActivePlanID:      plan.PlanID,
		PlanConfigVersion: plan.Version,
		// 🔴 No provider handle, and that is legal precisely because the plan charges nothing. The
		// database holds the same rule; this is the value that satisfies it.
		ProviderCustomerHandle: "",
		PlanCharges:            false,
		CreatedAt:              at,
	}

	var stored account.Account
	org, err := s.tenants.CreateOrganization(tenancy.NewOrganization{
		TenantID: tenantID,
		Name:     name,
		Issuer:   req.Issuer,
		Subject:  req.Subject,
		Email:    req.Email,
		At:       at,
	}, func(ex tenancy.Execer) error {
		if ex == nil {
			// In-memory path: no transaction to join. The identity store undoes its own three writes if
			// this returns an error, which is as strong as a store that loses everything on restart can be.
			out, err := s.accounts.Create(acct)
			if err != nil {
				return err
			}
			stored = out
			return s.seedPlacement(ctx, nil, tenantID, at)
		}
		out, err := account.CreateWithin(ctx, ex, acct)
		if err != nil {
			return err
		}
		stored = out
		return s.seedPlacement(ctx, ex, tenantID, at)
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Organization: org, Account: stored}, nil
}

// seedPlacement runs the creation-time placement seed, if one is wired.
//
// 🔴 A failure here FAILS THE SIGN-UP, because it runs inside the creation transaction and returning
// nil would commit an organization the deployment's own policy says should have been decided. That is
// the opposite of the bypass rule elsewhere in this system, and deliberately: an analysis that does not
// run is a missing enrichment, while an organization created outside its deployment's policy is a
// record that is wrong. The transaction is the reason the choice is available at all — without it the
// only options would be "commit a wrong record" or "leave a half-created org".
func (s *Service) seedPlacement(ctx context.Context, ex tenancy.Execer, tenantID string, at time.Time) error {
	if s.seed == nil {
		return nil
	}
	return s.seed(ctx, ex, tenantID, at)
}
