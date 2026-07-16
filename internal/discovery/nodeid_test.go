package discovery

import "testing"

// Node IDs are deterministic and stable across benign line shifts, unique per call site (I3 / doc 06).
func TestNodeIDStability(t *testing.T) {
	base := NodeIdentity{
		ModulePkgPath:      "github.com/acme/app/internal/llm",
		EnclosingSymbolFQN: "(*Service).Summarize",
		Selector:           "Messages.New",
		OccurrenceIndex:    0,
	}

	// Deterministic: same tuple -> same id (recompute from an equal identity, not the same call twice).
	again := NodeIdentity{
		ModulePkgPath:      "github.com/acme/app/internal/llm",
		EnclosingSymbolFQN: "(*Service).Summarize",
		Selector:           "Messages.New",
		OccurrenceIndex:    0,
	}
	if base.NodeID() != again.NodeID() {
		t.Fatal("node_id not deterministic for an identical tuple")
	}

	// A benign line shift is not part of the identity (no line field), so the id is unchanged. Two calls
	// in one symbol differ by occurrence index.
	second := base
	second.OccurrenceIndex = 1
	if base.NodeID() == second.NodeID() {
		t.Fatal("occurrence index must make same-selector calls distinct")
	}

	// Different selector -> different id.
	diffSel := base
	diffSel.Selector = "Chat.Completions.New"
	if diffSel.NodeID() == base.NodeID() {
		t.Fatal("different selector must yield a different id")
	}

	// Different enclosing symbol -> different id.
	diffSym := base
	diffSym.EnclosingSymbolFQN = "(*Service).Classify"
	if diffSym.NodeID() == base.NodeID() {
		t.Fatal("different enclosing symbol must yield a different id")
	}

	// Format is stable and prefixed.
	if got := base.NodeID(); len(got) != 18 || got[:2] != "n_" {
		t.Fatalf("unexpected node_id format %q", got)
	}
}

// The Selector excludes the receiver variable name, so renaming a local does not churn the node_id
// (doc 06 §3.1: "Rename a local variable used in the args -> unchanged").
func TestSelectorIgnoresReceiverName(t *testing.T) {
	reg := mustRegistry(t)
	srcA := `package svc
import "github.com/anthropics/anthropic-sdk-go"
func run(client *anthropic.Client) { client.Messages.New(nil, anthropic.MessageNewParams{}) }`
	srcB := `package svc
import "github.com/anthropics/anthropic-sdk-go"
func run(c *anthropic.Client) { c.Messages.New(nil, anthropic.MessageNewParams{}) }` // receiver renamed client->c

	a, _ := detect(t, reg, nil, srcA)
	b, _ := detect(t, reg, nil, srcB)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("want 1 node each, got %d and %d", len(a), len(b))
	}
	if a[0].NodeID != b[0].NodeID {
		t.Fatalf("renaming the receiver must not change node_id: %s vs %s", a[0].NodeID, b[0].NodeID)
	}
}
