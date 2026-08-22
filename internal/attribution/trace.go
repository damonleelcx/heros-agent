package attribution

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// trace.go holds the small, deterministic trace-reading primitives the attribution and diagnosis
// engines share: execution order, contract-violation detection against the node's IR contract, and
// per-node span lookups. They read the trace and nothing else — no store, no gateway.

// executionOrder returns the distinct node ids of a trace in execution (start-time) order. Ties on
// start time break by node id so the order is total and deterministic: the same trace must yield the
// same order every run, or first-divergence would move under nothing but map iteration.
//
// 🔴 Start time is only evidence of SEQUENCE when the spans do not overlap. Under P34 concurrency two
// nodes run at once and their start times differ by whatever the scheduler chose — see
// executionOrderDeclared and spansOverlap, and p34_overlap_holdout_test.go, which measured exactly that
// failure before it was fixed.
func executionOrder(tr evalharness.Trace) []string {
	type ne struct {
		id    string
		start int64
	}
	first := map[string]int64{}
	for _, sp := range tr.NodeSpans() {
		id := attrString(sp.Attributes, telemetry.AttrNodeID)
		if id == "" {
			id = sp.Name
		}
		s := sp.StartTime.UnixNano()
		if prev, ok := first[id]; !ok || s < prev {
			first[id] = s
		}
	}
	nes := make([]ne, 0, len(first))
	for id, s := range first {
		nes = append(nes, ne{id, s})
	}
	sort.Slice(nes, func(i, j int) bool {
		if nes[i].start != nes[j].start {
			return nes[i].start < nes[j].start
		}
		return nes[i].id < nes[j].id
	})
	out := make([]string, 0, len(nes))
	for _, n := range nes {
		out = append(out, n.id)
	}
	return out
}

// contractViolated reports whether a node's OUTPUT diverges from its declared contract on this trace:
// the node span errored or is flagged failed, OR the node's output payload fails the node's IR output
// schema. The schema check is what makes "node 3 drops its output contract" localize to node 3 even
// though node 3's span status is OK and a downstream node is the one that fails to parse it.
func contractViolated(ir *discovery.IR, tr evalharness.Trace, nodeID string) bool {
	for _, sp := range tr.NodeSpans() {
		if attrString(sp.Attributes, telemetry.AttrNodeID) != nodeID {
			continue
		}
		if sp.Status == telemetry.SpanStatusError {
			return true
		}
		if f, ok := sp.Attributes[telemetry.AttrNodeFailed].(bool); ok && f {
			return true
		}
	}
	sch := nodeOutputSchema(ir, nodeID)
	if sch == nil {
		return false
	}
	out, ok := tr.NodeOutputs[nodeID]
	if !ok || len(out) == 0 {
		// No output where a contract is declared is itself a contract violation: the node was expected
		// to produce a schema-valid payload and produced nothing.
		return true
	}
	return !schemaValid(sch, out)
}

// nodeOutputSchema returns a node's declared output schema as raw JSON, or nil if the node declares
// none or the IR is absent.
func nodeOutputSchema(ir *discovery.IR, nodeID string) json.RawMessage {
	if ir == nil {
		return nil
	}
	for _, n := range ir.Nodes {
		if n.NodeID != nodeID {
			continue
		}
		if len(n.IOContract.OutputSchema) == 0 {
			return nil
		}
		b, err := json.Marshal(n.IOContract.OutputSchema)
		if err != nil {
			return nil
		}
		return b
	}
	return nil
}

// schemaValid reuses the harness's self-contained schema compiler (remote $ref refused) so the
// contract check here agrees exactly with the JSON-schema evaluator P4 scores with — a second schema
// implementation would eventually disagree with the first.
func schemaValid(schema, payload json.RawMessage) bool {
	sch, err := evalharness.CompileSchema(schema)
	if err != nil {
		// A broken schema is the IR's defect, not the variant's; treat it as "no contract to violate"
		// rather than blaming the node.
		return true
	}
	var doc any
	if err := json.Unmarshal(payload, &doc); err != nil {
		return false
	}
	return sch.Validate(doc) == nil
}

// jsonEqual compares two JSON payloads for semantic equality (key order / whitespace insensitive).
func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b))
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	na, _ := json.Marshal(av)
	nb, _ := json.Marshal(bv)
	return bytes.Equal(na, nb)
}

// attrString reads a string span attribute, empty if absent or not a string.
func attrString(attrs map[string]any, key string) string {
	if attrs == nil {
		return ""
	}
	if s, ok := attrs[key].(string); ok {
		return s
	}
	return ""
}

// attrFloat reads a numeric span attribute as float64, 0 if absent or non-numeric.
func attrFloat(attrs map[string]any, key string) float64 {
	if attrs == nil {
		return 0
	}
	switch v := attrs[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

// ORDERING UNDER CONCURRENCY (P34 task 6.4)
// ─────────────────────────────────────────
//
// # What the holdout found
//
// `executionOrder` above orders nodes by span start time. `firstDivergenceOrdered` then returns the
// FIRST node in that walk whose output violates its contract. When two nodes run concurrently and BOTH
// diverge, "first" becomes a statement about which goroutine the scheduler started first — so the same
// two contract violations, the same durations and the same outputs localized to different nodes
// depending on a nanosecond. `TestBothNodesDivergingIsWhereOrderActuallyDecides` measured it: alpha when
// alpha's span started first, beta when beta's did.
//
// 🔴 A one-guilty-node holdout cannot see this, and the first version of that file did not. With a
// single violating node the walk finds it in any order, reports 100% under overlap, and proves nothing.
// The shape where order decides is the shape that had to be built.
//
// # The fix, and why it is the DECLARED order
//
// P34 design D4 keeps `Order` as a linear sequence containing every node precisely so that "a replay
// visits nodes in that sequence even when the live run overlapped them". That sequence is the answer:
// it is authored, it is part of `config_hash`, and it does not move between runs. Ordering overlapping
// nodes by it makes attribution REPLAY-CONSISTENT — the localization a reader sees is the one a replay
// would produce — which is the property the whole axis promises.
//
// 🚫 It is NOT "pick the alphabetically-first node". That is deterministic and arbitrary, which would
// convert an unstable answer into a stably wrong one. Determinism is not the goal; agreeing with the
// declared sequence is.

// spansOverlap reports whether any two NODE spans in the trace overlap in wall-clock time.
//
// It is the trigger rather than "did the spec declare a group", because the two can disagree: a run may
// overlap spans the spec never declared concurrent (a framework doing its own thing), and a spec may
// declare a group whose members happened not to overlap on this run. What matters for reading the trace
// is what the trace DID.
func spansOverlap(tr evalharness.Trace) bool {
	type iv struct{ start, end int64 }
	byNode := map[string]iv{}
	for _, sp := range tr.NodeSpans() {
		id := attrString(sp.Attributes, telemetry.AttrNodeID)
		if id == "" {
			id = sp.Name
		}
		s, e := sp.StartTime.UnixNano(), sp.EndTime.UnixNano()
		if cur, ok := byNode[id]; ok {
			if s < cur.start {
				cur.start = s
			}
			if e > cur.end {
				cur.end = e
			}
			byNode[id] = cur
			continue
		}
		byNode[id] = iv{s, e}
	}
	ivs := make([]iv, 0, len(byNode))
	for _, v := range byNode {
		ivs = append(ivs, v)
	}
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].start < ivs[j].start })
	for i := 1; i < len(ivs); i++ {
		// Strictly after: two spans that merely touch at a boundary are sequential, not concurrent.
		if ivs[i].start < ivs[i-1].end {
			return true
		}
	}
	return false
}

// executionOrderDeclared returns the trace's executed nodes ordered by the spec's DECLARED order, with
// start-time order for any executed node the declaration does not list.
//
// A node the declaration does not contain is appended in start-time order rather than dropped: a trace
// may legitimately contain a node the spec never named (a framework's own step), and dropping it would
// remove a candidate for first-divergence — silently moving the localization to the next node along.
func executionOrderDeclared(tr evalharness.Trace, declared []string) []string {
	byStart := executionOrder(tr)
	if len(declared) == 0 {
		return byStart
	}
	executed := map[string]bool{}
	for _, id := range byStart {
		executed[id] = true
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(byStart))
	for _, id := range declared {
		if executed[id] && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range byStart {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
