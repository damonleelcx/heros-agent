package proposal

import (
	"fmt"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/typedcontract"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// ContractGate decides whether a candidate satisfies the P5 typed per-node I/O contract. A candidate
// whose changed wiring would break a downstream node's typed input — with no adapter to reconcile it —
// is refused (§1.4). This is the SAME validator P5 uses (typedcontract.ValidateOrdering), so a
// candidate that would be rejected at commit is rejected here at emission instead of surfaced.
type ContractGate interface {
	// Admit reports whether the candidate is contract-valid. When ok is false, reason names the
	// specific offending edge. When ok is true, adapters lists any adapters the validator would insert
	// (carried as in P5 — an "adapted" ordering is admissible).
	Admit(c Candidate) (ok bool, reason string, adapters []typedcontract.AdapterInsertion)
}

// TypedContractGate is the real gate: it runs typedcontract.ValidateOrdering over the candidate's
// ordering against the discovered IR's node I/O contracts.
type TypedContractGate struct {
	IR      *discovery.IR
	Catalog *typedcontract.Catalog
}

// NewTypedContractGate builds a gate over an IR, using the default adapter catalog.
func NewTypedContractGate(ir *discovery.IR) TypedContractGate {
	return TypedContractGate{IR: ir, Catalog: typedcontract.DefaultCatalog()}
}

// Admit validates the candidate's ordering. A candidate that changes only a node dimension (model,
// prompt, skills, context) leaves the wiring and every node's declared I/O contract unchanged, so it
// is coherent by construction; only structural candidates (reorder / prune / merge) can be rejected
// here — which is exactly where a nonsensical change would otherwise slip through.
func (g TypedContractGate) Admit(c Candidate) (bool, string, []typedcontract.AdapterInsertion) {
	if g.IR == nil || c.Spec == nil {
		return true, "", nil
	}
	ord := orderingFromSpec(c.Spec)
	v := typedcontract.ValidateOrdering(g.IR, ord, g.Catalog)
	switch v.Kind {
	case typedcontract.VerdictCoherent:
		return true, "", nil
	case typedcontract.VerdictAdapted:
		return true, "", v.Adapters
	default:
		reason := "contract violation"
		if len(v.Diagnostics) > 0 {
			d := v.Diagnostics[0]
			reason = fmt.Sprintf("edge %s→%s: %s (%v)", d.FromNodeID, d.ToNodeID, d.Reason, d.Fields)
		}
		return false, reason, nil
	}
}

// orderingFromSpec projects a Variant Spec into the minimal Ordering the contract validator reads.
func orderingFromSpec(s *variantspec.VariantSpec) typedcontract.Ordering {
	edges := make([]typedcontract.Edge, 0, len(s.Edges))
	for _, e := range s.Edges {
		edges = append(edges, typedcontract.Edge{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: e.Kind})
	}
	return typedcontract.Ordering{Order: append([]string(nil), s.Order...), Edges: edges}
}
