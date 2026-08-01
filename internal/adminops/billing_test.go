package adminops_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/verification"
)

// billing_test.go covers task 4.3 — the entitlement override (FR8, load-bearing) and Billing-Ops
// oversight (FR9).

// TestOverrideTakesEffectWithNoCodeDeployAndIsAudited is FR8's load-bearing scenario.
//
// "No code deploy" is asserted the only way it can be: the entitlement gate is queried BEFORE and
// AFTER the override in the same running process, with no rebuild, no migration, and no restart —
// and the answer changes, because everything downstream resolves the plan from the config store.
func TestOverrideTakesEffectWithNoCodeDeployAndIsAudited(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleBillingOps)

	gate := entitlement.NewGate(h.accounts, h.plans, h.usage)
	gate.SetClock(h.clk.now)

	// tenant-castle is on Team: no auto-merge.
	before, err := gate.AutoMerge(tenantCastle)
	if err != nil {
		t.Fatalf("AutoMerge: %v", err)
	}
	if before.Allowed {
		t.Fatal("precondition: a Team tenant should not be entitled to auto-merge")
	}

	receipt, err := h.entitlements.Override(ctx, tenantCastle, "enterprise",
		"ticket ACC-77: signed enterprise agreement, entitlement pending the next billing cycle", adminops.Confirm())
	if err != nil {
		t.Fatalf("Override: %v", err)
	}

	after, err := gate.AutoMerge(tenantCastle)
	if err != nil {
		t.Fatalf("AutoMerge: %v", err)
	}
	if !after.Allowed {
		t.Fatalf("the override did not take effect in-process: %s", after.Reason)
	}
	if after.PlanName != "Enterprise" {
		t.Errorf("plan after override = %q, want Enterprise", after.PlanName)
	}

	// Audited with actor, target, the entitlement changed, the reason and the timestamp.
	entries := h.entriesFor(adminaudit.ActionEntitlementOverride)
	if len(entries) != 2 {
		t.Fatalf("override wrote %d audit entries, want 2", len(entries))
	}
	outcome := entries[1]
	if outcome.ActorAdminID != h.adminIDs[adminrbac.RoleBillingOps] {
		t.Errorf("audit actor = %q", outcome.ActorAdminID)
	}
	if outcome.Target != adminops.TenantTarget(tenantCastle) {
		t.Errorf("audit target = %q", outcome.Target)
	}
	if outcome.Evidence["to_plan_name"] != "Enterprise" || outcome.Evidence["from_plan_id"] != "team" {
		t.Errorf("audit evidence does not record the entitlement changed: %v", outcome.Evidence)
	}
	if outcome.Evidence["plan_config_version"] == "" {
		t.Error("the override did not pin the plan-config version it resolved under")
	}
	if outcome.Reason == "" || outcome.CreatedAt.IsZero() {
		t.Error("the override entry is missing its reason or timestamp")
	}
	if receipt.Result != adminops.ResultApplied {
		t.Errorf("receipt result = %q", receipt.Result)
	}
	h.assertChainIntact()

	// The audited evidence carries plan NAMES and REFERENCES, never a price value.
	if err := adminops.AssertNoPriceValue(outcome.Evidence); err != nil {
		t.Errorf("override evidence carries a price-like field: %v", err)
	}
}

// TestOverrideOntoAnUnpublishedPlanIsRefused: an override that would leave the tenant pointing at a
// plan nothing resolves is refused up front rather than applied and discovered by the customer.
func TestOverrideOntoAnUnpublishedPlanIsRefused(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleBillingOps)
	if _, err := h.entitlements.Override(ctx, tenantCastle, "platinum", "typo", adminops.Confirm()); err == nil {
		t.Fatal("an override onto an unpublished plan was accepted")
	}
	acct, _ := h.accounts.Get(tenantCastle)
	if acct.ActivePlanID != "team" {
		t.Errorf("a refused override changed the plan to %q", acct.ActivePlanID)
	}
}

// TestSupportCannotOverrideEntitlement: least privilege at the override path.
func TestSupportCannotOverrideEntitlement(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSupport)
	if _, err := h.entitlements.Override(ctx, tenantCastle, "enterprise", "customer asked", adminops.Confirm()); !errors.Is(err, adminops.ErrDenied) {
		t.Fatalf("Support overriding an entitlement: err = %v, want ErrDenied", err)
	}
	if acct, _ := h.accounts.Get(tenantCastle); acct.ActivePlanID != "team" {
		t.Fatal("a denied override took effect")
	}
	if _, err := h.entitlements.Plans(ctx); !errors.Is(err, adminops.ErrDenied) {
		t.Errorf("Support enumerated the plan catalog: err = %v, want ErrDenied", err)
	}
}

// TestBillingOpsCreditIsAdditiveAndAuditedAndSupportCannot is FR9's first scenario.
func TestBillingOpsCreditIsAdditiveAndAuditedAndSupportCannot(t *testing.T) {
	h := newHarness(t)
	chargeID := h.seedMeteredCharge(tenantAcme)

	// ── Support cannot ──
	supportCtx := h.ctx(adminrbac.RoleSupport)
	if _, err := h.bills.IssueCredit(supportCtx, tenantAcme, chargeID, "customer complained", adminops.Confirm()); !errors.Is(err, adminops.ErrDenied) {
		t.Fatalf("Support issuing a credit: err = %v, want ErrDenied", err)
	}
	if _, err := h.bills.Oversight(supportCtx, tenantAcme, h.period); !errors.Is(err, adminops.ErrDenied) {
		t.Fatalf("Support reading billing oversight: err = %v, want ErrDenied", err)
	}

	// ── Billing-Ops can, and the correction is ADDITIVE ──
	ctx := h.ctx(adminrbac.RoleBillingOps)
	before := len(h.billing.Ledger().Events(tenantAcme, h.period.ID))
	receipt, err := h.bills.IssueCredit(ctx, tenantAcme, chargeID,
		"ticket BIL-311: metered usage double-reported during the provider incident", adminops.Confirm())
	if err != nil {
		t.Fatalf("IssueCredit: %v", err)
	}
	events := h.billing.Ledger().Events(tenantAcme, h.period.ID)
	if len(events) != before+1 {
		t.Fatalf("a credit produced %d new ledger rows, want exactly 1 (additive)", len(events)-before)
	}

	// The ORIGINAL charge is intact — same status, same quantity, same provider ref.
	original, err := findEvent(events, chargeID)
	if err != nil {
		t.Fatalf("the original charge is gone from the ledger: %v", err)
	}
	if original.Status != billing.StatusRecorded || original.ProviderRef == "" {
		t.Errorf("the original charge was mutated by the correction: %+v", original)
	}
	credit, err := findEvent(events, receipt.Evidence["billing_event_id"])
	if err != nil {
		t.Fatalf("the credit is not in the ledger: %v", err)
	}
	if credit.Type != billing.TypeCredit {
		t.Errorf("credit event type = %q, want %q", credit.Type, billing.TypeCredit)
	}
	if credit.CausedBy == "" || !strings.Contains(credit.CausedBy, chargeID) {
		t.Errorf("the credit does not name the charge it corrects: caused_by = %q", credit.CausedBy)
	}

	// ── and it is in the ADMIN audit log too ──
	adminEntries := h.entriesFor(adminaudit.ActionBillingCredit)
	if len(adminEntries) != 2 {
		t.Fatalf("the credit wrote %d admin audit entries, want 2", len(adminEntries))
	}
	if adminEntries[1].Evidence["billing_event_id"] != credit.EventID {
		t.Error("the admin audit entry does not reference the billing event it produced")
	}
	h.assertChainIntact()
}

// TestGainshareChargeWithNoVerifiedEvidenceIsAnException is FR9's second scenario.
func TestGainshareChargeWithNoVerifiedEvidenceIsAnException(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleBillingOps)

	// A genuine, merged, verified saving raises a gainshare charge that TRACES.
	delta := metering.VerifiedDelta{
		Ref: "vd-real-1", ProposalID: "prop-1", CustomerID: tenantAcme, Period: h.period.ID,
		Verdict: verification.Verdict{
			GateResult: verification.GatePass, HeldOut: true, Significant: true, RegressionPass: true,
			Delta: evalstats.Interval{Mean: 0.4, Low: 0.35, High: 0.45},
		},
		Merged: true, MergeCommit: "9f3c1a2merge", BaselineSUM: 100, OptimizedSUM: 60,
		Baseline: metering.BaselineMethod{
			ID: "holdout-v1", EvalSetHash: "es-1", HoldoutCaseIDs: []string{"c4", "c5", "c6"},
			GeneratingCaseIDs: []string{"c1", "c2", "c3"}, Seeds: []int64{1, 2, 3},
			BaselineConfigHash: "base-1", CandidateConfigHash: "cand-1",
		},
	}
	h.deltas.Put(delta)
	if _, err := h.billing.ChargeGainshare(context.Background(), tenantAcme, h.period, h.deltas, h.savings); err != nil {
		t.Fatalf("ChargeGainshare: %v", err)
	}

	view, err := h.bills.Oversight(ctx, tenantAcme, h.period)
	if err != nil {
		t.Fatalf("Oversight: %v", err)
	}
	if len(view.Gainshare) != 1 {
		t.Fatalf("oversight shows %d gainshare charges, want 1", len(view.Gainshare))
	}
	good := view.Gainshare[0]
	if !good.Valid() {
		t.Fatalf("a merged, verified saving was flagged as an exception: %s", good.Exception)
	}
	if len(good.MergeCommits) == 0 || good.MergeCommits[0] != "9f3c1a2merge" {
		t.Errorf("the charge does not show the merge commit behind it: %+v", good)
	}
	if view.Exceptions != 0 {
		t.Errorf("exceptions = %d, want 0", view.Exceptions)
	}

	// ── Now the failure case: the evidence no longer resolves ──
	//
	// A ledger that has lost the entry is exactly what a mis-attributed or rolled-back saving looks
	// like from the billing side, and the console must NOT render the charge as valid.
	forgetful, err := adminops.NewBillingService(h.exec, h.billing, emptyDeltaLedger{}, h.links)
	if err != nil {
		t.Fatalf("NewBillingService: %v", err)
	}
	broken, err := forgetful.Oversight(ctx, tenantAcme, h.period)
	if err != nil {
		t.Fatalf("Oversight: %v", err)
	}
	if len(broken.Gainshare) != 1 {
		t.Fatalf("oversight shows %d gainshare charges, want 1", len(broken.Gainshare))
	}
	if broken.Gainshare[0].Valid() {
		t.Fatal("a gainshare charge whose evidence does not resolve was shown as valid")
	}
	if broken.Exceptions != 1 {
		t.Errorf("exceptions = %d, want 1", broken.Exceptions)
	}
}

// emptyDeltaLedger resolves nothing — the shape of a ledger that has lost the entry a charge names.
type emptyDeltaLedger struct{}

func (emptyDeltaLedger) VerifiedDeltas(string, string) ([]metering.VerifiedDelta, error) {
	return nil, nil
}
func (emptyDeltaLedger) ByRef(string) (metering.VerifiedDelta, bool) {
	return metering.VerifiedDelta{}, false
}
func (emptyDeltaLedger) Describe() string { return "empty" }

// TestOversightShowsInvoicesDunningAndReconciliation: the read half of FR9.
func TestOversightShowsInvoicesDunningAndReconciliation(t *testing.T) {
	h := newHarness(t)
	h.seedMeteredCharge(tenantAcme)
	ctx := h.ctx(adminrbac.RoleBillingOps)

	view, err := h.bills.Oversight(ctx, tenantAcme, h.period)
	if err != nil {
		t.Fatalf("Oversight: %v", err)
	}
	if len(view.Invoices) == 0 {
		t.Fatal("oversight shows no invoices for a tenant that was charged")
	}
	if view.ReconciliationDegraded {
		t.Fatalf("reconciliation reported degraded against a healthy provider: %s", view.ReconciliationDetail)
	}
	if !view.ReconciliationMatched {
		t.Errorf("reconciliation reported drift on a clean period: %+v", view.Drift)
	}
	for _, line := range view.Invoices {
		if line.EventID == "" || line.Type == "" || line.Status == "" {
			t.Errorf("invoice line is missing identity fields: %+v", line)
		}
	}
}

// TestReconciliationTransportFailureRendersDegradedNotMatched: a provider outage is DEGRADED, never
// "matched" — a transport failure is not absence of drift (FR26).
func TestReconciliationTransportFailureRendersDegradedNotMatched(t *testing.T) {
	h := newHarness(t)
	h.seedMeteredCharge(tenantAcme)
	ctx := h.ctx(adminrbac.RoleBillingOps)

	h.provider.SetDown(true)
	view, err := h.bills.Oversight(ctx, tenantAcme, h.period)
	if err != nil {
		t.Fatalf("Oversight during a provider outage returned an error instead of a degraded view: %v", err)
	}
	if !view.ReconciliationDegraded {
		t.Fatal("a provider outage was not reported as degraded")
	}
	if view.ReconciliationMatched {
		t.Fatal("a provider outage was reported as a matched reconciliation")
	}
	if view.ReconciliationDetail == "" {
		t.Error("the degraded view does not say what failed")
	}
}

// findEvent locates a billing event by id.
func findEvent(events []billing.BillingEvent, id string) (billing.BillingEvent, error) {
	for _, e := range events {
		if e.EventID == id {
			return e, nil
		}
	}
	return billing.BillingEvent{}, errors.New("no such event: " + id)
}

// ── The money-in-git fence ──────────────────────────────────────────────────────────────────────

// pricedValue matches a currency amount, a rate, or a price band written as a literal.
//
// # Why each pattern is shaped the way it is
//
// The fence has to tell a BUSINESS NUMBER from a number that merely contains a currency character or
// a percent sign, because the tree it scans now includes SQL, CSS and JavaScript:
//
//	currency  requires a MONETARY form — a decimal amount ($0.003, $12.50) or a thousands separator
//	          ($1,299). A bare `$1` is a Postgres placeholder in `QueryRow(..., $1)` and a regex
//	          backreference in `.replace(re, "$1")`; flagging those would make the fence cry wolf on
//	          every build until somebody deleted it, which is a far worse outcome than the one dead
//	          pattern it would have caught.
//	rate      a percentage is only a business rate when it is not a LENGTH. `width: 100%` and
//	          `inset(50%)` are CSS lengths; `200% zoom` is prose. Those are excluded by context in
//	          pricedLine below rather than by weakening this pattern.
//
// The price-band form stays exact: `price_band = 3` is unambiguous wherever it appears.
var pricedValue = regexp.MustCompile(`(?i)(\$\s?\d[\d,]*\.\d|\$\s?\d{1,3}(,\d{3})+|\d[\d,]*(\.\d+)?\s?(usd|eur|gbp)\b|\d+(\.\d+)?\s?%|price[_ ]?(band|amount|value)\s*[:=]\s*\d)`)

// currencyOrBand is the context-INDEPENDENT half of the fence: an amount or a named band is a
// business number wherever it is written, including in a comment. Only the percentage reading depends
// on context.
var currencyOrBand = regexp.MustCompile(`(?i)(\$\s?\d[\d,]*\.\d|\$\s?\d{1,3}(,\d{3})+|price[_ ]?(band|amount|value)\s*[:=]\s*\d)`)

// cssLength matches a percentage used as a LAYOUT value rather than as a rate — a CSS declaration, a
// CSS length function, or an inline style object. These are lengths in a stylesheet, not money.
var cssLength = regexp.MustCompile(`(?i)(style=\{\{|\b(width|height|inset|top|right|bottom|left|flex|basis|translate|scale|size|zoom|opacity|stop-color|gradient)\b\s*[:(]|\b(calc|min|max|clamp|inset|rgb|rgba|hsl|hsla)\s*\()`)

// pricedLine reports whether a source line carries a priced literal, discounting the contexts where a
// percent sign is not a rate: a stylesheet, a CSS/inline-style length, and a comment.
//
// Comments are excluded because the fence's subject is what the PRODUCT says, not what an engineer
// wrote about it — and because a comment explaining a money rule (like this one) must be able to
// name the shape it is describing without failing the rule it describes.
//
// # Why a whole stylesheet is exempt from the percentage reading
//
// `cssLength` recognises a percentage inside a declaration it knows the property of, or inside a CSS
// function. That covers most of a stylesheet and missed two shapes the moment a real one was added to
// the tree: a keyframe SELECTOR (`0%,` / `50% {` / `100% {`, which is not a declaration at all) and a
// percentage inside a data URI (`height='100%25'` in an inline SVG, where it is URL-encoded and the
// surrounding property is `background-image`).
//
// Extending the property list would have fixed those two and left the next two. The honest rule is
// the structural one: **a `.css` file has no prices in it.** Money in this product is a reference
// resolved from the config store, and a stylesheet cannot resolve one. So a stylesheet keeps the
// context-independent half of the fence — a currency amount or a named band is still caught, because
// `content: "$9.99"` would still be a business number in version history — and loses only the
// percentage reading, which in that file can only ever be a length, a selector or an opacity.
func pricedLine(path, line string) bool {
	trimmed := strings.TrimSpace(line)
	isComment := strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") ||
		strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "<!--")
	if isComment || strings.HasSuffix(path, ".css") || cssLength.MatchString(line) {
		// Still catch a currency amount or a named band even in these contexts — only the percentage
		// reading is context-dependent.
		return currencyOrBand.MatchString(line)
	}
	return pricedValue.MatchString(line)
}

// isText reports whether a tracked file is source the fence can meaningfully read.
//
// # Why this exists
//
// The fence walks everything `git ls-files` reports under the console tree, and that tree now carries
// self-hosted font binaries. A `.woff2` has no lines and no literals: splitting its bytes on "\n"
// produces spans of compressed data, and a span of compressed data contains every two-character
// sequence eventually — including `6%`. The fence reported eleven priced literals in four fonts, none
// of which was a number anybody wrote.
//
// That is worse than a missed leak. A gate whose failures are meaningless is a gate people learn to
// skim past, and the one real finding arrives in the middle of a page of noise from a font file.
//
// The test is content-based rather than an extension allowlist, because the allowlist would need
// amending for every binary the tree ever gains — and the amendment nobody makes is the day the fence
// starts crying wolf again. A NUL byte, or bytes that are not valid UTF-8, means this is not text.
func isText(b []byte) bool {
	return utf8.Valid(b) && !bytes.ContainsRune(b, 0)
}

// TestPricedLineDiscriminates proves the fence still goes RED, and says exactly where it goes green.
//
// It exists because the two carve-outs above — a stylesheet, and a file that is not text — were added
// to stop false positives, and a carve-out added to stop noise is one refactor away from stopping
// everything. A fence nobody has watched fail is a fence nobody knows is connected, so this watches it
// fail on the shapes that matter and pass on the shapes that were crying wolf.
func TestPricedLineDiscriminates(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		line   string
		priced bool
	}{
		// The fence's actual subject: money written into version history.
		{"currency in Go", "internal/adminops/plan.go", `price := "$12.50"`, true},
		{"currency with separator", "internal/adminops/plan.go", `const enterprise = "$1,299"`, true},
		{"named band", "internal/adminops/plan.go", `price_band = 3`, true},
		{"rate in Go", "internal/adminops/plan.go", `overage := 15% of base`, true},
		{"currency in a comment", "internal/adminops/plan.go", `// the Team plan is $49.00`, true},

		// 🔴 A stylesheet loses the percentage reading and KEEPS the currency one. If this case ever
		// goes green, the `.css` carve-out has stopped being a carve-out and become a hole.
		{"currency in CSS", "web/admin-console/src/app/globals.css", `content: "$9.99";`, true},

		// The shapes that were failing the build with no business number anywhere in them.
		{"keyframe selector", "web/admin-console/src/app/globals.css", `  50% {`, false},
		{"keyframe list", "web/admin-console/src/app/globals.css", `  0%,`, false},
		{"percentage in a data URI", "web/admin-console/src/app/globals.css", `background-image: url("…height='100%25'…");`, false},
		{"a plain CSS length", "web/admin-console/src/app/globals.css", `width: 100%;`, false},
		{"a length outside CSS", "web/admin-console/src/components/bar.tsx", `style={{ width: "80%" }}`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pricedLine(tc.path, tc.line); got != tc.priced {
				t.Errorf("pricedLine(%q, %q) = %v, want %v", tc.path, tc.line, got, tc.priced)
			}
		})
	}

	// And a binary is never read as source. The four font files under the console tree reported
	// eleven "priced literals" between them, every one of which was a byte pair inside compressed
	// glyph data.
	if isText([]byte{0x77, 0x4f, 0x46, 0x32, 0x00, 0x36, 0x25}) {
		t.Error("a blob containing a NUL byte was treated as source")
	}
	if !isText([]byte("width: 100%;\n")) {
		t.Error("ordinary UTF-8 source was treated as a binary")
	}
}

// TestNoPricedValueInAdminSource is P8's half of the money-in-git rule (FR8, task 13.3).
//
// It scans the GIT-TRACKED admin-console source for a priced literal. Plans are named, limits are
// quantities, and prices are opaque REFERENCES resolved from the config store — so a currency amount
// or a percentage appearing in this tree means somebody put a business number into version history,
// which is permanent and cannot be un-published.
func TestNoPricedValueInAdminSource(t *testing.T) {
	root := repoRoot(t)
	out, err := exec.Command("git", "-C", root, "ls-files",
		"internal/adminops", "internal/adminrbac", "internal/adminaudit", "internal/adminidentity",
		"internal/api/p8.go", "cmd/proof/operatorconsole", "web/admin-console").Output()
	if err != nil {
		t.Skipf("git ls-files unavailable (%v) — the fence runs in CI where the index is present", err)
	}
	files := strings.Fields(string(out))
	if len(files) == 0 {
		t.Skip("no P8 files are tracked yet — the fence has nothing to scan")
	}
	for _, rel := range files {
		if strings.Contains(rel, "node_modules/") || strings.HasSuffix(rel, ".lock") ||
			strings.HasSuffix(rel, "package-lock.json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		// A money fence must be able to name what it scans for. This test and its JavaScript sibling
		// (the shipped-bundle scan) are the two places these tokens legitimately appear as PATTERNS.
		if strings.Contains(rel, "billing_test.go") || strings.HasSuffix(rel, "scripts/scan-bundle.mjs") {
			continue
		}
		if !isText(b) {
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			if pricedLine(rel, line) {
				t.Errorf("%s:%d contains a priced literal — plans are named and prices are references: %q", rel, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// repoRoot walks up from the package directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the module root")
	return ""
}

// TestPlanOptionsCarryNamesNotPrices: the console's plan picker is priceless by construction.
func TestPlanOptionsCarryNamesNotPrices(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleBillingOps)
	opts, err := h.entitlements.Plans(ctx)
	if err != nil {
		t.Fatalf("Plans: %v", err)
	}
	if len(opts) != 4 {
		t.Fatalf("Plans returned %d options, want the four named plans", len(opts))
	}
	names := map[string]bool{}
	for _, o := range opts {
		if o.PlanName == "" {
			t.Errorf("plan %q has no name", o.PlanID)
		}
		names[o.PlanName] = true
	}
	for _, want := range []string{"Free", "Team", "Business", "Enterprise"} {
		if !names[want] {
			t.Errorf("the plan picker is missing the %s plan", want)
		}
	}
	// The enterprise option names auto_merge as a feature, so the override's blast radius is legible
	// before it is applied.
	for _, o := range opts {
		if o.PlanID == "enterprise" && !containsStr(o.Features, string(plancfg.FeatureAutoMerge)) {
			t.Error("the Enterprise option does not name auto-merge, so an override's effect is invisible")
		}
	}
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestOversightShowsThePlanTrailTheInvoiceListStructurallyCannot is the gap a real operator hit.
//
// 🔴 A plan change is written to the ledger with NO period — it is not a periodic event. The invoice
// list is period-filtered (`Events(tenant, period)`, which skips any row whose period differs), so it
// can never contain one. The result was an oversight page that showed a metered charge and its credit
// while three audited plan changes for the same tenant were invisible, and an operator asking "why is
// this tenant on Enterprise, and who moved them there?" had no surface that answered it.
//
// The fix is a separate, deliberately UNFILTERED trail — not stamping a period onto an event that has
// none, which would make "period" mean two different things in one ledger.
func TestOversightShowsThePlanTrailTheInvoiceListStructurallyCannot(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleBillingOps)

	// Two audited plan changes, exactly as the entitlement sync writes them: no period.
	for _, r := range []struct{ cause, reason string }{
		{"subscription:sub_1", "invoice paid: plan free -> Business"},
		{"subscription:sub_2", "invoice paid: plan Business -> Enterprise"},
	} {
		if _, err := h.billing.Ledger().Append(billing.BillingEvent{
			CustomerID: tenantAcme, Type: billing.TypePlanChange,
			IdempotencyKey: r.cause, CausedBy: r.cause, Reason: r.reason,
			Status: billing.StatusRecorded, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("append plan change: %v", err)
		}
	}

	view, err := h.bills.Oversight(ctx, tenantAcme, h.period)
	if err != nil {
		t.Fatalf("Oversight: %v", err)
	}

	// The invoice list still does not carry them — that is correct, and asserting it keeps the two
	// surfaces from quietly merging.
	for _, inv := range view.Invoices {
		if inv.Type == string(billing.TypePlanChange) {
			t.Errorf("a period-less plan change appeared in the period-filtered invoice list: %+v", inv)
		}
	}

	if len(view.PlanHistory) != 2 {
		t.Fatalf("plan history has %d entries, want 2 — the audited trail is invisible again", len(view.PlanHistory))
	}
	// Newest first: an operator reads "what is true now" and then downwards into history.
	if !strings.Contains(view.PlanHistory[0].Reason, "Enterprise") {
		t.Errorf("newest entry is %q, want the Enterprise move first", view.PlanHistory[0].Reason)
	}
	if view.PlanHistory[0].CausedBy == "" {
		t.Error("a plan change with no cause is not auditable — it says entitlement moved and nothing about why")
	}
}
