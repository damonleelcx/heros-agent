package billing

import (
	"context"
	"errors"
	"fmt"
	"github.com/heros-foreal/agentd/internal/account"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/plancfg"
)

// collection.go is P21 section 6's server half: capturing a payment method, and changing plan by NAME.
//
// ## Why this is a SEPARATE interface rather than four more methods on Provider
//
// Design Decision 1 is ratified: `billing.Provider` does not widen. That is not a rule being obeyed
// reluctantly here — it is the right shape for this particular capability, because collection is
// genuinely optional. A processor integration that bills correctly but has no hosted checkout is still
// a valid `Provider`; what it cannot do is mint a session, and the honest way to express "cannot" is a
// second interface it does not implement rather than a method that returns an error nobody expects.
//
// The Service type-asserts. A deployment whose provider lacks collection gets a NAMED refusal, not a
// nil dereference and not a silently missing button.
//
// ## What "by plan NAME" means and why it is load-bearing
//
// The console asks for "Team". It never sends a price, a price reference, or an amount — it does not
// have one, and after the payment-UI fence it cannot acquire one. The name is resolved against the
// published plan configuration here, server-side, which is the only place that knows which `price_ref`
// is current. That is what keeps a price change a configuration change rather than a deploy.

// CheckoutRequest asks the provider for a hosted collection surface.
type CheckoutRequest struct {
	ProviderCustomerHandle string
	// CustomerID is the PLATFORM's customer id, stamped on the objects the session creates so the
	// lifecycle events that follow can be attributed back without a lookup.
	CustomerID string
	// PriceRef is the OPAQUE provider price handle from the plan config. Never an amount.
	PriceRef string
	PlanID   string
	PlanName string
	// SuccessURL / CancelURL are where the browser lands. Both are required: a customer who abandons
	// checkout must land somewhere the product chose rather than on the provider's default.
	SuccessURL     string
	CancelURL      string
	IdempotencyKey string
}

// CheckoutSession is where the browser goes to hand its card to the provider.
//
// Note what is NOT here: no card field, no form, no token the platform could store. This struct is the
// whole of the platform's involvement in collection, and it is a pointer at somebody else's page.
type CheckoutSession struct {
	SessionRef string `json:"session_ref"`
	// URL is the hosted Checkout page. ClientSecret is the embedded Payment Element's short-lived
	// credential. Exactly one of them is used; both are short-lived and single-purpose.
	URL          string    `json:"url,omitempty"`
	ClientSecret string    `json:"client_secret,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

// UpdateSubscriptionRequest repoints an existing subscription at a different plan's price.
type UpdateSubscriptionRequest struct {
	SubscriptionRef string
	PriceRef        string
	PlanID          string
	PlanName        string
	IdempotencyKey  string
}

// CollectionProvider is the OPTIONAL payment-collection capability.
type CollectionProvider interface {
	// CreateCheckoutSession mints a session whose card data goes browser→provider directly.
	CreateCheckoutSession(ctx context.Context, req CheckoutRequest) (CheckoutSession, error)
	// UpdateSubscriptionPrice moves a subscription onto a different plan's price. Proration is the
	// provider's.
	UpdateSubscriptionPrice(ctx context.Context, req UpdateSubscriptionRequest) (SubscriptionResult, error)
}

// ErrNoCollection is returned when the wired provider cannot collect a payment method.
//
// It is a NAMED refusal rather than a nil dereference or a missing button, because the operational
// answer differs from every other billing failure: nothing is wrong with the deployment, it simply has
// a provider that does not do this, and the console must say so instead of rendering a control that
// cannot work.
var ErrNoCollection = errors.New("billing: the configured billing provider does not support payment collection")

// ErrUnknownPlanName is returned for a plan name absent from the published configuration.
var ErrUnknownPlanName = errors.New("billing: no plan with that name in the published configuration")

// collection returns the provider's collection capability, or a named refusal.
func (s *Service) collection() (CollectionProvider, error) {
	c, ok := s.provider.(CollectionProvider)
	if !ok {
		return nil, fmt.Errorf("%w (%s)", ErrNoCollection, s.provider.Describe())
	}
	return c, nil
}

// CollectionAvailable reports whether this deployment can collect a payment method at all. The console
// asks BEFORE rendering the control, so a customer is never offered a button that returns an error.
func (s *Service) CollectionAvailable() bool {
	_, ok := s.provider.(CollectionProvider)
	return ok
}

// PlanByName resolves a customer-facing plan NAME to its published configuration.
//
// Case-insensitive, because "team" typed by a person and "Team" rendered by the console are the same
// plan and a case mismatch is not a product decision.
func (s *Service) PlanByName(name string) (plancfg.PlanConfig, error) {
	want := strings.TrimSpace(strings.ToLower(name))
	if want == "" {
		return plancfg.PlanConfig{}, fmt.Errorf("%w: (empty)", ErrUnknownPlanName)
	}
	for _, p := range s.plans.Plans() {
		if strings.ToLower(p.DisplayName) == want || strings.ToLower(p.PlanID) == want {
			return p, nil
		}
	}
	return plancfg.PlanConfig{}, fmt.Errorf("%w: %q", ErrUnknownPlanName, name)
}

// PlanOption is one plan a customer may move to, by name.
type PlanOption struct {
	PlanID  string `json:"plan_id"`
	Name    string `json:"name"`
	Rank    int    `json:"rank"`
	Current bool   `json:"current"`
	// Direction is what moving there would be from where the customer is now: upgrade / downgrade /
	// current. Computed from RANK, never from a price — the console has no price and must not infer one.
	Direction string `json:"direction"`
	// Subscribable is false for a plan with no subscription price reference (the free tier). The control
	// is then absent rather than present-and-broken.
	Subscribable bool `json:"subscribable"`
}

// PlanOptions lists every plan the customer can move to, by name, ordered cheapest-first by RANK.
//
// Rank rather than price: the console must be able to say "upgrade" or "downgrade" without holding a
// number, and rank is configuration exactly as the price reference is.
func (s *Service) PlanOptions(customerID string) []PlanOption {
	acct, err := s.accounts.Get(customerID)
	current := ""
	if err == nil {
		current = acct.ActivePlanID
	}
	currentRank := -1
	plans := s.plans.Plans()
	for _, p := range plans {
		if p.PlanID == current {
			currentRank = p.Rank
		}
	}

	out := make([]PlanOption, 0, len(plans))
	for _, p := range plans {
		opt := PlanOption{
			PlanID: p.PlanID, Name: p.DisplayName, Rank: p.Rank,
			Current:      p.PlanID == current,
			Subscribable: p.PriceRefs["subscription"] != "",
		}
		switch {
		case opt.Current:
			opt.Direction = "current"
		case currentRank < 0:
			opt.Direction = "subscribe"
		case p.Rank > currentRank:
			opt.Direction = "upgrade"
		default:
			opt.Direction = "downgrade"
		}
		out = append(out, opt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rank < out[j].Rank })
	return out
}

// StartCheckout mints a collection session for a plan named by the customer (task 6.1).
//
// The BFF calls this; the browser is then redirected to what comes back. The Stripe key never leaves
// this process, and the value the console receives is short-lived and points at the provider's own page.
func (s *Service) StartCheckout(ctx context.Context, customerID, planName, successURL, cancelURL string) (CheckoutSession, error) {
	coll, err := s.collection()
	if err != nil {
		return CheckoutSession{}, err
	}
	plan, err := s.PlanByName(planName)
	if err != nil {
		return CheckoutSession{}, err
	}
	priceRef := plan.PriceRefs["subscription"]
	if priceRef == "" {
		return CheckoutSession{}, fmt.Errorf("billing: plan %s has no subscription price reference in the config store, so there is nothing to check out", plan.DisplayName)
	}
	acct, err := s.accounts.Get(customerID)
	if err != nil {
		return CheckoutSession{}, err
	}
	handle := acct.ProviderCustomerHandle
	if handle == "" {
		// 🔴 P27: mint the provider customer AND PERSIST IT.
		//
		// `EnsureCustomer` was already called here and its answer was thrown away. That was invisible
		// before P27 because every account was hand-created with a handle already in it; now a Free
		// account starts with none, so this is the FIRST place one exists. Not persisting it means the
		// next checkout mints again, the platform never learns which provider customer is theirs, and
		// `SetPlan(…, charges: true)` fails the invariant the database now holds — a paid plan with no
		// billing customer.
		//
		// The handle is stored BEFORE the session is created, so a failure between the two leaves an
		// account that knows its provider customer rather than one that has an orphan at the provider.
		if handle, err = s.provider.EnsureCustomer(ctx, customerID); err != nil {
			return CheckoutSession{}, err
		}
		if _, err := s.accounts.SetProviderHandle(customerID, handle); err != nil {
			return CheckoutSession{}, fmt.Errorf("billing: the provider customer was created but could "+
				"not be recorded, so a retry would create a second one: %w", err)
		}
	}

	// Derived, not typed: a second click under the same intent must not mint a second subscription.
	key := fmt.Sprintf("checkout:%s:%s:%s", customerID, plan.PlanID, plan.Version)
	return coll.CreateCheckoutSession(ctx, CheckoutRequest{
		ProviderCustomerHandle: handle,
		CustomerID:             customerID,
		PriceRef:               priceRef,
		PlanID:                 plan.PlanID,
		PlanName:               plan.DisplayName,
		SuccessURL:             successURL,
		CancelURL:              cancelURL,
		IdempotencyKey:         key,
	})
}

// PlanChangeResult is what a subscribe / upgrade / downgrade did.
type PlanChangeResult struct {
	PlanID   string `json:"plan_id"`
	PlanName string `json:"plan_name"`
	// Status is the provider's subscription status, verbatim.
	Status string `json:"status,omitempty"`
	// Changed is false when the customer was already on that plan.
	Changed bool `json:"changed"`
	// CheckoutRequired is true when there is no subscription to move yet: the customer must attach a
	// payment method first. The console then sends them to checkout rather than reporting a failure,
	// because "you have not paid yet" is a step, not an error.
	CheckoutRequired bool `json:"checkout_required"`
}

// ChangePlan subscribes, upgrades or downgrades by plan NAME (task 6.2).
//
// ## The order, and what each step owns
//
//  1. The provider's subscription is repointed at the new plan's price reference. PRORATION IS
//     STRIPE'S — the platform neither computes nor stores it.
//  2. The entitlement flips at the PLAN-CHANGE EVENT, by the same audited path the webhook uses
//     (P7 Q4). It does not wait for an invoice: what a customer can do follows the change they just
//     made, and the money follows Stripe's own schedule.
//
// Each system owns its fact and neither guesses the other's. Doing it the other way — waiting for
// Stripe to confirm before flipping the entitlement — would leave a customer who just upgraded staring
// at a paywall for a webhook round trip.
func (s *Service) ChangePlan(ctx context.Context, customerID, planName string) (PlanChangeResult, error) {
	plan, err := s.PlanByName(planName)
	if err != nil {
		return PlanChangeResult{}, err
	}
	acct, err := s.accounts.Get(customerID)
	if err != nil {
		return PlanChangeResult{}, err
	}
	res := PlanChangeResult{PlanID: plan.PlanID, PlanName: plan.DisplayName}
	if acct.ActivePlanID == plan.PlanID {
		return res, nil
	}

	// 🔴 A downgrade below the seats currently held is refused, and the refusal names BOTH numbers.
	//
	// The alternative — accept it and let the seat gate start denying — puts the organization in a state
	// it did not choose and cannot see: members it already has, over an allowance it just bought, with
	// the first symptom being an unrelated action failing. Refusing here, with removal named as the
	// remedy, is the same shape the invitation limit uses, deliberately: two ways to hit the same wall
	// should read the same way.
	if err := s.refuseSeatDowngrade(customerID, acct, plan); err != nil {
		return PlanChangeResult{}, err
	}

	subRef := s.SubscriptionRef(customerID)
	priceRef := plan.PriceRefs["subscription"]

	switch {
	case priceRef == "":
		// The free tier: there is no price to move to. The subscription is left alone — cancelling it is
		// Stripe's job through the customer's own cancel flow, and doing it here would be the platform
		// making a money decision on a customer's behalf.
	case subRef == "":
		// Nothing to repoint. The customer has no payment method yet, so this is a checkout, not a
		// change — and saying so is more useful than failing.
		res.CheckoutRequired = true
		return res, nil
	default:
		coll, cerr := s.collection()
		if cerr != nil {
			return PlanChangeResult{}, cerr
		}
		updated, uerr := coll.UpdateSubscriptionPrice(ctx, UpdateSubscriptionRequest{
			SubscriptionRef: subRef,
			PriceRef:        priceRef,
			PlanID:          plan.PlanID,
			PlanName:        plan.DisplayName,
			IdempotencyKey:  fmt.Sprintf("plan_price:%s:%s:%s", customerID, plan.PlanID, plan.Version),
		})
		if uerr != nil {
			// 🔴 The entitlement is NOT flipped. A plan change the provider refused is not a plan change,
			// and granting it anyway would hand out entitlements nobody is being billed for.
			return PlanChangeResult{}, fmt.Errorf("billing: change plan at the provider: %w", uerr)
		}
		res.Status = updated.Status
	}

	changed, err := s.applyPlanChange(customerID, plan.PlanID, "plan_change:"+customerID+":"+plan.PlanID+":"+plan.Version,
		"customer changed plan")
	if err != nil {
		return PlanChangeResult{}, err
	}
	res.Changed = changed
	s.RecordSubscriptionPlan(customerID, plan.PlanID)
	return res, nil
}

// SeatCounter reports the seats an organization holds NOW. Optional: a deployment with no identity store
// wired cannot count seats, and the downgrade check is then skipped rather than evaluated against a zero
// that would let every downgrade through while looking checked.
type SeatCounter interface {
	SeatsHeld(tenantID string) (int, error)
}

// WithSeatCounter points the service at the live seat count, so a downgrade can be refused before it
// leaves the organization over its new allowance.
func (s *Service) WithSeatCounter(c SeatCounter) *Service { s.seats = c; return s }

// ErrSeatsExceedPlan is the named refusal a downgrade below the held seat count produces. Named, because
// the console shows a different screen for it than for any other plan-change failure: this one has a
// remedy the customer can act on.
var ErrSeatsExceedPlan = errors.New("billing: that plan includes fewer seats than this organization holds")

// refuseSeatDowngrade returns ErrSeatsExceedPlan, with both numbers in the message, when moving to
// `plan` would leave the organization over its allowance.
func (s *Service) refuseSeatDowngrade(customerID string, acct account.Account, plan plancfg.PlanConfig) error {
	if s.seats == nil {
		return nil
	}
	allowed, set := plan.Limit(plancfg.LimitSeats)
	// An operator's per-tenant override REPLACES the plan's allowance for this one limit, unchanged from
	// P7 — so a downgrade an operator has already made room for is not refused.
	if v, ok := acct.QuotaOverride(string(plancfg.LimitSeats)); ok {
		allowed, set = v, true
	}
	if !set {
		return nil // unset == unlimited
	}
	held, err := s.seats.SeatsHeld(customerID)
	if err != nil {
		// Unmeasurable is not zero, and it is not a refusal either: a membership-store outage must not
		// block a plan change the customer is entitled to make.
		return nil
	}
	if float64(held) <= allowed {
		return nil
	}
	return fmt.Errorf("%w: %s includes %s seats and this organization holds %d — remove a member first",
		ErrSeatsExceedPlan, plan.DisplayName, strconv.FormatFloat(allowed, 'f', -1, 64), held)
}
