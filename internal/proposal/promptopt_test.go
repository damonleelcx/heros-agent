package proposal

import (
	"encoding/json"
	"strings"
	"testing"
)

func groundingReq() PromptOptimizeRequest {
	return PromptOptimizeRequest{
		NodeID:         "answer",
		BasePromptRef:  strings.Repeat("p", 64),
		BasePromptBody: "Classify the ticket.",
		RequiredFields: []string{"label", "confidence"},
		FailingCases: []FailingCaseGrounding{
			{CaseID: "c2", FailureReason: "missing field confidence", TraceRef: strings.Repeat("b", 64)},
			{CaseID: "c1", FailureReason: "missing field label", TraceRef: strings.Repeat("a", 64)},
		},
	}
}

// §2.1 / §2.4: the optimizer proposes an edit grounded in the specific failing cases, with a
// format-constraint pinning the violated fields, traceable to those cases.
func TestSelfRefine_GroundedAndTraceable(t *testing.T) {
	edit, err := SelfRefineOptimizer{}.Optimize(groundingReq())
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	for _, id := range []string{"c1", "c2"} {
		if !edit.Grounding.GroundedIn(id) {
			t.Errorf("edit is not traceable to case %s", id)
		}
	}
	if edit.FormatConstraint == "" {
		t.Fatal("no format constraint was added for a contract violation")
	}
	if !strings.Contains(edit.NewPromptBody, "label") || !strings.Contains(edit.NewPromptBody, "confidence") {
		t.Errorf("format constraint did not pin the violated fields:\n%s", edit.NewPromptBody)
	}
	// The edit references the specific observed failures, i.e. it is grounded, not generic.
	if !strings.Contains(edit.NewPromptBody, "missing field label") {
		t.Errorf("rewrite is not grounded in the observed failure reasons:\n%s", edit.NewPromptBody)
	}
}

// §2.2: an ungrounded rewrite (no attached cases) is rejected, not emitted.
func TestSelfRefine_RejectsUngrounded(t *testing.T) {
	_, err := SelfRefineOptimizer{}.Optimize(PromptOptimizeRequest{NodeID: "answer", BasePromptBody: "x"})
	if err != ErrUngrounded {
		t.Fatalf("want ErrUngrounded for a rewrite with no attached cases, got %v", err)
	}
}

// §2.2: the grounding hash is a pure function of the grounding CONTENT — case order does not change it.
func TestGrounding_HashIsOrderIndependent(t *testing.T) {
	a, err := SelfRefineOptimizer{}.Optimize(groundingReq())
	if err != nil {
		t.Fatal(err)
	}
	// Reverse the case order in the request; the grounded content is the same, so the hash must match.
	req := groundingReq()
	req.FailingCases[0], req.FailingCases[1] = req.FailingCases[1], req.FailingCases[0]
	b, err := SelfRefineOptimizer{}.Optimize(req)
	if err != nil {
		t.Fatal(err)
	}
	if a.Grounding.Hash != b.Grounding.Hash {
		t.Errorf("grounding hash depends on case order: %s != %s", a.Grounding.Hash, b.Grounding.Hash)
	}
}

// §2.3: optimizer inputs (failing-case bundle, possible PII) and rendered prompts are stored as
// content-hashed blobs, retrievable by hash — never inline. Only the hashes are safe to log.
func TestPersistRewrite_StoresContentHashedBlobs(t *testing.T) {
	edit, err := SelfRefineOptimizer{}.Optimize(groundingReq())
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemBlobStore()
	p, err := PersistRewrite(store, edit)
	if err != nil {
		t.Fatalf("PersistRewrite: %v", err)
	}
	if len(p.GroundingHash) != 64 || len(p.PromptHash) != 64 {
		t.Fatalf("blob references must be 64-hex content hashes: %+v", p)
	}
	// The prompt body is retrievable by hash and byte-identical.
	body, ok := store.Get(p.PromptHash)
	if !ok || string(body) != edit.NewPromptBody {
		t.Error("rendered prompt was not stored content-addressably")
	}
	// The grounding bundle round-trips by hash.
	gb, ok := store.Get(p.GroundingHash)
	if !ok {
		t.Fatal("grounding bundle blob missing")
	}
	var got GroundingBundle
	if err := json.Unmarshal(gb, &got); err != nil {
		t.Fatalf("grounding blob is not the bundle: %v", err)
	}
	if got.Hash != edit.Grounding.Hash {
		t.Errorf("stored grounding hash %s != edit grounding hash %s", got.Hash, edit.Grounding.Hash)
	}
}
