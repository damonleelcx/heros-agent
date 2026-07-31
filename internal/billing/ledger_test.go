package billing

import (
	"strings"
	"testing"
)

// TestEveryChargeBearingIdempotencyKeyIsCustomerScoped is a fence, not a unit test.
//
// A provider key that two customers can both produce is a charge or a credit that can be answered with
// the other customer's object. Four of the five key helpers were customer-scoped from the start and the
// fifth was not, which is exactly how this class of bug survives review: the odd one out looks like the
// others until someone lines them up. This lines them up.
func TestEveryChargeBearingIdempotencyKeyIsCustomerScoped(t *testing.T) {
	const (
		a = "cus_alpha"
		b = "cus_beta"
	)
	for _, tc := range []struct {
		name string
		key  func(customer string) string
	}{
		{"usage report", func(c string) string { return UsageReportIdempotencyKey(c, "2026-07", "sum") }},
		{"metered charge", func(c string) string { return MeteredChargeIdempotencyKey(c, "2026-07", "sum") }},
		{"subscription charge", func(c string) string { return SubscriptionChargeIdempotencyKey(c, "2026-07", "team") }},
		{"gainshare charge", func(c string) string { return GainshareIdempotencyKey(c, "2026-07") }},
		// The event id is deliberately IDENTICAL for both customers here. That is the whole scenario:
		// two ledgers whose sequences are not globally unique, which is the ordinary case for a
		// per-tenant sequence or a restored ledger.
		{"correction", func(c string) string { return CorrectionIdempotencyKey(TypeCredit, c, "be_000002", "wrong period") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ka, kb := tc.key(a), tc.key(b)
			if ka == kb {
				t.Fatalf("two customers produce the SAME provider idempotency key %q — the second customer's "+
					"call would be answered with the first customer's object", ka)
			}
			if !strings.Contains(ka, a) {
				t.Errorf("key %q does not carry the customer id, so nothing prevents the collision above", ka)
			}
		})
	}
}
