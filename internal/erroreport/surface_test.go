package erroreport_test

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/erroreport"
)

// surface_test.go asserts that "the closed surface enum" is closed across BOTH languages.
//
// The enum has two halves — the browser surfaces in `web/design-system/third-party-policy.ts` and the
// server surfaces in `internal/erroreport/surface.go` — and "closed" is only a real property if nothing
// falls between them. Two closed sets that overlap, or a call site naming an id that is in neither, would
// each produce exactly the outcome the enum exists to prevent: a `surface` field that is not from a
// closed set, discovered by reading an inbox.

var tsSurfaceLine = regexp.MustCompile(`^\s*"([a-z0-9_.-]+)",\s*$`)

func browserSurfaces(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../web/design-system/third-party-policy.ts")
	if err != nil {
		t.Fatalf("read the shared artefact: %v", err)
	}
	src := string(raw)
	start := strings.Index(src, "export const SURFACES = [")
	if start < 0 {
		t.Fatal("the shared artefact no longer declares SURFACES — the browser half of the enum is missing")
	}
	end := strings.Index(src[start:], "] as const;")
	if end < 0 {
		t.Fatal("SURFACES is not terminated — the parse would silently read the rest of the file")
	}
	var out []string
	for _, line := range strings.Split(src[start:start+end], "\n") {
		if m := tsSurfaceLine.FindStringSubmatch(line); m != nil {
			out = append(out, m[1])
		}
	}
	if len(out) < 30 {
		t.Fatalf("parsed only %d browser surfaces — the parse is wrong, and a wrong parse makes this "+
			"assertion vacuous rather than failing", len(out))
	}
	return out
}

func TestTheSurfaceEnumIsClosedAcrossBothHalves(t *testing.T) {
	browser := browserSurfaces(t)
	server := erroreport.Surfaces

	seen := map[string]string{}
	for _, s := range browser {
		if where, dup := seen[s]; dup {
			t.Errorf("surface %q is declared twice (%s and browser)", s, where)
		}
		seen[s] = "browser"
	}
	for _, s := range server {
		if where, dup := seen[s]; dup {
			t.Errorf("surface %q is declared in both halves (%s and server) — an id must resolve to one place", s, where)
		}
		seen[s] = "server"
		if !erroreport.ValidServerSurface(s) {
			t.Errorf("server surface %q is in the list but fails ValidServerSurface", s)
		}
	}

	// A browser surface must never validate as a server one, or the Go boundary would accept an id it
	// has no business stamping on a server event.
	for _, s := range browser {
		if erroreport.ValidServerSurface(s) {
			t.Errorf("browser surface %q is accepted by the SERVER half", s)
		}
	}

	// And neither half may contain something path-shaped.
	for s := range seen {
		if strings.Contains(s, "/") {
			t.Errorf("surface %q contains a path segment — a surface id is never a URL", s)
		}
	}
}

func TestASurfaceOutsideTheEnumDoesNotReachTheWire(t *testing.T) {
	ev := erroreport.Event{Type: "*errors.errorString", Surface: "/app/variants/var-7f31c9/scorecard"}
	if got := ev.Wire()["surface"]; got != "unknown" {
		t.Errorf("surface = %v; a path-shaped id must be replaced by \"unknown\", not passed through", got)
	}
	ev.Surface = "platform.api"
	if got := ev.Wire()["surface"]; got != "platform.api" {
		t.Errorf("surface = %v; a valid id must survive, or the replacement rule is indistinguishable "+
			"from dropping the field entirely", got)
	}
}

// TestNoIntegrationReachesTheCLI is the P24 half of the CLI's offline guarantee.
//
// The CLI runs on a CUSTOMER'S MACHINE. An error reporter there would transmit from inside their
// network, about their runs, with their machine as the source — which is a different product from the
// one they installed. The existing `TestCLIPackageLinksNoNetworkStack` already makes this structurally
// impossible by banning `net/http` across the whole transitive graph; this states the P24-specific half
// so that the reason is findable from this package rather than only from a test about network stacks.
func TestNoIntegrationReachesTheCLI(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	cmd := exec.Command("go", "list", "-deps", "./internal/cli")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps ./internal/cli: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "github.com/heros-foreal/agentd/internal/erroreport" {
			t.Fatal("internal/cli links internal/erroreport. The CLI runs on a customer's machine; a " +
				"crash reporter there transmits from inside their network about their runs. No " +
				"integration reaches the CLI, and that is a property of the BUILD, not of everyone " +
				"remembering.")
		}
	}
}
