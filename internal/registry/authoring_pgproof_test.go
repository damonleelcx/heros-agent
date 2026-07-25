//go:build pgproof

// Live-Postgres proof of the P10 prompt-authoring write/read path (task 2.7). Runs the real
// migrations (see TestMain in store_pgproof_test.go) and asserts through the READ path — a test that
// stopped at the write return could not see a shadowed entry, and immutability is exactly the property
// that only the read path can prove.
//
//	make pg-proof   # or: HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/registry/

package registry

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestPGProof_RepublishIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id1, err := s.RegisterPrompt(ctx, "t:tenant-A/triage", "Triage: {{ticket}}")
	if err != nil {
		t.Fatal(err)
	}
	// Byte-identical republish must return the same content-addressed id and create no duplicate.
	id2, err := s.RegisterPrompt(ctx, "t:tenant-A/triage", "Triage: {{ticket}}")
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("idempotent republish returned a different id: %s vs %s", id1, id2)
	}
	timeline, err := s.PromptTimeline(ctx, "t:tenant-A/triage")
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 1 {
		t.Fatalf("republish created a duplicate: timeline has %d entries", len(timeline))
	}
}

func TestPGProof_EditLeavesPriorVersionResolvableAndRenderingIdentically(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	oldID, err := s.RegisterPrompt(ctx, "t:tenant-A/triage", "Triage: {{ticket}}")
	if err != nil {
		t.Fatal(err)
	}
	// Render the OLD version now, through the read path, to capture its output.
	oldBefore, err := s.ResolvePrompt(ctx, oldID)
	if err != nil {
		t.Fatal(err)
	}
	renderedBefore, err := oldBefore.Template.Render(map[string]string{"ticket": "T-1"})
	if err != nil {
		t.Fatal(err)
	}

	// Edit: publish a new version of the same name.
	newID, err := s.RegisterPrompt(ctx, "t:tenant-A/triage", "Please triage this ticket: {{ticket}}")
	if err != nil {
		t.Fatal(err)
	}
	if newID == oldID {
		t.Fatal("an edit must produce a new version id")
	}

	// The prior version is STILL resolvable and renders EXACTLY as before the edit.
	oldAfter, err := s.ResolvePrompt(ctx, oldID)
	if err != nil {
		t.Fatalf("prior version no longer resolvable after edit: %v", err)
	}
	renderedAfter, err := oldAfter.Template.Render(map[string]string{"ticket": "T-1"})
	if err != nil {
		t.Fatal(err)
	}
	if renderedBefore != renderedAfter {
		t.Fatalf("prior version rendered differently after an edit: %q vs %q", renderedBefore, renderedAfter)
	}

	// Both versions appear in the timeline, oldest first.
	timeline, err := s.PromptTimeline(ctx, "t:tenant-A/triage")
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 2 {
		t.Fatalf("timeline should list both versions, got %d", len(timeline))
	}
	if timeline[0].VersionID != oldID {
		t.Fatalf("timeline is not oldest-first: first is %s, want %s", timeline[0].VersionID, oldID)
	}
	if timeline[0].CreatedAt.IsZero() {
		t.Fatal("timeline entry is missing creation metadata")
	}
}

func TestPGProof_TimelineCarriesSlotSetPerVersion(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.RegisterPrompt(ctx, "t:tenant-A/p", "one slot {{a}}"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterPrompt(ctx, "t:tenant-A/p", "two slots {{a}} {{b}}"); err != nil {
		t.Fatal(err)
	}
	timeline, err := s.PromptTimeline(ctx, "t:tenant-A/p")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(timeline[0].Slots, []string{"a"}) {
		t.Fatalf("v1 slots = %v, want [a]", timeline[0].Slots)
	}
	if !reflect.DeepEqual(timeline[1].Slots, []string{"a", "b"}) {
		t.Fatalf("v2 slots = %v, want [a b]", timeline[1].Slots)
	}
}

func TestPGProof_EmptyTimelineIsNotAnError(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	timeline, err := s.PromptTimeline(ctx, "t:tenant-A/never-published")
	if err != nil {
		t.Fatalf("an empty timeline must not be an error: %v", err)
	}
	if timeline == nil {
		t.Fatal("an empty timeline must be a non-nil empty slice, distinguishable from a decode failure")
	}
	if len(timeline) != 0 {
		t.Fatalf("expected empty timeline, got %d", len(timeline))
	}
}

func TestPGProof_DiffReportsSlotChangeSeparatelyFromBody(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	a, err := s.RegisterPrompt(ctx, "t:tenant-A/d", "Triage: {{ticket}}")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.RegisterPrompt(ctx, "t:tenant-A/d", "Triage: {{ticket}} tier {{tier}}")
	if err != nil {
		t.Fatal(err)
	}
	diff, err := s.DiffPromptVersions(ctx, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(diff.SlotsAdded, []string{"tier"}) {
		t.Fatalf("SlotsAdded = %v, want [tier] — the slot change must be reported explicitly", diff.SlotsAdded)
	}
	if len(diff.SlotsRemoved) != 0 {
		t.Fatalf("SlotsRemoved = %v, want empty", diff.SlotsRemoved)
	}
	if !diff.BodyChanged {
		t.Fatal("BodyChanged should be true")
	}
}

// Task 6.1: preview fidelity is a BYTE-COMPARISON, not an eyeball. The previewed string must equal the
// string a real run sends. Both go through the SAME Template.Render, so this proves StudioRender does
// not reformat, summarise, or approximate — a preview that approximates is a confident lie.
func TestPGProof_PreviewIsByteIdenticalToTheRunPath(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.RegisterPrompt(ctx, "t:tenant-A/fidelity", "Triage {{ticket}} at tier {{tier}}\n(end)")
	if err != nil {
		t.Fatal(err)
	}
	bindings := map[string]string{"ticket": "T-9\nmultiline", "tier": "gold"}

	preview, err := s.StudioRender(ctx, id, bindings)
	if err != nil {
		t.Fatal(err)
	}
	// The run path: resolve the version and render it exactly as the runtime would.
	entry, err := s.ResolvePrompt(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	runPath, err := entry.Template.Render(bindings)
	if err != nil {
		t.Fatal(err)
	}
	if preview != runPath {
		t.Fatalf("preview is not byte-identical to the run path:\n preview: %q\n run:     %q", preview, runPath)
	}
}

// Task 6.1 unhappy path: a missing binding names the slot rather than rendering a hole.
func TestPGProof_PreviewMissingBindingNamesSlot(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	id, err := s.RegisterPrompt(ctx, "t:tenant-A/hole", "Need {{ticket}} and {{tier}}")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.StudioRender(ctx, id, map[string]string{"ticket": "T-1"})
	if err == nil {
		t.Fatal("a missing binding must fail, not render a hole")
	}
	if !strings.Contains(err.Error(), "tier") {
		t.Fatalf("the failure must name the unbound slot; got %v", err)
	}
}

// M3.1/M3.5 — the model catalog (matrix rows) lists registered models, latest version per name.
func TestPGProof_ModelCatalogListsLatestPerName(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.RegisterModel(ctx, "sonnet", ModelSpec{Provider: "anthropic", ModelID: "claude-sonnet-5"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterModel(ctx, "gpt", ModelSpec{Provider: "openai", ModelID: "gpt-5"}); err != nil {
		t.Fatal(err)
	}
	// A second version of sonnet — the catalog keeps the latest, not both.
	temp := 0.5
	if _, err := s.RegisterModel(ctx, "sonnet", ModelSpec{Provider: "anthropic", ModelID: "claude-sonnet-5", Params: ModelParams{Temperature: &temp}}); err != nil {
		t.Fatal(err)
	}

	catalog, err := s.ModelCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 2 {
		t.Fatalf("catalog should list one row per name, got %d", len(catalog))
	}
	byName := map[string]ModelCatalogEntry{}
	for _, e := range catalog {
		byName[e.Name] = e
	}
	if byName["sonnet"].ModelID != "claude-sonnet-5" || byName["gpt"].Provider != "openai" {
		t.Fatalf("catalog rows wrong: %+v", catalog)
	}
}

func TestPGProof_DiffWordingOnlyChangeHasUnchangedSlotSet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	a, err := s.RegisterPrompt(ctx, "t:tenant-A/w", "Triage: {{ticket}}")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.RegisterPrompt(ctx, "t:tenant-A/w", "Please triage: {{ticket}}")
	if err != nil {
		t.Fatal(err)
	}
	diff, err := s.DiffPromptVersions(ctx, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.SlotsAdded) != 0 || len(diff.SlotsRemoved) != 0 {
		t.Fatalf("a wording-only edit must leave the slot set unchanged, got +%v -%v", diff.SlotsAdded, diff.SlotsRemoved)
	}
	if !diff.BodyChanged {
		t.Fatal("BodyChanged should be true for a wording change")
	}
}
