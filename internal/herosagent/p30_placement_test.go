package herosagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/providercall"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// P30 workstream 7 — customer-side runtime and parity.

// ── the two vocabularies that live in two packages ───────────────────────────────────────────────

// `internal/runlink` spells the abstention causes out rather than importing this package, because the
// egress package must not import the thing whose output it constrains. That is the right call and it
// buys a drift, so this is the test that CAN see both.
//
// 🔴 It compares the SETS, not the lengths. A length check passes for a rename, which is exactly the
// edit that would make a CLI submit a cause this platform refuses — and it would fail at a customer's
// terminal rather than here.
func TestAbstentionCausesMatchTheAgent(t *testing.T) {
	mine := map[string]bool{}
	for _, r := range AbstentionReasons() {
		mine[string(r)] = true
	}
	theirs := map[string]bool{}
	for _, c := range runlink.AbstentionCauses() {
		theirs[c] = true
	}
	for c := range theirs {
		if !mine[c] {
			t.Errorf("the wire accepts abstention cause %q and this package does not produce it — a CLI "+
				"submitting it would be refused by ParseAbstentionReason at the ingest", c)
		}
	}
	for c := range mine {
		if !theirs[c] {
			t.Errorf("this package produces abstention cause %q and the wire's closed set omits it — a "+
				"customer-placed run declining for that cause could not report it", c)
		}
	}
}

// The same drift, for who may author an edge.
//
// The sets are deliberately NOT equal: `discovery.FactAuthor` has four members and the wire accepts
// two, because `detector` and `operator` cannot author an edge a CLI submits. The assertion is that the
// wire's set is a SUBSET of the authors this package understands — widening a wire vocabulary to match
// an internal one "just in case" is how a boundary stops describing what actually crosses it.
func TestWireEdgeAuthorsAreOnesThisPackageUnderstands(t *testing.T) {
	understood := map[string]bool{
		string(authorFrontend): true,
		string(authorHEROS):    true,
	}
	for _, a := range runlink.EdgeAuthors() {
		if !understood[a] {
			t.Errorf("the wire accepts edge author %q, which the ingest does not branch on — an edge "+
				"carrying it would be silently treated as frontend-authored and written without a floor", a)
		}
	}
	if len(runlink.EdgeAuthors()) != len(understood) {
		t.Errorf("the wire accepts %d authors and the ingest understands %d",
			len(runlink.EdgeAuthors()), len(understood))
	}
}

// ── task 7.5 · the placement gate ────────────────────────────────────────────────────────────────

func TestPlacementGateAnswersPerHost(t *testing.T) {
	for _, c := range []struct {
		host      Host
		placement Placement
		allowed   bool
		says      string
	}{
		{HostPlatform, PlacementPlatform, true, ""},
		{HostCustomer, PlacementCustomer, true, ""},
		{HostPlatform, PlacementCustomer, false, "own machine"},
		{HostCustomer, PlacementPlatform, false, "platform's credential"},
		{HostPlatform, PlacementDisabled, false, "default"},
		{HostCustomer, PlacementDisabled, false, "default"},
	} {
		err := c.host.MayRun(c.placement)
		if c.allowed {
			if err != nil {
				t.Errorf("%s + %s was refused: %v", c.host, c.placement, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s + %s was allowed", c.host, c.placement)
			continue
		}
		if !errors.Is(err, ErrWrongPlacement) {
			t.Errorf("%s + %s refused with an unmatchable error: %v", c.host, c.placement, err)
		}
		// 🔴 The four refusals must not be one sentence. An operator reading "refused" learns nothing;
		// the point of the gate is that it says where the analysis DOES happen.
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s + %s says %q, which does not tell the reader %q", c.host, c.placement, err, c.says)
		}
	}
}

// 🔴 A CUSTOMER-PLACED TENANT MAKES ZERO PROVIDER CALLS PLATFORM-SIDE — asserted on the COUNT, not on
// the absence of an error, for the reason task 10.3 gives: "no error" is what a stub returns too.
func TestPlatformRunnerRunsNothingForACustomerPlacedTenant(t *testing.T) {
	m := &recordingModel{result: RawResult{Edges: []RawEdge{{From: "a", To: "c", Kind: "data", Confidence: conf(0.9)}}}}
	r, _ := testRunner(t, m)

	res, err := r.Infer(context.Background(), inputFor(irWith([]string{"a", "b", "c"})), "cfg1", PlacementCustomer)
	if err == nil {
		t.Fatal("the platform runner analysed a customer-placed tenant")
	}
	if m.count() != 0 {
		t.Errorf("the provider was called %d times for a tenant the platform may not analyse", m.count())
	}
	if res.Code != CodeWrongPlacement {
		t.Errorf("code is %q, want %q — `disabled` would tell a customer-placed tenant that HEROS is "+
			"off, while their own CI is producing inferences", res.Code, CodeWrongPlacement)
	}
}

// The gate sits AHEAD of the cache read, and this is the test that says so: a stored answer exists and
// is still not served. A placement that changed after an inference was stored must not keep being
// answered from the row it left behind.
func TestAStoredAnswerIsNotServedAfterThePlacementMovesAway(t *testing.T) {
	m := &recordingModel{result: RawResult{Edges: []RawEdge{{From: "a", To: "c", Kind: "data", Confidence: conf(0.9)}}}}
	r, store := testRunner(t, m)
	ctx := context.Background()
	ir := irWith([]string{"a", "b", "c"})

	if _, err := r.Infer(ctx, inputFor(ir), "cfg1", PlacementPlatform); err != nil {
		t.Fatal(err)
	}
	if store.Len() != 1 {
		t.Fatalf("nothing was stored, so the test cannot prove the cache was skipped")
	}

	if _, err := r.Infer(ctx, inputFor(ir), "cfg1", PlacementCustomer); err == nil {
		t.Error("the platform served a stored answer for a tenant it may no longer analyse — a real row, " +
			"and an artifact of a placement that has since changed")
	}
}

// `ReInfer` reaches the model without going through `Infer`, so it needs its own gate. A rule that is
// true of one entry point and not the other is the shape this fence exists to catch.
func TestReInferIsGatedToo(t *testing.T) {
	m := &recordingModel{result: RawResult{Edges: []RawEdge{{From: "a", To: "c", Kind: "data", Confidence: conf(0.9)}}}}
	r, _ := testRunner(t, m)
	ctx := context.Background()
	ir := irWith([]string{"a", "b", "c"})

	if _, err := r.Infer(ctx, inputFor(ir), "cfg1", PlacementPlatform); err != nil {
		t.Fatal(err)
	}
	before := m.count()
	if _, _, err := r.ReInfer(ctx, inputFor(ir), "cfg1", PlacementCustomer); err == nil {
		t.Error("re-inference ran platform-side for a customer-placed tenant")
	}
	if m.count() != before {
		t.Errorf("re-inference made %d provider calls past the gate", m.count()-before)
	}
}

// A stored inference names the placement its WRITER ran under, never the argument it was handed.
func TestAStoredInferenceCannotClaimAPlacementItsHostCouldNotRun(t *testing.T) {
	m := &recordingModel{result: RawResult{Edges: []RawEdge{{From: "a", To: "c", Kind: "data", Confidence: conf(0.9)}}}}
	store := NewMemInferenceStore()
	r, err := NewCustomerRunner(m, store, 0.5, func() int64 { return 1 })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Infer(context.Background(), inputFor(irWith([]string{"a", "b", "c"})), "cfg1", PlacementCustomer); err != nil {
		t.Fatal(err)
	}
	got, ok, _ := store.Get(context.Background(), "wf", "rev1", "cfg1")
	if !ok {
		t.Fatal("nothing stored")
	}
	if got.Placement != PlacementCustomer {
		t.Errorf("a customer-side run stored placement %q — the graph would attribute it to the platform",
			got.Placement)
	}
}

// ── task 7.2 · one context-assembly path ─────────────────────────────────────────────────────────

// 🔴 THE ANTI-SKEW ASSERTION. Both hosts assemble the same Input and the bytes are identical.
//
// It compares BYTES rather than structs, because bytes are what a provider receives: two structs that
// compare equal can still marshal differently if one side sorts a slice or omits an empty field, and
// the divergence would be invisible to a struct comparison and visible to a model.
func TestBothHostsAssembleByteIdenticalContext(t *testing.T) {
	ir := irWith([]string{"a", "b", "c"}, [2]string{"a", "b"})
	in := inputFor(ir)

	platformSide := &capturingModel{}
	customerSide := &capturingModel{}

	pr, err := NewRunner(platformSide, NewMemInferenceStore(), 0.5, func() int64 { return 1 })
	if err != nil {
		t.Fatal(err)
	}
	cr, err := NewCustomerRunner(customerSide, NewMemInferenceStore(), 0.5, func() int64 { return 1 })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Infer(context.Background(), in, "cfg1", PlacementPlatform); err != nil {
		t.Fatal(err)
	}
	if _, err := cr.Infer(context.Background(), in, "cfg1", PlacementCustomer); err != nil {
		t.Fatal(err)
	}
	if platformSide.seen == nil || customerSide.seen == nil {
		t.Fatal("a runner never assembled anything, so this test proves nothing")
	}
	if string(platformSide.seen) != string(customerSide.seen) {
		t.Errorf("the two placements sent different context.\n  platform: %s\n  customer: %s\n\n"+
			"  D6: two runners with one prompt is the classic shape that produces train/serve skew, and "+
			"the divergence is invisible because both `work`.", platformSide.seen, customerSide.seen)
	}
}

// A ModelInput the assembler did not produce carries no bytes and says so — which is what makes
// "one context-assembly path" a property of the type rather than a convention.
func TestAZeroModelInputRefusesToBeSent(t *testing.T) {
	var bypassed ModelInput
	if _, err := bypassed.Bytes(); !errors.Is(err, ErrAssemblerBypassed) {
		t.Errorf("a hand-built ModelInput yielded %v, want a refusal — a second runner could construct "+
			"one field by field and the compiler would be happy", err)
	}
}

// capturingModel records the exact assembled bytes it was handed.
type capturingModel struct{ seen []byte }

func (c *capturingModel) Infer(_ context.Context, in Input) (RawResult, providercall.Usage, error) {
	assembled, err := AssembleModelInput(in)
	if err != nil {
		return RawResult{}, providercall.Usage{}, err
	}
	b, err := assembled.Bytes()
	if err != nil {
		return RawResult{}, providercall.Usage{}, err
	}
	c.seen = b
	// The narrative NAMES THE HOST, deliberately. Task 7.6 asserts edge-set parity and explicitly does
	// not compare narrative; a test that had quietly started comparing prose would fail here, which is
	// the only way to know it is still asserting what it claims.
	return RawResult{
		Edges:     []RawEdge{{From: "a", To: "c", Kind: "data", Confidence: conf(0.9)}},
		Narrative: "assessed by a host whose prose is its own",
	}, providercall.Usage{}, nil
}
