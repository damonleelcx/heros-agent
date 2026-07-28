package proposal

import (
	"fmt"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/typedcontract"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// ContractGate decides whether a candidate satisfies the P5 typed per-node I/O contract. A candidate
// whose changed wiring would break a downstream node's typed input — with no adapter to reconcile it —
// is refused (§1.4). This is the SAME gate P5's editor commits through (variantspec.GateReorder), so a
// candidate that would be rejected at commit is rejected here at emission instead of surfaced.
type ContractGate interface {
	// Admit reports whether the candidate is contract-valid and returns the GATED candidate — the spec
	// that leaves the gate. On an `adapted` verdict that spec is adapter-AUGMENTED: the bridging
	// adapters are recorded on it and its edges are rewired producer→adapter→consumer, so the candidate
	// that goes on to be compiled is the one whose diff will carry the adapter source.
	//
	// When ok is false, reason names the specific offending edge and the returned candidate is not
	// runnable — nothing downstream may use it.
	Admit(c Candidate) (gated Candidate, ok bool, reason string)
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
//
// 🔴 It routes through variantspec.GateReorder rather than calling ValidateOrdering itself (P15 task
// 3.2). That is not a refactor for tidiness — it is the reason the gate cannot be half-applied. Two
// things follow from having ONE gate:
//
//   - the rejected path returns NO runnable spec, structurally, so an incoherent ordering cannot reach
//     the transform engine even by mistake (design Decision 3);
//   - the `adapted` path returns the ADAPTER-AUGMENTED spec, so the bridging adapter travels on the
//     candidate into compilation and appears in the reviewable diff. Reading the verdict's adapters and
//     discarding them — which this gate did before P15 — produced a candidate whose config_hash claimed
//     a bridged edge that no diff contained (decisions.md D-2).
func (g TypedContractGate) Admit(c Candidate) (Candidate, bool, string) {
	if g.IR == nil || c.Spec == nil {
		return c, true, ""
	}
	catalog := g.Catalog
	if catalog == nil {
		catalog = typedcontract.DefaultCatalog()
	}
	gated, v := variantspec.GateReorder(g.IR, c.Spec, catalog)
	if gated == nil {
		reason := "contract violation"
		if len(v.Diagnostics) > 0 {
			d := v.Diagnostics[0]
			reason = fmt.Sprintf("edge %s→%s: %s (%v)", d.FromNodeID, d.ToNodeID, d.Reason, d.Fields)
		}
		return c, false, reason
	}
	c.Spec = gated
	return c, true, ""
}
