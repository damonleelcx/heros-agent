// Package irwriteback implements P5's ADDITIVE IR write-back (task 8.2) and permissive-schema
// refinement (task 8.3). Reconciled runtime edges/nodes (§5) and confirmed behavioral labels (§6) are
// written back to the IR at the SAME `ir_version` MAJOR, so a pre-P5 consumer still parses the enriched
// IR (the P0 additive-evolution rule, Decision 7). Node `io_contract` schemas MAY be refined from
// observed trace shapes — tightening coherence without a schema-version break — and the refinement is
// SURFACED (which nodes stay permissive, which orderings a refinement would affect), never silent.
package irwriteback

import (
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

// AddBehavioralLabels writes confirmed behavioral labels (source=behavioral) into the IR ADDITIVELY: it
// APPENDS them to the matching subgraph's / node's pattern_labels, preserving the structural candidates
// already there (the evidence trail), and declares the pattern-labels MINOR — same MAJOR, so a pre-P5
// consumer pinned to MAJOR 1 still parses it. It returns a copy; the input IR is untouched.
func AddBehavioralLabels(ir *discovery.IR, confirmed []patternclassifier.Label) (*discovery.IR, error) {
	if ir == nil {
		return nil, fmt.Errorf("irwriteback: nil IR")
	}
	out := *ir
	out.Nodes = append([]discovery.IRNode(nil), ir.Nodes...)
	out.Subgraphs = append([]discovery.IRSubgraph(nil), ir.Subgraphs...)
	for i := range out.Nodes {
		out.Nodes[i].PatternLabels = append([]discovery.IRPatternLabel(nil), ir.Nodes[i].PatternLabels...)
	}
	for i := range out.Subgraphs {
		out.Subgraphs[i].PatternLabels = append([]discovery.IRPatternLabel(nil), ir.Subgraphs[i].PatternLabels...)
	}

	nodeIdx := map[string]int{}
	for i := range out.Nodes {
		nodeIdx[out.Nodes[i].NodeID] = i
	}
	sgIdx := map[string]int{}
	for i := range out.Subgraphs {
		sgIdx[out.Subgraphs[i].SubgraphID] = i
	}

	wrote := 0
	// Sort for deterministic output.
	labels := append([]patternclassifier.Label(nil), confirmed...)
	sort.Slice(labels, func(i, j int) bool { return patternclassifier.LabelLess(labels[i], labels[j]) })

	for _, l := range labels {
		// The write-time gate: nothing reaches the IR without passing Validate. A confirmed behavioral
		// label is source=behavioral, uncapped, not a candidate — Validate enforces that shape.
		if err := l.Validate(); err != nil {
			return nil, fmt.Errorf("irwriteback: refusing to write an invalid behavioral label: %w", err)
		}
		wire := discovery.IRPatternLabel{
			Pattern: string(l.Pattern), Confidence: l.Confidence, Source: string(l.Source),
			SubgraphRef: l.SubgraphRef, TaxonomyVersion: l.TaxonomyVersion, Candidate: l.Candidate,
		}
		if i, ok := sgIdx[l.SubgraphRef]; ok {
			out.Subgraphs[i].PatternLabels = appendIfAbsent(out.Subgraphs[i].PatternLabels, wire)
		} else if i, ok := nodeIdx[l.SubgraphRef]; ok {
			out.Nodes[i].PatternLabels = appendIfAbsent(out.Nodes[i].PatternLabels, wire)
		} else {
			return nil, fmt.Errorf("irwriteback: behavioral label %q references %q, which is neither a subgraph nor a node",
				l.Pattern, l.SubgraphRef)
		}
		wrote++
	}
	for i := range out.Nodes {
		sortWireLabels(out.Nodes[i].PatternLabels)
	}
	for i := range out.Subgraphs {
		sortWireLabels(out.Subgraphs[i].PatternLabels)
	}
	if wrote > 0 && sameOrEarlier(out.IRVersion, discovery.IRVersionPatternLabels) {
		out.IRVersion = discovery.IRVersionPatternLabels
	}
	return &out, nil
}

// appendIfAbsent appends a label unless an identical (pattern, source) label already exists — so
// re-running confirmation is idempotent and does not duplicate a label.
func appendIfAbsent(ls []discovery.IRPatternLabel, l discovery.IRPatternLabel) []discovery.IRPatternLabel {
	for _, e := range ls {
		if e.Pattern == l.Pattern && e.Source == l.Source && e.SubgraphRef == l.SubgraphRef {
			return ls
		}
	}
	return append(ls, l)
}

func sortWireLabels(ls []discovery.IRPatternLabel) {
	sort.SliceStable(ls, func(i, j int) bool {
		if ls[i].Pattern != ls[j].Pattern {
			return ls[i].Pattern < ls[j].Pattern
		}
		return ls[i].Source < ls[j].Source
	})
}

// sameOrEarlier reports whether current <= target by the frozen version strings. A crude compare is
// enough: the versions are "1.0.0" / "1.1.0" / "1.2.0", same length and MAJOR.
func sameOrEarlier(current, target string) bool { return current <= target }
