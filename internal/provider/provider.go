// Package provider is the boundary to a language model.
//
// # Why there is an interface here at all, given one provider is wired
//
// Not for hypothetical future providers — that is the reason people give, and it is usually wrong. It
// is because the WHOLE SYSTEM below this line was built and proven against a substituted outside world,
// and the seam that made that possible has to survive contact with a real one. A concrete client called
// directly from the tools would delete the seam that every existing test depends on.
//
// # 🔴 Usage is reported by the PROVIDER, never estimated
//
// Ceilings are denominated in tokens and money. A caller that estimates its own spend has a ceiling that
// drifts from reality in whichever direction is least convenient to notice — and the direction it drifts
// is "cheaper than it was", because estimates are built from happy paths.
package provider

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Message is one turn in a conversation.
type Message struct {
	Role    string // "system", "user", "assistant"
	Content string
}

// Request is one completion.
type Request struct {
	Model    string
	Messages []Message
	// MaxTokens bounds the response. Required: a completion with no output bound is an unbounded spend
	// in a single call, and no outer ceiling can interrupt a call already in flight.
	MaxTokens int
	// Temperature is a pointer so "unset" is distinguishable from "deliberately zero". Zero is a real,
	// commonly-wanted value, and a plain float64 makes it indistinguishable from a forgotten field.
	Temperature *float64
	// Reasoning controls chain-of-thought.
	//
	// # 🔴 Why this has no usable zero value
	//
	// The qwen3.8 line enables thinking BY DEFAULT, and so did the DeepSeek provider this rule was first
	// written against. A caller who does not think about this silently buys a chain of thought on every
	// call — billed as output tokens, at the output rate, consuming the output budget before a single
	// character of the answer is written. That is exactly how a structured-extraction call that needs 80
	// tokens of JSON hits a 1,200-token ceiling and reports itself truncated.
	//
	// 🔴 The default differs PER MODEL, not per vendor, which is why this cannot be solved by picking a
	// provider. Older Qwen ids do not think by default; the current ones do. An omitted field therefore
	// means "whatever this particular model prefers", which is the silent spend difference this refuses.
	//
	// It also silently disables `temperature`: the provider documents that temperature, top_p,
	// presence_penalty and frequency_penalty "will not trigger an error but will also have no effect" in
	// thinking mode. So a caller setting temperature 0 for determinism gets neither the determinism nor
	// an error saying why.
	//
	// So the empty value is REFUSED by ValidateRequest. Every call site has to state what it wants, the
	// same way a ceiling has to be stated: a forgotten field must not silently buy the expensive option.
	Reasoning Reasoning
	// JSONObject asks the provider to constrain output to a JSON object. Not a substitute for parsing
	// defensively — it is a hint that improves the odds, not a guarantee that changes them to one.
	JSONObject bool
}

// Reasoning is how much chain-of-thought a call should do.
type Reasoning string

const (
	// NoReasoning disables thinking. The right choice for structured extraction: the model is filling in
	// a fixed shape from text it was given, not solving anything, and reasoning tokens here are pure cost
	// against the output ceiling.
	//
	// It also makes `temperature` effective again, which is what makes a run reproducible.
	NoReasoning Reasoning = "none"
	// LowReasoning, HighReasoning and MaxReasoning enable thinking at increasing effort. Reasoning tokens
	// are billed as OUTPUT and count against MaxTokens, so raising effort narrows the room left for the
	// answer.
	LowReasoning  Reasoning = "low"
	HighReasoning Reasoning = "high"
	MaxReasoning  Reasoning = "max"
)

// Valid reports membership of the closed set.
func (r Reasoning) Valid() bool {
	switch r {
	case NoReasoning, LowReasoning, HighReasoning, MaxReasoning:
		return true
	}
	return false
}

// Usage is what a call actually consumed, as reported by the provider.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
	// CachedInputTokens is the portion of input served from the provider's prompt cache. Carried
	// separately because it is priced differently, and folding it into InputTokens would overstate cost
	// by the cache discount on every cached call.
	CachedInputTokens int64
	// ReasoningTokens is the part of OutputTokens spent on chain-of-thought.
	//
	// 🔴 Reported separately although it is billed identically, because it answers a question the total
	// cannot: "did this call spend its budget thinking or answering?" A truncated response with 900
	// reasoning tokens and 60 of answer needs thinking turned down, not a bigger ceiling — and those two
	// fixes are indistinguishable from the total alone.
	ReasoningTokens int64
}

// Total is every token the call touched.
func (u Usage) Total() int64 { return u.InputTokens + u.OutputTokens }

// Response is one completion's result.
type Response struct {
	Content string
	Usage   Usage
	// CostMicroCents is computed from Usage and the model's price, in MILLIONTHS of a cent.
	//
	// 🔴 Micro-cents, not cents, and this was learned from a real call. A 300-token DeepSeek completion
	// costs 0.0127 cents. Rounding that up to the cent — the correct instinct, since under-reporting is
	// the dangerous direction — overstates it by 79x, and a nine-axis assessment bills 9 cents instead
	// of 0.11. A ceiling denominated in a unit coarser than the thing it measures is not conservative;
	// it is simply wrong, and it trips on runs that had spent almost nothing.
	CostMicroCents int64
	// Model is what actually answered, which is not always what was asked for.
	Model   string
	Latency time.Duration
	// FinishReason says why generation stopped. 🔴 Surfaced rather than swallowed: a response truncated
	// at the token limit is a DIFFERENT thing from a complete one, and a caller that cannot tell them
	// apart will parse a half-written JSON object and report the parse failure as a model error.
	FinishReason string
}

// Truncated reports whether the response was cut short by the output limit.
func (r Response) Truncated() bool { return r.FinishReason == "length" }

// Provider completes a request.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
}

// Delta is one increment of a streamed completion.
//
// Text and Reasoning are carried SEPARATELY rather than concatenated. They are different things to a
// reader — one is the answer, the other is the model working — and a surface that cannot tell them
// apart either shows the chain of thought as if it were the reply, or hides that anything is happening
// at all. They are also billed differently against MaxTokens, which is the other reason Usage splits
// them.
type Delta struct {
	Text      string
	Reasoning string
}

// StreamingProvider is a provider that can also deliver a completion incrementally.
//
// # 🔴 Why this is a SEPARATE, OPTIONAL interface rather than a method on Provider
//
// Provider is implemented by every fake in every test and by the scripted fixtures the whole system was
// proven against. Widening it would force all of them to grow a method they have no use for, to satisfy
// a capability only one caller wants — and the usual result of that is a pile of `panic("unimplemented")`
// bodies, which is a compile-time promise that lies at run time.
//
// So callers type-assert, and a provider that cannot stream stays perfectly legal. The caller must have
// a non-streaming path anyway: streaming is a DISPLAY improvement, and a display improvement that can
// take the answer down with it is not one.
//
// # 🔴 The returned Response is the authority, not the deltas
//
// CompleteStream returns exactly what Complete would: the same content, the same provider-reported
// Usage, the same cost, the same FinishReason. The sink is best-effort decoration — a caller that
// accumulated the deltas itself and trusted that instead would be building its own copy of the answer,
// and the two would drift on exactly the calls that matter (a truncated response, a mid-stream error).
// Deltas are for showing; the Response is for acting on and for the ledger.
type StreamingProvider interface {
	Provider
	CompleteStream(ctx context.Context, req Request, sink func(Delta)) (Response, error)
}

// Error classes. Typed because the retry ladder must treat them differently: retrying an authentication
// failure burns the whole ladder to arrive at the same answer, and retrying a rate limit immediately is
// how a rate limit becomes a longer rate limit.
var (
	// ErrAuth — the credential is wrong or revoked. NEVER retry: it will not become right.
	ErrAuth = errors.New("provider: authentication rejected")
	// ErrRateLimited — back off and try later.
	ErrRateLimited = errors.New("provider: rate limited")
	// ErrUpstream — the provider failed on its own side. Retryable.
	ErrUpstream = errors.New("provider: upstream error")
	// ErrRequest — the request was malformed. Not retryable: the same request will fail the same way.
	ErrRequest = errors.New("provider: request rejected")
	// ErrBudget — the caller's own bound was violated before the call was made.
	ErrBudget = errors.New("provider: request violates its own bounds")
)

// Retryable reports whether an error is worth another attempt.
//
// 🔴 The DEFAULT IS FALSE. An unrecognised error is not retried, because the failure mode of the other
// default is a loop: something unexpected happens, every attempt reproduces it, and the retry ladder
// spends the full budget confirming it. An error worth retrying can be classified when somebody sees it.
func Retryable(err error) bool {
	switch {
	case errors.Is(err, ErrRateLimited), errors.Is(err, ErrUpstream):
		return true
	case errors.Is(err, context.DeadlineExceeded):
		return true
	default:
		return false
	}
}

// Price is a model's cost per million tokens, in cents.
//
// # 🔴 These are STATED VALUES with a date, not measurements
//
// They are read from the provider's published pricing at the date below and are not verified by this
// code against anything. A price change makes every cost figure here quietly wrong in the direction of
// under-reporting, which is the direction nobody investigates. Anything that bills a customer must read
// live pricing rather than this table; this exists so a spend CEILING has a number to work with.
type Price struct {
	InputCentsPerMTok       float64
	CachedInputCentsPerMTok float64
	OutputCentsPerMTok      float64
	// StatedAt is when these were transcribed. Carried in the struct so a reader sees the age of the
	// number rather than having to find a comment.
	StatedAt string
	// Source is where they came from.
	Source string
}

// MicroCentsPerCent is the ledger's resolution: costs accumulate in millionths of a cent.
const MicroCentsPerCent = 1_000_000

// CostMicroCents computes a call's cost in micro-cents, rounded UP.
//
// 🔴 Up, not nearest — a ledger that rounds down lets a long run of cheap calls accumulate real spend
// while the recorded total stays behind, and the gap grows with exactly the workload that matters: many
// small calls. Rounding up at MICRO-cent resolution keeps that safety without the 79x distortion that
// rounding up at cent resolution produced on a real 300-token call.
func (p Price) CostMicroCents(u Usage) int64 {
	uncached := u.InputTokens - u.CachedInputTokens
	if uncached < 0 {
		uncached = 0
	}
	const perM = 1_000_000.0
	cents := float64(uncached)/perM*p.InputCentsPerMTok +
		float64(u.CachedInputTokens)/perM*p.CachedInputCentsPerMTok +
		float64(u.OutputTokens)/perM*p.OutputCentsPerMTok
	if cents <= 0 {
		return 0
	}
	micro := cents * MicroCentsPerCent
	whole := int64(micro)
	if micro > float64(whole) {
		whole++
	}
	return whole
}

// FormatCents renders micro-cents as a human currency string, for a surface that shows spend.
func FormatCents(microCents int64) string {
	return fmt.Sprintf("$%.4f", float64(microCents)/MicroCentsPerCent/100)
}

// ValidateRequest enforces the bounds a request must carry before it is sent.
func ValidateRequest(r Request) error {
	if r.Model == "" {
		return fmt.Errorf("%w: no model named", ErrRequest)
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("%w: no messages", ErrRequest)
	}
	if r.MaxTokens <= 0 {
		return fmt.Errorf("%w: MaxTokens is %d — a completion with no output bound is an unbounded "+
			"spend inside a single call, which no outer ceiling can interrupt", ErrBudget, r.MaxTokens)
	}
	if !r.Reasoning.Valid() {
		return fmt.Errorf("%w: Reasoning is %q — the provider enables thinking by default at high "+
			"effort, so leaving this unset silently buys chain-of-thought that is billed as output and "+
			"consumes MaxTokens before the answer starts. State one of: none, low, high, max",
			ErrBudget, r.Reasoning)
	}
	return nil
}
