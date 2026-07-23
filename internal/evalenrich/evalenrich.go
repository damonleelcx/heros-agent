// Package evalenrich is P5's activation of the P4 eval-set generator's seed-from-real-traces interface
// (task 7.1) and its per-path targeting over the reconciled graph (task 7.2). P4 shipped the
// SeedTraceGenerator returning ErrGeneratorInactive precisely so P5 adds a DATA SOURCE to an existing
// layer, rather than inserting a new layer into a loop whose ordering was fixed without it.
//
//   - Seed from real traces: the observed trace INPUTS are the most realistic baseline available — they
//     are what users actually sent — so they become seed cases directly.
//   - Per-path targeting: the reconciler (§5) surfaced runtime-only edges and loop invocation counts
//     static analysis could not; this generates cases that FORCE each reconciled path, including those
//     runtime-only edges and loop min/typical/max iteration counts, feeding back into P4 coverage.
package evalenrich

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/dynamictracing"
	"github.com/heros-foreal/agentd/internal/evalgen"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/reconcile"
)

// BlobFetcher resolves a content-hashed input blob back to its bytes. The interceptor stored inputs by
// hash (redacted); this reads them back to seed a case.
type BlobFetcher interface {
	Get(ctx context.Context, contentHash string) ([]byte, error)
}

// TraceSeedSource implements evalgen.TraceCaseSource. It mines the observed trace inputs as seed cases
// and, from the reconciliation report, emits per-path targets. Constructing it and assigning it to a
// SeedTraceGenerator.Source is what "activates" the P4 layer.
type TraceSeedSource struct {
	WorkflowID string
	Calls      []dynamictracing.TracedCall
	Blobs      BlobFetcher
	// Report is the reconciliation of this traced run — the source of runtime-only edges and loop
	// bounds for per-path targeting. Optional: seeding works without it.
	Report *reconcile.Report
}

// Ensure the source satisfies the P4 interface at compile time.
var _ evalgen.TraceCaseSource = (*TraceSeedSource)(nil)

// CasesFromTraces yields seed cases from the observed inputs plus per-path targets. Deterministic:
// inputs are de-duplicated by content hash and emitted in sorted hash order, so the same trace yields
// the same seed set.
func (s *TraceSeedSource) CasesFromTraces(ctx context.Context, ir *discovery.IR, gap evalgen.Gap) ([]evalharness.Case, error) {
	var out []evalharness.Case

	// 1. Seed from real trace inputs (task 7.1).
	seen := map[string]bool{}
	var hashes []string
	inputByHash := map[string][]byte{}
	for _, c := range s.Calls {
		h := c.InputsBlobHash
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		b, err := s.fetch(ctx, h)
		if err != nil {
			// Best-effort: a missing blob just means one fewer seed, never a failed generation.
			continue
		}
		if !json.Valid(b) {
			continue // a seed case's input must be valid JSON the harness can drive
		}
		hashes = append(hashes, h)
		inputByHash[h] = b
	}
	sort.Strings(hashes)
	for _, h := range hashes {
		out = append(out, evalharness.Case{
			CaseID:     "seed_trace_" + short(h),
			WorkflowID: s.WorkflowID,
			Input:      inputByHash[h],
			Label:      evalharness.LabelNone,
			Origin:     evalharness.OriginSeedTrace,
		})
	}

	// 2. Per-path targeting over the reconciled graph (task 7.2).
	if s.Report != nil {
		out = append(out, PerPathTargets(s.WorkflowID, *s.Report)...)
	}
	return out, nil
}

// PerPathTargets generates cases that FORCE each reconciled path: every runtime-only edge, and for each
// looping node its min/typical/max iteration counts. The cases carry PathTags naming the target so P4's
// coverage measurer can tell whether a run actually forced it. Inputs are minimal JSON placeholders —
// the point is the PATH TAG (the target), which the loop generator + prober refine.
func PerPathTargets(workflowID string, rep reconcile.Report) []evalharness.Case {
	var out []evalharness.Case

	// Runtime-only edges: one target each, so P4 must generate a case that forces the edge static
	// analysis missed.
	for _, e := range rep.RuntimeOnlyEdges() {
		tag := evalgen.EdgeID(e.FromNodeID, e.ToNodeID)
		out = append(out, evalharness.Case{
			CaseID:     "path_target_edge_" + sanitize(e.FromNodeID+"_"+e.ToNodeID),
			WorkflowID: workflowID,
			Input:      json.RawMessage(`{}`),
			Label:      evalharness.LabelNone,
			Origin:     evalharness.OriginSeedTrace,
			PathTags:   []string{tag},
		})
	}

	// Loop bounds: a node observed to iterate >1 gets min/typical/max targets.
	for _, n := range rep.Nodes {
		if n.InvocationCount <= 1 {
			continue
		}
		for _, bound := range []string{"min", "typical", "max"} {
			out = append(out, evalharness.Case{
				CaseID:     "path_target_loop_" + sanitize(n.NodeID) + "_" + bound,
				WorkflowID: workflowID,
				Input:      json.RawMessage(`{}`),
				Label:      evalharness.LabelNone,
				Origin:     evalharness.OriginSeedTrace,
				PathTags:   []string{evalgen.LoopBoundID(n.NodeID, bound)},
			})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].CaseID < out[j].CaseID })
	return out
}

func (s *TraceSeedSource) fetch(ctx context.Context, hash string) ([]byte, error) {
	if s.Blobs == nil {
		return nil, fmt.Errorf("evalenrich: no blob fetcher configured")
	}
	return s.Blobs.Get(ctx, hash)
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func sanitize(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b = append(b, byte(r))
		default:
			b = append(b, '_')
		}
	}
	return string(b)
}

// HashInput content-addresses an input, matching how the interceptor stored it, so a caller can build a
// blob store keyed identically.
func HashInput(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
