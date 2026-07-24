package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// drift_test.go is P9 task 6.3: renaming a Go view field must FAIL THE BUILD rather than producing a
// blank cell.
//
// # Why this test is written the way it is
//
// The gate under test is `go run ./cmd/consoletypes -check`, and the only honest way to know it is
// connected is to make it go red on purpose. So the test copies the checked-in artifact aside,
// perturbs it exactly as a drifted read model would (one field renamed), asserts the gate exits
// non-zero and says which artifact is stale, and restores it.
//
// Perturbing the ARTIFACT rather than the Go struct is deliberate: they are two ends of the same
// comparison, and a test that edited a source file in the repository under test would leave the tree
// broken if it were interrupted. This side is restorable in a `defer`.
//
// A gate nobody has ever seen fail is a gate nobody knows is wired up.

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// cmd/consoletypes -> repository root
	return filepath.Dir(filepath.Dir(dir))
}

func TestDriftGate_PassesOnTheCurrentTree(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("go", "run", "./cmd/consoletypes", "-check", "-root", root)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the drift gate fails on an unmodified tree — run `make console-types`:\n%s", out)
	}
	if !strings.Contains(string(out), "artifacts are current") {
		t.Errorf("unexpected gate output: %s", out)
	}
}

func TestDriftGate_GoesRedWhenAViewFieldIsRenamed(t *testing.T) {
	root := repoRoot(t)
	artifact := filepath.Join(root, "web", "console", "src", "lib", "types.generated.ts")

	original, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("reading the generated contract: %v", err)
	}
	t.Cleanup(func() {
		if err := os.WriteFile(artifact, original, 0o644); err != nil {
			t.Fatalf("restoring %s: %v", artifact, err)
		}
	})

	// The exact drift this gate exists for: a field the browser reads is no longer the field the
	// server sends. Without the gate the console compiles, renders, and shows an em-dash.
	drifted := strings.Replace(string(original), "config_hash_display", "config_hash_short", 1)
	if drifted == string(original) {
		t.Fatal("the perturbation did not apply — the field this test perturbs no longer exists")
	}
	if err := os.WriteFile(artifact, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "run", "./cmd/consoletypes", "-check", "-root", root)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the drift gate PASSED on a drifted contract — it is not connected:\n%s", out)
	}
	if !strings.Contains(string(out), "out of date") {
		t.Errorf("the gate failed without saying the artifact is stale:\n%s", out)
	}
	if !strings.Contains(string(out), "make console-types") {
		t.Errorf("the gate failed without naming the remedy:\n%s", out)
	}
}

func TestGenerator_RefusesRatherThanEmittingAny(t *testing.T) {
	// The generator must refuse a construct it cannot map, rather than emitting `any`. An `any` in a
	// generated contract is a contract that has quietly stopped being one: the build stays green, the
	// type-checker stops helping, and nobody finds out until the blank cell.
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "consoletypes", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "an interface field has no static shape") {
		t.Error("the generator no longer refuses interface fields")
	}
	generated, err := os.ReadFile(filepath.Join(repoRoot(t), "web", "console", "src", "lib", "types.generated.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(generated), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if strings.Contains(line, ": any") || strings.Contains(line, "unknown;") {
			t.Errorf("the generated contract contains an untyped field: %s", strings.TrimSpace(line))
		}
	}
}
