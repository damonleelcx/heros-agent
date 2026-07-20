package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// This file implements the P3 context-engineering strategies behind the P2 Policy interface
// (context-strategies spec). Every policy is host-side and deterministic given policy + params +
// conversation + seed: LLM-free policies (`full-history`, `sliding-window`, `semantic-compaction`)
// produce a byte-identical assembly; policies with an LLM/retriever step (`summarization`,
// reranked `rag-retrieval`) produce an identical *resolved request* under the fixed seed. A policy
// that needs a model or retriever reaches it through HostServices — the trusted host's gateway —
// so no provider credential is ever exposed to a sandbox.

// Message is one conversation message a context policy assembles over. It is deliberately minimal —
// the policy layer reasons about role + content, not a provider's wire shape.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Conversation is the node's input conversation, oldest message first.
type Conversation struct {
	Messages []Message `json:"messages"`
}

// AssembledContext is a policy's output plus the telemetry a run needs to slice it (task 1.9). A
// lossy policy MUST populate DropRatio so a "compaction dropped the answer" defect is measurable in
// P4 (Decision 7); rag-retrieval populates RetrievedChunks; an LLM-using policy exposes ResolvedRequest
// so determinism is assertable as "identical resolved request" rather than "identical bytes".
type AssembledContext struct {
	Messages           []Message
	AssembledTokens    int
	SourceMessageCount int
	// DropRatio is the fraction of source tokens the policy dropped, in [0,1]. 0 for lossless policies.
	DropRatio float64
	// RetrievedChunks is how many chunks a retrieval policy pulled in. 0 for non-retrieval policies.
	RetrievedChunks int
	// Lossy marks a policy that can drop information (summarization, compaction). The telemetry layer
	// emits a drop-ratio event only for lossy policies, so a lossless policy's 0.0 is not mistaken for
	// "measured no drop".
	Lossy bool
	// ResolvedRequest is the deterministic, seed-bound request an LLM/retriever policy issued. Non-nil
	// only for `summarization` and reranked `rag-retrieval`; it is the determinism handle (P2's
	// reproducibility ceiling: identical resolved request under a fixed seed, not identical provider bytes).
	ResolvedRequest *ResolvedRequest
}

// ResolvedRequest is what an LLM/retriever policy resolved and handed to HostServices. Captured so two
// runs of the same policy + params + conversation + seed can be proven to have issued the identical
// request even though the provider's output bytes are outside the determinism contract.
type ResolvedRequest struct {
	Op       string    `json:"op"` // "summarize" | "retrieve" | "rerank"
	ModelRef string    `json:"model_ref,omitempty"`
	Ref      string    `json:"ref,omitempty"` // retriever_ref for retrieval
	Query    string    `json:"query,omitempty"`
	TopK     int       `json:"top_k,omitempty"`
	Seed     int64     `json:"seed"`
	Messages []Message `json:"messages,omitempty"`
}

// Chunk is one retrieved passage.
type Chunk struct {
	ID    string  `json:"id"`
	Text  string  `json:"text"`
	Score float64 `json:"score"`
}

// HostServices are the trusted-host operations a context policy may need. summarization and
// rag-retrieval call these; the host performs the model/retrieval call through the P2 provider
// gateway with real secrets. This interface is the seam that keeps the credential on the host: a
// policy never holds one, and — critically — this call path is host-side, never inside a sandbox
// isolate (context-strategies spec: "execute that call on the trusted host, never from a sandboxed node").
type HostServices interface {
	// Summarize runs the summarizer model named by req.ModelRef and returns the summary text.
	Summarize(ctx context.Context, req ResolvedRequest) (summary string, err error)
	// Retrieve resolves req.Ref and returns up to req.TopK chunks; if req.Op is "rerank" the seed
	// threads reproducibility through the rerank.
	Retrieve(ctx context.Context, req ResolvedRequest) (chunks []Chunk, err error)
}

// ErrInvalidPolicyParams is returned when a node's context-policy params are out of range or malformed
// at resolution — negative window_size, top_k ≤ 0, a missing required ref. The node fails closed rather
// than assembling under an unvalidated policy (context-strategies spec: "out-of-range params fail closed").
var ErrInvalidPolicyParams = errors.New("registry: invalid context-policy params")

// PolicyParamError names the policy and the offending param so the unhappy path tells the user exactly
// which dimension broke (Product Designer discipline: design the unhappy path first).
type PolicyParamError struct {
	Policy string
	Param  string
	Reason string
}

func (e *PolicyParamError) Error() string {
	if e.Param == "" {
		return fmt.Sprintf("%s: context policy %q: %s", ErrInvalidPolicyParams.Error(), e.Policy, e.Reason)
	}
	return fmt.Sprintf("%s: context policy %q param %q: %s", ErrInvalidPolicyParams.Error(), e.Policy, e.Param, e.Reason)
}

func (e *PolicyParamError) Unwrap() error { return ErrInvalidPolicyParams }

func paramErr(policy, param, reason string) error {
	return &PolicyParamError{Policy: policy, Param: param, Reason: reason}
}

// ─────────────────────────────────────────────────────────────────────────────
// full-history (alias of the P2 `full` policy, under the context-strategies spec name)
// ─────────────────────────────────────────────────────────────────────────────

// FullHistoryPolicy is the spec-named identity policy. It shares FullPolicy's behavior exactly so a
// Variant Spec may select either the P2 name `full` or the P3 spec name `full-history`; keeping both
// avoids renaming P2's pinned entries while satisfying the P3 "five named policies" requirement.
type FullHistoryPolicy struct{}

func (FullHistoryPolicy) Name() string                  { return "full-history" }
func (FullHistoryPolicy) ParamsSchema() json.RawMessage { return FullPolicy{}.ParamsSchema() }
func (FullHistoryPolicy) Assemble(ctx context.Context, host HostServices, conv Conversation, params json.RawMessage, seed int64) (AssembledContext, error) {
	return FullPolicy{}.Assemble(ctx, host, conv, params, seed)
}

// ─────────────────────────────────────────────────────────────────────────────
// sliding-window {window_size}
// ─────────────────────────────────────────────────────────────────────────────

// SlidingWindowPolicy keeps the most recent window_size messages. Deterministic and LLM-free: the
// output is a verbatim tail of the input, so two runs with the same params are byte-identical.
type SlidingWindowPolicy struct{}

func (SlidingWindowPolicy) Name() string { return "sliding-window" }

func (SlidingWindowPolicy) ParamsSchema() json.RawMessage {
	// minimum:1 makes window_size ≤ 0 a registration-time rejection AND a resolution-time one; the
	// Assemble check below is defense in depth for a params object that reached the node another way.
	return json.RawMessage(`{"type":"object","properties":{"window_size":{"type":"integer","minimum":1}},"required":["window_size"],"additionalProperties":false}`)
}

type slidingWindowParams struct {
	WindowSize int `json:"window_size"`
}

func (SlidingWindowPolicy) Assemble(_ context.Context, _ HostServices, conv Conversation, params json.RawMessage, _ int64) (AssembledContext, error) {
	var p slidingWindowParams
	if err := strictUnmarshal("sliding-window", params, &p); err != nil {
		return AssembledContext{}, err
	}
	if p.WindowSize <= 0 {
		return AssembledContext{}, paramErr("sliding-window", "window_size", "must be > 0")
	}
	msgs := conv.Messages
	if len(msgs) > p.WindowSize {
		msgs = msgs[len(msgs)-p.WindowSize:]
	}
	out := copyMessages(msgs)
	return AssembledContext{
		Messages:           out,
		AssembledTokens:    estimateTokens(out),
		SourceMessageCount: len(conv.Messages),
		DropRatio:          0, // windowing keeps whole messages; nothing is lossily rewritten
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// summarization {summarizer_model_ref}
// ─────────────────────────────────────────────────────────────────────────────

// SummarizationPolicy replaces the conversation with a single summary produced by a summarizer model
// on the trusted host. Lossy: it emits a drop ratio. Determinism is at the resolved-request level —
// the request sent to the summarizer (model ref, seed, input messages) is identical across runs.
type SummarizationPolicy struct{}

func (SummarizationPolicy) Name() string { return "summarization" }

func (SummarizationPolicy) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"summarizer_model_ref":{"type":"string","minLength":1}},"required":["summarizer_model_ref"],"additionalProperties":false}`)
}

type summarizationParams struct {
	SummarizerModelRef string `json:"summarizer_model_ref"`
}

func (SummarizationPolicy) Assemble(ctx context.Context, host HostServices, conv Conversation, params json.RawMessage, seed int64) (AssembledContext, error) {
	var p summarizationParams
	if err := strictUnmarshal("summarization", params, &p); err != nil {
		return AssembledContext{}, err
	}
	if p.SummarizerModelRef == "" {
		return AssembledContext{}, paramErr("summarization", "summarizer_model_ref", "must not be empty")
	}
	if host == nil {
		// A summarization policy with no host services would silently degrade to running the summarizer
		// nowhere. Fail closed: the node does not assemble under a half-wired policy.
		return AssembledContext{}, paramErr("summarization", "", "requires host services to reach the summarizer model")
	}
	req := ResolvedRequest{
		Op:       "summarize",
		ModelRef: p.SummarizerModelRef,
		Seed:     seed,
		Messages: copyMessages(conv.Messages), // the resolved request is deterministic given the input
	}
	summary, err := host.Summarize(ctx, req)
	if err != nil {
		return AssembledContext{}, fmt.Errorf("summarization: summarizer %q failed: %w", p.SummarizerModelRef, err)
	}
	out := []Message{{Role: "system", Content: summary}}
	sourceTokens := estimateTokens(conv.Messages)
	assembled := estimateTokens(out)
	return AssembledContext{
		Messages:           out,
		AssembledTokens:    assembled,
		SourceMessageCount: len(conv.Messages),
		DropRatio:          dropRatio(sourceTokens, assembled),
		Lossy:              true,
		ResolvedRequest:    &req,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// rag-retrieval {top_k, retriever_ref}
// ─────────────────────────────────────────────────────────────────────────────

// RAGRetrievalPolicy retrieves top_k chunks for the latest user turn and prepends them as context.
// Retrieval runs on the trusted host via HostServices. An optional rerank threads the seed so the
// resolved request is reproducible.
type RAGRetrievalPolicy struct{}

func (RAGRetrievalPolicy) Name() string { return "rag-retrieval" }

func (RAGRetrievalPolicy) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"top_k":{"type":"integer","minimum":1},"retriever_ref":{"type":"string","minLength":1},"rerank":{"type":"boolean"}},"required":["top_k","retriever_ref"],"additionalProperties":false}`)
}

type ragParams struct {
	TopK         int    `json:"top_k"`
	RetrieverRef string `json:"retriever_ref"`
	Rerank       bool   `json:"rerank"`
}

func (RAGRetrievalPolicy) Assemble(ctx context.Context, host HostServices, conv Conversation, params json.RawMessage, seed int64) (AssembledContext, error) {
	var p ragParams
	if err := strictUnmarshal("rag-retrieval", params, &p); err != nil {
		return AssembledContext{}, err
	}
	if p.TopK <= 0 {
		return AssembledContext{}, paramErr("rag-retrieval", "top_k", "must be > 0")
	}
	if p.RetrieverRef == "" {
		return AssembledContext{}, paramErr("rag-retrieval", "retriever_ref", "must not be empty")
	}
	if host == nil {
		return AssembledContext{}, paramErr("rag-retrieval", "", "requires host services to reach the retriever")
	}
	op := "retrieve"
	if p.Rerank {
		op = "rerank" // an optional rerank carries the seed for reproducibility (task 1.4)
	}
	req := ResolvedRequest{
		Op:    op,
		Ref:   p.RetrieverRef,
		Query: latestUserQuery(conv.Messages),
		TopK:  p.TopK,
		Seed:  seed,
	}
	chunks, err := host.Retrieve(ctx, req)
	if err != nil {
		return AssembledContext{}, fmt.Errorf("rag-retrieval: retriever %q failed: %w", p.RetrieverRef, err)
	}
	// Just-in-time retrieval: the retrieved chunks are PREPENDED as a system context block, ahead of
	// the conversation, so the original turns are preserved (retrieval augments, it does not drop).
	out := make([]Message, 0, len(chunks)+len(conv.Messages))
	for _, c := range chunks {
		out = append(out, Message{Role: "system", Content: c.Text})
	}
	out = append(out, copyMessages(conv.Messages)...)
	return AssembledContext{
		Messages:           out,
		AssembledTokens:    estimateTokens(out),
		SourceMessageCount: len(conv.Messages),
		DropRatio:          0, // augmentation, not compaction
		RetrievedChunks:    len(chunks),
		ResolvedRequest:    &req,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// semantic-compaction {target_tokens}
// ─────────────────────────────────────────────────────────────────────────────

// SemanticCompactionPolicy deterministically drops whole messages, oldest first, until the assembled
// context fits under target_tokens. LLM-free and byte-identical across runs. Lossy: it emits a drop
// ratio so P4 can measure when compaction removed the answer.
type SemanticCompactionPolicy struct{}

func (SemanticCompactionPolicy) Name() string { return "semantic-compaction" }

func (SemanticCompactionPolicy) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"target_tokens":{"type":"integer","minimum":1}},"required":["target_tokens"],"additionalProperties":false}`)
}

type compactionParams struct {
	TargetTokens int `json:"target_tokens"`
}

func (SemanticCompactionPolicy) Assemble(_ context.Context, _ HostServices, conv Conversation, params json.RawMessage, _ int64) (AssembledContext, error) {
	var p compactionParams
	if err := strictUnmarshal("semantic-compaction", params, &p); err != nil {
		return AssembledContext{}, err
	}
	if p.TargetTokens <= 0 {
		return AssembledContext{}, paramErr("semantic-compaction", "target_tokens", "must be > 0")
	}
	sourceTokens := estimateTokens(conv.Messages)
	// Keep the most-recent messages whose running total stays within target_tokens. Walk newest→oldest
	// so recency wins; a single message larger than the target is kept alone (dropping everything would
	// assemble empty context, a worse failure than one over-budget message). Deterministic: no map order,
	// no clock, integer arithmetic only.
	kept := 0
	total := 0
	for i := len(conv.Messages) - 1; i >= 0; i-- {
		t := estimateTokens(conv.Messages[i : i+1])
		if kept > 0 && total+t > p.TargetTokens {
			break
		}
		total += t
		kept++
	}
	out := copyMessages(conv.Messages[len(conv.Messages)-kept:])
	assembled := estimateTokens(out)
	return AssembledContext{
		Messages:           out,
		AssembledTokens:    assembled,
		SourceMessageCount: len(conv.Messages),
		DropRatio:          dropRatio(sourceTokens, assembled),
		Lossy:              true,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

// requireNoParams rejects any params on a no-param policy (`full`/`full-history`). additionalProperties
// is also enforced at registration, but a resolution-time check keeps a policy honest if params reach
// Assemble by another route.
func requireNoParams(policy string, params json.RawMessage) error {
	if len(params) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(params, &m); err != nil {
		return paramErr(policy, "", "params is not valid JSON")
	}
	if len(m) > 0 {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return paramErr(policy, keys[0], "policy takes no params")
	}
	return nil
}

// strictUnmarshal decodes params and rejects unknown fields, so a typo'd param name is a loud error
// rather than a silently-ignored setting (backend rule: fail loud, no silent fallback).
func strictUnmarshal(policy string, params json.RawMessage, dst any) error {
	if len(params) == 0 {
		return paramErr(policy, "", "params are required for this policy")
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return paramErr(policy, "", fmt.Sprintf("params do not match the policy schema: %v", err))
	}
	return nil
}

func copyMessages(in []Message) []Message {
	out := make([]Message, len(in))
	copy(out, in)
	return out
}

// estimateTokens is a deterministic, host-side token estimate: it never calls a tokenizer service, so
// two runs agree exactly. ~4 chars/token is the standard rough ratio; the point is stability across
// runs and monotonicity in length, not provider-exact counts.
func estimateTokens(msgs []Message) int {
	chars := 0
	for _, m := range msgs {
		chars += len([]rune(m.Role)) + len([]rune(m.Content)) + 1 // +1 role/content separator
	}
	return (chars + 3) / 4
}

// dropRatio is the fraction of source tokens a lossy policy removed, clamped to [0,1].
func dropRatio(source, assembled int) float64 {
	if source <= 0 || assembled >= source {
		return 0
	}
	return float64(source-assembled) / float64(source)
}

// latestUserQuery is the retrieval query: the most recent user message, else the last message.
func latestUserQuery(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	if len(msgs) > 0 {
		return msgs[len(msgs)-1].Content
	}
	return ""
}
