// Package paymentsview is the P21 collection read model: the payment surface a customer sees, built
// from the P7 billing view and the billing Service's own view of what can be collected.
//
// # Why this package exists at all
//
// It did not, and that is the whole reason P21 could not be mounted on a real deployment. The read
// model was written once, inside `cmd/proof/payments`, over that binary's in-memory `state` — so the
// only thing in this repository that could satisfy `api.PaymentsSource` was a demo. `internal/launch`
// passed `MountPayments(nil)`, the route answered 503, and the reason recorded against it ("no payment
// provider is configured") was true but incomplete: even with a provider there was nothing to mount.
//
// This is the same shape as every "no persistent adapter exists outside a demo binary" capability, and
// it is fixed the same way: extract the projection, keep it honest, and let the deployment wire it.
//
// # What it does NOT do
//
// It performs no arithmetic on money and holds no amount. Every figure it renders is one the billing
// Service or the P7 view already computed, and the amounts themselves live with the provider — this
// package moves opaque references. It also makes no policy decision: whether a plan is subscribable,
// whether collection is available at all, and whether a price reference resolves are all answers it
// asks for, never answers it derives.
package paymentsview

import (
	"context"
	"errors"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/billingview"
)

// rfc3339 is the one timestamp format this package emits.
const rfc3339 = "2006-01-02T15:04:05Z07:00"

// Source implements api.PaymentsSource over the durable P7 view and the billing Service.
type Source struct {
	billing *billingview.Source
	svc     *billing.Service
}

// New wires the read model.
//
// Both collaborators are required, and neither is substitutable by a stub: without the P7 view there
// is no invoice to attach a payment method to, and without the Service there is no provider to ask
// whether collection is possible. A Source missing either would answer with a shape that looks
// complete and is not, which is the failure this package was extracted to stop repeating.
func New(bv *billingview.Source, svc *billing.Service) (*Source, error) {
	if bv == nil || svc == nil {
		return nil, errors.New("paymentsview: the collection read model needs the P7 billing view and the billing service")
	}
	return &Source{billing: bv, svc: svc}, nil
}

// Payment returns the whole collection read model for one customer-period.
//
// It returns false when the customer has no billing account. That is deliberately the SAME answer the
// P7 view gives, rather than an empty payment surface: a customer with no account has no invoice, no
// plan and no method, and rendering the collection half over nothing would show a Subscribe button
// belonging to no account.
func (s *Source) Payment(customerID, period string) (api.PaymentView, bool) {
	b, ok := s.billing.Billing(customerID, period)
	if !ok {
		return api.PaymentView{}, false
	}
	v := api.PaymentView{Billing: b, CollectionAvailable: s.svc.CollectionAvailable()}

	// The price-reference preflight, seen from the CUSTOMER's side: which plans cannot be bought right
	// now. The reference that failed and the provider's own words stay on /readyz — two audiences, two
	// surfaces, and a customer has no use for a price id.
	unpurchasable := s.svc.UnpurchasablePlans()
	for _, opt := range s.svc.PlanOptions(customerID) {
		v.Plans = append(v.Plans, api.PlanOptionView{
			PlanID: opt.PlanID, Name: opt.Name, Rank: opt.Rank, Current: opt.Current,
			Direction: opt.Direction, Subscribable: opt.Subscribable,
			Unavailable: unpurchasable[opt.PlanID],
		})
	}
	if len(unpurchasable) > 0 {
		issue := &api.PricingIssueView{}
		for _, opt := range v.Plans {
			if opt.Unavailable {
				issue.Plans = append(issue.Plans, opt.Name)
			}
		}
		if rep, ran := s.svc.PricingStatus(); ran {
			issue.CheckedAt = rep.RanAt.Format(rfc3339)
		}
		v.PricingIssue = issue
	}

	st := s.svc.BillingState(customerID)
	v.PaymentMethod = api.PaymentMethodView{
		Present: st.PaymentMethodPresent, Brand: st.PaymentMethodBrand, Last4: st.PaymentMethodLast4,
	}
	// 🔴 A dunning state with no next step is an alarm, not information. Each unhappy state carries the
	// provider's word for what happened AND the path back, because the customer reading this page is
	// the only person who can clear it.
	switch {
	case st.PaymentFailed:
		v.PaymentMethod.Status = "payment_failed"
		v.PaymentMethod.Reason = "The payment provider could not take payment on the card on file."
		v.PaymentMethod.RestorePath = "Add or update the payment method below. The provider retries on its own schedule, and a working card ends the retries."
	case st.PastDue:
		v.PaymentMethod.Status = "past_due"
		v.PaymentMethod.Reason = "The payment provider has this subscription past due while it retries payment."
		v.PaymentMethod.RestorePath = "Update the payment method below to end the retries before the grace window closes."
	case st.PaymentMethodPresent:
		v.PaymentMethod.Status = "ok"
	}
	return v, true
}

// StartCheckout mints a provider-hosted collection session.
//
// The card is entered on the PROVIDER's page and never reaches this platform — which is why this
// returns a URL or a client secret and never a form. Nothing here validates a card, because nothing
// here ever sees one.
func (s *Source) StartCheckout(ctx context.Context, customerID, planName, successURL, cancelURL string) (api.CheckoutView, error) {
	session, err := s.svc.StartCheckout(ctx, customerID, planName, successURL, cancelURL)
	if err != nil {
		return api.CheckoutView{}, err
	}
	out := api.CheckoutView{URL: session.URL, ClientSecret: session.ClientSecret, SessionRef: session.SessionRef}
	if !session.ExpiresAt.IsZero() {
		out.ExpiresAt = session.ExpiresAt.Format(rfc3339)
	}
	return out, nil
}

// ChangePlan subscribes, upgrades or downgrades by plan NAME.
//
// `CheckoutRequired` is passed through rather than resolved here: a change that needs a payment method
// is not a failure, and the console renders it as the next step. Deciding that here would put a second
// copy of the provider's rule in the projection layer.
func (s *Source) ChangePlan(ctx context.Context, customerID, planName string) (api.PlanChangeView, error) {
	res, err := s.svc.ChangePlan(ctx, customerID, planName)
	if err != nil {
		return api.PlanChangeView{}, err
	}
	return api.PlanChangeView{
		PlanID: res.PlanID, PlanName: res.PlanName, Status: res.Status,
		Changed: res.Changed, CheckoutRequired: res.CheckoutRequired,
	}, nil
}

// compile-time proof that this satisfies the mount contract, so a change to either side fails here
// rather than at the call in internal/launch.
var _ api.PaymentsSource = (*Source)(nil)
