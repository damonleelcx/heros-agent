package conversation

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// intent_test.go is §6.15's GO half.
//
// # Why both halves exist rather than one
//
// `web/console/tests/conversation.test.mjs` compares the two tables and is the definitive set-equality
// fence. It runs under `make console-test`, which needs npm and a production console build.
//
// A Go developer adding an intent runs `make go`. If the only fence lived on the console side, they would
// get a green build for a change that makes a surface unreachable by sentence — and would find out when
// somebody else's CI job failed, or, far more likely, when a customer met a polite refusal about a surface
// that plainly exists.
//
// So this reads `web/console/src/lib/routes.ts` as TEXT and asserts the same equality. Reading the file
// rather than importing anything is what makes it possible at all from Go, and it has the better property
// besides: the list that is PINNED is the one committed, not one a bundler rewrote.

// consoleList reads one exported string array out of routes.ts.
func consoleList(t *testing.T, name string) []string {
	t.Helper()
	path := filepath.Join("..", "..", "web", "console", "src", "lib", "routes.ts")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	block := regexp.MustCompile(`export const ` + name + `: readonly string\[\] = \[([^\]]*)\]`).
		FindStringSubmatch(string(src))
	if block == nil {
		t.Fatalf("routes.ts declares no %s — this fence's scan is broken, not the code", name)
	}
	var out []string
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(block[1], -1) {
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

// TestIntentSetEqualsTheWorkingSurfaceSet is D9, from the Go side.
//
// 🔴 The drift this catches runs in ONE direction: a surface ships, nobody adds its intent, and the
// conversation quietly cannot reach it. Nothing fails — the user asks and gets a REFUSAL, which is
// well-formed, polite, and indistinguishable from the surface not existing. That is the shape P26 found
// after fourteen phases of operator-console drift, with nothing going red the whole time.
func TestIntentSetEqualsTheWorkingSurfaceSet(t *testing.T) {
	fromGo := RouteBackedSurfaces()
	fromConsole := consoleList(t, "WORKING_SURFACES")

	if len(fromGo) != len(fromConsole) {
		t.Fatalf("the intent table names %d console routes and the console declares %d working surfaces\n"+
			"  intents:  %s\n  surfaces: %s",
			len(fromGo), len(fromConsole), strings.Join(fromGo, ", "), strings.Join(fromConsole, ", "))
	}
	for i := range fromGo {
		if fromGo[i] != fromConsole[i] {
			t.Errorf("intent surface %q has no matching working surface (%q is at that position)",
				fromGo[i], fromConsole[i])
		}
	}
}

// TestEveryOutOfScopeRedirectionNamesARealSurface stops FR26's refusals pointing at a 404.
//
// A refusal that says "that is done at /app/billing" is only better than a bare abstention if
// `/app/billing` exists. A redirection to a route nobody serves is worse than no redirection: it sends a
// person somewhere and then fails them there.
func TestEveryOutOfScopeRedirectionNamesARealSurface(t *testing.T) {
	declared := map[string]bool{}
	for _, s := range consoleList(t, "OUT_OF_SCOPE_SURFACES") {
		declared[s] = true
	}
	for _, surface := range OutOfScopeSurfaces() {
		if !declared[surface] {
			t.Errorf("the router redirects to %q, which the console does not declare as an out-of-scope "+
				"surface. A refusal that names a route nobody serves sends a person to a 404.", surface)
		}
	}
}

// TestEveryIntentIsBackedByExactlyOneThing keeps the two backings honest.
//
// A capability-backed intent with a `/app/…` surface would be silently absent from the set-equality fence
// above — the fence reads route-backed intents only — so an intent could be added, be unreachable, and
// pass. This is the check that closes that.
func TestEveryIntentIsBackedByExactlyOneThing(t *testing.T) {
	if len(Intents()) != 14 {
		t.Fatalf("the intent set has %d members; PRD §6.7 declares fourteen", len(Intents()))
	}
	for _, spec := range Intents() {
		if spec.Surface == "" {
			t.Errorf("%s names no surface", spec.Intent)
		}
		if spec.Question == "" {
			t.Errorf("%s carries no question; the refusal renders this list, so a missing one is a "+
				"boundary the user is not shown", spec.Intent)
		}
		switch spec.Backing {
		case BackedByRoute:
			if !strings.HasPrefix(spec.Surface, "/app/") {
				t.Errorf("%s is route-backed and names %q, which is not a console route", spec.Intent, spec.Surface)
			}
			if spec.Capability != "" {
				t.Errorf("%s is route-backed and also names a capability (%q)", spec.Intent, spec.Capability)
			}
		case BackedByCapability:
			if strings.HasPrefix(spec.Surface, "/app/") {
				t.Errorf("%s is capability-backed and names a console route (%q). It would be invisible "+
					"to the set-equality fence, so it could be unreachable and still pass.",
					spec.Intent, spec.Surface)
			}
			if spec.Capability == "" {
				t.Errorf("%s is capability-backed and names no capability", spec.Intent)
			}
		default:
			t.Errorf("%s has backing %q, which is neither", spec.Intent, spec.Backing)
		}
	}
	// The capability-backed intents, by name. Written out so that adding one — or REMOVING one — is a
	// decision somebody records rather than one they inherit.
	//
	// 🔴 `surface-assessment` was here until P33 shipped `/app/assess`. Its removal is the intended
	// direction of travel for this list: a capability with nowhere to render is a promise, and the day
	// it gets a surface it becomes route-backed and the set-equality fence starts guarding it. A list
	// that only ever grows would be a list of promises nobody retired.
	want := []string{"autonomous-improvement-run"}
	got := Capabilities()
	if len(got) != len(want) {
		t.Fatalf("the intent table reaches %d capabilities: %v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("capability[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCanDoIsGeneratedFromTheTable is §7.7's mechanism, asserted rather than assumed.
//
// A hand-written list of fourteen sentences beside a table of fourteen intents is two lists that disagree
// within a quarter — and the copy is the one that is wrong and the one the user reads.
func TestCanDoIsGeneratedFromTheTable(t *testing.T) {
	list := CanDo()
	if len(list) != len(Intents()) {
		t.Fatalf("CanDo() lists %d things and the intent set has %d", len(list), len(Intents()))
	}
	for i, spec := range Intents() {
		if list[i] != spec.Question {
			t.Errorf("CanDo()[%d] = %q, want the intent table's own %q", i, list[i], spec.Question)
		}
	}
}
