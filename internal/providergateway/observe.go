package providergateway

import (
	"context"
	"time"

	"github.com/heros-foreal/agentd/internal/registry"
)

// Observer is the seam P2.5's OpenTelemetry instrumentation attaches to (task 6.4: "structure
// run/node/transform records so P2.5 OTel instrumentation attaches at the gateway and the
// transform/build/run path with ZERO application change").
//
// "Zero application change" is the whole requirement, and it is a claim about THIS package, not about
// P2.5. It holds only if there is somewhere to attach that is not an edit here. So: an interface, and
// a hook that is already called on every path Complete can take.
//
// Why an Observer rather than importing OTel now: P2.5 has not picked its span store or its TSDB yet
// (storage-decision-record §1, OQ1). Taking the dependency today would bake a choice this phase has
// no standing to make, and P2 would be carrying an SDK it never calls. An interface costs one
// indirection and leaves the decision where it belongs.
type Observer interface {
	// OnCall fires once per completed Complete, success or failure, AFTER all retries. Exactly once
	// per logical call, because that is what a span is — CallInfo.Attempts carries the retries.
	OnCall(ctx context.Context, info CallInfo)
}

// CallInfo is everything an instrument needs about one provider call.
//
// The fields are chosen against P0's seven-tag contract (metric-event.schema.json:
// {variant_id, run_id, node_id, case_id, seed, timestamp, config_hash}) so P2.5 can emit a
// conformant event without reaching back into the gateway for anything. The tags the GATEWAY cannot
// know — variant_id, run_id, node_id, case_id, config_hash — arrive on IdempotencyKey's coordinates
// and via the context the caller passes; the gateway supplies what only it knows: which provider
// answered, how much it cost in tokens, how long it took, and how many attempts it really took.
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
	// RateLimited reports whether ANY attempt in this logical call received a 429 (task 1.5's
	// rate-limit-hit metric). A retried-then-succeeded call still saw the rate limit, so this is
	// separate from Err: the outage signal must survive the recovery.
	RateLimited bool
	// Err is the terminal error, nil on success. It is already scrubbed — an observer that logs this
	// cannot leak a credential, which matters because logging it is exactly what an observer is for.
	Err error
}

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
