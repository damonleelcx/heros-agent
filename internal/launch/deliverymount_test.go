package launch

import (
	"database/sql"
	"strings"
	"testing"
)

// deliverymount_test.go guards the same failure billingmount_test.go does, for the two capabilities
// that just changed reason.
//
// P5.5 and P12 were unmounted for phases with "no persistent adapter exists outside a demo binary" and
// "its gate and pending providers read verification state that has no store yet". Both sentences are now
// false: migrations 0025/0029/0030 exist, the verdict ingest records real measurements, and
// internal/deliveryroute is the route registry P12 had been missing since it landed. The risk this file
// guards against is an operator reading a stale sentence about a missing adapter while the actual
// missing thing is one environment variable.

func TestDeliveryAbsentReasonNamesTheONENextAction(t *testing.T) {
	noDB := deliveryAbsentReason(nil, "/etc/heros/plans.json", nil)
	if !strings.Contains(noDB, "DATABASE_URL") {
		t.Errorf("with no database the reason must name DATABASE_URL, got %q", noDB)
	}
	if strings.Contains(noDB, "PLAN_CATALOG_PATH") {
		t.Errorf("with no database the reason must not send the operator after a catalog: %q", noDB)
	}

	// 🔴 A database but no catalog. Delivery's gate is ENTITLEMENT, and without a plan catalog this
	// deployment cannot decide whether a tenant may have a pull request opened for them. The reason must
	// name the variable AND say why the question has no safe default — an operator who reads only
	// "not entitled" will go looking at the customer's plan.
	noCatalog := deliveryAbsentReason(&sql.DB{}, "", nil)
	if !strings.Contains(noCatalog, "PLAN_CATALOG_PATH") {
		t.Errorf("with no catalog the reason must name PLAN_CATALOG_PATH, got %q", noCatalog)
	}
	if !strings.Contains(strings.ToLower(noCatalog), "entitle") {
		t.Errorf("the reason must say WHAT the catalog is needed for here (entitlement), got %q", noCatalog)
	}
	if strings.Contains(noCatalog, "DATABASE_URL") {
		t.Errorf("with a database present the reason must not blame the database: %q", noCatalog)
	}

	// The stale sentences. Both would send an operator to wait for work that has shipped.
	for _, r := range []string{noDB, noCatalog, deliveryAbsentReason(&sql.DB{}, "/x", nil)} {
		low := strings.ToLower(r)
		if strings.Contains(low, "demo binary") {
			t.Errorf("the absent reason still claims no adapter exists outside a demo binary — "+
				"internal/hostedproposals is that adapter: %q", r)
		}
		if strings.Contains(low, "no store yet") {
			t.Errorf("the absent reason still claims delivery's verification state has no store — "+
				"proposalstore and deliveryroute are that store: %q", r)
		}
	}
}

// The P5.5 absent reason must also stop blaming a missing adapter.
func TestProposalsAbsentReasonNamesTheDatabaseNotAMissingAdapter(t *testing.T) {
	caps := capabilityReasons(t)
	reason, ok := caps["p55_proposals"]
	if !ok {
		// Served on this fixture — which is a legitimate outcome and means there is no reason to check.
		return
	}
	if strings.Contains(strings.ToLower(reason), "demo binary") {
		t.Errorf("p5.5 is unmounted for a reason that no longer holds (%q). internal/hostedproposals "+
			"renders the surface from the durable store; what a deployment can lack now is the store.", reason)
	}
	if !strings.Contains(reason, "DATABASE_URL") {
		t.Errorf("the reason must name what is missing, got %q", reason)
	}
}

// The live monitor is the one read surface this deployment genuinely cannot serve, and its reason has
// to say WHY rather than inheriting the generic "no adapter" sentence — which was stale for P5.5 and
// P12 and is wrong here for a different reason: an adapter would not help.
func TestTheRunMonitorReasonNamesTheRealBlockers(t *testing.T) {
	caps := capabilityReasons(t)
	reason, ok := caps["p25_run_monitor"]
	if !ok {
		t.Fatal("the run monitor is served on this fixture; if that is now true, this test's premise is " +
			"stale and the capability needs a different guard")
	}
	if strings.Contains(strings.ToLower(reason), "demo binary") {
		t.Errorf("the run monitor is unmounted for a reason that names a missing ADAPTER (%q). An adapter "+
			"would not help: the platform hears of a run only after it finished, and per-node state is "+
			"eval data that never crosses the boundary.", reason)
	}
	// Both blockers must be named. Either one alone reads as a gap somebody could close by wiring.
	for _, want := range []string{"link", "eval data"} {
		if !strings.Contains(strings.ToLower(reason), want) {
			t.Errorf("the reason must name %q, got %q", want, reason)
		}
	}
	// And it must point at what IS served, so the sentence is not purely a refusal.
	if !strings.Contains(reason, "p45_scorecard") {
		t.Errorf("the reason should name the surfaces that DO render a linked run, got %q", reason)
	}
}
