//go:build pgproof

// Live-Postgres proof for the delivery-route registry.
//
// What cannot be checked against a fake, and is checked here:
//
//   - The round trip through REAL COLUMNS. The no-database fence in roundtrip_test.go proves a Route
//     survives the column list I wrote down; only this proves the column list matches the table. Those
//     are two different claims, and 0026 is what happens when the second one goes unchecked.
//
//   - Migration 0027's `base_ref` NOT NULL. It is what makes a route reconstructible at all, and a map
//     would accept a route without one and hand back something Validate rejects.
//
//   - The "an explained lost capability" CHECK on capability_kind/capability_detail. A lost capability with no detail is a
//     banner nobody can act on; the database is what refuses it, so a fake asserts nothing.
//
//   - The "" has-any probe, over the CHECK forbidding an empty `target`, which makes a literal lookup impossible.
//
//     make pg-proof
//     HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/deliveryroute/
package deliveryroute

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	fd "github.com/heros-foreal/agentd/internal/forgedelivery"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
)

func pgStore(t *testing.T, schema string) (*PGStore, *sql.DB) {
	t.Helper()
	db, err := pgtest.Open(schema)
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	s, err := NewPGStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return s, db
}

func route(owner, repo, base, workflow string) fd.Route {
	return fd.Route{
		Mode:      fd.ModeCI,
		ForgeKind: fd.ForgeGitHub,
		Target:    fd.Target{Owner: owner, Repo: repo, Base: base, Workflow: workflow},
	}
}

// The claim the whole migration exists for: a route written to the real table comes back DELIVERABLE.
func TestAStoredRouteComesBackDeliverable(t *testing.T) {
	s, _ := pgStore(t, "deliveryroute_roundtrip")
	ctx := t.Context()

	want := route("nousresearch", "hermes-agent", "main", "")
	if err := s.Put(ctx, "tenant-a", want); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := s.RouteFor(ctx, "tenant-a", want.Target.Key())
	if err != nil {
		t.Fatalf("route for: %v", err)
	}
	if got == nil {
		t.Fatal("the route just written was not found")
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("the route read back from the real table is not deliverable: %v\n"+
			"this is exactly what 0026's shape produced, and Service.Pending would have swallowed it "+
			"as an empty pending list", err)
	}
	if *got != want {
		t.Errorf("round trip through the table changed the route:\n wrote: %+v\n read:  %+v", want, *got)
	}
}

// The monorepo case: Target.Key() carries the workflow, base_ref carries the branch, and the two must
// not be confused for one another.
func TestAWorkflowScopedRouteKeepsBothItsWorkflowAndItsBase(t *testing.T) {
	s, _ := pgStore(t, "deliveryroute_workflow")
	ctx := t.Context()

	billing := route("acme", "platform", "develop", "billing")
	reporting := route("acme", "platform", "release/2026.08", "reporting")
	for _, r := range []fd.Route{billing, reporting} {
		if err := s.Put(ctx, "tenant-b", r); err != nil {
			t.Fatalf("put %s: %v", r.Target.Key(), err)
		}
	}

	got, err := s.RouteFor(ctx, "tenant-b", reporting.Target.Key())
	if err != nil || got == nil {
		t.Fatalf("route for %s: %+v %v", reporting.Target.Key(), got, err)
	}
	if got.Target.Workflow != "reporting" || got.Target.Base != "release/2026.08" {
		t.Errorf("two workflows in one repository must keep their own base: got %+v", got.Target)
	}
	// Same repository, different route. Keying on the repository alone would have collapsed these.
	if *got == billing {
		t.Error("the two workflows collapsed into one route")
	}
}

// 🔴 The has-any probe. Service.anyRoute calls this, and a literal implementation returns nil forever
// because the CHECK forbidding an empty `target` makes the row it looks for impossible — so every tenant with routes would be
// told they have none.
func TestAnEmptyTargetIsTheHasAnyProbe(t *testing.T) {
	s, db := pgStore(t, "deliveryroute_hasany")
	ctx := t.Context()

	// Before anything is configured: nil, and no error. "No route" is a condition, not a failure.
	got, err := s.RouteFor(ctx, "tenant-c", "")
	if err != nil {
		t.Fatalf("has-any probe on an unconfigured tenant errored: %v", err)
	}
	if got != nil {
		t.Fatalf("expected no route, got %+v", got)
	}

	if err := s.Put(ctx, "tenant-c", route("acme", "one", "main", "")); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err = s.RouteFor(ctx, "tenant-c", "")
	if err != nil {
		t.Fatalf("has-any probe: %v", err)
	}
	if got == nil {
		t.Fatal("the has-any probe found nothing for a tenant that has a route — " +
			"RouteConditionFor would report `no_route` to a tenant who is fully configured")
	}

	// And the reason a literal lookup cannot work: the database refuses the row it would look for.
	_, err = db.ExecContext(ctx,
		`INSERT INTO delivery_route (tenant_id, target, base_ref, forge, mode)
		 VALUES ('tenant-c', '', 'main', 'github', 'ci')`)
	if err == nil {
		t.Error("the table accepted an empty target — the has-any probe's target space is no longer " +
			"disjoint from real targets, and RouteFor's overload becomes ambiguous")
	}

	// Another tenant's route is not this tenant's. The probe is scoped like every other read.
	if err := s.Put(ctx, "tenant-d", route("other", "repo", "main", "")); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err = s.RouteFor(ctx, "tenant-c", "")
	if err != nil || got == nil {
		t.Fatalf("has-any probe: %+v %v", got, err)
	}
	if got.Target.Owner == "other" {
		t.Error("the has-any probe crossed tenants")
	}
}

func TestCapabilityIsIntactRevokedOrDegradedAndSaysWhy(t *testing.T) {
	s, _ := pgStore(t, "deliveryroute_capability")
	ctx := t.Context()

	one := route("acme", "one", "main", "")
	two := route("acme", "two", "main", "")
	for _, r := range []fd.Route{one, two} {
		if err := s.Put(ctx, "tenant-e", r); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	// Intact is reported as the empty kind, never as an error.
	kind, detail, err := s.Capability(ctx, "tenant-e")
	if err != nil || kind != "" || detail != "" {
		t.Fatalf("a configured tenant with no lost capability: got %q/%q, %v", kind, detail, err)
	}

	if err := s.SetCapability(ctx, "tenant-e", one.Target.Key(), fd.RouteDegraded,
		"the CI credential expired on 2026-08-01"); err != nil {
		t.Fatalf("set degraded: %v", err)
	}
	kind, detail, err = s.Capability(ctx, "tenant-e")
	if err != nil || kind != fd.RouteDegraded || detail == "" {
		t.Fatalf("expected a degraded condition with a detail, got %q/%q, %v", kind, detail, err)
	}

	// Revoked wins over degraded: they have different next actions, and rotating a token for an
	// installation that no longer exists is the wrong one.
	if err := s.SetCapability(ctx, "tenant-e", two.Target.Key(), fd.RouteRevoked,
		"the hosted App installation was removed by the customer"); err != nil {
		t.Fatalf("set revoked: %v", err)
	}
	kind, detail, err = s.Capability(ctx, "tenant-e")
	if err != nil {
		t.Fatalf("capability: %v", err)
	}
	if kind != fd.RouteRevoked {
		t.Errorf("with both conditions present the stronger one must be reported, got %q (%q)", kind, detail)
	}

	// Restoring is deliberate and one route at a time.
	if err := s.ClearCapability(ctx, "tenant-e", two.Target.Key()); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if kind, _, err = s.Capability(ctx, "tenant-e"); err != nil || kind != fd.RouteDegraded {
		t.Errorf("clearing one route's condition must leave the other's, got %q, %v", kind, err)
	}
}

// A re-Put is a reconfiguration of WHERE deliveries go. It must not read as "the revoked installation
// came back" — that would re-enable delivery through a credential that no longer exists.
func TestReconfiguringARouteDoesNotClearALostCapability(t *testing.T) {
	s, _ := pgStore(t, "deliveryroute_reput")
	ctx := t.Context()

	r := route("acme", "one", "main", "")
	if err := s.Put(ctx, "tenant-f", r); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := s.SetCapability(ctx, "tenant-f", r.Target.Key(), fd.RouteRevoked,
		"installation removed"); err != nil {
		t.Fatalf("set revoked: %v", err)
	}

	moved := r
	moved.Target.Base = "develop"
	if err := s.Put(ctx, "tenant-f", moved); err != nil {
		t.Fatalf("re-put: %v", err)
	}

	got, err := s.RouteFor(ctx, "tenant-f", moved.Target.Key())
	if err != nil || got == nil {
		t.Fatalf("route for: %+v %v", got, err)
	}
	if got.Target.Base != "develop" {
		t.Errorf("the re-put did not take: base is %q", got.Target.Base)
	}
	kind, _, err := s.Capability(ctx, "tenant-f")
	if err != nil {
		t.Fatalf("capability: %v", err)
	}
	if kind != fd.RouteRevoked {
		t.Errorf("changing the base branch cleared a revoked installation (now %q) — delivery would "+
			"be offered through a credential the customer removed", kind)
	}
}

// The database is the authority on "a lost capability must say what was lost". The store refuses it
// first so the caller gets a sentence, and this proves the constraint is really there underneath.
func TestALostCapabilityMustBeExplained(t *testing.T) {
	s, db := pgStore(t, "deliveryroute_explained")
	ctx := t.Context()

	r := route("acme", "one", "main", "")
	if err := s.Put(ctx, "tenant-g", r); err != nil {
		t.Fatalf("put: %v", err)
	}

	err := s.SetCapability(ctx, "tenant-g", r.Target.Key(), fd.RouteDegraded, "   ")
	if err == nil {
		t.Fatal("an unexplained degradation was accepted")
	}
	if !strings.Contains(err.Error(), "must say what was lost") {
		t.Errorf("expected the refusal to name what is missing, got: %v", err)
	}

	// And under the store, the constraint itself.
	if _, err := db.ExecContext(ctx,
		`UPDATE delivery_route SET capability_kind = 'degraded', capability_detail = ''
		  WHERE tenant_id = 'tenant-g'`); err == nil {
		t.Error("the table accepted a lost capability with no detail — the console would render a " +
			"banner with no next action")
	}
}

// Marking a route that is not there must be reported. This runs from a webhook or a credential check,
// and a silent zero-row UPDATE is how a tenant keeps being offered deliveries through a dead credential.
func TestMarkingAnAbsentRouteIsReported(t *testing.T) {
	s, _ := pgStore(t, "deliveryroute_absent")
	ctx := t.Context()

	err := s.SetCapability(ctx, "tenant-h", "acme/never-configured", fd.RouteRevoked, "installation removed")
	if err == nil {
		t.Fatal("marking a route that does not exist reported success")
	}
	if !strings.Contains(err.Error(), "no route") {
		t.Errorf("expected the refusal to say the route is missing, got: %v", err)
	}
}

// Migration 0027's NOT NULL. Without it the table can hold the row that reads back as an undeliverable
// route, which is the defect 0026 introduced.
func TestTheTableRefusesARouteWithNoBaseBranch(t *testing.T) {
	_, db := pgStore(t, "deliveryroute_baseref")
	ctx := t.Context()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO delivery_route (tenant_id, target, forge, mode)
		 VALUES ('tenant-i', 'acme/one', 'github', 'ci')`); err == nil {
		t.Error("base_ref is nullable or defaulted — a row written without a base branch reads back as " +
			"a route Validate rejects, and Service.Pending reports that as an empty list")
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO delivery_route (tenant_id, target, base_ref, forge, mode)
		 VALUES ('tenant-i', 'acme/one', '', 'github', 'ci')`); err == nil {
		t.Error("the table accepted an empty base branch")
	}
}

// Storing a declared-but-unimplemented forge is legitimate configuration; the CHECK's job is to keep a
// typo out, not to re-decide what P12 implements.
func TestTheTableStoresADeclaredForgeAndRefusesATypo(t *testing.T) {
	s, db := pgStore(t, "deliveryroute_forge")
	ctx := t.Context()

	gitlab := fd.Route{
		Mode:      fd.ModeCI,
		ForgeKind: fd.ForgeGitLab,
		Target:    fd.Target{Owner: "acme", Repo: "one", Base: "main"},
	}
	if err := s.Put(ctx, "tenant-j", gitlab); err != nil {
		t.Fatalf("storing a gitlab route must be allowed (delivering to it is refused in Go): %v", err)
	}
	got, err := s.RouteFor(ctx, "tenant-j", gitlab.Target.Key())
	if err != nil || got == nil {
		t.Fatalf("route for: %+v %v", got, err)
	}
	if got.ForgeKind != fd.ForgeGitLab {
		t.Errorf("forge came back as %q", got.ForgeKind)
	}
	// ...and it is refused where the reason can be stated.
	if err := got.Validate(); err == nil {
		t.Error("a gitlab route validated as deliverable")
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO delivery_route (tenant_id, target, base_ref, forge, mode)
		 VALUES ('tenant-j', 'acme/two', 'main', 'githbu', 'ci')`); err == nil {
		t.Error("the table accepted a misspelled forge")
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO delivery_route (tenant_id, target, base_ref, forge, mode)
		 VALUES ('tenant-j', 'acme/three', 'main', 'github', 'nightly')`); err == nil {
		t.Error("the table accepted an unknown delivery mode — the column decides who holds a " +
			"credential that can write to a customer's repository")
	}
}

func TestListIsScopedToOneTenant(t *testing.T) {
	s, _ := pgStore(t, "deliveryroute_list")
	ctx := t.Context()

	if err := s.Put(ctx, "tenant-k", route("acme", "one", "main", "")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := s.Put(ctx, "tenant-l", route("other", "two", "main", "")); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := s.List(ctx, "tenant-k")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Target.Owner != "acme" {
		t.Fatalf("list crossed tenants or lost a route: %+v", got)
	}
	if err := got[0].Validate(); err != nil {
		t.Errorf("a listed route is not deliverable: %v", err)
	}
}
