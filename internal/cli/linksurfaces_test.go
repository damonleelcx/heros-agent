package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// 🔴 P29 §2.10 — the surface list is DERIVED, not hand-written.
//
// The defect this whole phase exists to close is fifteen console surfaces that had nothing to say and no
// screen saying why. The CLI's answer is to name each one and how to fill it — and that answer is worth
// exactly as much as its completeness. A list somebody maintains by hand falls behind the product on the
// first page anybody adds, and it falls behind SILENTLY, which is the same failure shape one layer up.
//
// So the console's own route directory is the source of truth, and this test compares the two. A page
// added under `web/console/src/app/app/` fails here until a decision is recorded for it — including the
// decision "this is not a data surface", which is `FillNotByLinking` with a sentence.
//
// Verified red by adding a directory `web/console/src/app/app/scorecards/` with a page in it: the test
// failed naming `scorecards` and quoting the two things a person has to decide.
func TestEveryConsoleSurfaceIsAccountedForInTheLinkReport(t *testing.T) {
	root := filepath.Join("..", "..", "web", "console", "src", "app", "app")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("the console tree is not present in this checkout: %v", err)
	}

	onDisk := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Next.js route groups `(x)` and private folders `_x` are not routes.
		if strings.HasPrefix(name, "(") || strings.HasPrefix(name, "_") {
			continue
		}
		onDisk[name] = true
	}
	if len(onDisk) < 5 {
		t.Fatalf("found only %d route director(ies) under %s — the reader is not reading the console, so "+
			"every assertion here would pass for the wrong reason", len(onDisk), root)
	}

	declared := map[string]bool{}
	for _, s := range LinkSurfaces() {
		if declared[s.Name] {
			t.Errorf("surface %q is declared twice", s.Name)
		}
		declared[s.Name] = true
		if s.FillWith == "" {
			t.Errorf("surface %q names no way to fill it. Every entry says something: a fillable one names "+
				"the ONE option, and an unfillable one says why nothing would help — a blank is how a "+
				"reader ends up hunting for an option that does not exist.", s.Name)
		}
		if s.Route != "/app/"+s.Name {
			t.Errorf("surface %q has route %q; the route must be /app/%s so the two cannot drift", s.Name, s.Route, s.Name)
		}
	}

	var missing, stale []string
	for name := range onDisk {
		if !declared[name] {
			missing = append(missing, name)
		}
	}
	for name := range declared {
		if !onDisk[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)

	if len(missing) > 0 {
		t.Errorf("the console serves these pages and `heros link` says nothing about them:\n  %s\n\n"+
			"Decide for each, in LinkSurfaces(): which mechanism fills it (link / with-ir / link-receipt), "+
			"and the ONE thing a reader would type. A page that linking cannot fill is FillNotByLinking "+
			"with a sentence saying why — omitting it is what produced the empty console this phase is "+
			"fixing.", strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("`heros link` names these surfaces and the console does not serve them:\n  %s\n"+
			"A link that sends a reader to a page that does not exist is worse than one that says nothing.",
			strings.Join(stale, "\n  "))
	}
}

// A default link fills some surfaces and not others, and the report must say so rather than claiming
// everything. Asserted directly, because a report that marked everything filled would satisfy the
// completeness test above and be useless.
func TestTheSurfaceReportDistinguishesFilledFromUnfilled(t *testing.T) {
	def := ReportLinkSurfaces(false, false)
	full := ReportLinkSurfaces(true, true)

	countFilled := func(rs []LinkSurfaceReport) int {
		n := 0
		for _, r := range rs {
			if r.Filled {
				n++
			}
		}
		return n
	}
	d, f := countFilled(def), countFilled(full)
	if d == 0 {
		t.Error("a default link filled NO surface — the run itself lands on /app/runs, so this is wrong " +
			"and it would tell a reader their successful link did nothing")
	}
	if f <= d {
		t.Errorf("opting in filled %d surface(s) and the default filled %d — the opt-ins must fill MORE, "+
			"or the whole message is decoration", f, d)
	}
	// And the ones linking cannot fill stay unfilled even with every opt-in, which is the honest answer.
	for _, r := range full {
		if r.Name == "variants" && r.Filled {
			t.Error("`variants` was reported as filled by linking. It is an AUTHORING surface: it fills " +
				"when a Variant Spec is submitted, and linking travels the other way. Claiming otherwise " +
				"sends a reader to a page that will be empty.")
		}
	}
}
