package executor

import (
	"context"

	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

// This file is the wiring between the derived idempotency key and the gateway that must honour it
// (task 5.1's second clause: "gateway de-dupes so a retried call is not billed twice").
//
// Without it, IdempotencyKey and providergateway.Request.IdempotencyKey are two correct halves that
// never meet: the executor derives a key nobody sends, and the gateway faithfully sends a field
// nobody sets. Each half's tests pass. The charge is still double.

// Gateway is the provider gateway a node invocation calls through. An interface so this package does
// not force a concrete gateway on its callers, and so the de-duplication contract can be tested
// against a stub that counts charges.
type Gateway interface {
	Complete(ctx context.Context, entry *registry.ModelEntry, req providergateway.Request, seed *int64) (*providergateway.Response, error)
}

// NodeInvocation identifies one logical provider call within a run — the unit PRD OQ1 pins the
// idempotency key to.
type NodeInvocation struct {
	RunID  string
	NodeID string
	// AttemptGroup distinguishes a RETRY of one logical call (same group — one charge) from a new
	// invocation such as a loop's next iteration (new group — genuinely a second charge).
	AttemptGroup int
	// Seed is the run's seed. Passed per call rather than read from the model entry, because the
	// reproducibility unit is {config_hash, seed} with the RUN supplying it (PRD §7/FR16); the
	// entry's seed is only a default.
	Seed *int64
}

// CallProvider invokes the gateway for one node invocation, stamping the derived idempotency key.
//
// This is the ONLY intended path from a node to a provider, and the stamping is not optional here:
// making the caller remember to set the key is exactly how a retry becomes a second charge. The key
// is derived from the invocation's own coordinates, so two executors racing the same node — an
// at-least-once queue delivering twice (PRD OQ4) — independently produce the SAME key and the
// provider collapses them, even though neither knew about the other.
//
// The gateway's own bounded retries (task 4.4) reuse this one Request, so every attempt carries the
// same key. That is what makes retrying safe rather than expensive.
func CallProvider(ctx context.Context, gw Gateway, entry *registry.ModelEntry, req providergateway.Request, inv NodeInvocation) (*providergateway.Response, error) {
	req.IdempotencyKey = IdempotencyKey(inv.RunID, inv.NodeID, inv.AttemptGroup)
	return gw.Complete(ctx, entry, req, inv.Seed)
}
