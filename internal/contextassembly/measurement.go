package contextassembly

import (
	"context"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/registry"
)

// Retrieval measurement pinning (P16 task 6.4, design.md Decision 5, FR12/NFR2)
// ─────────────────────────────────────────────────────────────────────────────
//
// A measurement run pins the retriever, its parameters, and the seed, so re-running the same
// `config_hash` at the same `source_revision` issues the IDENTICAL resolved retrieval request —
// including any rerank.
//
// # Why an unpinned retriever is not "slightly noisy", it is a broken hash
//
// The alternative is to let the retriever run free and average over seeds, which models real retrieval
// variance. design.md Decision 5 rejects it on **L2 稳定 + reproducibility**: an unpinned retriever
// makes a `config_hash` non-reproducible, so two runs of the same configuration disagree — and
// re-deriving a result from its hash is the one thing the whole lineage design exists to guarantee. A
// platform whose principle is "verification decides" cannot have configurations that mean two things.
//
// # The reproducibility CEILING is the resolved request, and saying so is the honest part
//
// P3 set this and P16 keeps it: for a policy with a retriever or a model behind it, determinism is
// asserted at the RESOLVED REQUEST — the ref, the params, the query, the top-k, the seed — never at the
// provider's output bytes, which are outside anything this platform controls. Claiming byte-determinism
// for a network call would be a promise the system cannot keep, and a promise that fails silently.

// ErrUnpinnedMeasurement is returned when a run that claims to be a measurement does not pin what a
// measurement has to pin. It is a refusal: an unpinned run's number must not reach the verified-delta
// ledger, because nothing can re-derive it.
var ErrUnpinnedMeasurement = errors.New("contextassembly: not a pinned measurement run")

// MeasurementPin is the triple that makes a retrieval run reproducible. All three are required —
// each answers a different "same as what?":
//
//	ConfigHash      the same CONFIGURATION (which policy, which params, which retriever ref)
//	SourceRevision  the same CODE (the call sites and the graph the configuration was resolved against)
//	Seed            the same DRAW (what the rerank and any sampling step do)
//
// Two of the three is not a pin. A config_hash without a source_revision names a configuration over
// source that may have moved; a seed without either names a draw over nothing.
type MeasurementPin struct {
	ConfigHash     string
	SourceRevision string
	Seed           int64
}

// Validate reports whether this pin is complete enough to call a run a measurement.
//
// 🔴 Seed 0 is a legitimate pinned seed — it is a value, not an absence — so it is deliberately NOT
// checked for emptiness. Treating 0 as "unset" would reject the most obvious seed anyone picks.
func (p MeasurementPin) Validate() error {
	if p.ConfigHash == "" {
		return fmt.Errorf("%w: no config_hash, so the run names no configuration to re-derive", ErrUnpinnedMeasurement)
	}
	if p.SourceRevision == "" {
		return fmt.Errorf("%w: no source_revision, so the configuration was resolved against source that "+
			"cannot be identified", ErrUnpinnedMeasurement)
	}
	return nil
}

// Measurement is one pinned retrieval assembly: the assembled context, and the resolved request that is
// the determinism handle.
type Measurement struct {
	Assembled registry.AssembledContext
	// Request is what the policy actually issued to the host. It is the artifact two runs are compared
	// at — the reproducibility claim is "identical resolved request", never "identical provider bytes".
	// Nil for an LLM-free policy, which needs no handle because its output IS byte-identical.
	Request *registry.ResolvedRequest
}

// Measure runs a pinned retrieval assembly and returns its determinism handle alongside the result.
//
// It refuses an unpinned run rather than measuring it. That refusal is the enforcement of the spec's
// "an unpinned retriever is not a measurement run": the alternative — measure it and label the result
// somehow — puts an unreproducible number one careless join away from the ledger.
func (r Runner) Measure(ctx context.Context, req Request, pin MeasurementPin) (Measurement, error) {
	if err := pin.Validate(); err != nil {
		return Measurement{}, err
	}
	// The pin's seed is the seed. A request carrying a different one would mean the run measured a draw
	// the pin does not name, so the pin — the thing that will be recorded — wins.
	req.Seed = pin.Seed
	req.Tags.Seed = pin.Seed
	if req.Tags.ConfigHash == "" {
		req.Tags.ConfigHash = pin.ConfigHash
	}

	got, err := r.Assemble(ctx, req)
	if err != nil {
		return Measurement{}, err
	}
	return Measurement{Assembled: got, Request: got.ResolvedRequest}, nil
}

// SameRequest reports whether two measurements issued the identical resolved request — the assertion
// NFR2 defines determinism as, stated as a function so no caller re-derives what "identical" means.
//
// Two LLM-free measurements (both handles nil) are identical iff their assembled messages are, which is
// the stronger byte-level claim those policies can actually keep.
func SameRequest(a, b Measurement) bool {
	if a.Request == nil || b.Request == nil {
		if a.Request != nil || b.Request != nil {
			return false
		}
		return sameMessages(a.Assembled.Messages, b.Assembled.Messages)
	}
	x, y := *a.Request, *b.Request
	if x.Op != y.Op || x.ModelRef != y.ModelRef || x.Ref != y.Ref || x.Query != y.Query ||
		x.TopK != y.TopK || x.Seed != y.Seed {
		return false
	}
	return sameMessages(x.Messages, y.Messages)
}

func sameMessages(a, b []registry.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
