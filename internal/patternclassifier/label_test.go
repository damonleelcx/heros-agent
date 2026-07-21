package patternclassifier

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func ruleLabel() Label {
	return Label{
		Pattern: Routing, Confidence: ConfidenceTopologyDetermined, Source: SourceRule,
		SubgraphRef: "sg_a", DetectorID: "routing.control_fanout.v1", TaxonomyVersion: TaxonomyVersion,
	}
}

func TestValidateAcceptsWellFormedLabels(t *testing.T) {
	if err := ruleLabel().Validate(); err != nil {
		t.Fatalf("valid rule label rejected: %v", err)
	}
	llm := Label{
		Pattern: GuardrailsSafety, Confidence: 0.5, Source: SourceLLM, SubgraphRef: "sg_b",
		LLMRunRef: "run_1", TaxonomyVersion: TaxonomyVersion, Candidate: true,
	}
	if err := llm.Validate(); err != nil {
		t.Fatalf("valid llm label rejected: %v", err)
	}
}

// Task 1.3 / FR: a label naming a pattern outside the closed vocabulary is rejected AT WRITE TIME,
// whichever layer offered it.
func TestValidateRejectsOutOfTaxonomy(t *testing.T) {
	for _, p := range []Pattern{"router", "self_healing_agent", "", "Routing"} {
		l := ruleLabel()
		l.Pattern = p
		err := l.Validate()
		if err == nil {
			t.Fatalf("pattern %q was accepted; the taxonomy must be closed", p)
		}
		if !strings.Contains(err.Error(), "not in the fixed") {
			t.Errorf("pattern %q: unclear rejection %v", p, err)
		}
	}
}

func TestValidateRejectsBadConfidence(t *testing.T) {
	for _, c := range []float64{-0.01, 1.01, math.NaN(), math.Inf(1)} {
		l := ruleLabel()
		l.Confidence = c
		if err := l.Validate(); err == nil {
			t.Errorf("confidence %v accepted; must be in [0,1]", c)
		}
	}
}

// A missing confidence must not decode to 0.0 — that would read as a confident "definitely not".
func TestUnmarshalRejectsMissingConfidence(t *testing.T) {
	var l Label
	err := json.Unmarshal([]byte(`{"pattern":"routing","source":"rule","subgraph_ref":"sg_a"}`), &l)
	if err == nil {
		t.Fatal("a label without a confidence must be invalid")
	}
	if !strings.Contains(err.Error(), "missing confidence") {
		t.Errorf("unclear error: %v", err)
	}
	// 0.0 explicitly present is legal and must survive the round trip.
	if err := json.Unmarshal([]byte(`{"pattern":"routing","confidence":0,"source":"rule","subgraph_ref":"sg_a","detector_id":"d","taxonomy_version":"1.0.0"}`), &l); err != nil {
		t.Fatalf("explicit confidence 0 rejected: %v", err)
	}
	if l.Confidence != 0 {
		t.Errorf("confidence = %v, want 0", l.Confidence)
	}
	if err := l.Validate(); err != nil {
		t.Errorf("explicit confidence 0 must be a valid label: %v", err)
	}
}

// Unknown fields are IGNORED, not rejected: forward compatibility is a parse contract (P0 NFR1).
func TestUnmarshalIgnoresUnknownFields(t *testing.T) {
	var l Label
	in := `{"pattern":"routing","confidence":0.9,"source":"rule","subgraph_ref":"sg_a","detector_id":"d","taxonomy_version":"1.0.0","future_field":{"x":1}}`
	if err := json.Unmarshal([]byte(in), &l); err != nil {
		t.Fatalf("a document from a later MINOR must still parse: %v", err)
	}
	if err := l.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestValidateRequiresSubgraphRefAndVersion(t *testing.T) {
	l := ruleLabel()
	l.SubgraphRef = ""
	if err := l.Validate(); err == nil {
		t.Error("a label with no subgraph_ref must be invalid")
	}
	l = ruleLabel()
	l.TaxonomyVersion = ""
	if err := l.Validate(); err == nil {
		t.Error("a label with no taxonomy_version must be invalid")
	}
}

func TestValidateEnforcesProvenance(t *testing.T) {
	cases := map[string]func(*Label){
		"rule without detector_id":  func(l *Label) { l.DetectorID = "" },
		"rule carrying llm_run_ref": func(l *Label) { l.LLMRunRef = "run_1" },
		"unknown source":            func(l *Label) { l.Source = "human" },
	}
	for name, mutate := range cases {
		l := ruleLabel()
		mutate(&l)
		if err := l.Validate(); err == nil {
			t.Errorf("%s: accepted, want rejected", name)
		}
	}
	llm := Label{Pattern: Routing, Confidence: 0.4, Source: SourceLLM, SubgraphRef: "sg", TaxonomyVersion: TaxonomyVersion}
	if err := llm.Validate(); err == nil {
		t.Error("source=llm without llm_run_ref: accepted, want rejected")
	}
}

// The honesty boundary is MECHANICAL: a behavioral pattern may not be asserted above the candidate
// cap by any layer, and must be marked as a candidate. This fence is what stops a future detector
// from quietly promoting Reflection to a confirmed fact before P5 traces exist.
func TestBehavioralPatternsAreCappedCandidates(t *testing.T) {
	over := Label{
		Pattern: Reflection, Confidence: BehavioralCandidateCap + 0.01, Source: SourceRule,
		SubgraphRef: "sg", DetectorID: "d", TaxonomyVersion: TaxonomyVersion, Candidate: true,
	}
	if err := over.Validate(); err == nil {
		t.Fatal("behavioral pattern above the candidate cap was accepted")
	}
	unmarked := over
	unmarked.Confidence = BehavioralCandidateCap
	unmarked.Candidate = false
	if err := unmarked.Validate(); err == nil {
		t.Fatal("behavioral pattern not marked candidate was accepted")
	}
	ok := over
	ok.Confidence = BehavioralCandidateCap
	if err := ok.Validate(); err != nil {
		t.Fatalf("behavioral candidate at the cap must be valid: %v", err)
	}
	// The converse: a structurally-determined pattern must NOT masquerade as a candidate.
	structural := ruleLabel()
	structural.Candidate = true
	if err := structural.Validate(); err == nil {
		t.Fatal("structural pattern marked candidate was accepted")
	}
}

func TestLabelLessIsTotalAndStable(t *testing.T) {
	a := Label{SubgraphRef: "sg_a", Pattern: Routing, Source: SourceRule, DetectorID: "x"}
	b := Label{SubgraphRef: "sg_a", Pattern: ToolUse, Source: SourceRule, DetectorID: "x"}
	c := Label{SubgraphRef: "sg_b", Pattern: Routing, Source: SourceRule, DetectorID: "x"}
	if !LabelLess(a, b) || LabelLess(b, a) {
		t.Error("pattern must break the tie within a subgraph")
	}
	if !LabelLess(b, c) || LabelLess(c, b) {
		t.Error("subgraph_ref must be the primary key")
	}
	if LabelLess(a, a) {
		t.Error("order must be irreflexive")
	}
}

func TestDiagnosticsAreRecordedNotSilent(t *testing.T) {
	var s diagSink
	bad := ruleLabel()
	bad.Pattern = "router"
	s.rejectLabel(bad, bad.Validate())
	got := s.sorted()
	if len(got) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(got))
	}
	if got[0].RawPattern != "router" || got[0].Stage != StageLabelWrite {
		t.Errorf("diagnostic must keep the rejected value verbatim: %+v", got[0])
	}
	if !strings.Contains(got[0].String(), "router") {
		t.Errorf("String() must name the rejected value: %s", got[0])
	}
}
