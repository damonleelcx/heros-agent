package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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
// SelectionPolicy — the policies P16 can materialize into source
// ─────────────────────────────────────────────────────────────────────────────

// SelectionPolicy is a context policy whose assembly is a SUBSET of the input messages, chosen without
// reading, rewriting, or synthesizing any message's content (P16 task 2.2).
//
// # Why this interface exists, and why it lives HERE rather than in the transform
//
// P16 materializes a context policy at a Go call site by DELETING the messages the policy does not
// retain from the static list the author wrote. That is only sound if the codemod's idea of "which
// messages survive" is the same one the host-side `Assemble` uses at run time. Two implementations of
// that rule would be two answers to one question, and the failure mode is the worst this platform has:
// a diff that windows differently than the policy the `config_hash` names, scored as that policy.
//
// So the rule is written ONCE, here, and `Assemble` is a caller of it exactly as the codemod is. A
// policy that cannot express its assembly as a retained subset — because it summarizes, retrieves, or
// re-encodes — does not implement this interface, and the transform's refusal for it is then a fact
// about the type rather than a list someone has to remember to update.
type SelectionPolicy interface {
	Policy
	// Retain returns the indexes of msgs the policy keeps, ascending, preserving source order. It never
	// reads a message's content except to measure its size, and never returns an index twice.
	Retain(params json.RawMessage, msgs []Message) ([]int, error)
}

// retainedTail is the shared shape of every selection policy implemented so far: keep the most recent
// `keep` messages. Recency wins because a conversation's newest turns are the ones the model is
// answering; dropping from the front is what both policies below already did.
func retainedTail(total, keep int) []int {
	if keep > total {
		keep = total
	}
	out := make([]int, 0, keep)
	for i := total - keep; i < total; i++ {
		out = append(out, i)
	}
	return out
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

// Retain keeps the most recent window_size messages. It is the SINGLE definition of what this policy
// windows to: Assemble below and P16's Go call-site materializer both read it, so a materialized
// window and a runtime-assembled one cannot disagree.
func (SlidingWindowPolicy) Retain(params json.RawMessage, msgs []Message) ([]int, error) {
	var p slidingWindowParams
	if err := strictUnmarshal("sliding-window", params, &p); err != nil {
		return nil, err
	}
	if p.WindowSize <= 0 {
		return nil, paramErr("sliding-window", "window_size", "must be > 0")
	}
	return retainedTail(len(msgs), p.WindowSize), nil
}

func (sw SlidingWindowPolicy) Assemble(_ context.Context, _ HostServices, conv Conversation, params json.RawMessage, _ int64) (AssembledContext, error) {
	keep, err := sw.Retain(params, conv.Messages)
	if err != nil {
		return AssembledContext{}, err
	}
	out := selectMessages(conv.Messages, keep)
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

// Retain keeps the most-recent messages whose running total stays within target_tokens. Walk
// newest→oldest so recency wins; a single message larger than the target is kept alone (dropping
// everything would assemble empty context, a worse failure than one over-budget message).
// Deterministic: no map order, no clock, integer arithmetic only.
//
// Like SlidingWindowPolicy.Retain, this is the single definition the host-side assembly and P16's
// call-site materializer share.
func (SemanticCompactionPolicy) Retain(params json.RawMessage, msgs []Message) ([]int, error) {
	var p compactionParams
	if err := strictUnmarshal("semantic-compaction", params, &p); err != nil {
		return nil, err
	}
	if p.TargetTokens <= 0 {
		return nil, paramErr("semantic-compaction", "target_tokens", "must be > 0")
	}
	kept, total := 0, 0
	for i := len(msgs) - 1; i >= 0; i-- {
		t := estimateTokens(msgs[i : i+1])
		if kept > 0 && total+t > p.TargetTokens {
			break
		}
		total += t
		kept++
	}
	return retainedTail(len(msgs), kept), nil
}

func (sc SemanticCompactionPolicy) Assemble(_ context.Context, _ HostServices, conv Conversation, params json.RawMessage, _ int64) (AssembledContext, error) {
	keep, err := sc.Retain(params, conv.Messages)
	if err != nil {
		return AssembledContext{}, err
	}
	sourceTokens := estimateTokens(conv.Messages)
	out := selectMessages(conv.Messages, keep)
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
// hierarchical-summary {summarizer_model_ref, recent_verbatim}   (P16 task 4.2)
// ─────────────────────────────────────────────────────────────────────────────

// HierarchicalSummaryPolicy keeps the most recent `recent_verbatim` turns EXACTLY as written and
// replaces everything older with a single summary produced host-side. It is the two-tier shape of
// conversational memory: recency verbatim, history compressed.
//
// # Why this is a new POLICY and not a new field, a new spec shape, or a new Dimension
//
// It costs a `Name` / `ParamsSchema` / `Assemble` implementation and a row through `Store.AddPolicy`
// (store.go). Nothing else moves: no registry schema, no `ContextSpec` change, no `Dimension` member.
// That is the whole point of the interface P2 landed and P3 filled — a policy validates its own params
// at registration without the registry ever learning its shape (design.md Decision 6, decisions.md D-1).
//
// # Why ONE summarizer call, not one per tier
//
// The determinism contract for a host-calling policy is a single captured `ResolvedRequest` — the
// handle two runs are proved identical at (NFR2). A policy that issued N calls would have N requests
// and one field to report them in, so its determinism claim would be partial by construction. One call
// over the summarized tier keeps the handle complete and honest. A future N-tier variant is a different
// policy with a different name, which is exactly the extensibility this interface buys.
type HierarchicalSummaryPolicy struct{}

func (HierarchicalSummaryPolicy) Name() string { return "hierarchical-summary" }

func (HierarchicalSummaryPolicy) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"summarizer_model_ref":{"type":"string","minLength":1},"recent_verbatim":{"type":"integer","minimum":1}},"required":["summarizer_model_ref","recent_verbatim"],"additionalProperties":false}`)
}

type hierarchicalParams struct {
	SummarizerModelRef string `json:"summarizer_model_ref"`
	RecentVerbatim     int    `json:"recent_verbatim"`
}

func (HierarchicalSummaryPolicy) Assemble(ctx context.Context, host HostServices, conv Conversation, params json.RawMessage, seed int64) (AssembledContext, error) {
	var p hierarchicalParams
	if err := strictUnmarshal("hierarchical-summary", params, &p); err != nil {
		return AssembledContext{}, err
	}
	if p.SummarizerModelRef == "" {
		return AssembledContext{}, paramErr("hierarchical-summary", "summarizer_model_ref", "must not be empty")
	}
	if p.RecentVerbatim <= 0 {
		return AssembledContext{}, paramErr("hierarchical-summary", "recent_verbatim", "must be > 0")
	}
	if host == nil {
		return AssembledContext{}, paramErr("hierarchical-summary", "",
			"requires host services to reach the summarizer model")
	}

	split := len(conv.Messages) - p.RecentVerbatim
	if split <= 0 {
		// The whole conversation fits in the verbatim tier, so there is no history to summarize. Pass it
		// through unchanged rather than call the summarizer over nothing: a summary of an empty history is
		// a model call that costs money and adds no information, and it would make the assembly depend on
		// a provider for a result that is a copy of the input.
		out := copyMessages(conv.Messages)
		return AssembledContext{
			Messages:           out,
			AssembledTokens:    estimateTokens(out),
			SourceMessageCount: len(conv.Messages),
			DropRatio:          0,
			Lossy:              true, // the POLICY is lossy; this run happened to drop nothing (see below)
		}, nil
	}

	older := copyMessages(conv.Messages[:split])
	req := ResolvedRequest{
		Op:       "summarize",
		ModelRef: p.SummarizerModelRef,
		Seed:     seed,
		Messages: older,
	}
	summary, err := host.Summarize(ctx, req)
	if err != nil {
		return AssembledContext{}, fmt.Errorf("hierarchical-summary: summarizer %q failed: %w",
			p.SummarizerModelRef, err)
	}

	out := make([]Message, 0, 1+p.RecentVerbatim)
	out = append(out, Message{Role: "system", Content: summary})
	out = append(out, copyMessages(conv.Messages[split:])...)
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
// structured-extraction {fields}   (P16 task 4.3, PRD §14 Q4)
// ─────────────────────────────────────────────────────────────────────────────

// StructuredExtractionPolicy replaces the conversation with one system message carrying the declared
// fields and their values, harvested from the conversation deterministically: for each declared field,
// the value is the remainder of the line after the LAST `field:` marker in the conversation. LLM-free
// and byte-identical across runs.
//
// # 🔴 It is LOSSY, and that is a DECISION, not a default (PRD §14 Q4)
//
// The tempting reading is that extraction is a representation change, like `full-history` — the same
// information in a tidier shape, and therefore lossless. It is not, and the difference is exactly what
// drop tolerance exists to catch: extraction is a PROJECTION. Everything in the conversation that is
// not one of the declared fields is unrecoverable afterwards, so a downstream node that needed a detail
// outside the schema has lost it. Marking it lossless would mean the drop-tolerance gate never fired on
// the one policy whose whole mechanism is discarding what the schema did not name — the gate would be
// decoration on precisely the case it was built for.
//
// So `Lossy` is true and the drop is MEASURED, not assumed: a run that extracted a conversation which
// was already almost entirely field markers reports a small drop, and a run that threw away pages of
// discussion reports a large one. The gate reads the measurement, never the flag alone.
//
// # Fail-closed on a field the conversation does not carry
//
// A declared field with no value is an error, not an omission. Assembling a context missing a field the
// configuration declared would run the node on inputs it was not configured for and report the result
// under a `config_hash` that claims the field was there.
type StructuredExtractionPolicy struct{}

func (StructuredExtractionPolicy) Name() string { return "structured-extraction" }

func (StructuredExtractionPolicy) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"fields":{"type":"array","minItems":1,"items":{"type":"string","minLength":1}}},"required":["fields"],"additionalProperties":false}`)
}

type extractionParams struct {
	Fields []string `json:"fields"`
}

func (StructuredExtractionPolicy) Assemble(_ context.Context, _ HostServices, conv Conversation, params json.RawMessage, _ int64) (AssembledContext, error) {
	var p extractionParams
	if err := strictUnmarshal("structured-extraction", params, &p); err != nil {
		return AssembledContext{}, err
	}
	if len(p.Fields) == 0 {
		return AssembledContext{}, paramErr("structured-extraction", "fields", "must declare at least one field")
	}

	// Field order follows the DECLARED order, never sorted: the params are part of config_hash, so the
	// author's order is identity-bearing and the assembled bytes must follow it.
	var b strings.Builder
	for i, f := range p.Fields {
		v, ok := lastFieldValue(conv.Messages, f)
		if !ok {
			return AssembledContext{}, paramErr("structured-extraction", f,
				"the conversation carries no value for this declared field; extracting anyway would assemble "+
					"a context missing a field the configuration declares")
		}
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(f)
		b.WriteString(": ")
		b.WriteString(v)
	}

	out := []Message{{Role: "system", Content: b.String()}}
	sourceTokens := estimateTokens(conv.Messages)
	assembled := estimateTokens(out)
	return AssembledContext{
		Messages:           out,
		AssembledTokens:    assembled,
		SourceMessageCount: len(conv.Messages),
		// Measured, not assumed — see the type's doc comment. A projection that happened to keep almost
		// everything reports almost no drop, and the gate reads the number.
		DropRatio: dropRatio(sourceTokens, assembled),
		Lossy:     true,
	}, nil
}

// lastFieldValue finds the value of `field:` in the conversation, taking the LAST occurrence so a later
// correction wins over an earlier statement — the same recency rule every other policy here follows.
func lastFieldValue(msgs []Message, field string) (string, bool) {
	marker := field + ":"
	var found string
	var ok bool
	for _, m := range msgs {
		for _, line := range strings.Split(m.Content, "\n") {
			idx := strings.Index(line, marker)
			if idx < 0 {
				continue
			}
			found = strings.TrimSpace(line[idx+len(marker):])
			ok = true
		}
	}
	return found, ok
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

// selectMessages materializes a Retain result: the retained messages, in source order, copied.
func selectMessages(in []Message, keep []int) []Message {
	out := make([]Message, 0, len(keep))
	for _, i := range keep {
		if i >= 0 && i < len(in) {
			out = append(out, in[i])
		}
	}
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
