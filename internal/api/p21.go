package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/heros-foreal/agentd/internal/billing"
)

// p21.go mounts the ONE inbound-from-internet path: Stripe's webhook endpoint.
//
// ## Why this file imports the billing package when p7.go deliberately does not
//
// p7.go is a READ MODEL surface: it owns its own view types so the console's contract is not the
// billing package's internal shape. This is not that. This is the billing capability's own inbound
// path, and the thing it returns — the HTTP status — is a MONEY DECISION, not a rendering choice:
// a non-2xx is a request for Stripe to retry, and a 2xx is a promise the event was recorded. That
// decision is made in `billing.HandleStripeWebhook`, where the code can actually see whether the
// effect persisted. Re-deriving it here, or in an adapter in each wiring, would put the decision
// somewhere that cannot see the fact it depends on — which is exactly how an acked-but-unrecorded
// event happens.
//
// ## Why the endpoint is unauthenticated in the platform's own scheme
//
// It is not: it is authenticated by Stripe's signature, verified against the secret from the Secrets
// seam, before a byte of the body is parsed into a decision. An API-key gate in front of it would be
// a second credential Stripe does not have and cannot present. The signature IS the authentication,
// and that is why verification is step one rather than a check somewhere in the middle.
//
// ## The posture of an internet-facing endpoint
//
//   - the body is BOUNDED before it is read, so an unbounded POST cannot exhaust memory;
//   - the signature is verified before any side effect and before any parse into a decision;
//   - the signed timestamp bounds the replay window (billing.WebhookMaxSkew);
//   - the response body is minimal and carries no payload, no signature, and no secret — a rejected
//     webhook's diagnostics must not become a second leak.

// ─────────────────────────────────────────────────────────────────────────────
// The payment surface (P21 tasks 6.1–6.4)
// ─────────────────────────────────────────────────────────────────────────────

// PaymentsSource is what the API asks of the billing capability for the collection surface.
//
// ONE read method, not four. The billing page answers four questions that must agree with each
// other — what plan am I on, what has this period cost, what am I being charged, and can you charge
// me — and four independently-fetched documents is exactly how a page ends up showing a SUM that
// disagrees with the invoice next to it. That is p7.go's reasoning, and it applies here unchanged.
type PaymentsSource interface {
	// Payment returns the whole collection read model for one customer-period. An empty period means
	// "the current one".
	Payment(customerID, period string) (PaymentView, bool)
	// StartCheckout mints a provider-hosted collection session server-side.
	StartCheckout(ctx context.Context, customerID, planName, successURL, cancelURL string) (CheckoutView, error)
	// ChangePlan subscribes / upgrades / downgrades by plan NAME.
	ChangePlan(ctx context.Context, customerID, planName string) (PlanChangeView, error)
}

// PaymentView is the billing page's whole read model.
//
// It EMBEDS the P7 billing view rather than restating any of it. Every figure the page renders — SUM,
// meters, invoice lines and their bases, the mirrored provider state — is the same document the account
// surface reads, so the two cannot disagree, and P21 adds only what P7 did not have: what plans exist
// by name, whether a card is on file, and whether this deployment can collect one at all.
type PaymentView struct {
	// Billing is the P7 read model, verbatim.
	Billing BillingView `json:"billing"`
	// Plans is every plan the customer can move to, by NAME, with the DIRECTION the move would be.
	// There is no price here and there cannot be: the direction is computed from configured rank.
	Plans []PlanOptionView `json:"plans"`
	// PaymentMethod is the mirrored provider state for the card on file.
	PaymentMethod PaymentMethodView `json:"payment_method"`
	// CollectionAvailable is false when the wired provider cannot collect a payment method. The console
	// asks BEFORE rendering the control, so a customer is never offered a button that cannot work.
	CollectionAvailable bool `json:"collection_available"`
	// PricingIssue, when set, is the misconfigured-billing state: one or more plans cannot be purchased
	// because their price does not resolve at the payment provider. It is a DIFFERENT fact from
	// Unavailable (the provider is unreachable) and from an empty period, and rendering any of them as
	// another is the mistake the unhappy-state discipline exists to prevent.
	PricingIssue *PricingIssueView `json:"pricing_issue,omitempty"`
	// Unavailable, when set, is the named reason the billing provider could not be reached. It is a
	// first-class state (task 6.4): "billing is temporarily unavailable" and "you have no invoice" are
	// different facts, and rendering the second for the first is a lie the page tells confidently.
	Unavailable *BillingUnavailableView `json:"unavailable,omitempty"`
}

// PlanOptionView is one plan a customer may move to.
type PlanOptionView struct {
	PlanID string `json:"plan_id"`
	// Name is the customer-facing plan NAME. It is the only identifier the console sends back.
	Name    string `json:"name"`
	Rank    int    `json:"rank"`
	Current bool   `json:"current"`
	// Direction is subscribe / upgrade / downgrade / current, computed server-side from rank.
	Direction string `json:"direction"`
	// Subscribable is false for a plan with no subscription price reference (the free tier); the control
	// is then absent rather than present-and-broken.
	Subscribable bool `json:"subscribable"`
	// Unavailable is true when this plan's price does not resolve at the payment provider, so it cannot
	// be bought right now (P21 Decision 9). The control is disabled and SAYS SO, rather than being
	// offered and failing at checkout.
	Unavailable bool `json:"unavailable"`
}

// PaymentMethodView is the payment-method status, mirrored from the provider.
//
// Brand and the last four characters are DISPLAY facts — what a customer needs to answer "which card
// is this". They are not card data, not a token, and not enough to charge anything. There is no field
// here that could hold more, which is the point.
type PaymentMethodView struct {
	Present bool   `json:"present"`
	Brand   string `json:"brand,omitempty"`
	Last4   string `json:"last4,omitempty"`
	// Status is the provider's own word for the account's payment health (past_due, payment_failed,
	// ok), carried verbatim so the page never recomputes dunning.
	Status string `json:"status,omitempty"`
	// Reason and RestorePath are set on an unhappy status: what went wrong, in the provider's terms, and
	// what the customer does about it. A dunning banner with no next step is an alarm.
	Reason      string `json:"reason,omitempty"`
	RestorePath string `json:"restore_path,omitempty"`
}

// PricingIssueView is the customer's half of the price-reference preflight.
//
// 🔴 It carries plan NAMES and nothing else. The reference that failed, the provider's error and the
// charge kind are operator facts on `/readyz`; a customer needs to know they cannot buy this right now
// and that nobody has been charged. Putting an internal identifier in front of them would be the
// mechanism leak the billing copy rules forbid.
type PricingIssueView struct {
	// Plans is the customer-facing names of the plans that cannot be purchased right now.
	Plans []string `json:"plans"`
	// CheckedAt is when the platform last checked, so the state is not read as permanent.
	CheckedAt string `json:"checked_at,omitempty"`
}

// BillingUnavailableView is the named "we could not reach billing" state.
type BillingUnavailableView struct {
	// Detail is what actually failed, in words a support engineer can act on.
	Detail string `json:"detail"`
	// Retryable distinguishes an outage (come back) from a refusal (something must change first).
	Retryable bool `json:"retryable"`
}

// CheckoutView is what the console receives to send the browser to the provider.
//
// 🔴 Note what is absent: the Stripe API key. The console receives a short-lived URL or client secret
// and nothing else, which is design Decision 2 and Decision 6 expressed as a payload shape rather than
// as a rule somebody has to remember.
type CheckoutView struct {
	// URL is the hosted checkout page; ClientSecret is the embedded element's short-lived credential.
	URL          string `json:"url,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	SessionRef   string `json:"session_ref,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

// PlanChangeView is the result of a subscribe / upgrade / downgrade.
type PlanChangeView struct {
	PlanID   string `json:"plan_id"`
	PlanName string `json:"plan_name"`
	// Status is the provider's subscription status, verbatim.
	Status  string `json:"status,omitempty"`
	Changed bool   `json:"changed"`
	// CheckoutRequired is true when there is no subscription to move yet. The console then sends the
	// customer to checkout rather than showing a failure, because "you have not paid yet" is a step.
	CheckoutRequired bool `json:"checkout_required"`
}

// MountP21Payments registers the collection surface — P21 tasks 6.1 and 6.2.
func (s *Server) MountP21Payments(src PaymentsSource) {
	s.p21 = src
	s.Mux.HandleFunc("GET /api/p21/customers/{customer_id}/payment", s.handleP21Payment)
	s.Mux.HandleFunc("POST /api/p21/customers/{customer_id}/checkout-session", s.handleP21Checkout)
	s.Mux.HandleFunc("POST /api/p21/customers/{customer_id}/plan", s.handleP21Plan)
}

func (s *Server) handleP21Payment(w http.ResponseWriter, r *http.Request) {
	if s.p21 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the p21 payment surface is not mounted on this server"})
		return
	}
	customerID := r.PathValue("customer_id")
	v, ok := s.p21.Payment(customerID, r.URL.Query().Get("period"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no billing account for " + customerID})
		return
	}
	if v.Billing.LinkCoverage == nil {
		v.Billing.LinkCoverage = s.linkCoverageFor(customerID)
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleP21Checkout(w http.ResponseWriter, r *http.Request) {
	if s.p21 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the p21 payment surface is not mounted on this server"})
		return
	}
	var body struct {
		PlanName   string `json:"plan_name"`
		SuccessURL string `json:"success_url"`
		CancelURL  string `json:"cancel_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid checkout request"})
		return
	}
	v, err := s.p21.StartCheckout(r.Context(), r.PathValue("customer_id"), body.PlanName, body.SuccessURL, body.CancelURL)
	if err != nil {
		// 422 rather than 500: the request was understood and refused for a stated reason (an unknown
		// plan name, a provider that cannot collect, a plan with no subscription price). A 500 would send
		// the reader looking for an outage that is not happening.
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleP21Plan(w http.ResponseWriter, r *http.Request) {
	if s.p21 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the p21 payment surface is not mounted on this server"})
		return
	}
	var body struct {
		PlanName string `json:"plan_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid plan change request"})
		return
	}
	v, err := s.p21.ChangePlan(r.Context(), r.PathValue("customer_id"), body.PlanName)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// BillingWebhookSink is the billing capability's inbound webhook path. Satisfied by *billing.Service.
type BillingWebhookSink interface {
	// HandleStripeWebhook verifies, dedupes, persists and applies one delivery, and returns the HTTP
	// status the endpoint must write.
	HandleStripeWebhook(ctx context.Context, body []byte, signatureHeader string) billing.WebhookAck
}

// billingWebhookMaxBody bounds an inbound delivery. Stripe events are small; a megabyte is generous
// against the largest real one and is a hard stop against a POST that is not from Stripe at all.
const billingWebhookMaxBody = 1 << 20

// MountBillingWebhook registers `POST /billing/webhook` — P21 task 4.2.
//
// It is mounted separately from the /api/* surface on purpose. The path is documented as the single
// inbound-from-internet route (the mirror of P19's egress allowlist), and a deployment that does not
// expose it simply does not call this — there is no flag that half-enables an endpoint.
func (s *Server) MountBillingWebhook(sink BillingWebhookSink) {
	s.billingWebhook = sink
	s.Mux.HandleFunc("POST /billing/webhook", s.handleBillingWebhook)
}

func (s *Server) handleBillingWebhook(w http.ResponseWriter, r *http.Request) {
	if s.billingWebhook == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "the billing webhook path is not mounted on this server",
		})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, billingWebhookMaxBody))
	if err != nil {
		// A body that could not be read in full was never verifiable, so it is refused without touching
		// anything. It is a 400 rather than a 500: nothing was recorded, and a retry of an oversized or
		// truncated POST produces the same answer.
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "the webhook body could not be read in full — it was rejected before any side effect",
		})
		return
	}

	ack := s.billingWebhook.HandleStripeWebhook(r.Context(), body, r.Header.Get(billing.StripeSignatureHeader))

	// The status comes from the billing capability verbatim. This handler does not decide it, does not
	// upgrade it, and does not collapse it — see the file comment.
	writeJSON(w, ack.Status, ack)
}
