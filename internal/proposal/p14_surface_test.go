package proposal

import (
	"context"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P14 §8 — change legibility.
//
// The surface's job here is to make two different things look different. Binding a platform skill
// CONSTRUCTS a value from a sealed contract; pruning a provider tool DELETES a declaration the author
// wrote. A reviewer who approved one has not approved the other, and before the split there was no
// field that could tell them apart.

func surfaceIR(nodeID string) *discovery.IR {
	return &discovery.IR{
		IRVersion: "1.0.0",
		Nodes: []discovery.IRNode{{
			NodeID: nodeID, Kind: "static_definition",
			Tools: []discovery.IRTool{
				{Name: "weatherTool", DeclaredAt: &discovery.IRToolLocation{Line: 10, Index: 0}},
				{Name: "sqlTool", DeclaredAt: &discovery.IRToolLocation{Line: 10, Index: 1}},
			},
			Skills: []string{"search_kb"},
		}},
	}
}

// ── 8.1 tool vs skill, drawn from the split fields ───────────────────────────────────────────────

func TestChangeSurfaceDistinguishesToolFromSkill(t *testing.T) {
	const node = "n1"
	skillRef := strings.Repeat("9", 64)

	base := &variantspec.VariantSpec{
		SourceRevision: "rev1", Order: []string{node},
		Nodes: map[string]variantspec.NodeOverride{node: {}},
	}

	t.Run("binding a platform skill", func(t *testing.T) {
		cand := cloneSpec(base)
		addSkill(cand, node, skillRef)
		p := Present(Compiled{
			Candidate: Candidate{Operator: OpAddSkill, NodeID: node, Spec: cand},
			IR:        surfaceIR(node),
		}, base)

		c := findDimChange(t, p.SpecDiff, "skills")
		if c.Kind != KindSkillBind {
			t.Fatalf("a skill binding must be labelled %q, got %q", KindSkillBind, c.Kind)
		}
		if c.Kind.Legible() != "bound a platform skill" {
			t.Errorf("the legible sentence drifted: %q", c.Kind.Legible())
		}
		if len(c.Items) != 1 {
			t.Errorf("the change must name what was bound, got %v", c.Items)
		}
		// 🔴 And it must NOT read as a tool change.
		if hasDimChange(p.SpecDiff, "tools") {
			t.Error("binding a platform skill produced a TOOL change; the two mechanics are opposite and " +
				"the surface must not conflate them")
		}
	})

	t.Run("pruning a provider tool", func(t *testing.T) {
		cand := cloneSpec(base)
		setToolSelection(cand, node, []string{"weatherTool"})
		p := Present(Compiled{
			Candidate: Candidate{Operator: OpToolPrune, NodeID: node, Spec: cand},
			IR:        surfaceIR(node),
		}, base)

		c := findDimChange(t, p.SpecDiff, "tools")
		if c.Kind != KindToolPrune {
			t.Fatalf("a tool prune must be labelled %q, got %q", KindToolPrune, c.Kind)
		}
		if c.Kind.Legible() != "pruned a provider tool" {
			t.Errorf("the legible sentence drifted: %q", c.Kind.Legible())
		}
		// The dropped tool is NAMED. "one fewer tool" is not a thing a reviewer can approve.
		if len(c.Items) != 1 || c.Items[0] != "sqlTool" {
			t.Fatalf("the change must name the pruned tool, got %v", c.Items)
		}
		// The baseline set comes from the IR's SPLIT field — the only place the full offered set lives.
		if !strings.Contains(c.From, "weatherTool") || !strings.Contains(c.From, "sqlTool") {
			t.Errorf("the before-state must be the node's discovered tool set, got %q", c.From)
		}
		if !strings.Contains(c.To, "weatherTool") || strings.Contains(c.To, "sqlTool") {
			t.Errorf("the after-state must be the kept set, got %q", c.To)
		}
		if hasDimChange(p.SpecDiff, "skills") {
			t.Error("pruning a provider tool produced a SKILL change")
		}
	})

	t.Run("unbinding and reranking are told apart", func(t *testing.T) {
		other := strings.Repeat("8", 64)
		bound := &variantspec.VariantSpec{
			SourceRevision: "rev1", Order: []string{node},
			Nodes: map[string]variantspec.NodeOverride{node: {SkillRefs: []string{skillRef, other}}},
		}

		unbind := cloneSpec(bound)
		removeSkill(unbind, node, other)
		p := Present(Compiled{Candidate: Candidate{Operator: OpRemoveSkill, NodeID: node, Spec: unbind}}, bound)
		if c := findDimChange(t, p.SpecDiff, "skills"); c.Kind != KindSkillUnbind {
			t.Errorf("a removal must be labelled %q, got %q", KindSkillUnbind, c.Kind)
		}

		rerank := cloneSpec(bound)
		rerank.Nodes[node] = variantspec.NodeOverride{SkillRefs: []string{other, skillRef}}
		p = Present(Compiled{Candidate: Candidate{Operator: OpAddRerank, NodeID: node, Spec: rerank}}, bound)
		c := findDimChange(t, p.SpecDiff, "skills")
		if c.Kind != KindSkillRerank {
			t.Fatalf("the same skills in a new order must be labelled %q, got %q", KindSkillRerank, c.Kind)
		}
		// A rerank changes config_hash, so it is a real change — it must never render as "nothing moved".
		if c.Kind.Legible() == "" {
			t.Error("a rerank rendered with no sentence")
		}
	})
}

// ── 8.2 a refusal is a NAMED surface, not a diff that looks complete ─────────────────────────────

func TestRefusalIsNamedNotSwallowed(t *testing.T) {
	root := targetRoot(t)
	sites, err := discovery.IndexGoCallSites(root, nil)
	if err != nil {
		t.Fatalf("IndexGoCallSites: %v", err)
	}
	var agentID string
	for id, s := range sites {
		if strings.Contains(readSymbol(t, root, s), "agent") {
			agentID = id
		}
	}
	if agentID == "" {
		t.Fatal("fixture node `agent` not found")
	}

	// A skill whose sealed contract this engine cannot materialize into a call-site literal. The
	// transform refuses it by name (skillbind.go); the surface has to carry that refusal.
	entry, err := registry.NewSkillEntry(strings.Repeat("5", 64), "opaque", registry.SkillSpec{
		ImplHandle: "builtin:opaque", InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object"}`)})
	if err != nil {
		t.Fatalf("NewSkillEntry: %v", err)
	}
	resolved := &variantspec.Resolved{
		ConfigHash: strings.Repeat("c", 64), SourceRevision: "rev1", Language: "go",
		Overrides: map[string]variantspec.ResolvedOverride{
			agentID: {Skills: []*registry.SkillEntry{entry}},
		},
	}
	comp := Compiler{Resolver: fixedResolver{resolved}, Root: root, Build: okBuild{}, IR: surfaceIR(agentID)}

	got, err := comp.Compile(context.Background(), Candidate{
		Operator: OpAddSkill, NodeID: agentID, Dimensions: []string{"skills"}, Spec: baseSpec()})

	// 🔴 The refusal does NOT abort the batch. A refusal returned as an error takes every other
	// candidate down with it and lands in a log the user never opens.
	if err != nil {
		t.Fatalf("a refusal must be a verdict about the candidate, not a compiler failure: %v", err)
	}
	if got.BuildStatus != BuildRefused {
		t.Fatalf("want BuildStatus %q, got %q", BuildRefused, got.BuildStatus)
	}
	if got.Surfaceable() {
		t.Error("a refused candidate must never be ranked or verified")
	}

	// 🚫 And NO diff was produced. A partial diff alongside a refusal is exactly the "looks complete"
	// failure D-14.3 forbids.
	if got.Patch != nil {
		t.Fatal("a refusal shipped a diff")
	}

	if !got.Refusal.Refused() {
		t.Fatal("the refusal was swallowed: BuildStatus says refused but nothing says why")
	}
	if got.Refusal.NodeID != agentID {
		t.Errorf("the refusal must name the node, got %q", got.Refusal.NodeID)
	}
	if got.Refusal.Dimension != "skills" {
		t.Errorf("the refusal must name the dimension, got %q", got.Refusal.Dimension)
	}
	if !strings.Contains(got.Refusal.Reason, "properties") {
		t.Errorf("the refusal must carry the transform's own reason verbatim, got %q", got.Refusal.Reason)
	}

	// It survives into the presentation, which is what the console renders.
	pres := Present(got, baseSpec())
	if pres.Refusal == nil {
		t.Fatal("the presentation dropped the refusal; a refusal the surface omits reads as a change that happened")
	}
	if pres.SourceDiff != "" {
		t.Error("the presentation carries a source diff for a refused change")
	}
}

// A genuine failure is NOT dressed up as a refusal. Mis-classifying one would report "we chose not to"
// about something that broke, which is worse than either message alone.
func TestRealFailureIsNotReportedAsARefusal(t *testing.T) {
	comp := Compiler{Resolver: erroringResolver{}, Root: t.TempDir(), Build: okBuild{}}
	_, err := comp.Compile(context.Background(), Candidate{
		Operator: OpAddSkill, NodeID: "n1", Dimensions: []string{"skills"}, Spec: baseSpec()})
	if err == nil {
		t.Fatal("an infrastructure failure must propagate as an error, not be recorded as a refusal")
	}
}

type erroringResolver struct{}

func (erroringResolver) Resolve(*variantspec.VariantSpec) (*variantspec.Resolved, error) {
	return nil, context.DeadlineExceeded
}

func findDimChange(t *testing.T, changes []DimChange, dim string) DimChange {
	t.Helper()
	for _, c := range changes {
		if c.Dimension == dim {
			return c
		}
	}
	t.Fatalf("no %q change in the spec diff: %+v", dim, changes)
	return DimChange{}
}

func hasDimChange(changes []DimChange, dim string) bool {
	for _, c := range changes {
		if c.Dimension == dim {
			return true
		}
	}
	return false
}
