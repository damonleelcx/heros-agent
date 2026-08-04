package billing

import (
	"errors"
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/account"
)

// entitlementsync.go is P21 section 5: what a customer can do follows what they pay for — in BOTH
// directions, by an audited plan change, and never by deleting anything.
//
// ## The three properties, and why each is a property rather than a preference
//
//   - **AUDITED.** Every move is `account.SetPlan` (pinning the plan config version) PLUS a
//     `TypePlanChange` ledger row. "Why did this tenant lose auto-merge" is the first question support
//     is asked, and it has to be answerable from the ledger rather than from someone's memory of a
//     deploy.
//   - **REVERSIBLE.** Paying again is another plan change that restores the plan. Degradation is
//     therefore a forward operation, exactly as a correction is, and every prior row stays intact.
//   - **NEVER A DELETE.** Nothing here removes an account, a usage record, or a billing row. A
//     degrade-by-delete is irreversible and unauditable, which are the two things money state may never
//     be.
//
// ## Why the audit row is written BEFORE the plan moves
//
// The two writes can fail independently, so the order decides which inconsistency is possible. Row
// first means the failure mode is *an audit row whose plan change did not land* — visible, reconcilable,
// and re-driven by the provider's next retry. Plan first means the failure mode is *a plan change with
// no audit row*, which is invisible: nothing in the system knows to look for it, and the question
// "when did this tenant move to Free" has no answer at all.
//
// ## What the grace window is, and why nothing happens during it
//
// `invoice.payment_failed` and `past_due` mirror state and change NO plan. That is not leniency — it is
// refusing to fight Stripe's dunning schedule. Stripe is still retrying the card during that window; a
// platform that degraded on the first failure would yank a paying customer's access over a transient
// decline and then have to put it back. The degrade happens at the boundary Stripe's schedule defines,
// which arrives as `customer.subscription.deleted` (or an `updated` carrying a terminal status).

// FreePlanID is the plan a lapsed subscription degrades to. It is a constant rather than a literal at
// the call site for the same reason every other name in this package is: a plan id spelled two ways is
// a degradation that silently lands nowhere.
const FreePlanID = "free"

// ErrNoDegradeTarget is returned when the free plan is not in the published configuration.
//
// It FAILS CLOSED — the account keeps its current plan — because the alternative is moving a customer
// to a plan that does not exist, which resolves to no entitlements at all. Being over-entitled for an
// hour while somebody publishes the plan config is a smaller wrong than being locked out of a product
// that was paid for.
var ErrNoDegradeTarget = errors.New("billing: the free plan is not in the published plan configuration, so a degradation has nowhere to land")

// WithFreePlan overrides the degradation target. A deployment whose free tier is named something else
// sets it once, here, rather than at each call site.
func (s *Service) WithFreePlan(planID string) *Service {
	if strings.TrimSpace(planID) != "" {
		s.freePlan = planID
	}
	return s
}

// RecordSubscriptionPlan remembers which plan a customer's subscription grants.
//
// It exists because a Stripe event carries the plan only where the platform stamped it on the
// subscription's metadata, and an invoice event carries no plan at all. This is the platform's own
// record of "what did this customer subscribe to", consulted when the event does not say.
func (s *Service) RecordSubscriptionPlan(customerID, planID string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.subPlans == nil {
		s.subPlans = map[string]string{}
	}
	s.subPlans[customerID] = planID
}

// SubscriptionPlan is the plan the platform recorded for a customer's subscription, if any.
func (s *Service) SubscriptionPlan(customerID string) string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.subPlans[customerID]
}

// PlanChangeIdempotencyKey identifies one plan change. Derived, never typed at a call site — the same
// discipline ledger.go applies to every other key, and for the same reason: the key IS the identity of
// the change, and two spellings of it are two rows for one event.
//
// The CAUSE is part of the key so that two different events moving a customer to the same plan (a
// cancel, then a re-subscribe, then another cancel) stay distinct rows, while a redelivery of one event
// is recognized as the same change.
func PlanChangeIdempotencyKey(customerID, planID, cause string) string {
	return fmt.Sprintf("plan_change:%s:%s:%s", customerID, planID, cause)
}

// planSyncOutcome describes what a lifecycle event did to the entitlement. It is returned rather than
// logged so the endpoint can report it and a test can assert on it.
type planSyncOutcome struct {
	// Changed is true when the account's plan actually moved.
	Changed bool
	// PlanID is the plan in force after the event.
	PlanID string
	// Reason is why — the same sentence that goes on the ledger row.
	Reason string
}

// syncEntitlement moves the account's plan in response to one applied lifecycle event.
//
// It is called from inside the webhook's PERSIST step, before the ack, and not from a caller afterwards.
// That placement is load-bearing: an entitlement change applied after the 2xx would be an effect the
// platform promised to have recorded and had not, which is the same acked-but-unrecorded failure
// persist-then-ack exists to prevent — just one layer up.
func (s *Service) syncEntitlement(p WebhookPayload) (planSyncOutcome, error) {
	if s.accounts == nil || s.plans == nil {
		return planSyncOutcome{}, nil
	}
	if strings.TrimSpace(p.CustomerID) == "" {
		// An event the platform could not attribute to one of its accounts changes no entitlement. It is
		// not an error: Stripe sends events for objects the platform did not create, and guessing which
		// account they belong to would be worse than doing nothing.
		return planSyncOutcome{}, nil
	}
	// 🔴 …and NEITHER IS A CUSTOMER ID THAT NAMES NO ACCOUNT HERE. The check above only caught the case
	// where nothing named a customer at all; an id that IS present and resolves to no account is the
	// same situation — "the platform could not attribute this event to one of its accounts" — and it
	// arrived by a different route: `platform_customer_id` in the provider's metadata, which
	// decodeWebhook trusts ahead of the handle lookup precisely because the platform stamped it.
	//
	// It happens for real: an account provisioned on a DIFFERENT deployment sharing one Stripe account,
	// an object created by hand in the dashboard, a tenant deleted here but not there. Every one of them
	// used to end the same way — `applyPlanChange` calls `accounts.Get`, gets ErrNotFound, the whole
	// delivery fails, the claim is released, and Stripe retries an event that can NEVER succeed. For
	// days, on an endpoint an operator reads as an outage.
	//
	// ⚠️ ONLY ErrNotFound. A store that is DOWN must still fail the delivery so the provider retries —
	// that retry is the recovery. Collapsing the two would silently drop entitlement changes for real
	// paying customers during a database blip, which is the opposite failure and a far worse one.
	if _, err := s.accounts.Get(p.CustomerID); err != nil {
		if errors.Is(err, account.ErrNotFound) {
			return planSyncOutcome{}, nil
		}
		return planSyncOutcome{}, fmt.Errorf("billing: entitlement sync cannot read account %q: %w", p.CustomerID, err)
	}

	switch p.Type {
	case WebhookInvoicePaid:
		return s.grantPlan(p, "invoice paid")

	case WebhookSubscriptionCreated, WebhookSubscriptionUpdated:
		switch p.Status {
		case "active", "trialing":
			return s.grantPlan(p, "subscription active")
		case "canceled", "unpaid", "incomplete_expired":
			// A terminal status IS the grace-end: Stripe has finished retrying.
			return s.degradeToFree(p, "subscription "+p.Status)
		}
		// past_due and every other non-terminal status: mirror only. The grace window is Stripe's.
		return planSyncOutcome{}, nil

	case WebhookSubscriptionCanceled:
		return s.degradeToFree(p, "subscription canceled")

	case WebhookInvoicePaymentFailed, WebhookSubscriptionPastDue:
		// 🔴 Deliberately nothing. See the file comment: this is the grace window Stripe is retrying in,
		// and degrading here would fight the dunning schedule the platform does not own.
		return planSyncOutcome{}, nil
	}
	return planSyncOutcome{}, nil
}

// grantPlan sets the account to the plan its subscription pays for.
func (s *Service) grantPlan(p WebhookPayload, why string) (planSyncOutcome, error) {
	planID := firstNonEmpty(p.PlanID, s.SubscriptionPlan(p.CustomerID))
	if planID == "" {
		// The event names no plan and the platform recorded none. Doing nothing is correct: granting a
		// guessed plan is granting entitlements nobody sold.
		return planSyncOutcome{}, nil
	}
	return s.changePlan(p, planID, why)
}

// degradeToFree moves the account to the free tier at the boundary. It DELETES NOTHING.
func (s *Service) degradeToFree(p WebhookPayload, why string) (planSyncOutcome, error) {
	free := s.freePlanID()
	if _, err := s.plans.ResolvePlan(free); err != nil {
		return planSyncOutcome{}, fmt.Errorf("%w (%s): %v", ErrNoDegradeTarget, free, err)
	}
	return s.changePlan(p, free, why)
}

func (s *Service) freePlanID() string {
	if s.freePlan != "" {
		return s.freePlan
	}
	return FreePlanID
}

// changePlan performs the audited plan change for one lifecycle event.
func (s *Service) changePlan(p WebhookPayload, planID, why string) (planSyncOutcome, error) {
	plan, err := s.plans.ResolvePlan(planID)
	if err != nil {
		// A plan the configuration does not know is refused rather than set. Setting it would leave the
		// account pointing at a definition nothing can resolve, and every entitlement check would then
		// fail closed for a customer who paid.
		return planSyncOutcome{}, fmt.Errorf("billing: entitlement sync cannot resolve plan %q: %w", planID, err)
	}
	changed, err := s.applyPlanChange(p.CustomerID, planID, "webhook:"+p.ProviderEventID, why)
	if err != nil {
		return planSyncOutcome{}, err
	}
	return planSyncOutcome{Changed: changed, PlanID: plan.PlanID, Reason: why}, nil
}

// applyPlanChange is THE audited plan change — the one path both the webhook sync and the console's
// own subscribe/upgrade/downgrade go through.
//
// One path rather than two, because the audit is the point: two implementations of "move the plan and
// record why" is one implementation that eventually forgets the second half, and the row that goes
// missing is the one nobody notices until they need it.
//
// It reports whether the plan actually moved. A no-op change authors NO row: an audit entry claiming a
// change that did not happen is worse than no entry, because it is read as evidence.
func (s *Service) applyPlanChange(customerID, planID, cause, why string) (bool, error) {
	acct, err := s.accounts.Get(customerID)
	if err != nil {
		return false, fmt.Errorf("billing: plan change: %w", err)
	}
	plan, err := s.plans.ResolvePlan(planID)
	if err != nil {
		return false, fmt.Errorf("billing: plan change cannot resolve plan %q: %w", planID, err)
	}
	if acct.ActivePlanID == plan.PlanID && acct.PlanConfigVersion == plan.Version {
		return false, nil
	}

	reason := fmt.Sprintf("%s: plan %s -> %s", why, displayOrID(acct.ActivePlanID), plan.DisplayName)
	key := PlanChangeIdempotencyKey(customerID, plan.PlanID, cause)

	// ── 1. AUDIT FIRST — see the file comment on ordering ─────────────────────
	if _, err := s.ledger.Append(BillingEvent{
		CustomerID:     customerID,
		Type:           TypePlanChange,
		IdempotencyKey: key,
		CausedBy:       cause,
		Reason:         reason,
		Status:         StatusRecorded,
		CreatedAt:      s.now().UTC(),
	}); err != nil && !errors.Is(err, ErrDuplicateKey) {
		return false, fmt.Errorf("billing: audit plan change: %w", err)
	}

	// ── 2. MOVE THE PLAN, pinning the config version it resolved under ────────
	if _, err := s.accounts.SetPlan(customerID, plan.PlanID, plan.Version); err != nil {
		return false, fmt.Errorf("billing: set plan: %w", err)
	}
	return true, nil
}

func displayOrID(planID string) string {
	if planID == "" {
		return "(none)"
	}
	return planID
}
