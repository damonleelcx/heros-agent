//go:build pgproof

// The UPGRADE axis (task 11.4), which is a different axis from fresh install and shares almost nothing
// with it.
//
// # Why a clean-database test proves nothing about this
//
// Every other proof in this repository starts by applying the whole migration set to an empty schema.
// That is the FRESH INSTALL path, and it is the one nobody's customer is on. The path that carries risk
// runs against a database that already holds rows written by the previous release — and the failures it
// has are failures a clean run cannot express: a column added NOT NULL onto existing rows, a constraint
// that no current row satisfies, a seed that overwrites what a customer changed, a credential that stops
// resolving because the code now reads a table the old rows are not in.
//
// So this test builds the BEFORE state first: migrations through 37, then a customer's world written
// into it — a billable account, a run with its whole lineage, and tenants that existed only as
// credentials in a configuration file, because before P27 that is the only place a tenant existed.
//
// Then it upgrades. Then it asserts the three things an upgrade is allowed to be judged on:
//
//	NOTHING LOST      the account and the run are still there, unchanged, and still readable
//	EVERY KEY WORKS   every configured credential still authenticates, through the DURABLE path it now
//	                  resolves against rather than the map it used to
//	THE GAP IS NAMED  the pre-existing run has no owner, is excluded from every listing, and is COUNTED
//	                  — a silently shorter list is the failure mode D6 exists to prevent
package launch

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/heros-foreal/agentd/internal/tenancy"
	_ "github.com/lib/pq"
)

var upgradeAt = time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)

const (
	upgradeHash = "aaaa111122223333444455556666777788889999aaaabbbbccccddddeeee0000"
	// The two keys a customer is already using. If either stops working, the upgrade locked somebody out.
	acmeKey   = "sk-acme-existing-key-do-not-break"
	globexKey = "sk-globex-existing-key-do-not-break"
)

// applyThroughUpgrade executes migrations up to and including maxID — the schema the PREVIOUS release
// shipped. Applying everything and then inserting would produce rows that postdate the change, which is
// the fresh-install path wearing an upgrade's name.
func applyThroughUpgrade(t *testing.T, db *sql.DB, maxID int64) {
	t.Helper()
	all, err := pgmigrate.Load()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	for _, m := range all {
		if m.ID > maxID {
			return
		}
		if _, err := db.Exec(m.SQL); err != nil {
			t.Fatalf("applying %s: %v", m.Name, err)
		}
	}
}

// seedPreUpgradeWorld writes what a deployed platform holds the moment before the upgrade.
func seedPreUpgradeWorld(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, stmt := range []struct {
		what string
		sql  string
		args []any
	}{
		{"workflow", `INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		  VALUES ('wf_before','https://example.invalid/r','abc123','go','1.0.0')`, nil},
		{"variant", `INSERT INTO variant (variant_id, workflow_id, label)
		  VALUES ('v_before','wf_before','before the upgrade')`, nil},
		{"config", `INSERT INTO config (config_hash, variant_id, workflow_id, ir_version, lineage_json)
		  VALUES ($1,'v_before','wf_before','1.0.0','{}')`, []any{upgradeHash}},
		{"variant_spec", `INSERT INTO variant_spec (config_hash, source_revision, spec_json)
		  VALUES ($1,'rev-before','{}')`, []any{upgradeHash}},
		{"transform", `INSERT INTO transform (config_hash, source_revision, build_status, verification_strength)
		  VALUES ($1,'rev-before','built','type-checked')`, []any{upgradeHash}},
		// A run that finished before anybody thought to record who owned it.
		{"run", `INSERT INTO run (run_id, config_hash, source_revision, seed, status, finished_at)
		  VALUES ('run_before',$1,'rev-before',7,'succeeded', now())`, []any{upgradeHash}},
		// A billable customer, with the provider handle the old NOT NULL required.
		{"account", `INSERT INTO account (customer_id, provider_customer_handle, active_plan_id, plan_config_version)
		  VALUES ('acme','cus_EXISTINGCUSTOMER','team','cfg-old')`, nil},
	} {
		if _, err := db.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed %s: %v", stmt.what, err)
		}
	}
}

// upgradeConfig is the deployment's configuration file — the ONLY place a tenant existed before P27.
func upgradeConfig() config.Config {
	return config.Config{
		TenantCredentials: []config.TenantCredential{
			{TenantID: "acme", APIKey: acmeKey, Role: "owner", KeyID: "key_acme"},
			{TenantID: "globex", APIKey: globexKey, Role: "member", KeyID: "key_globex"},
		},
	}
}

func TestPGUpgrade_ADeployedDatabaseKeepsEverythingAndEveryKeyStillWorks(t *testing.T) {
	db, err := pgtest.Open("launch_upgrade_axis")
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	// ── BEFORE ───────────────────────────────────────────────────────────────────────────────────────
	applyThroughUpgrade(t, db, 37)
	seedPreUpgradeWorld(t, db)

	// The pre-P27 authentication path: a map built from the configuration file at boot, with no store
	// behind it. This is what every existing customer's key resolves against today.
	cfg := upgradeConfig()
	before := auth.NewRegistry(cfg)
	for _, k := range []string{acmeKey, globexKey} {
		if _, ok := before.Lookup(k); !ok {
			t.Fatalf("the BEFORE state is wrong: %q does not authenticate against the configuration "+
				"path, so this test would prove nothing about the upgrade", k)
		}
	}

	// ── THE UPGRADE ──────────────────────────────────────────────────────────────────────────────────
	res, err := pgmigrate.Apply(ctx, db)
	if err != nil {
		t.Fatalf("the upgrade failed against a database that already holds rows: %v\n\n"+
			"This is the failure a clean-install proof cannot have: every ADD COLUMN, CHECK and index in "+
			"the new migrations has to be satisfiable by data that already exists.", err)
	}
	if len(res.Applied) == 0 {
		t.Fatal("the upgrade applied nothing; the BEFORE state was already current")
	}
	t.Logf("upgrade applied %d migration(s) over a populated database", len(res.Applied))

	// The boot path, in full — pick the store, seed it from configuration, point the registry at it.
	sys, err := buildAccountSystem(cfg, db, upgradeAt)
	if err != nil {
		t.Fatalf("the account system refused to compose after the upgrade: %v", err)
	}

	// ── NOTHING LOST ─────────────────────────────────────────────────────────────────────────────────
	accounts, err := account.NewPGStore(db)
	if err != nil {
		t.Fatalf("account store: %v", err)
	}
	acct, err := accounts.Get("acme")
	if err != nil {
		t.Fatalf("the customer's account is unreadable after the upgrade: %v", err)
	}
	if acct.ProviderCustomerHandle != "cus_EXISTINGCUSTOMER" || acct.ActivePlanID != "team" {
		t.Errorf("the account changed across the upgrade: %+v", acct)
	}
	// 🔴 `plan_charges` defaults TRUE, which is what keeps every existing row valid under the new CHECK.
	// A default of FALSE would have quietly declared every paying customer free.
	if !acct.PlanCharges {
		t.Error("an existing billable account came out of the upgrade marked as charging nothing — the " +
			"new column's default is what protects this, and it is the difference between an invoice and " +
			"a free tier")
	}
	// And List(), which is what the operator console and the billing webhook call.
	if all, lerr := accounts.List(); lerr != nil {
		t.Errorf("listing accounts after the upgrade failed: %v", lerr)
	} else if len(all) != 1 {
		t.Errorf("the account list holds %d rows after the upgrade, want the 1 that was there", len(all))
	}

	runs := executor.NewStore(db)
	rec, err := runs.Get(ctx, "run_before")
	if err != nil {
		t.Fatalf("the run that existed before the upgrade is gone: %v", err)
	}
	if rec.ConfigHash != upgradeHash || rec.Status != "succeeded" {
		t.Errorf("the pre-existing run changed: %+v", rec)
	}

	// ── EVERY KEY STILL WORKS ────────────────────────────────────────────────────────────────────────
	//
	// Through the DURABLE registry now, not the map. This is the assertion the whole seed exists for: a
	// registry pointed at an unseeded store refuses every configured credential, which on a slow database
	// is an upgrade that locks out every existing customer for as long as the seed takes.
	after := auth.NewRegistry(cfg).WithSource(sys.Store)
	for _, k := range []struct{ key, tenant, role string }{
		{acmeKey, "acme", "owner"},
		{globexKey, "globex", "member"},
	} {
		p, ok := after.Lookup(k.key)
		if !ok {
			t.Errorf("%s's configured credential stopped working after the upgrade. Every existing "+
				"customer authenticates with one of these.", k.tenant)
			continue
		}
		if p.TenantID != k.tenant {
			t.Errorf("%s's key now resolves to organization %q", k.tenant, p.TenantID)
		}
		if p.Role != k.role {
			t.Errorf("%s's key now carries role %q, was %q", k.tenant, p.Role, k.role)
		}
	}

	// The tenants are rows now, and the seed created them without being asked twice.
	for _, id := range []string{"acme", "globex"} {
		if _, terr := sys.Store.GetTenant(id); terr != nil {
			t.Errorf("the configured organization %q did not become a row: %v", id, terr)
		}
	}

	// ── THE GAP IS NAMED ─────────────────────────────────────────────────────────────────────────────
	if rec.TenantID != "" {
		t.Errorf("the pre-existing run acquired the owner %q. Nothing wrote that, which means something "+
			"guessed — and a guessed owner on billed usage is unfalsifiable afterwards.", rec.TenantID)
	}
	listed, err := runs.ListForTenant(ctx, "acme", 50, time.Time{})
	if err != nil {
		t.Fatalf("list acme's runs: %v", err)
	}
	for _, r := range listed {
		if r.RunID == "run_before" {
			t.Error("the pre-ownership run appears in an organization's listing — it belongs to nobody, " +
				"and putting it in somebody's list is the guess D6 forbids")
		}
	}
	n, err := runs.PreOwnedCount(ctx)
	if err != nil {
		t.Fatalf("count the pre-ownership residue: %v", err)
	}
	if n != 1 {
		t.Errorf("the pre-ownership residue counts %d, want 1. A run excluded from every listing and "+
			"counted nowhere is a run the customer cannot tell from one that was deleted.", n)
	}
}

// TestPGUpgrade_ASecondBootChangesNothing is the other half of the upgrade axis: a rolling restart.
//
// 🔴 D4 decided it — configuration is a SEED, the database is the truth. The failure it avoids is
// concrete rather than theoretical: a customer signs up at 02:00, and the 03:00 rolling restart
// reconciles against a config file that does not mention them. Reconciliation would delete the tenant,
// its members and its credentials — the exact property this phase exists to create, destroyed by the
// mechanism meant to preserve it.
func TestPGUpgrade_ASecondBootChangesNothing(t *testing.T) {
	db, err := pgtest.Open("launch_upgrade_reboot")
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	if _, err := pgmigrate.Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}
	cfg := upgradeConfig()
	sys, err := buildAccountSystem(cfg, db, upgradeAt)
	if err != nil {
		t.Fatalf("first boot: %v", err)
	}

	// Between the two boots, a customer does something the configuration file will never know about:
	// somebody signs up, and somebody is invited into a CONFIGURED organization.
	selfServe, err := sys.Store.CreateTenant(tenancy.Tenant{
		TenantID: "org_selfserve", Name: "Signed Up At 0200", Status: tenancy.StatusActive, CreatedAt: upgradeAt,
	})
	if err != nil {
		t.Fatalf("create the self-serve organization: %v", err)
	}
	joiner, err := sys.Store.UpsertUser(tenancy.User{
		Issuer: "https://idp", Subject: "sub-joiner", Email: "joiner@acme.com", CreatedAt: upgradeAt,
	})
	if err != nil {
		t.Fatalf("create the person: %v", err)
	}
	if _, err := sys.Store.PutMembership(tenancy.Membership{
		UserID: joiner.UserID, TenantID: "acme", Role: tenancy.RoleMember,
		Status: tenancy.MemberActive, JoinedAt: upgradeAt,
	}); err != nil {
		t.Fatalf("add the member: %v", err)
	}

	// ── The 03:00 restart. Same configuration, same code, second time. ───────────────────────────────
	if _, err := buildAccountSystem(cfg, db, upgradeAt.Add(time.Hour)); err != nil {
		t.Fatalf("the second boot failed: %v", err)
	}

	if _, err := sys.Store.GetTenant(selfServe.TenantID); err != nil {
		t.Errorf("the self-serve organization did not survive a restart: %v\n"+
			"Configuration is a SEED. A restart that reconciles against the config file deletes every "+
			"customer the file does not mention, which is every customer who signed themselves up.", err)
	}
	members, err := sys.Store.ListMembers("acme")
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	var found bool
	for _, m := range members {
		if m.UserID == joiner.UserID && m.Active() {
			found = true
		}
	}
	if !found {
		t.Error("the member invited into a CONFIGURED organization was removed by the restart. The " +
			"configuration file names the organization and not its people, so reconciling from it " +
			"deletes them.")
	}
	// And the configured keys are still exactly what they were — created if absent, never rewritten.
	after := auth.NewRegistry(cfg).WithSource(sys.Store)
	if _, ok := after.Lookup(acmeKey); !ok {
		t.Error("the configured credential stopped working after a restart")
	}
}
