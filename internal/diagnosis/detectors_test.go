package diagnosis

import (
	"encoding/json"
	"testing"

	"github.com/heros-foreal/agentd/internal/attribution"
)

// Task 5.4 (first half): the prompt-format-drift detector names the faulty node's cause on every run
// of the same trace.
func TestDetect_PromptFormatDriftDeterministic(t *testing.T) {
	ir := diagIR()
	fc := promptDriftCase("c1")

	var first []TypedCause
	for run := 0; run < 5; run++ {
		got := Detect(ir, fc)
		var drift *TypedCause
		for i := range got {
			if got[i].Code == CausePromptFormatDrift {
				drift = &got[i]
			}
		}
		if drift == nil {
			t.Fatalf("run %d: prompt-format-drift not detected; got %+v", run, got)
		}
		if drift.NodeID != "node3" {
			t.Fatalf("run %d: prompt-format-drift node = %q, want node3", run, drift.NodeID)
		}
		if drift.Source != SourceRule || drift.Confidence != 1.0 {
			t.Fatalf("run %d: rule cause must be source=rule confidence=1.0; got %+v", run, *drift)
		}
		if len(drift.Evidence) == 0 || drift.Evidence[0] != "c1" {
			t.Fatalf("run %d: cause must carry its failing case as evidence; got %+v", run, drift.Evidence)
		}
		if run == 0 {
			first = got
		} else {
			a, _ := json.Marshal(first)
			b, _ := json.Marshal(got)
			if string(a) != string(b) {
				t.Fatalf("run %d: detection not deterministic:\n a=%s\n b=%s", run, a, b)
			}
		}
	}
}

// Task 5.4 (second half): a context-overflow fixture trips the overflow detector.
func TestDetect_ContextOverflowFires(t *testing.T) {
	ir := diagIR()
	got := Detect(ir, contextOverflowCase("c1"))
	var found *TypedCause
	for i := range got {
		if got[i].Code == CauseContextOverflow {
			found = &got[i]
		}
	}
	if found == nil {
		t.Fatalf("context-overflow not detected; got %+v", got)
	}
	if found.NodeID != "node3" {
		t.Errorf("overflow node = %q, want node3", found.NodeID)
	}
}

// Task 5.1: the tool-schema-mismatch detector fires on a repeated/schema tool error.
func TestDetect_ToolSchemaMismatchFires(t *testing.T) {
	ir := diagIR()
	got := Detect(ir, toolSchemaCase("c1"))
	found := false
	for _, c := range got {
		if c.Code == CauseToolSchemaMismatch && c.NodeID == "node3" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tool-schema-mismatch not detected; got %+v", got)
	}
}

// Task 5.3: rules run first on every failing case, and the residue is exactly the cases no rule
// explained.
func TestDetectSet_ResidueIsUnexplained(t *testing.T) {
	ir := diagIR()
	explained := promptDriftCase("explained")
	// A residue case: run it under an IR with no output contract so no rule fires.
	residCase := unexplainedCase("residue")

	// Mix them; DetectSet must classify each correctly under its own IR — here we run explained under
	// diagIR and check residue separately under noSchemaIR to isolate the rule surface.
	causes, residue := DetectSet(ir, []attribution.FailingCase{explained})
	if len(causes) == 0 {
		t.Fatalf("explained case produced no causes")
	}
	if len(residue) != 0 {
		t.Fatalf("explained case wrongly in residue: %+v", residue)
	}

	_, residue2 := DetectSet(noSchemaIR(), []attribution.FailingCase{residCase})
	if len(residue2) != 1 || residue2[0].Case.CaseID != "residue" {
		t.Fatalf("unexplained case should be the residue; got %+v", residue2)
	}
}
