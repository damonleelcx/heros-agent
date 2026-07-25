package runlink

import "testing"

// The allowlist is a public contract (task 1.1). These tests pin its shape so a careless edit is a
// test failure, not a silent boundary change.

func TestAllowlistIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	validCats := map[string]bool{"metrics": true, "ir_structure": true, "provenance": true, "scores": true, "run_metadata": true}
	for _, f := range Allowlist {
		if f.Name == "" {
			t.Fatalf("allowlist field with empty name")
		}
		if seen[f.Name] {
			t.Fatalf("duplicate allowlist key %q", f.Name)
		}
		seen[f.Name] = true
		if !validCats[f.Category] {
			t.Errorf("field %q has unknown category %q", f.Name, f.Category)
		}
		if f.Why == "" {
			t.Errorf("field %q has no justification — the allowlist is a review artifact", f.Name)
		}
	}
	if len(AllowlistKeys()) != len(Allowlist) {
		t.Fatalf("AllowlistKeys drifted from Allowlist")
	}
}

func TestContentIsNotExpressibleOnTheAllowlist(t *testing.T) {
	// Content keys must never appear. This is a guard against a well-meaning future edit that adds, say,
	// "prompt_text" for a richer dashboard (design Decision 4 rejects exactly that). The markers are
	// distinctive enough not to collide with legitimate keys ("tokens" the count, "source_revision" the
	// commit id) — they name the content itself: prompt/diff/credential/secret/password/file contents.
	forbidden := []string{"prompt", "diff", "credential", "secret", "password", "file_content", "source_code", "env_value", "api_key"}
	for _, f := range Allowlist {
		for _, bad := range forbidden {
			if containsFold(f.Name, bad) {
				t.Errorf("allowlist key %q looks like content (%q) — content never crosses the boundary", f.Name, bad)
			}
		}
	}
}

func TestPermittedAndCategory(t *testing.T) {
	if !Permitted("metrics.cost") {
		t.Error("metrics.cost should be permitted")
	}
	if Permitted("prompt_text") {
		t.Error("prompt_text must never be permitted")
	}
	if CategoryOf("scores.value") != "scores" {
		t.Errorf("scores.value category = %q, want scores", CategoryOf("scores.value"))
	}
	if CategoryOf("nope") != "" {
		t.Error("unknown key should have empty category")
	}
}

func TestLinkTargetPin(t *testing.T) {
	// Run linking works with https://heros-agent.space and nothing else.
	good := []string{
		"https://heros-agent.space",
		"https://heros-agent.space/",
		"https://heros-agent.space/api/p11/link",
	}
	for _, u := range good {
		if !IsLinkTarget(u) {
			t.Errorf("IsLinkTarget(%q) = false, want true", u)
		}
	}
	bad := []string{
		"http://heros-agent.space",          // wrong scheme
		"https://evil.example",              // wrong host
		"https://heros-agent.space.evil",    // suffix attack
		"https://heros-agent.space:8443",    // wrong port
		"https://staging.heros-agent.space", // subdomain
		"",
	}
	for _, u := range bad {
		if IsLinkTarget(u) {
			t.Errorf("IsLinkTarget(%q) = true, want false", u)
		}
	}
	if PlatformBaseURL != "https://heros-agent.space" {
		t.Fatalf("the platform endpoint must be pinned to https://heros-agent.space, got %q", PlatformBaseURL)
	}
}

func containsFold(s, sub string) bool {
	// tiny case-insensitive contains, to avoid pulling strings into the security package's test surface
	ls, lsub := toLower(s), toLower(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
