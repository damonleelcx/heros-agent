package typedcontract

import (
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// Edge is one graph edge in a candidate ordering. Only data edges carry an I/O contract obligation;
// control edges sequence execution without a data dependency, so ValidateOrdering ignores their
// schemas (but still uses them, and the node Order, to decide producer-before-consumer position).
type Edge struct {
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	Kind       string `json:"kind"` // "data" | "control"
}

// Ordering is a candidate arrangement to validate: the node execution order plus the edges. It is the
// minimal shape ValidateOrdering needs, decoupled from variantspec.VariantSpec so this package (the
// contract layer) does not depend on the config layer.
type Ordering struct {
	Order []string
	Edges []Edge
}

// VerdictKind is the total classification of an ordering (Decision 2 — no "unknown" bucket).
type VerdictKind string

const (
	// VerdictCoherent: every data edge satisfies its contract as-is; no adapter needed.
	VerdictCoherent VerdictKind = "coherent"
	// VerdictAdapted: every data edge is either coherent or bridged by an inserted catalog adapter.
	VerdictAdapted VerdictKind = "adapted"
	// VerdictRejected: at least one data edge is incoherent with no admissible adapter. The ordering is
	// NOT persisted as a runnable Variant Spec, and no source transformation is generated for it.
	VerdictRejected VerdictKind = "rejected"
)

// AdapterInsertion records one adapter the validator would insert on a mismatching-but-adaptable edge.
// It names the edge, so the UI can anchor a preview to it, and carries the typed AdapterMatch.
type AdapterInsertion struct {
	FromNodeID string       `json:"from_node_id"`
	ToNodeID   string       `json:"to_node_id"`
	Adapter    AdapterMatch `json:"adapter"`
}

// DiagnosticReason distinguishes the two ways an edge can be incoherent, because they have different
// user-facing explanations and neither is adapter-fixable.
type DiagnosticReason string

const (
	// ReasonMissingProducer: the consumer is ordered BEFORE the producer of a field it requires — the
	// data does not exist yet at the consumer's execution point. No adapter can conjure absent data.
	ReasonMissingProducer DiagnosticReason = "missing_producer"
	// ReasonUnadaptable: producer and consumer are correctly ordered but their schemas mismatch and no
	// catalog adapter bridges the gap.
	ReasonUnadaptable DiagnosticReason = "unadaptable"
)

// Diagnostic names an incoherent edge specifically enough to act on: the producer, the consumer, and
// the exact mismatching field(s). Attributable to the offending edge, never a whole-graph error
// (FR: "attributable to the specific offending edge").
type Diagnostic struct {
	FromNodeID string           `json:"from_node_id"`
	ToNodeID   string           `json:"to_node_id"`
	Reason     DiagnosticReason `json:"reason"`
	Fields     []string         `json:"fields"`
	Message    string           `json:"message"`
}

// Verdict is the total, pure result of ValidateOrdering: exactly one Kind, plus the adapters (when
// Adapted) or diagnostics (when Rejected). Coherent and Adapted carry no diagnostics; Rejected carries
// no adapters — the three are mutually exclusive.
type Verdict struct {
	Kind        VerdictKind        `json:"kind"`
	Adapters    []AdapterInsertion `json:"adapters,omitempty"`
	Diagnostics []Diagnostic       `json:"diagnostics,omitempty"`
}

// IsCoherent reports whether the ordering is runnable (coherent as-is or via adapters).
func (v Verdict) IsCoherent() bool { return v.Kind == VerdictCoherent || v.Kind == VerdictAdapted }

// ValidateOrdering applies Satisfies to EVERY producer→consumer data edge and returns a total, pure,
// deterministic verdict (tasks 1.3–1.5). It is a pure function of the node schemas and the catalog:
// the same ordering over the same IR yields the same verdict and the same inserted adapters on every
// evaluation. It generates NO source transformation — validation gates codemod generation (ADR-001),
// so a rejected ordering never reaches the transform engine.
//
// Rejection dominates: if any edge is incoherent (bad position or unadaptable schema mismatch) the
// whole ordering is rejected, because a runnable spec must have every edge coherent.
func ValidateOrdering(ir *discovery.IR, ordering Ordering, catalog *Catalog) Verdict {
	if catalog == nil {
		catalog = DefaultCatalog()
	}
	schemas := nodeSchemas(ir)
	position := positionIndex(ordering.Order)

	// Sort edges so iteration order — and therefore the adapter/diagnostic order — is deterministic.
	edges := append([]Edge(nil), ordering.Edges...)
	sort.Slice(edges, func(i, j int) bool { return edgeLess(edges[i], edges[j]) })

	var adapters []AdapterInsertion
	var diags []Diagnostic

	for _, e := range edges {
		if e.Kind != "data" {
			continue // only data edges carry an I/O-contract obligation
		}
		prod, okP := schemas[e.FromNodeID]
		cons, okC := schemas[e.ToNodeID]
		if !okP || !okC {
			// An edge referencing a node the IR/ordering does not contain is malformed. Fail closed:
			// treat it as incoherent rather than skipping it (skipping would admit a broken edge).
			diags = append(diags, Diagnostic{
				FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Reason: ReasonUnadaptable,
				Message: "edge references a node absent from the IR",
			})
			continue
		}

		// Position check first: a consumer placed before its data producer is incoherent regardless of
		// schema — the data has not been produced yet. This is the canonical "consumer before its
		// producer" incoherent reorder, and it is NOT adapter-fixable.
		if pp, okpp := position[e.FromNodeID]; okpp {
			if pc, okpc := position[e.ToNodeID]; okpc && pp >= pc {
				fields := flowingRequiredFields(prod.OutputSchema, cons.InputSchema)
				diags = append(diags, Diagnostic{
					FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Reason: ReasonMissingProducer,
					Fields: fields,
					Message: fmt.Sprintf("consumer %q is ordered before its producer %q, so the field(s) %s it consumes are not available yet",
						e.ToNodeID, e.FromNodeID, quoteJoin(fields)),
				})
				continue
			}
		}

		res := Satisfies(prod.OutputSchema, cons.InputSchema)
		if res.OK {
			continue // edge coherent as-is
		}

		// Mismatching edge: adaptable or incoherent (task 1.4). Never admit it as coherent.
		if a, ok := catalog.FindAdapter(prod.OutputSchema, cons.InputSchema); ok {
			adapters = append(adapters, AdapterInsertion{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Adapter: *a})
			continue
		}
		diags = append(diags, Diagnostic{
			FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Reason: ReasonUnadaptable,
			Fields:  mismatchFields(res.Mismatches),
			Message: fmt.Sprintf("no catalog adapter bridges %q→%q: %s", e.FromNodeID, e.ToNodeID, joinMismatches(res.Mismatches)),
		})
	}

	switch {
	case len(diags) > 0:
		return Verdict{Kind: VerdictRejected, Diagnostics: diags}
	case len(adapters) > 0:
		return Verdict{Kind: VerdictAdapted, Adapters: adapters}
	default:
		return Verdict{Kind: VerdictCoherent}
	}
}

// nodeSchemas indexes each node's io_contract by node_id.
func nodeSchemas(ir *discovery.IR) map[string]discovery.IRIOContract {
	out := map[string]discovery.IRIOContract{}
	if ir == nil {
		return out
	}
	for _, n := range ir.Nodes {
		out[n.NodeID] = n.IOContract
	}
	return out
}

// positionIndex maps node_id → its 0-based slot in the execution order.
func positionIndex(order []string) map[string]int {
	out := make(map[string]int, len(order))
	for i, id := range order {
		out[id] = i
	}
	return out
}

// flowingRequiredFields returns the consumer-required fields the producer declares — the fields that
// flow on this data edge. Used to name the affected fields in a position (missing-producer) diagnostic.
func flowingRequiredFields(producerOut, consumerIn map[string]any) []string {
	prodProps := properties(producerOut)
	var out []string
	for _, f := range requiredFields(consumerIn) {
		if _, ok := prodProps[f]; ok {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		// The consumer requires fields but the producer declares none of them by name (permissive
		// producer). Name all consumer-required fields — they are the ones at risk.
		out = requiredFields(consumerIn)
	}
	return out
}

func mismatchFields(ms []FieldMismatch) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Field)
	}
	sort.Strings(out)
	return out
}

func joinMismatches(ms []FieldMismatch) string {
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		parts = append(parts, describeMismatch(m))
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

func quoteJoin(fs []string) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = fmt.Sprintf("%q", f)
	}
	return strings.Join(parts, ", ")
}

func edgeLess(a, b Edge) bool {
	if a.FromNodeID != b.FromNodeID {
		return a.FromNodeID < b.FromNodeID
	}
	if a.ToNodeID != b.ToNodeID {
		return a.ToNodeID < b.ToNodeID
	}
	return a.Kind < b.Kind
}
