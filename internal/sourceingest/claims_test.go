package sourceingest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// claims_test.go is §8.3 and §8.4 made machine-checkable.
//
// # Why two product rules are enforced by a test
//
// Both are the kind of rule that is obeyed until somebody is writing copy at speed or wiring a feature
// under deadline, and both fail SILENTLY:
//
//	§8.3  "never describe the platform as WATCHING a repository; it reads a revision when asked."
//	      A customer who reads "watches" expects a finding minutes after a merge, waits, and concludes
//	      the product is broken rather than idle. Nothing goes red — the copy is grammatical.
//	§8.4  "a feature that works only under a connection is a defect, not a tier."
//	      A gate written as `if err == ErrNoConnection { return empty }` looks like careful handling and
//	      turns the DEFAULT mode into a downgrade nobody chose. Nothing goes red — the branch is correct
//	      Go and the tests for the connected path all pass.

// repoRoot is this package's path to the tree.
func repoRoot() string { return filepath.Join("..", "..") }

// ── §8.3 · the platform reads when asked; it does not watch ──────────────────────────────────────

// watchingWords are the verbs that describe a SUBSCRIPTION rather than a read.
//
// Matched as whole words next to a repository noun, not as bare substrings: "watch" appears inside
// perfectly ordinary prose ("worth watching for"), and a fence that cried wolf on that would be
// switched off within a week — which is how the rule it protects stops being enforced at all.
var watchingWords = regexp.MustCompile(
	`(?i)\b(watch(es|ing)?|monitor(s|ing)?|track(s|ing)?|sync(s|ing|hronis|hroniz)|poll(s|ing)?|subscribe[sd]?|listen(s|ing)?)\b`)

// prohibitionHeading opens a region whose subject IS the forbidden wording — a "what must never be
// said" table has to quote it, and quoting it is not saying it.
var prohibitionHeading = regexp.MustCompile(`(?i)(must never be said|do not say|never describe|what is NOT)`)

// repositoryNouns are what those verbs must not be applied to.
var repositoryNouns = regexp.MustCompile(`(?i)\b(your repositor(y|ies)|the repositor(y|ies)|repos?\b|the connection|connected repositor(y|ies))`)

// customerFacingCopy is where §8.3 applies: text a customer reads.
//
// 🚫 It deliberately does NOT include Go source or tests. The rule is about how the product DESCRIBES
// itself, and a comment in a store explaining that we do not watch is the opposite of a violation.
func customerFacingCopy(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	roots := []string{
		filepath.Join(repoRoot(), "web", "console", "src", "components", "connections.tsx"),
		filepath.Join(repoRoot(), "web", "console", "src", "app", "app", "connections"),
		filepath.Join(repoRoot(), "docs", "sales", "P32-repo-intake-claims.md"),
	}
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, fi os.FileInfo, werr error) error {
			if werr != nil || fi.IsDir() {
				return nil
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			out[path] = string(b)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if len(out) == 0 {
		t.Fatal("no customer-facing copy was found — this fence would pass vacuously")
	}
	return out
}

// TestTheProductNeverDescribesItselfAsWatchingARepository is §8.3.
func TestTheProductNeverDescribesItselfAsWatchingARepository(t *testing.T) {
	for path, body := range customerFacingCopy(t) {
		// 🔴 The exemption is SECTION-scoped, not line-scoped, and the first version got that wrong.
		//
		// It exempted a line carrying a refusal marker, and went red on the sales doc's own "what must
		// never be said" table — whose whole job is to QUOTE the forbidden sentence so a reader knows
		// which one it is. A per-line marker would have to be repeated on every row, and the row
		// somebody forgets is a row that fails the build for saying the right thing.
		//
		// So a prohibition HEADING opens an exempt region that runs to the next heading. That models
		// what is actually true: inside "what must never be said", the forbidden words are the subject.
		inProhibition := false
		for i, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				inProhibition = prohibitionHeading.MatchString(line)
				continue
			}
			if inProhibition || strings.Contains(line, "🚫") || strings.Contains(line, "never describe") {
				continue
			}
			if watchingWords.MatchString(line) && repositoryNouns.MatchString(line) {
				t.Errorf("%s:%d describes the platform as watching a repository:\n  %s\n\n"+
					"§8.3: it READS A REVISION WHEN ASKED. There is no webhook, no polling loop and no "+
					"subscription to your pushes. A customer who reads this expects a finding minutes after "+
					"a merge, waits, and concludes the product is broken rather than idle.",
					path, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// TestTheCopyStatesTheUnattendedBoundary is §8.2's half of the same subject.
//
// The mirror of the fence above: it refuses a word, and this one REQUIRES a sentence. A copy set that
// merely avoided "watches" while never saying what a connection actually permits would pass the first
// and fail the customer.
func TestTheCopyStatesTheUnattendedBoundary(t *testing.T) {
	var found bool
	for _, body := range customerFacingCopy(t) {
		if strings.Contains(body, "usable when you are not present") ||
			strings.Contains(body, "usable when the customer is not present") {
			found = true
		}
	}
	if !found {
		t.Error("no customer-facing copy states that a connection is usable when the customer is not " +
			"present. §8.2 requires the boundary be said out loud — it is the one thing about this grant " +
			"a customer is least likely to have expected.")
	}
}

// ── §8.4 · no feature is gated on a connection ───────────────────────────────────────────────────

// connectionGateAllowlist is every package that may legitimately know a connection exists.
//
// 🔴 An allowlist of PACKAGES, not of call sites, and short on purpose. A feature gate is written where
// a feature lives, and the four entries below are the connection's own domain, its surface, its wiring,
// and its CLI — none of which is a feature that could be gated.
var connectionGateAllowlist = map[string]string{
	"internal/sourceingest": "the connection's own domain — it defines ErrNoConnection",
	"internal/api":          "the connection SURFACE, whose whole subject is connections",
	"internal/launch":       "the wiring, which chooses the implementation once",
}

// TestNoFeatureIsGatedOnAConnection is §8.4.
//
// # 🔴 What it actually checks, and why that is the right proxy
//
// "A feature works only under a connection" is not directly detectable. What IS detectable is the shape
// a gate takes: some package outside the connection's own domain has to learn whether a connection
// exists, and the only ways to learn are `ErrNoConnection`, `ConnectionStore` and `Connection` itself.
// A package that cannot name any of them cannot gate on one.
//
// The mode router is what makes this true rather than merely unenforced: everything downstream receives
// a `Source` and cannot tell which implementation it holds, which is design D1's whole point.
func TestNoFeatureIsGatedOnAConnection(t *testing.T) {
	markers := []string{"ErrNoConnection", "sourceingest.Connection", "ConnectionStore", "ModeConnected"}

	root := filepath.Join(repoRoot(), "internal")
	scanned := 0
	err := filepath.Walk(root, func(path string, fi os.FileInfo, werr error) error {
		if werr != nil || fi.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		pkg := filepath.ToSlash(filepath.Dir(path))
		pkg = pkg[strings.Index(pkg, "internal"):]
		if _, ok := connectionGateAllowlist[pkg]; ok {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		scanned++
		for _, marker := range markers {
			if strings.Contains(string(b), marker) {
				t.Errorf("%s names %q, and its package is not one that may know a connection exists.\n\n"+
					"§8.4 and FR12: NO feature is gated on a connection. A tenant who only pushes bundles "+
					"reaches every surface. The way that stops being true is a package outside the "+
					"connection's own domain learning whether one exists — which is what this line does.\n"+
					"If this package legitimately owns connections, add it to connectionGateAllowlist WITH "+
					"THE REASON.", filepath.ToSlash(path), marker)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if scanned < 100 {
		t.Fatalf("only %d files were scanned — this fence is broken, not the code", scanned)
	}
}

// TestTheDiscoveryPipelineTakesASourceAndNotAModeRouter pins design D1 at the seam.
//
// 🔴 `hostdiscovery.NewRunner` must accept the INTERFACE. If it took a `*ModeRouter`, every consumer
// below it would gain the ability to ask which mode it was holding — and the first one to ask would be
// the one that renders a "connect to see this" prompt. The interface is what makes D1 structural rather
// than a convention.
func TestTheDiscoveryPipelineTakesASourceAndNotAModeRouter(t *testing.T) {
	path := filepath.Join(repoRoot(), "internal", "hostdiscovery", "hostdiscovery.go")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if strings.Contains(string(b), "ModeRouter") || strings.Contains(string(b), "GitSource") {
		t.Error("internal/hostdiscovery names a concrete source implementation. It must take " +
			"`sourceingest.Source` — every consumer receives a snapshot and cannot tell how it was " +
			"obtained (design D1), and a concrete type is how that stops being true.")
	}
	if !strings.Contains(string(b), "sourceingest.Source") {
		t.Error("internal/hostdiscovery does not take sourceingest.Source — this fence is reading the " +
			"wrong file, or the seam moved")
	}
}

// TestTheModeRouterIsConstructedExactlyOnce is task 2.10.
//
// *"One constructor call in `internal/launch` selects the implementation — no branch threaded through
// the pipeline."* A second construction site is a second place the choice is made, and two places that
// make one choice eventually disagree.
func TestTheModeRouterIsConstructedExactlyOnce(t *testing.T) {
	root := filepath.Join(repoRoot(), "internal")
	sites := []string{}
	err := filepath.Walk(root, func(path string, fi os.FileInfo, werr error) error {
		if werr != nil || fi.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), "NewModeRouter(") && !strings.HasSuffix(path, "moderouter.go") {
			sites = append(sites, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(sites) != 1 {
		t.Errorf("NewModeRouter is called from %d places (%v); task 2.10 requires exactly ONE, in "+
			"internal/launch — two places that make one choice eventually disagree", len(sites), sites)
	}
	if len(sites) == 1 && !strings.Contains(sites[0], "internal/launch") {
		t.Errorf("NewModeRouter is called from %s, not from internal/launch", sites[0])
	}
}

// TestThisPackageImportsNothingThatWouldMakeItAConsumer keeps the domain a domain.
//
// A `sourceingest` that imported a console type or a discovery runner would be a package that could
// start deciding what its callers render — which is how "the mode is invisible downstream" becomes
// "the mode is invisible except where it is not".
func TestThisPackageImportsNothingThatWouldMakeItAConsumer(t *testing.T) {
	forbidden := []string{"internal/api", "internal/hostdiscovery", "internal/conversation", "internal/launch"}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		checked++
		f, perr := parser.ParseFile(token.NewFileSet(), e.Name(), nil, parser.ImportsOnly)
		if perr != nil {
			t.Fatalf("parse %s: %v", e.Name(), perr)
		}
		for _, imp := range f.Imports {
			p, uerr := strconv.Unquote(imp.Path.Value)
			if uerr != nil {
				t.Fatalf("unquote: %v", uerr)
			}
			for _, bad := range forbidden {
				if strings.HasSuffix(p, bad) {
					t.Errorf("%s imports %s — this package supplies source and must not know what its "+
						"callers do with it", e.Name(), p)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files were parsed; this fence is asserting nothing")
	}
}
