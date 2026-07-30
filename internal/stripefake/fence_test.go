package stripefake_test

import (
	"os/exec"
	"strings"
	"testing"
)

// fence_test.go is the "no mock in production" gate.
//
// A fake payment processor is exactly as dangerous as it is useful. It is useful because the P21 tests
// and the P21 demo need a Stripe that can be driven into an outage, an ambiguous failure and an
// idempotent replay on demand. It is dangerous because a fake reachable from a shipping code path is a
// deployment that reports charges nobody made — and that failure is silent, because every log line says
// success.
//
// The rule is therefore mechanical rather than cultural: only TEST binaries and `cmd/` demos may import
// this package. Nothing under `internal/` that ships may. A convention would be obeyed until the first
// hurry; `go list` is not.

// allowedPrefixes are the import paths permitted to depend on the fake. `cmd/` is where demos live, and
// a demo is a program somebody runs deliberately, not a library something links by accident.
var allowedPrefixes = []string{
	"github.com/heros-foreal/agentd/cmd/",
	"github.com/heros-foreal/agentd/internal/stripefake",
}

func TestOnlyTestsAndDemosImportTheFakeStripe(t *testing.T) {
	// `go list ./...` is relative to the working directory, which for a test is its own package. The
	// module root is asked for explicitly so the fence sees the whole module rather than one directory.
	rootOut, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	// `.Deps` rather than `.Imports`: the TRANSITIVE graph is the question. A shipping package that
	// reaches the fake five hops down is exactly as broken as one that imports it directly, and it is
	// the case a source grep — and a direct-import check — both miss. Test-only imports are deliberately
	// absent from `.Deps`, which is what makes "tests may" true without an exemption.
	cmd := exec.Command("go", "list",
		"-f", `{{.ImportPath}}{{range .Deps}} {{.}}{{end}}`, "./...")
	cmd.Dir = strings.TrimSpace(string(rootOut))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}

	const fake = "github.com/heros-foreal/agentd/internal/stripefake"
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 20 {
		t.Fatalf("go list returned %d packages — the fence is not seeing the module, so it would pass vacuously", len(lines))
	}

	offenders := 0
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		importer, deps := fields[0], fields[1:]
		reaches := false
		for _, imp := range deps {
			if imp == fake {
				reaches = true
			}
		}
		if !reaches {
			continue
		}
		allowed := false
		for _, p := range allowedPrefixes {
			if strings.HasPrefix(importer, p) {
				allowed = true
			}
		}
		if !allowed {
			offenders++
			t.Errorf("%s imports the FAKE Stripe. A fake reachable from a shipping code path is a "+
				"deployment that reports charges nobody made, and the failure is silent because every "+
				"log line says success. Use billing.StubProvider for a hermetic default, or the real "+
				"provider in test mode", importer)
		}
	}
	t.Logf("checked %d packages; %d import the fake outside tests and demos", len(lines), offenders)
}
