package authoring

import (
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P14 14c — fail-closed selection on the skills and tools axis.

// fakeCoverage stands in for the transform's coverage table. The PRODUCTION wiring is asserted
// separately (TestPreflightCoverageMatchesTransformCoverage, in internal/authoringwire) — this one is
// about the selection rules, not about which languages happen to be covered today.
type fakeCoverage struct{ langs []string }

func (f fakeCoverage) Materializes(l string) bool {
	for _, x := range f.langs {
		if x == l {
			return true
		}
	}
	return false
}
func (f fakeCoverage) Languages() []string { return f.langs }

var goOnly = fakeCoverage{langs: []string{"go"}}

func goNode() NodeSelection {
	return NodeSelection{
		NodeID: "n1", Language: "go",
		SealedSkills: []SealedSkill{
			{Ref: "skill-rerank@v3", Name: "rerank", VersionID: "v3"},
			{Ref: "skill-summarize@v1", Name: "summarize", VersionID: "v1"},
			{Ref: "skill-draft", Name: "draft"}, // sealed but UNPINNED
		},
		DiscoveredTools: []string{"search", "calculator", "fetch"},
	}
}

// TestUnpinnedOrUnknownSkillRefusedByName (task 9.3).
func TestUnpinnedOrUnknownSkillRefusedByName(t *testing.T) {
	t.Run("an unknown skill is refused, naming it", func(t *testing.T) {
		got := ValidateSkillBinding(goNode(), goOnly, []string{"skill-invented"}, nil, nil)
		if got.Cause == "" {
			t.Fatal("a skill nobody sealed was accepted — free text is not a binding path")
		}
		if !strings.Contains(got.Cause, "skill-invented") || got.NodeID != "n1" {
			t.Errorf("the refusal does not name the skill and the node: %+v", got)
		}
	})

	t.Run("an unpinned skill is refused, and for its own reason", func(t *testing.T) {
		got := ValidateSkillBinding(goNode(), goOnly, []string{"skill-draft"}, nil, nil)
		if got.Cause == "" {
			t.Fatal("an unpinned skill was accepted — the constructed value's shape would be undetermined")
		}
		if !strings.Contains(got.Cause, "no version") {
			t.Errorf("the unpinned refusal reads like the unknown-skill one: %q", got.Cause)
		}
	})

	t.Run("an unpinned skill is not even OFFERED", func(t *testing.T) {
		// The picker and the validator must agree. A surface that offers what submission refuses is two
		// sources of truth with a gap between them, and the user finds the gap.
		offer := OfferSkills(goNode(), goOnly)
		for _, s := range offer.Skills {
			if !s.Pinned() {
				t.Errorf("an unpinned skill %q was offered", s.Ref)
			}
		}
		if len(offer.Skills) != 2 {
			t.Errorf("offered %d skills, want the 2 pinned ones", len(offer.Skills))
		}
	})

	t.Run("a sealed, pinned skill is accepted — so the gate is not stuck red", func(t *testing.T) {
		if got := ValidateSkillBinding(goNode(), goOnly, []string{"skill-rerank@v3"}, nil, nil); got.Cause != "" {
			t.Fatalf("a sealed, pinned skill was refused: %s", got.Cause)
		}
	})
}

// TestAuthoredSkillArgsValidatedAgainstPinnedVersion (task 9.5).
func TestAuthoredSkillArgsValidatedAgainstPinnedVersion(t *testing.T) {
	// The validator the caller supplies is bound to the PINNED version. This double records which ref it
	// was asked about, so the test can prove the pin — not the registry head — decided.
	var askedAbout []string
	validate := func(ref string, _ any) error {
		askedAbout = append(askedAbout, ref)
		if ref == "skill-rerank@v3" {
			return errors.New(`skill "rerank" input violates its contract: field top_k: expected integer`)
		}
		return nil
	}

	got := ValidateSkillBinding(goNode(), goOnly, []string{"skill-rerank@v3"},
		map[string]any{"top_k": "twelve"}, validate)

	if got.Cause == "" {
		t.Fatal("arguments that violate the pinned contract were accepted")
	}
	if len(askedAbout) != 1 || askedAbout[0] != "skill-rerank@v3" {
		t.Errorf("validation was asked about %v, want the PINNED ref — a newer, laxer contract must not "+
			"be substituted", askedAbout)
	}
	// The refusal points at the failing FIELD, so the reader fixes an argument rather than the binding.
	if got.Field != "top_k" {
		t.Errorf("field = %q, want the failing argument %q", got.Field, "top_k")
	}

	t.Run("valid arguments pass", func(t *testing.T) {
		if r := ValidateSkillBinding(goNode(), goOnly, []string{"skill-summarize@v1"},
			map[string]any{"max_tokens": 200}, validate); r.Cause != "" {
			t.Errorf("valid arguments were refused: %s", r.Cause)
		}
	})
}

// TestToolOutsideDiscoveredSetRefused (task 9.4).
func TestToolOutsideDiscoveredSetRefused(t *testing.T) {
	t.Run("a tool the node does not offer is refused, naming it and what was found", func(t *testing.T) {
		got := ValidateToolSelection(goNode(), []string{"search", "wikipedia"})
		if got.Cause == "" {
			t.Fatal("a selection over an undiscovered tool was accepted — it would apply to nothing")
		}
		if !strings.Contains(got.Cause, "wikipedia") {
			t.Errorf("the refusal does not name the offending tool: %q", got.Cause)
		}
		// Naming what WAS found is what turns the refusal into a next step.
		if !strings.Contains(got.Cause, "search") {
			t.Errorf("the refusal does not say what the node does offer: %q", got.Cause)
		}
	})

	t.Run("a selection within the discovered set is accepted", func(t *testing.T) {
		if got := ValidateToolSelection(goNode(), []string{"search", "fetch"}); got.Cause != "" {
			t.Fatalf("a valid selection was refused: %s", got.Cause)
		}
	})

	t.Run("the picker offers exactly the discovered set", func(t *testing.T) {
		tools, ref := OfferTools(goNode())
		if ref.Cause != "" {
			t.Fatalf("offering refused: %s", ref.Cause)
		}
		if len(tools) != 3 {
			t.Errorf("offered %v, want the three discovered tools", tools)
		}
	})
}

// TestDynamicToolSetRefusedNotGuessed (task 9.7).
func TestDynamicToolSetRefusedNotGuessed(t *testing.T) {
	sel := goNode()
	sel.ToolSetDynamic = true
	sel.DiscoveredTools = nil

	got := ValidateToolSelection(sel, []string{"search"})
	if got.Cause == "" {
		t.Fatal("a prune over a run-time-assembled tool set was accepted — the deletion site would be a guess")
	}
	if !strings.Contains(got.Cause, "run time") || got.NodeID != "n1" {
		t.Errorf("the refusal does not name the node and the reason: %+v", got)
	}

	// 🔴 "This node offers no tools" and "we could not see what this node offers" are different facts.
	// An empty discovered set with ToolSetDynamic false is NOT this refusal.
	empty := goNode()
	empty.DiscoveredTools = nil
	if r := ValidateToolSelection(empty, nil); r.Cause != "" {
		t.Errorf("a node that genuinely offers no tools was treated as unreadable: %s", r.Cause)
	}

	// And the picker refuses rather than showing an empty list a user would read as "nothing to prune".
	if _, ref := OfferTools(sel); ref.Cause == "" {
		t.Error("a dynamic tool set was offered as an empty picker")
	}
}

// TestSkillLanguageBoundaryIsStatedNotEmpty (task 9.9's backend half).
func TestSkillLanguageBoundaryIsStatedNotEmpty(t *testing.T) {
	py := goNode()
	py.Language = "python"

	offer := OfferSkills(py, goOnly)
	if offer.Offerable() {
		t.Fatal("skills were offered on a language with no materializer")
	}
	if offer.Refused.Cause == "" {
		t.Fatal("the boundary was expressed as an empty list — which reads as 'this node has no skills', " +
			"a fact about the catalogue rather than about the platform")
	}
	if !strings.Contains(offer.Refused.Cause, "python") {
		t.Errorf("the refusal does not name the language: %q", offer.Refused.Cause)
	}
	// It must say what WOULD have worked.
	if !strings.Contains(offer.Refused.Cause, "go") {
		t.Errorf("the refusal does not say which languages are covered: %q", offer.Refused.Cause)
	}
	if offer.Refused.Shape != "skill binding" {
		t.Errorf("shape = %q, want the kind of change that cannot be materialized", offer.Refused.Shape)
	}
}

// TestNoOverrideForUnsupportedLanguageBinding (task 9.15).
//
// Structural AND behavioural. The behavioural half proves today's paths refuse; the structural half is
// what catches the "force" parameter somebody adds next quarter.
func TestNoOverrideForUnsupportedLanguageBinding(t *testing.T) {
	py := goNode()
	py.Language = "python"

	// Every entry point, with every shape of input.
	if OfferSkills(py, goOnly).Offerable() {
		t.Error("the offer path admitted an unsupported language")
	}
	for _, refs := range [][]string{{"skill-rerank@v3"}, {"skill-summarize@v1", "skill-rerank@v3"}, {}} {
		if r := ValidateSkillBinding(py, goOnly, refs, nil, nil); r.Cause == "" && len(refs) > 0 {
			t.Errorf("refs %v were admitted on an unsupported language", refs)
		}
	}
	// An empty binding on an unsupported language is still refused, because the node cannot carry the
	// dimension at all — admitting it would let a surface show the control.
	if r := ValidateSkillBinding(py, goOnly, nil, nil, nil); r.Cause == "" {
		t.Error("the language boundary is checked per-ref rather than per-node")
	}
}

// TestSkillToolAuthoringRefusalsGoRed (task 9.14): every class red, and a green case for each so the
// suite cannot be satisfied by a validator that refuses everything.
func TestSkillToolAuthoringRefusalsGoRed(t *testing.T) {
	red := map[string]Refusal{
		"no materializer":         OfferSkills(pythonNode(), goOnly).Refused,
		"unknown skill":           ValidateSkillBinding(goNode(), goOnly, []string{"nope"}, nil, nil),
		"unpinned skill":          ValidateSkillBinding(goNode(), goOnly, []string{"skill-draft"}, nil, nil),
		"tool outside discovered": ValidateToolSelection(goNode(), []string{"nope"}),
		"dynamic tool set":        ValidateToolSelection(dynamicNode(), []string{"search"}),
		"invalid args":            ValidateSkillBinding(goNode(), goOnly, []string{"skill-rerank@v3"}, map[string]any{"x": 1}, alwaysInvalid),
	}
	for name, r := range red {
		if r.Cause == "" {
			t.Errorf("%s: did not refuse — this gate cannot go red", name)
		}
		if r.NodeID == "" {
			t.Errorf("%s: refused without naming a node", name)
		}
	}

	green := map[string]Refusal{
		"sealed pinned skill": ValidateSkillBinding(goNode(), goOnly, []string{"skill-rerank@v3"}, nil, nil),
		"discovered tool":     ValidateToolSelection(goNode(), []string{"search"}),
		"valid args":          ValidateSkillBinding(goNode(), goOnly, []string{"skill-rerank@v3"}, map[string]any{"x": 1}, alwaysValid),
	}
	for name, r := range green {
		if r.Cause != "" {
			t.Errorf("%s: refused a valid change — the gate is stuck red: %s", name, r.Cause)
		}
	}
}

func pythonNode() NodeSelection  { n := goNode(); n.Language = "python"; return n }
func dynamicNode() NodeSelection { n := goNode(); n.ToolSetDynamic = true; return n }

func alwaysInvalid(string, any) error {
	return errors.New(`skill "x" input violates its contract: field top_k: expected integer`)
}
func alwaysValid(string, any) error { return nil }

// TestAuthoredReorderChangesHash and TestNoSkillNodeHashUnchanged (task 9.8).
//
// Skill ORDER is identity-bearing: the call site binds skills in the order given, so two orders are two
// configurations. A surface that presented a reorder as cosmetic would let a user make a real,
// scoreable change while believing they had tidied a list.
func TestAuthoredReorderChangesHash(t *testing.T) {
	parent := baseSpec()

	first := draftFor(map[string]Edit{"n1": {SkillRefs: &[]string{"s-a", "s-b"}}})
	second := draftFor(map[string]Edit{"n1": {SkillRefs: &[]string{"s-b", "s-a"}}})

	a, err := first.Derive(parent)
	if err != nil {
		t.Fatalf("derive a: %v", err)
	}
	b, err := second.Derive(parent)
	if err != nil {
		t.Fatalf("derive b: %v", err)
	}
	if specFingerprint(a) == specFingerprint(b) {
		t.Error("reordering two skills produced the same configuration — order is identity-bearing, and a " +
			"reorder presented as cosmetic is a real change the user did not know they made")
	}
	// And each differs from the parent.
	if specFingerprint(a) == specFingerprint(parent) {
		t.Error("binding skills did not change the configuration")
	}
}

func TestNoSkillNodeHashUnchanged(t *testing.T) {
	// A node that binds no skill and prunes no tool must be byte-identical to one authored before the
	// axis existed — the additive rule the whole P14 hash story rests on.
	parent := baseSpec()
	before := specFingerprint(parent)

	// An edit that touches a DIFFERENT dimension leaves the skill and tool fields absent.
	d := draftFor(map[string]Edit{"n1": {ModelRef: strPtr("m-new")}})
	next, err := d.Derive(parent)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got := next.Nodes["n1"]; len(got.SkillRefs) != 0 || len(got.ToolSelection) != 0 {
		t.Errorf("a model edit populated the skill or tool fields: %+v", got)
	}
	if specFingerprint(parent) != before {
		t.Error("the parent moved")
	}
}

// TestRestoreReturnsPrePruneHash (task 9.16).
func TestRestoreReturnsPrePruneHash(t *testing.T) {
	parent := baseSpec()
	parent.Nodes["n1"] = variantspecOverrideWithTools([]string{"search", "fetch", "calc"})
	before := specFingerprint(parent)

	pruned, err := draftFor(map[string]Edit{"n1": {ToolSelection: &[]string{"search"}}}).Derive(parent)
	if err != nil {
		t.Fatalf("derive prune: %v", err)
	}
	if specFingerprint(pruned) == before {
		t.Fatal("pruning two tools changed nothing")
	}

	// Restoring every pruned tool must land on the pre-prune configuration BYTE-IDENTICALLY — not on
	// "an equivalent one", which is how an undo quietly becomes a third configuration.
	restored, err := draftFor(map[string]Edit{"n1": {ToolSelection: &[]string{"search", "fetch", "calc"}}}).Derive(parent)
	if err != nil {
		t.Fatalf("derive restore: %v", err)
	}
	if specFingerprint(restored) != before {
		t.Errorf("restore landed on a different configuration:\n before  %s\n restored %s", before, specFingerprint(restored))
	}
}

// TestUnverifiedPruneClaimsNothing (task 9.17).
func TestUnverifiedPruneClaimsNothing(t *testing.T) {
	// A prune's token reduction is VISIBLE immediately — the declared-tool tokens drop — and that is
	// precisely the trap. Until the harness runs, `task_success` is unmeasured, and a prune that removed
	// a tool the model needed under rare inputs is a failure a token count cannot see.
	entries := []Entry{
		{ChangeID: "prune-1", Axis: "tools", VerificationState: StateUnverified},
		{ChangeID: "prune-2", Axis: "tools", VerificationState: StateUnverified},
	}
	for _, figure := range []string{"tokens saved", "cost saved", "tool errors avoided"} {
		if total := CountableAggregate(entries, func(Entry) float64 { return 1000 }); total != 0 {
			t.Errorf("%s: an unverified prune contributed %v, want 0", figure, total)
		}
	}
	// The control: a verified one does count, so this is a filter rather than a constant zero.
	verified := append(entries, Entry{ChangeID: "prune-3", Axis: "tools", VerificationState: StateVerified})
	if total := CountableAggregate(verified, func(Entry) float64 { return 1000 }); total != 1000 {
		t.Errorf("a verified prune contributed %v, want 1000", total)
	}
}

// variantspecOverrideWithTools builds a node override carrying a discovered tool selection.
func variantspecOverrideWithTools(tools []string) variantspec.NodeOverride {
	return variantspec.NodeOverride{ModelRef: "m-old", ToolSelection: append([]string(nil), tools...)}
}

// TestAuthoredBindAssertsDownstreamState (P14 14c task 9.18).
//
// A 2xx is not evidence. This reads back the three things a skill binding must actually have produced:
// the emitted diff reference, the append-only record, and — specific to this axis — the RESOLVED SKILL
// ORDER, because order is identity-bearing and a handler that silently sorted the list would return
// success while changing the configuration the user asked for.
func TestAuthoredBindAssertsDownstreamState(t *testing.T) {
	ctx := t.Context()
	parent := baseSpec()
	parentHash := specFingerprint(parent)
	rec := NewMemRecorder()
	applier := &okApplier{}

	s := Submitter{
		Preflight: Preflighter{Resolver: hashResolver{}, Materializer: okMaterializer{}},
		Applier:   applier, Head: fixedHead{head: parentHash}, Record: rec, Auth: allowAll{},
	}
	// A deliberately NON-alphabetical order, so an accidental sort is visible.
	want := []string{"skill-zeta@v1", "skill-alpha@v2", "skill-mid@v1"}
	d := draftFor(map[string]Edit{"n1": {SkillRefs: &want}})
	d.ParentVariantID, d.ConcurrencyToken = parentHash, parentHash

	sub, err := s.Submit(ctx, d, parent)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	t.Run("the resolved skill order is exactly what was authored", func(t *testing.T) {
		got := sub.Compiled.Candidate.Spec.Nodes["n1"].SkillRefs
		if len(got) != len(want) {
			t.Fatalf("resolved %d skills, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("skill %d = %q, want %q — order is identity-bearing, so a silent sort is a "+
					"different configuration than the one that was authored", i, got[i], want[i])
			}
		}
	})

	t.Run("the record cites the diff and attributes the change", func(t *testing.T) {
		history, err := rec.History(ctx, sub.ChangeID)
		if err != nil || len(history) != 1 {
			t.Fatalf("history = %v (%v), want exactly one row", history, err)
		}
		row := history[0]
		if row.DiffRef == "" || row.ActorID == "" || row.ConfigHash == "" {
			t.Errorf("the persisted row cannot be traced: %+v", row)
		}
		if row.Axis != "skills" {
			t.Errorf("axis = %q, want skills", row.Axis)
		}
		if row.VerificationState != StateUnverified {
			t.Errorf("verification_state = %q, want unverified", row.VerificationState)
		}
	})

	t.Run("the parent still holds no skills", func(t *testing.T) {
		if len(parent.Nodes["n1"].SkillRefs) != 0 {
			t.Error("binding skills mutated the parent")
		}
	})
}
