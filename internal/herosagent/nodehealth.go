package herosagent

import (
	"sort"
	"sync"
)

// nodehealth.go is P36 task 8.1: per-node inference counts, latency, spend and failure rates, on a
// readable health endpoint.
//
// # 🔴 Why per node, and why an aggregate is not an answer
//
// "An aggregate over a graph says the agent is slow, not which node is." That is the task's own
// sentence and it is the whole design. A five-node definition whose p99 doubled did so at one node, and
// every remediation — a cheaper model, a tighter loop, a removed node — is a decision about which one.
//
// # 🔴 Why in-process counters rather than a query over the stored per-node record
//
// `heros_inference.nodes_json` holds the DURABLE attribution, and it is the right place for it: it
// answers "which node produced this edge" months later. It is the wrong source for a health endpoint.
//
// A health read that runs `jsonb_array_elements` over the inference table is a real-time query against
// the events table, which is exactly what a CQRS split exists to prevent — and the specific failure is
// nasty: the endpoint goes slow when the database goes slow, so the signal degrades at the moment it is
// most needed, and a monitor times out on the one call that would have told somebody why.
//
// So the counters are held here, updated as a run completes, and read in constant time. They are lost
// on restart, and that is stated rather than hidden: `NodeHealth` reports `SinceMS` so a reader can
// tell a quiet node from a freshly restarted process.

// NodeCounters is one node's running totals.
//
// 🔴 SUMS, not means. A sum is associative, so two processes' counters can be added; a running mean
// cannot be, and a fleet view built from means would be a mean of means — which is not the mean, and is
// wrong by more the more uneven the traffic is.
type NodeCounters struct {
	Inferences     int64
	ProviderCalls  int64
	TokensIn       int64
	TokensOut      int64
	Failures       int64
	Skips          int64
	LatencyTotalMS int64
}

// NodeHealthRegistry accumulates per-node observations.
//
// A concrete type rather than an interface at this layer: there is one implementation, and a seam here
// would be a seam a deployment could leave nil while every surface still reported that per-node health
// was being collected.
type NodeHealthRegistry struct {
	mu sync.RWMutex
	m  map[string]*NodeCounters
	// sinceMS is when this registry started counting. 🔴 Reported alongside the numbers, because
	// "this node has done nothing" and "this process restarted a minute ago" are the same zeros.
	sinceMS int64
}

// NewNodeHealthRegistry returns an empty registry, stamped with the time it started counting.
func NewNodeHealthRegistry(nowMS int64) *NodeHealthRegistry {
	return &NodeHealthRegistry{m: map[string]*NodeCounters{}, sinceMS: nowMS}
}

// Observe records one node's run. It is the function `WithNodeHealth` hands the runner.
//
// 🔴 A SKIPPED node is counted as a skip and NOT as an inference, and a FAILED one is counted as a
// failure and not as a success. Folding either into `Inferences` would make a definition whose
// conditional edge never fires look identical to one whose nodes all run — and the second is the
// configuration somebody is paying for.
func (r *NodeHealthRegistry) Observe(run NodeRun) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.m[run.NodeID]
	if c == nil {
		c = &NodeCounters{}
		r.m[run.NodeID] = c
	}
	switch {
	case run.Skipped:
		c.Skips++
		// 🚫 No latency, no tokens, no provider call. A skipped node cost nothing, and adding a zero to
		// the latency sum would drag the mean down — reporting a node as fast because it did not run.
		return
	case run.Failed:
		c.Failures++
	default:
		c.Inferences++
	}
	c.ProviderCalls += int64(run.ProviderCalls)
	c.TokensIn += int64(run.TokensIn)
	c.TokensOut += int64(run.TokensOut)
	c.LatencyTotalMS += run.LatencyMS
}

// NodeHealth returns one node's counters. A node nobody has observed returns the zero value, which the
// caller renders as "not yet run" rather than as a measurement.
func (r *NodeHealthRegistry) NodeHealth(nodeID string) NodeCounters {
	if r == nil {
		return NodeCounters{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if c := r.m[nodeID]; c != nil {
		return *c
	}
	return NodeCounters{}
}

// NodeHealthEntry is one node's counters as a health document carries them.
type NodeHealthEntry struct {
	NodeID string `json:"node_id"`
	NodeCounters
	// LatencyMeanMS is derived at read, from the sum and the call count. Absent as a MEAN when nothing
	// ran — zero here would read as instantaneous.
	LatencyMeanMS int64 `json:"latency_mean_ms"`
	// FailureRate is failures over attempts (failures + inferences). 🔴 Attempts, not calls: a node
	// that made three provider calls in one loop and failed once failed ONE of its runs, not one third
	// of its calls, and the rate an operator acts on is the first number.
	FailureRate float64 `json:"failure_rate"`
}

// NodeHealthDocument is what the health endpoint serves (task 8.1).
type NodeHealthDocument struct {
	// SinceMS is when this process started counting. 🔴 Required reading beside every zero: a node with
	// no inferences and a `since` of a minute ago is a restarted process, not an idle node.
	SinceMS int64             `json:"since_ms"`
	Nodes   []NodeHealthEntry `json:"nodes"`
}

// Document renders the registry, sorted by node id so two reads of an unchanged registry are
// byte-identical.
func (r *NodeHealthRegistry) Document() NodeHealthDocument {
	out := NodeHealthDocument{Nodes: []NodeHealthEntry{}}
	if r == nil {
		return out
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out.SinceMS = r.sinceMS
	ids := make([]string, 0, len(r.m))
	for id := range r.m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := *r.m[id]
		e := NodeHealthEntry{NodeID: id, NodeCounters: c}
		if c.ProviderCalls > 0 {
			e.LatencyMeanMS = c.LatencyTotalMS / c.ProviderCalls
		}
		if attempts := c.Inferences + c.Failures; attempts > 0 {
			e.FailureRate = float64(c.Failures) / float64(attempts)
		}
		out.Nodes = append(out.Nodes, e)
	}
	return out
}
