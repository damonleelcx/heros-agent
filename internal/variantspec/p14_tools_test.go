package variantspec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// P14 §5 — DimTools and tool selection (decisions.md D-14.2).

// toolIR is the two-node test IR with a recorded P14 tool split on n_b: two statically-declared tools
// and one platform skill, which is the shape a selection is validated against.
func toolIR() *discovery.IR {
	ir := testIR()
	for i := range ir.Nodes {
		if ir.Nodes[i].NodeID != "n_b" {
			continue
		}
		ir.Nodes[i].Tools = []discovery.IRTool{
			{Name: "weatherTool", DeclaredAt: &discovery.IRToolLocation{Line: 13, Index: 0}},
			{Name: "sqlTool", DeclaredAt: &discovery.IRToolLocation{Line: 13, Index: 1}},
		}
		ir.Nodes[i].Skills = []string{"search_kb"}
	}
	return ir
}

func resolveWithIR(t *testing.T, spec *VariantSpec, ir *discovery.IR) (*Resolved, error) {
	t.Helper()
	return Resolve(context.Background(), spec, ir, newFakeRegistries())
}

// ── 5.1 the closed enum grew by exactly one ──────────────────────────────────────────────────────

func TestDimToolsInClosedEnum(t *testing.T) {
	if DimTools != "tools" {
		t.Fatalf("DimTools is %q; the wire value is stored on rows and cannot drift", DimTools)
	}
	// The five P14 froze, IN ORDER, as the PREFIX of the enum. A prefix and not the whole list: the
	// enum is closed against accidents, not against later phases — P17 appends DimMemory deliberately,
	// through the same eight-step checklist, and a length equality here would have failed for that
	// addition while saying nothing about the claim this test actually makes, which is "P14 grew the
	// enum by exactly one, at the end, and did not disturb the four before it".
	want := []Dimension{DimModel, DimPrompt, DimSkills, DimContext, DimTools}
	got := Dimensions()
	if len(got) < len(want) {
		t.Fatalf("the closed enum has %d member(s), want at least the %d P14 froze: %v", len(got), len(want), got)
	}
	seen := map[Dimension]bool{}
	for i, d := range got {
		if i < len(want) && d != want[i] {
			t.Errorf("Dimensions()[%d] = %q, want %q — P14's five are the enum's stable prefix", i, d, want[i])
		}
		if seen[d] {
			t.Errorf("%q appears twice in the closed enum", d)
		}
		seen[d] = true
	}

	// DimTools is reported as an overridden dimension exactly when a selection is set — this is what
	// makes the transform's per-dimension dispatch mechanical rather than a rule to remember.
	if dims := (ResolvedOverride{}).Dimensions(); len(dims) != 0 {
		t.Errorf("an empty override reports dimensions %v", dims)
	}
	dims := (ResolvedOverride{ToolSelection: []string{"weatherTool"}}).Dimensions()
	if len(dims) != 1 || dims[0] != DimTools {
		t.Errorf("a tool selection must report exactly DimTools, got %v", dims)
	}
}

// ── 5.2 the override validates structurally, without the IR ──────────────────────────────────────

func TestToolSelectionOverrideValidates(t *testing.T) {
	t.Run("an empty entry is rejected", func(t *testing.T) {
		s := baseSpec()
		s.Nodes["n_b"] = NodeOverride{ToolSelection: []string{"weatherTool", ""}}
		err := s.Validate()
		if err == nil || !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("want ErrInvalidSpec, got %v", err)
		}
		var se *SpecError
		if !errors.As(err, &se) || se.Dim != DimTools {
			t.Errorf("the rejection must name the tools dimension, got %v", err)
		}
	})

	t.Run("a duplicate is rejected rather than de-duplicated", func(t *testing.T) {
		s := baseSpec()
		s.Nodes["n_b"] = NodeOverride{ToolSelection: []string{"weatherTool", "weatherTool"}}
		if err := s.Validate(); err == nil {
			t.Fatal("a selection that keeps the same tool twice must be rejected; silently collapsing it " +
				"would hide the author's misunderstanding rather than surface it")
		}
	})

	t.Run("a selection makes the override non-empty", func(t *testing.T) {
		if (NodeOverride{ToolSelection: []string{"weatherTool"}}).isEmpty() {
			t.Fatal("a node whose only override is a tool prune must not be treated as un-overridden; it " +
				"would never reach the transform and config_hash would not record it")
		}
	})

	t.Run("SelectedTools canonicalizes to a sorted set", func(t *testing.T) {
		got := (NodeOverride{ToolSelection: []string{"sqlTool", "weatherTool"}}).SelectedTools()
		if len(got) != 2 || got[0] != "sqlTool" || got[1] != "weatherTool" {
			t.Fatalf("SelectedTools must canonicalize, got %v", got)
		}
		if (NodeOverride{}).SelectedTools() != nil {
			t.Error("an unset selection must canonicalize to nil, not to an empty slice")
		}
	})

	// 🚫 A tool selection contributes nothing to Refs(): a tool has no registry identity, so a loader
	// asked to resolve one would fail on something that was never meant to be registered.
	t.Run("a selection is not a registry ref", func(t *testing.T) {
		s := baseSpec()
		s.Nodes["n_b"] = NodeOverride{ToolSelection: []string{"weatherTool"}}
		for _, r := range s.Refs() {
			if r == "weatherTool" {
				t.Fatal("a discovered tool identifier leaked into the spec's registry refs")
			}
		}
	})
}

// ── 5.3 fail closed against the discovered set ───────────────────────────────────────────────────

func TestToolSelectionFailsClosedOnUndiscovered(t *testing.T) {
	t.Run("a tool the node does not offer is rejected", func(t *testing.T) {
		s := baseSpec()
		s.Nodes["n_b"] = NodeOverride{ToolSelection: []string{"weatherTool", "phantomTool"}}
		_, err := resolveWithIR(t, s, toolIR())
		if !errors.Is(err, ErrToolNotDiscovered) {
			t.Fatalf("want ErrToolNotDiscovered, got %v", err)
		}
		var se *SpecError
		if !errors.As(err, &se) {
			t.Fatalf("want a *SpecError, got %T", err)
		}
		if se.NodeID != "n_b" || se.Dim != DimTools || se.Ref != "phantomTool" {
			t.Errorf("the rejection must name node, dimension and the offending tool; got node=%q dim=%q ref=%q",
				se.NodeID, se.Dim, se.Ref)
		}
		if !strings.Contains(se.Detail, "sqlTool") {
			t.Errorf("the rejection must say what the node DOES offer, or the user cannot act on it: %s", se.Detail)
		}
	})

	// 🔴 The pre-split IR case. `Tools == nil` means "this IR does not record tools", NOT "this node
	// offers none" — accepting a selection against it would be a false acceptance on every IR authored
	// before P14, which is the whole population of stored IRs.
	t.Run("an IR that predates the split is rejected, not treated as tool-free", func(t *testing.T) {
		s := baseSpec()
		s.Nodes["n_b"] = NodeOverride{ToolSelection: []string{"weatherTool"}}
		_, err := resolveWithIR(t, s, testIR()) // testIR records no split
		if !errors.Is(err, ErrToolNotDiscovered) {
			t.Fatalf("a selection against an IR with no recorded tool set must be refused, got %v", err)
		}
		if !strings.Contains(err.Error(), "predates") {
			t.Errorf("the rejection must distinguish 'not recorded' from 'no tools', got: %v", err)
		}
	})

	t.Run("a selection over discovered tools resolves", func(t *testing.T) {
		s := baseSpec()
		s.Nodes["n_b"] = NodeOverride{ToolSelection: []string{"weatherTool"}}
		r, err := resolveWithIR(t, s, toolIR())
		if err != nil {
			t.Fatalf("a selection naming only discovered tools must resolve: %v", err)
		}
		ov, ok := r.Overrides["n_b"]
		if !ok {
			t.Fatal("the resolved override does not carry the tool selection")
		}
		if len(ov.ToolSelection) != 1 || ov.ToolSelection[0] != "weatherTool" {
			t.Errorf("resolved selection = %v", ov.ToolSelection)
		}
	})
}

// ── 5.4 additive hash participation ──────────────────────────────────────────────────────────────

func TestToolPruneChangesHash_NoPruneByteIdentical(t *testing.T) {
	regs := newFakeRegistries()
	ir := toolIR()

	hashCanon := func(t *testing.T, spec *VariantSpec) (string, string) {
		t.Helper()
		r, err := Resolve(context.Background(), spec, ir, regs)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		canon, err := r.Config.Canonical()
		if err != nil {
			t.Fatalf("Canonical: %v", err)
		}
		return r.ConfigHash, string(canon)
	}

	noPruneHash, noPruneCanon := hashCanon(t, baseSpec())

	pruned := baseSpec()
	pruned.Nodes["n_b"] = NodeOverride{ToolSelection: []string{"weatherTool"}}
	prunedHash, prunedCanon := hashCanon(t, pruned)

	if noPruneHash == prunedHash {
		t.Fatal("pruning a tool did not change config_hash; a prune the eval cannot tell from the " +
			"baseline is a change nobody can measure")
	}
	if !strings.Contains(prunedCanon, `"tool_selection"`) {
		t.Errorf("a pruning node must carry its selection in the hashed bytes:\n%s", prunedCanon)
	}

	// 🔴 The additive half: a node that prunes nothing emits NO tool_selection key at all.
	if strings.Contains(noPruneCanon, "tool_selection") {
		t.Fatalf("a no-prune node emitted a tool_selection key; a pre-P14 configuration must serialize "+
			"byte-identically:\n%s", noPruneCanon)
	}
}

// A selection is a SET: the same kept tools written in a different order canonicalize identically.
// Contrast skill_refs, whose order IS identity-bearing. Both halves are asserted together because the
// asymmetry is the contract, and a test of either alone would pass for an implementation that sorted
// both or neither.
func TestReorderSemantics(t *testing.T) {
	regs := newFakeRegistries()
	a := strings.Repeat("1", 64)
	b := strings.Repeat("2", 64)
	regs.skills[a] = p14Skill(t, a, "search_kb")
	regs.skills[b] = p14Skill(t, b, "issue_lookup")
	ir := toolIR()

	hash := func(t *testing.T, o NodeOverride) string {
		t.Helper()
		s := baseSpec()
		s.Nodes["n_b"] = o
		r, err := Resolve(context.Background(), s, ir, regs)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		return r.ConfigHash
	}

	toolsForward := hash(t, NodeOverride{ToolSelection: []string{"sqlTool", "weatherTool"}})
	toolsReversed := hash(t, NodeOverride{ToolSelection: []string{"weatherTool", "sqlTool"}})
	if toolsForward != toolsReversed {
		t.Error("two specs keeping the same tool SET in different authoring order produced different " +
			"config_hashes; they describe one configuration and every eval comparison would fragment")
	}

	skillsForward := hash(t, NodeOverride{SkillRefs: []string{a, b}})
	skillsReversed := hash(t, NodeOverride{SkillRefs: []string{b, a}})
	if skillsForward == skillsReversed {
		t.Error("reordering skill_refs did not change config_hash; skill order is identity-bearing " +
			"because the call site binds them in that order")
	}
}

// ── 7.7 determinism: same IR + spec + registry → identical config_hash ───────────────────────────
//
// The transform half (byte-identical materialized diff) is asserted in internal/transform
// (TestSkillToolTransformDeterministic, TestToolPruneIsDeterministic). This is the CONFIG half, and it
// needs saying separately: a resolver that iterated a map would produce a stable diff and a wandering
// hash, and the pair {config_hash, diff} is what reproducibility is keyed on — either one drifting
// breaks it.
func TestSkillToolResolveDeterministic(t *testing.T) {
	regs := newFakeRegistries()
	a := strings.Repeat("1", 64)
	b := strings.Repeat("2", 64)
	regs.skills[a] = p14Skill(t, a, "search_kb")
	regs.skills[b] = p14Skill(t, b, "issue_lookup")
	ir := toolIR()

	spec := baseSpec()
	spec.Nodes["n_b"] = NodeOverride{
		SkillRefs:     []string{b, a}, // order is identity-bearing, so it must survive verbatim
		ToolSelection: []string{"sqlTool", "weatherTool"},
	}

	var firstHash, firstCanon string
	for i := 0; i < 8; i++ {
		r, err := Resolve(context.Background(), spec, ir, regs)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		canon, err := r.Config.Canonical()
		if err != nil {
			t.Fatalf("run %d canonical: %v", i, err)
		}
		if i == 0 {
			firstHash, firstCanon = r.ConfigHash, string(canon)
			continue
		}
		if r.ConfigHash != firstHash {
			t.Fatalf("run %d hashed %s, run 0 hashed %s; resolution is iterating something unordered",
				i, r.ConfigHash, firstHash)
		}
		if string(canon) != firstCanon {
			t.Fatalf("run %d produced different canonical bytes:\n%s\nvs\n%s", i, canon, firstCanon)
		}
	}

	// And the two orderings really are treated differently, so the stability above is not the stability
	// of a resolver that sorted everything into one answer.
	if !strings.Contains(firstCanon, `"issue_lookup@`) {
		t.Fatalf("the resolved skills lost their names:\n%s", firstCanon)
	}
	if strings.Index(firstCanon, "issue_lookup@") > strings.Index(firstCanon, "search_kb@") {
		t.Error("skill_refs were sorted; the spec's declared binding order is identity-bearing and must survive")
	}
}
