package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// p27_fence_fixtures_test.go is P27 task 11.1: four fences, each DRILLED against a checked-in broken
// fixture it has been observed failing on.
//
// # Why a fixture and not a comment saying "this was tested"
//
// Every fence here is a source scan, and a source scan on a clean tree returns nothing. So does a source
// scan with a typo in its pattern, a scan whose walk never reaches the directories that matter, and a
// scan that was quietly narrowed to make an unrelated failure go away. All four produce the identical
// signal — green — and green is what a reviewer sees.
//
// The fixtures under `testdata/p27-fences/` are the difference. Each is a small file that commits the
// violation its fence exists to catch, and each fence is run against BOTH: the fixture, where it must
// find the violation, and the real tree, where it must find none. A fence that stops working now fails
// the first assertion, loudly, naming which of the four went blind.
//
// # Why the fixtures are named `*.go.fixture`
//
// They must not compile. `Go` excludes `testdata/` from builds, so a `.go` file there is already inert —
// but `gofmt`, `go vet`, editors and the linter all still walk it, and a deliberately-wrong file that
// tools keep reporting is a file somebody eventually "fixes". The suffix makes the intent unmissable and
// keeps every tool out. It also keeps them out of the REAL-tree scans below without needing an
// exclusion, which matters: an exclusion is how a fence stops seeing a directory.
//
// # What the four are
//
//	header              a request describing its own authority (`X-Console-Tenant`), reintroduced
//	unowned-run         a run created with an empty owner — indistinguishable from a pre-P27 run forever
//	seat-from-usage     the seat gate reading a metered flow instead of membership (the P7 defect)
//	unhashed-credential a plaintext secret assigned to the stored `Hash` column

const fenceFixtureRoot = "testdata/p27-fences"

// repoRoot is where the real-tree scans start. Two levels up from internal/api.
const repoRoot = "../.."

// finding is one violation: where it is, and the line that commits it.
type finding struct {
	path string
	line int
	text string
}

func (f finding) String() string {
	return f.path + ":" + strconv.Itoa(f.line) + ": " + strings.TrimSpace(f.text)
}

// ── the walker ───────────────────────────────────────────────────────────────────────────────────────

// scanner is one fence: which files it reads, and what it considers a violation.
type scanner struct {
	name string
	// interesting decides which files this fence reads. It receives a slash-separated path and whether
	// this scan is over the FIXTURE tree.
	//
	// 🔴 The flag is not a convenience. Without it the fixture files were `interesting` to the real-tree
	// scan as well — they live under `internal/api/testdata/`, which is inside the repository — so every
	// fence reported its own broken fixture as a violation of the real tree. Two scans over one corpus
	// need to disagree about one directory, and the disagreement has to be explicit.
	interesting func(path string, fixtures bool) bool
	// violation reports whether a single line commits the violation. It receives the line with comments
	// still attached — a fence that stripped them could not tell a comment from code, and the callers
	// that WANT to allow comments say so themselves.
	violation func(line string) bool
}

// scan walks a root and returns every finding.
//
// 🔴 It reads `*.go.fixture` too. A walker that filtered by extension the way the real-tree scans do
// would silently find nothing in the fixture directory, and the drill below would pass by finding
// nothing where nothing was looked for — the exact failure the drill exists to catch, one level up.
func (s scanner) scan(t *testing.T, root string, fixtures bool) []finding {
	t.Helper()
	var out []finding
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		slash := filepath.ToSlash(path)
		if !s.interesting(slash, fixtures) {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for i, line := range strings.Split(string(b), "\n") {
			if s.violation(line) {
				out = append(out, finding{path: slash, line: i + 1, text: line})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s: walk %s: %v", s.name, root, err)
	}
	return out
}

// sourceExt is the set of files that could actually SHIP a violation. Documentation and specs discuss
// all four of these by name on purpose.
func sourceExt(path string) bool {
	switch filepath.Ext(path) {
	case ".go", ".ts", ".tsx", ".js", ".mjs":
		return true
	}
	return false
}

// notOurOwnScaffolding excludes the paths every real-tree scan must skip: build output, vendored code,
// this file, and the fixtures — which are `.go.fixture` and therefore already excluded by extension, so
// this is belt and braces rather than the mechanism.
func notOurOwnScaffolding(path string) bool {
	for _, seg := range []string{"/node_modules/", "/.claude/", "/.next/", "/testdata/", "/dist/"} {
		if strings.Contains(path, seg) {
			return false
		}
	}
	return !strings.HasSuffix(path, "p27_fence_fixtures_test.go")
}

// ── fence 1 · the header that describes its own authority ────────────────────────────────────────────

var headerPattern = regexp.MustCompile(`X-Console-Tenant`)

// headerFence allows the string inside a COMMENT. The history of why the header is gone is worth
// keeping, and a fence that forbade the explanation would delete the reason along with the code —
// `ownership_fence_test.go` made the same allowance for the same reason and this is that rule, extracted
// so there is one implementation of it rather than two that can drift.
var headerFence = scanner{
	name: "X-Console-Tenant",
	interesting: func(p string, fixtures bool) bool {
		if fixtures {
			return strings.HasSuffix(p, ".go.fixture")
		}
		if !sourceExt(p) || !notOurOwnScaffolding(p) {
			return false
		}
		// A test that SETS the header to prove it changes nothing is this fence's ally. The target is
		// code that forwards it.
		return !strings.HasSuffix(p, "_test.go") && !strings.HasSuffix(p, ".test.mjs")
	},
	violation: func(line string) bool {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "#") {
			return false
		}
		return headerPattern.MatchString(line)
	},
}

// ── fence 2 · a run created with no owner ────────────────────────────────────────────────────────────

// unownedRunPattern matches a run-store Start whose owner argument is the empty literal.
//
// The empty value is LEGAL and means pre-ownership — a run from before P27 whose owner was never
// written. That is exactly why writing one deliberately is the violation: a new run spelled that way is
// indistinguishable from a genuine pre-P27 row forever, so it belongs to nobody, appears in nobody's
// listing, and its usage is billed to nobody.
var unownedRunPattern = regexp.MustCompile(`\.Start\((?:[^()]|\([^()]*\))*,\s*""\s*\)`)

var unownedRunFence = scanner{
	name: "a run created with no owner",
	interesting: func(p string, fixtures bool) bool {
		if fixtures {
			return strings.HasSuffix(p, ".go.fixture")
		}
		// Non-test Go only. Tests seed pre-ownership rows on purpose — `internal/pgmigrate` and
		// `internal/executor` both do, to prove the residue is handled — and flagging them would teach
		// somebody to widen this pattern until it stopped seeing anything.
		return strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") && notOurOwnScaffolding(p)
	},
	violation: func(line string) bool {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			return false
		}
		return unownedRunPattern.MatchString(line)
	},
}

// ── fence 3 · a seat count read from the usage store ─────────────────────────────────────────────────

// The category error, made mechanical: the seat meter resolved through the USAGE store.
//
// A seat count is a STATE and lives in membership. `plancfg.LimitSeats` and `metering.MetricSeats` have
// both existed since P7 and nothing ever wrote a `seats` usage record, so the gate compared an allowance
// against zero forever and passed — a plan that sold five seats admitted five hundred, and nothing
// failed. `entitlement.stateMetrics` is the correction: it routes the seat meter to `SeatCounter`
// (membership) instead of to `observed` (usage).
//
// 🔴 The rule is LINE-scoped, and the first draft was file-scoped. That version flagged
// `internal/entitlement/entitlement.go` itself — the file that HOLDS the correction — because it names
// both the limit and the metric in one table, and it flagged `internal/billingview` and three `cmd/proof`
// views for rendering an invoice line that legitimately cites the period peak. A fence that reports the
// cure as the disease is a fence somebody switches off. What is actually wrong is a seat quantity coming
// OUT of a usage read, so that is what this matches.
var seatFromUsagePattern = regexp.MustCompile(`(?:observed\(|usage\[|Usage\(|\.Get\(metering\.Key)[^\n]*MetricSeats|MetricSeats[^\n]*(?:observed\(|usage\[)`)

var seatFromUsageFence = scanner{
	name: "a seat count read from the usage store",
	interesting: func(p string, fixtures bool) bool {
		if fixtures {
			return strings.HasSuffix(p, ".go.fixture")
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || !notOurOwnScaffolding(p) {
			return false
		}
		// `internal/seats` WRITES the period peak through `RecordUsage`, which is the one legitimate
		// meeting of these two ideas — the peak is what an invoice may cite. Excluding the cure from the
		// diagnosis, and only the cure.
		return !strings.Contains(p, "/internal/seats/")
	},
	violation: func(line string) bool {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			return false
		}
		return seatFromUsagePattern.MatchString(line)
	},
}

// stateMetricsPattern is the other half, and it is a POSITIVE assertion rather than a ban: the seat meter
// must be CLASSIFIED as a state. Deleting that one line silently sends `measure` back through `observed`,
// and every line-scoped ban above would still be satisfied — the regression leaves no new code, only a
// missing entry.
var stateMetricsPattern = regexp.MustCompile(`metering\.MetricSeats:\s*true`)

// ── fence 4 · a credential written unhashed ──────────────────────────────────────────────────────────

// unhashedPattern matches a `Hash:` field assigned something that is plainly a plaintext: a string
// literal, or an identifier named after the secret it carries.
//
// The type already makes the accident hard — `Credential` has a Hash and no secret field — but the field
// takes any string, and a caller that HOLDS the plaintext is one assignment away from storing it. A
// stored plaintext is a bearer token at rest: whoever can read the row can authenticate as its owner,
// and no rotation of ours can tell that it happened.
var unhashedPattern = regexp.MustCompile(`\bHash:\s*(?:"[^"]*"|(?:[A-Za-z_][A-Za-z0-9_.]*\.)?(?i:secret|plaintext|plainText|apiKey|api_key|rawKey|rawSecret|token)\b)`)

var unhashedCredentialFence = scanner{
	name: "a credential written unhashed",
	interesting: func(p string, fixtures bool) bool {
		if fixtures {
			return strings.HasSuffix(p, ".go.fixture")
		}
		return strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") && notOurOwnScaffolding(p)
	},
	violation: func(line string) bool {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			return false
		}
		// `HashSecret(secret)` is the CORRECT spelling and contains the word. Match on the field
		// assignment only, and let the hashing call through.
		if strings.Contains(line, "HashSecret(") {
			return false
		}
		return unhashedPattern.MatchString(line)
	},
}

// ── the drills ───────────────────────────────────────────────────────────────────────────────────────

// TestEveryFenceIsRedAgainstItsBrokenFixture is the assertion 11.1 actually asks for. It runs FIRST in
// the file, before any test relies on a fence being green, for the reason the origin gate's self-test
// runs first: a green result from a broken fence is indistinguishable from a green result from a clean
// tree, and the second is what everybody assumes.
func TestEveryFenceIsRedAgainstItsBrokenFixture(t *testing.T) {
	for _, drill := range []struct {
		dir   string
		find  func(t *testing.T, root string, fixtures bool) []finding
		what  string
		costs string
	}{
		{
			dir:  "header",
			find: headerFence.scan,
			what: "a forwarder that reintroduces X-Console-Tenant",
			costs: "a request describes its own authority again, which is what ADR-008 Rule 2 forbids and " +
				"the state P27 found the platform in",
		},
		{
			dir:  "unowned-run",
			find: unownedRunFence.scan,
			what: "a run started with an empty owner",
			costs: "a new run becomes indistinguishable from a pre-P27 one forever: nobody's listing " +
				"returns it and nobody is billed for it",
		},
		{
			dir:   "seat-from-usage",
			find:  seatFromUsageFence.scan,
			what:  "a seat allowance compared against a metered usage record",
			costs: "the P7 defect returns — a plan that sells five seats admits five hundred, silently",
		},
		{
			dir:   "unhashed-credential",
			find:  unhashedCredentialFence.scan,
			what:  "a plaintext secret assigned to the stored Hash column",
			costs: "the stored value becomes a bearer token at rest, and no rotation of ours can tell it happened",
		},
	} {
		t.Run(drill.dir, func(t *testing.T) {
			root := filepath.Join(fenceFixtureRoot, drill.dir)
			if _, err := os.Stat(root); err != nil {
				t.Fatalf("the broken fixture at %s is missing: %v\n"+
					"Without it this fence has never been observed failing, and a source scan that finds "+
					"nothing looks identical whether it works or not.", root, err)
			}
			got := drill.find(t, root, true)
			if len(got) == 0 {
				t.Fatalf("the %s fence found NOTHING in its own broken fixture (%s).\n"+
					"The fixture commits exactly one violation: %s. If this fence has gone blind, what it "+
					"stops noticing is: %s.", drill.dir, root, drill.what, drill.costs)
			}
			t.Logf("red as required — %d finding(s), first at %s", len(got), got[0])
		})
	}
}

// TestTheRealTreeIsCleanUnderEveryFence is the ordinary assertion, and it means something only because
// the drill above ran.
func TestTheRealTreeIsCleanUnderEveryFence(t *testing.T) {
	for _, f := range []struct {
		name string
		find func(t *testing.T, root string, fixtures bool) []finding
		why  string
	}{
		{"X-Console-Tenant", headerFence.scan,
			"Scope travels INSIDE the credential. A header that names authority is a loaded gun with the " +
				"safety on: the next person to see it reads it as the isolation mechanism and makes it " +
				"load-bearing. If a tracing field is wanted, give it a name that does not read as authority."},
		{"a run created with no owner", unownedRunFence.scan,
			"The owner comes from the verified principal and is written once. An empty value means " +
				"PRE-OWNERSHIP — a run from before P27 whose owner is not recoverable — and a new run must " +
				"never be written into that state."},
		{"a seat count read from the usage store", seatFromUsageFence.scan,
			"The current seat count is read from MEMBERSHIP (`seats.Current`). A metered `seats` record is " +
				"the period PEAK an invoice may cite, and nothing writes it at membership time — gating on " +
				"it compares an allowance against a permanent zero."},
		{"a credential written unhashed", unhashedCredentialFence.scan,
			"`tenancy.HashSecret` is the one way a presented secret becomes a stored value, and nothing " +
				"in the store can return a plaintext afterwards because nothing holds one."},
	} {
		t.Run(f.name, func(t *testing.T) {
			if got := f.find(t, repoRoot, false); len(got) > 0 {
				var lines []string
				for _, g := range got {
					lines = append(lines, "  "+g.String())
				}
				t.Fatalf("%s:\n%s\n\n%s", f.name, strings.Join(lines, "\n"), f.why)
			}
		})
	}
}

// TestTheSeatMeterIsStillClassifiedAsAState is fence 3's positive half.
//
// The ban above catches a seat quantity coming out of a usage read. It cannot catch the OTHER way the P7
// defect returns, because that regression adds no code: delete one entry from `entitlement.stateMetrics`
// and `measure` falls straight through to `observed` with nothing new to match on. So this asserts the
// entry is present — in the real file, by reading it — and the broken fixture is a `stateMetrics` table
// with the seat entry removed.
func TestTheSeatMeterIsStillClassifiedAsAState(t *testing.T) {
	const gate = repoRoot + "/internal/entitlement/entitlement.go"
	b, err := os.ReadFile(gate)
	if err != nil {
		t.Fatalf("read %s: %v — this fence cannot judge the classification without it", gate, err)
	}
	if !stateMetricsPattern.MatchString(string(b)) {
		t.Fatalf("%s no longer classifies the seat meter as a STATE.\n"+
			"Without `metering.MetricSeats: true` in stateMetrics, `measure` resolves the seat allowance "+
			"through `observed` — the usage store — and nothing writes a seats usage record at membership "+
			"time. The comparison then runs against a permanent zero and every seat allowance passes, "+
			"which is the P7 defect this phase exists to undo. Nothing else would fail.", gate)
	}

	// And the fixture, so this assertion has been watched red too.
	fixture := filepath.Join(fenceFixtureRoot, "seat-from-usage", "gate.go.fixture")
	fb, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read %s: %v", fixture, err)
	}
	if stateMetricsPattern.MatchString(string(fb)) {
		t.Fatalf("%s is supposed to be the BROKEN version — a stateMetrics table with the seat entry "+
			"removed — and it still classifies the meter. The assertion above has never been observed "+
			"failing.", fixture)
	}
}
