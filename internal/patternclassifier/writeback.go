package patternclassifier

import (
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// WriteBack returns a COPY of ir carrying the classification's labels, written into the
// P0-reserved `pattern_labels` field — node-scoped labels onto their node, region-scoped labels
// onto their subgraph.
//
// It returns a copy rather than mutating in place: the input IR is a contract document that other
// consumers may hold, and a classifier that silently rewrites its caller's IR would make "did this
// IR carry labels?" depend on evaluation order.
//
// The write is ADDITIVE in the strict sense: an IR with no labels serialises byte-identically to how
// it did before P3.5 existed (every new field is omitempty), and a labelled IR validates against the
// same workflow-ir.schema.json at the same MAJOR. Only the MINOR is bumped, and only when labels are
// actually written.
func WriteBack(ir *discovery.IR, res Result) (*discovery.IR, error) {
	if ir == nil {
		return nil, fmt.Errorf("patternclassifier: nil IR")
	}
	out := *ir
	out.Nodes = append([]discovery.IRNode(nil), ir.Nodes...)
	out.Edges = append([]discovery.IREdge(nil), ir.Edges...)
	out.Subgraphs = append([]discovery.IRSubgraph(nil), ir.Subgraphs...)

	nodeIdx := make(map[string]int, len(out.Nodes))
	for i := range out.Nodes {
		nodeIdx[out.Nodes[i].NodeID] = i
		out.Nodes[i].PatternLabels = nil // a re-classification replaces labels; it never accumulates them
	}

	// Only the subgraphs this classification actually defines are written. A stale subgraph from a
	// previous run must not survive with labels nothing produced.
	out.Subgraphs = nil
	sgIdx := map[string]int{}
	for _, sg := range res.Subgraphs {
		sgIdx[sg.SubgraphID] = len(out.Subgraphs)
		out.Subgraphs = append(out.Subgraphs, discovery.IRSubgraph{
			SubgraphID: sg.SubgraphID, NodeIDs: append([]string(nil), sg.NodeIDs...),
		})
	}

	wrote := 0
	for _, l := range res.Labels {
		// The write-time gate again, at the actual write. Validate() ran when the label was minted,
		// but this is the boundary that matters: nothing reaches the IR without passing it, however
		// the Result was assembled or by whom.
		if err := l.Validate(); err != nil {
			return nil, fmt.Errorf("patternclassifier: refusing to write an invalid label: %w", err)
		}
		wire := discovery.IRPatternLabel{
			Pattern: string(l.Pattern), Confidence: l.Confidence, Source: string(l.Source),
			SubgraphRef: l.SubgraphRef, DetectorID: l.DetectorID, LLMRunRef: l.LLMRunRef,
			TaxonomyVersion: l.TaxonomyVersion, Candidate: l.Candidate,
		}
		if i, ok := sgIdx[l.SubgraphRef]; ok {
			out.Subgraphs[i].PatternLabels = append(out.Subgraphs[i].PatternLabels, wire)
		} else if i, ok := nodeIdx[l.SubgraphRef]; ok {
			out.Nodes[i].PatternLabels = append(out.Nodes[i].PatternLabels, wire)
		} else {
			return nil, fmt.Errorf("patternclassifier: label %q references %q, which is neither a subgraph nor a node",
				l.Pattern, l.SubgraphRef)
		}
		wrote++
	}

	// Deterministic output: the same classification must serialise byte-identically every time.
	for i := range out.Nodes {
		sortWireLabels(out.Nodes[i].PatternLabels)
	}
	for i := range out.Subgraphs {
		sortWireLabels(out.Subgraphs[i].PatternLabels)
	}
	sort.SliceStable(out.Subgraphs, func(a, b int) bool { return out.Subgraphs[a].SubgraphID < out.Subgraphs[b].SubgraphID })

	if wrote > 0 {
		// Declared only when labels were actually written: an IR that gained nothing keeps saying
		// exactly what it said before.
		out.IRVersion = discovery.IRVersionPatternLabels
	}
	return &out, nil
}

func sortWireLabels(ls []discovery.IRPatternLabel) {
	sort.SliceStable(ls, func(i, j int) bool {
		if ls[i].Pattern != ls[j].Pattern {
			return ls[i].Pattern < ls[j].Pattern
		}
		return ls[i].Source < ls[j].Source
	})
}

// ReadLabels extracts every label carried by an IR, from both nodes and subgraphs, validating each.
// It is the inverse of WriteBack and exists so a consumer (the UI, P4's metric selection) reads
// labels through one checked path rather than reaching into the document and trusting what it finds.
//
// An invalid stored label is REPORTED, not skipped: a label that fails validation on read means
// either the document was written by something that bypassed the gate or the taxonomy moved under
// it, and both are things a consumer must know rather than quietly drop.
func ReadLabels(ir *discovery.IR) ([]Label, []Diagnostic, error) {
	if ir == nil {
		return nil, nil, fmt.Errorf("patternclassifier: nil IR")
	}
	var out []Label
	var diags diagSink
	take := func(ls []discovery.IRPatternLabel, fallbackRef string) {
		for _, w := range ls {
			ref := w.SubgraphRef
			if ref == "" {
				// A P0/P1-era label carries only {pattern, confidence}. Its region is the thing it
				// was attached to, which is the most that document ever said.
				ref = fallbackRef
			}
			l := Label{
				Pattern: Pattern(w.Pattern), Confidence: w.Confidence, Source: Source(w.Source),
				SubgraphRef: ref, DetectorID: w.DetectorID, LLMRunRef: w.LLMRunRef,
				TaxonomyVersion: w.TaxonomyVersion, Candidate: w.Candidate,
			}
			if err := l.Validate(); err != nil {
				diags.rejectLabel(l, err)
				continue
			}
			out = append(out, l)
		}
	}
	for _, n := range ir.Nodes {
		take(n.PatternLabels, n.NodeID)
	}
	for _, sg := range ir.Subgraphs {
		take(sg.PatternLabels, sg.SubgraphID)
	}
	sort.SliceStable(out, func(i, j int) bool { return LabelLess(out[i], out[j]) })
	return out, diags.sorted(), nil
}
