package forgedelivery

import "context"

// HaltReaderFunc adapts a function to HaltReader, so a caller can wire adminops.KillSwitchService's
// HaltsMerge (a global + per-tenant, fail-closed reader) into delivery without this package importing
// the admin layer — which would couple delivery to the operator console.
type HaltReaderFunc func(tenantID string) (bool, string, error)

// HaltsDelivery implements HaltReader.
func (f HaltReaderFunc) HaltsDelivery(tenantID string) (bool, string, error) { return f(tenantID) }

// Job pairs a proposal with the route and writer it should be delivered through.
type Job struct {
	Proposal Proposal
	Route    *Route
	Forge    ForgeWriter
}

// BatchResult is one job's outcome within a batch.
type BatchResult struct {
	Job    Job
	Result Result
	Err    error
}

// DeliverBatch delivers many proposals with PER-REPOSITORY FAILURE ISOLATION (task 2.9): one
// repository's forge outage — or any per-job error — is contained to that job's BatchResult and blocks
// no other job, and a failed delivery does not lose its proposal (the Job, including the proposal, is
// returned in the result so the caller can retry it). Nothing is shared across jobs that a failure in
// one could corrupt.
//
// It is deliberately sequential and simple: isolation here is about a failure in one job not affecting
// another, which sequential execution already guarantees. Concurrency across repositories is a caller's
// choice and does not change the isolation property, which is why it is not baked in.
func (d *Deliverer) DeliverBatch(ctx context.Context, jobs []Job) []BatchResult {
	out := make([]BatchResult, 0, len(jobs))
	for _, j := range jobs {
		// A context cancellation stops the batch, but already-completed jobs keep their results and the
		// remaining jobs keep their proposals — nothing is lost.
		if err := ctx.Err(); err != nil {
			out = append(out, BatchResult{Job: j, Err: err})
			continue
		}
		res, err := d.Deliver(ctx, j.Proposal, j.Route, j.Forge)
		out = append(out, BatchResult{Job: j, Result: res, Err: err})
	}
	return out
}
