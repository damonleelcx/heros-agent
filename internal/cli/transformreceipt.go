package cli

import (
	"errors"
	"strings"

	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/transform"
)

// transformreceipt.go projects a generated patch onto the third opt-in egress payload.
//
// It is the counterpart of workflowir.go and follows the same discipline for the same reason: `runlink`
// owns the wire shape and does not import the transform engine, so a field added to `transform.Patch`
// tomorrow is ABSENT from the wire until somebody writes a line here.
//
// 🔴 The one field this file must never read is `Patch.Diff`. It is right there, it is the product's
// primary artefact, and `TransformReceipt` has no field it could occupy — the three integers below are
// what stands in for it. `diffstat` walks the diff to COUNT, and the counting is done here rather than
// on the platform precisely so the thing being counted never leaves.

// BuildTransformReceipt reads the allowlisted fields off a successful patch.
func BuildTransformReceipt(p *transform.Patch, workflowID, toolVersion string) runlink.TransformReceipt {
	r := runlink.TransformReceipt{
		ContractVersion: runlink.TransformReceiptContractVersion,
		ConfigHash:      p.ConfigHash,
		SourceRevision:  p.SourceRevision,
		WorkflowID:      workflowID,
		ToolVersion:     toolVersion,
		CoverageVersion: transform.CoverageTableVersion(),
		Status:          "applied",
	}
	if p.IsEmpty() {
		// A spec with no overrides is the legitimate baseline, not a failure, and it is not `applied`
		// either — nothing was. Its own terminal state, so the console does not render "0 files changed"
		// under a heading that claims a transform happened.
		r.Status = "baseline"
	}
	// Per NODE, not per (node, dimension). `Touched` is keyed by both, and a node whose model and context
	// were rewritten is one node with one outcome — reporting it twice would double every count on the
	// surface that renders them.
	seen := map[string]bool{}
	for _, td := range p.Touched {
		if seen[td.NodeID] {
			continue
		}
		seen[td.NodeID] = true
		r.NodeOutcomes = append(r.NodeOutcomes, runlink.WireNodeOutcome{
			NodeID: td.NodeID, Outcome: runlink.OutcomeApplied,
		})
	}
	r.FilesChanged, r.LinesAdded, r.LinesRemoved = diffstat(p.Diff)
	return r
}

// BuildRefusedTransformReceipt reads what a REFUSAL says.
//
// A refusal is a receipt too, and it is arguably the more useful one: `/app/transforms/…` resolving to
// "the engine declined this node, and here is the class" is a real answer, where the surface being empty
// is indistinguishable from the customer never having tried.
//
// Returns false when the error is not a refusal — an internal failure has no node, no cause and no
// diffstat, and manufacturing a receipt out of one would report a platform bug as a fact about the
// customer's code.
func BuildRefusedTransformReceipt(err error, configHash, sourceRevision, workflowID, toolVersion string) (runlink.TransformReceipt, bool) {
	var re *transform.RewriteError
	if !errors.As(err, &re) || re.NodeID == "" || !re.Cause.Valid() {
		return runlink.TransformReceipt{}, false
	}
	return runlink.TransformReceipt{
		ContractVersion: runlink.TransformReceiptContractVersion,
		ConfigHash:      configHash,
		SourceRevision:  sourceRevision,
		WorkflowID:      workflowID,
		ToolVersion:     toolVersion,
		CoverageVersion: transform.CoverageTableVersion(),
		Status:          "refused",
		NodeOutcomes: []runlink.WireNodeOutcome{{
			NodeID: re.NodeID, Outcome: runlink.OutcomeRefused, Cause: string(re.Cause),
		}},
		// No diffstat: nothing was generated. Three zeros are the truthful counts, and they are
		// distinguishable from "applied, changing nothing" by `Status`.
	}, true
}

// diffstat counts a unified diff without retaining any of it.
//
// 🔴 It returns three integers and holds no line. The `+++`/`---` headers are counted as files and
// deliberately never returned as PATHS: a path is a fact about the customer's repository layout, the
// console renders a number, and a field that could hold a path is a field that could hold a filename
// carrying a customer's project structure.
func diffstat(diff []byte) (files, added, removed int) {
	for _, line := range strings.Split(string(diff), "\n") {
		switch {
		case strings.HasPrefix(line, "+++"):
			files++
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "@@"):
			// Header lines. Counted as neither an addition nor a removal — a `---` header starts with the
			// same character as a removed line, and miscounting it would inflate every receipt by one per
			// file.
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return files, added, removed
}
