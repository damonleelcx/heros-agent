package providergateway

import (
	"context"
	"time"

	"github.com/heros-foreal/agentd/internal/providercall"
	"github.com/heros-foreal/agentd/internal/registry"
)

// The observation vocabulary — Observer, CallInfo, Usage, StopReason and ErrTimeout — now lives in
// `internal/providercall`, and this package re-exports it. Nothing about the contract changed: an alias is
// type IDENTITY, so `providergateway.CallInfo` and `providercall.CallInfo` are one type, and an observer
// written against either satisfies both.
//
// # Why it moved
//
// `internal/telemetry` implements Observer, and it imported THIS package purely to name the value it is
// handed. That import made `net/http` — which this package links, for the adapters and the AWS SDK —
// reachable from everything that could reach telemetry, including `internal/cli`, whose entire guarantee
// is that it cannot reach the network because it does not link a network stack in. One import for one
// struct name cost a structural guarantee five levels up the graph.
//
// Splitting the vocabulary out costs nothing: describing a call that is over needs no transport. What
// stays here is everything that MAKES a call — the request types, the adapters, the retry loop, the
// credentials — because that is what the network dependency is for.

// Observer is the seam instrumentation attaches to. See providercall.Observer.
type Observer = providercall.Observer

// CallInfo is everything an instrument needs about one provider call. See providercall.CallInfo.
type CallInfo = providercall.CallInfo

// WithObserver attaches an instrument. Nil-safe: the zero Gateway has no observer and calls no hook.
func WithObserver(o Observer) Option { return func(g *Gateway) { g.observer = o } }

// observe fires the hook if one is attached.
//
// attempts and rateLimited are passed explicitly rather than read from resp, because resp is nil on
// every failure path — and a call that failed after 3 retries reporting Attempts=0 would understate
// exactly the reliability signal (retry count, rate-limit exposure) task 1.5 needs most when things go
// wrong. The gateway tracks both across the retry loop and hands them here so a failure carries the
// same fidelity as a success.
func (g *Gateway) observe(ctx context.Context, entry *registry.ModelEntry, req Request, seed *int64, started time.Time, resp *Response, err error, attempts int, rateLimited bool) {
	if g.observer == nil {
		return
	}
	info := CallInfo{
		Provider: entry.Spec.Provider, ModelID: entry.Spec.ModelID, ModelVersionID: entry.VersionID,
		IdempotencyKey: req.IdempotencyKey, Seed: seed,
		Attempts: attempts, RateLimited: rateLimited,
		Duration: time.Since(started), Err: err,
	}
	if resp != nil {
		info.Usage, info.StopReason = resp.Usage, resp.StopReason
	}
	g.observer.OnCall(ctx, info)
}
