package patternclassifier

import (
	"fmt"
	"sort"
)

// DiagStage names where a diagnostic was raised. Kept coarse: a diagnostic answers "which layer
// produced something unusable", and the message carries the detail.
type DiagStage string

const (
	// StageLabelWrite: a label was rejected by Label.Validate before reaching the IR.
	StageLabelWrite DiagStage = "label_write"
	// StageLLMFallback: the constrained LLM returned something outside the contract.
	StageLLMFallback DiagStage = "llm_fallback"
	// StagePartition: the IR could not be partitioned as expected (dangling edge, empty graph).
	StagePartition DiagStage = "partition"
)

// Diagnostic is a recorded rejection. Rejections are NEVER silent: dropping an out-of-taxonomy LLM
// output without a trace is how a classifier comes to look healthy while classifying nothing —
// "failures must be visible, not silent". The classifier returns its diagnostics alongside labels
// so a caller can surface them (and so a test can assert a rejection actually happened).
type Diagnostic struct {
	Stage DiagStage `json:"stage"`
	// SubgraphRef is the region the rejected label claimed, when known.
	SubgraphRef string `json:"subgraph_ref,omitempty"`
	// RawPattern is the pattern string as offered, INCLUDING an out-of-taxonomy or free-text one.
	// It is kept verbatim: the value that was rejected is the whole point of the record.
	RawPattern string `json:"raw_pattern,omitempty"`
	Source     Source `json:"source,omitempty"`
	Reason     string `json:"reason"`
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("[%s] subgraph=%q pattern=%q source=%q: %s", d.Stage, d.SubgraphRef, d.RawPattern, d.Source, d.Reason)
}

// diagSink collects diagnostics during one classification. Not exported: callers receive the sorted
// slice on Result, so there is one way for a diagnostic to escape and it is deterministic.
type diagSink struct{ items []Diagnostic }

func (s *diagSink) add(d Diagnostic) { s.items = append(s.items, d) }

// rejectLabel is the ONE place a label is turned away. Both the rule layer and the LLM fallback call
// it, so "reject + record" cannot be implemented twice and drift.
func (s *diagSink) rejectLabel(l Label, err error) {
	s.add(Diagnostic{
		Stage: StageLabelWrite, SubgraphRef: l.SubgraphRef, RawPattern: string(l.Pattern),
		Source: l.Source, Reason: err.Error(),
	})
}

func (s *diagSink) sorted() []Diagnostic {
	out := append([]Diagnostic(nil), s.items...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Stage != out[j].Stage {
			return out[i].Stage < out[j].Stage
		}
		if out[i].SubgraphRef != out[j].SubgraphRef {
			return out[i].SubgraphRef < out[j].SubgraphRef
		}
		if out[i].RawPattern != out[j].RawPattern {
			return out[i].RawPattern < out[j].RawPattern
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}
