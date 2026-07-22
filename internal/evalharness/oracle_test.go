package evalharness

import (
	"context"
	"encoding/json"
	"testing"
)

// REGRESSION FENCE for the defect a real repository supplied.
//
// P1 discovery over nousresearch/hermes-agent emits `{"type":"object"}` as the I/O contract for all
// 40 of its nodes — its syntactic frontend does not resolve types. That schema accepts EVERY object,
// so schema-validity returned 1 for every output of every variant; a variant answering wrong 70% of
// the time scored task_success 1.000 and passed the min-quality gate. The eval set's report card
// counted those cases as oracles because it asked "is one present?" rather than "can it say no?".
func TestUnconstrainedSchemaIsNotADecisiveOracle(t *testing.T) {
	for _, tc := range []struct {
		name     string
		c        Case
		decisive bool
	}{
		{
			// The exact contract hermes-agent's discovery emitted, 40 times.
			name:     "hermes-agent's unconstrained object contract",
			c:        Case{OutputSchema: json.RawMessage(`{"type":"object"}`)},
			decisive: false,
		},
		{
			name:     "empty schema accepts literally everything",
			c:        Case{OutputSchema: json.RawMessage(`{}`)},
			decisive: false,
		},
		{
			name:     "a schema with a required property can fail",
			c:        Case{OutputSchema: json.RawMessage(`{"type":"object","required":["a"]}`)},
			decisive: true,
		},
		{
			name:     "a schema constraining a property type can fail",
			c:        Case{OutputSchema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`)},
			decisive: true,
		},
		{
			name:     "a reference is decisive by construction",
			c:        Case{Reference: json.RawMessage(`{"a":"correct"}`)},
			decisive: true,
		},
		{
			name:     "a match-everything regex is not an oracle",
			c:        Case{Pattern: `.*`},
			decisive: false,
		},
		{
			name:     "a real regex can fail",
			c:        Case{Pattern: `^\{"a":"correct"\}$`},
			decisive: true,
		},
		{
			name:     "no oracle at all",
			c:        Case{},
			decisive: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.c.DecisiveOracle()
			if v.Decisive != tc.decisive {
				t.Fatalf("want decisive=%v, got %v (reason %q)", tc.decisive, v.Decisive, v.Reason)
			}
			if !v.Decisive && tc.c.HasOracle() && v.Reason == "" {
				t.Fatal("an indecisive oracle must explain itself")
			}
		})
	}
}

// The two predicates answer DIFFERENT questions and both are needed: presence drives the gold/weak
// label rule (a case carrying a schema is carrying an oracle, however weak, and must be labeled),
// while decisiveness drives what the report card counts as evidence.
func TestPresenceAndDecisivenessAreDifferentQuestions(t *testing.T) {
	c := Case{
		CaseID: "c-1", WorkflowID: "wf",
		Input:        json.RawMessage(`{"q":"x"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		Label:        LabelGold,
	}
	if !c.HasOracle() {
		t.Fatal("a case carrying a schema HAS an oracle, and the label rule depends on that")
	}
	if c.HasDecisiveOracle() {
		t.Fatal("an unconstrained schema is not evidence")
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("the case is still structurally valid: %v", err)
	}
}

// And the consequence the fence exists for: with hermes's contract, schema validity cannot separate
// a correct answer from a wrong one.
func TestUnconstrainedSchemaCannotSeparateRightFromWrong(t *testing.T) {
	e := NewJSONSchemaValidity()
	c := Case{
		CaseID: "c-1", WorkflowID: "wf",
		Input:        json.RawMessage(`{"q":"x"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		Label:        LabelGold,
	}
	scores := map[string]float64{}
	for _, out := range []string{`{"a":"correct"}`, `{"a":"wrong"}`} {
		tr := newTrace("r").node("n", 0, 1, 0, 0, false).output(out).build()
		got, err := e.Evaluate(context.Background(), tr, c, RunTarget())
		if err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		scores[out] = got
	}
	if scores[`{"a":"correct"}`] != scores[`{"a":"wrong"}`] {
		t.Fatal("fixture no longer reproduces the defect")
	}
	// The evaluator is behaving correctly — the SCHEMA is the problem, and the report card is what
	// has to notice. This is why the fix lives in the oracle probe, not in the evaluator.
	if !c.HasDecisiveOracle() {
		return
	}
	t.Fatal("a schema that scores a right and a wrong answer identically must not count as an oracle")
}
