package configresolver

import (
	"context"
	"errors"
	"testing"

	"github.com/heros-foreal/agentd/internal/telemetry"
)

// P10 §9 reconciliation + verification interlock tests.

// Task 9.5 🔴 — the reconciliation MUST be able to go red. A check that cannot fail is decoration, so
// this deliberately resolves a mismatched hash and asserts the run FAILS.
func TestReconciliation_GoesRedOnMismatch(t *testing.T) {
	rec := NewReconciliation("requested-hash")
	rec.Observe("requested-hash") // a matching invocation
	rec.Observe("DIFFERENT-hash")  // one mismatched invocation
	rec.Observe("requested-hash")

	if rec.OK() {
		t.Fatal("a run with ANY mismatched invocation must fail — the check must be able to go red")
	}
	if !errors.Is(rec.Verdict(), ErrConfigHashMismatch) {
		t.Fatalf("verdict = %v, want ErrConfigHashMismatch", rec.Verdict())
	}
}

// Task 9.2 — a fully-matching run is scored; the mismatch check covers every invocation, not partially.
func TestReconciliation_AllMatchPasses(t *testing.T) {
	rec := NewReconciliation("h")
	for i := 0; i < 100; i++ {
		rec.Observe("h")
	}
	if !rec.OK() {
		t.Fatalf("a run whose every invocation matched must pass: %v", rec.Verdict())
	}
}

func TestReconciliation_OneMismatchAmongManyFailsWholeRun(t *testing.T) {
	rec := NewReconciliation("h")
	for i := 0; i < 99; i++ {
		rec.Observe("h")
	}
	rec.Observe("other") // the single bad one, last
	if rec.OK() {
		t.Fatal("a single differing invocation must fail the whole run, not be partially scored")
	}
}

// Task 9.3 / 9.6 — a pinned resolver reads only the embedded document; a configured override that would
// change the hash is not consulted.
func TestPinned_IgnoresOverridesEntirely(t *testing.T) {
	pinned, err := NewPinned(doc("embedded-verified"))
	if err != nil {
		t.Fatal(err)
	}
	// Even if we could attach an override, a pinned resolver refreshes to nothing. Simulate a refresh
	// with a would-be override source present by constructing a normal resolver for contrast.
	pinned.local = staticSource{name: "local", raw: doc("override-that-should-be-ignored")}
	pinned.Refresh(context.Background())
	if got := pinned.Resolve().ConfigHash; got != "embedded-verified" {
		t.Fatalf("a pinned resolver must ignore overrides, got %q", got)
	}
	if pinned.Health().Degraded {
		t.Fatal("a pinned resolver is not degraded — it simply does not consult overrides")
	}
}

// Task 9.6 — an unverified resolution is marked and is refused at the highest automation level.
func TestRefuseUnverified_MarkedAndRefusableByLevel(t *testing.T) {
	// A document whose verified_config_hash is absent → unverified.
	unverified, _ := New([]byte(`{"schema":"heros.agentcfg/v1","config_hash":"h1","nodes":{}}`))
	if unverified.Resolve().Verified() {
		t.Fatal("a document with no verified_config_hash must be unverified")
	}
	// Marked in the tag set on every invocation (task 9.4).
	tags := unverified.Tags()
	if tags[telemetry.AttrUnverified] != "true" {
		t.Fatalf("unverified must be marked in the tags, got %v", tags)
	}
	// Permitted under a permissive level (marked, not blocked)...
	if refuse, _ := unverified.RefuseUnverified(false); refuse {
		t.Fatal("an unverified resolution is permitted (marked) under a permissive automation level")
	}
	// ...refused at the highest level that requires verified configurations.
	refuse, reason := unverified.RefuseUnverified(true)
	if !refuse || reason == "" {
		t.Fatal("the highest automation level must refuse an unverified resolution, with a reason")
	}
}

func TestRefuseUnverified_VerifiedConfigPasses(t *testing.T) {
	verified, _ := New([]byte(`{"schema":"heros.agentcfg/v1","config_hash":"h1","verified_config_hash":"h1","nodes":{}}`))
	if !verified.Resolve().Verified() {
		t.Fatal("a document whose verified_config_hash equals its config_hash is verified")
	}
	if refuse, _ := verified.RefuseUnverified(true); refuse {
		t.Fatal("a verified configuration must not be refused even at the highest level")
	}
	if verified.Tags()[telemetry.AttrUnverified] != "false" {
		t.Fatal("a verified configuration must be tagged unverified=false")
	}
}
