package cli

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// registry_test.go is the drift guard behind P23 Decision 14.
//
// The registry is only trustworthy if it cannot disagree with the binary. Three ways it could:
//
//	1. a command is dispatched but not registered  → the reference silently omits it
//	2. a command is registered but not dispatched  → the reference documents a fiction
//	3. an exit code's documented meaning drifts    → a customer's pipeline branches on a lie
//
// Each is asserted below. The first two are the ones that actually happen, because adding a subcommand
// is a normal Tuesday and remembering the docs is not.

// dispatchedCommands parses this package's own dispatch switch for the command literals it handles.
//
// Reading the source is deliberate. The alternative — invoking every plausible name and checking for
// "unknown command" — cannot enumerate, so it can catch (2) and never (1), and (1) is the failure that
// accumulates.
func dispatchedCommands(t *testing.T) map[string]bool {
	t.Helper()
	source, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("read app.go: %v", err)
	}
	text := string(source)

	// The two switches in Main: the early one (help/version, before flag parsing) and the dispatch one.
	found := map[string]bool{}
	for _, m := range regexp.MustCompile(`case ("(?:[a-z-]+)"(?:, "(?:[a-z-]+)")*):`).FindAllStringSubmatch(text, -1) {
		for _, lit := range strings.Split(m[1], ", ") {
			name := strings.Trim(lit, `"`)
			// `-h` and `--help` are aliases of `help`; the registry documents the canonical name only,
			// because a reference with three entries for one command is a reference nobody trusts.
			if name == "-h" || name == "--help" {
				continue
			}
			found[name] = true
		}
	}
	if len(found) == 0 {
		t.Fatal("parsed no dispatch cases from app.go — this guard has become a no-op, which is worse than absent")
	}
	return found
}

func TestRegistryMatchesDispatch(t *testing.T) {
	dispatched := dispatchedCommands(t)
	registered := map[string]bool{}
	for _, c := range Commands() {
		registered[c.Name] = true
	}

	var missing, extra []string
	for name := range dispatched {
		if !registered[name] {
			missing = append(missing, name)
		}
	}
	for name := range registered {
		if !dispatched[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) > 0 {
		t.Errorf("dispatched but NOT in the registry: %v\n"+
			"Add each to registry.go. Until you do, the generated CLI reference omits it — and a command "+
			"nobody can look up is a command nobody uses correctly.", missing)
	}
	if len(extra) > 0 {
		t.Errorf("in the registry but NOT dispatched: %v\n"+
			"The reference would document a command this binary does not have.", extra)
	}
}

func TestRegistryFlagsResolveToTheCatalogue(t *testing.T) {
	for _, c := range Commands() {
		for _, name := range c.Flags {
			if _, ok := FlagSpec(name); !ok {
				t.Errorf("command %q lists flag %q, which is not in the flag catalogue", c.Name, name)
			}
		}
	}
}

func TestEveryCommandCarriesARunnableExampleAndASuccessCriterion(t *testing.T) {
	// P23 task 6.5: every entry gets a runnable invocation, what success looks like, and the success exit
	// code. A reference entry that names a command and stops is a list, not documentation.
	for _, c := range Commands() {
		if !strings.HasPrefix(c.Example, "heros ") {
			t.Errorf("command %q has no runnable invocation (got %q)", c.Name, c.Example)
		}
		if !strings.Contains(c.Example, c.Name) {
			t.Errorf("command %q's example does not invoke it: %q", c.Name, c.Example)
		}
		if c.Success == "" {
			t.Errorf("command %q does not say what success looks like", c.Name)
		}
		if c.SuccessExit != ExitOK {
			t.Errorf("command %q claims success exit %d; success is %d", c.Name, c.SuccessExit, ExitOK)
		}
	}
}

func TestNetworkCommandsDocumentTheirUnavailableOutcome(t *testing.T) {
	// P23 task 6.3. A reader should meet "unavailable in this build" in the reference, not at the terminal
	// — and the sentence must be the one the binary actually prints, so it is greppable.
	for _, c := range Commands() {
		if c.Avail == AvailOffline {
			if c.Unavailable != "" {
				t.Errorf("offline command %q declares an unavailable outcome it can never produce", c.Name)
			}
			continue
		}
		if c.Unavailable == "" {
			t.Errorf("command %q needs the network but does not document its unavailable-in-this-build outcome", c.Name)
		}
	}

	// And the documented sentence must match what Main emits with no NetCommands wired in.
	for _, c := range Commands() {
		if c.Unavailable == "" {
			continue
		}
		var out, errBuf strings.Builder
		s := Streams{Out: &out, Err: &errBuf}
		code := Main([]string{c.Name}, s, func(string) (string, bool) { return "", false }, nil)
		if code != ExitOperational {
			t.Errorf("%s with no net commands exited %d, want %d", c.Name, code, ExitOperational)
		}
		if !strings.Contains(errBuf.String(), c.Unavailable) {
			t.Errorf("%s printed %q, which does not contain the documented outcome %q",
				c.Name, errBuf.String(), c.Unavailable)
		}
	}
}

func TestExitCodesMatchConstants(t *testing.T) {
	want := map[int]string{
		ExitOK:          "ok",
		ExitGateFailed:  "configured-gate-failed",
		ExitOperational: "operational-error",
		ExitInvalidCfg:  "invalid-config",
	}
	got := map[int]string{}
	for _, e := range ExitCodes() {
		got[e.Code] = e.Name
		if e.Remedy == "" {
			t.Errorf("exit code %d documents no remedy — a code without one is a number, not a contract", e.Code)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("documented %d exit codes, the contract has %d", len(got), len(want))
	}
	for code, name := range want {
		if got[code] != name {
			t.Errorf("exit code %d is documented as %q, the constant means %q", code, got[code], name)
		}
	}
}
