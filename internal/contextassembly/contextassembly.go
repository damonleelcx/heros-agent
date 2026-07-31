// Package contextassembly is the trusted host's context-assembly step (P16 task 5.2).
//
// P3 shipped the policies and their host-side `Assemble`; P16 ships the call-site materialization that
// makes a policy reach a run. What sat between them, unwritten, is this: the seam that runs a resolved
// policy for one node of one run AND records what it dropped.
//
// # Why the emit lives in the seam rather than at each call site
//
// FR7 says a materialized lossy policy's observed drop is recorded per node per run. Written as a rule
// ("remember to emit after assembling"), that guarantee holds until the first caller forgets, and its
// failure mode is silent: the assembly still works, the run still scores, and only the drop signal —
// the thing the drop-tolerance gate and the context-overflow diagnosis both read — is missing. So it is
// not a rule here. `Assemble` assembles and emits, and there is no exported way to do the first without
// the second. A caller that wanted to skip the record would have to call `registry.Policy.Assemble`
// directly, which is a visible thing to do in review rather than an omission nobody notices.
//
// # No new metric family
//
// Everything published here is the family telemetry already defines: `context_assembled_tokens`,
// `context_source_messages`, `context_drop_ratio`, `context_retrieved_chunks`. P16 adds no metric name
// (NFR6) — the loss is read from `context_drop_ratio` and the reduction from `eval_tokens_total`, both
// of which existed before this phase.
//
// # The credential never moves
//
// A policy that needs a summarizer or a retriever reaches it through `registry.HostServices`, which is
// wired HERE, on the trusted host. The policy never holds a secret and the sandbox never sees one
// (NFR8).
package contextassembly

import (
	"context"
	"fmt"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// Runner assembles context for a node and records the outcome. It holds the two things assembly needs
// and a caller should not have to thread: the host services a policy reaches its model or retriever
// through, and the collector the measurement is published to.
type Runner struct {
	// Host is the trusted-host gateway. Nil is legitimate for a deployment with only LLM-free policies;
	// a host-calling policy then fails closed inside the policy itself rather than assembling nowhere.
	Host registry.HostServices
	// Collector receives the assembly metrics. Nil is a no-op emit (telemetry's own convention), for a
	// caller that has not wired telemetry — never a reason to skip the assembly.
	Collector *telemetry.Collector
}

// Request is one node's context assembly within one run.
type Request struct {
	// Tags is the full P0 tag set — variant, run, node, case, seed, config_hash. It is required, not
	// optional: a drop measurement that cannot be attributed to a node of a run under a configuration is
	// a number with nowhere to go.
	Tags telemetry.P0Tags
	// Entry is the RESOLVED context entry: the policy implementation plus the params config_hash froze.
	Entry *registry.ContextEntry
	// Conversation is the node's input conversation, oldest message first.
	Conversation registry.Conversation
	// Seed is the run's seed. It is the reproducibility half of `config_hash + seed`, and for a
	// host-calling policy it is what makes the issued ResolvedRequest identical across runs.
	Seed int64
}

// Assemble runs the node's resolved context policy on the trusted host and records the outcome.
//
// The returned AssembledContext is the messages the node will actually run on. The recorded events are
// the same ones any other assembly publishes — a lossy policy's `context_drop_ratio` among them, which
// is what makes "compaction dropped the answer" a measurable defect rather than a hypothesis.
func (r Runner) Assemble(ctx context.Context, req Request) (registry.AssembledContext, error) {
	if req.Entry == nil {
		return registry.AssembledContext{}, fmt.Errorf(
			"contextassembly: no resolved context entry for node %q; a node cannot assemble under a policy "+
				"nobody resolved", req.Tags.NodeID)
	}
	if req.Entry.Policy == nil {
		// A context entry whose implementation is unbound would otherwise assemble nothing and look like a
		// node with no context. Fail closed: resolution binds the implementation, so an unbound one means
		// the entry never went through ResolveContextPolicy.
		return registry.AssembledContext{}, fmt.Errorf(
			"contextassembly: context entry %s names policy %q with no bound implementation",
			req.Entry.VersionID, req.Entry.Spec.Policy)
	}

	got, err := req.Entry.Policy.Assemble(ctx, r.Host, req.Conversation, req.Entry.Spec.Params, req.Seed)
	if err != nil {
		// 🔴 Nothing is recorded for a failed assembly. A partial measurement of an assembly that did not
		// happen is worse than none: it would put a drop ratio on the board for a run that never assembled.
		return registry.AssembledContext{}, fmt.Errorf("contextassembly: node %q policy %q: %w",
			req.Tags.NodeID, req.Entry.Spec.Policy, err)
	}

	Record(r.Collector, req.Tags, req.Entry.Spec.Policy, got)
	return got, nil
}

// Record publishes one assembly outcome through the existing context-assembly telemetry.
//
// It is exported so a caller that already assembled through another path (a replay, a backfill) can
// still record the measurement — but note that `Assemble` above always calls it, so the ordinary path
// cannot forget. The translation is deliberately total: every field the policy measured maps to a field
// telemetry already publishes, and P16 introduces no new one.
//
// 🔴 `Lossy` is carried across rather than inferred from `DropRatio > 0`. The two are different facts: a
// lossless policy's 0.0 means "this policy cannot drop", while a lossy policy's 0.0 means "this run
// measured no drop". Collapsing them would publish a drop-ratio event for a policy that never measures
// one, and would hide the case a reader most needs — a lossy policy that happened to drop nothing today.
func Record(c *telemetry.Collector, tags telemetry.P0Tags, policy string, a registry.AssembledContext) {
	telemetry.EmitContextAssembly(c, tags, telemetry.ContextAssembly{
		Policy:          policy,
		AssembledTokens: a.AssembledTokens,
		SourceMessages:  a.SourceMessageCount,
		Lossy:           a.Lossy,
		DropRatio:       a.DropRatio,
		RetrievedChunks: a.RetrievedChunks,
	})
}

// ObservedDrop is what the drop-tolerance gate and the scorecard read back: a node's measured drop
// under a named policy, and whether that policy measures drop at all.
//
// It exists so "0.0" is never ambiguous at the consumer. `Measured` is false for a lossless policy,
// whose zero is a property of the policy rather than an observation of the run.
type ObservedDrop struct {
	NodeID   string  `json:"node_id"`
	Policy   string  `json:"policy"`
	Ratio    float64 `json:"ratio"`
	Measured bool    `json:"measured"`
}

// Observe projects an assembly outcome into the signal shape scoring and diagnosis consume.
func Observe(nodeID, policy string, a registry.AssembledContext) ObservedDrop {
	return ObservedDrop{NodeID: nodeID, Policy: policy, Ratio: a.DropRatio, Measured: a.Lossy}
}
