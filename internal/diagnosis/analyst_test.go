package diagnosis

import (
	"context"
	"testing"

	"github.com/heros-foreal/agentd/internal/attribution"
)

// scriptedAnalyst is the stub LLM analyst. It answers per case from a script and counts its calls, so
// a test can assert the analyst was invoked on EXACTLY the residue and nothing more.
type scriptedAnalyst struct {
	responses map[string]AnalystResponse // case_id → response
	calls     []string                   // case_ids the analyst was asked about, in order
}

func (a *scriptedAnalyst) Analyze(_ context.Context, fc attribution.FailingCase, _ Rubric) (AnalystResponse, error) {
	a.calls = append(a.calls, fc.Case.CaseID)
	if r, ok := a.responses[fc.Case.CaseID]; ok {
		return r, nil
	}
	return AnalystResponse{Code: string(CauseNonConvergence), Confidence: 0.8}, nil
}

func testVariant() attribution.Variant {
	return attribution.Variant{VariantID: "v-diag", ConfigHash: "cfg", EvalSetHash: "es", WorkflowID: "wf-diag"}
}

// Task 6.1: the analyst is invoked only on the residue (rule-explained cases are excluded).
func TestAnalyze_CalledOnlyOnResidue(t *testing.T) {
	ir := diagIR()
	// Two rule-explained cases (prompt drift) + one residue case (no schema to violate under diagIR;
	// force residue by using a case whose node emits schema-valid output and no other signal).
	explained1 := promptDriftCase("e1")
	explained2 := promptDriftCase("e2")
	residue := cleanButFailingCase("r1") // schema-valid output, no structural signal → residue

	cases := []attribution.FailingCase{explained1, explained2, residue}
	analyst := &scriptedAnalyst{responses: map[string]AnalystResponse{
		"r1": {Code: string(CauseNonConvergence), Confidence: 0.8},
	}}

	diags, _, _, err := Diagnose(context.Background(), analyst, ir, testVariant(), cases, AnalystCalibration{}, 0)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(analyst.calls) != 1 || analyst.calls[0] != "r1" {
		t.Fatalf("analyst called on %v, want exactly [r1]", analyst.calls)
	}
	// The two explained cases must be diagnosed by rule, the residue by analyst.
	nRule, nAnalyst := 0, 0
	for _, d := range diags {
		switch d.Source {
		case SourceRule:
			nRule++
		case SourceAnalyst:
			nAnalyst++
		}
	}
	if nAnalyst != 1 {
		t.Errorf("analyst diagnoses = %d, want 1", nAnalyst)
	}
	if nRule < 2 {
		t.Errorf("rule diagnoses = %d, want ≥2", nRule)
	}
}

// Task 6.2: an off-taxonomy analyst response is rejected and never recorded as a diagnosis.
func TestAnalyze_RejectsOffTaxonomy(t *testing.T) {
	ir := diagIR()
	residue := cleanButFailingCase("r1")
	analyst := &scriptedAnalyst{responses: map[string]AnalystResponse{
		"r1": {Code: "it_just_felt_wrong", Confidence: 0.9}, // free-text, off-taxonomy
	}}
	diags, rejects, _, err := Diagnose(context.Background(), analyst, ir, testVariant(), []attribution.FailingCase{residue}, AnalystCalibration{}, 0)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	for _, d := range diags {
		if d.Source == SourceAnalyst {
			t.Fatalf("off-taxonomy analyst label was recorded as a diagnosis: %+v", d)
		}
	}
	if len(rejects) != 1 || rejects[0].RawLabel != "it_just_felt_wrong" {
		t.Fatalf("off-taxonomy label should be rejected+logged; got %+v", rejects)
	}
}

// Task 6.3 + 6.4: agreement is reported alongside every analyst diagnosis; a below-floor analyst is
// flagged.
func TestDiagnose_AgreementReportedAndBelowFloorFlagged(t *testing.T) {
	ir := diagIR()
	residue := cleanButFailingCase("r1")
	analyst := &scriptedAnalyst{responses: map[string]AnalystResponse{
		"r1": {Code: string(CauseNonConvergence), Confidence: 0.9},
	}}

	// A calibration that is below the floor.
	human := map[string]TaxonomyCode{"h1": CauseNonConvergence, "h2": CauseRetrievalMiss, "h3": CauseMisroute, "h4": CauseContextOverflow}
	analystLabels := map[string]TaxonomyCode{"h1": CauseNonConvergence, "h2": CauseContextOverflow, "h3": CauseContextOverflow, "h4": CauseContextOverflow}
	cal := Calibrate("diag_analyst", human, analystLabels, DefaultAnalystFloor)
	if !cal.BelowFloor() {
		t.Fatalf("expected below-floor calibration; got %+v", cal)
	}

	diags, _, _, err := Diagnose(context.Background(), analyst, ir, testVariant(), []attribution.FailingCase{residue}, cal, 0)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	var an *Diagnosis
	for i := range diags {
		if diags[i].Source == SourceAnalyst {
			an = &diags[i]
		}
	}
	if an == nil {
		t.Fatalf("no analyst diagnosis produced")
	}
	if an.NHuman != 4 {
		t.Errorf("n_human = %d, want 4", an.NHuman)
	}
	if an.Agreement != cal.Agreement {
		t.Errorf("agreement not reported alongside diagnosis: got %v want %v", an.Agreement, cal.Agreement)
	}
	if !an.AnalystFlagged {
		t.Errorf("below-floor analyst diagnosis must be flagged")
	}
}

// Low confidence is marked visibly.
func TestDiagnose_LowConfidenceMarked(t *testing.T) {
	ir := diagIR()
	residue := cleanButFailingCase("r1")
	analyst := &scriptedAnalyst{responses: map[string]AnalystResponse{
		"r1": {Code: string(CauseNonConvergence), Confidence: 0.2}, // below default 0.5
	}}
	diags, _, _, _ := Diagnose(context.Background(), analyst, ir, testVariant(), []attribution.FailingCase{residue}, AnalystCalibration{}, 0)
	found := false
	for _, d := range diags {
		if d.Source == SourceAnalyst {
			found = true
			if !d.LowConfidence {
				t.Errorf("confidence 0.2 diagnosis not marked low-confidence")
			}
		}
	}
	if !found {
		t.Fatalf("no analyst diagnosis produced")
	}
}

// Task 6.6: rule wins on conflict; the analyst disagreement is logged, not applied.
func TestResolve_RuleWinsOnConflict(t *testing.T) {
	rule := []TypedCause{{Code: CausePromptFormatDrift, NodeID: "node3", CaseID: "c1", Source: SourceRule, Confidence: 1}}
	analyst := []TypedCause{{Code: CauseContextOverflow, NodeID: "node3", CaseID: "c1", Source: SourceAnalyst, Confidence: 0.9}}
	kept, conflicts := Resolve(rule, analyst)
	if len(kept) != 1 || kept[0].Source != SourceRule {
		t.Fatalf("rule must win the conflict; kept = %+v", kept)
	}
	if len(conflicts) != 1 || conflicts[0].RuleCode != CausePromptFormatDrift || conflicts[0].AnalystCode != CauseContextOverflow {
		t.Fatalf("analyst disagreement must be logged; got %+v", conflicts)
	}
}

// Task 6.5: no apply path — Diagnose emits only reports. (Structural: there is no proposal type and no
// mutation argument. This test asserts the record is a pure report shape carrying evidence.)
func TestDiagnose_EmitsReportsWithEvidenceOnly(t *testing.T) {
	ir := diagIR()
	diags, _, _, _ := Diagnose(context.Background(), nil, ir, testVariant(), []attribution.FailingCase{promptDriftCase("c1")}, AnalystCalibration{}, 0)
	if len(diags) == 0 {
		t.Fatalf("expected at least one diagnosis")
	}
	for _, d := range diags {
		if len(d.EvidenceCaseIDs) == 0 {
			t.Errorf("diagnosis %s has no evidence — a diagnosis must never be a bare label", d.DiagID)
		}
		if d.TaxonomyVersion != TaxonomyVersion {
			t.Errorf("diagnosis missing frozen taxonomy version")
		}
	}
}
