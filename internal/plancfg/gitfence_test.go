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
