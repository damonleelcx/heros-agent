package api

import (
	"context"
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
