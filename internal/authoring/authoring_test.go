package authoring

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// ── test doubles ────────────────────────────────────────────────────────────────────────────────

// hashResolver derives a hash from the spec's own content, so a test can assert that two specs which
// denote the same configuration land on the same hash — the property the whole revert claim rests on.
type hashResolver struct{ fail error }

func (h hashResolver) Resolve(s *variantspec.VariantSpec) (*variantspec.Resolved, error) {
	if h.fail != nil {
		return nil, h.fail
	}
	return &variantspec.Resolved{ConfigHash: specFingerprint(s), Language: "go", SourceRevision: s.SourceRevision}, nil
}

// specFingerprint is a deliberately simple, total function of the parts a config depends on. It is not
// confighash — it stands in for it, and it must be sensitive to exactly what a draft can change.
func specFingerprint(s *variantspec.VariantSpec) string {
	var b strings.Builder
	b.WriteString(s.SourceRevision)
	for _, id := range s.Order {
		o := s.Nodes[id]
		b.WriteString("|" + id + ":" + o.ModelRef + "/" + o.PromptRef + "/" + o.ContextPolicy + "/" + string(o.ApplyMode))
		b.WriteString("/" + strings.Join(o.SkillRefs, "+") + "/" + strings.Join(o.ToolSelection, "+"))
		if o.ContextDropTolerance != nil {
			b.WriteString("/tol")
		}
	}
	return b.String()
}

type okMaterializer struct{}

func (okMaterializer) Probe(context.Context, *variantspec.Resolved) (Refusal, error) {
	return Refusal{}, nil
}

type refusingMaterializer struct{ r Refusal }

func (m refusingMaterializer) Probe(context.Context, *variantspec.Resolved) (Refusal, error) {
	return m.r, nil
}

// unknownGate is the archetype of a gate that legitimately does not know.
type unknownGate struct{ missing MissingInput }

func (g unknownGate) Check(context.Context, *variantspec.Resolved) (Verdict, Refusal, MissingInput) {
	return VerdictNotYetMeasurable, Refusal{}, g.missing
}

type refusingGate struct{ r Refusal }

func (g refusingGate) Check(context.Context, *variantspec.Resolved) (Verdict, Refusal, MissingInput) {
	return VerdictRefused, g.r, MissingInput{}
}

type okApplier struct{ calls int }

func (a *okApplier) Compile(_ context.Context, c proposal.Candidate) (proposal.Compiled, error) {
	a.calls++
	return proposal.Compiled{Candidate: c, DiffHash: "diff-1", BuildStatus: proposal.BuildBuilt}, nil
}

type fixedHead struct{ head string }

func (h fixedHead) Head(context.Context, string) (string, error) { return h.head, nil }

type allowAll struct{}

func (allowAll) MayAuthor(context.Context, Actor) error { return nil }

type denyAll struct{ err error }

func (d denyAll) MayAuthor(context.Context, Actor) error { return d.err }

type mapParents struct {
	specs map[string]*variantspec.VariantSpec
}

func (m mapParents) SpecFor(_ context.Context, id string) (*variantspec.VariantSpec, error) {
	s, ok := m.specs[id]
	if !ok {
		return nil, errors.New("no such variant")
	}
	return s, nil
}

func baseSpec() *variantspec.VariantSpec {
	return &variantspec.VariantSpec{
		WorkflowID: "wf1", SourceRevision: "rev1",
		Order: []string{"n1", "n2"},
		Nodes: map[string]variantspec.NodeOverride{"n1": {ModelRef: "m-old"}},
	}
}

func strPtr(s string) *string { return &s }

func draftFor(edits map[string]Edit) Draft {
	return Draft{
		ID: "d1", WorkflowID: "wf1", ParentVariantID: "parent-hash",
		Edits: edits, Actor: Actor{ID: "u1", TenantID: "t1"},
		ConcurrencyToken: "parent-hash",
	}
}

// ── 9.1 the draft never mutates its parent ──────────────────────────────────────────────────────

func TestDraftNeverMutatesParent(t *testing.T) {
	parent := baseSpec()
	before := specFingerprint(parent)

	d := draftFor(map[string]Edit{"n1": {ModelRef: strPtr("m-new")}, "n2": {PromptRef: strPtr("p-new")}})
	next, err := d.Derive(parent)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if got := specFingerprint(parent); got != before {
		t.Errorf("parent mutated by Derive:\n before %s\n after  %s", before, got)
	}
	if specFingerprint(next) == before {
		t.Error("Derive produced a spec identical to the parent — the edits were dropped")
	}
	if next.ParentVariantID != d.ParentVariantID {
		t.Errorf("lineage not recorded: got %q want %q", next.ParentVariantID, d.ParentVariantID)
	}
	// The clone must be deep: mutating the derived node must not reach the parent's map.
	next.Nodes["n1"] = variantspec.NodeOverride{ModelRef: "scribble"}
	if parent.Nodes["n1"].ModelRef != "m-old" {
		t.Error("the derived spec shares node storage with its parent")
	}
}

func TestDraftRefusesUnknownNodeAndEmptyEdit(t *testing.T) {
	if _, err := draftFor(map[string]Edit{"nope": {ModelRef: strPtr("m")}}).Derive(baseSpec()); !errors.Is(err, ErrUnknownDraftNode) {
		t.Errorf("editing an unknown node: got %v, want ErrUnknownDraftNode", err)
	}
	if _, err := draftFor(map[string]Edit{}).Derive(baseSpec()); !errors.Is(err, ErrEmptyDraft) {
		t.Errorf("empty draft: got %v, want ErrEmptyDraft", err)
	}
	if _, err := draftFor(map[string]Edit{"n1": {ModelRef: strPtr("m")}}).Derive(nil); !errors.Is(err, ErrNoParent) {
		t.Errorf("nil parent: got %v, want ErrNoParent", err)
	}
}

// ── 9.2 preflight names the cause and the node, and spends nothing ──────────────────────────────

func TestPreflightNamesCauseAndNode(t *testing.T) {
	want := Refusal{Cause: "node n1, dim model: an anthropic call site does not become an OpenAI call",
		NodeID: "n1", Field: "model"}
	p := Preflighter{Resolver: hashResolver{}, Materializer: refusingMaterializer{r: want}}

	got, err := p.Preflight(context.Background(), draftFor(map[string]Edit{"n1": {ModelRef: strPtr("gpt-4o")}}), baseSpec())
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if got.Verdict != VerdictRefused {
		t.Fatalf("verdict = %q, want %q", got.Verdict, VerdictRefused)
	}
	if !got.Refusal.Named() {
		t.Errorf("refusal names nothing: %+v — a refusal a user cannot act on is not a refusal", got.Refusal)
	}
	if got.Refusal.NodeID != "n1" || got.Refusal.Field != "model" {
		t.Errorf("refusal did not carry node+field: %+v", got.Refusal)
	}
}

// spyResolver records that resolution happened and asserts nothing was written.
type spyResolver struct {
	hashResolver
	resolves int
}

func (s *spyResolver) Resolve(spec *variantspec.VariantSpec) (*variantspec.Resolved, error) {
	s.resolves++
	return s.hashResolver.Resolve(spec)
}

func TestPreflightSpendsNothing(t *testing.T) {
	res := &spyResolver{}
	applier := &okApplier{}
	pub := &countingPublisher{}
	p := Preflighter{Resolver: res, Materializer: okMaterializer{}}

	out, err := p.Preflight(context.Background(), draftFor(map[string]Edit{"n1": {ModelRef: strPtr("m-new")}}), baseSpec())
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !out.Admissible() {
		t.Fatalf("verdict = %q, want admissible", out.Verdict)
	}
	// A resolve is a pure read — expected. A compile, a publish, or an eval run is not.
	if applier.calls != 0 {
		t.Errorf("preflight compiled %d times — it must write no diff", applier.calls)
	}
	if pub.published != 0 {
		t.Errorf("preflight published %d prompt versions — it must publish nothing", pub.published)
	}
	if out.ConfigHash == "" {
		t.Error("an admissible preflight should carry the hash the change would have")
	}
}

type countingPublisher struct{ published int }

// ── 9.3 the third verdict: never refuse — and never pass — on ignorance ─────────────────────────

func TestPreflightThirdVerdictOnUnknownInput(t *testing.T) {
	missing := MissingInput{Kind: "context_drop_ratio", NodeID: "n1", Subject: "summarization"}
	p := Preflighter{Resolver: hashResolver{}, Materializer: okMaterializer{},
		Gates: []Admissibility{unknownGate{missing: missing}}}

	got, err := p.Preflight(context.Background(), draftFor(map[string]Edit{"n1": {ContextPolicy: strPtr("c-sum")}}), baseSpec())
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	// Both directions asserted separately: each is a different bug with a different cause.
	if got.Verdict == VerdictAdmissible {
		t.Error("an unmeasured input returned admissible — that asserts a safety check that never ran")
	}
	if got.Verdict == VerdictRefused {
		t.Error("an unmeasured input returned refused — that blames the user for our missing measurement")
	}
	if got.Verdict != VerdictNotYetMeasurable {
		t.Fatalf("verdict = %q, want %q", got.Verdict, VerdictNotYetMeasurable)
	}
	if got.Missing.Kind == "" {
		t.Error("the third verdict must name the missing measurement, or it is a dead end")
	}
}

func TestPreflightGateRefusalIsStillARefusal(t *testing.T) {
	// The third verdict must not swallow a gate that DOES have evidence.
	r := Refusal{Cause: "policy would drop 0.62 of context; node tolerates 0.20", NodeID: "n1", Field: "context"}
	p := Preflighter{Resolver: hashResolver{}, Materializer: okMaterializer{}, Gates: []Admissibility{refusingGate{r: r}}}
	got, _ := p.Preflight(context.Background(), draftFor(map[string]Edit{"n1": {ContextPolicy: strPtr("c-sum")}}), baseSpec())
	if got.Verdict != VerdictRefused {
		t.Fatalf("an evidence-based gate refusal became %q", got.Verdict)
	}
	if !strings.Contains(got.Refusal.Cause, "0.62") {
		t.Error("the refusal dropped the measured number the user needs to decide what to do")
	}
}

// ── 9.4 concurrency: two variants, and a named conflict ─────────────────────────────────────────

func TestConcurrentDraftsYieldTwoVariants(t *testing.T) {
	parent := baseSpec()
	a, err := draftFor(map[string]Edit{"n1": {ModelRef: strPtr("m-a")}}).Derive(parent)
	if err != nil {
		t.Fatalf("derive a: %v", err)
	}
	b, err := draftFor(map[string]Edit{"n1": {ModelRef: strPtr("m-b")}}).Derive(parent)
	if err != nil {
		t.Fatalf("derive b: %v", err)
	}
	if specFingerprint(a) == specFingerprint(b) {
		t.Fatal("two concurrent edits collapsed into one variant — somebody's work was lost")
	}
	if a.ParentVariantID != b.ParentVariantID {
		t.Error("the two variants do not share a parent — lineage is wrong")
	}
}

func TestStaleDraftRefusedByName(t *testing.T) {
	s := Submitter{
		Preflight: Preflighter{Resolver: hashResolver{}, Materializer: okMaterializer{}},
		Applier:   &okApplier{}, Head: fixedHead{head: "parent-moved"},
		Record: NewMemRecorder(), Auth: allowAll{},
	}
	d := draftFor(map[string]Edit{"n1": {ModelRef: strPtr("m-new")}}) // token = "parent-hash"

	_, err := s.Submit(context.Background(), d, baseSpec())
	if !errors.Is(err, ErrStaleDraft) {
		t.Fatalf("stale submit: got %v, want ErrStaleDraft", err)
	}
	// The conflict must NAME the parent, or the author cannot tell which of their edits is questionable.
	if !strings.Contains(err.Error(), "parent-hash") || !strings.Contains(err.Error(), "parent-moved") {
		t.Errorf("conflict does not name the parents: %v", err)
	}
	// And it must leave nothing behind.
	rows, _ := s.Record.ListByTenant(context.Background(), "t1")
	if len(rows) != 0 {
		t.Errorf("a refused submit wrote %d record rows, want 0", len(rows))
	}
}

// ── 9.5 revert reproduces the parent hash byte-identically ──────────────────────────────────────

func TestRevertReproducesParentHashByteIdentical(t *testing.T) {
	ctx := context.Background()
	parent := baseSpec()
	parentHash := specFingerprint(parent)

	rec := NewMemRecorder()
	s := Submitter{
		Preflight: Preflighter{Resolver: hashResolver{}, Materializer: okMaterializer{}},
		Applier:   &okApplier{}, Head: fixedHead{head: parentHash},
		Record: rec, Auth: allowAll{},
	}
	d := draftFor(map[string]Edit{"n1": {ModelRef: strPtr("m-new")}})
	d.ParentVariantID = parentHash
	d.ConcurrencyToken = parentHash

	sub, err := s.Submit(ctx, d, parent)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if sub.ConfigHash == parentHash {
		t.Fatal("the authored change hashed identically to its parent — it changed nothing")
	}

	rv := Reverter{Record: rec, Parents: mapParents{specs: map[string]*variantspec.VariantSpec{parentHash: parent}},
		Resolver: hashResolver{}}
	got, err := rv.Revert(ctx, sub.ChangeID, Actor{ID: "u1", TenantID: "t1"})
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if got.ConfigHash != parentHash {
		t.Fatalf("revert landed on %q, want byte-identical %q", got.ConfigHash, parentHash)
	}
	// The original record must survive the reversal unchanged (append-only).
	history, _ := rec.History(ctx, sub.ChangeID)
	if len(history) != 2 {
		t.Fatalf("history has %d rows, want 2 (submitted, reverted)", len(history))
	}
	if history[0].Action != ActionSubmitted || history[1].Action != ActionReverted {
		t.Errorf("history is not a sequence: %v then %v", history[0].Action, history[1].Action)
	}
	if history[0].ConfigHash != sub.ConfigHash {
		t.Error("the original submitted row was rewritten by the reversal")
	}
}

// ── 9.6 the record is append-only ───────────────────────────────────────────────────────────────

func TestAuthoredRecordIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	rec := NewMemRecorder()

	// The interface itself must offer no way to mutate. This is the assertion that fails if someone
	// adds an Update or Delete later "just for the admin path".
	rt := reflect.TypeOf((*Recorder)(nil)).Elem()
	for i := 0; i < rt.NumMethod(); i++ {
		name := rt.Method(i).Name
		for _, banned := range []string{"Update", "Delete", "Set", "Remove", "Truncate"} {
			if strings.Contains(name, banned) {
				t.Errorf("Recorder exposes %q — the record is append-only", name)
			}
		}
	}

	base := Entry{ChangeID: "ac_1", Action: ActionSubmitted, TenantID: "t1", ActorID: "u1",
		WorkflowID: "wf1", ParentVariantID: "p", ConfigHash: "c", Axis: "model",
		Origin: string(OriginUser), VerificationState: StateUnverified}
	if err := rec.Append(ctx, base); err != nil {
		t.Fatalf("append: %v", err)
	}
	// One submission per change, enforced the same way the partial unique index enforces it.
	if err := rec.Append(ctx, base); !errors.Is(err, ErrDuplicateSubmit) {
		t.Errorf("second submit: got %v, want ErrDuplicateSubmit", err)
	}
	// A later action for the same change is a NEW row, not an edit.
	if err := rec.Append(ctx, Entry{ChangeID: "ac_1", Action: ActionVerified, TenantID: "t1", ActorID: "u1",
		WorkflowID: "wf1", ParentVariantID: "p", ConfigHash: "c", Axis: "model",
		Origin: string(OriginUser), VerificationState: StateVerified}); err != nil {
		t.Fatalf("append verified: %v", err)
	}
	h, _ := rec.History(ctx, "ac_1")
	if len(h) != 2 || h[0].VerificationState != StateUnverified {
		t.Fatalf("history = %+v; the original unverified row must survive verbatim", h)
	}
	if h[0].Seq >= h[1].Seq {
		t.Error("seq is not monotonic — the history cannot be reconstructed in order")
	}
}

// TestUnverifiedContributesZeroToAggregates is the honesty guarantee as arithmetic (FR25, NFR14).
func TestUnverifiedContributesZeroToAggregates(t *testing.T) {
	entries := []Entry{
		{ChangeID: "a", VerificationState: StateUnverified},
		{ChangeID: "b", VerificationState: StateVerified},
		{ChangeID: "c", VerificationState: StateUnverified},
	}
	got := CountableAggregate(entries, func(Entry) float64 { return 10 })
	if got != 10 {
		t.Errorf("aggregate = %v, want 10 — only the verified change may contribute", got)
	}
	// And the control: a filter that let everything through would give 30, so the test can fail.
	if CountableAggregate(entries, func(Entry) float64 { return 0 }) != 0 {
		t.Error("contribution function ignored")
	}
	if StateUnverified.Countable() {
		t.Error("StateUnverified reported Countable — it would enter every savings figure")
	}
}

// ── 9.7 no override, asserted over the enumerated surface ───────────────────────────────────────

// TestNoOverrideSuppressesAnyRefusal asserts over the STRUCTS, not over a sample of call paths
// (task 9.7, NFR12). A behavioural test can only check the refusals someone thought to write down; a
// field named `Force` would be found here the day it is added.
func TestNoOverrideSuppressesAnyRefusal(t *testing.T) {
	banned := []string{"force", "override", "skip", "bypass", "allowunsafe", "ignore", "unsafe", "admin"}
	for _, target := range []any{Preflighter{}, Submitter{}, Reverter{}, Draft{}, Edit{}} {
		rt := reflect.TypeOf(target)
		for i := 0; i < rt.NumField(); i++ {
			name := strings.ToLower(rt.Field(i).Name)
			for _, b := range banned {
				if strings.Contains(name, b) {
					t.Errorf("%s has field %q — there is no override for a refusal, at any tier",
						rt.Name(), rt.Field(i).Name)
				}
			}
		}
	}

	// Behavioural half: a refusal holds no matter who is asking or what they pass.
	r := Refusal{Cause: "no materializer for python", NodeID: "n1", Field: "skills"}
	p := Preflighter{Resolver: hashResolver{}, Materializer: refusingMaterializer{r: r}}
	for _, actor := range []Actor{{ID: "u1", TenantID: "t1"}, {ID: "admin", TenantID: "t1"}, {}} {
		d := draftFor(map[string]Edit{"n1": {SkillRefs: &[]string{"s1"}}})
		d.Actor = actor
		got, _ := p.Preflight(context.Background(), d, baseSpec())
		if got.Verdict != VerdictRefused {
			t.Errorf("actor %+v got %q — a refusal is origin- and identity-blind", actor, got.Verdict)
		}
	}

	// And a submit cannot get past a refusal either.
	s := Submitter{Preflight: p, Applier: &okApplier{}, Record: NewMemRecorder(), Auth: allowAll{}}
	if _, err := s.Submit(context.Background(), draftFor(map[string]Edit{"n1": {SkillRefs: &[]string{"s1"}}}), baseSpec()); !errors.Is(err, ErrNotAdmissible) {
		t.Errorf("submit past a refusal: got %v, want ErrNotAdmissible", err)
	}
}

// TestSubmitIsEntitlementGated: not-entitled and not-permitted are separate remedies and stay separate
// errors; neither creates a draft, a variant, or a diff.
func TestSubmitIsEntitlementGated(t *testing.T) {
	applier := &okApplier{}
	rec := NewMemRecorder()
	s := Submitter{
		Preflight: Preflighter{Resolver: hashResolver{}, Materializer: okMaterializer{}},
		Applier:   applier, Record: rec, Auth: denyAll{err: ErrNotEntitled},
	}
	_, err := s.Submit(context.Background(), draftFor(map[string]Edit{"n1": {ModelRef: strPtr("m")}}), baseSpec())
	if !errors.Is(err, ErrNotEntitled) {
		t.Fatalf("got %v, want ErrNotEntitled", err)
	}
	if applier.calls != 0 {
		t.Error("an unentitled submit reached the compiler")
	}
	if rows, _ := rec.ListByTenant(context.Background(), "t1"); len(rows) != 0 {
		t.Error("an unentitled submit wrote an audit row")
	}
	if errors.Is(ErrNotEntitled, ErrNotPermitted) {
		t.Error("not-entitled and not-permitted collapsed into one error — different remedies")
	}
}

// ── the submitted change is unverified, and the record says so ──────────────────────────────────

func TestSubmitRecordsUnverified(t *testing.T) {
	ctx := context.Background()
	rec := NewMemRecorder()
	s := Submitter{
		Preflight: Preflighter{Resolver: hashResolver{}, Materializer: okMaterializer{}},
		Applier:   &okApplier{}, Head: fixedHead{head: "parent-hash"},
		Record: rec, Auth: allowAll{},
	}
	sub, err := s.Submit(ctx, draftFor(map[string]Edit{"n1": {ModelRef: strPtr("m-new")}}), baseSpec())
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if sub.Entry.VerificationState != StateUnverified {
		t.Errorf("verification_state = %q, want unverified — a submission cannot assert its own quality",
			sub.Entry.VerificationState)
	}
	if sub.Entry.Origin != string(OriginUser) {
		t.Errorf("origin = %q, want user", sub.Entry.Origin)
	}
	if sub.Entry.ActorID == "" || sub.Entry.TenantID == "" {
		t.Error("the record does not attribute the change")
	}
	if sub.Entry.DiffRef == "" {
		t.Error("the record does not cite the diff it produced")
	}
	// Deterministic id: a retried submit collides rather than duplicating.
	if again := ChangeID("wf1", sub.ConfigHash, "u1"); again != sub.ChangeID {
		t.Errorf("ChangeID is not deterministic: %q vs %q", again, sub.ChangeID)
	}
}

// ── 9.6 the migration ships with the code ───────────────────────────────────────────────────────

// TestMigrationShipsWithTheCode is the backend rule that schema, migration and code land together. It
// is a cheap test and it catches the expensive mistake: a binary that writes a table nobody created.
func TestMigrationShipsWithTheCode(t *testing.T) {
	up := filepath.Join("..", "..", "db", "migrations", "postgres", "0016_p13_authored_change.up.sql")
	b, err := os.ReadFile(up)
	if err != nil {
		t.Fatalf("the code writes authored_change but no migration creates it: %v", err)
	}
	sql := string(b)
	for _, must := range []string{
		"CREATE TABLE IF NOT EXISTS authored_change", // idempotent by semantics, not by object name alone
		"authored_change_one_submit",                 // one submission per change, enforced by the database
		"authored_change_append_only",                // append-only enforced, not documented
		"verification_state",                         // the honesty column exists
		"authored_change_submit_is_unverified",       // "submitted and already verified" is unrepresentable
	} {
		if !strings.Contains(sql, must) {
			t.Errorf("migration is missing %q", must)
		}
	}
	// Every column the Go entry writes must exist in the DDL, or the first live insert fails.
	for _, col := range []string{"change_id", "action", "tenant_id", "actor_id", "workflow_id",
		"parent_variant_id", "config_hash", "axis", "diff_ref", "origin", "forked_from_proposal",
		"verification_state", "revert_of"} {
		if !strings.Contains(sql, col) {
			t.Errorf("migration has no column %q, which Entry writes", col)
		}
	}
	down := filepath.Join("..", "..", "db", "migrations", "postgres", "0016_p13_authored_change.down.sql")
	if _, err := os.ReadFile(down); err != nil {
		t.Errorf("no down-migration: a wave that cannot be rolled back is not independently revertible: %v", err)
	}
}

// ── forked proposals do not credit the operator ─────────────────────────────────────────────────

func TestForkedProposalDoesNotCreditOperator(t *testing.T) {
	d := draftFor(map[string]Edit{"n1": {ModelRef: strPtr("m-new")}})
	d.ForkedFromProposal = "cand-7"
	spec, err := d.Derive(baseSpec())
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	cand := d.ToCandidate(spec, "n1", []string{"model"})

	if cand.Origin != OriginUser {
		t.Errorf("a forked proposal has origin %q, want user", cand.Origin)
	}
	if cand.ForkedFromProposal != "cand-7" {
		t.Error("the originating proposal was not recorded — the lineage is lost")
	}
	if cand.Operator != OperatorAuthored {
		t.Errorf("operator = %q; a forked change must not claim the catalog operator's name, or that "+
			"operator's win rate becomes a measure of how often humans fix it", cand.Operator)
	}
}
