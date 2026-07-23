package linkage

import (
	"os"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// TestExtractPython_RealHermesFile runs the extractor over a REAL hermes-agent source file when the
// clone is present (set HERMES_REPO to its path), proving the extractor works on production Python and
// not only a hand-written snippet. It is a spike: it prints what it recovered so a reviewer can check.
func TestExtractPython_RealHermesFile(t *testing.T) {
	repo := os.Getenv("HERMES_REPO")
	if repo == "" {
		t.Skip("set HERMES_REPO to a hermes-agent checkout to run the real-source spike")
	}
	src, err := os.ReadFile(repo + "/agent/chat_completion_helpers.py")
	if err != nil {
		t.Skipf("real file not readable: %v", err)
	}
	sites := []LLMSite{{NodeID: "dispatch", EnclosingSymbol: "_dispatch_nonstreaming_api_request"}}
	got := ExtractPythonCallSites(src, sites)
	if len(got) != 1 {
		t.Fatalf("expected one CallSite; got %d", len(got))
	}
	cs := got[0]
	t.Logf("REAL hermes _dispatch_nonstreaming_api_request: %d callees, %d state-refs", len(cs.Callees), len(cs.StateRefs))
	t.Logf("  callees: %v", cs.Callees)
	t.Logf("  stateRefs: %v", cs.StateRefs)
	if len(cs.Callees) == 0 && len(cs.StateRefs) == 0 {
		t.Errorf("extractor recovered nothing from a real 300 KB agent file — expected some call-graph/state signal")
	}
}

// A realistic hand-rolled-agent snippet, modeled on hermes-agent's real shape: a dispatch method
// forwards to a create boundary and a fallback, and every method threads the same `_session_messages`
// conversation object — exactly the linkage that framework detection misses.
const hermesShapedPy = `
class Client:
    def __init__(self):
        self._session_messages = []

    def _dispatch_nonstreaming_api_request(self, api_kwargs):
        messages = self._session_messages
        response = self._create_boundary(api_kwargs)
        self._session_messages.append(response)
        return response

    def _create_boundary(self, api_kwargs):
        msgs = self._session_messages
        return self.client.chat.completions.create(**api_kwargs)

    def _call_fallback_candidate(self, api_kwargs):
        self._session_messages.append("fallback")
        return self._dispatch_nonstreaming_api_request(api_kwargs)
`

// Task 13.1 (source side): the extractor recovers the call-graph and shared-state signals from REAL
// Python — no regex, tree-sitter — so InferStatic is fed by actual code, not a hand-built fixture.
func TestExtractPython_CallGraphAndSharedState(t *testing.T) {
	sites := []LLMSite{
		{NodeID: "dispatch", EnclosingSymbol: "Client._dispatch_nonstreaming_api_request"},
		{NodeID: "create", EnclosingSymbol: "Client._create_boundary"},
		{NodeID: "fallback", EnclosingSymbol: "Client._call_fallback_candidate"},
	}
	got := ExtractPythonCallSites([]byte(hermesShapedPy), sites)

	bySym := map[string]CallSite{}
	for _, c := range got {
		bySym[c.EnclosingSymbol] = c
	}

	// dispatch resolves `self._create_boundary` → "Client._create_boundary".
	dispatch := bySym["Client._dispatch_nonstreaming_api_request"]
	if !contains(dispatch.Callees, "Client._create_boundary") {
		t.Fatalf("dispatch should call Client._create_boundary; callees=%v", dispatch.Callees)
	}
	// every method reads/writes self._session_messages.
	for _, sym := range []string{"Client._dispatch_nonstreaming_api_request", "Client._create_boundary", "Client._call_fallback_candidate"} {
		if !contains(bySym[sym].StateRefs, "self._session_messages") {
			t.Errorf("%s should reference self._session_messages; got %v", sym, bySym[sym].StateRefs)
		}
	}
	// fallback calls dispatch (call-graph).
	if !contains(bySym["Client._call_fallback_candidate"].Callees, "Client._dispatch_nonstreaming_api_request") {
		t.Errorf("fallback should call dispatch; got %v", bySym["Client._call_fallback_candidate"].Callees)
	}

	// Feed the REAL extracted signals into InferStatic → a call-graph edge dispatch→create is recovered.
	edges := InferStatic(got)
	var sawCallGraph, sawSharedState bool
	for _, e := range edges {
		if e.From == "dispatch" && e.To == "create" && e.Signal == SignalCallGraph {
			sawCallGraph = true
		}
		if e.Signal == SignalSharedState {
			sawSharedState = true
		}
	}
	if !sawCallGraph {
		t.Fatalf("InferStatic over extracted signals should recover dispatch→create call-graph edge; got %+v", edges)
	}
	if !sawSharedState {
		t.Errorf("shared-state edges should be recovered; got %+v", edges)
	}
}

// End-to-end: real extraction → InferStatic → persisted into the IR (1.2.0) with provenance →
// read back with provenance intact. This is the write path that makes recovered topology durable.
func TestExtractPython_PersistsToIRWithProvenance(t *testing.T) {
	sites := []LLMSite{
		{NodeID: "dispatch", EnclosingSymbol: "Client._dispatch_nonstreaming_api_request"},
		{NodeID: "create", EnclosingSymbol: "Client._create_boundary"},
	}
	edges := InferStatic(ExtractPythonCallSites([]byte(hermesShapedPy), sites))
	if len(edges) == 0 {
		t.Fatal("expected recovered edges")
	}

	// Persist into an IR (the P1 write path), then read back via FromIR.
	irEdges := ToIREdges(edges)
	var sawProvenance bool
	for _, e := range irEdges {
		if e.Provenance == string(ProvInferredStatic) {
			sawProvenance = true
			if e.Confidence == 0 {
				t.Errorf("persisted inferred edge should carry a confidence; got %+v", e)
			}
		}
	}
	if !sawProvenance {
		t.Fatalf("IR edges should carry inferred_static provenance; got %+v", irEdges)
	}

	ir := &discovery.IR{Edges: irEdges}
	roundTripped := FromIR(ir)
	for _, e := range roundTripped {
		if e.From == "dispatch" && e.To == "create" {
			if e.Provenance != ProvInferredStatic {
				t.Fatalf("FromIR should preserve inferred_static provenance; got %q", e.Provenance)
			}
			return
		}
	}
	t.Fatalf("dispatch→create edge lost in IR round-trip; got %+v", roundTripped)
}

// The extractor never executes source and does not crash on messy input (tree-sitter is error-tolerant).
func TestExtractPython_MessyInputDoesNotCrash(t *testing.T) {
	got := ExtractPythonCallSites([]byte("def broken(:\n  self._x = \n  client.chat.completions.create("), []LLMSite{
		{NodeID: "n", EnclosingSymbol: "broken"},
	})
	if len(got) != 1 {
		t.Fatalf("should return one CallSite even on messy input; got %d", len(got))
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
