package main

import (
	"os"
	"strings"
	"testing"
)

// main_test.go covers the one piece of this demo that is a SECURITY posture rather than a
// demonstration: how the Stripe API key gets in.
//
// A command-line flag was the obvious shape and it is the wrong one — it puts a credential in shell
// history and in `ps` output for every user on the box. That is the same class of exposure the Secrets
// seam exists to prevent, and a demo is the code most likely to be copied by the person least likely to
// notice. So the flag is gone, and these assert that the replacements actually work and that the
// refusal actually refuses.

// withStdin replaces os.Stdin for one test.
func withStdin(t *testing.T, content string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString(content); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = w.Close()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig; _ = r.Close() })
}

func TestStripeAPIKeyComesFromTheEnvironment(t *testing.T) {
	t.Setenv("STRIPE_API_KEY", "sk_test_from_the_environment")
	*keyStdin = false
	t.Cleanup(func() { *keyStdin = false })

	got, err := stripeAPIKey()
	if err != nil {
		t.Fatalf("stripeAPIKey: %v", err)
	}
	if got != "sk_test_from_the_environment" {
		t.Errorf("key = %q, want the value from STRIPE_API_KEY", got)
	}
}

func TestStripeAPIKeyCanComeFromStdin(t *testing.T) {
	// Explicitly with the environment ALSO set, so the test proves stdin is preferred when asked for
	// rather than accidentally reading the env and passing.
	t.Setenv("STRIPE_API_KEY", "sk_test_from_the_environment")
	withStdin(t, "sk_test_piped_in\n")
	*keyStdin = true
	t.Cleanup(func() { *keyStdin = false })

	got, err := stripeAPIKey()
	if err != nil {
		t.Fatalf("stripeAPIKey: %v", err)
	}
	if got != "sk_test_piped_in" {
		t.Errorf("key = %q, want the piped value (trimmed)", got)
	}
}

// TestStripeAPIKeyRefusesWithNothingToRead: the refusal must NAME both correct paths. A demo that just
// said "no key" would leave the reader reaching for the flag that no longer exists.
func TestStripeAPIKeyRefusesWithNothingToRead(t *testing.T) {
	t.Setenv("STRIPE_API_KEY", "")
	*keyStdin = false
	t.Cleanup(func() { *keyStdin = false })

	_, err := stripeAPIKey()
	if err == nil {
		t.Fatal("a missing key was accepted")
	}
	msg := err.Error()
	for _, want := range []string{"STRIPE_API_KEY", "-api-key-stdin"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q: %q", want, msg)
		}
	}
	if !strings.Contains(msg, "shell history") {
		t.Errorf("the refusal does not say WHY it is not a flag, so the next person adds one back: %q", msg)
	}
}

// TestNoAPIKeyFlagExists is the structural half. The refusal above is only true while there is nothing
// else to reach for.
func TestNoAPIKeyFlagExists(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if strings.Contains(string(source), `flag.String("api-key"`) {
		t.Error("an -api-key flag was reintroduced — a credential on the command line lands in shell " +
			"history and in ps output; use STRIPE_API_KEY or -api-key-stdin")
	}
}

// TestPlaceholderCatalogIsNotUsableAgainstRealStripe states, as a test, the thing prerequisite (C) is
// about: the built-in catalog's references are PLACEHOLDERS. Anyone pointing this demo at real Stripe
// must publish a catalog of their own with `-plans`, and the preflight is what tells them whether they
// did it right.
func TestPlaceholderCatalogIsNotUsableAgainstRealStripe(t *testing.T) {
	if !strings.Contains(hermesCatalog, "price_ref_team_sub") {
		t.Skip("the built-in catalog no longer carries the placeholder this test describes")
	}
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(source), `flag.String("plans"`) {
		t.Error("the built-in catalog carries placeholder price references and there is no -plans flag, " +
			"so a real-Stripe run would require editing this file")
	}
}
