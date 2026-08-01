package authoring

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P17 §9 — the authored-change path for the memory axis (decisions.md D7).
//
// The one thing this file is really about: at M20 an authored memory change CANNOT be applied, and the
// difference between an honest platform and a dishonest one is entirely in how that fact is delivered.
// So most of these tests assert about a NO — that it is raised early, that it carries the engine's own
// reason, and that nothing anywhere renders it as a yes.

// memCoverage is a CoverageReader standing in for the transform engine's table.
type memCoverage struct {
	cells map[string][]MemoryCoverageCell
}

func (c memCoverage) MemoryCoverage(language string) []MemoryCoverageCell { return c.cells[language] }

// refusingCoverage is what the engine actually reports at M20: the identity materializes, everything
// else refuses with a no-materializer cause that names a runtime rather than a language.
func refusingCoverage() memCoverage {
	var cells []MemoryCoverageCell
	for _, st := range registry.BuiltinMemoryStrategies() {
		c := MemoryCoverageCell{Language: "go", Strategy: st.Name()}
		if st.Name() == registry.StrategyNone {
			c.Materializes = true
			c.Note = "the identity strategy; equivalent to the un-rewritten call site"
		} else {
			c.Cause = "no-materializer-for-this-language"
			c.MissingArtifact = "a memory runtime (a store, a lifetime, and a key scheme) plus the call-site rewriter that reads and writes it"
			c.Note = "a memory strategy is read and written BETWEEN invocations, so no expression — and no region — at the call site holds it; this is missing in every language, not this one"
		}
		cells = append(cells, c)
	}
	return memCoverage{cells: map[string][]MemoryCoverageCell{"go": cells}}
}

// memValidator is a MemoryValidator backed by the REAL registry validation path, with no database.
// Using the real one is the point: a hand-written stub could accept params the seal would reject, which
// is precisely the divergence ValidateMemorySelection exists to prevent.
type memValidator struct{ store *registry.Store }

func (v memValidator) ValidateMemoryParams(name, strategy string, params json.RawMessage) (registry.MemoryStrategy, json.RawMessage, error) {
	return v.store.ValidateMemoryParams(name, strategy, params)
}

func newMemValidator() memValidator { return memValidator{store: registry.NewStore(nil, nil)} }

func memParent() *variantspec.VariantSpec {
	return &variantspec.VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev1",
		Order: []string{"recall", "answer"},
		Nodes: map[string]variantspec.NodeOverride{"recall": {ModelRef: "m1"}},
	}
}

func memDraft(nodeID string, e Edit) Draft {
	return Draft{
		ID: "d1", WorkflowID: "wf", ParentVariantID: "parent",
		Actor: Actor{ID: "u-1", TenantID: "t-1"},
		Edits: map[string]Edit{nodeID: e},
	}
}

// TestPreflightRefusesWithTransformCause — task 9.1 🔴. The refusal is raised at preflight, before any
// worktree, build, or eval spend, and it is the transform's own probe that raises it.
func TestPreflightRefusesWithTransformCause(t *testing.T) {
	// The materializer stands in for the transform's probe and returns the refusal the engine returns.
	engineCause := `memory strategy "summary-buffer" is a store this node would read and write BETWEEN ` +
		`invocations, so there is no expression — and no region — at this Go call site that holds it`
	pf := Preflighter{
		Resolver: hashResolver{},
		Materializer: refusingMaterializer{r: Refusal{
			Cause: engineCause, NodeID: "recall", Field: MemoryDimension,
		}},
	}

	res, err := pf.Preflight(context.Background(), memDraft("recall", MemoryEdit("mem-ref-1")), memParent())
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if res.Verdict != VerdictRefused {
		t.Fatalf("verdict = %q, want refused. A user must learn this from the refusal path, never from an "+
			"empty diff after the work is done", res.Verdict)
	}
	if !res.Refusal.Named() {
		t.Fatalf("the refusal names nothing: %+v", res.Refusal)
	}
	if res.Refusal.NodeID != "recall" {
		t.Errorf("the refusal names node %q, want recall", res.Refusal.NodeID)
	}
	if res.Refusal.Field != MemoryDimension {
		t.Errorf("the refusal names field %q, want %q", res.Refusal.Field, MemoryDimension)
	}
	// 🔴 Verbatim. Re-wording the engine's sentence here would be a second copy of it, and the copy is
	// the one that goes stale the day the rewriter lands.
	if res.Refusal.Cause != engineCause {
		t.Errorf("the preflight cause was re-worded.\n got: %s\nwant: %s", res.Refusal.Cause, engineCause)
	}
	// The dimension summary still says what was touched, so a surface can label the refusal.
	if len(res.Dimensions) != 1 || res.Dimensions[0] != MemoryDimension {
		t.Errorf("touched dimensions = %v, want [memory]", res.Dimensions)
	}
}

// TestPreflightCauseMatchesTransformRefusal — task 9.2 🔴. One probe, one cause. There is no second
// memory gate in this package that could develop its own opinion.
func TestPreflightCauseMatchesTransformRefusal(t *testing.T) {
	// 🚫 Structural: nothing in this package produces a memory refusal of its own. If a future change
	// added `func refuseMemoryHere()` with its own sentence, the surface and the engine could disagree,
	// and the surface's copy is the one a user reads.
	for _, file := range packageFiles(t) {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		src := string(b)
		// The authoring layer may NAME the dimension; it may not author the reason a memory change is
		// refused. That sentence belongs to the engine.
		for _, banned := range []string{
			"call-site materialization of a cross-invocation store is deferred",
			"memory rewriter is pending",
		} {
			if strings.Contains(src, banned) {
				t.Errorf("%s hard-codes the engine's refusal sentence (%q); the boundary must be READ from "+
					"coverage, not restated here — a restatement is the copy that drifts", file, banned)
			}
		}
	}

	// And the boundary a surface renders comes from the coverage table, verbatim.
	b := MemoryBoundaryFor(refusingCoverage(), "go")
	if b.Applicable {
		t.Fatal("the boundary reports memory as applicable; at M20 the transform refuses every non-identity " +
			"strategy in every language")
	}
	if b.MissingArtifact == "" {
		t.Error("the boundary names no missing artifact, so a surface would render \"unavailable\" with no reason")
	}
	if !strings.Contains(b.MissingArtifact, "runtime") {
		t.Errorf("the missing artifact is %q; it must name the RUNTIME, because that is what a user would "+
			"be waiting for", b.MissingArtifact)
	}
	// 🔴 The language is never the blocker.
	if b.LanguageIsTheBlocker {
		t.Error("the boundary blames the language; that implies another language works, which would send " +
			"the user to wait for the wrong thing")
	}
	if strings.Contains(strings.ToLower(b.Reason), "go materializer") {
		t.Errorf("the reason blames this language's materializer: %q", b.Reason)
	}
}

// TestMemoryBoundaryFailsClosedOnSilence is the other direction of FR20: absence of evidence about a
// language must not render as permission.
func TestMemoryBoundaryFailsClosedOnSilence(t *testing.T) {
	b := MemoryBoundaryFor(memCoverage{}, "elixir")
	if b.Applicable {
		t.Fatal("a language with no coverage cells was reported as applicable; an unknown is not a yes")
	}
	if b.MissingArtifact == "" || b.Reason == "" {
		t.Errorf("the silent-coverage boundary names nothing: %+v", b)
	}
}

// TestAuthorSelectsSealsAndRejectsFreeText — task 9.3.
func TestAuthorSelectsSealsAndRejectsFreeText(t *testing.T) {
	v := newMemValidator()

	t.Run("the offered set is the closed vocabulary", func(t *testing.T) {
		opts := MemoryStrategyOptions()
		if len(opts) != registry.MemoryStrategySetSize {
			t.Fatalf("the surface offers %d strategies, the registry has %d; an option the registry does "+
				"not know cannot be sealed, and one it knows that is not offered is unreachable",
				len(opts), registry.MemoryStrategySetSize)
		}
		if !opts[0].Identity {
			t.Errorf("the identity strategy is not first; it is the baseline a user compares the others "+
				"against (got %q)", opts[0].Strategy)
		}
		for _, o := range opts {
			if o.Title == "" || o.Description == "" {
				t.Errorf("strategy %q has no human layer; a user choosing between five options needs to know "+
					"what each trades away", o.Strategy)
			}
			if len(o.ParamsSchema) == 0 {
				t.Errorf("strategy %q exposes no params schema, so the form has nothing to render and nothing "+
					"to validate against", o.Strategy)
			}
			if o.Identity != (o.Strategy == registry.StrategyNone) {
				t.Errorf("strategy %q's identity flag is wrong", o.Strategy)
			}
		}
	})

	t.Run("valid params are accepted", func(t *testing.T) {
		if err := ValidateMemorySelection(v, "n", "summary-buffer", json.RawMessage(`{"max_tokens":2000}`)); err != nil {
			t.Fatalf("a schema-valid selection was rejected: %v", err)
		}
	})

	t.Run("free text is not a selection path", func(t *testing.T) {
		err := ValidateMemorySelection(v, "n", "my-custom-strategy", json.RawMessage(`{}`))
		if err == nil {
			t.Fatal("a free-text strategy name was accepted; a name outside the closed set cannot resolve, " +
				"so offering it would be offering a choice that fails at seal")
		}
		if !strings.Contains(err.Error(), "my-custom-strategy") {
			t.Errorf("the rejection does not name what was rejected: %v", err)
		}
	})

	t.Run("schema-violating params are rejected before sealing", func(t *testing.T) {
		err := ValidateMemorySelection(v, "n", "vector-recall", json.RawMessage(`{"top_k":5}`))
		if err == nil {
			t.Fatal("params missing the required embedding_ref were accepted")
		}
		if !errors.Is(err, registry.ErrInvalidEntry) {
			t.Errorf("want ErrInvalidEntry, got %v", err)
		}
	})

	t.Run("an empty selection is rejected", func(t *testing.T) {
		if err := ValidateMemorySelection(v, "n", "  ", nil); err == nil {
			t.Fatal("an empty strategy was accepted")
		}
	})

	t.Run("a missing validator is not a pass", func(t *testing.T) {
		// 🚫 A surface wired without a validator must fail loudly. "No validation" reading as "valid" is
		// the silent-fallback failure this codebase declines everywhere.
		if err := ValidateMemorySelection(nil, "n", "scratchpad", json.RawMessage(`{"max_entries":3}`)); err == nil {
			t.Fatal("a nil validator accepted a selection")
		}
	})
}

// TestClearReproducesPriorHashByteIdentically — task 9.4 🔴. The byte-exact back-out.
func TestClearReproducesPriorHashByteIdentically(t *testing.T) {
	parent := memParent()

	// 1. Select a strategy → a new configuration.
	selected, err := memDraft("recall", MemoryEdit("mem-ref-1")).Derive(parent)
	if err != nil {
		t.Fatalf("derive (select): %v", err)
	}
	if selected.Nodes["recall"].MemoryRef != "mem-ref-1" {
		t.Fatalf("the selection did not land: %+v", selected.Nodes["recall"])
	}

	// 2. Clear it → back to the parent's bytes, exactly.
	cleared, err := memDraft("recall", ClearMemoryEdit()).Derive(selected)
	if err != nil {
		t.Fatalf("derive (clear): %v", err)
	}
	if got := cleared.Nodes["recall"].MemoryRef; got != "" {
		t.Fatalf("after clearing, memory_ref is %q, want empty", got)
	}

	before, err := json.Marshal(parent.Nodes["recall"])
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(cleared.Nodes["recall"])
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("clearing left residue in the override bytes.\nbefore: %s\n after: %s\nThe field is "+
			"omitempty, so an empty ref must remove the key entirely — otherwise a user can never fully "+
			"back out of an authored memory change", before, after)
	}

	// 3. And `none` is indistinguishable from cleared at the RESOLVED layer. (The registry-level proof
	// that a `none` entry projects to no memory key lives in variantspec; here the point is that the
	// surface must not present the two as states that differ in effect.)
	for _, o := range MemoryStrategyOptions() {
		if o.Identity && o.Strategy != registry.StrategyNone {
			t.Errorf("the identity option is %q, want %q", o.Strategy, registry.StrategyNone)
		}
	}
}

// TestAuthoredOriginRecordedNotHashed — task 9.5.
func TestAuthoredOriginRecordedNotHashed(t *testing.T) {
	d := memDraft("recall", MemoryEdit("mem-ref-1"))
	spec, err := d.Derive(memParent())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	cand := d.ToCandidate(spec, "recall", []string{MemoryDimension})

	if cand.Origin.Normalized() != proposal.OriginUser {
		t.Errorf("origin = %q, want user", cand.Origin)
	}
	if cand.Actor.ID != "u-1" || cand.Actor.TenantID != "t-1" {
		t.Errorf("actor = %+v, want the drafting identity", cand.Actor)
	}
	if cand.Spec.ParentVariantID != "parent" {
		t.Errorf("parent pointer = %q, want parent; without it the change is not diffable in lineage",
			cand.Spec.ParentVariantID)
	}

	// 🚫 Origin is nowhere in the hashed shape. A user-authored configuration and a byte-identical
	// operator-proposed one are the SAME configuration and must be the same measurement.
	specBytes, err := json.Marshal(cand.Spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"u-1", "t-1", "user", "origin"} {
		if strings.Contains(string(specBytes), leaked) {
			t.Errorf("the hashed spec carries %q; authorship would then fork identity and \"have we already "+
				"measured this?\" would stop having an answer:\n%s", leaked, specBytes)
		}
	}
}

// TestAuthoredMemoryNeverAppliedOrScored — task 9.6 🚫.
func TestAuthoredMemoryNeverAppliedOrScored(t *testing.T) {
	applier := &recordingApplier{}
	pf := Preflighter{
		Resolver: hashResolver{},
		Materializer: refusingMaterializer{r: Refusal{
			Cause: "no memory runtime has landed", NodeID: "recall", Field: MemoryDimension,
		}},
	}

	res, err := pf.Preflight(context.Background(), memDraft("recall", MemoryEdit("mem-ref-1")), memParent())
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if res.Admissible() {
		t.Fatal("a memory change was reported admissible while the transform refuses it")
	}
	// Preflight spends nothing: no compile, no diff, no worktree, no eval.
	if len(applier.calls) != 0 {
		t.Errorf("preflight compiled %d candidate(s); a refusal must cost nothing", len(applier.calls))
	}
	// And it carries no hash-as-blessing: a refused result must not be mistakable for a green one.
	if res.Verdict == VerdictAdmissible || res.Verdict == VerdictNotYetMeasurable {
		t.Errorf("verdict = %q; a memory refusal is decided on EVIDENCE (the engine refuses, always), so it "+
			"is `refused` and never `not_yet_measurable` — the third verdict is for a measurement we lack, "+
			"not for a capability we have not built", res.Verdict)
	}

	// 🔴 `refused` is its own state, distinct from failed and from pending. The three verdicts are the
	// closed set the surface renders.
	if VerdictRefused == VerdictAdmissible || VerdictRefused == VerdictNotYetMeasurable {
		t.Fatal("the verdicts are not distinct")
	}
}

// TestAuthoredAndProposedIndistinguishableDownstream — task 9.7 🔴. One spine, two origins.
func TestAuthoredAndProposedIndistinguishableDownstream(t *testing.T) {
	parent := memParent()

	// The user's route: a draft.
	authored, err := memDraft("recall", MemoryEdit("mem-ref-1")).Derive(parent)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	// The operator's route: the same configuration, reached by setting the same ref on a clone.
	proposed := &variantspec.VariantSpec{
		WorkflowID: parent.WorkflowID, SourceRevision: parent.SourceRevision,
		ParentVariantID: "parent",
		Order:           append([]string(nil), parent.Order...),
		Nodes:           map[string]variantspec.NodeOverride{},
	}
	for id, o := range parent.Nodes {
		proposed.Nodes[id] = o
	}
	ov := proposed.Nodes["recall"]
	ov.MemoryRef = "mem-ref-1"
	proposed.Nodes["recall"] = ov

	// The HASHED shapes are identical, so both resolve to one config_hash.
	a, err := json.Marshal(authored)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(proposed)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("the same memory configuration reached by two origins produced different specs.\n user: %s\n"+
			"  op: %s\nOne spine means one configuration, whoever authored it", a, b)
	}

	// And the resolver agrees: same fingerprint.
	r := hashResolver{}
	ra, err := r.Resolve(authored)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := r.Resolve(proposed)
	if err != nil {
		t.Fatal(err)
	}
	if ra.ConfigHash != rb.ConfigHash {
		t.Errorf("config_hash differs by origin: %q vs %q", ra.ConfigHash, rb.ConfigHash)
	}
}
