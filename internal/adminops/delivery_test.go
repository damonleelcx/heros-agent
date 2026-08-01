package adminops_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/changedelivery"
	"github.com/heros-foreal/agentd/internal/forgedelivery"
)

// delivery_test.go covers P26 wave 26b — the delivery oversight surface.
//
// The load-bearing assertion in this file is TestAClosedPullRequestIsNeverReadAsAMerge. A merge is
// OBSERVED; a pull request that closed may have been merged, squashed, rebased or abandoned, and only
// one of those is a delivery. Reading a close as a merge would tell an operator a change shipped when
// it did not — and, one figure downstream, would put an unearned saving in front of a customer.

// seedDelivery opens a delivery for a tenant and optionally advances it to a terminal state.
func seedDelivery(t *testing.T, h *harness, tenantID, rev string, terminal forgedelivery.State, commit string) string {
	t.Helper()
	id := forgedelivery.DeliveryID("cfg1", rev, "main")
	base := forgedelivery.Entry{
		DeliveryID: id, TenantID: tenantID, ConfigHash: "cfg1", SourceRevision: rev,
		Target: "main", ForgeRef: "pr-" + rev, Mode: forgedelivery.ModeCI,
		State: forgedelivery.StateOpened, Actor: "customer-ci", At: h.clk.now(),
	}
	if err := h.deliveries.Append(context.Background(), base); err != nil {
		t.Fatalf("Append opened: %v", err)
	}
	if terminal != "" {
		next := base
		next.State = terminal
		next.MergeCommit = commit
		if err := h.deliveries.Append(context.Background(), next); err != nil {
			t.Fatalf("Append %s: %v", terminal, err)
		}
	}
	return id
}

// TestAClosedPullRequestIsNeverReadAsAMerge defends P26 task 3.3 and the requirement "A merge SHALL be
// shown as observed, and the outcome SHALL have three states".
//
// 🔴 If this fails, the surface is inferring a merge from a pull request closing. That is the one
// mistake this read model exists to not make.
func TestAClosedPullRequestIsNeverReadAsAMerge(t *testing.T) {
	h := newHarness(t)
	seedDelivery(t, h, tenantAcme, "rev-merged", forgedelivery.StateMerged, "abc123")
	seedDelivery(t, h, tenantAcme, "rev-closed", forgedelivery.StateClosed, "")
	seedDelivery(t, h, tenantAcme, "rev-open", "", "")

	view, err := h.delivery.Tenant(h.ctx(adminrbac.RoleSupport), tenantAcme)
	if err != nil {
		t.Fatalf("Tenant: %v", err)
	}
	byRevision := map[string]adminops.DeliveryRow{}
	for _, r := range view.Rows {
		byRevision[r.SourceRevision] = r
	}
	if len(byRevision) != 3 {
		t.Fatalf("expected three deliveries, got %d: %+v", len(byRevision), view.Rows)
	}

	if got := byRevision["rev-merged"].Merge; got != adminops.MergeObserved {
		t.Fatalf("an OBSERVED merge reads as %q, want %q", got, adminops.MergeObserved)
	}
	if got := byRevision["rev-closed"].Merge; got != adminops.MergeClosedUnmerged {
		t.Fatalf("a pull request closed WITHOUT merging reads as %q, want %q. A close is not a merge: "+
			"it may have been squashed, rebased or abandoned, and only one of those is a delivery.",
			got, adminops.MergeClosedUnmerged)
	}
	if got := byRevision["rev-open"].Merge; got != adminops.MergeUnknown {
		t.Fatalf("an OPEN pull request reads as %q, want %q — the surface must not display the most "+
			"likely outcome in place of one it has not observed", got, adminops.MergeUnknown)
	}
	// The merge commit travels with the observed merge and with nothing else: a merge state with no
	// commit behind it is an assertion nobody made.
	if byRevision["rev-merged"].MergeCommit == "" {
		t.Fatal("an observed merge carries no merge commit")
	}
	if byRevision["rev-closed"].MergeCommit != "" {
		t.Fatal("a closed-unmerged delivery carries a merge commit")
	}
}

// TestTheThreeMergeStatesStayThree defends the same requirement structurally: nothing may collapse the
// enum to two, and every state the P12 record can hold must map to one of the three.
func TestTheThreeMergeStatesStayThree(t *testing.T) {
	if got := len(adminops.MergeStates()); got != 3 {
		t.Fatalf("MergeStates() has %d values, want 3 — a two-valued merge outcome forces `unknown` to "+
			"be rendered as one of the others", got)
	}
	// Totality: every P12 lifecycle state resolves. A state with no mapping would fall through to a
	// zero value, and a zero-valued MergeState renders as an empty cell rather than as an answer.
	for _, s := range []forgedelivery.State{
		forgedelivery.StateOpened, forgedelivery.StateUpdated, forgedelivery.StateSuperseded,
		forgedelivery.StateClosed, forgedelivery.StateMerged, forgedelivery.StateReverted,
	} {
		h := newHarness(t)
		terminal := s
		if s == forgedelivery.StateOpened {
			// `opened` is the FIRST row seedDelivery writes; a second one hits the partial unique index
			// that makes idempotency hold under concurrency, which is a different property than this
			// test's.
			terminal = ""
		}
		seedDelivery(t, h, tenantAcme, "rev-"+string(s), terminal, "")
		view, err := h.delivery.Tenant(h.ctx(adminrbac.RoleSupport), tenantAcme)
		if err != nil {
			t.Fatalf("Tenant: %v", err)
		}
		if len(view.Rows) == 0 {
			t.Fatalf("no row for lifecycle state %q", s)
		}
		got := view.Rows[0].Merge
		var known bool
		for _, m := range adminops.MergeStates() {
			if m == got {
				known = true
			}
		}
		if !known {
			t.Fatalf("lifecycle state %q maps to %q, which is not one of the three merge states", s, got)
		}
	}
}

// TestEveryDeliveryAggregateOffersItsDrillDown defends P26 task 3.7.
//
// An aggregate with no path to its samples hides a single tenant's pathological repository inside a
// fleet-wide number.
func TestEveryDeliveryAggregateOffersItsDrillDown(t *testing.T) {
	h := newHarness(t)
	seedDelivery(t, h, tenantAcme, "rev-a", forgedelivery.StateMerged, "abc")
	seedDelivery(t, h, tenantBoreal, "rev-b", forgedelivery.StateClosed, "")

	view, err := h.delivery.Fleet(h.ctx(adminrbac.RolePlatformSRE))
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(view.Counts) == 0 {
		t.Fatal("the fleet view offers no counts")
	}
	for _, c := range view.Counts {
		if strings.TrimSpace(c.DrillDown) == "" {
			t.Fatalf("count %q offers no drill-down — an aggregate with no path to its samples is "+
				"treated as incomplete", c.Label)
		}
	}
	// And the fleet aggregate really does span tenants, rather than silently serving one.
	tenants := map[string]bool{}
	for _, r := range view.Rows {
		tenants[r.TenantID] = true
	}
	if len(tenants) < 2 {
		t.Fatalf("the fleet aggregate covers %d tenant(s), want at least 2", len(tenants))
	}
}

// TestEveryCrossTenantDeliveryReadIsAuditedOnTheReadPath defends P26 task 3.5 and the requirement
// "Every cross-tenant delivery read SHALL be audited on the same code path as the read".
func TestEveryCrossTenantDeliveryReadIsAuditedOnTheReadPath(t *testing.T) {
	h := newHarness(t)
	before := len(h.entriesFor(adminaudit.ActionCrossTenantView))

	if _, err := h.delivery.Fleet(h.ctx(adminrbac.RolePlatformSRE)); err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	entries := h.entriesFor(adminaudit.ActionCrossTenantView)
	if len(entries) != before+1 {
		t.Fatalf("a cross-tenant delivery read wrote %d audit entries, want 1", len(entries)-before)
	}
	e := entries[len(entries)-1]
	if e.ActorAdminID == "" {
		t.Fatal("the audit entry records no actor")
	}
	if e.Evidence["read_model"] != "delivery" {
		t.Fatalf("the audit entry does not record the read model: %v", e.Evidence)
	}
	if e.Evidence["scope"] != "fleet" {
		t.Fatalf("the audit entry does not record the scope: %v", e.Evidence)
	}
	h.assertChainIntact()

	// A per-tenant read is a DIFFERENT read with its own entry, against that tenant — so an auditor
	// sees which tenant was looked at rather than only that somebody opened a fleet page.
	if _, err := h.delivery.Tenant(h.ctx(adminrbac.RolePlatformSRE), tenantAcme); err != nil {
		t.Fatalf("Tenant: %v", err)
	}
	entries = h.entriesFor(adminaudit.ActionCrossTenantView)
	last := entries[len(entries)-1]
	if !strings.Contains(last.Target, tenantAcme) {
		t.Fatalf("a per-tenant delivery read was audited against %q, not the tenant", last.Target)
	}
}

// TestTheDeliverySurfaceIsReadOnly defends P26 task 3.8 and the requirement "The delivery surface
// SHALL be read-only".
//
// 🔴 It enumerates the service's exported methods rather than asserting on a list somebody maintains.
// A `Retry`, `Close`, `Merge` or `Reopen` added later fails HERE, with the reason: delivery is
// downstream of verification and never a path around it, and in the default mode the platform holds no
// forge credential — so an operator retry would have to create one.
func TestTheDeliverySurfaceIsReadOnly(t *testing.T) {
	allowed := map[string]bool{"Tenant": true, "Fleet": true, "History": true}
	typ := reflect.TypeOf(&adminops.DeliveryService{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !allowed[name] {
			t.Fatalf("DeliveryService exposes %q. This surface reads and does nothing: no method may "+
				"open, close, retry or merge a delivery, and none may cause the platform to hold or use "+
				"a forge credential (P26 §3.8).", name)
		}
	}
	// And the read model says so on the wire, so the console renders the boundary rather than assuming
	// it from an absence of buttons.
	h := newHarness(t)
	view, err := h.delivery.Fleet(h.ctx(adminrbac.RolePlatformSRE))
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if !view.ReadOnly {
		t.Fatal("the delivery read model does not declare itself read-only")
	}
}

// TestUndeliverableCausesStayTypedAndSeparate defends P26 task 3.4 and the requirement "The console
// SHALL show the change-delivery rollout state and its undeliverable causes".
func TestUndeliverableCausesStayTypedAndSeparate(t *testing.T) {
	h := newHarness(t)
	view, err := h.delivery.Fleet(h.ctx(adminrbac.RolePlatformSRE))
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(view.Undeliverable) == 0 {
		t.Fatal("no undeliverable causes are reported — the ADR-010 table has refusals in it")
	}
	// Each cause is a STABLE IDENTIFIER from the changedelivery package, never prose invented here.
	valid := map[string]bool{}
	for _, c := range changedelivery.Causes() {
		valid[string(c)] = true
	}
	var total int
	for _, c := range view.Undeliverable {
		if !valid[c.Cause] {
			t.Fatalf("undeliverable cause %q is not one of the package's stable identifiers", c.Cause)
		}
		if c.Owner == "" {
			t.Fatalf("cause %q names no owner — 'whose move is it' is the one word that decides what a "+
				"reader does next", c.Cause)
		}
		// 🔴 A permanent boundary names NO missing artifact. Attaching one turns "this cannot be built"
		// into "this has not been built yet", and a reader is then told to wait for something that will
		// never arrive.
		if c.Permanent && c.MissingArtifact != "" {
			t.Fatalf("permanent cause %q names missing artifact %q — a boundary has no artifact to build",
				c.Cause, c.MissingArtifact)
		}
		total += c.Count
	}
	if total != view.UndeliverableTotal {
		t.Fatalf("the typed causes sum to %d but the total says %d", total, view.UndeliverableTotal)
	}
	// The causes are in EVALUATION order — nobody, you, the platform — not sorted by volume. Sorting by
	// count would put the platform's backlog first and the permanent boundary last, which is the reading
	// order that sends an engineer to do work that will not help.
	want := []string{}
	for _, c := range changedelivery.Causes() {
		for _, got := range view.Undeliverable {
			if got.Cause == string(c) {
				want = append(want, string(c))
			}
		}
	}
	var got []string
	for _, c := range view.Undeliverable {
		got = append(got, c.Cause)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("undeliverable causes are ordered %v, want evaluation order %v", got, want)
	}
}

// TestDeliveryIsDeniedWithoutTheCapability keeps the surface behind its gate, and keeps the denial
// informative rather than a bare refusal.
func TestDeliveryIsDeniedWithoutTheCapability(t *testing.T) {
	h := newHarness(t)
	// Billing-Ops holds delivery.read; the role that does NOT is exercised through the gate directly,
	// because every seeded role in the fixture holds it except none — so this asserts the gate's own
	// answer for a principal with no live role at all.
	d := h.gate.Authorize("adm-nobody", adminrbac.CapDeliveryRead, adminops.TargetGlobal)
	if d.Allowed {
		t.Fatal("delivery.read was allowed for a principal holding no role")
	}
	if len(d.HeldBy) == 0 {
		t.Fatal("the denial names no holder — a refusal owes the operator an escalation path")
	}
}

// TestTheDeliverySurfaceLinksTheChainOnlyWhereTheChainCovers defends the requirement that the delivery
// surface links to the audit chain "for the paths it does cover" — and, by omission, does not link for
// the path it does not.
func TestTheDeliverySurfaceLinksTheChainOnlyWhereTheChainCovers(t *testing.T) {
	h := newHarness(t)
	seedDelivery(t, h, tenantAcme, "rev-ci-merged", forgedelivery.StateMerged, "abc")

	view, err := h.delivery.Tenant(h.ctx(adminrbac.RoleSupport), tenantAcme)
	if err != nil {
		t.Fatalf("Tenant: %v", err)
	}
	if len(view.Rows) == 0 {
		t.Fatal("no rows")
	}
	// A CI-mediated delivery merges in the CUSTOMER'S CI under a credential the platform does not hold.
	// The chain structurally cannot record it, so the row names no chain entry — linking anyway would
	// imply a coverage the chain does not have.
	if view.Rows[0].AuditTarget != "" {
		t.Fatalf("a CI-mediated delivery links to the audit chain (%q), implying the chain records a "+
			"merge it structurally cannot observe", view.Rows[0].AuditTarget)
	}
	// And the surface restates the boundary from the ONE place that defines it, so the delivery surface
	// and the audit surface cannot describe it two ways.
	if view.MergeCoverage.Statement != adminops.MergeCoverage().Statement {
		t.Fatal("the delivery surface states merge coverage in its own words — two descriptions of one " +
			"boundary is how they start disagreeing")
	}
}
