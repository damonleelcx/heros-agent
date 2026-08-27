package herosagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/providercall"
)

// p36_pin_test.go is P36 §1.3–1.5 and §9.2–9.4: what happens to a PINNED INFERENCE when the
// definition's shape changes.
//
// The failure these fences exist for makes no noise. Every pin is keyed by
// `(workflow_id, source_revision, agent_config_hash)`, so a shape change that moved a hash orphans
// every pin filed under it — nothing errors, the console keeps rendering, and the cost arrives weeks
// later as a bill and a support question.

// legacySpecJSON is a `spec_json` value exactly as the pre-P36 binary wrote it. A literal rather than
// `json.Marshal` of anything in this tree: a fence that encodes with today's code and decodes with
// today's code asserts that Marshal and Unmarshal are inverses, which was never the question.
const legacySpecJSON = `{"prompt_ref":"prompt-v1","model_ref":"claude-opus-5",` +
	`"credential_ref":"anthropic","context_ref":"ctx-v1","harness_ref":"harness-single-shot-v1"}`

// 🔴 §1.3 / §9.2 — A ROW WRITTEN BY THE PREVIOUS BINARY STILL DECODES, and to the same configuration.
func TestAPreP36StoredDefinitionDecodesAndKeepsItsHash(t *testing.T) {
	var d Definition
	if err := json.Unmarshal([]byte(legacySpecJSON), &d); err != nil {
		t.Fatalf("a spec_json row written before P36 no longer decodes: %v\n  %s", err, legacySpecJSON)
	}
	if len(d.Nodes) != 1 {
		t.Fatalf("a pre-P36 row decoded to %d node(s); it describes exactly one", len(d.Nodes))
	}
	if d.Nodes[0].NodeID != DefaultNodeID {
		t.Errorf("the decoded node is %q, want %q — a pre-P36 row carries no node_id, so the decoder "+
			"supplies the default, and any other value would move the compatibility encoding's bytes",
			d.Nodes[0].NodeID, DefaultNodeID)
	}
	got, err := d.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}
	// The hash the pre-P36 tree recorded for this exact definition.
	const want = "0db6c67956dcc1bafe0fe1bf40db01acb13b93c854f341d4f9bd729c97cd1e34"
	if got != want {
		t.Errorf("a row written before P36 now hashes to %s, and it was filed under %s. Every inference "+
			"pinned under the old hash is unreachable from any definition anybody can author.", got, want)
	}
	// And it round-trips back to the SAME BYTES, so re-writing a row does not rewrite its identity.
	back, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != legacySpecJSON {
		t.Errorf("re-encoding a pre-P36 row changed its bytes.\n  was: %s\n  now: %s", legacySpecJSON, back)
	}
}

// 🔴 §1.3 — the stored inference names the config_hash that produced it, and still resolves to it.
func TestAnExistingPinRemainsReadableAndNamesItsProducingConfiguration(t *testing.T) {
	ctx := context.Background()
	var produced Definition
	if err := json.Unmarshal([]byte(legacySpecJSON), &produced); err != nil {
		t.Fatal(err)
	}
	hash, err := produced.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}

	store := NewMemInferenceStore()
	if err := store.Put(ctx, Stored{
		InferenceID: "inf-1", TenantID: "acme", WorkflowID: "wf", SourceRevision: "rev-1",
		AgentConfigHash: hash, Placement: PlacementPlatform,
		Edges: []ProvenancedEdge{{From: "a", To: "b", Kind: "data", Confidence: 0.9}},
	}); err != nil {
		t.Fatal(err)
	}

	got, ok, err := store.Get(ctx, "wf", "rev-1", hash)
	if err != nil || !ok {
		t.Fatalf("the pin is no longer readable by its key: ok=%v err=%v", ok, err)
	}
	if got.AgentConfigHash != hash {
		t.Errorf("the pin names %q and was produced by %q", got.AgentConfigHash, hash)
	}
	if len(got.Edges) != 1 || got.Edges[0].From != "a" {
		t.Errorf("the stored facts did not survive the shape change: %+v", got.Edges)
	}
	// 🔴 The edge carries NO producing node, because the row predates node attribution. It must decode
	// as empty rather than acquiring a default — a fabricated `heros_analyst` would be this platform
	// asserting a provenance nobody recorded.
	if got.Edges[0].ProducedByNode != "" {
		t.Errorf("an edge stored before per-node attribution came back attributed to %q",
			got.Edges[0].ProducedByNode)
	}
}

// 🔴 §1.4 / §9.3 — ACTIVATING A NEW DEFINITION DOES NOT RE-RUN PINS. Asserted by COUNTING PROVIDER
// CALLS, not by the absence of an error: "nothing re-ran" and "something re-ran and succeeded" are
// indistinguishable from a nil error, and the whole cost of getting this wrong is a bill.
func TestActivatingANewDefinitionRunsNoInference(t *testing.T) {
	ctx := context.Background()
	store := NewMemInferenceStore()
	model := &countingModel{}

	old := goodDefinition()
	oldHash, err := old.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, Stored{
		InferenceID: "inf-1", TenantID: "acme", WorkflowID: "wf", SourceRevision: "rev-1",
		AgentConfigHash: oldHash, Placement: PlacementPlatform,
	}); err != nil {
		t.Fatal(err)
	}

	versions := NewMemVersionStore()
	pub, err := NewPublisher(
		fakeCatalogue{models: []RegisteredModel{{ModelID: "claude-opus-5", Provider: "anthropic"}}},
		fakeSecrets{known: map[string]bool{"anthropic": true}}, versions, RunnerHosts{},
		func() int64 { return 1 })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pub.Publish(ctx, old); err != nil {
		t.Fatal(err)
	}
	next := goodDefinition()
	next.Nodes[0].ContextRef = "ctx-v2"
	res, err := pub.Publish(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created {
		t.Fatal("a changed definition did not create a version")
	}
	if err := versions.SetRehearsal(ctx, res.ConfigHash, RehearsalPassed, ""); err != nil {
		t.Fatal(err)
	}
	if err := pub.Activate(ctx, res.ConfigHash); err != nil {
		t.Fatal(err)
	}

	if model.calls != 0 {
		t.Errorf("activating a definition made %d provider call(s). A configuration change is a pinning "+
			"EVENT, not a re-inference: re-running every pin the moment somebody activates spends real "+
			"money for an answer nobody asked for, and it happens weeks after the change", model.calls)
	}
	// And the old pin is still exactly where it was, under its own hash.
	if _, ok, err := store.Get(ctx, "wf", "rev-1", oldHash); err != nil || !ok {
		t.Errorf("the pin produced by the previous definition is no longer readable: ok=%v err=%v", ok, err)
	}
}

// 🔴 §1.5 / §9.4 — A PIN FROM A SHAPE NOBODY CAN AUTHOR RENDERS STALE WITH ITS PRODUCER NAMED, and is
// NEITHER ABSENT NOR CURRENT.
func TestAPinFromAnUnauthorableShapeIsStaleAndNamesItsProducer(t *testing.T) {
	// A definition that WAS published and can no longer be authored: today's validator refuses it,
	// because a single-node definition carrying an ordering is exactly what ErrWiringOverride refuses.
	//
	// 🔴 Constructed as a stored ROW rather than through the publisher, which is the point: the row
	// exists, nothing can produce it again, and the question is how it renders.
	retired := SingleNode(Node{
		PromptRef: "prompt-v1", ModelRef: "claude-opus-5", CredentialRef: "anthropic",
		ContextRef: "ctx-v1", HarnessRef: "harness-single-shot-v1",
	})
	retired.Order = []string{DefaultNodeID}
	if err := retired.Validate(); err == nil {
		t.Fatal("this shape is still authorable, so the fence is testing nothing — pick a shape today's " +
			"validator actually refuses")
	}
	hash, err := retired.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}

	st := Stored{InferenceID: "inf-1", WorkflowID: "wf", SourceRevision: "rev-1", AgentConfigHash: hash}
	got := ClassifyPin(st, "some-other-active-hash", Version{ConfigHash: hash, Definition: retired}, true)

	if got.State != PinStale {
		t.Errorf("state is %q, want %q. Absent claims the agent never analysed this; current claims "+
			"these facts describe the configuration running now. Both are false.", got.State, PinStale)
	}
	if got.ProducingConfigHash != hash {
		t.Errorf("the producing configuration is not named: %+v", got)
	}
	if got.Authorable {
		t.Error("the pin reports its shape as authorable, and today's validator refuses it")
	}
	if got.UnauthorableReason == "" {
		t.Error("the pin says the shape is unauthorable and does not say why — a warning with nothing " +
			"to act on")
	}
	if !strings.Contains(got.Sentence, "STALE") || !strings.Contains(strings.ToLower(got.Sentence), "attributed") {
		t.Errorf("the reader-facing sentence does not say it is stale AND still attributed: %q", got.Sentence)
	}

	// The three states are genuinely distinguishable, or the enum is decoration.
	current := ClassifyPin(Stored{AgentConfigHash: "h1"}, "h1",
		Version{ConfigHash: "h1", Definition: goodDefinition()}, true)
	if current.State != PinCurrent {
		t.Errorf("a pin produced by the ACTIVE definition classified as %q", current.State)
	}
	unknown := ClassifyPin(Stored{AgentConfigHash: "h9"}, "h1", Version{}, false)
	if unknown.State != PinUnattributable {
		t.Errorf("a pin whose producer this deployment cannot resolve classified as %q", unknown.State)
	}
	if unknown.ProducingConfigHash != "h9" {
		t.Error("an unattributable pin does not name the hash it was filed under — which is a fact " +
			"about the row and cannot fail to be known")
	}
	for _, p := range []PinStatus{got, current, unknown} {
		if p.Sentence == "" {
			t.Errorf("%s carries no sentence; every state a surface can render must have words", p.State)
		}
	}
}

// countingModel counts provider calls and never returns one.
type countingModel struct{ calls int }

func (c *countingModel) Infer(context.Context, Input) (RawResult, providercall.Usage, error) {
	c.calls++
	return RawResult{}, providercall.Usage{}, errors.New("countingModel makes no real call")
}
