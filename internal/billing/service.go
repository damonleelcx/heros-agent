package billing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/plancfg"
)

// service.go is the billing capability's front door: subscriptions, metered reporting, charges,
// additive corrections, and the outage buffer's recovery drain.
//
// ## The charge protocol, and why the order is what it is
//
// Every charge follows the same four steps:
//
//	1. WRITE AHEAD    — append a `pending` billing_event under the idempotency key.
//	2. CALL PROVIDER  — with the SAME key.
//	3. SETTLE         — stamp the provider's refs onto the row, mark it `recorded`.
//	4. ON DUPLICATE   — if step 1 says the key exists, do NOT raise a second charge; resume or return.
//
// Step 1 before step 2 is deliberate. If the provider were called first, an ambiguous failure (the
// provider records the charge, the response is lost) would leave a charge the platform has no record
// of — invisible money. Writing ahead means the worst case is a `pending` row that a retry resumes,
// and the retry cannot double-charge because both the ledger's UNIQUE key and the provider's own
// idempotency refuse the duplicate.
//
// That is also the outage story: with the provider down, step 2 fails and the row stays `pending`. The
// usage is already safe in `usage_record`; the charge is buffered. `FlushPending` drains the buffer on
// recovery, under the same keys, so the outage window is billed exactly once.

// Service binds the provider, the ledger, the account store, the plan resolver and the meter.
type Service struct {
	provider Provider
	ledger   Ledger
	accounts account.Store
	plans    *plancfg.Resolver
	meter    *metering.Meter
	secrets  Secrets
	now      Clock

	// subs maps a customer to their provider subscription ref. Held here rather than on `account`
	// because it is a billing-integration detail, and `account` is the model three capabilities share.
	subs map[string]string

	// deliveries is the `webhook_delivery` dedupe table. A service with no delivery store REFUSES to
	// process webhooks rather than processing them un-deduped (see HandleWebhook).
	deliveries DeliveryStore
	// states mirrors the provider-owned invoice/subscription state per customer — what the UI's
	// payment-failed / past-due / dunning states render from. Mirrored, never computed.
	//
	// It is a StateStore rather than a map (P21 task 4.3) so that "the effect was persisted" is a claim
	// the webhook path can FAIL to make. With a map it is unrepresentable, and a persist-then-ack test
	// against a code path that cannot go wrong is a restatement of the implementation, not a test.
	stateMu sync.Mutex
	states  StateStore
	// subPlans is the platform's own record of which plan a customer's subscription grants (P21 task
	// 5.1). A Stripe INVOICE event carries no plan, so without this the entitlement sync would have to
	// guess one — and a guessed plan is entitlements nobody sold.
	subPlans map[string]string
	// freePlan is the degradation target. Empty means FreePlanID.
	freePlan string
	// pricing caches the last price-reference preflight (Decision 9). Cached rather than probed per
	// request: /readyz reports it, and resolving prices on every readiness check would make a provider
	// latency spike read as this service being unhealthy.
	pricing pricingCache

	// rollout is the P7 feature flag. Nil means "no rollout gate configured", which is the correct
	// behaviour for a test harness; a DEPLOYMENT wires one, and its zero value is fully dark.
	rollout *Rollout
	// observe emits the revenue signals on the P2.5 substrate and raises the two alerts. Nil is safe —
	// every method on *Observer nil-guards — because a telemetry outage must never stop a charge.
	observe *metering.Observer
}

// WithRollout attaches the P7 feature flag. Without one, no rollout gate is applied — the shape a
// hermetic test wants, and never the shape a deployment should have.
func (s *Service) WithRollout(r *Rollout) *Service {
	s.rollout = r
	return s
}

// WithObserver attaches the revenue observer.
func (s *Service) WithObserver(o *metering.Observer) *Service {
	s.observe = o
	return s
}

// Rollout returns the attached feature flag, if any.
func (s *Service) Rollout() *Rollout { return s.rollout }

// WithDeliveries attaches the webhook delivery store. Separate from the constructor because a
// deployment that does not expose a webhook endpoint legitimately has none — and in that case
// HandleWebhook must refuse rather than silently process an un-deduped delivery.
func (s *Service) WithDeliveries(d DeliveryStore) *Service {
	s.deliveries = d
	return s
}

// Deliveries exposes the webhook delivery store for read paths.
func (s *Service) Deliveries() DeliveryStore { return s.deliveries }

// NewService builds the billing service. Every dependency is required: a billing service missing its
// ledger or its plan resolver would fail at the first charge, and discovering that at charge time is
// the worst possible moment.
func NewService(p Provider, l Ledger, accts account.Store, plans *plancfg.Resolver, m *metering.Meter, secrets Secrets) (*Service, error) {
	if p == nil || l == nil || accts == nil || plans == nil || m == nil {
		return nil, errors.New("billing: provider, ledger, account store, plan resolver and meter are all required")
	}
	return &Service{provider: p, ledger: l, accounts: accts, plans: plans, meter: m, secrets: secrets,
		now: time.Now, subs: map[string]string{}, states: NewMemStates(),
		subPlans: map[string]string{}}, nil
}

// WithStates replaces the mirrored-state store. A deployment wires the durable one; a test wires one it
// can take down, which is how the persist-then-ack failure is injected rather than imagined.
func (s *Service) WithStates(st StateStore) *Service {
	if st != nil {
		s.states = st
	}
	return s
}

// SetClock injects a deterministic clock (tests).
func (s *Service) SetClock(c Clock) { s.now = c }

// Ledger exposes the append-only ledger for read paths (the invoice surface, the auditor).
func (s *Service) Ledger() Ledger { return s.ledger }

// Provider exposes the provider for read paths (invoice, subscription state).
func (s *Service) Provider() Provider { return s.provider }

// Describe names the live provider and secrets source — the health signal, never a credential.
func (s *Service) Describe() map[string]string {
	out := map[string]string{"provider": s.provider.Describe()}
	// The PRICING preflight's stored summary (Decision 9). "not_run" is a distinct answer from
	// "verified": a surface that reported the second for the first would tell an operator their pricing
	// is fine when nothing has checked it.
	if rep, ran := s.PricingStatus(); ran {
		out["pricing"] = rep.Summary()
	} else {
		out["pricing"] = "not_run"
	}
	if s.secrets != nil {
		info := s.secrets.Describe()
		out["secrets_source"] = info.Kind
		if info.Detail != "" {
			out["secrets_detail"] = info.Detail
		}
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Subscriptions (task 3.1)
// ─────────────────────────────────────────────────────────────────────────────

// StartSubscription places a customer on their active plan's subscription price and records the
// change in the ledger. Proration and dunning are the provider's; the platform stores only the handle.
func (s *Service) StartSubscription(ctx context.Context, customerID string) (SubscriptionResult, error) {
	acct, plan, err := s.resolve(customerID)
	if err != nil {
		return SubscriptionResult{}, err
	}
	priceRef := plan.PriceRefs["subscription"]
	if priceRef == "" {
		return SubscriptionResult{}, fmt.Errorf("billing: plan %q has no subscription price reference in the config store", plan.PlanID)
	}
	key := "subscribe:" + customerID + ":" + plan.PlanID + ":" + plan.Version
	res, err := s.provider.CreateSubscription(ctx, SubscriptionRequest{
		ProviderCustomerHandle: acct.ProviderCustomerHandle,
		PriceRef:               priceRef,
		PlanID:                 plan.PlanID,
		PlanName:               plan.DisplayName,
		IdempotencyKey:         key,
	})
	if err != nil {
		return SubscriptionResult{}, fmt.Errorf("billing: create subscription: %w", err)
	}
	s.subs[customerID] = res.SubscriptionRef

	// The subscription change is audited even though it moves no money yet: "when did this customer
	// become a Team subscriber" is a question the period must be able to answer.
	ev := BillingEvent{
		CustomerID: customerID, Type: TypeSubscriptionChange, IdempotencyKey: key,
		CausedBy: "plan:" + plan.PlanID + "@" + plan.Version, Reason: "subscription started on plan " + plan.DisplayName,
		Status: StatusRecorded, ProviderRef: res.SubscriptionRef, CreatedAt: s.now().UTC(),
	}
	if _, err := s.ledger.Append(ev); err != nil && !errors.Is(err, ErrDuplicateKey) {
		return SubscriptionResult{}, fmt.Errorf("billing: audit subscription change: %w", err)
	}
	return res, nil
}

// SubscriptionRef is the provider subscription handle held for a customer, if any.
func (s *Service) SubscriptionRef(customerID string) string { return s.subs[customerID] }

// SubscriptionState reads the provider's own subscription state — including dunning. The platform
// REFLECTS it; it never recomputes it.
func (s *Service) SubscriptionState(ctx context.Context, customerID string) (SubscriptionResult, error) {
	ref := s.subs[customerID]
	if ref == "" {
		return SubscriptionResult{}, fmt.Errorf("billing: no subscription for %s", customerID)
	}
	return s.provider.Subscription(ctx, ref)
}

// ─────────────────────────────────────────────────────────────────────────────
// Metered usage + charges (task 3.1, 3.2)
// ─────────────────────────────────────────────────────────────────────────────

// ReportUsage hands one meter's recorded usage to the provider and marks the usage record reported.
//
// The idempotency key is derived from {customer, period, metric}, so a retried report for the same
// tuple is the SAME report — the provider recognizes it and records nothing new (FR10).
func (s *Service) ReportUsage(ctx context.Context, customerID string, p metering.Period, metric metering.Metric) (UsageResult, error) {
	acct, plan, err := s.resolve(customerID)
	if err != nil {
		return UsageResult{}, err
	}
	rec, err := s.meter.Usage().Get(metering.Key{CustomerID: customerID, Period: p.ID, Metric: metric})
	if err != nil {
		return UsageResult{}, fmt.Errorf("billing: no usage to report: %w", err)
	}
	res, err := s.provider.ReportUsage(ctx, UsageReport{
		ProviderCustomerHandle: acct.ProviderCustomerHandle,
		SubscriptionRef:        s.subs[customerID],
		Metric:                 string(metric),
		Period:                 p.ID,
		Quantity:               rec.Quantity,
		PriceRef:               plan.PriceRefs["metered"],
		IdempotencyKey:         UsageReportIdempotencyKey(customerID, p.ID, string(metric)),
	})
	if err != nil {
		// The usage stays un-reported in `usage_record`, so recovery re-reports it under the same key.
		// Nothing is lost and nothing is billed twice.
		return UsageResult{}, fmt.Errorf("billing: report usage: %w", err)
	}
	if _, err := s.meter.Usage().MarkReported(rec.Key(), res.UsageRef); err != nil {
		return res, fmt.Errorf("billing: mark usage reported: %w", err)
	}
	s.observe.UsageReported(customerID, p, metric, rec.Quantity)
	if metric == metering.MetricSUM {
		s.observe.SUMRecorded(customerID, p, rec.Quantity)
	}
	return res, nil
}

// Charge raises one charge for a customer-period (the design's
// `Charge(customer, period, kind, idempotency_key)`).
//
// It returns the ledger row. A retry under the same key returns the ORIGINAL row and raises no second
// charge, whether the first attempt succeeded, failed ambiguously, or was buffered by an outage.
func (s *Service) Charge(ctx context.Context, customerID string, p metering.Period, kind ChargeKind, idempotencyKey string) (BillingEvent, error) {
	return s.charge(ctx, chargeSpec{
		CustomerID: customerID, Period: p, Kind: kind, IdempotencyKey: idempotencyKey,
	})
}

// chargeSpec is one charge's full intent. Gainshare adds evidence; nothing else differs, which is why
// there is one charge path rather than three that can drift.
type chargeSpec struct {
	CustomerID     string
	Period         metering.Period
	Kind           ChargeKind
	IdempotencyKey string
	// Quantity / CausedBy / Description override the defaults derived from the meter.
	Quantity    float64
	HasQuantity bool
	CausedBy    string
	Description string
	Evidence    []string
	EventType   EventType
}

func (s *Service) charge(ctx context.Context, spec chargeSpec) (BillingEvent, error) {
	if !KnownChargeKind(spec.Kind) {
		return BillingEvent{}, fmt.Errorf("billing: refusing a charge of unknown kind %q", spec.Kind)
	}
	if spec.IdempotencyKey == "" {
		return BillingEvent{}, errors.New("billing: a charge requires an idempotency key")
	}
	// The rollout gate comes BEFORE anything is written or called: a dark deployment must not leave
	// pending ledger rows behind that a later flip would settle into real charges.
	if s.rollout != nil {
		if err := s.rollout.AllowCharge(spec.Kind); err != nil {
			return BillingEvent{}, err
		}
	}
	acct, plan, err := s.resolve(spec.CustomerID)
	if err != nil {
		return BillingEvent{}, err
	}

	qty, causedBy := spec.Quantity, spec.CausedBy
	if !spec.HasQuantity {
		// A metered charge bills the usage record; a subscription charge is a flat period charge.
		switch spec.Kind {
		case KindMetered:
			rec, err := s.meter.Usage().Get(metering.Key{CustomerID: spec.CustomerID, Period: spec.Period.ID, Metric: metering.MetricSUM})
			if err != nil {
				return BillingEvent{}, fmt.Errorf("billing: metered charge has no usage to bill: %w", err)
			}
			qty = rec.Quantity
			causedBy = fmt.Sprintf("usage_record:%s/%s/%s", spec.CustomerID, spec.Period.ID, metering.MetricSUM)
		case KindSubscription:
			qty = 1
			causedBy = fmt.Sprintf("plan:%s@%s", plan.PlanID, plan.Version)
		}
	}
	if causedBy == "" {
		return BillingEvent{}, ErrNoCause
	}
	priceRef := plan.PriceRefs[string(spec.Kind)]
	if priceRef == "" {
		return BillingEvent{}, fmt.Errorf("billing: plan %q has no %s price reference in the config store", plan.PlanID, spec.Kind)
	}
	evType := spec.EventType
	if evType == "" {
		evType = TypeCharge
	}
	desc := spec.Description
	if desc == "" {
		desc = causedBy
	}

	// ── 1. WRITE AHEAD ────────────────────────────────────────────────────────
	row, err := s.ledger.Append(BillingEvent{
		CustomerID: spec.CustomerID, Period: spec.Period.ID, Type: evType, Kind: spec.Kind,
		IdempotencyKey: spec.IdempotencyKey, CausedBy: causedBy, Quantity: qty,
		Evidence: spec.Evidence, Status: StatusPending, CreatedAt: s.now().UTC(),
	})
	switch {
	case err == nil:
		// New charge — fall through to the provider.
	case errors.Is(err, ErrDuplicateKey):
		if row.Status == StatusRecorded {
			// Already charged. This is the never-double-charge path: return the original, call nothing.
			return row, nil
		}
		// A previous attempt was buffered or failed ambiguously. Resume it under the SAME key; the
		// provider will either record it once or recognize it as the duplicate it already has.
	default:
		return BillingEvent{}, fmt.Errorf("billing: write-ahead ledger append: %w", err)
	}

	// ── 2. CALL PROVIDER (same key) ───────────────────────────────────────────
	res, perr := s.provider.RaiseCharge(ctx, ChargeRequest{
		ProviderCustomerHandle: acct.ProviderCustomerHandle,
		Kind:                   spec.Kind,
		Period:                 spec.Period.ID,
		PriceRef:               priceRef,
		Quantity:               qty,
		IdempotencyKey:         spec.IdempotencyKey,
		Description:            desc,
	})
	if perr != nil {
		// BUFFERED: the row stays `pending`, FlushPending drains it on recovery under the same key.
		// This ALERTS (task 8.1): a charge that did not settle is revenue that silently did not happen.
		s.observe.ChargeFailed(spec.CustomerID, spec.Period.ID, string(spec.Kind), qty, perr.Error())
		return row, fmt.Errorf("billing: charge buffered, provider call failed: %w", perr)
	}

	// ── 3. SETTLE ─────────────────────────────────────────────────────────────
	settled, err := s.ledger.Settle(spec.IdempotencyKey, res.ChargeRef, res.AmountRef, s.now())
	if err != nil && !errors.Is(err, ErrAlreadySettled) {
		return row, fmt.Errorf("billing: settle: %w", err)
	}
	if spec.Kind == KindGainshare {
		s.observe.GainshareBilled(spec.CustomerID, spec.Period, qty, len(spec.Evidence))
	}
	return settled, nil
}

// FlushPending drains the outage buffer: every `pending` row is retried against the provider under its
// ORIGINAL idempotency key, so the outage window is billed exactly once.
//
// It returns the rows it settled and the rows still pending. Both matter — "how much is still buffered"
// is a health signal, and reporting only successes would let a permanently stuck row hide forever.
func (s *Service) FlushPending(ctx context.Context) (settled []BillingEvent, stillPending []BillingEvent, err error) {
	for _, row := range s.ledger.Pending() {
		if !row.Type.ChargeBearing() {
			continue
		}
		acct, plan, rerr := s.resolve(row.CustomerID)
		if rerr != nil {
			stillPending = append(stillPending, row)
			continue
		}
		res, perr := s.provider.RaiseCharge(ctx, ChargeRequest{
			ProviderCustomerHandle: acct.ProviderCustomerHandle,
			Kind:                   row.Kind,
			Period:                 row.Period,
			PriceRef:               plan.PriceRefs[string(row.Kind)],
			Quantity:               row.Quantity,
			IdempotencyKey:         row.IdempotencyKey,
			Description:            row.CausedBy,
		})
		if perr != nil {
			stillPending = append(stillPending, row)
			continue
		}
		done, serr := s.ledger.Settle(row.IdempotencyKey, res.ChargeRef, res.AmountRef, s.now())
		if serr != nil && !errors.Is(serr, ErrAlreadySettled) {
			stillPending = append(stillPending, row)
			continue
		}
		settled = append(settled, done)
	}
	return settled, stillPending, nil
}

// resolve fetches the account and its live plan definition together — the pair every billing operation
// needs, resolved in one place so a caller cannot use one without the other.
func (s *Service) resolve(customerID string) (account.Account, plancfg.PlanConfig, error) {
	acct, err := s.accounts.Get(customerID)
	if err != nil {
		return account.Account{}, plancfg.PlanConfig{}, err
	}
	plan, err := s.plans.ResolvePlan(acct.ActivePlanID)
	if err != nil {
		return account.Account{}, plancfg.PlanConfig{}, fmt.Errorf("billing: resolve plan for %s: %w", customerID, err)
	}
	return acct, plan, nil
}
