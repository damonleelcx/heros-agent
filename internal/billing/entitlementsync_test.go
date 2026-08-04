package billing

import (
	"context"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/plancfg"
)

// entitlementsync_test.go is P21 section 5: what a customer can do follows what they pay for, in both
// directions, audited, and reversible.
//
// The tests are written around the two failures that actually happen in production. One: a customer
// stops paying and keeps the paid entitlement, which is revenue quietly leaking. Two: a customer pays
// and does not get it back, which is a support ticket that starts with "I was charged". Both are
// asserted here as round trips rather than as single transitions, because a one-way test passes on an
// implementation that can only go one way.

// syncHarness is a webhook harness plus the entitlement gate the plan change must be visible through.
type syncHarness struct {
	*webhookHarness
	gate *entitlement.Gate
	h    *harness
}

func newSyncHarness(t *testing.T) *syncHarness {
	t.Helper()
	h := newHarness(t, "free")
	deliveries, states := NewMemDeliveries(), NewMemStates()
	h.svc.WithDeliveries(deliveries).WithStates(states)
	gate := entitlement.NewGate(h.accounts, h.plans, h.usage)
	return &syncHarness{
		webhookHarness: &webhookHarness{svc: h.svc, deliveries: deliveries, states: states, now: clockNow},
		gate:           gate,
		h:              h,
	}
}

// planChanges returns the customer's TypePlanChange ledger rows, oldest first.
func (s *syncHarness) planChanges(customerID string) []BillingEvent {
	var out []BillingEvent
	for _, ev := range testEvents(s.svc.Ledger(), customerID, "") {
		if ev.Type == TypePlanChange {
			out = append(out, ev)
		}
	}
	return out
}

func (s *syncHarness) activePlan(t *testing.T, customerID string) string {
	t.Helper()
	acct, err := s.h.accounts.Get(customerID)
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	return acct.ActivePlanID
}

// TestPaidSubscriptionGrantsThePlanByAnAuditedChange is task 5.1 / FR17.
func TestPaidSubscriptionGrantsThePlanByAnAuditedChange(t *testing.T) {
	s := newSyncHarness(t)
	ctx := context.Background()

	// The customer starts on Free, and the dashboard is not theirs to use.
	if got := s.activePlan(t, "cus_acme"); got != "free" {
		t.Fatalf("starting plan = %q, want free", got)
	}
	before, err := s.gate.CheckEntitlement("cus_acme", plancfg.FeatureDashboard, entitlement.LevelAssisted)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if before.Allowed {
		t.Fatal("the fixture's free plan already entitles the dashboard — this test would prove nothing")
	}

	// The platform records what was subscribed to, then Stripe says it was paid.
	s.svc.RecordSubscriptionPlan("cus_acme", "team")
	body, sig := s.stripeDelivery("evt_paid", WebhookInvoicePaid, "cus_acme", map[string]string{"invoice_ref": "in_1"})
	ack := s.svc.HandleStripeWebhook(ctx, body, sig)
	if ack.Status != 200 || !ack.Applied {
		t.Fatalf("invoice.paid: %+v", ack)
	}

	// The plan moved, and it PINNED the config version it resolved under — without the version a closed
	// period can no longer be explained after the next publish.
	acct, _ := s.h.accounts.Get("cus_acme")
	if acct.ActivePlanID != "team" {
		t.Fatalf("active plan = %q, want team", acct.ActivePlanID)
	}
	if acct.PlanConfigVersion == "" || acct.PlanConfigVersion != s.h.plans.Version() {
		t.Errorf("plan_config_version = %q, want the resolver's %q", acct.PlanConfigVersion, s.h.plans.Version())
	}

	// It is AUDITED: one TypePlanChange row, naming the event that caused it.
	rows := s.planChanges("cus_acme")
	if len(rows) != 1 {
		t.Fatalf("%d plan-change rows, want 1: %+v", len(rows), rows)
	}
	if !strings.Contains(rows[0].CausedBy, "evt_paid") {
		t.Errorf("the audit row does not name the event that caused it: %+v", rows[0])
	}
	if rows[0].Reason == "" {
		t.Error("the audit row carries no reason — 'why did this tenant change plan' must be answerable from the ledger")
	}

	// And the GATE reflects it. Asserting the account field alone would pass on an implementation that
	// moved the plan somewhere the gate does not read.
	after, err := s.gate.CheckEntitlement("cus_acme", plancfg.FeatureDashboard, entitlement.LevelAssisted)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !after.Allowed {
		t.Errorf("the entitlement gate does not reflect the paid plan: %+v", after)
	}
}

// TestSubscriptionUpdatedActiveGrantsFromTheEventsOwnMetadata proves the plan can arrive on the event
// itself — the metadata the platform stamped when it created the subscription — with no local record.
func TestSubscriptionUpdatedActiveGrantsFromTheEventsOwnMetadata(t *testing.T) {
	s := newSyncHarness(t)
	body, sig := s.stripeDelivery("evt_active", WebhookSubscriptionUpdated, "cus_acme", map[string]string{
		"status": "active", "plan_id": "team",
	})
	if ack := s.svc.HandleStripeWebhook(context.Background(), body, sig); ack.Status != 200 {
		t.Fatalf("%+v", ack)
	}
	if got := s.activePlan(t, "cus_acme"); got != "team" {
		t.Errorf("active plan = %q, want team", got)
	}
}

// TestDunningGraceWindowKeepsTheEntitlement is task 5.2's first half.
//
// Nothing happens on payment_failed or past_due. That is not leniency — it is refusing to fight
// Stripe's dunning schedule, which the platform does not own and must not recompute.
func TestDunningGraceWindowKeepsTheEntitlement(t *testing.T) {
	s := newSyncHarness(t)
	ctx := context.Background()
	s.svc.RecordSubscriptionPlan("cus_acme", "team")

	body, sig := s.stripeDelivery("evt_paid", WebhookInvoicePaid, "cus_acme", nil)
	if ack := s.svc.HandleStripeWebhook(ctx, body, sig); ack.Status != 200 {
		t.Fatalf("%+v", ack)
	}
	rowsBefore := len(s.planChanges("cus_acme"))

	for _, step := range []struct {
		id    string
		typ   WebhookType
		extra map[string]string
	}{
		{"evt_failed", WebhookInvoicePaymentFailed, map[string]string{"invoice_ref": "in_2"}},
		{"evt_pastdue", WebhookSubscriptionPastDue, nil},
		{"evt_updated_pastdue", WebhookSubscriptionUpdated, map[string]string{"status": "past_due"}},
	} {
		body, sig := s.stripeDelivery(step.id, step.typ, "cus_acme", step.extra)
		if ack := s.svc.HandleStripeWebhook(ctx, body, sig); ack.Status != 200 {
			t.Fatalf("%s: %+v", step.typ, ack)
		}
		// 🔴 The entitlement survives the whole grace window Stripe is retrying in.
		if got := s.activePlan(t, "cus_acme"); got != "team" {
			t.Fatalf("%s degraded the plan to %q mid-grace-window — that fights Stripe's dunning schedule "+
				"and yanks a paying customer's access over a transient decline", step.typ, got)
		}
	}
	if n := len(s.planChanges("cus_acme")); n != rowsBefore {
		t.Errorf("the grace window authored %d plan-change rows, want 0", n-rowsBefore)
	}
	// The state IS mirrored, so the console can show a past-due banner. Mirrored, not acted on.
	if st := s.svc.BillingState("cus_acme"); !st.PastDue || !st.PaymentFailed {
		t.Errorf("the dunning state was not mirrored: %+v", st)
	}
}

// TestCanceledSubscriptionDegradesToFreeAndDeletesNothing is task 5.2's second half.
func TestCanceledSubscriptionDegradesToFreeAndDeletesNothing(t *testing.T) {
	s := newSyncHarness(t)
	ctx := context.Background()
	s.svc.RecordSubscriptionPlan("cus_acme", "team")

	body, sig := s.stripeDelivery("evt_paid", WebhookInvoicePaid, "cus_acme", nil)
	if ack := s.svc.HandleStripeWebhook(ctx, body, sig); ack.Status != 200 {
		t.Fatalf("%+v", ack)
	}
	acctBefore, _ := s.h.accounts.Get("cus_acme")
	ledgerBefore := len(testEvents(s.svc.Ledger(), "cus_acme", ""))

	body, sig = s.stripeDelivery("evt_canceled", WebhookSubscriptionCanceled, "cus_acme", nil)
	ack := s.svc.HandleStripeWebhook(ctx, body, sig)
	if ack.Status != 200 || !ack.Applied {
		t.Fatalf("subscription.deleted: %+v", ack)
	}

	if got := s.activePlan(t, "cus_acme"); got != "free" {
		t.Fatalf("active plan after cancel = %q, want free", got)
	}
	// 🔴 NOTHING was deleted. The account is the same account; the ledger only grew.
	acctAfter, err := s.h.accounts.Get("cus_acme")
	if err != nil {
		t.Fatalf("the account was DELETED by a degradation: %v", err)
	}
	if acctAfter.CustomerID != acctBefore.CustomerID || acctAfter.CreatedAt != acctBefore.CreatedAt {
		t.Errorf("the account was replaced rather than repointed: %+v -> %+v", acctBefore, acctAfter)
	}
	if acctAfter.ProviderCustomerHandle != acctBefore.ProviderCustomerHandle {
		t.Error("the provider customer handle was dropped — the billing history would be split")
	}
	if after := len(testEvents(s.svc.Ledger(), "cus_acme", "")); after <= ledgerBefore {
		t.Errorf("the ledger did not grow (%d -> %d) — the degradation was not audited", ledgerBefore, after)
	}

	// The gate follows it down, too.
	d, err := s.gate.CheckEntitlement("cus_acme", plancfg.FeatureDashboard, entitlement.LevelAssisted)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if d.Allowed {
		t.Error("the entitlement gate still grants the paid feature after cancellation")
	}
	// The denial names a path back rather than being a bare no.
	if d.Reason == "" {
		t.Error("the denial carries no reason")
	}
}

// TestTerminalSubscriptionUpdateIsAlsoTheGraceEnd covers the statuses that mean Stripe has finished
// retrying, delivered as `customer.subscription.updated` rather than as a deletion.
func TestTerminalSubscriptionUpdateIsAlsoTheGraceEnd(t *testing.T) {
	for _, status := range []string{"canceled", "unpaid", "incomplete_expired"} {
		t.Run(status, func(t *testing.T) {
			s := newSyncHarness(t)
			s.svc.RecordSubscriptionPlan("cus_acme", "team")
			body, sig := s.stripeDelivery("evt_paid", WebhookInvoicePaid, "cus_acme", nil)
			if ack := s.svc.HandleStripeWebhook(context.Background(), body, sig); ack.Status != 200 {
				t.Fatalf("%+v", ack)
			}

			body, sig = s.stripeDelivery("evt_term", WebhookSubscriptionUpdated, "cus_acme", map[string]string{"status": status})
			if ack := s.svc.HandleStripeWebhook(context.Background(), body, sig); ack.Status != 200 {
				t.Fatalf("%+v", ack)
			}
			if got := s.activePlan(t, "cus_acme"); got != "free" {
				t.Errorf("status %q left the plan at %q, want free", status, got)
			}
		})
	}
}

// TestDegradationIsReversible is task 5.3 — the round trip, with every audit row intact.
func TestDegradationIsReversible(t *testing.T) {
	s := newSyncHarness(t)
	ctx := context.Background()
	s.svc.RecordSubscriptionPlan("cus_acme", "team")

	steps := []struct {
		id       string
		typ      WebhookType
		extra    map[string]string
		wantPlan string
	}{
		{"evt_1", WebhookInvoicePaid, nil, "team"},
		{"evt_2", WebhookSubscriptionCanceled, nil, "free"},
		{"evt_3", WebhookInvoicePaid, nil, "team"},
		{"evt_4", WebhookSubscriptionCanceled, nil, "free"},
		{"evt_5", WebhookInvoicePaid, nil, "team"},
	}
	for _, step := range steps {
		body, sig := s.stripeDelivery(step.id, step.typ, "cus_acme", step.extra)
		if ack := s.svc.HandleStripeWebhook(ctx, body, sig); ack.Status != 200 {
			t.Fatalf("%s: %+v", step.id, ack)
		}
		if got := s.activePlan(t, "cus_acme"); got != step.wantPlan {
			t.Fatalf("%s (%s): plan = %q, want %q", step.id, step.typ, got, step.wantPlan)
		}
	}

	// 🔴 Every transition left its row. Five moves, five rows, none overwritten — the ledger is
	// append-only, so the whole history of what this tenant could do is reconstructable.
	rows := s.planChanges("cus_acme")
	if len(rows) != len(steps) {
		t.Fatalf("%d plan-change rows for %d transitions — a reversal must ADD a row, never edit one: %+v",
			len(rows), len(steps), rows)
	}
	for i, ev := range rows {
		if ev.Status != StatusRecorded {
			t.Errorf("row %d is %s, want recorded", i, ev.Status)
		}
		if !strings.Contains(ev.CausedBy, steps[i].id) {
			t.Errorf("row %d names %q, want the event %q — the rows are out of order or overwritten",
				i, ev.CausedBy, steps[i].id)
		}
	}

	// And the gate is back where it started.
	d, err := s.gate.CheckEntitlement("cus_acme", plancfg.FeatureDashboard, entitlement.LevelAssisted)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !d.Allowed {
		t.Error("paying again did not restore the entitlement")
	}
}

// TestARedeliveryDoesNotAuthorASecondPlanChangeRow: the dedupe covers the entitlement sync too, and a
// no-op change authors nothing even when it does run.
func TestARedeliveryDoesNotAuthorASecondPlanChangeRow(t *testing.T) {
	s := newSyncHarness(t)
	ctx := context.Background()
	s.svc.RecordSubscriptionPlan("cus_acme", "team")

	body, sig := s.stripeDelivery("evt_paid", WebhookInvoicePaid, "cus_acme", nil)
	for i := 0; i < 4; i++ {
		if ack := s.svc.HandleStripeWebhook(ctx, body, sig); ack.Status != 200 {
			t.Fatalf("delivery %d: %+v", i, ack)
		}
	}
	if n := len(s.planChanges("cus_acme")); n != 1 {
		t.Errorf("%d plan-change rows after four deliveries of one event, want 1", n)
	}

	// A DIFFERENT event that would move the plan to where it already is authors nothing either: an audit
	// entry claiming a change that did not happen is worse than no entry.
	body, sig = s.stripeDelivery("evt_paid_again", WebhookInvoicePaid, "cus_acme", nil)
	if ack := s.svc.HandleStripeWebhook(ctx, body, sig); ack.Status != 200 {
		t.Fatalf("%+v", ack)
	}
	if n := len(s.planChanges("cus_acme")); n != 1 {
		t.Errorf("a no-op plan change authored a row: %d rows", n)
	}
}

// TestAnUnknownPlanIsRefusedRatherThanSet: setting a plan the configuration does not know would leave
// the account pointing at a definition nothing can resolve, and then every entitlement check fails
// closed for a customer who paid.
func TestAnUnknownPlanIsRefusedRatherThanSet(t *testing.T) {
	s := newSyncHarness(t)
	body, sig := s.stripeDelivery("evt_bogus", WebhookSubscriptionUpdated, "cus_acme", map[string]string{
		"status": "active", "plan_id": "platinum-unlimited",
	})
	ack := s.svc.HandleStripeWebhook(context.Background(), body, sig)
	if ack.Status < 500 {
		t.Fatalf("status = %d, want 5xx — the event was not fully applied, so it must be retried", ack.Status)
	}
	if got := s.activePlan(t, "cus_acme"); got != "free" {
		t.Errorf("the account moved to %q on an unresolvable plan", got)
	}
	// The claim was released, so Stripe's retry can succeed once the plan config is published.
	if s.deliveries.Seen("evt_bogus") {
		t.Error("the claim survived a failed sync — the retry would apply nothing")
	}
}

// TestAnEventThePlatformCannotAttributeChangesNoEntitlement: Stripe sends events for objects the
// platform did not create, and guessing which account they belong to is worse than doing nothing.
func TestAnEventThePlatformCannotAttributeChangesNoEntitlement(t *testing.T) {
	s := newSyncHarness(t)
	body, sig := s.stripeDelivery("evt_stranger", WebhookInvoicePaid, "", nil)
	if ack := s.svc.HandleStripeWebhook(context.Background(), body, sig); ack.Status != 200 {
		t.Fatalf("an unattributable event must be acknowledged, not retried forever: %+v", ack)
	}
	if got := s.activePlan(t, "cus_acme"); got != "free" {
		t.Errorf("an unattributable event moved a plan to %q", got)
	}
}

// TestACancellationForAnUnknownAccountIsAcknowledged is the retry-loop fence.
//
// 🔴 Found on the LIVE deployment, by a $0.00 subscription created solely to prove Stripe could deliver.
// Its `customer.subscription.deleted` carried `platform_customer_id` for an account that does not exist
// here — which decodeWebhook trusts ahead of the handle lookup, precisely because the platform stamps
// that field itself. `applyPlanChange` then called `accounts.Get`, got ErrNotFound, and failed the whole
// delivery; the claim was released and Stripe retried an event that could never succeed. The mirror had
// already been written, so the visible state was worse than either outcome alone: the effect applied,
// no delivery row, and an endpoint retrying forever.
//
// The empty-customer guard above it did not help — the id was PRESENT. "Cannot attribute this to one of
// our accounts" has two shapes and only one was covered.
func TestACancellationForAnUnknownAccountIsAcknowledged(t *testing.T) {
	h, deliveries := withWebhooks(t)

	in := signed(t, WebhookPayload{
		ProviderEventID: "evt_cancel_unknown_account",
		Type:            WebhookSubscriptionCanceled,
		CustomerID:      "cus_not_an_account_here",
		Status:          "canceled",
	}, clockNow)

	res, err := h.svc.HandleWebhook(context.Background(), in)
	if err != nil {
		t.Fatalf("a cancellation for an unknown account FAILED the delivery (%v) — Stripe would retry "+
			"an event that can never succeed", err)
	}
	if res.PlanChanged {
		t.Error("an account that does not exist had its plan changed")
	}
	// The claim must STICK. A released claim is what turns this into an infinite retry.
	if deliveries.Count() != 1 {
		t.Errorf("deliveries recorded = %d, want 1 — the claim was released, so the provider retries", deliveries.Count())
	}
	if !deliveries.Seen("evt_cancel_unknown_account") {
		t.Error("the delivery was not recorded, so a redelivery would be reprocessed")
	}
}
