package launch

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// capabilitydocs_test.go pins deploy/README.md's capability table to the capability list the code
// actually registers.
//
// # Why this exists
//
// That table is the answer to "what will I get if I install this", read before anyone can run the
// thing and check. It drifted badly and silently: it listed P4 eval board, P4.5 scorecard, P5 graph
// editor, P5.5 proposals, P3.5 pattern graph and P12 delivery as "registered, not mounted" long after
// all six were mounted for real, said P7 billing's only store was in-memory after the ledger became
// durable, and never mentioned P1 source discovery, the workflow IR, the verdict ingest, proposal
// compile or proposal generation at all.
//
// Every one of those is a documentation bug with a product cost: the table talks an operator OUT of
// capabilities they have, and leaves them unable to find ones they were never told about. Nothing
// failed, no test went red, and the boot log had been printing the truth the whole time.
//
// # What it checks, and what it deliberately does NOT
//
// It checks the SET of capability names, in both directions: every name the code can register appears
// in the table, and every capability-shaped name in the table is one the code registers.
//
// It does NOT check served-vs-not-mounted. That depends on a live deployment — a database, a plan
// catalog, a model catalog, a console origin — and asserting it from a unit test would mean either
// standing up Postgres here or hard-coding a second copy of the mount conditions, which is a second
// source of truth that can disagree with the first. The boot log is the authority for STATE; this
// fence is the authority for COVERAGE. Stating that limit is the point: a fence mistaken for a
// stronger one is how a green suite comes to mean less than people think.

const (
	capabilitiesSrc = "capabilities.go"
	deployREADME    = "../../deploy/README.md"
)

// reRegistered finds every capability name the code registers, from the two calls that record one.
//
// ⚠️ No closing quote after the capture. Several names are followed by a parenthetical in the SAME
// string — `served("p10_studio_matrix (models + render; …)")` — and the first draft of this pattern
// required the string to end at the id, so it silently matched only the eight names that had no
// suffix. It still passed, because those eight were all documented. A fence that quietly narrows its
// own input is the failure mode `minRegistered` below exists to catch.
var reRegistered = regexp.MustCompile(`\b(?:served|absent)\("([a-z0-9_]+)`)

// minRegistered is a floor on how many capabilities the pattern must find. It is not the exact count —
// that would fail on every new capability, which is a fence nobody keeps — but it is far enough above
// a broken pattern's yield to catch one.
const minRegistered = 15

// reDocumented finds the capability each TABLE ROW is about: a line that opens a markdown row with the
// id in backticks.
//
// ⚠️ Anchored to the row, not to the file. Scanning the whole README for a backticked id was the first
// draft, and it could not tell a row from a mention: deleting the `p45_scorecard` row left the fence
// green, because the `p25_run_monitor` row's prose names `p45_scorecard` while explaining what a
// linked run CAN show. The claim under test is "this capability has a row an operator can read", and a
// cross-reference in someone else's row is not that.
var reDocumented = regexp.MustCompile("(?m)^\\| `(p[0-9]+[a-z0-9_]*)`")

func registeredCapabilities(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(capabilitiesSrc)
	if err != nil {
		t.Fatalf("read %s: %v", capabilitiesSrc, err)
	}
	out := map[string]bool{}
	for _, m := range reRegistered.FindAllStringSubmatch(string(b), -1) {
		out[m[1]] = true
	}
	if len(out) < minRegistered {
		t.Fatalf("found only %d registered capabilities, want at least %d — the pattern has stopped "+
			"matching most of them, so everything below is passing over a set too small to guard. "+
			"Check reRegistered against the `served(`/`absent(` calls in %s.", len(out), minRegistered, capabilitiesSrc)
	}
	return out
}

func documentedCapabilities(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(deployREADME)
	if err != nil {
		t.Fatalf("read %s: %v", deployREADME, err)
	}
	out := map[string]bool{}
	for _, m := range reDocumented.FindAllStringSubmatch(string(b), -1) {
		out[m[1]] = true
	}
	return out
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestEveryCapabilityIsInTheDeployTable is the direction that bit: a capability shipped, and the table
// that tells an operator what they get never learned about it.
func TestEveryCapabilityIsInTheDeployTable(t *testing.T) {
	documented := documentedCapabilities(t)
	var missing []string
	for _, name := range sorted(registeredCapabilities(t)) {
		if !documented[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("deploy/README.md's capability table does not mention %d capability/ies this deployment "+
			"registers: %s.\n"+
			"That table is what somebody reads BEFORE they can install and look. A capability missing from "+
			"it is one they will never go looking for — and the boot log has been printing it the whole "+
			"time, which is why nothing caught this.\n"+
			"Add a row using the id in backticks, and take the state from "+
			"`docker compose logs agentd | grep -E 'served|not mounted'` rather than from memory.",
			len(missing), strings.Join(missing, ", "))
	}
}

// TestTheDeployTableInventsNoCapability is the other direction, and it is the one that had the table
// telling operators their scorecard was unavailable for weeks after it shipped. A row for something
// the code no longer registers is worse than a missing row: it reads as authoritative.
func TestTheDeployTableInventsNoCapability(t *testing.T) {
	registered := registeredCapabilities(t)
	var unknown []string
	for _, name := range sorted(documentedCapabilities(t)) {
		if !registered[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		t.Errorf("deploy/README.md names %d capability/ies this deployment does not register: %s.\n"+
			"Either the name is stale (renamed or removed) or it is a typo. Both render as a confident "+
			"claim about a surface that does not exist, which an operator cannot disprove without reading "+
			"this source file.", len(unknown), strings.Join(unknown, ", "))
	}
}

// ── The rows that are ROUTES rather than capabilities ───────────────────────────────────────────────
//
// Three rows in that table name paths instead of capability ids: the health probes, the four
// build-property reads, and the billing webhook. The fence above cannot see them — they never pass
// through `served()`/`absent()` — so they were the one part of the table nothing checked, which is
// exactly the state the rest of it was in before it drifted.
//
// What makes them checkable is that each row states a STRUCTURAL claim, not a runtime one:
//
//   **served** … always   → registered by `api.New`, so it needs no database, no catalog and no mount
//   **not registered**    → NOT registered by `api.New`; it appears only where a deployment mounts it
//
// Both are properties of one function body, so both can be read off the source. That is deliberately
// narrower than "this route answers 200": whether a handler works is the job of the tests that call
// it, and re-asserting it here would be a second, weaker copy of them.

const serverSrc = "../api/server.go"

// reRouteRow matches a table row whose first cell is backticked absolute paths, capturing the whole
// row so the state cell can be read too.
var reRouteRow = regexp.MustCompile("(?m)^\\| ((?:`(?:[A-Z]+ )?/[^`]+`(?:, )?)+) \\| ([^|]+) \\|")

// reRoutePath pulls each backticked path out of that first cell.
// The optional METHOD prefix matters: the webhook row is written `POST /billing/webhook`, and the
// first draft of these two patterns silently skipped it — so the ONE row that claims a path is not
// exposed by default was the one row this fence did not read. `checkedRouteRows` is the floor that
// makes that kind of narrowing fail instead of pass.
var reRoutePath = regexp.MustCompile("`(?:[A-Z]+ )?(/[^`]+)`")

// checkedRouteRows is how many path claims must be asserted: SEVEN, the number in the table today
// (two health probes, four build-property reads, one webhook). It is set to the exact current count
// rather than a comfortable margin, because the margin is where a narrowing pattern hides — the first
// draft used six, dropped the webhook, and passed. Removing a documented route legitimately means
// lowering this by hand, which is a deliberate edit rather than a silent loss of coverage.
const checkedRouteRows = 7

// newBody returns the body of api.New — the registrations that happen unconditionally.
func newBody(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(serverSrc)
	if err != nil {
		t.Fatalf("read %s: %v", serverSrc, err)
	}
	src := string(b)
	start := strings.Index(src, "func New(db *sql.DB, cfg config.Config) *Server {")
	if start < 0 {
		t.Fatal("could not find api.New — this fence reads its body and cannot run without it")
	}
	// Up to the line that stops registering and starts composing the handler.
	end := strings.Index(src[start:], "var h http.Handler = s.Mux")
	if end < 0 {
		t.Fatal("could not find the end of api.New's registration block")
	}
	return src[start : start+end]
}

// TestRouteRowsMatchWhatNewRegisters holds the three non-capability rows to their claims.
func TestRouteRowsMatchWhatNewRegisters(t *testing.T) {
	b, err := os.ReadFile(deployREADME)
	if err != nil {
		t.Fatalf("read %s: %v", deployREADME, err)
	}
	rows := reRouteRow.FindAllStringSubmatch(string(b), -1)
	if len(rows) == 0 {
		t.Fatal("no route rows found in the capability table — this fence is passing over an empty set")
	}
	body := newBody(t)

	checked := 0
	for _, row := range rows {
		paths := reRoutePath.FindAllStringSubmatch(row[1], -1)
		state := row[2]
		for _, p := range paths {
			path := p[1]
			registered := strings.Contains(body, `"GET `+path+`"`) || strings.Contains(body, `"POST `+path+`"`)
			switch {
			case strings.Contains(state, "**served**"):
				checked++
				if !registered {
					t.Errorf("the table says %s is **served** always, but api.New does not register it.\n"+
						"Either it moved behind a mount — in which case it is no longer unconditional and "+
						"the row is now false for a deployment with no database — or it was renamed and the "+
						"row still names the old path.", path)
				}
			case strings.Contains(state, "**not registered**"):
				checked++
				if registered {
					t.Errorf("the table says %s is **not registered** on a fresh install, but api.New "+
						"registers it unconditionally. That row is what tells an operator this is the one "+
						"path not exposed by default; it must not be the one path that is.", path)
				}
			}
		}
	}
	if checked < checkedRouteRows {
		t.Fatalf("asserted only %d path claim(s), want at least %d — the row or path pattern has stopped "+
			"matching part of the table, so this fence is passing over a smaller set than it reports.",
			checked, checkedRouteRows)
	}
}
