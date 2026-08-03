package adminops_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// exit_test.go is P26 wave 26f — the reconciliation the phase exits on.
//
// Two of these are the phase's own claims turned into assertions rather than sentences: that no existing
// role widened, and that no new table was created. The third is the headline metric, which is written so
// it can report that the phase FAILED.

// preP26Capabilities is the capability set as it stood BEFORE this change, written out by hand.
//
// 🔴 Hand-written on purpose. Deriving it from the current code would make the assertion tautological —
// it would compare `Capabilities` to itself and pass on any widening. This list is the historical fact
// the assertion is against, and editing it to make a test pass is editing the evidence.
var preP26Capabilities = map[adminrbac.Capability][]adminrbac.Role{
	adminrbac.CapTenantRead:          {adminrbac.RoleSupport, adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapJobRead:             {adminrbac.RoleSupport, adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapImpersonateRead:     {adminrbac.RoleSupport, adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapBillingRead:         {adminrbac.RoleBillingOps, adminrbac.RoleSuperadmin},
	adminrbac.CapBillingCorrect:      {adminrbac.RoleBillingOps, adminrbac.RoleSuperadmin},
	adminrbac.CapEntitlementOverride: {adminrbac.RoleBillingOps, adminrbac.RoleSuperadmin},
	adminrbac.CapJobRetry:            {adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapJobCancel:           {adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapRegistryAdmin:       {adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapKillSwitch:          {adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapTenantSuspend:       {adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapTenantQuota:         {adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapRoleGrant:           {adminrbac.RoleSuperadmin},
	adminrbac.CapGDPRExecute:         {adminrbac.RoleSuperadmin},
	adminrbac.CapCrossTenantRead:     {adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapAuditRead:           {adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapImpersonateElevate:  {adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
}

// TestNoExistingRoleWidened defends P26 task 8.2.
//
// The reading it enforces, stated because the two halves of the requirement look contradictory: for
// every capability that existed BEFORE this change, the set of roles holding it is unchanged. A NEW
// capability may be held where it was deliberately granted — a new capability held nowhere would be
// unreachable, which is the one thing D8 is not asking for. What is forbidden is a role picking up an
// existing power because a new page made it convenient.
func TestNoExistingRoleWidened(t *testing.T) {
	for capability, want := range preP26Capabilities {
		got := adminrbac.HoldersOf(capability)
		if len(got) != len(want) {
			t.Fatalf("capability %s is now held by %v; before P26 it was held by %v. No existing role may "+
				"gain a capability it did not hold (P26 §8.2, design D8).", capability, got, want)
		}
		held := map[adminrbac.Role]bool{}
		for _, r := range got {
			held[r] = true
		}
		for _, r := range want {
			if !held[r] {
				t.Fatalf("capability %s LOST holder %s — a role narrowing is also a change this phase did "+
					"not intend", capability, r)
			}
		}
	}
	// And the only capabilities that are NEW are the three this phase argued for.
	newOnes := map[adminrbac.Capability]bool{
		adminrbac.CapDeliveryRead: true, adminrbac.CapReleaseRead: true, adminrbac.CapAxisRead: true,
	}
	for _, c := range adminrbac.Capabilities {
		if _, existed := preP26Capabilities[c]; existed {
			continue
		}
		if !newOnes[c] {
			t.Fatalf("capability %s appeared without being one of P26's three declared additions", c)
		}
		if len(adminrbac.HoldersOf(c)) == 0 {
			t.Fatalf("new capability %s is granted to no role — deny-by-default made it unreachable", c)
		}
	}
}

// TestNoNewTableWasCreated defends P26 task 8.2's second half and design D7.
//
// Creating a table is a one-way door, and "build it now for future use" is refused. Where a read was not
// derivable this phase recorded `not-yet-readable` with the missing collection named — which is an
// honest gap with a cause, and directly actionable by the next phase.
//
// # Why this is an allowlist rather than a ceiling
//
// It used to assert that NO migration numbered above 19 existed, on the reasoning that 19 was the
// highest at P26's exit so anything above it had to be P26's. That proxy holds exactly until the next
// phase adds a migration for its own reasons — and then the fence fails for a change it was never aimed
// at, and the tempting fix is to raise the number, which retires the fence silently.
//
// So later migrations are allowed, one at a time, each named with WHOSE it is. A genuinely new P26 table
// still fails, because nobody would be able to add it here without writing down that P26 owns it.
func TestNoNewTableWasCreated(t *testing.T) {
	dir := filepath.Join("..", "..", "db", "migrations", "postgres")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	// The highest migration at P26's exit. Anything above it must be listed below with its owner.
	const highestAtP26Exit = 19
	// Migrations added AFTER P26, and the phase each belongs to. P26 owns none of them, which is the
	// claim this test exists to keep true.
	postP26 := map[string]string{
		"0020_p11_run_links":          "P11 run linking — the durable Store that let the capability mount (P19 §11)",
		"0021_p11_workflow_ir":        "P11 opt-in workflow structure — the store behind `heros link --with-ir`, and the data the pattern graph is finally drawn from",
		"0022_platform_discovery":     "P1 platform-side discovery — the pushed source snapshot and the classified graph discovered from it, which is what lets the pattern graph carry LABELS rather than unclassified dots",
		"0023_run_link_eval_evidence": "P4/P4.5 — the EVIDENCE behind a linked run's scores (case and seed counts, the customer's own gate verdict, per-node cost/latency). Columns on run_link, not a new table; it is what makes the eval board and the scorecard mountable without the platform inventing a gate outcome",
	}
	numbered := regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.(up|down)\.sql$`)
	for _, e := range entries {
		m := numbered.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		var n int
		for _, r := range m[1] {
			n = n*10 + int(r-'0')
		}
		if n <= highestAtP26Exit {
			continue
		}
		if _, known := postP26[m[1]+"_"+m[2]]; !known {
			t.Fatalf("migration %s creates a table and names no owner. P26 creates NO new table: every "+
				"read derives from an existing store, and where one does not the ledger records "+
				"`not-yet-readable` naming the collection that would make it readable (design D7). If this "+
				"migration belongs to another phase, add it to postP26 with that phase — if it is P26's, "+
				"it must not exist.", e.Name())
		}
	}
}

// TestTheHeadlineMetricCanReportFailure defends P26 task 8.3.
//
// 🔴 The assertion is not that the number is good. It is that the measurement WORKS and can say the
// phase failed — a metric that can only report success is not a metric. So this drives all three
// verdicts through the real classifier over real audit records.
func TestTheHeadlineMetricCanReportFailure(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSupport)

	// BEFORE: four impersonation sessions, three of them opened for a question a P26 surface now
	// answers. These go through the REAL impersonation service, so the reasons land in the real audit
	// chain by the real code path.
	before := []string{
		"ticket 41: the customer asks whether their delivery pull request was merged",
		"ticket 42: checking why this tenant's prompt change was refused — coverage question",
		"ticket 43: which version is this tenant running, signing key question after the rotation",
		"ticket 44: reproducing a rendering fault the customer reported on their own dashboard",
	}
	for _, reason := range before {
		if _, _, err := h.impersonation.Start(ctx, tenantAcme, reason, 5*time.Minute, adminops.Confirm()); err != nil {
			t.Fatalf("Start: %v", err)
		}
	}
	baseline := adminops.MeasureDisplacement("before", h.audit)
	if baseline.Sessions != 4 {
		t.Fatalf("baseline counted %d sessions, want 4", baseline.Sessions)
	}
	if baseline.Displaceable != 3 {
		t.Fatalf("baseline classified %d sessions as displaceable, want 3 (by_subject=%v, unclassified=%d)",
			baseline.Displaceable, baseline.BySubject, baseline.Unclassified)
	}
	if baseline.Unclassified != 1 {
		t.Fatalf("baseline left %d reasons unclassified, want 1 — the remainder must be reported, not "+
			"assumed to be zero", baseline.Unclassified)
	}

	// The three verdicts, driven through the real comparator.
	unmoved := adminops.CompareDisplacement(baseline, baseline)
	if !strings.HasPrefix(unmoved.Verdict, "UNMOVED") {
		t.Fatalf("an unchanged ratio reports %q — it must say the surfaces did not displace what they "+
			"were built to displace", unmoved.Verdict)
	}
	improved := adminops.CompareDisplacement(baseline, adminops.DisplacementReading{
		Label: "after", Sessions: 4, Displaceable: 1, Unclassified: 3, Ratio: 0.25,
	})
	if !strings.HasPrefix(improved.Verdict, "DISPLACED") {
		t.Fatalf("a fallen ratio reports %q", improved.Verdict)
	}
	if improved.Delta >= 0 {
		t.Fatalf("a fallen ratio has delta %v, want negative", improved.Delta)
	}
	worse := adminops.CompareDisplacement(adminops.DisplacementReading{
		Label: "before", Sessions: 4, Displaceable: 1, Ratio: 0.25,
	}, baseline)
	if !strings.HasPrefix(worse.Verdict, "WORSE") {
		t.Fatalf("a risen ratio reports %q — the metric must be able to say this went the wrong way",
			worse.Verdict)
	}
	// And an absent baseline is not silently reported as a perfect score.
	none := adminops.CompareDisplacement(adminops.DisplacementReading{Label: "before"}, baseline)
	if !strings.HasPrefix(none.Verdict, "NO BASELINE") {
		t.Fatalf("an empty baseline reports %q — a ratio over an empty corpus is not a measurement",
			none.Verdict)
	}

	// The credited surfaces are the ones the ledger says exist, so the claim is checkable.
	for _, want := range []string{"/axes", "/billing", "/delivery", "/oversight", "/releases"} {
		var found bool
		for _, s := range unmoved.Surfaces {
			if s == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("the metric credits %v, which does not include %s", unmoved.Surfaces, want)
		}
	}
}

// TestTheDisplacementClassifierReportsItsRemainder defends the honesty of the number itself.
func TestTheDisplacementClassifierReportsItsRemainder(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSupport)
	if _, _, err := h.impersonation.Start(ctx, tenantAcme,
		"ticket 90: something the classifier has no term for at all", time.Minute, adminops.Confirm()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	r := adminops.MeasureDisplacement("after", h.audit)
	if r.Unclassified != 1 || r.Displaceable != 0 {
		t.Fatalf("an unmatched reason was classified: %+v", r)
	}
	if r.Ratio != 0 {
		t.Fatalf("ratio = %v with nothing displaceable", r.Ratio)
	}
	// The subject list stays SHORT. A long one would inflate the number by claiming credit for lookups
	// these surfaces do not answer.
	if n := len(adminops.DisplaceableSubjects()); n > 6 {
		t.Fatalf("the displaceable-subject list has grown to %d entries — every addition claims credit "+
			"for a lookup, and the metric is only as honest as that list is short", n)
	}
	for _, s := range adminops.DisplaceableSubjects() {
		if s.Surface == "" || len(s.Terms) == 0 {
			t.Fatalf("displaceable subject %s names no surface or no terms", s.ID)
		}
	}
}
