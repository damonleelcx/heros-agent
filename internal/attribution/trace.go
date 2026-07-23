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
