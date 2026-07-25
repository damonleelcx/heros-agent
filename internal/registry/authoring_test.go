package registry

import (
	"reflect"
	"testing"
)

// Pure-function tests for the P10 read-model helpers (tasks 2.4, 2.5, 2.6). These need no Postgres —
// the DB-backed publish/read idempotency proof is in authoring_pgproof_test.go behind the pgproof tag.

func TestLineDiff_ReportsAddsRemovesAndContext(t *testing.T) {
	a := "line one\nshared\nold middle\nlast"
	b := "line one\nshared\nnew middle\nlast"
	got := lineDiff(a, b)
	want := []DiffLine{
		{Op: DiffContext, Text: "line one"},
		{Op: DiffContext, Text: "shared"},
		{Op: DiffRemove, Text: "old middle"},
		{Op: DiffAdd, Text: "new middle"},
		{Op: DiffContext, Text: "last"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lineDiff mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestLineDiff_IsDeterministic(t *testing.T) {
	a := "alpha\nbeta\ngamma"
	b := "alpha\ngamma\ndelta"
	first := lineDiff(a, b)
	for i := 0; i < 20; i++ {
		if !reflect.DeepEqual(lineDiff(a, b), first) {
			t.Fatalf("lineDiff is not deterministic across runs")
		}
	}
}

func TestLineDiff_TrailingNewlineIsNotASpuriousEmptyLine(t *testing.T) {
	// A body and "that body plus a final newline" have the same logical lines; splitLines drops the
	// single trailing empty line so they do not diff as a spurious empty-line add.
	got := lineDiff("only line", "only line\n")
	for _, d := range got {
		if d.Op != DiffContext {
			t.Fatalf("trailing newline produced a %s op: %+v", d.Op, got)
		}
	}
}

func TestSetDiff_AddedAndRemovedAreExplicitAndSorted(t *testing.T) {
	added, removed := setDiff([]string{"ticket", "tier"}, []string{"ticket", "customer", "account"})
	if !reflect.DeepEqual(added, []string{"account", "customer"}) {
		t.Fatalf("added = %v, want [account customer]", added)
	}
	if !reflect.DeepEqual(removed, []string{"tier"}) {
		t.Fatalf("removed = %v, want [tier]", removed)
	}
}

func TestSetDiff_NoChangeIsEmptyNotNil(t *testing.T) {
	added, removed := setDiff([]string{"a", "b"}, []string{"a", "b"})
	if added == nil || removed == nil {
		t.Fatalf("added/removed must be non-nil so they serialise as [] not null")
	}
	if len(added) != 0 || len(removed) != 0 {
		t.Fatalf("expected no change, got added=%v removed=%v", added, removed)
	}
}

func TestAnalyzeImpact_AddedUnbindableSlotBlocksNode(t *testing.T) {
	// The node's call site feeds `ticket`; the proposed body adds {{customer_tier}}, which nothing binds.
	nodes := []ImpactNode{{
		NodeID:        "n_triage",
		CallSiteExprs: []string{"ticket"},
		Analyzable:    true,
	}}
	got, err := AnalyzeImpact("Triage: {{ticket}} for {{customer_tier}}", nodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blocked) != 1 || got.Blocked[0].NodeID != "n_triage" {
		t.Fatalf("expected n_triage blocked, got %+v", got.Blocked)
	}
	if !contains(got.Blocked[0].Reason, "customer_tier") {
		t.Fatalf("reason should name the unbindable slot, got %q", got.Blocked[0].Reason)
	}
}

func TestAnalyzeImpact_SafeWordingEditBlocksNothing(t *testing.T) {
	nodes := []ImpactNode{{NodeID: "n_triage", CallSiteExprs: []string{"ticket"}, Analyzable: true}}
	got, err := AnalyzeImpact("Please triage this ticket: {{ticket}}", nodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blocked) != 0 {
		t.Fatalf("a wording-only edit must block nothing, got %+v", got.Blocked)
	}
}

func TestAnalyzeImpact_UnclaimedOperandBlocks(t *testing.T) {
	// The proposed body removes the slot that used to claim `ticket`, leaving it unclaimed — a value the
	// rewrite would silently drop. promptExprFor refuses this; impact analysis predicts it.
	nodes := []ImpactNode{{NodeID: "n_triage", CallSiteExprs: []string{"ticket"}, Analyzable: true}}
	got, err := AnalyzeImpact("A fixed prompt with no slots", nodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blocked) != 1 || !contains(got.Blocked[0].Reason, "ticket") {
		t.Fatalf("expected n_triage blocked for unclaimed ticket, got %+v", got.Blocked)
	}
}

func TestAnalyzeImpact_UnanalyzableNodeIsNamedNotOmitted(t *testing.T) {
	nodes := []ImpactNode{{NodeID: "n_opaque", Analyzable: false, UnanalyzableWhy: "call site assembles its prompt dynamically"}}
	got, err := AnalyzeImpact("Any body {{x}}", []ImpactNode(nodes))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Unanalyzed) != 1 || got.Unanalyzed[0].NodeID != "n_opaque" {
		t.Fatalf("an undecidable node must be reported unanalyzed, not omitted; got %+v", got)
	}
	if len(got.Blocked) != 0 {
		t.Fatalf("an unanalyzable node must not be reported as blocked")
	}
}

func TestAnalyzeImpact_BoundSlotSatisfiesWithoutCallSite(t *testing.T) {
	// A slot satisfied by an explicit binding needs no call-site operand (variable-bindings, additive).
	nodes := []ImpactNode{{NodeID: "n", BoundSlots: []string{"tier"}, Analyzable: true}}
	got, err := AnalyzeImpact("Tier is {{tier}}", nodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blocked) != 0 {
		t.Fatalf("a bound slot should satisfy without a call-site expr; got %+v", got.Blocked)
	}
}

func TestAnalyzeImpact_MalformedBodyIsRejectedNamingPosition(t *testing.T) {
	_, err := AnalyzeImpact("broken {{ not-a-slot", nil)
	if err == nil {
		t.Fatal("a malformed proposed body must be rejected")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
