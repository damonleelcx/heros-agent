package registry

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fakeHost records the resolved request a policy issues and returns canned results. It lets a test
// assert the trusted-host call path (tasks 1.3, 1.4) and the resolved-request determinism ceiling
// (task 1.8) without a real gateway or retriever.
type fakeHost struct {
	summarizeReqs []ResolvedRequest
	retrieveReqs  []ResolvedRequest
	summary       string
	chunks        []Chunk
	summErr       error
	retrErr       error
}

func (h *fakeHost) Summarize(_ context.Context, req ResolvedRequest) (string, error) {
	h.summarizeReqs = append(h.summarizeReqs, req)
	return h.summary, h.summErr
}

func (h *fakeHost) Retrieve(_ context.Context, req ResolvedRequest) ([]Chunk, error) {
	h.retrieveReqs = append(h.retrieveReqs, req)
	return h.chunks, h.retrErr
}

func convOf(pairs ...string) Conversation {
	var msgs []Message
	for i := 0; i+1 < len(pairs); i += 2 {
		msgs = append(msgs, Message{Role: pairs[i], Content: pairs[i+1]})
	}
	return Conversation{Messages: msgs}
}

func policyByName(t *testing.T, name string) Policy {
	t.Helper()
	for _, p := range BuiltinPolicies() {
		if p.Name() == name {
			return p
		}
	}
	t.Fatalf("policy %q is not a builtin", name)
	return nil
}

// Task 1.1 / spec "All five policies are selectable by name": each spec-named policy is a builtin.
func TestBuiltinPolicies_AllFiveSpecNamesPresent(t *testing.T) {
	for _, name := range []string{"full-history", "sliding-window", "summarization", "rag-retrieval", "semantic-compaction"} {
		if policyByName(t, name) == nil {
			t.Fatalf("policy %q missing", name)
		}
	}
}

// Task 1.2 + 1.8: sliding-window keeps the last window_size messages and is byte-identical across runs.
func TestSlidingWindow_KeepsTailAndIsDeterministic(t *testing.T) {
	p := policyByName(t, "sliding-window")
	conv := convOf("user", "a", "assistant", "b", "user", "c", "assistant", "d")
	params := json.RawMessage(`{"window_size":2}`)

	got1, err := p.Assemble(context.Background(), nil, conv, params, 7)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(got1.Messages) != 2 || got1.Messages[0].Content != "c" || got1.Messages[1].Content != "d" {
		t.Fatalf("window did not keep the last two: %+v", got1.Messages)
	}
	if got1.SourceMessageCount != 4 {
		t.Errorf("source count = %d, want 4", got1.SourceMessageCount)
	}
	got2, _ := p.Assemble(context.Background(), nil, conv, params, 7)
	if !reflect.DeepEqual(got1, got2) {
		t.Errorf("sliding-window not deterministic:\n%+v\n%+v", got1, got2)
	}
	// window larger than the conversation keeps everything.
	all, _ := p.Assemble(context.Background(), nil, conv, json.RawMessage(`{"window_size":100}`), 7)
	if len(all.Messages) != 4 {
		t.Errorf("oversized window dropped messages: %d", len(all.Messages))
	}
}

// Task 1.6 / spec "Out-of-range params fail closed": window_size ≤ 0 and top_k ≤ 0 are rejected with a
// typed error that names the policy and the param, and nothing is assembled.
func TestParams_FailClosed(t *testing.T) {
	cases := []struct {
		policy string
		params string
		param  string
	}{
		{"sliding-window", `{"window_size":0}`, "window_size"},
		{"sliding-window", `{"window_size":-3}`, "window_size"},
		{"rag-retrieval", `{"top_k":0,"retriever_ref":"r"}`, "top_k"},
		{"rag-retrieval", `{"top_k":-1,"retriever_ref":"r"}`, "top_k"},
		{"semantic-compaction", `{"target_tokens":0}`, "target_tokens"},
	}
	for _, tc := range cases {
		p := policyByName(t, tc.policy)
		out, err := p.Assemble(context.Background(), &fakeHost{}, convOf("user", "hi"), json.RawMessage(tc.params), 1)
		if err == nil {
			t.Fatalf("%s %s: expected fail-closed, got %+v", tc.policy, tc.params, out)
		}
		if !errors.Is(err, ErrInvalidPolicyParams) {
			t.Errorf("%s: want ErrInvalidPolicyParams, got %v", tc.policy, err)
		}
		var pe *PolicyParamError
		if !errors.As(err, &pe) || pe.Policy != tc.policy || pe.Param != tc.param {
			t.Errorf("%s: error should name policy+param, got %v", tc.policy, err)
		}
		if len(out.Messages) != 0 {
			t.Errorf("%s: assembled context despite bad params: %+v", tc.policy, out.Messages)
		}
	}
}

// Unknown params are a loud error, never silently ignored (backend rule).
func TestParams_UnknownFieldRejected(t *testing.T) {
	p := policyByName(t, "sliding-window")
	_, err := p.Assemble(context.Background(), nil, convOf("user", "hi"), json.RawMessage(`{"window_size":2,"typo":9}`), 1)
	if !errors.Is(err, ErrInvalidPolicyParams) {
		t.Fatalf("unknown param not rejected: %v", err)
	}
}

// Task 1.3 + 1.8: summarization calls the host summarizer (host-side, credential-free path) and the
// resolved request is identical across two runs with the same params + seed.
func TestSummarization_CallsHostAndResolvesIdenticalRequest(t *testing.T) {
	p := policyByName(t, "summarization")
	conv := convOf("user", "a long turn", "assistant", "a longer reply that should be summarized")
	params := json.RawMessage(`{"summarizer_model_ref":"model_v1"}`)

	h := &fakeHost{summary: "SUMMARY"}
	out, err := p.Assemble(context.Background(), h, conv, params, 42)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(h.summarizeReqs) != 1 {
		t.Fatalf("summarizer was not called exactly once: %d", len(h.summarizeReqs))
	}
	if h.summarizeReqs[0].ModelRef != "model_v1" || h.summarizeReqs[0].Seed != 42 {
		t.Errorf("resolved request wrong: %+v", h.summarizeReqs[0])
	}
	if len(out.Messages) != 1 || out.Messages[0].Content != "SUMMARY" {
		t.Errorf("summary not assembled: %+v", out.Messages)
	}
	if !out.Lossy || out.DropRatio <= 0 {
		t.Errorf("summarization must report a drop ratio > 0, got lossy=%v ratio=%v", out.Lossy, out.DropRatio)
	}
	if out.ResolvedRequest == nil {
		t.Fatal("summarization must expose its resolved request for determinism assertions")
	}

	// Determinism ceiling: identical resolved request under the same seed.
	h2 := &fakeHost{summary: "SUMMARY"}
	out2, _ := p.Assemble(context.Background(), h2, conv, params, 42)
	if !reflect.DeepEqual(h.summarizeReqs[0], h2.summarizeReqs[0]) {
		t.Errorf("resolved request not identical across runs:\n%+v\n%+v", h.summarizeReqs[0], h2.summarizeReqs[0])
	}
	if !reflect.DeepEqual(out.ResolvedRequest, out2.ResolvedRequest) {
		t.Error("resolved request differs between runs")
	}
}

// Summarization fails closed when host services are absent — it must not silently skip the summarizer.
func TestSummarization_FailsClosedWithoutHost(t *testing.T) {
	p := policyByName(t, "summarization")
	_, err := p.Assemble(context.Background(), nil, convOf("user", "hi"), json.RawMessage(`{"summarizer_model_ref":"m"}`), 1)
	if !errors.Is(err, ErrInvalidPolicyParams) {
		t.Fatalf("want fail-closed without host, got %v", err)
	}
}

// Task 1.4: rag-retrieval retrieves top-k on the host, prepends chunks, and an optional rerank carries
// the seed in the resolved request.
func TestRAGRetrieval_TopKAndRerankSeed(t *testing.T) {
	p := policyByName(t, "rag-retrieval")
	conv := convOf("user", "what is the capital of France?")
	h := &fakeHost{chunks: []Chunk{{ID: "1", Text: "Paris is the capital."}, {ID: "2", Text: "France is in Europe."}}}

	out, err := p.Assemble(context.Background(), h, conv, json.RawMessage(`{"top_k":5,"retriever_ref":"ret_v1","rerank":true}`), 99)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(h.retrieveReqs) != 1 {
		t.Fatalf("retriever not called once: %d", len(h.retrieveReqs))
	}
	r := h.retrieveReqs[0]
	if r.TopK != 5 || r.Ref != "ret_v1" || r.Op != "rerank" || r.Seed != 99 {
		t.Errorf("resolved retrieve request wrong: %+v", r)
	}
	if r.Query != "what is the capital of France?" {
		t.Errorf("query should be the latest user turn, got %q", r.Query)
	}
	if out.RetrievedChunks != 2 {
		t.Errorf("retrieved-chunk count = %d, want 2", out.RetrievedChunks)
	}
	// chunks prepended, original conversation preserved.
	if len(out.Messages) != 3 || out.Messages[0].Content != "Paris is the capital." || out.Messages[2].Role != "user" {
		t.Errorf("chunks not prepended ahead of the conversation: %+v", out.Messages)
	}
	// Without rerank, op is a plain retrieve.
	h2 := &fakeHost{chunks: h.chunks}
	_, _ = p.Assemble(context.Background(), h2, conv, json.RawMessage(`{"top_k":5,"retriever_ref":"ret_v1"}`), 99)
	if h2.retrieveReqs[0].Op != "retrieve" {
		t.Errorf("non-rerank op = %q, want retrieve", h2.retrieveReqs[0].Op)
	}
}

// Task 1.5 + 1.8: semantic-compaction bounds assembled tokens under target_tokens, emits a drop ratio,
// and is byte-identical across runs.
func TestSemanticCompaction_BoundsTokensAndReportsDrop(t *testing.T) {
	p := policyByName(t, "semantic-compaction")
	// Six messages of ~7 tokens each (~28 chars). target 10 tokens keeps only the most recent 1.
	conv := convOf(
		"user", "aaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"assistant", "bbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"user", "cccccccccccccccccccccccccccc",
		"assistant", "dddddddddddddddddddddddddddd",
		"user", "eeeeeeeeeeeeeeeeeeeeeeeeeeee",
		"assistant", "ffffffffffffffffffffffffffff",
	)
	out, err := p.Assemble(context.Background(), nil, conv, json.RawMessage(`{"target_tokens":10}`), 5)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if out.AssembledTokens > 10 {
		t.Errorf("compaction exceeded target: %d > 10", out.AssembledTokens)
	}
	if len(out.Messages) == 0 || out.Messages[len(out.Messages)-1].Content[:1] != "f" {
		t.Errorf("compaction did not keep the most recent message: %+v", out.Messages)
	}
	if !out.Lossy || out.DropRatio <= 0 {
		t.Errorf("compaction must report drop ratio > 0, got lossy=%v ratio=%v", out.Lossy, out.DropRatio)
	}
	// Determinism.
	out2, _ := p.Assemble(context.Background(), nil, conv, json.RawMessage(`{"target_tokens":10}`), 5)
	if !reflect.DeepEqual(out, out2) {
		t.Error("semantic-compaction not deterministic")
	}
	// A conversation already under target is passed through with no drop.
	small := convOf("user", "hi")
	under, _ := p.Assemble(context.Background(), nil, small, json.RawMessage(`{"target_tokens":1000}`), 5)
	if under.DropRatio != 0 || len(under.Messages) != 1 {
		t.Errorf("under-target conversation should be lossless: %+v", under)
	}
}

// full-history is the lossless identity: assembled == source, no host call, deterministic.
func TestFullHistory_Identity(t *testing.T) {
	p := policyByName(t, "full-history")
	conv := convOf("user", "a", "assistant", "b")
	out, err := p.Assemble(context.Background(), nil, conv, nil, 1)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if !reflect.DeepEqual(out.Messages, conv.Messages) || out.DropRatio != 0 || out.Lossy {
		t.Errorf("full-history is not the lossless identity: %+v", out)
	}
}

// Task 1.7 / spec "Swapping the policy changes assembly and config_hash only": a context entry's
// content address (which is hashed into config_hash) changes when the policy or its params change, and
// is stable when they do not. seal() is the same canonicalizer that produces config_hash, so this is
// the registry half of the per-node config-swap guarantee — no workflow code is involved.
func TestContextEntry_ConfigSwapChangesContentAddressOnly(t *testing.T) {
	idFull, _, err := seal(KindContext, "ctx", ContextSpec{Policy: "full-history", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	idWin10, _, _ := seal(KindContext, "ctx", ContextSpec{Policy: "sliding-window", Params: json.RawMessage(`{"window_size":10}`)})
	idWin20, _, _ := seal(KindContext, "ctx", ContextSpec{Policy: "sliding-window", Params: json.RawMessage(`{"window_size":20}`)})

	if idFull == idWin10 {
		t.Error("swapping full-history → sliding-window did not change the content address")
	}
	if idWin10 == idWin20 {
		t.Error("changing window_size did not change the content address")
	}
	// Stable when nothing changes (config_hash is a function of config, not of when it was computed).
	again, _, _ := seal(KindContext, "ctx", ContextSpec{Policy: "sliding-window", Params: json.RawMessage(`{"window_size":10}`)})
	if again != idWin10 {
		t.Error("identical spec produced a different content address")
	}
}

// Two nodes, two policies, one build: each assembles independently under its own config (spec scenario
// "Two nodes in one workflow use different policies").
func TestTwoNodes_DifferentPoliciesAssembleIndependently(t *testing.T) {
	conv := convOf("user", "a", "assistant", "b", "user", "c")
	rag := policyByName(t, "rag-retrieval")
	summ := policyByName(t, "summarization")
	h := &fakeHost{chunks: []Chunk{{Text: "chunk"}}, summary: "s"}

	a, err := rag.Assemble(context.Background(), h, conv, json.RawMessage(`{"top_k":5,"retriever_ref":"r"}`), 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := summ.Assemble(context.Background(), h, conv, json.RawMessage(`{"summarizer_model_ref":"m"}`), 1)
	if err != nil {
		t.Fatal(err)
	}
	if a.RetrievedChunks != 1 || b.ResolvedRequest.Op != "summarize" {
		t.Errorf("policies did not assemble independently: rag=%+v summ=%+v", a, b)
	}
}

// Task 6.1 / 6.3 (AI-engineer lens): the policy family matches the context-engineering discipline
// exactly, and every LOSSY policy reports a drop ratio so a "compaction dropped the answer" defect is
// diagnosable in P4. This test is the executable form of the context-semantics confirmation.
func TestPolicyFamily_MatchesContextEngineeringDiscipline(t *testing.T) {
	// The five disciplines: full history / sliding window / summarization / RAG just-in-time retrieval /
	// semantic compaction. Each is present under its spec name.
	family := map[string]bool{}
	for _, p := range BuiltinPolicies() {
		family[p.Name()] = true
	}
	for _, name := range []string{"full-history", "sliding-window", "summarization", "rag-retrieval", "semantic-compaction"} {
		if !family[name] {
			t.Errorf("context-engineering discipline %q is not implemented", name)
		}
	}

	// Lossy policies MUST mark Lossy and populate a drop ratio; lossless ones must not claim a drop.
	conv := convOf("user", "aaaaaaaaaaaaaaaaaaaa", "assistant", "bbbbbbbbbbbbbbbbbbbb", "user", "cccccccccccccccccccc")
	h := &fakeHost{summary: "s"}
	summ, _ := SummarizationPolicy{}.Assemble(context.Background(), h, conv, json.RawMessage(`{"summarizer_model_ref":"m"}`), 1)
	comp, _ := SemanticCompactionPolicy{}.Assemble(context.Background(), nil, conv, json.RawMessage(`{"target_tokens":5}`), 1)
	for name, out := range map[string]AssembledContext{"summarization": summ, "semantic-compaction": comp} {
		if !out.Lossy {
			t.Errorf("%s must be marked lossy", name)
		}
		if out.DropRatio <= 0 {
			t.Errorf("%s must report a drop ratio > 0 for a compacted conversation, got %v", name, out.DropRatio)
		}
	}
	// full-history / sliding-window are lossless.
	full, _ := FullHistoryPolicy{}.Assemble(context.Background(), nil, conv, nil, 1)
	win, _ := SlidingWindowPolicy{}.Assemble(context.Background(), nil, conv, json.RawMessage(`{"window_size":2}`), 1)
	for name, out := range map[string]AssembledContext{"full-history": full, "sliding-window": win} {
		if out.Lossy {
			t.Errorf("%s must not be marked lossy", name)
		}
	}
}

// Task 7.1 fixture: a long conversation that exercises every policy boundary in one place — window
// truncation, compaction token-bound, retrieval augmentation, summarization collapse, and the full
// identity. This is the context-side counterpart to the malicious repo-tool set.
func longConversation(nTurns int) Conversation {
	var msgs []Message
	for i := 0; i < nTurns; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		// ~10 tokens per message so token-bounded policies have something to trim.
		msgs = append(msgs, Message{Role: role, Content: strings.Repeat("word ", 8) + itoa(i)})
	}
	return Conversation{Messages: msgs}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestLongConversation_EveryPolicyBoundary(t *testing.T) {
	conv := longConversation(40)
	h := &fakeHost{summary: "digest", chunks: []Chunk{{Text: "c1"}, {Text: "c2"}, {Text: "c3"}}}
	ctx := context.Background()

	// full-history keeps all 40.
	full, _ := FullHistoryPolicy{}.Assemble(ctx, nil, conv, nil, 1)
	if len(full.Messages) != 40 {
		t.Errorf("full-history kept %d, want 40", len(full.Messages))
	}
	// sliding-window keeps exactly the last 12.
	win, _ := SlidingWindowPolicy{}.Assemble(ctx, nil, conv, json.RawMessage(`{"window_size":12}`), 1)
	if len(win.Messages) != 12 || win.Messages[11].Content != conv.Messages[39].Content {
		t.Errorf("sliding-window boundary wrong: kept %d, last=%q", len(win.Messages), win.Messages[len(win.Messages)-1].Content)
	}
	// semantic-compaction stays under the token bound and drops the rest.
	comp, _ := SemanticCompactionPolicy{}.Assemble(ctx, nil, conv, json.RawMessage(`{"target_tokens":40}`), 1)
	if comp.AssembledTokens > 40 {
		t.Errorf("compaction exceeded target: %d", comp.AssembledTokens)
	}
	if comp.SourceMessageCount != 40 || len(comp.Messages) >= 40 || comp.DropRatio <= 0 {
		t.Errorf("compaction did not drop from a 40-turn conversation: %+v", comp)
	}
	// rag-retrieval augments: 3 chunks prepended ahead of all 40 turns.
	rag, _ := RAGRetrievalPolicy{}.Assemble(ctx, h, conv, json.RawMessage(`{"top_k":3,"retriever_ref":"r"}`), 1)
	if rag.RetrievedChunks != 3 || len(rag.Messages) != 43 {
		t.Errorf("rag augmentation wrong: chunks=%d total=%d", rag.RetrievedChunks, len(rag.Messages))
	}
	// summarization collapses 40 turns to a single lossy digest.
	summ, _ := SummarizationPolicy{}.Assemble(ctx, h, conv, json.RawMessage(`{"summarizer_model_ref":"m"}`), 1)
	if len(summ.Messages) != 1 || !summ.Lossy || summ.DropRatio <= 0 {
		t.Errorf("summarization did not collapse the conversation lossily: %+v", summ)
	}
}
