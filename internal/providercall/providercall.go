// Package providercall is the vocabulary for DESCRIBING a provider call that has already happened:
// the observer seam, the per-call record it carries, and the normalized token/stop accounting inside it.
//
// # Why this is its own package
//
// It was extracted from `internal/providergateway` because of one edge with a long shadow.
// `internal/telemetry` implements the observer, and to name the value it is handed it imported
// providergateway — which links an HTTP client and the AWS SDK. That import made `net/http` reachable
// from every package that could reach telemetry, and telemetry is deep in the graph:
//
//	internal/cli → authoring/authoringwire → proposal → attribution/diagnosis
//	             → evalharness/linkage → telemetry → providergateway → net/http
//
// `internal/cli` is the CLI's offline command surface — `discover`, `apply`, `eval`, `doctor`, `init` —
// and its whole guarantee is that it CANNOT reach the network because it does not link a network stack
// in. That guarantee is structural or it is nothing: a promise maintained by everyone remembering is
// the kind that was already broken here for three phases before anyone checked.
//
// Nothing in this package needs a network. A metrics package should not have to link an AWS SDK in
// order to name a struct describing a call somebody else made, and now it does not.
//
// # Why the gateway's API did not change
//
// `providergateway` re-exports every name below as a type alias, so `providergateway.CallInfo`,
// `providergateway.Usage`, `providergateway.StopEndTurn` and `providergateway.Observer` all still work
// and still mean exactly the same types. Aliases are identity, not conversion: an `Instrument` written
// against `providercall.Observer` satisfies `providergateway.Observer` because they are one interface.
// The extraction is therefore invisible to every existing call site, which is what makes it safe to do
// to a package this many things depend on.
//
// # What belongs here, and what does not
//
// Here: the description of a call that is over. Not here: anything that MAKES one. The request types,
// the adapters, the retry loop, the credential handling and the transport all stay in
// providergateway, because they are what the network dependency is actually for.
package providercall

import (
	"context"
	"errors"
	"time"
)

// Observer is the seam P2.5's instrumentation attaches to (P2 task 6.4: "structure run/node/transform
// records so P2.5 OTel instrumentation attaches at the gateway and the transform/build/run path with
// ZERO application change").
//
// "Zero application change" is a claim about the GATEWAY, not about P2.5, and it holds only if there is
// somewhere to attach that is not an edit to the gateway. So: an interface, and a hook the gateway
// already calls on every path a completion can take.
//
// Why an interface rather than importing OTel directly: P2.5 has not picked its span store or its TSDB
// (storage-decision-record §1, OQ1). Taking that dependency would bake a choice this phase has no
// standing to make, and the gateway would carry an SDK it never calls. An interface costs one
// indirection and leaves the decision where it belongs.
type Observer interface {
	// OnCall fires once per completed call, success or failure, AFTER all retries — exactly once per
	// LOGICAL call, because that is what a span is. CallInfo.Attempts carries the retries.
	OnCall(ctx context.Context, info CallInfo)
}

// CallInfo is everything an instrument needs about one provider call.
//
// The fields are chosen against P0's seven-tag contract (metric-event.schema.json:
// {variant_id, run_id, node_id, case_id, seed, timestamp, config_hash}) so an observer can emit a
// conformant event without reaching back into the gateway for anything. The tags the GATEWAY cannot
// know — variant_id, run_id, node_id, case_id, config_hash — arrive on IdempotencyKey's coordinates and
// through the context the caller passes; the gateway supplies what only it knows: which provider
// answered, what it cost in tokens, how long it took, and how many attempts it really took.
type CallInfo struct {
	Provider string
	ModelID  string
	// ModelVersionID is the registry entry's content address, so an event can be joined back to the
	// exact model version without re-resolving it.
	ModelVersionID string
	IdempotencyKey string
	Seed           *int64
	Attempts       int
	Duration       time.Duration
	Usage          Usage
	StopReason     StopReason
	// RateLimited reports whether ANY attempt in this logical call received a 429 (P2 task 1.5's
	// rate-limit-hit metric). A retried-then-succeeded call still saw the rate limit, so this is
	// separate from Err: the outage signal must survive the recovery.
	RateLimited bool
	// Err is the terminal error, nil on success. It is already scrubbed — an observer that logs this
	// cannot leak a credential, which matters because logging it is exactly what an observer is for.
	Err error
}

// StopReason is a normalized stop reason. Providers spell these differently ("stop"/"end_turn",
// "length"/"max_tokens", "tool_calls"/"tool_use"); everything downstream branches on one vocabulary.
type StopReason string

const (
	StopEndTurn       StopReason = "end_turn"
	StopMaxTokens     StopReason = "max_tokens"
	StopToolUse       StopReason = "tool_use"
	StopContentFilter StopReason = "content_filter"
	StopOther         StopReason = "other"
)

// Usage is normalized token accounting. Cost metrics attach to this; the field names follow the GenAI
// semantic conventions' shape so that instrumentation is a read rather than a translation.
type Usage struct {
	InputTokens      int
	OutputTokens     int
	ThinkingTokens   int
	CacheReadTokens  int
	CacheWriteTokens int
}

// ErrTimeout: the per-call deadline expired.
//
// It lives here rather than beside the gateway's other sentinels because an OBSERVER branches on it —
// `errors.Is(info.Err, ErrTimeout)` is how a timeout is told apart from a provider rejection when
// classifying a call — and that made it part of the observation vocabulary rather than part of the
// transport. The gateway re-exports it, so `providergateway.ErrTimeout` is the same error value.
var ErrTimeout = errors.New("providergateway: call timed out")
