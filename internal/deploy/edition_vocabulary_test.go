// edition_vocabulary_test.go asserts that every substrate labels itself with a name the code accepts.
//
// # The defect this exists to stop recurring
//
// `HEROS_EDITION` is read in exactly one place in Go — `erroreport.FromEnv` — and it is validated
// against a CLOSED SET. Everywhere else in the tree it is a free string that lands on an error report
// and on an analytics event as the dimension you group by.
//
// For the whole of P24 the two halves disagreed and nothing said so. The manifests carried a COMMERCIAL
// vocabulary (`open-core`, `managed`, `enterprise`) and the code enforced a DEPLOYMENT-SHAPE one
// (`hosted`, `compose`, `kubernetes`, `airgapped`, `dev`). The sets are disjoint, so **every substrate
// this repository ships logged `error_reporting.edition.unrecognised` at boot and reported its edition
// as `unknown`** — the platform's own production deployment included, until 2026-08-04.
//
// It is the quietest possible failure. Nothing crashes, nothing 500s, `/readyz` still says `configured`,
// and the reports arrive. They simply all arrive under one label, so the first question anyone asks of
// the data — "is this our hosted deployment or a customer's?" — cannot be answered, and the WARN that
// says so is one line in a boot log nobody reads twice.
//
// # Why a test and not a comment on the constant
//
// The disagreement was already documented, in the sense that both vocabularies were written down: the
// closed set in `deployment.go`, the commercial names in `deploy/AWS.md`'s variable table. Two correct
// documents that describe different things is exactly how this survived review. A gate that reads the
// manifests and compares them to the enum is the only form of the statement that cannot drift.
package deploy

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/erroreport"
)

// editionAssignment matches the two spellings the manifests use, and only those:
//
//	k8s      - { name: HEROS_EDITION, value: "kubernetes" }
//	compose  HEROS_EDITION: ${HEROS_EDITION:-compose}
//
// A `${VAR}` reference with no default is deliberately NOT matched: it names no value, so there is
// nothing for this gate to judge. A `${VAR:-default}` IS matched on its default, because a default is a
// value somebody will ship without ever setting the variable.
var editionAssignment = regexp.MustCompile(
	`HEROS_EDITION(?:["']?\s*[,:]\s*value\s*:\s*|\s*:\s*)["']?(?:\$\{HEROS_EDITION:-)?([a-zA-Z0-9_.-]+)\}?["']?`,
)

func TestEveryManifestEditionIsOneTheCodeAccepts(t *testing.T) {
	roots := []string{
		"../../deploy/docker-compose.platform.yml",
		"../../deploy/docker-compose.admin-console.yml",
		"../../deploy/k8s",
	}

	// The closed set is read from the package that enforces it rather than restated here. A second copy
	// of the list is a second thing to update, and the update that gets missed is this one.
	known := map[string]bool{}
	for _, e := range erroreport.Editions {
		known[e] = true
	}

	found := 0
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			b, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, m := range editionAssignment.FindAllStringSubmatch(string(b), -1) {
				value := m[1]
				// A bare `${HEROS_EDITION}` passthrough resolves at runtime and names nothing here.
				if strings.HasPrefix(value, "$") {
					continue
				}
				found++
				if !known[value] {
					t.Errorf("%s sets HEROS_EDITION=%q, which internal/erroreport does not accept "+
						"(known: %v).\n\nThis does not fail loudly at runtime — it logs "+
						"error_reporting.edition.unrecognised once at boot and then reports every error "+
						"and every analytics event from this substrate as edition \"unknown\". The label "+
						"is the dimension you group by, so getting it wrong does not lose the data, it "+
						"merges this substrate's data with everyone else's.\n\nHEROS_EDITION is a "+
						"deployment SHAPE, not a commercial tier: open-core/managed/enterprise are not "+
						"values it can carry.", path, value, erroreport.Editions)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	// 🔴 Without this the gate passes on a tree where the variable was renamed or dropped everywhere —
	// which is the same green as a tree where every value is correct, and means the opposite.
	if found < 4 {
		t.Fatalf("only %d HEROS_EDITION assignment(s) found across %v. The base carries one per "+
			"deployment and Compose carries its defaults; this few means the matcher stopped matching "+
			"or the variable moved, and a gate that scans nothing reports the same green as a gate that "+
			"scans everything.", found, roots)
	}
}

// TestTheEditionGateIsConnected demonstrates the gate RED, so a green run is known to mean something.
func TestTheEditionGateIsConnected(t *testing.T) {
	for _, bad := range []string{"managed", "open-core", "enterprise", ""} {
		if len(bad) > 0 {
			for _, e := range erroreport.Editions {
				if e == bad {
					t.Fatalf("%q is in erroreport.Editions, so the commercial vocabulary is no longer "+
						"refused and this gate has stopped asserting what it claims", bad)
				}
			}
		}
	}

	// And the matcher itself must find the two real spellings, or the walk above proves nothing.
	for _, sample := range []struct{ text, want string }{
		{`            - { name: HEROS_EDITION, value: "kubernetes" }`, "kubernetes"},
		{`      HEROS_EDITION: ${HEROS_EDITION:-compose}`, "compose"},
		{`                  - { name: HEROS_EDITION, value: "airgapped" }`, "airgapped"},
	} {
		m := editionAssignment.FindStringSubmatch(sample.text)
		if m == nil {
			t.Fatalf("the matcher does not recognise %q — the manifest walk would skip it silently", sample.text)
		}
		if m[1] != sample.want {
			t.Errorf("matcher read %q from %q, want %q", m[1], sample.text, sample.want)
		}
	}
}
