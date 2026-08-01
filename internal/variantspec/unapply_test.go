package variantspec

import (
	"context"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// P13 task 2.6 / spec "A compression that removes a live slot is refused naming the slot": a prompt
// rewrite whose pinned prompt drops a slot the discovered call site still supplies is refused at
// resolve, naming the slot, and produces no diff.

// irWithDiscoveredVars returns an IR whose single node's DISCOVERED prompt declares the given variables
// (the runtime values the call site feeds), so a rewrite that drops one of them un-applies the node.
func irWithDiscoveredVars(vars []string) *discovery.IR {
	return &discovery.IR{
		IRVersion: "1.0.0",
		Nodes: []discovery.IRNode{{
			NodeID: "n", Kind: "static_definition",
			CallSite:        discovery.IRCallSite{File: "p.go", Symbol: "f"},
			Model:           discovery.IRModel{Provider: "anthropic", ModelID: "m", Params: map[string]any{}},
			Prompt:          discovery.IRPrompt{Inline: "Triage {{ticket}}", Variables: vars},
			ContextAssembly: discovery.IRContextAssembly{Policy: "inline_messages"},
			IOContract:      discovery.IRIOContract{InputSchema: map[string]any{"properties": map[string]any{}}},
		}},
		Edges: []discovery.IREdge{},
	}
}

func TestCompressionUnApplyIsRefusedNamingSlot(t *testing.T) {
	regs := newFakeRegistries()
	// The rewritten (compressed) prompt DROPS {{ticket}} — a slot the discovered call site still feeds.
	regs.prompts["compressed"] = promptEntry(t, "compressed", "Triage the ticket.")

	spec := &VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev",
		Order: []string{"n"},
		Nodes: map[string]NodeOverride{"n": {PromptRef: "compressed"}},
		Edges: []Edge{},
	}
	_, err := Resolve(context.Background(), spec, irWithDiscoveredVars([]string{"ticket"}), regs)
	assertSpecError(t, err, ErrRewriteUnappliesNode, "n", DimPrompt, "ticket")
}

// 5.3 (QA gate): the un-apply refusal goes RED — a compression that drops a live {{slot}} is refused
// naming the slot, and no diff is produced (resolution fails before any transform).
func TestUnApplyRefusalGoesRed(t *testing.T) {
	regs := newFakeRegistries()
	regs.prompts["dropped"] = promptEntry(t, "dropped", "Summarize the input.") // drops {{doc}}
	spec := &VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev",
		Order: []string{"n"},
		Nodes: map[string]NodeOverride{"n": {PromptRef: "dropped"}},
		Edges: []Edge{},
	}
	resolved, err := Resolve(context.Background(), spec, irWithDiscoveredVars([]string{"doc"}), regs)
	assertSpecError(t, err, ErrRewriteUnappliesNode, "n", DimPrompt, "doc")
	if resolved != nil {
		t.Error("a refused un-apply must produce no resolved config (hence no diff)")
	}
}

// The mirror admissible case: a rewrite that KEEPS the live slot resolves cleanly (no false refusal).
func TestRewriteKeepingLiveSlotResolves(t *testing.T) {
	regs := newFakeRegistries()
	regs.prompts["kept"] = promptEntry(t, "kept", "Please triage {{ticket}} carefully.")
	spec := &VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev",
		Order: []string{"n"},
		Nodes: map[string]NodeOverride{"n": {PromptRef: "kept"}},
		Edges: []Edge{},
	}
	if _, err := Resolve(context.Background(), spec, irWithDiscoveredVars([]string{"ticket"}), regs); err != nil {
		t.Fatalf("a rewrite that keeps the live slot must resolve, got: %v", err)
	}
}
