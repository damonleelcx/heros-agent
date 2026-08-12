package herosagent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// P30 task 7.4 — what the ingest refuses, and what it records instead of writing.

// ingestFixture wires an Ingester over an ACTIVE published version, which is the only state that
// accepts anything.
func ingestFixture(t *testing.T) (*Ingester, *MemInferenceStore, string) {
	t.Helper()
	versions := NewMemVersionStore()
	const hash = "cfg-active"
	if err := versions.Put(context.Background(), Version{
		ConfigHash: hash, RehearsalState: RehearsalPassed, CreatedAtMS: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := versions.Activate(context.Background(), hash, 2); err != nil {
		t.Fatal(err)
	}
	inferences := NewMemInferenceStore()
	ing, err := NewIngester(versions, inferences, 0.7, func() int64 { return 99 })
	if err != nil {
		t.Fatal(err)
	}
	return ing, inferences, hash
}

func submission(hash string, edges ...SubmittedEdge) Submission {
	return Submission{
		TenantID: "t1", WorkflowID: "wf", SourceRevision: "rev1",
		AgentConfigHash: hash, Placement: PlacementCustomer,
		NodeIDs: []string{"a", "b", "c"},
		Edges:   edges,
	}
}

func herosEdge(from, to string, c float64) SubmittedEdge {
	return SubmittedEdge{From: from, To: to, Kind: "data", Author: "heros", Confidence: c}
}

// 🔴 THE FLOOR, APPLIED TO A NUMBER THE PLATFORM DID NOT PRODUCE. Below-floor is not written and IS
// recorded — the two halves are separate assertions because dropping it silently would satisfy "not
// written" completely.
func TestIngestAppliesTheConfidenceFloorToASubmittedFact(t *testing.T) {
	ing, store, hash := ingestFixture(t)

	res, err := ing.Accept(context.Background(), submission(hash,
		herosEdge("a", "c", 0.9),
		herosEdge("b", "c", 0.4),
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 1 || res.Written[0].From != "a" {
		t.Errorf("wrote %+v, want only the above-floor edge", res.Written)
	}
	var found *Abstention
	for i := range res.Abstentions {
		if res.Abstentions[i].Subject == "b→c" {
			found = &res.Abstentions[i]
		}
	}
	if found == nil {
		t.Fatal("the below-floor fact was dropped and NOT recorded — an unrecorded refusal is one nothing " +
			"can aggregate, and `which abstention dominates` is the question that tells an operator what to fix")
	}
	if found.Reason != AbstainBelowFloor {
		t.Errorf("recorded cause %q, want %q", found.Reason, AbstainBelowFloor)
	}
	if found.Confidence == nil || *found.Confidence != 0.4 {
		t.Errorf("the recorded confidence is %v, want the value that fell short", found.Confidence)
	}

	stored, ok, _ := store.Get(context.Background(), "wf", "rev1", hash)
	if !ok {
		t.Fatal("nothing was stored")
	}
	if len(stored.Edges) != 1 {
		t.Errorf("the STORE holds %d edges — the floor must apply to what is written, not only to what "+
			"is returned", len(stored.Edges))
	}
}

// 🔴 TASK 7.4 — an unknown `agent_config_hash` is refused BY NAME, and nothing is written.
func TestIngestRefusesAnUnknownAgentVersionByName(t *testing.T) {
	ing, store, _ := ingestFixture(t)

	_, err := ing.Accept(context.Background(), submission("cfg-nobody-published", herosEdge("a", "c", 0.9)))
	if !errors.Is(err, ErrUnknownAgentVersion) {
		t.Fatalf("err is %v, want ErrUnknownAgentVersion", err)
	}
	if !strings.Contains(err.Error(), "cfg-nobody") {
		t.Errorf("the refusal does not name the hash it refused: %v", err)
	}
	if store.Len() != 0 {
		t.Error("a refused submission wrote something")
	}
}

// A published-but-stood-down definition is a real row, so a hash check alone passes. Accepting its
// output would let a customer keep submitting under a definition the operator retired — including one
// that failed its rehearsal, which is what the gate exists to prevent.
func TestIngestRefusesAPublishedButInactiveVersion(t *testing.T) {
	versions := NewMemVersionStore()
	ctx := context.Background()
	if err := versions.Put(ctx, Version{ConfigHash: "cfg-old", RehearsalState: RehearsalFailed, CreatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	ing, err := NewIngester(versions, NewMemInferenceStore(), 0.7, func() int64 { return 1 })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ing.Accept(ctx, submission("cfg-old", herosEdge("a", "c", 0.9))); err == nil {
		t.Error("output from a definition that FAILED its rehearsal was accepted")
	}
}

// An agent-authored fact naming no version is refused. It is not "storable with a caveat": there is no
// definition to re-run it against and no hash to compare a later answer with.
func TestIngestRefusesAnAgentFactWithNoVersion(t *testing.T) {
	ing, _, _ := ingestFixture(t)
	_, err := ing.Accept(context.Background(), submission("", herosEdge("a", "c", 0.9)))
	if !errors.Is(err, ErrUnattributedInference) {
		t.Errorf("err is %v, want ErrUnattributedInference", err)
	}
}

// 🔴 TASK 7.5 — `disabled` refuses ingest, and the sentence says the default is why.
func TestIngestRefusesADisabledTenant(t *testing.T) {
	ing, store, hash := ingestFixture(t)
	sub := submission(hash, herosEdge("a", "c", 0.9))
	sub.Placement = PlacementDisabled

	_, err := ing.Accept(context.Background(), sub)
	if !errors.Is(err, ErrWrongPlacement) {
		t.Fatalf("err is %v, want ErrWrongPlacement", err)
	}
	if store.Len() != 0 {
		t.Error("a disabled tenant's submission was stored")
	}
}

// A `platform`-placed tenant submitting results is refused too. The platform already has the answer; a
// submission would be a second one, produced by a second credential, stored under a placement that
// contradicts the row.
func TestIngestRefusesAPlatformPlacedTenant(t *testing.T) {
	ing, _, hash := ingestFixture(t)
	sub := submission(hash, herosEdge("a", "c", 0.9))
	sub.Placement = PlacementPlatform
	if _, err := ing.Accept(context.Background(), sub); !errors.Is(err, ErrWrongPlacement) {
		t.Errorf("err is %v, want ErrWrongPlacement", err)
	}
}

// 🔴 D3 FENCE 1 AT THE INGEST. A submitted `heros` edge over a pair the same payload's FRONTEND edges
// establish is recorded, never written — rule-derived topology is immutable to HEROS on both hosts.
func TestIngestRefusesAnAgentEdgeOverAFrontendOne(t *testing.T) {
	ing, _, hash := ingestFixture(t)
	sub := submission(hash,
		SubmittedEdge{From: "a", To: "b", Kind: "sequential", Author: "frontend"},
		// The same pair, REVERSED — a frontend that established a→b established a relationship between
		// those nodes, and an agent proposing b→a is proposing a contradiction of a measured fact.
		herosEdge("b", "a", 0.99),
	)
	res, err := ing.Accept(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 0 {
		t.Errorf("wrote %+v over a pair a frontend established", res.Written)
	}
	if len(res.Abstentions) != 1 || res.Abstentions[0].Reason != AbstainFrontendOwns {
		t.Errorf("abstentions are %+v, want one %q", res.Abstentions, AbstainFrontendOwns)
	}
}

// The closed vocabularies apply to a submission exactly as they apply to a model answer: an unknown
// node id and an out-of-vocabulary kind are REJECTED, never repaired.
func TestIngestRejectsRatherThanRepairs(t *testing.T) {
	ing, _, hash := ingestFixture(t)
	res, err := ing.Accept(context.Background(), submission(hash,
		herosEdge("a", "zzz", 0.9),
		SubmittedEdge{From: "a", To: "c", Kind: "dataflow", Author: "heros", Confidence: 0.9},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 0 {
		t.Errorf("wrote %+v — a `kind` of \"dataflow\" must not be coerced to \"data\"", res.Written)
	}
	got := map[AbstentionReason]bool{}
	for _, a := range res.Abstentions {
		got[a.Reason] = true
	}
	if !got[AbstainUnknownNode] || !got[AbstainOutOfVocabulary] {
		t.Errorf("abstention causes %v, want both unknown_node and out_of_vocabulary", got)
	}
}

// A frontend-authored edge is P29's structure and is not this ingest's to keep or decline — it passes
// through untouched and is not written into the inference.
func TestIngestLeavesFrontendEdgesAlone(t *testing.T) {
	ing, _, hash := ingestFixture(t)
	res, err := ing.Accept(context.Background(), Submission{
		TenantID: "t1", WorkflowID: "wf", SourceRevision: "rev1",
		AgentConfigHash: hash, Placement: PlacementCustomer, NodeIDs: []string{"a", "b"},
		Edges: []SubmittedEdge{{From: "a", To: "b", Kind: "sequential", Author: "frontend"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 0 || len(res.Abstentions) != 0 {
		t.Errorf("a frontend edge produced %+v / %+v", res.Written, res.Abstentions)
	}
}

// 🔴 A submission carrying no agent facts is not this ingest's business at all — which is what keeps
// every existing `heros link --with-ir` working on the day this deploys. Q2 makes `disabled` the
// default, so on that day EVERY tenant is disabled, and an ingest that refused whole payloads for a
// disabled tenant would break the P29 structure upload for the entire fleet at once.
func TestAPayloadWithNoAgentFactsIsNotAnAgentSubmission(t *testing.T) {
	plain := Submission{
		TenantID: "t1", WorkflowID: "wf", SourceRevision: "rev1", Placement: PlacementDisabled,
		NodeIDs: []string{"a", "b"},
		Edges:   []SubmittedEdge{{From: "a", To: "b", Kind: "sequential", Author: "frontend"}},
	}
	if plain.HasAgentFacts() {
		t.Error("a pre-P30 structure upload was classified as an agent submission — on deploy day that " +
			"is every customer, and Q2's default placement would refuse all of them")
	}

	// And the inverse: heros-authored edges with no hash MUST be classified as an agent submission, so
	// they reach the refusal rather than being waved through as plain structure.
	unattributed := plain
	unattributed.Edges = append(unattributed.Edges, herosEdge("a", "c", 0.9))
	if !unattributed.HasAgentFacts() {
		t.Error("agent-authored edges with no hash read as `nothing to see`, which is the failure looking " +
			"like the safe answer")
	}
}

// The customer's own abstentions are carried through, so a customer-placed inference has the same
// shape as a platform-placed one. An asymmetry would read as an agent that abstains less on customer
// hardware — a conclusion about the model drawn from a gap in a wire format.
func TestIngestCarriesTheSubmittedAbstentions(t *testing.T) {
	ing, _, hash := ingestFixture(t)
	sub := submission(hash, herosEdge("a", "c", 0.9))
	sub.Abstentions = []SubmittedAbstention{{Subject: "b", Cause: string(AbstainNoCandidate)}}

	res, err := ing.Accept(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Abstentions) != 1 || res.Abstentions[0].Subject != "b" {
		t.Errorf("abstentions are %+v, want the submitted one", res.Abstentions)
	}
}

// A cause outside the closed enum is refused rather than stored as prose.
func TestIngestRefusesAnAbstentionCauseOutsideTheEnum(t *testing.T) {
	ing, _, hash := ingestFixture(t)
	sub := submission(hash)
	sub.Abstentions = []SubmittedAbstention{{Subject: "b", Cause: "the model seemed unsure"}}
	if _, err := ing.Accept(context.Background(), sub); err == nil {
		t.Error("a free-text abstention cause was accepted, and a reason nothing can aggregate is a " +
			"reason nothing can act on")
	}
}

// 🚫 The platform records NO token counts for a customer-placed inference. The customer spent their own
// credential; a zero would render as "this analysis was free", which is a claim about somebody else's
// bill.
func TestACustomerPlacedInferenceCarriesNoPlatformSpend(t *testing.T) {
	ing, store, hash := ingestFixture(t)
	if _, err := ing.Accept(context.Background(), submission(hash, herosEdge("a", "c", 0.9))); err != nil {
		t.Fatal(err)
	}
	stored, ok, _ := store.Get(context.Background(), "wf", "rev1", hash)
	if !ok {
		t.Fatal("nothing stored")
	}
	if stored.TokensIn != 0 || stored.TokensOut != 0 {
		t.Errorf("the platform recorded %d/%d tokens for a run it did not pay for",
			stored.TokensIn, stored.TokensOut)
	}
}

// An ingest with no version store cannot tell a published hash from a string, so it must not exist.
func TestAnIngestWithoutAVersionStoreIsRefusedAtConstruction(t *testing.T) {
	if _, err := NewIngester(nil, NewMemInferenceStore(), 0.7, func() int64 { return 1 }); err == nil {
		t.Error("an ingest was built with no way to verify a config_hash — its fence would be off " +
			"exactly when the deployment is misconfigured")
	}
}
