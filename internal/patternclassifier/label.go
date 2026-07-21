package patternclassifier

import (
	"encoding/json"
	"fmt"
	"math"
)

// Source names the layer that produced a label. Closed set: a third source would change what
// "rules-first precedence" means and must be a deliberate contract change, not a new string.
type Source string

const (
	SourceRule Source = "rule"
	SourceLLM  Source = "llm"
)

// Confidence bands. These are the calibrated values the detectors draw from, not free numbers —
// calibration_test.go checks each detector's band against the hand-labeled fixture set and reports
// agreement. The bands exist so "how sure are we" is a taxonomy-level decision with a stated reason,
// rather than a magic constant per detector.
const (
	// ConfidenceTopologyDetermined: the signature is the pattern. Nothing about the runtime can make
	// a linear two-LLM data chain not be a prompt chain. Reserved for detectors whose predicate is
	// definitional over the IR.
	ConfidenceTopologyDetermined = 0.95

	// ConfidenceTopologyStrong: the signature determines the pattern given the node metadata
	// Discovery extracts, but that metadata is itself best-effort (a "manager" role or a "cost"
	// condition is read off names/policies, not proven). Discounted for that inference step.
	ConfidenceTopologyStrong = 0.80

	// BehavioralCandidateCap is the HARD ceiling on any pattern whose Detection is behavioral. It is
	// the honesty boundary made mechanical: structure proved a loop EXISTS, not that it ITERATES, so
	// the label may never claim more than "candidate". Label.Validate rejects anything above it —
	// a detector that tries to assert a behavioral pattern at 0.9 fails, loudly, at write time.
	BehavioralCandidateCap = 0.60
)

// Label is one pattern label against one region of the IR — the record written to pattern_labels.
//
// Shape is fixed by PRD §8.5 / design Decision 6. It reuses the field P0 reserved on nodes and
// subgraphs, so writing labels is additive: an IR with labels and an IR without both validate at the
// same ir_version MAJOR.
type Label struct {
	// Pattern is drawn from the closed 20-pattern taxonomy. Anything else is rejected at write time.
	Pattern Pattern `json:"pattern"`
	// Confidence in [0,1]. Required — a label without one is invalid (see UnmarshalJSON).
	Confidence float64 `json:"confidence"`
	// Source is the producing layer: rule or llm.
	Source Source `json:"source"`
	// SubgraphRef names the region this label applies to. Required: a label with no subgraph_ref is
	// invalid, because a label that does not say WHAT it labels cannot dispatch a metric-set.
	SubgraphRef string `json:"subgraph_ref"`
	// DetectorID identifies the rule detector. Required when Source is rule, forbidden otherwise —
	// so a label's provenance is always reconstructible.
	DetectorID string `json:"detector_id,omitempty"`
	// LLMRunRef keys the reproducibility record (model/seed/temperature/prompt_version). Required
	// when Source is llm, forbidden otherwise.
	LLMRunRef string `json:"llm_run_ref,omitempty"`
	// TaxonomyVersion pins the vocabulary this label was drawn from.
	TaxonomyVersion string `json:"taxonomy_version"`
	// Candidate marks a label that structure could not confirm (a behavioral pattern). It is
	// redundant with IsBehavioral(Pattern) by construction and is written anyway: a consumer reading
	// a stored label must not have to carry the taxonomy to know the label is unconfirmed.
	Candidate bool `json:"candidate,omitempty"`
}

// labelWire mirrors Label with a pointer Confidence so "absent" is distinguishable from "0.0".
// Confidence 0 is a legal value; a MISSING confidence is not, and the two must not collapse.
type labelWire struct {
	Pattern         Pattern  `json:"pattern"`
	Confidence      *float64 `json:"confidence"`
	Source          Source   `json:"source"`
	SubgraphRef     string   `json:"subgraph_ref"`
	DetectorID      string   `json:"detector_id,omitempty"`
	LLMRunRef       string   `json:"llm_run_ref,omitempty"`
	TaxonomyVersion string   `json:"taxonomy_version"`
	Candidate       bool     `json:"candidate,omitempty"`
}

// UnmarshalJSON rejects a label with no confidence field. Without this, encoding/json would decode
// `{"pattern":"routing"}` into a label reading 0.0 — a confident "definitely not" — which is the
// exact silent failure this package exists to avoid.
func (l *Label) UnmarshalJSON(b []byte) error {
	// Lenient on unknown fields BY DESIGN: the P0 evolution contract makes forward compatibility a
	// PARSE contract — a consumer pinned to MAJOR n must ignore fields a later MINOR added, not
	// reject the document. Strictness here would break exactly the additive evolution P0 promised.
	var w labelWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	if w.Confidence == nil {
		return fmt.Errorf("pattern label %q: missing confidence (a label without a confidence is invalid)", w.Pattern)
	}
	*l = Label{
		Pattern: w.Pattern, Confidence: *w.Confidence, Source: w.Source,
		SubgraphRef: w.SubgraphRef, DetectorID: w.DetectorID, LLMRunRef: w.LLMRunRef,
		TaxonomyVersion: w.TaxonomyVersion, Candidate: w.Candidate,
	}
	return nil
}

// Validate is the single write-time gate every label passes before it can reach the IR — from the
// rule layer, from the LLM fallback, or from a decoded document. It is deliberately total: each
// rule below corresponds to a way a label could be meaningless to a downstream consumer.
func (l Label) Validate() error {
	if !InTaxonomy(l.Pattern) {
		return fmt.Errorf("pattern %q is not in the fixed %d-pattern taxonomy (version %s)", l.Pattern, TaxonomySize, TaxonomyVersion)
	}
	if math.IsNaN(l.Confidence) || l.Confidence < 0 || l.Confidence > 1 {
		return fmt.Errorf("pattern %q: confidence %v is not in [0,1]", l.Pattern, l.Confidence)
	}
	if l.SubgraphRef == "" {
		return fmt.Errorf("pattern %q: missing subgraph_ref (a label must name the region it applies to)", l.Pattern)
	}
	if l.TaxonomyVersion == "" {
		return fmt.Errorf("pattern %q: missing taxonomy_version", l.Pattern)
	}
	switch l.Source {
	case SourceRule:
		if l.DetectorID == "" {
			return fmt.Errorf("pattern %q: source=rule requires detector_id", l.Pattern)
		}
		if l.LLMRunRef != "" {
			return fmt.Errorf("pattern %q: source=rule must not carry llm_run_ref", l.Pattern)
		}
	case SourceLLM:
		if l.LLMRunRef == "" {
			return fmt.Errorf("pattern %q: source=llm requires llm_run_ref", l.Pattern)
		}
		if l.DetectorID != "" {
			return fmt.Errorf("pattern %q: source=llm must not carry detector_id", l.Pattern)
		}
	default:
		return fmt.Errorf("pattern %q: source %q is not one of rule|llm", l.Pattern, l.Source)
	}
	// The honesty boundary, mechanised. A behavioral pattern cannot be CONFIRMED from structure, so
	// no layer may assert one above the candidate cap, and it must be marked as a candidate.
	if IsBehavioral(l.Pattern) {
		if l.Confidence > BehavioralCandidateCap {
			return fmt.Errorf("pattern %q is behavioral: confidence %.2f exceeds the structural-candidate cap %.2f (confirmation is P5)",
				l.Pattern, l.Confidence, BehavioralCandidateCap)
		}
		if !l.Candidate {
			return fmt.Errorf("pattern %q is behavioral: label must be marked candidate (structure cannot confirm it)", l.Pattern)
		}
	} else if l.Candidate {
		return fmt.Errorf("pattern %q is structurally determined: it must not be marked candidate", l.Pattern)
	}
	return nil
}

// LabelLess is the total order labels are sorted by before they are written. Deterministic output is
// a hard requirement (the same IR must produce BYTE-IDENTICAL labels), and map iteration inside the
// detectors is not ordered, so ordering is imposed here rather than hoped for.
func LabelLess(a, b Label) bool {
	if a.SubgraphRef != b.SubgraphRef {
		return a.SubgraphRef < b.SubgraphRef
	}
	if a.Pattern != b.Pattern {
		return a.Pattern < b.Pattern
	}
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	return a.DetectorID < b.DetectorID
}
