package erroreport_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// doc_test.go is the gate that makes "the published contract renders from the code" a fact rather than
// a sentence (P24 task 2.2).
//
// # Why this is not obviously necessary, and is
//
// `internal/runlink`'s allowlist says the same thing about its own contract document — "the contract doc
// docs/decisions/p11-contracts.md renders from this list" — and nothing checks it. That claim has been
// in the tree since P11. It may well still be true; the point is that nobody would know if it were not,
// and the person who finds out is a security reviewer who asked the document what an event can contain
// and got last quarter's answer while every test stayed green.
//
// So the generator has a `-check` mode and this runs it. A field added to the allowlist without
// regenerating the document is a red build.

func TestTheReviewDocumentIsGeneratedFromTheAllowlist(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	cmd := exec.Command("go", "run", "./cmd/erroreportdoc", "-check", "-root", ".")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the review document has drifted from the allowlist:\n%s", out)
	}
	if !strings.Contains(string(out), "matches the allowlist") {
		t.Fatalf("the check did not report a match:\n%s", out)
	}
}

// TestTheDocumentGateGoesRed proves the gate is connected, by regenerating against a document that has
// been edited — in a COPY, so a crashed run cannot leave the real one wrong.
func TestTheDocumentGateGoesRed(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	const target = "../../docs/decisions/p24-error-event-allowlist.md"
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read the document: %v", err)
	}

	root := t.TempDir()
	if err := os.MkdirAll(root+"/docs/decisions", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	edited := strings.Replace(string(original), "| `trace_id` |", "| `trace_id_but_edited` |", 1)
	if edited == string(original) {
		t.Fatal("the fixture edit changed nothing — the test would prove the gate is red for no reason")
	}
	if err := os.WriteFile(root+"/docs/decisions/p24-error-event-allowlist.md", []byte(edited), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cmd := exec.Command("go", "run", "./cmd/erroreportdoc", "-check", "-root", root)
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("an edited document passed the check:\n%s", out)
	}
	if !strings.Contains(string(out), "DRIFTED") {
		t.Errorf("the failure does not name the problem:\n%s", out)
	}
}
