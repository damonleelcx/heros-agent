//go:build pgproof

// Live-Postgres CONTRACT test for the P26 delivery read model (task 3.9).
//
// It drives the operator read model over `deliveryrecord.PGStore` against the REAL `delivery` table
// produced by the REAL migration chain — no inlined `CREATE TABLE` standing in for a production table.
// The reason is not ceremony: the read model's whole job is to preserve a distinction the database
// enforces (a close is not a merge, and exactly one open pull request exists per delivery), and a test
// against a hand-written approximation of that table proves the approximation behaves.
//
// It runs in CI's `db-proof` job, which already runs `go test -tags pgproof ./internal/adminops/`.
package adminops_test

import (
	"context"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	"github.com/heros-foreal/agentd/internal/forgedelivery"
)

// TestDeliveryReadModelAgainstTheRealDeliveryTable is P26 task 3.9.
//
// 🔴 The assertion that matters: the three merge outcomes survive the round trip through the real
// table. If Postgres, the store and the read model ever disagree about what a closed pull request
// means, this is where it shows — not on an operator's screen.
func TestDeliveryReadModelAgainstTheRealDeliveryTable(t *testing.T) {
	ctx := context.Background()
	store := deliveryrecord.NewPGStore(testDB)

	// The operator layer, wired the way production wires it: a real gate, a real audit chain and the
	// real command path, from the SAME fixture the in-memory tests use. A read model tested with its
	// authorization removed is a read model whose authorization is untested.
	h := newHarness(t)
	accounts := account.NewMemStore()
	for _, id := range []string{"pgtenant-merged", "pgtenant-closed", "pgtenant-open"} {
		if _, err := accounts.Create(account.Account{
			CustomerID: id, ProviderCustomerHandle: "h-" + id, ActivePlanID: "team",
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}
	svc, err := adminops.NewDeliveryService(h.exec, store, accounts)
	if err != nil {
		t.Fatalf("NewDeliveryService: %v", err)
	}

	seed := func(tenant, rev string, terminal forgedelivery.State, commit string) {
		t.Helper()
		id := forgedelivery.DeliveryID("cfg-pg", rev, "main")
		base := forgedelivery.Entry{
			DeliveryID: id, TenantID: tenant, ConfigHash: "cfg-pg", SourceRevision: rev,
			Target: "main", ForgeRef: "pr-" + rev, Mode: forgedelivery.ModeCI,
			State: forgedelivery.StateOpened, Actor: "customer-ci", At: time.Now().UTC(),
		}
		if err := store.Append(ctx, base); err != nil {
			t.Fatalf("Append opened %s: %v", rev, err)
		}
		if terminal == "" {
			return
		}
		next := base
		next.State = terminal
		next.MergeCommit = commit
		next.At = time.Now().UTC()
		if err := store.Append(ctx, next); err != nil {
			t.Fatalf("Append %s %s: %v", terminal, rev, err)
		}
	}

	seed("pgtenant-merged", "pgrev-merged", forgedelivery.StateMerged, "deadbeefcafe")
	seed("pgtenant-closed", "pgrev-closed", forgedelivery.StateClosed, "")
	seed("pgtenant-open", "pgrev-open", "", "")

	want := map[string]adminops.MergeState{
		"pgtenant-merged": adminops.MergeObserved,
		"pgtenant-closed": adminops.MergeClosedUnmerged,
		"pgtenant-open":   adminops.MergeUnknown,
	}
	for tenant, expected := range want {
		view, err := svc.Tenant(h.ctx(adminrbac.RolePlatformSRE), tenant)
		if err != nil {
			t.Fatalf("Tenant(%s): %v", tenant, err)
		}
		if view.Degraded {
			t.Fatalf("Tenant(%s) degraded: %s", tenant, view.Detail)
		}
		if len(view.Rows) != 1 {
			t.Fatalf("Tenant(%s) returned %d rows, want 1", tenant, len(view.Rows))
		}
		if got := view.Rows[0].Merge; got != expected {
			t.Fatalf("through the REAL delivery table, %s reads as %q, want %q", tenant, got, expected)
		}
	}

	// The append-only history is reachable, which is the drill-down behind a row.
	history, err := svc.History(h.ctx(adminrbac.RolePlatformSRE), "pgtenant-merged",
		forgedelivery.DeliveryID("cfg-pg", "pgrev-merged", "main"))
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history has %d entries, want 2 (opened, merged)", len(history))
	}
	if history[0].State != forgedelivery.StateOpened || history[1].State != forgedelivery.StateMerged {
		t.Fatalf("history is not in append order: %v then %v", history[0].State, history[1].State)
	}

	// And the fleet aggregate spans the three tenants without losing the distinction.
	fleet, err := svc.Fleet(h.ctx(adminrbac.RolePlatformSRE))
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	counts := map[string]int{}
	for _, c := range fleet.Counts {
		counts[c.Label] = c.Value
		if c.DrillDown == "" {
			t.Fatalf("fleet count %q offers no drill-down", c.Label)
		}
	}
	for _, m := range adminops.MergeStates() {
		if counts[string(m)] < 1 {
			t.Fatalf("the fleet aggregate reports %d deliveries in state %q, want at least 1 — the three "+
				"outcomes collapsed somewhere between Postgres and the read model", counts[string(m)], m)
		}
	}
}
