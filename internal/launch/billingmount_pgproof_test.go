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
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// writeCatalog publishes a minimal plan catalog. A FILE, never git-tracked — it carries prices.
func writeCatalog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plans.json")
	// 🔴 `plans` is an ARRAY. It was a map here, and the catalog therefore never parsed — which nothing
	// noticed, because until launch called Reload the fixture only had to EXIST. The stat passed, the
	// capability mounted, and the file's contents were never read by anything. Getting the shape wrong
	// in the fixture and getting it wrong in the deployment were the same bug wearing two hats.
	body := map[string]any{
		"version":      "v1",
		"published_at": time.Now().UTC().Format(time.RFC3339),
		"plans": []map[string]any{
			{
				"plan_id": "plan_team", "display_name": "Team", "rank": 1,
				"features":   []string{"cli", "discovery", "dashboard"},
				"limits":     map[string]float64{"seats": 5},
				"price_refs": map[string]string{"subscription": "price_ref_fixture_team"},
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

	caps, err := mountCapabilities(api.New(nil, config.Config{}), db, t.TempDir(), "", nil)
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

	caps, err := mountCapabilities(api.New(nil, config.Config{}), db, t.TempDir(), "", nil)
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

// TestCollectionMountsWhenAModeIsDeclared is the end-to-end half of the P21 wiring: with a database, a
// plan catalog and a declared mode, checkout actually mounts.
//
// It runs against real Postgres because that is the only place the billing block executes — the
// provider, the read model and the mount all sit inside `if pg != nil`, so a unit test over a nil DB
// proves nothing about them. `collectionProvider` is unit-tested separately; this asserts the wiring
// downstream of it, which is where P21 was broken for a different reason entirely: there was no
// api.PaymentsSource outside a demo binary, so even a correct provider had nothing to mount.
//
// 🔴 Mode `test`, and no credential is set. That is deliberate and it is not a weaker test: the
// provider resolves its key at the MOMENT OF USE, so construction and mounting are exactly what this
// asserts, and a checkout attempt would fail closed with "credential unavailable". A fence that needed
// a live key to prove the wiring would be a fence nobody could run.
func TestCollectionMountsWhenAModeIsDeclared(t *testing.T) {
	db, err := pgtest.Open("launch_collection_mount")
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	t.Setenv("PLAN_CATALOG_PATH", writeCatalog(t))
	t.Setenv(BillingModeEnv, "test")

	caps, err := mountCapabilities(api.New(nil, config.Config{}), db, t.TempDir(), "", providergateway.EnvSecrets{})
	if err != nil {
		t.Fatalf("mountCapabilities with a declared billing mode: %v", err)
	}
	for _, c := range caps {
		if strings.HasPrefix(c.Name, "p21_payments") {
			if !c.Served {
				t.Fatalf("p21 did not mount with %s=test: %s", BillingModeEnv, c.Why)
			}
			if !strings.Contains(c.Name, "mode test") {
				t.Errorf("the capability line does not name the mode it mounted in: %q.\n"+
					"An operator reading the boot log has to be able to tell a deployment that charges "+
					"real money from one that does not, and the mode is the only thing that says so.", c.Name)
			}
			return
		}
	}
	t.Fatal("p21_payments is not in the capability list at all")
}

// TestThePublishedCatalogIsActuallyLoaded is the fence for the defect that made every plan name
// unresolvable on a real deployment.
//
// A plancfg.Resolver does not read its source on construction: `loaded` stays false and ResolvePlan
// returns ErrNoConfig until Reload is called. Nothing in the deployed path called it — Reload appeared
// only in four cmd/ binaries — so the catalog was PUBLISHED (the boot stat found the file, and that is
// what mounted billing, delivery and the entitlement gate) and never READ.
//
// 🔴 Nothing failed. No error was logged, no capability reported absent, and /readyz stayed green. The
// symptom was that `Plans()` returned empty, so checkout answered "no plan with that name in the
// published configuration" for `Team` — a plan the file plainly contains — and every entitlement
// decision resolved against nothing. Exactly the "mounted surface that fails on its first read"
// outcome the boot-time stat exists to prevent, displaced one step: the file's EXISTENCE was proven
// and its CONTENTS never were.
//
// The assertion is deliberately about the CONTENTS, not about mounting. Mounting was never the broken
// part.
func TestThePublishedCatalogIsActuallyLoaded(t *testing.T) {
	db, err := pgtest.Open("launch_catalog_loaded")
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	catalog := writeCatalog(t)
	t.Setenv("PLAN_CATALOG_PATH", catalog)
	if _, err := mountCapabilities(api.New(nil, config.Config{}), db, t.TempDir(), "", nil); err != nil {
		t.Fatalf("mountCapabilities: %v", err)
	}

	// The same resolver the deployment builds, loaded the same way. If launch stops calling Reload this
	// returns nothing, which is the whole defect.
	plans := plancfg.NewResolver(plancfg.NewFileSource(catalog), nil)
	if _, err := plans.Reload("fence"); err != nil {
		t.Fatalf("the fixture catalog does not load: %v", err)
	}
	if got := plans.Plans(); len(got) == 0 {
		t.Fatal("the published catalog resolves to zero plans")
	}

	// And the boot path: a resolver nobody reloads answers ErrNoConfig for everything, which is what
	// every plan lookup on the deployment was getting.
	unloaded := plancfg.NewResolver(plancfg.NewFileSource(catalog), nil)
	if len(unloaded.Plans()) != 0 {
		t.Fatal("a Resolver now self-loads; if that is intended, this fence and launch's Reload should " +
			"both be revisited rather than left as belt-and-braces nobody understands")
	}
}
