package diagnosis

import (
	"encoding/json"
	"sort"
	"testing"
)

// freeze_test.go pins the FROZEN diagnosis record schema (task 8.3 / design Q7): the exact contract
// P5.5's change operators consume. If a field name here changes, the P5.5 handoff changes with it —
// this test is the tripwire that makes that a deliberate, reviewed act rather than an accident.
func TestDiagnosisRecord_FrozenSchema(t *testing.T) {
	d := Diagnosis{
		DiagID: "id", VariantID: "v", EvalSetHash: "es", ConfigHash: "cfg", NodeID: "n",
		TaxonomyCode: CausePromptFormatDrift, TaxonomyVersion: TaxonomyVersion, Source: SourceRule,
		Confidence: 1.0, EvidenceCaseIDs: []string{"c1"},
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := make([]string, 0, len(m))
	for k := range m {
		got = append(got, k)
	}
	sort.Strings(got)

	want := []string{
		"agreement", "analyst_flagged", "calibrated", "confidence", "config_hash", "diag_id",
		"eval_set_hash", "evidence_case_ids", "low_confidence", "n_human", "node_id", "source",
		"taxonomy_code", "taxonomy_version", "variant_id",
	}
	if len(got) != len(want) {
		t.Fatalf("frozen diagnosis schema drifted:\n got  %v\n want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frozen diagnosis schema drifted at %d:\n got  %v\n want %v", i, got, want)
		}
	}

	// The record must carry the load-bearing contract fields for the P5.5 operator map: a code, a
	// node, evidence, a confidence, and an agreement.
	for _, required := range []string{"taxonomy_code", "node_id", "evidence_case_ids", "confidence", "agreement"} {
		if _, ok := m[required]; !ok {
			t.Errorf("frozen diagnosis record missing required field %q", required)
		}
	}
}

// The taxonomy itself is versioned and closed — freeze its identity so a code cannot silently change
// meaning under P5.5's operators.
func TestTaxonomy_VersionedAndClosed(t *testing.T) {
	if TaxonomyVersion == "" {
		t.Fatal("taxonomy must be versioned")
	}
	codes := Codes()
	if len(codes) == 0 {
		t.Fatal("taxonomy must be non-empty")
	}
	for _, c := range codes {
		if !ValidCode(c) {
			t.Errorf("Codes() returned a code ValidCode rejects: %q", c)
		}
	}
	if ValidCode("not_a_real_cause") {
		t.Error("ValidCode must reject an off-taxonomy code")
	}
}
