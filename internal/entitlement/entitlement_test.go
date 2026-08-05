package entitlement

import (
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/plancfg"
)

// The fixture config store: four NAMED plans with limits and OPAQUE price references. No amount
// appears anywhere — a price is a provider handle, and the packaging that matters here is the
// feature/limit table, which is configuration.
const fixtureCatalog = `{
  "version": "cfg-test-1",
  "plans": [
    {"plan_id":"free","display_name":"Free","rank":0,
     "features":["cli","discovery"],
     "limits":{"sum_band":100,"seats":1,"retention_days":7,"eval_compute":10},
     "price_refs":{}},
    {"plan_id":"team","display_name":"Team","rank":1,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":1000,"seats":5,"retention_days":30,"eval_compute":100},
     "price_refs":{"subscription":"price_ref_team_sub","metered":"price_ref_team_metered"}},
    {"plan_id":"business","display_name":"Business","rank":2,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":10000,"seats":25,"retention_days":90,"eval_compute":1000},
     "price_refs":{"subscription":"price_ref_biz_sub","metered":"price_ref_biz_metered"}},
    {"plan_id":"enterprise","display_name":"Enterprise","rank":3,
     "features":["cli","discovery","assisted_pr","dashboard","auto_merge"],
     "limits":{"seats":500,"retention_days":365},
     "price_refs":{"subscription":"price_ref_ent_sub","metered":"price_ref_ent_metered","gainshare":"price_ref_ent_gainshare"}}
  ]
}`

var (
	julyStart = time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	july      = metering.MonthPeriod(julyStart)
)

// allPlans is every plan id in the fixture, so a matrix test iterates the REAL catalog.
var allPlans = []string{"free", "team", "business", "enterprise"}

func newGate(t *testing.T) (*Gate, *account.MemStore, *metering.MemUsageStore, *plancfg.Resolver) {
	t.Helper()
	src := plancfg.NewMemSource()
	if err := src.PublishJSON([]byte(fixtureCatalog)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	plans := plancfg.NewResolver(src, plancfg.NewMemAudit())
	plans.SetClock(func() time.Time { return julyStart })
	if _, err := plans.Reload("fixture"); err != nil {
		t.Fatalf("reload: %v", err)
	}
	accts := account.NewMemStore()
	for _, id := range allPlans {
		if _, err := accts.Create(account.Account{
			CustomerID: "cus_" + id, ProviderCustomerHandle: "prov_cus_" + id,
			ActivePlanID: id, PlanConfigVersion: plans.Version(), CreatedAt: julyStart,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	usage := metering.NewMemUsageStore()
	g := NewGate(accts, plans, usage)
	g.SetClock(func() time.Time { return julyStart })
	return g, accts, usage, plans
}

// TestEntitlementMatrix is task 5.4 / FR5 / FR8 — the packaging boundary as a table.
//
// It iterates the REAL Surfaces list against the REAL plan catalog, so a surface added later without a
// matrix entry fails here rather than silently going ungated.
func TestEntitlementMatrix(t *testing.T) {
	g, _, _, _ := newGate(t)

	// want[feature][plan] = allowed.
	want := map[plancfg.Feature]map[string]bool{
		plancfg.FeatureCLI:        {"free": true, "team": true, "business": true, "enterprise": true},
		plancfg.FeatureDiscovery:  {"free": true, "team": true, "business": true, "enterprise": true},
		plancfg.FeatureAssistedPR: {"free": false, "team": true, "business": true, "enterprise": true},
		plancfg.FeatureDashboard:  {"free": false, "team": true, "business": true, "enterprise": true},
		plancfg.FeatureAutoMerge:  {"free": false, "team": false, "business": false, "enterprise": true},
	}
	if len(want) != len(Surfaces) {
		t.Fatalf("the matrix covers %d surfaces but %d are gated — a surface is untested", len(want), len(Surfaces))
	}

	for _, s := range Surfaces {
		row, ok := want[s.Feature]
		if !ok {
			t.Fatalf("gated surface %q has no matrix row", s.Feature)
		}
		for _, plan := range allPlans {
			t.Run(string(s.Feature)+"/"+plan, func(t *testing.T) {
				d, err := s.Check(g, "cus_"+plan)
				if err != nil {
					t.Fatalf("check: %v", err)
				}
				if err := d.Validate(); err != nil {
					t.Fatalf("incoherent decision: %v (%+v)", err, d)
				}
				if d.Allowed != row[plan] {
					t.Fatalf("%s on %s: allowed=%v, want %v (reason %q)", s.Feature, plan, d.Allowed, row[plan], d.Reason)
				}
				if d.Allowed {
					return
				}
				// Task 5.3: EVERY denial names its reason and the plan that lifts it.
				if d.Reason == "" || d.ReasonCode == "" {
					t.Errorf("denial names no reason: %+v", d)
				}
				if d.ReasonCode != ReasonNotEntitled {
					t.Errorf("reason code = %q, want %q", d.ReasonCode, ReasonNotEntitled)
				}
				if d.UpgradePlan == "" || d.UpgradePlanName == "" {
					t.Errorf("denial offers no upgrade path: %+v", d)
				}
				// The upgrade plan must ACTUALLY lift it — an invitation to a plan that also denies is
				// worse than none.
				up, err := s.Check(g, "cus_"+d.UpgradePlan)
				if err != nil {
					t.Fatalf("check upgrade plan: %v", err)
				}
				if !up.Allowed {
					t.Errorf("the named upgrade plan %q still denies %s: %q", d.UpgradePlan, s.Feature, up.Reason)
				}
			})
		}
	}
}

// TestUpgradePathIsTheCheapestPlanThatLifts: pointing a Free customer at Enterprise when Team would do
// is a paywall; naming the plan that actually lifts what they hit is an invitation.
func TestUpgradePathIsTheCheapestPlanThatLifts(t *testing.T) {
	g, _, _, _ := newGate(t)

	d, err := g.AssistedPR("cus_free")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if d.UpgradePlan != "team" {
		t.Errorf("upgrade plan = %q, want the cheapest that lifts it (team)", d.UpgradePlan)
	}
	if d.UpgradePlanName != "Team" {
		t.Errorf("upgrade plan NAME = %q, want Team — plans are named to customers", d.UpgradePlanName)
	}

	// Auto-merge is Enterprise-only, so Team's upgrade path skips Business.
	d, err = g.AutoMerge("cus_team")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if d.UpgradePlan != "enterprise" {
		t.Errorf("upgrade plan = %q, want enterprise", d.UpgradePlan)
	}
}

// TestBothAxesAreChecked: the plan and the automation level are independent gates, and the STRICTER
// one wins. An Enterprise customer asking for auto-merge under an Assisted contract is denied — no
// plan fixes that, and the denial says so instead of dangling a misleading upgrade.
func TestBothAxesAreChecked(t *testing.T) {
	g, _, _, _ := newGate(t)

	for _, level := range []AutomationLevel{LevelAdvisory, LevelAssisted} {
		d, err := g.CheckEntitlement("cus_enterprise", plancfg.FeatureAutoMerge, level)
		if err != nil {
			t.Fatalf("check: %v", err)
		}
		if d.Allowed {
			t.Errorf("auto-merge was allowed at the %s level on Enterprise — the level axis is not checked", level)
		}
		if d.ReasonCode != ReasonLevelTooLow {
			t.Errorf("reason code = %q, want %q", d.ReasonCode, ReasonLevelTooLow)
		}
		if d.UpgradePlan != "" {
			t.Errorf("a level mismatch offered an upgrade PLAN (%q) — no plan fixes it, and saying so is misleading", d.UpgradePlan)
		}
		if err := d.Validate(); err != nil {
			t.Errorf("incoherent decision: %v", err)
		}
	}

	// And the plan axis still denies at the right level.
	d, err := g.CheckEntitlement("cus_team", plancfg.FeatureAutoMerge, LevelAutonomous)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if d.Allowed || d.ReasonCode != ReasonNotEntitled {
		t.Errorf("Team at the autonomous level: %+v", d)
	}

	// Assisted PRs need at least the Assisted level, even on a plan that includes them.
	d, err = g.CheckEntitlement("cus_team", plancfg.FeatureAssistedPR, LevelAdvisory)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if d.Allowed || d.ReasonCode != ReasonLevelTooLow {
		t.Errorf("Assisted PR at the advisory level: %+v", d)
	}
}

// TestOverLimitIsDeniedWithANamedReasonAndUpgradePath is task 5.5 / FR7 — never silently allowed,
// never silently dropped.
func TestOverLimitIsDeniedWithANamedReasonAndUpgradePath(t *testing.T) {
	g, _, usage, _ := newGate(t)

	// Team's SUM band is 1000 in the fixture; put the customer past it.
	if _, _, err := usage.Upsert(metering.UsageRecord{
		CustomerID: "cus_team", Period: july.ID, Metric: metering.MetricSUM,
		Quantity: 1500, SourceDigest: "d", UpdatedAt: julyStart,
	}); err != nil {
		t.Fatalf("seed usage: %v", err)
	}

	d, err := g.AssistedPR("cus_team")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if d.Allowed {
		t.Fatal("an over-SUM-band action was SILENTLY ALLOWED — the revenue-leak failure mode")
	}
	if d.ReasonCode != ReasonOverLimit {
		t.Fatalf("reason code = %q, want %q", d.ReasonCode, ReasonOverLimit)
	}
	if d.Limit == nil {
		t.Fatal("the denial does not say WHICH limit was hit")
	}
	if d.Limit.Limit != plancfg.LimitSUMBand || d.Limit.Allowed != 1000 || d.Limit.Observed != 1500 {
		t.Errorf("the denial does not carry the magnitude: %+v", *d.Limit)
	}
	if d.Limit.Period != july.ID {
		t.Errorf("the denial does not name the period: %q", d.Limit.Period)
	}
	if d.UpgradePlan != "business" {
		t.Errorf("upgrade plan = %q, want business (the cheapest whose band covers 1500)", d.UpgradePlan)
	}
	if err := d.Validate(); err != nil {
		t.Errorf("incoherent decision: %v", err)
	}

	// The named upgrade actually lifts it.
	bizDecision, err := g.CheckEntitlement("cus_business", plancfg.FeatureAssistedPR, LevelAssisted)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !bizDecision.Allowed {
		t.Errorf("the named upgrade plan still denies: %q", bizDecision.Reason)
	}

	// Enterprise sets NO sum_band in the fixture: an unset limit is UNLIMITED, not zero. If it were
	// read as zero, every Enterprise action would be denied — the opposite failure, equally silent.
	if _, _, err := usage.Upsert(metering.UsageRecord{
		CustomerID: "cus_enterprise", Period: july.ID, Metric: metering.MetricSUM,
		Quantity: 9_999_999, SourceDigest: "d", UpdatedAt: julyStart,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ent, err := g.AutoMerge("cus_enterprise")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !ent.Allowed {
		t.Errorf("an UNSET limit was read as zero: %+v", ent)
	}
}

// fakeSeats is a seat count read from wherever seats actually live. In production that is membership;
// here it is a number, because what this test drives is the GATE, not the counting.
type fakeSeats struct {
	held map[string]int
	err  error
}

func (f fakeSeats) SeatsHeld(tenantID string) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.held[tenantID], nil
}

// TestCheckLimitDeniesBeforeTheAllowanceIsConsumed: a seat request that would be the 6th on a 5-seat
// plan must be denied BEFORE the seat is taken, not after the meter notices.
//
// 🔴 P27 changed how this test SUPPLIES the current count, and the change is the point.
//
// It used to seed a `seats` usage record. That passed, and it proved the wrong thing: no code path in
// this platform has ever written such a record, so the gate was reading a value that is zero on every
// real deployment — which is why a 5-seat plan admitted five hundred. The count now comes from a seat
// counter, which in production reads membership, and `TestTheSeatGateNeverReadsTheUsageStore` below
// asserts the usage store is not consulted for it at all.
func TestCheckLimitDeniesBeforeTheAllowanceIsConsumed(t *testing.T) {
	g, _, _, _ := newGate(t)
	g.WithSeatCounter(fakeSeats{held: map[string]int{"cus_team": 5}})

	// At exactly the allowance: still inside.
	ok, err := g.CheckLimit("cus_team", plancfg.LimitSeats, metering.MetricSeats, 0)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !ok.Allowed {
		t.Errorf("being exactly at the allowance was denied: %+v", ok)
	}

	// One more seat would exceed it.
	d, err := g.CheckLimit("cus_team", plancfg.LimitSeats, metering.MetricSeats, 1)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if d.Allowed {
		t.Fatal("the 6th seat on a 5-seat plan was allowed")
	}
	if d.ReasonCode != ReasonOverLimit || d.Limit == nil || d.Limit.Observed != 6 {
		t.Errorf("the denial does not describe what would happen: %+v", d)
	}
	if d.UpgradePlan != "business" {
		t.Errorf("upgrade plan = %q, want business", d.UpgradePlan)
	}
}

// TestGateFailsClosedOnAnUnresolvableCustomer: an unknown customer or an unpublished plan DENIES.
// Failing open on an operational fault would hand the top tier to anyone the account store cannot find.
func TestGateFailsClosedOnAnUnresolvableCustomer(t *testing.T) {
	g, accts, _, _ := newGate(t)

	for _, s := range Surfaces {
		d, err := s.Check(g, "cus_nobody")
		if err != nil {
			t.Fatalf("check: %v", err)
		}
		if d.Allowed {
			t.Errorf("%s was ALLOWED for a customer with no account", s.Feature)
		}
		if d.ReasonCode != ReasonNoAccount {
			t.Errorf("%s: reason code = %q, want %q", s.Feature, d.ReasonCode, ReasonNoAccount)
		}
		if err := d.Validate(); err != nil {
			t.Errorf("incoherent decision: %v", err)
		}
	}

	// An account pointing at a plan the config store does not publish also denies.
	if _, err := accts.Create(account.Account{CustomerID: "cus_ghostplan",
		ProviderCustomerHandle: "prov_cus_ghost", ActivePlanID: "platinum",
		PlanConfigVersion: "cfg-test-1", CreatedAt: julyStart}); err != nil {
		t.Fatalf("create: %v", err)
	}
	d, err := g.CLI("cus_ghostplan")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if d.Allowed || d.ReasonCode != ReasonUnknownPlan {
		t.Errorf("an unpublished plan did not deny: %+v", d)
	}
}

// TestUnknownFeatureOrLevelDenies: a typo must deny, never escalate. The direction a mistake fails in
// matters most at the top of the range, where the actor can merge code.
func TestUnknownFeatureOrLevelDenies(t *testing.T) {
	g, _, _, _ := newGate(t)

	d, err := g.CheckEntitlement("cus_enterprise", plancfg.Feature("teleport"), LevelAutonomous)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if d.Allowed || d.ReasonCode != ReasonUnknownFeature {
		t.Errorf("an unknown feature did not deny: %+v", d)
	}

	d, err = g.CheckEntitlement("cus_enterprise", plancfg.FeatureAutoMerge, AutomationLevel("superuser"))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if d.Allowed || d.ReasonCode != ReasonUnknownLevel {
		t.Errorf("an unknown automation level did not deny: %+v", d)
	}
}

// TestPlanChangeInConfigTakesEffectAtTheGate is task 9.3 / FR6 seen end-to-end: repointing a plan's
// entitlement in the CONFIG STORE changes what the gate allows — same process, no code change, no
// deploy, no migration.
func TestPlanChangeInConfigTakesEffectAtTheGate(t *testing.T) {
	g, _, usage, plans := newGate(t)

	// Before: Team cannot auto-merge, and 1500 SUM is over its band.
	if d, _ := g.AutoMerge("cus_team"); d.Allowed {
		t.Fatal("precondition: Team must not auto-merge")
	}
	if _, _, err := usage.Upsert(metering.UsageRecord{CustomerID: "cus_team", Period: july.ID,
		Metric: metering.MetricSUM, Quantity: 1500, SourceDigest: "d", UpdatedAt: julyStart}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if d, _ := g.AssistedPR("cus_team"); d.Allowed {
		t.Fatal("precondition: Team must be over its SUM band at 1500")
	}

	// The ONLY thing that happens: a config publish.
	src := plancfg.NewMemSource()
	if err := src.PublishJSON([]byte(`{
	  "version": "cfg-test-2",
	  "plans": [
	    {"plan_id":"free","display_name":"Free","rank":0,"features":["cli","discovery"],
	     "limits":{"sum_band":100,"seats":1},"price_refs":{}},
	    {"plan_id":"team","display_name":"Team","rank":1,
	     "features":["cli","discovery","assisted_pr","dashboard","auto_merge"],
	     "limits":{"sum_band":5000,"seats":5},
	     "price_refs":{"subscription":"price_ref_team_sub","metered":"price_ref_team_metered"}},
	    {"plan_id":"business","display_name":"Business","rank":2,
	     "features":["cli","discovery","assisted_pr","dashboard"],
	     "limits":{"sum_band":10000,"seats":25},"price_refs":{"subscription":"price_ref_biz_sub"}},
	    {"plan_id":"enterprise","display_name":"Enterprise","rank":3,
	     "features":["cli","discovery","assisted_pr","dashboard","auto_merge"],
	     "limits":{"seats":500},"price_refs":{"subscription":"price_ref_ent_sub"}}
	  ]
	}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Repoint the SAME resolver at the new catalog — this is the hot reload, in-process.
	*plans = *plancfg.NewResolver(src, plancfg.NewMemAudit())
	plans.SetClock(func() time.Time { return julyStart })
	if _, err := plans.Reload("finance"); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if d, _ := g.AutoMerge("cus_team"); !d.Allowed {
		t.Errorf("after the config publish, Team still cannot auto-merge: %+v", d)
	}
	if d, _ := g.AssistedPR("cus_team"); !d.Allowed {
		t.Errorf("after the config publish, Team is still over its band at 1500: %+v", d)
	}
}

// TestMergeGateDeniesWhenItCannotAnswer: the P6 adapter must never fail open. Auto-merge is the
// highest-authority actor in the system.
func TestMergeGateDeniesWhenItCannotAnswer(t *testing.T) {
	g, _, _, _ := newGate(t)
	mg := NewMergeGate(g)

	allowed, reason, upgrade := mg.AllowAutoMerge("cus_enterprise")
	if !allowed {
		t.Fatalf("Enterprise was denied auto-merge: %s", reason)
	}
	if reason != "" || upgrade != "" {
		t.Errorf("an allow carries denial fields: %q / %q", reason, upgrade)
	}

	allowed, reason, upgrade = mg.AllowAutoMerge("cus_team")
	if allowed {
		t.Fatal("Team was allowed auto-merge")
	}
	if reason == "" {
		t.Error("the denial names no reason — the loop would audit a silent fallback")
	}
	if upgrade != "enterprise" {
		t.Errorf("upgrade = %q, want enterprise", upgrade)
	}

	// No account at all: denies, with a reason.
	allowed, reason, _ = mg.AllowAutoMerge("")
	if allowed {
		t.Fatal("a customer-less run was allowed to auto-merge")
	}
	if reason == "" {
		t.Error("the denial names no reason")
	}
}

// TestTheFloorHoldsForAnOverBandCustomer fences the scope decision in meteredLimits: the SUM band
// gates the surfaces that CONSUME spend, and the CLI + discovery are the plan's floor.
//
// The failure this prevents is specific and was found by the M10 end-to-end walk: with the band applied
// to every surface, a Free customer who overspends loses the CLI — the one tool that would show them
// they are overspending. A paywall that removes the diagnostic is not an invitation, and a Free tier
// that stops working the moment it is used is a demo.
func TestTheFloorHoldsForAnOverBandCustomer(t *testing.T) {
	g, _, usage, _ := newGate(t)

	// Free's band is 100 in the fixture; put the customer far past it.
	if _, _, err := usage.Upsert(metering.UsageRecord{
		CustomerID: "cus_free", Period: july.ID, Metric: metering.MetricSUM,
		Quantity: 100_000, SourceDigest: "d", UpdatedAt: julyStart,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, check := range []struct {
		name string
		fn   func(string) (Decision, error)
	}{{"cli", g.CLI}, {"discovery", g.Discovery}} {
		d, err := check.fn("cus_free")
		if err != nil {
			t.Fatalf("%s: %v", check.name, err)
		}
		if !d.Allowed {
			t.Errorf("%s was denied to an over-band Free customer (%s) — the floor must hold on every plan",
				check.name, d.Reason)
		}
	}

	// And the same customer on Team — where the spend-consuming surfaces ARE band-gated — is denied
	// there, so the exemption is scoped rather than a hole.
	if _, _, err := usage.Upsert(metering.UsageRecord{
		CustomerID: "cus_team", Period: july.ID, Metric: metering.MetricSUM,
		Quantity: 100_000, SourceDigest: "d", UpdatedAt: julyStart,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if d, _ := g.CLI("cus_team"); !d.Allowed {
		t.Error("the floor does not hold on Team either")
	}
	for _, check := range []struct {
		name string
		fn   func(string) (Decision, error)
	}{{"assisted PR", g.AssistedPR}, {"dashboard", g.Dashboard}} {
		d, err := check.fn("cus_team")
		if err != nil {
			t.Fatalf("%s: %v", check.name, err)
		}
		if d.Allowed {
			t.Errorf("%s was allowed to a wildly over-band customer — the band gates spend-consuming surfaces", check.name)
		}
		if d.ReasonCode != ReasonOverLimit {
			t.Errorf("%s denied for %q, want over_limit", check.name, d.ReasonCode)
		}
	}
}

// TestTheSeatGateNeverReadsTheUsageStore is 6.1, asserted rather than intended.
//
// A `seats` usage record is planted with a wildly wrong value. If the gate consults the usage store for
// seats — the behaviour that made `LimitSeats` decorative for the whole of P7 — this test sees the
// planted number instead of the real one and fails.
func TestTheSeatGateNeverReadsTheUsageStore(t *testing.T) {
	g, _, usage, _ := newGate(t)
	// A number nobody could reach, so a wrong read is unmistakable.
	if _, _, err := usage.Upsert(metering.UsageRecord{
		CustomerID: "cus_team", Period: july.ID, Metric: metering.MetricSeats,
		Quantity: 999, SourceDigest: "planted", UpdatedAt: julyStart,
	}); err != nil {
		t.Fatalf("plant: %v", err)
	}
	g.WithSeatCounter(fakeSeats{held: map[string]int{"cus_team": 1}})

	d, err := g.CheckLimit("cus_team", plancfg.LimitSeats, metering.MetricSeats, 1)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !d.Allowed {
		t.Fatalf("a 2nd seat on a 5-seat plan with ONE member held was denied — the gate read the "+
			"planted usage record (%v) instead of the seat counter: %+v", 999, d)
	}
}

// TestAnUnmeasurableSeatLimitIsSkippedRatherThanTreatedAsZero.
//
// 🔴 This is the honest half of the P7 correction. With no counter wired, the allowance cannot be
// evaluated — and comparing it against zero is precisely what made the check pass while enforcing
// nothing. A skipped limit is stated; a zero-compared one looks enforced.
func TestAnUnmeasurableSeatLimitIsSkippedRatherThanTreatedAsZero(t *testing.T) {
	g, _, _, _ := newGate(t)
	if g.SeatsEnforced() {
		t.Fatal("a gate with no seat counter reported that it enforces seats")
	}

	d, err := g.CheckLimit("cus_team", plancfg.LimitSeats, metering.MetricSeats, 500)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !d.Allowed {
		t.Fatal("an unmeasurable limit denied; skipping is the choice, because a metering gap must not " +
			"lock a customer out")
	}
	if d.Reason == "" {
		t.Error("the gate allowed without saying it could not measure — that is indistinguishable from " +
			"having measured and found room, which is the P7 failure exactly")
	}

	// A counter that ERRORS is also unmeasurable, not zero.
	g.WithSeatCounter(fakeSeats{err: errSeatStoreDown})
	d, err = g.CheckLimit("cus_team", plancfg.LimitSeats, metering.MetricSeats, 500)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !d.Allowed || d.Reason == "" {
		t.Errorf("a failing seat read was not treated as unmeasurable: %+v", d)
	}
}

var errSeatStoreDown = errors.New("the membership store is unreachable")
