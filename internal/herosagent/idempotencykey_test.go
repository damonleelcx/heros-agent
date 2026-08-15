package herosagent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/providercall"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

// idempotencykey_test.go fences the identity of a provider request.
//
// # The defect
//
// `GatewayModel.Infer` built the key as `defaultInferenceID(workflowID, sourceRevision, "")` under a
// comment reading "the three-part key IS the idempotency key". The third part was empty, so two
// DIFFERENT definitions analysing the same workflow at the same revision produced an IDENTICAL
// `Idempotency-Key` header. A provider that honours the header answers the second request from the
// first — and the activation gate, whose entire job is to tell definitions apart, then scores
// definition B on definition A's answers and reports it as a clean measurement.
//
// It bites hardest exactly where it matters most. On the rehearsal, WorkflowID is a fixture name and
// SourceRevision is the constant "fixture", so EVERY definition ever measured collided on all nine
// fixtures — the one code path where distinguishing definitions is the whole point.

// captureModel records the Input it was handed, so a test can assert what the model layer would key on.
type captureModel struct{ got Input }

func (c *captureModel) Infer(_ context.Context, in Input) (RawResult, providercall.Usage, error) {
	c.got = in
	return RawResult{Edges: []RawEdge{}, Labels: []RawLabel{}}, providercall.Usage{}, nil
}

func runnerInput() Input {
	return Input{
		WorkflowID: "wf", SourceRevision: "rev",
		RuleIR:  &discovery.IR{},
		Residue: Residue{Pairs: []Pair{{From: "a", To: "b"}}},
		Budget:  Budget{MaxTokens: 1000, MaxWall: time.Minute},
	}
}

// 🔴 THE DEFECT, asserted at the seam the model layer reads.
//
// The Runner must hand the model WHICH DEFINITION is running. Without it the model layer has nothing
// to key on and every definition looks like the same request.
func TestTheRunnerTellsTheModelWhichDefinitionIsRunning(t *testing.T) {
	m := &captureModel{}
	r, err := NewRunner(m, NewMemInferenceStore(), DefaultConfidenceFloor, func() int64 { return 1 })
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if _, err := r.Infer(context.Background(), runnerInput(), "cfg-alpha", PlacementPlatform); err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if m.got.AgentConfigHash != "cfg-alpha" {
		t.Fatalf("the model was handed AgentConfigHash %q, want \"cfg-alpha\".\n"+
			"Without it the provider request cannot name which definition it belongs to, and two "+
			"definitions analysing the same workflow become the same request.", m.got.AgentConfigHash)
	}
}

// 🔴 THE PROPERTY. Two definitions over the same workflow and revision must produce DIFFERENT keys.
//
// This is the assertion that would have caught the shipped state: with the config hash omitted, both
// sides of this comparison were equal.
func TestTwoDefinitionsOverOneWorkflowGetDifferentIdempotencyKeys(t *testing.T) {
	const workflow, revision = "go_chain", "fixture" // the rehearsal's own constants
	a := defaultInferenceID(workflow, revision, "cfg-alpha")
	b := defaultInferenceID(workflow, revision, "cfg-beta")

	if a == b {
		t.Fatalf("two definitions produced the same idempotency key (%s).\n"+
			"A provider honouring the header would answer the second from the first, so the activation "+
			"gate would score one definition on another's answers — on the rehearsal, where every "+
			"definition shares the fixture name and the constant revision, that is every measurement.", a)
	}

	// And the SAME definition must still be stable, or the retry-dedup this key exists for is lost.
	if defaultInferenceID(workflow, revision, "cfg-alpha") != a {
		t.Error("the key is not stable for one definition — a retry of the same inference would be " +
			"charged twice, which is what the idempotency key exists to prevent")
	}
}

// 🚫 The new field must NOT reach the model's input wire.
//
// `config_hash` is computed over the bytes the model is shown. Feeding the hash into those bytes would
// make a definition's identity depend on itself, and would move every existing config_hash — so this
// asserts the assembled input is byte-identical whether or not the field is set.
func TestTheConfigHashDoesNotEnterTheModelInputWire(t *testing.T) {
	without := runnerInput()
	with := runnerInput()
	with.AgentConfigHash = "cfg-alpha"

	a, err := AssembleModelInput(without)
	if err != nil {
		t.Fatalf("AssembleModelInput: %v", err)
	}
	b, err := AssembleModelInput(with)
	if err != nil {
		t.Fatalf("AssembleModelInput: %v", err)
	}
	ab, err := a.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	bb, err := b.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if string(ab) != string(bb) {
		t.Fatalf("setting AgentConfigHash changed the bytes the model is shown.\n"+
			"config_hash is computed over these bytes, so this would make a definition's identity depend "+
			"on itself and would move every config_hash already published.\nwithout: %s\nwith:    %s",
			ab, bb)
	}
	// Belt and braces: the hash string must not appear anywhere in the wire.
	if strings.Contains(string(bb), "cfg-alpha") {
		t.Error("the config hash appears in the model's input wire")
	}
}

// 🔴 THE FENCE THE DRILL DEMANDED, and the one the first attempt got wrong.
//
// The tests above assert `defaultInferenceID` and the Runner's hand-off. Neither touches the key
// `GatewayModel` actually puts on the wire — so restoring the original defect (passing "" for the
// config hash at that call site) left the whole package GREEN. A fence over the helper is not a fence
// over the call site, which is the exact shape of the bug being fixed here one level up.
//
// This drives a REAL gateway at a test server and reads the `Idempotency-Key` header off the request
// that arrives. It is the only assertion here that would have caught the shipped state.
func TestTheIdempotencyKeyOnTheWireDiffersPerDefinition(t *testing.T) {
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":`+
			`"{\"edges\":[],\"labels\":[],\"narrative\":\"n\"}"}}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	t.Cleanup(srv.Close)

	gw := providergateway.New(
		providergateway.StaticSecrets{providergateway.ProviderOpenAI: {APIKey: "sk-test"}},
		providergateway.WithBaseURL(providergateway.ProviderOpenAI, srv.URL),
	)
	entry := &registry.ModelEntry{Spec: registry.ModelSpec{
		Provider: providergateway.ProviderOpenAI, ModelID: "m",
	}}
	model, err := NewGatewayModel(gw, entry, "instruction")
	if err != nil {
		t.Fatalf("NewGatewayModel: %v", err)
	}

	// Two definitions, same workflow and revision — the rehearsal's exact situation.
	for _, hash := range []string{"cfg-alpha", "cfg-beta"} {
		in := runnerInput()
		in.WorkflowID, in.SourceRevision, in.AgentConfigHash = "go_chain", "fixture", hash
		if _, _, err := model.Infer(context.Background(), in); err != nil {
			t.Fatalf("Infer(%s): %v", hash, err)
		}
	}

	if len(keys) != 2 {
		t.Fatalf("expected 2 provider requests, saw %d — this fence is measuring nothing", len(keys))
	}
	if keys[0] == "" {
		t.Fatal("no Idempotency-Key reached the provider at all")
	}
	if keys[0] == keys[1] {
		t.Fatalf("two definitions sent the SAME Idempotency-Key (%s) for one workflow and revision.\n"+
			"A provider honouring the header answers the second from the first, so the activation gate "+
			"scores one definition on another's answers — and on the rehearsal, where every definition "+
			"shares the fixture name and the constant revision \"fixture\", that is every measurement.",
			keys[0])
	}
}
