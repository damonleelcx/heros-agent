package configresolver

import (
	"context"
	"errors"
	"testing"
)

// P10 §8 resolver tests (task 8.5): override unreachable, malformed, and stale-but-valid in turn;
// assert the last known-good stays in force, degraded is reported, and startup succeeds with every
// override source unavailable.

func doc(hash string) []byte {
	return []byte(`{"schema":"heros.agentcfg/v1","config_hash":"` + hash + `","nodes":{}}`)
}

// staticSource returns fixed bytes, or an error, to simulate reachable/unreachable/malformed sources.
type staticSource struct {
	name string
	raw  []byte
	err  error
}

func (s staticSource) Name() string { return s.name }
func (s staticSource) Load(_ context.Context) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.raw, nil
}

func TestNew_MalformedEmbeddedIsAHardError(t *testing.T) {
	if _, err := New([]byte("{not json")); err == nil {
		t.Fatal("a malformed embedded document must fail construction — it is a build defect")
	}
}

func TestNew_EmbeddedOnlyResolvesWithNoExternalDependency(t *testing.T) {
	r, err := New(doc("embedded1"))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Resolve().ConfigHash; got != "embedded1" {
		t.Fatalf("config hash = %q, want embedded1", got)
	}
	if r.Health().Degraded {
		t.Fatal("a resolver with no override configured is not degraded")
	}
}

func TestRefresh_StartupSucceedsWithEveryOverrideUnavailable(t *testing.T) {
	r, err := New(doc("embedded1"),
		WithLocalOverride(staticSource{name: "local", err: errors.New("no such file")}),
		WithRemote(staticSource{name: "remote", err: errors.New("connection refused")}),
	)
	if err != nil {
		t.Fatal("construction must not depend on override sources")
	}
	// A refresh with everything down must not panic or block; the embedded document stays in force.
	r.Refresh(context.Background())
	if got := r.Resolve().ConfigHash; got != "embedded1" {
		t.Fatalf("with all overrides down, the embedded document must stay in force, got %q", got)
	}
	if !r.Health().Degraded {
		t.Fatal("an unreachable override must report degraded, not fail silently")
	}
}

func TestRefresh_UnreachableOverrideKeepsLastKnownGood(t *testing.T) {
	// First adopt a valid local override, then make it unreachable — the adopted document must stay.
	local := &mutableSource{name: "local", raw: doc("override1")}
	r, err := New(doc("embedded1"), WithLocalOverride(local))
	if err != nil {
		t.Fatal(err)
	}
	r.Refresh(context.Background())
	if got := r.Resolve().ConfigHash; got != "override1" {
		t.Fatalf("a valid override should be adopted, got %q", got)
	}
	// Now the source breaks.
	local.err = errors.New("unreachable")
	r.Refresh(context.Background())
	if got := r.Resolve().ConfigHash; got != "override1" {
		t.Fatalf("last known-good must stay in force on failure, got %q", got)
	}
	h := r.Health()
	if !h.Degraded || h.FailedSource != "local" {
		t.Fatalf("degraded state must name the failed source, got %+v", h)
	}
}

func TestRefresh_MalformedOverrideIsNotAdopted(t *testing.T) {
	local := &mutableSource{name: "local", raw: []byte("{ this is not valid json")}
	r, err := New(doc("embedded1"), WithLocalOverride(local))
	if err != nil {
		t.Fatal(err)
	}
	r.Refresh(context.Background())
	if got := r.Resolve().ConfigHash; got != "embedded1" {
		t.Fatalf("a malformed override must NOT be adopted (never fail-open), got %q", got)
	}
	if !r.Health().Degraded {
		t.Fatal("a malformed override must report degraded")
	}
}

func TestRefresh_EmptyOverrideIsRejectedNeverFailOpen(t *testing.T) {
	local := &mutableSource{name: "local", raw: []byte("")}
	r, _ := New(doc("embedded1"), WithLocalOverride(local))
	r.Refresh(context.Background())
	if got := r.Resolve().ConfigHash; got != "embedded1" {
		t.Fatalf("an empty override must never replace the document with an empty/default config, got %q", got)
	}
}

func TestRefresh_RemoteWinsOverLocalWhenBothValid(t *testing.T) {
	r, _ := New(doc("embedded1"),
		WithLocalOverride(staticSource{name: "local", raw: doc("local1")}),
		WithRemote(staticSource{name: "remote", raw: doc("remote1")}),
	)
	r.Refresh(context.Background())
	if got := r.Resolve().ConfigHash; got != "remote1" {
		t.Fatalf("remote (last in resolution order) should win when valid, got %q", got)
	}
	if r.Health().Degraded {
		t.Fatal("a successful adoption clears the degraded state")
	}
}

func TestRefresh_RecoveryClearsDegraded(t *testing.T) {
	local := &mutableSource{name: "local", err: errors.New("down")}
	r, _ := New(doc("embedded1"), WithLocalOverride(local))
	r.Refresh(context.Background())
	if !r.Health().Degraded {
		t.Fatal("should be degraded while the source is down")
	}
	local.err = nil
	local.raw = doc("recovered")
	r.Refresh(context.Background())
	if r.Health().Degraded {
		t.Fatal("degraded must clear once an override is adopted again")
	}
	if r.Resolve().ConfigHash != "recovered" {
		t.Fatal("the recovered document should be in force")
	}
}

// mutableSource lets a test change a source's behaviour mid-run (reachable → unreachable → recovered).
type mutableSource struct {
	name string
	raw  []byte
	err  error
}

func (s *mutableSource) Name() string { return s.name }
func (s *mutableSource) Load(_ context.Context) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.raw, nil
}
