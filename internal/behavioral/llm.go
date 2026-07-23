package behavioral

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

// Classifier is the constrained LLM-as-classifier for the AMBIGUOUS RESIDUE only (task 6.4). It is
// handed one subgraph's evidence and MUST return a pattern from the fixed 20-pattern taxonomy with a
// confidence, or nothing. It never sees a subgraph a rule already decided.
type Classifier interface {
	// Classify returns a taxonomy pattern + confidence for an ambiguous subgraph, or ok=false to abstain.
	Classify(subgraphRef string, evidence map[string]any) (pattern patternclassifier.Pattern, confidence float64, ok bool)
}

// ClassifyResidue runs the constrained LLM classifier over ONLY the subgraphs the rules left
// unconfirmed, and returns candidate labels that NEVER override a confident rule/behavioral label
// (task 6.4). The returned labels are marked candidate with a capped confidence: an LLM guess about a
// behavioral pattern is still a guess, so it stays a structural-strength candidate, subordinate to any
// rule/behavioral confirmation.
//
// resolved is the set of subgraphRefs a rule or behavioral confirmation already decided; those are
// skipped entirely, which is the mechanical guarantee that the LLM cannot override them.
func ClassifyResidue(clf Classifier, ambiguous map[string]map[string]any, resolved map[string]bool) []patternclassifier.Label {
	if clf == nil {
		return nil
	}
	var out []patternclassifier.Label
	for _, ref := range sortedKeys(ambiguous) {
		if resolved[ref] {
			continue // a confident rule/behavioral label already owns this subgraph — never override it
		}
		pattern, confidence, ok := clf.Classify(ref, ambiguous[ref])
		if !ok || !patternclassifier.InTaxonomy(pattern) {
			continue
		}
		// Cap an LLM guess at the structural-candidate ceiling for behavioral patterns; a non-behavioral
		// LLM guess keeps its confidence but stays source=llm (not confirmed).
		if patternclassifier.IsBehavioral(pattern) && confidence > patternclassifier.BehavioralCandidateCap {
			confidence = patternclassifier.BehavioralCandidateCap
		}
		lbl := patternclassifier.Label{
			Pattern: pattern, Confidence: confidence, Source: patternclassifier.SourceLLM,
			SubgraphRef: ref, LLMRunRef: "behavioral-residue:" + ref,
			TaxonomyVersion: patternclassifier.TaxonomyVersion,
			Candidate:       patternclassifier.IsBehavioral(pattern),
		}
		out = append(out, lbl)
	}
	sort.Slice(out, func(i, j int) bool { return patternclassifier.LabelLess(out[i], out[j]) })
	return out
}

// ResolvedRefs collects the subgraphRefs a confirmation Result already decided, so ClassifyResidue can
// skip them.
func (r Result) ResolvedRefs() map[string]bool {
	out := map[string]bool{}
	for _, l := range r.Confirmed {
		out[l.SubgraphRef] = true
	}
	return out
}
