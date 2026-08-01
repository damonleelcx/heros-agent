// external_origins_test.go is the machine-checked half of P24 task 1.8: an installable or air-gapped
// artefact references ZERO external origins, and the check runs when the package is BUILT.
//
// # Why the assertion lives here and not in the packaging script alone
//
// `deploy/scripts/package-airgapped.sh` calls the gate, and `scripts/release-cli.sh` calls the gate.
// Both of those are only exercised when somebody cuts a release — which is precisely when nobody wants
// to discover that a gate has been removed. These tests run in `make go`, beside every other test, and
// they assert three separate things that a release-time-only check cannot:
//
//  1. the gate is still WIRED into both package builds (a deleted line is the likeliest failure);
//  2. the gate is GREEN on the exact file set the air-gapped packager stages today;
//  3. the gate goes RED on an injected reporting host, in a fixture, so it is known to be connected.
//
// # Why an air-gapped package is the strictest of the three substrates
//
// P24 installs an analytics tag, a session recorder and an error reporter, and configures all three for
// the platform's own hosted deployment ONLY. A customer running Compose or Kubernetes gets absence, and
// absence is silent. An air-gapped customer gets absence and CANNOT CHECK IT: they have no egress with
// which to observe something trying to leave, so by the time anything is visible the tarball is already
// in their machine room. The claim therefore has to be produced by the run that produces the artefact.
package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gatePath is the shared checker both package builds invoke.
const gatePath = "../../deploy/scripts/check-external-origins.sh"

// stagedByPackager is the exact set the air-gapped packager copies into a package, in its own order.
// Keeping the list here rather than re-globbing is deliberate: the packager copies BY NAME so a filled
// `.env` can never be swept in by a glob, and this test would be worthless if it scanned a different
// set from the one that ships.
var stagedByPackager = []string{
	"../../deploy/docker-compose.platform.yml",
	"../../deploy/docker-compose.admin-console.yml",
	"../../deploy/images.env",
	"../../deploy/.env.platform.example",
	"../../deploy/.env.admin-console.example",
	"../../deploy/k8s",
	"../../deploy/scripts/package-airgapped.sh",
	"../../deploy/scripts/verify-package.sh",
	"../../deploy/scripts/install-airgapped.sh",
	"../../deploy/scripts/doctor.sh",
	"../../deploy/scripts/pg-backup.sh",
}

func runGate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{gatePath}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestOriginGateIsConnected proves the gate goes red, before any test relies on it being green.
//
// A green result from an unconnected gate is indistinguishable from a green result from a clean tree,
// and the second one is what everybody assumes. So the self-test runs first.
func TestOriginGateIsConnected(t *testing.T) {
	out, err := runGate(t, "--self-test")
	if err != nil {
		t.Fatalf("the origin gate's self-test failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "self-test passed") {
		t.Fatalf("the origin gate's self-test did not report passing:\n%s", out)
	}

	// And red on the thing it is for: an analytics host written into an overlay.
	dir := t.TempDir()
	leak := filepath.Join(dir, "overlay.yaml")
	if err := os.WriteFile(leak, []byte("env:\n  - name: TAG\n    value: https://www.googletagmanager.com/gtag/js\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out, err = runGate(t, dir)
	if err == nil {
		t.Fatalf("an analytics host in a staged overlay did not fail the gate:\n%s", out)
	}
	for _, want := range []string{"FAILED", "googletagmanager"} {
		if !strings.Contains(out, want) {
			t.Errorf("the failure does not name %q:\n%s", want, out)
		}
	}

	// A reporting identity with no host of its own — the value that turns "absent" into "configured".
	dsn := filepath.Join(dir, "values.yaml")
	// A SYNTHETIC id, not the real property's. It only has to match the gate's shape, and putting a
	// live measurement id in the source tree would make the one place this repository names one a test
	// fixture — which is how an id ends up copied into a manifest by somebody looking for an example.
	if err := os.WriteFile(dsn, []byte("measurementId: G-FIXTURE0000\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out, err = runGate(t, dir)
	if err == nil {
		t.Fatalf("a GA4 measurement id in a staged file did not fail the gate:\n%s", out)
	}
	if !strings.Contains(out, "reporting identities") {
		t.Errorf("the failure does not name the identity class:\n%s", out)
	}
}

// TestAirGappedPackageReferencesZeroExternalOrigins is the assertion itself: the number is ZERO.
func TestAirGappedPackageReferencesZeroExternalOrigins(t *testing.T) {
	for _, path := range stagedByPackager {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("the packager stages %s, which does not exist: %v", path, err)
		}
	}
	out, err := runGate(t, stagedByPackager...)
	if err != nil {
		t.Fatalf("the air-gapped package's staged file set references an external origin:\n%s", out)
	}
	if !strings.Contains(out, "0 external origins, 0 reporting identities") {
		t.Fatalf("the gate did not report zero:\n%s", out)
	}
}

// TestBothPackageBuildsInvokeTheGate is the wiring assertion.
//
// The likeliest way this guarantee dies is not somebody adding an origin — it is somebody removing the
// line that checks. That is a one-line diff in a 200-line shell script, in a file nobody reads outside
// a release, and nothing else in the repository would notice.
func TestBothPackageBuildsInvokeTheGate(t *testing.T) {
	for _, build := range []struct{ path, why string }{
		{"../../deploy/scripts/package-airgapped.sh", "the air-gapped package"},
		{"../../scripts/release-cli.sh", "the P20 installable package"},
	} {
		b, err := os.ReadFile(build.path)
		if err != nil {
			t.Fatalf("read %s: %v", build.path, err)
		}
		src := string(b)
		if !strings.Contains(src, "check-external-origins.sh") {
			t.Errorf("%s (%s) no longer invokes the zero-external-origin gate", build.path, build.why)
		}
		// And it must be a hard failure, not a warning. A WARN-and-continue step is how a broken
		// enterprise build shipped an OSS binary in this repository's own recent history.
		if !strings.Contains(src, "exit 1") && !strings.Contains(src, "die ") {
			t.Errorf("%s invokes the gate but has no failing path", build.path)
		}
	}
}

// TestTheGateRefusesToBeQuietlyWidened pins the ONE allowance the installable package needs.
//
// `--allow github.com` exists because an installer must reach the public forge the customer is
// downloading from, and proxying that through our own origin to keep a check quiet is refused on the
// grounds P24's design already states: it would make a third-party flow look first-party. Every other
// host is zero, and this test fails if a second allowance is added without a decision.
func TestTheGateRefusesToBeQuietlyWidened(t *testing.T) {
	b, err := os.ReadFile("../../scripts/release-cli.sh")
	if err != nil {
		t.Fatalf("read release-cli.sh: %v", err)
	}
	allowed := map[string]bool{"github.com": true, "githubusercontent.com": true}
	fields := strings.Fields(string(b))
	for i, f := range fields {
		if f != "--allow" || i+1 >= len(fields) {
			continue
		}
		host := strings.Trim(fields[i+1], `"'\`)
		if !allowed[host] {
			t.Errorf("release-cli.sh permits %q. The installable package reaches the public forge it "+
				"downloads from and nothing else; a new allowance is a decision, not a build fix.", host)
		}
	}

	// The air-gapped packager permits NOTHING. Zero is the whole assertion there.
	pkg, err := os.ReadFile("../../deploy/scripts/package-airgapped.sh")
	if err != nil {
		t.Fatalf("read package-airgapped.sh: %v", err)
	}
	if strings.Contains(string(pkg), "--allow") {
		t.Error("the air-gapped packager passes --allow. An air-gapped package's external-origin count " +
			"is zero, with no exceptions — a customer with no egress cannot audit one.")
	}
}

// TestDeploymentManifestsCarryNoReportingIdentity is P24 task 7.4.
//
// # Why this is a separate test from the origin gate
//
// The origin gate asks "does this artefact name a host outside the customer's network". This asks a
// narrower and more specific question: does any deployment manifest carry a MEASUREMENT ID, a PROJECT
// ID, a DSN, or — the one that is easiest to miss — a **discovered default**.
//
// A discovered default is the dangerous shape. `HEROS_ERROR_REPORTING_DSN: ""` in a Compose file looks
// like absence and is not: it is a slot, one `helm --set` from being filled, in a file a customer edits
// without reading. `HEROS_GA4_MEASUREMENT_ID: ${GA_ID:-G-XXXX}` is worse — it is a default nobody chose
// that would take effect the day somebody's shell happened to export the variable.
//
// So the rule is that the variable NAMES do not appear in a deployment manifest at all. The integrations
// are configured for the platform's own hosted deployment, by its own environment, and a customer's
// manifest has no reason to mention them even to say they are empty.
func TestDeploymentManifestsCarryNoReportingIdentity(t *testing.T) {
	// Every variable that turns an integration on. If one of these appears in a manifest, the manifest
	// has an opinion about an integration it should not know exists.
	switches := []string{
		"HEROS_ERROR_REPORTING_DSN",
		"HEROS_GA4_MEASUREMENT_ID",
		"HEROS_GA4_API_SECRET",
		"HEROS_CLARITY_PROJECT_ID",
		"HEROS_SOURCEMAP_UPLOAD_TOKEN",
		"SENTRY_DSN",
		"NEXT_PUBLIC_SENTRY_DSN",
	}

	roots := []string{
		"../../deploy/docker-compose.platform.yml",
		"../../deploy/docker-compose.admin-console.yml",
		"../../deploy/.env.platform.example",
		"../../deploy/.env.admin-console.example",
		"../../deploy/k8s",
	}

	scanned := 0
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
			scanned++
			body := string(b)
			for _, name := range switches {
				if strings.Contains(body, name) {
					t.Errorf("%s mentions %s. A deployment manifest has no reason to name an integration "+
						"switch even to leave it empty: an empty slot is one `--set` from being filled, in a "+
						"file a customer edits without reading. The platform's own hosted deployment "+
						"configures these from its own environment.", path, name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if scanned < 5 {
		t.Fatalf("scanned only %d manifest(s) — the assertion is vacuous", scanned)
	}

	// And the AIR-GAPPED overlay specifically, which is the substrate that cannot audit itself.
	airgapped, err := os.ReadFile("../../deploy/k8s/overlays/airgapped/kustomization.yaml")
	if err != nil {
		t.Fatalf("read the airgapped overlay: %v", err)
	}
	for _, shape := range []string{"ingest.", "sentry", "clarity", "google-analytics", "googletagmanager", "G-"} {
		if strings.Contains(string(airgapped), shape) {
			t.Errorf("the airgapped overlay references %q", shape)
		}
	}
}
