package launch

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
)

// billingmount_test.go pins the CONDITION under which billing serves, and the reason it gives when it
// does not.
//
// P7 was unmounted for phases with the reason "its only store implementation is in-memory, so mounting
// it would record and then forget". That reason is now gone — the ledger, the accounts and the meters
// are all durable. What replaced it is a narrower and different gate: a plan catalog. The risk this
// file guards against is that the two get confused, and an operator reads a stale sentence about
// in-memory stores while the actual missing thing is one file away.

func TestBillingAbsentReasonNamesTheONENextAction(t *testing.T) {
	// No database: a deployment-wide fact. The catalog is irrelevant and must not be mentioned.
	noDB := billingAbsentReason(nil, "/etc/heros/plans.json", nil)
	if !strings.Contains(noDB, "DATABASE_URL") {
		t.Errorf("with no database the reason must name DATABASE_URL, got %q", noDB)
	}
	if strings.Contains(noDB, "PLAN_CATALOG_PATH") {
		t.Errorf("with no database the reason should not send the operator after a catalog: %q", noDB)
	}

	// A database but no catalog: one file away, and the reason must say which variable.
	noCatalog := billingAbsentReason(&sql.DB{}, "", nil)
	if !strings.Contains(noCatalog, "PLAN_CATALOG_PATH") {
		t.Errorf("with no catalog the reason must name PLAN_CATALOG_PATH, got %q", noCatalog)
	}
	if strings.Contains(noCatalog, "DATABASE_URL") {
		t.Errorf("with a database present the reason must not blame the database: %q", noCatalog)
	}

	// 🔴 The stale sentence. Billing's stores are durable now; a reason still claiming they are
	// in-memory would send an operator to wait for a phase that already shipped.
	for _, r := range []string{noDB, noCatalog, billingAbsentReason(&sql.DB{}, "/x", nil)} {
		if strings.Contains(strings.ToLower(r), "in-memory") {
			t.Errorf("the absent reason still claims billing's stores are in-memory — they are durable "+
				"as of the PGLedger/PGStore/PGUsageStore work: %q", r)
		}
	}
}

// TestPaymentsIsAbsentForTheRIGHTReason. P21's blocker changed too: it was the in-memory ledger, and it
// is now the absence of a payment provider. Serving checkout over the stub would offer a customer a
// button that mints nothing.
func TestPaymentsAbsentReasonNamesTheProviderNotTheLedger(t *testing.T) {
	caps := capabilityReasons(t)
	reason, ok := caps["p21_payments"]
	if !ok {
		t.Fatal("p21_payments is not registered at all — an unregistered route answers 404, which reads " +
			"as a broken URL rather than as a capability this deployment does not carry")
	}
	if !strings.Contains(reason, "provider") {
		t.Errorf("p21's reason must name the missing PROVIDER, got %q", reason)
	}
	if strings.Contains(strings.ToLower(reason), "in-memory") {
		t.Errorf("p21's reason still blames an in-memory store; the ledger is durable: %q", reason)
	}
}

// TestBillingIsRegisteredEvenWhenUnserved: an unregistered route answers 404, and 404 already means
// "no such thing". A capability this deployment does not carry must answer 503 and say so.
func TestBillingIsRegisteredEvenWhenUnserved(t *testing.T) {
	if _, ok := capabilityReasons(t)["p7_billing"]; !ok {
		t.Fatal("p7_billing is not in the capability list on a deployment with no database")
	}
}

// capabilityReasons mounts against NO database — the state a bare deployment boots in — and returns the
// reason recorded for each unserved capability.
func capabilityReasons(t *testing.T) map[string]string {
	t.Helper()
	caps, _, err := mountCapabilities(api.New(nil, config.Config{}), nil, t.TempDir(), "", nil, nil)
	if err != nil {
		t.Fatalf("mountCapabilities: %v", err)
	}
	out := map[string]string{}
	for _, c := range caps {
		if !c.Served {
			out[c.Name] = c.Why
		}
	}
	return out
}
