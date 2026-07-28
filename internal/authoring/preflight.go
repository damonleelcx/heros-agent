package authoring

import (
	"context"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// PREFLIGHT — the one genuinely new mechanism 13c adds (task 9.2, FR24, design Decision 11).
//
// It moves an EXISTING refusal earlier in time. That is the whole of it: if preflight can say something
// the transform cannot, the two have already diverged, and the editor will eventually bless what the
// engine rejects.
//
// # Why earlier, when the operator path is fine with late
//
// An operator is a program: discovering a refusal after generating a candidate costs it nothing. A
// person is not. They will have chosen a model, written a prompt, and formed an intention first. Worse,
// the two most common authoring refusals — this node's apply mode cannot carry a parameter, this node's
// language has no materializer — are STRUCTURAL PROPERTIES OF THE NODE, knowable before the user types
// anything. Withholding a fact the system already holds until after the work is done is the exact
// interaction-simplicity failure the ordering puts three levels above implementation convenience.
//
// # Why three verdicts and not two
//
// A boolean forces every unknown into one of the two answers, and BOTH are lies:
//
//   - reporting `admissible` for something never measured asserts that a safety check succeeded when it
//     never ran — on an axis (context) whose failure mode is precisely "no error anywhere, the answer
//     just quietly gets worse";
//   - reporting `refused` blames the USER'S CHANGE for the PLATFORM'S missing measurement, and would
//     make the axis unusable on every workflow nobody has evaluated yet, which is every new one.
//
// So the gate never refuses on ignorance — and never passes on ignorance. It says which measurement is
// missing, and that is a next step rather than a dead end.
//
// # Preflight spends nothing
//
// No prompt version is published, no diff is written, no evaluation run is enqueued. `variantspec.Resolve`
// is a pure read by construction, and the materializability probe discards what it produces. This is
// asserted, not intended: a preflight that quietly published a prompt version on every keystroke would
// fill the registry with garbage nobody asked for.
type Verdict string

const (
	// VerdictAdmissible: every gate passed on evidence. This change can be submitted.
	VerdictAdmissible Verdict = "admissible"
	// VerdictRefused: a gate refused, on evidence, and named what and why.
	VerdictRefused Verdict = "refused"
	// VerdictNotYetMeasurable: a gate could not decide because an input it needs has never been
	// measured. NOT a refusal, and NOT a pass.
	VerdictNotYetMeasurable Verdict = "not_yet_measurable"
)

// Refusal is a named "no". Every field exists because a refusal that omits it sends the reader hunting.
type Refusal struct {
	// Cause is the engine's own sentence, rendered verbatim — not re-worded here, because it was
	// written to be read by the person who has to decide what to do about it.
	Cause string `json:"cause"`
	// NodeID is which node. A graph-level refusal leaves it empty and sets Shape instead.
	NodeID string `json:"node_id,omitempty"`
	// Field is which dimension, slot, tool, or parameter. "the model" is not an answer; "temperature on
	// an inline node" is.
	Field string `json:"field,omitempty"`
	// Shape names the KIND of change that cannot be materialized (P15: merge / prune / non-adjacent
	// move …). It is separate from Cause because a surface groups by it.
	Shape string `json:"shape,omitempty"`
}

// Named reports whether this refusal actually names something. A refusal that names nothing is a bug in
// whatever produced it, and the preflight result carries this so a test can say so.
func (r Refusal) Named() bool { return r.Cause != "" && (r.NodeID != "" || r.Shape != "") }

// MissingInput is the third verdict's payload: which measurement is absent, and for what.
type MissingInput struct {
	// Kind names the measurement ("context_drop_ratio"), so the surface can offer the way to obtain it.
	Kind string `json:"kind"`
	// NodeID and Subject scope it ("node n3", "policy summarization").
	NodeID  string `json:"node_id,omitempty"`
	Subject string `json:"subject,omitempty"`
}

// Result is preflight's answer. Exactly one of the three verdicts, with the payload that verdict owes.
type Result struct {
	Verdict Verdict      `json:"verdict"`
	Refusal Refusal      `json:"refusal,omitempty"`
	Missing MissingInput `json:"missing,omitempty"`
	// ConfigHash is the hash the change WOULD have. Present on an admissible result. Computing it costs
	// a resolve, which is a pure read, and it is what lets a surface say "this is a new configuration"
	// before anything is submitted.
	ConfigHash string `json:"config_hash,omitempty"`
	// Dimensions and Nodes summarize what the draft touches.
	Dimensions []string `json:"dimensions,omitempty"`
	Nodes      []string `json:"nodes,omitempty"`
	// Adapters are the adapter nodes an ordering change would require, surfaced BEFORE submission so a
	// person agrees to them with their eyes open (P15 FR24). Empty on every other axis.
	Adapters []string `json:"adapters,omitempty"`
}

// Admissible reports whether this draft may be submitted.
func (r Result) Admissible() bool { return r.Verdict == VerdictAdmissible }

// Resolver resolves a spec. Production passes variantspec.Resolve bound to the IR and registries; it is
// an interface so preflight can be exercised without a live registry.
type Resolver interface {
	Resolve(spec *variantspec.VariantSpec) (*variantspec.Resolved, error)
}

// Materializer probes whether the codemod could materialize a resolved config, WITHOUT keeping what it
// produces. Production wires the real transform engine, which is what makes preflight and the transform
// incapable of disagreeing: they are the same refusals, asked at two different times.
//
// It lives behind an interface so this package never imports the codemod — see the structural test in
// spine_test.go, and the package doc for why that matters.
type Materializer interface {
	Probe(ctx context.Context, r *variantspec.Resolved) (Refusal, error)
}

// Admissibility is a gate that may legitimately not know. Returning VerdictNotYetMeasurable is a
// first-class answer, not an error path.
//
// P16's drop-tolerance gate is the archetype: it judges on a MEASURED drop ratio, and where no
// measurement exists it must say so rather than guess in either direction.
type Admissibility interface {
	// Check judges one resolved config. It returns VerdictAdmissible with a zero Refusal when it passes.
	Check(ctx context.Context, r *variantspec.Resolved) (Verdict, Refusal, MissingInput)
}

// Preflighter runs a draft through the gates without spending anything.
//
// 🚫 Note what this struct does NOT have: no Force, no Override, no SkipGates, no AllowUnsafe, and no
// plan or role field. There is no way to construct a Preflighter that permits what the engine refuses,
// because a refusal exists when the artifact would be wrong in a way the author cannot see at the moment
// of choosing — and a human asking for it does not make the SDK, the slot, or the language match.
// `TestNoOverrideSuppressesAnyRefusal` asserts this over the struct itself, not over a sample of paths.
type Preflighter struct {
	// Resolver is required: without it there is no config to judge.
	Resolver Resolver
	// Materializer is required: it is the half that keeps preflight honest about what can ship.
	Materializer Materializer
	// Gates are the admissibility gates, run in order. Optional — an empty set means no axis has an
	// evidence-based opinion, which is the correct state for the prompt/model axis.
	Gates []Admissibility
}

var (
	// ErrNoResolver / ErrNoMaterializer: a Preflighter missing either half would silently return
	// `admissible` for everything, which is the worst possible default for a gate.
	ErrNoResolver     = errors.New("authoring: preflight requires a resolver")
	ErrNoMaterializer = errors.New("authoring: preflight requires a materializer probe")
)

// Preflight evaluates a draft and returns exactly one verdict.
//
// The order is deliberate and matches the specs: structure, then resolution, then materializability,
// then evidence-based admissibility. Materializability comes before the drop gate because "this node's
// language has no rewriter" is a cheaper and more fundamental fact than "this policy drops too much" —
// telling someone their policy is too lossy on a node that could never have carried it is a worse
// answer, not a more thorough one.
func (p Preflighter) Preflight(ctx context.Context, d Draft, parent *variantspec.VariantSpec) (Result, error) {
	if p.Resolver == nil {
		return Result{}, ErrNoResolver
	}
	if p.Materializer == nil {
		return Result{}, ErrNoMaterializer
	}

	base := Result{Dimensions: d.TouchedDimensions(), Nodes: d.TouchedNodes()}

	// 1. Structure. A malformed draft is the author's mistake and is named as such.
	spec, err := d.Derive(parent)
	if err != nil {
		return refused(base, Refusal{Cause: err.Error(), NodeID: firstNode(d)}), nil
	}
	if err := spec.Validate(); err != nil {
		return refused(base, Refusal{Cause: err.Error(), NodeID: firstNode(d)}), nil
	}

	// 2. Resolution. This is where the un-apply refusal, the unknown tool, and the unresolved ref live —
	// each already naming its node and dimension, which is why the cause is rendered verbatim.
	resolved, err := p.Resolver.Resolve(spec)
	if err != nil {
		return refused(base, refusalFromSpecError(err, d)), nil
	}
	base.ConfigHash = resolved.ConfigHash

	// 3. Materializability. The same refusals the transform raises, asked before submission.
	ref, err := p.Materializer.Probe(ctx, resolved)
	if err != nil {
		return Result{}, fmt.Errorf("authoring: materializability probe: %w", err)
	}
	if ref.Cause != "" {
		return refused(base, ref), nil
	}
	base.Adapters = adapterIDs(spec)

	// 4. Evidence-based admissibility. A gate here may legitimately not know.
	for _, g := range p.Gates {
		v, r, missing := g.Check(ctx, resolved)
		switch v {
		case VerdictRefused:
			return refused(base, r), nil
		case VerdictNotYetMeasurable:
			base.Verdict = VerdictNotYetMeasurable
			base.Missing = missing
			return base, nil
		}
	}

	base.Verdict = VerdictAdmissible
	return base, nil
}

func refused(base Result, r Refusal) Result {
	base.Verdict = VerdictRefused
	base.Refusal = r
	return base
}

// refusalFromSpecError renders a resolve failure verbatim and lifts the node/dimension it already
// names. variantspec.SpecError carries both; re-deriving them here would be a second opinion about
// which node broke.
func refusalFromSpecError(err error, d Draft) Refusal {
	var se *variantspec.SpecError
	if errors.As(err, &se) {
		return Refusal{Cause: err.Error(), NodeID: se.NodeID, Field: string(se.Dim)}
	}
	return Refusal{Cause: err.Error(), NodeID: firstNode(d)}
}

func firstNode(d Draft) string {
	if nodes := d.TouchedNodes(); len(nodes) > 0 {
		return nodes[0]
	}
	return ""
}

func adapterIDs(s *variantspec.VariantSpec) []string {
	if len(s.InsertedAdapters) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.InsertedAdapters))
	for _, a := range s.InsertedAdapters {
		out = append(out, a.AdapterNodeID)
	}
	return out
}
