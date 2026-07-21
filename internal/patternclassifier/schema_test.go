package patternclassifier

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// compileIRSchema loads the FROZEN workflow-ir schema from disk — the same file CI validates
// against. Validating against a schema copied into the test would prove only that the copy agrees
// with itself.
func compileIRSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	path, err := filepath.Abs("../../schemas/workflow-ir.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the frozen IR schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("workflow-ir.schema.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("workflow-ir.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validateIR(t *testing.T, s *jsonschema.Schema, ir any) error {
	t.Helper()
	blob, err := json.Marshal(ir)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(blob))
	if err != nil {
		t.Fatal(err)
	}
	return s.Validate(doc)
}

// Task 6.1 / 8.7, in its strongest form: a REAL classification's output is validated against the
// REAL frozen schema — labelled and unlabelled, same document, same MAJOR. This is what makes the
// additive claim a proof rather than a description.
func TestClassifiedIRValidatesAgainstTheFrozenSchema(t *testing.T) {
	s := compileIRSchema(t)
	ir, res := classifyComposite(t)

	if err := validateIR(t, s, ir); err != nil {
		t.Fatalf("the UNLABELLED IR does not validate: %v", err)
	}
	labelled, err := WriteBack(ir, res)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateIR(t, s, labelled); err != nil {
		t.Fatalf("the LABELLED IR does not validate against the same schema: %v", err)
	}
	if majorOf(labelled.IRVersion) != majorOf(ir.IRVersion) {
		t.Fatalf("MAJOR changed: %q -> %q", ir.IRVersion, labelled.IRVersion)
	}
	// And with an llm-sourced label too — that path writes llm_run_ref instead of detector_id.
	m := &stubModel{reply: []RawLabel{{Pattern: string(GuardrailsSafety), Confidence: conf(0.5)}}}
	f := fxAmbiguous()
	llmRes, err := Classify(t.Context(), f.ir, fallbackOpts(f, m))
	if err != nil {
		t.Fatal(err)
	}
	llmIR, err := WriteBack(f.ir, llmRes)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateIR(t, s, llmIR); err != nil {
		t.Fatalf("an llm-labelled IR does not validate: %v", err)
	}
}

// The schema gate must be able to REJECT. If the new optional properties had been added as
// additionalProperties:true, everything would "validate" and the gate would be decorative.
func TestSchemaRejectsAMalformedLabel(t *testing.T) {
	s := compileIRSchema(t)
	ir, res := classifyComposite(t)
	labelled, err := WriteBack(ir, res)
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(labelled)
	var doc map[string]any
	if err := json.Unmarshal(blob, &doc); err != nil {
		t.Fatal(err)
	}
	sgs := doc["subgraphs"].([]any)
	label := sgs[0].(map[string]any)["pattern_labels"].([]any)[0].(map[string]any)

	for name, mutate := range map[string]func(){
		"unknown property":      func() { label["totally_new_field"] = 1 },
		"source outside enum":   func() { label["source"] = "human" },
		"confidence above one":  func() { label["confidence"] = 1.5 },
		"confidence not number": func() { label["confidence"] = "high" },
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := map[string]any{}
			for k, v := range label {
				snapshot[k] = v
			}
			mutate()
			if err := validateIR(t, s, doc); err == nil {
				t.Errorf("the schema accepted a label with a %s", name)
			}
			for k := range label {
				delete(label, k)
			}
			for k, v := range snapshot {
				label[k] = v
			}
		})
	}
}
