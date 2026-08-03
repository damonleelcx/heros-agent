//go:build pgproof

// Live-Postgres proof that billing actually MOUNTS.
//
// The unit test beside this one pins the reason billing gives when it is unserved. That is the easy
// half: an absent capability is absent whether or not the code that would serve it works. This is the
// other half — with a real database and a published catalog, the whole stack constructs and the
// capability reports served.
//
// It matters because the mount wires six collaborators (PGLedger, account.PGStore,
// metering.PGUsageStore, a plan resolver, a meter, an entitlement gate) and any one of them refusing
// its constructor turns a boot into an error return. A green unit suite proves none of that.
//
//	make pg-proof
//	HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/launch/
package launch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
)

// writeCatalog publishes a minimal plan catalog. A FILE, never git-tracked — it carries prices.
func writeCatalog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plans.json")
	body := map[string]any{
		"version":      "v1",
		"published_at": time.Now().UTC().Format(time.RFC3339),
		"plans": map[string]any{
			"plan_team": map[string]any{
				"plan_id": "plan_team", "display_name": "Team",
			},
		},
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

func TestBillingMountsWithADatabaseAndACatalog(t *testing.T) {
	db, err := pgtest.Open("launch_billing_mount")
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	t.Setenv("PLAN_CATALOG_PATH", writeCatalog(t))

	caps, err := mountCapabilities(api.New(nil, config.Config{}), db, t.TempDir(), "")
	if err != nil {
		t.Fatalf("mountCapabilities with a database and a catalog: %v\n\n"+
			"The billing stack wires six collaborators; any one refusing its constructor turns a "+
			"deployment's boot into this error.", err)
	}

	// Prefix match, deliberately: this list's convention is that a SERVED name carries a parenthetical
	// describing what it serves ("p7_billing (durable ledger, …)") while an ABSENT one is the bare id.
	// An exact-match assertion looks right and silently never fires on the served path — which is how
	// this test first "passed" the failing case.
	for _, c := range caps {
		if !strings.HasPrefix(c.Name, "p7_billing") {
			continue
		}
		if !c.Served {
			t.Fatalf("p7_billing is still unserved with a database and a catalog present: %s", c.Why)
		}
		t.Logf("served: %s", c.Name)
		return
	}
	t.Fatal("p7_billing is not in the capability list at all")
}

// TestBillingStaysUnservedWithoutACatalog: the gate is real, not decorative.
func TestBillingStaysUnservedWithoutACatalog(t *testing.T) {
	db, err := pgtest.Open("launch_billing_nocatalog")
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	t.Setenv("PLAN_CATALOG_PATH", "")

	caps, err := mountCapabilities(api.New(nil, config.Config{}), db, t.TempDir(), "")
	if err != nil {
		t.Fatalf("mountCapabilities: %v", err)
	}
	for _, c := range caps {
		if strings.HasPrefix(c.Name, "p7_billing") && c.Served {
			t.Fatal("billing served with NO plan catalog — it cannot name the plan a customer is on, " +
				"so it cannot price anything")
		}
	}
}
