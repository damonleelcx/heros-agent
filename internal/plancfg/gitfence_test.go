package plancfg

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// gitfence_test.go is task 1.3's CI assertion: **plan config is sourced only from the config store**,
// so no plan catalog and no priced plan value may exist in a git-tracked file.
//
// Why this is a test and not a shell grep: the fence must be AUTO-DISCOVERING. A hand-maintained
// allowlist of "files we check" protects only the files somebody remembered to add — it cannot protect
// the invariant. This enumerates the ENTIRE git index every run, so a catalog committed under any name,
// in any directory, is caught the first time.
//
// It is also anti-vacuous: if `git ls-files` returns nothing (wrong working directory, no git), the
// test FAILS rather than passing over an empty set. A fence whose assertions are vacuously true is a
// fence that quietly stopped guarding.

// dataExts are the file kinds a configuration catalog can actually be LOADED from. Source files are
// excluded on purpose: this package's own fixtures embed a catalog in a Go string literal, and those
// fixtures are exactly what the tasks require (fixture plan definitions with price REFERENCES and no
// real prices). The rule being enforced is "no priced value, no live catalog in git" — not "no test may
// mention a plan".
var dataExts = map[string]bool{
	".json": true, ".yaml": true, ".yml": true, ".toml": true,
	".ini": true, ".cfg": true, ".conf": true, ".env": true, ".properties": true,
}

// catalogMarkers are the shapes only a plan catalog has: a plan list plus at least one packaging field.
var (
	reHasPlans     = regexp.MustCompile(`(?i)"?plans"?\s*[:=]`)
	rePlanID       = regexp.MustCompile(`(?i)"?plan_id"?\s*[:=]`)
	rePackaging    = regexp.MustCompile(`(?i)"?(price_refs?|sum_band|seat_limit|retention_days|gainshare_rate)"?\s*[:=]`)
	rePricedNumber = regexp.MustCompile(`(?i)"?(price|amount|unit_price|rate|fee|percent|pct|usd_per|monthly|annual)[a-z_]*"?\s*[:=]\s*"?-?\d`)
)

// looksLikePlanCatalog reports whether content is a plan catalog — a plan list carrying packaging
// fields. Factored out so the detector itself can be proven to fire (see TestPlanCatalogDetectorGoesRed).
func looksLikePlanCatalog(path, content string) bool {
	if !dataExts[strings.ToLower(filepath.Ext(path))] {
		return false
	}
	if !reHasPlans.MatchString(content) && !rePlanID.MatchString(content) {
		return false
	}
	return rePackaging.MatchString(content)
}

// pricedPlanValue reports whether content carries a numeric PRICE inside a plan-catalog context. A
// price reference ("price_ref": "price_ref_team_sub") is a string handle and is fine; a numeric one is
// a dollar amount in git, which is the thing the money-in-git rule forbids.
func pricedPlanValue(path, content string) bool {
	if !dataExts[strings.ToLower(filepath.Ext(path))] {
		return false
	}
	if !reHasPlans.MatchString(content) && !rePlanID.MatchString(content) {
		return false
	}
	return rePricedNumber.MatchString(content)
}

// ── P21 task 1.2: the same fence, extended to the PAYMENT UI ────────────────
//
// P7's fence covers the shapes a plan catalog is LOADED from — data files. P21 adds the one surface P7
// could not have anticipated: a payment UI, where a price is most tempting to hardcode "just for
// display". Decision 7 says a price value exists in Stripe and nowhere else, so a priced literal in a
// React source file is the same violation as one in a JSON catalog, and it must fail the same way.
//
// It stays AUTO-DISCOVERING rather than becoming a list of billing files: an allowlist protects only the
// files somebody remembered to add, and the marketing page that quotes a monthly figure is exactly the
// file nobody remembers. So the selector is "every git-tracked source a web application ships"
// (`web/**/src/**`), and the narrowing is done on PLAN CONTEXT — a priced literal is only a plan price
// where the path or the file is talking about plans, billing, checkout, invoices or subscriptions.
//
// The client bundle — the JavaScript the browser actually downloads — is covered by the console's own
// build-time `scripts/scan-bundle.mjs`, which runs as the last step of `npm run build`. Two layers,
// because they fail differently: this one catches the literal at commit time in any file, that one
// catches whatever survives a build, including a value that arrived through a dependency.

// uiExts are the file kinds a payment surface is written in.
var uiExts = map[string]bool{
	".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".css": true, ".html": true,
}

var (
	// rePlanContext is the narrowing: a number is a PLAN PRICE only where plans are being discussed.
	rePlanContext = regexp.MustCompile(`(?i)\b(plan|billing|checkout|invoice|subscription|subscribe|pricing|price_ref)`)
	// rePricedLiteral is the money shape. The dollar branch requires two digits or a decimal on purpose:
	// `$1`..`$9` are regex backreferences in a `.replace()` call, and a fence that cried wolf on those
	// would be switched off within a week.
	rePricedLiteral = regexp.MustCompile(`(?i)(\$\s?(\d{2,}|\d+\.\d))|` +
		`(\b\d[\d,]*(\.\d+)?\s?(usd|eur|gbp)\b)|` +
		`(\b(price|amount|unit_price|rate|fee|percent|pct|usd_per|monthly|annual)[a-z_]*\s*[:=]\s*['"` + "`" + `]?-?\d)|` +
		`(\d\s?(/\s?mo\b|/\s?month\b|per\s+seat\b|per\s+month\b))`)
)

// shippedUISource reports whether path is UI source that a browser can end up executing — anything
// under a web application's `src/`. Build scripts and test files are deliberately outside the fence:
// they never reach a browser, and two of them (`scripts/scan-bundle.mjs`, the console's acceptance
// tests) are the price fences THEMSELVES, whose patterns necessarily spell out the shapes a price
// takes. A fence that flagged its own sibling fence would be switched off within a week — and the
// built output those scripts guard is covered by `scan-bundle.mjs` at build time, so nothing is lost.
func shippedUISource(path string) bool {
	if !uiExts[strings.ToLower(filepath.Ext(path))] {
		return false
	}
	p := filepath.ToSlash(path)
	return strings.HasPrefix(p, "web/") && strings.Contains(p, "/src/")
}

// pricedPaymentUI reports whether a UI source carries a priced literal in a plan/billing context.
// Factored out so the detector itself can be proven to fire (TestPaymentUIPriceDetectorGoesRed).
//
// The plan context is taken from the PATH or the CONTENT: a figure in `.../pricing/page.tsx` is a plan
// price by location even if the sentence around it never says "plan".
func pricedPaymentUI(path, content string) bool {
	if !shippedUISource(path) {
		return false
	}
	if !rePlanContext.MatchString(path) && !rePlanContext.MatchString(content) {
		return false
	}
	return rePricedLiteral.MatchString(content)
}

// repoRoot walks up from the test's working directory to the git root.
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse failed — this fence must run inside the repository: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestNoPlanCatalogInGitTrackedFile enumerates the whole git index and asserts none of it is a plan
// catalog. Plans live in the config store; git holds the mechanism, never the packaging.
func TestNoPlanCatalogInGitTrackedFile(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	files := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")

	// Anti-vacuity: an empty index would make every assertion below trivially pass.
	if len(files) < 100 {
		t.Fatalf("git ls-files returned only %d files — the fence is not seeing the repository, so its "+
			"assertions would be vacuously true", len(files))
	}

	scanned := 0
	for _, rel := range files {
		if rel == "" || !dataExts[strings.ToLower(filepath.Ext(rel))] {
			continue
		}
		abs := filepath.Join(root, rel)
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() || st.Size() > 1<<20 {
			continue
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		scanned++
		content := string(b)
		if looksLikePlanCatalog(rel, content) {
			t.Errorf("%s is a git-tracked PLAN CATALOG — plan definitions belong in the config store, "+
				"never in git (design Decision 3)", rel)
		}
		if pricedPlanValue(rel, content) {
			t.Errorf("%s carries a numeric PRICE in a plan context — prices are provider references, "+
				"never values, and never in git", rel)
		}
	}
	if scanned == 0 {
		t.Fatal("scanned 0 candidate data files — the fence found nothing to check, which is not evidence")
	}
	t.Logf("scanned %d git-tracked data files for plan catalogs / priced plan values", scanned)
}

// TestPlanCatalogDetectorGoesRed proves the fence can FAIL. A guard that has never been shown to fire
// is decoration: these are the exact byte shapes a committed catalog or a committed price would have,
// and the detector must catch each one — while leaving the legitimate shapes alone.
func TestPlanCatalogDetectorGoesRed(t *testing.T) {
	mustCatch := []struct{ name, path, body string }{
		{"json catalog", "config/plans.json", catalogV1},
		{"yaml catalog", "deploy/pricing.yaml", "plans:\n  - plan_id: team\n    sum_band: 1000\n"},
		{"renamed catalog", "internal/data/x.json", `{"plans":[{"plan_id":"a","price_refs":{"s":"r"}}]}`},
	}
	for _, c := range mustCatch {
		if !looksLikePlanCatalog(c.path, c.body) {
			t.Errorf("detector missed a committed catalog: %s", c.name)
		}
	}

	mustCatchPriced := []struct{ name, path, body string }{
		{"numeric price", "config/plans.json", `{"plans":[{"plan_id":"team","price":49}]}`},
		{"gainshare pct", "config/plans.yml", "plans:\n  - plan_id: ent\n    percent_of_savings: 20\n"},
		{"monthly amount", "x.json", `{"plan_id":"team","monthly_usd":49.00}`},
	}
	for _, c := range mustCatchPriced {
		if !pricedPlanValue(c.path, c.body) {
			t.Errorf("detector missed a committed price: %s", c.name)
		}
	}

	mustIgnore := []struct{ name, path, body string }{
		{"go source", "internal/plancfg/plancfg.go", `PriceRefs map[string]string  // "price_refs"`},
		{"markdown spec", "openspec/x.md", "plans: Free / Team, price_refs are opaque"},
		{"unrelated json", "schemas/metric-event.schema.json", `{"properties":{"cost_usd":{"type":"number"}}}`},
		{"price REFERENCE only", "config/plans.json", `{"plans":[{"plan_id":"t","price_refs":{"subscription":"price_ref_t"}}]}`},
	}
	for _, c := range mustIgnore {
		if pricedPlanValue(c.path, c.body) {
			t.Errorf("price detector false-positived on %s", c.name)
		}
	}
	// The last case IS a catalog (correctly) — assert only the three that must not trip the catalog
	// detector either.
	for _, c := range mustIgnore[:3] {
		if looksLikePlanCatalog(c.path, c.body) {
			t.Errorf("catalog detector false-positived on %s", c.name)
		}
	}
}

// TestNoPricedLiteralInPaymentUI is P21 task 1.2: the fence extended to the payment UI.
//
// It enumerates the git index exactly as the catalog fence does — same walk, same anti-vacuity — and
// asserts no git-tracked UI source carries a priced literal while talking about plans. The payment UI is
// the surface Decision 7 is most often violated on, because "just show the price on the button" reads as
// a rendering decision rather than as a price committed to git.
func TestNoPricedLiteralInPaymentUI(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	files := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")

	scanned := 0
	for _, rel := range files {
		if rel == "" || !shippedUISource(rel) {
			continue
		}
		abs := filepath.Join(root, rel)
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() || st.Size() > 1<<20 {
			continue
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		scanned++
		if pricedPaymentUI(rel, string(b)) {
			t.Errorf("%s carries a PRICED LITERAL in a plan/billing context — a concrete price lives in "+
				"Stripe and is referenced only by its opaque price_ref (P21 design Decision 7). Read the "+
				"figure back from the billing API instead of writing it into the UI", rel)
		}
	}

	// Anti-vacuity: this repository ships two web applications. A run that found almost no UI sources is
	// a fence looking at the wrong tree, and its silence would be mistaken for a clean result.
	if scanned < 20 {
		t.Fatalf("scanned only %d git-tracked UI sources — the payment-UI fence is not seeing the web "+
			"applications, so its assertions would be vacuously true", scanned)
	}
	t.Logf("scanned %d git-tracked UI sources for priced literals in a plan/billing context", scanned)
}

// TestPaymentUIPriceDetectorGoesRed proves the payment-UI fence can FAIL — the same anti-decoration
// discipline the catalog detector holds itself to. These are the exact byte shapes a hardcoded price
// takes in a React payment surface.
func TestPaymentUIPriceDetectorGoesRed(t *testing.T) {
	mustCatch := []struct{ name, path, body string }{
		{"dollar amount on a plan button", "web/console/src/app/app/billing/page.tsx",
			`<button>Subscribe to Team — $49/mo</button>`},
		{"decimal amount in a plan card", "web/console/src/components/billing.tsx",
			`const label = "Business plan, $199.00 per month";`},
		{"currency-suffixed figure", "web/console/src/app/(public)/pricing/page.tsx",
			`<p>Team: 49 USD billed monthly</p>`},
		{"priced field in a plan map", "web/console/src/lib/plans.ts",
			`export const plans = [{ id: "team", monthly_amount: 49 }];`},
		{"per-seat rate", "web/admin-console/src/billing/seats.tsx",
			`<span>Subscription: 12 per seat</span>`},
	}
	for _, c := range mustCatch {
		if !pricedPaymentUI(c.path, c.body) {
			t.Errorf("payment-UI price detector missed %s", c.name)
		}
	}

	mustIgnore := []struct{ name, path, body string }{
		// The legitimate shape: a plan by NAME and an opaque reference.
		{"plan by name only", "web/console/src/app/app/billing/page.tsx",
			`<h2>Your plan: {view.plan_name}</h2><p>price_ref {view.price_ref}</p>`},
		// A figure read back from the API is the whole point — it is not a literal.
		{"figure from the API", "web/console/src/app/app/billing/page.tsx",
			`<Stat label="Spend under management" value={view.sum} note={view.sum_unit} />`},
		// Regex backreferences are why the dollar branch requires two digits or a decimal.
		{"regex backreference", "web/console/src/lib/format.ts",
			`return s.replace(/(\d)(\d{3})/g, "$1,$2"); // plan label formatting`},
		// A number outside any plan context is somebody else's concern.
		{"unrelated number", "web/console/src/components/monitor.tsx",
			`const timeoutMs = 3000; const retries = 5;`},
		// Non-UI extensions belong to the catalog fence above, not this one.
		{"go source", "internal/billing/stripe.go",
			`// the plan's price_ref, never an amount: 49 would be a price in git`},
	}
	for _, c := range mustIgnore {
		if pricedPaymentUI(c.path, c.body) {
			t.Errorf("payment-UI price detector false-positived on %s", c.name)
		}
	}
}
