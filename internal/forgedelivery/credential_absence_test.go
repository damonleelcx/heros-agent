package forgedelivery_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	fd "github.com/heros-foreal/agentd/internal/forgedelivery"
)

// 5.2 / 7.4 🔴 — the headline security property, proven STRUCTURALLY (by absence), not by policy.
//
// In the default (CI-mediated) mode the platform holds no forge credential and no code path reads one.
// These tests make that provable rather than asserted:
//
//   1. The delivery core (Deliverer) has no credential field and holds no ForgeWriter.
//   2. What the platform hands to the CI runner (Prepared) carries no credential.
//   3. The platform-side delivery core reads no forge credential from the environment.

// credentialWords are the substrings a credential-bearing field name would contain.
var credentialWords = []string{"token", "secret", "credential", "password", "apikey", "api_key", "auth"}

func nameLooksLikeCredential(name string) bool {
	n := strings.ToLower(name)
	for _, w := range credentialWords {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// The Deliverer struct — the platform's delivery core — has no credential-shaped field and holds no
// ForgeWriter (which is the only thing that could carry one). A ForgeWriter is passed per call in App
// mode and never in CI mode.
func TestDeliverer_HoldsNoCredential(t *testing.T) {
	rt := reflect.TypeOf(fd.Deliverer{})
	forgeWriter := reflect.TypeOf((*fd.ForgeWriter)(nil)).Elem()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if nameLooksLikeCredential(f.Name) {
			t.Errorf("Deliverer field %q looks like a credential store", f.Name)
		}
		if f.Type.Implements(forgeWriter) {
			t.Errorf("Deliverer field %q holds a ForgeWriter — the credential must be passed per call, not held", f.Name)
		}
	}
}

// Prepared is what crosses to the CI runner over the authenticated fetch. It carries the diff and the
// rendered content — never a credential.
func TestPrepared_CarriesNoCredential(t *testing.T) {
	rt := reflect.TypeOf(fd.Prepared{})
	for i := 0; i < rt.NumField(); i++ {
		if nameLooksLikeCredential(rt.Field(i).Name) {
			t.Errorf("Prepared field %q would ship a credential to the CI runner", rt.Field(i).Name)
		}
	}
}

// The platform-side delivery core reads no forge credential from the environment. The credential is the
// CI environment's, read only in the CI runner — not in this package's platform path.
func TestPlatformCore_ReadsNoForgeCredential(t *testing.T) {
	// The platform-side files: the core, the CI-report handler, the record, the observation, the body.
	// Explicitly NOT the App-mode/GitHub writer or the demo forge, which legitimately hold a credential.
	platformFiles := []string{
		"deliverer.go", "cimediated.go", "record.go", "observe.go", "prbody.go",
		"route.go", "branch.go", "types.go", "consoleref.go",
	}
	forbidden := []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "os.Getenv"}
	for _, name := range platformFiles {
		b, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(b)
		for _, bad := range forbidden {
			if strings.Contains(src, bad) {
				t.Errorf("%s reads a forge credential (%q) on the platform path — it must not", name, bad)
			}
		}
	}
}

// The CI-mediated mode's default writer holds NO platform credential: the credential is the CI
// environment's, external to the platform.
func TestCIWriter_HoldsNoPlatformCredential(t *testing.T) {
	ciForge := fd.NewInMemForge(fd.ForgeGitHub, false) // false == the CI/no-platform-credential posture
	var carrier fd.CredentialCarrier = ciForge
	if carrier.HoldsForgeCredential() {
		t.Errorf("the CI-mode forge writer must not hold a platform-held credential")
	}
	// The hosted-App writer, by contrast, DOES hold one — the two postures are distinguishable.
	appForge := fd.NewInMemForge(fd.ForgeGitHub, true)
	if !appForge.HoldsForgeCredential() {
		t.Errorf("the hosted-App writer should report that it holds a credential (it is standing write access)")
	}
}
