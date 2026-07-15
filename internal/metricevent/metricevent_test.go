package metricevent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func i64(v int64) *int64     { return &v }
func f64(v float64) *float64 { return &v }

const goodHash = "3a7b9c1d2e4f60718293a4b5c6d7e8f90112233445566778899aabbccddeeff0"

func validEvent() Event {
	return Event{
		SchemaVersion: SchemaVersion,
		VariantID:     "v3",
		RunID:         "run_01",
		NodeID:        "classify@f.py:1",
		CaseID:        "case_00042",
		Seed:          i64(0), // 0 is a LEGITIMATE seed, must not read as absent
		Timestamp:     "2026-07-15T14:03:11.482Z",
		ConfigHash:    goodHash,
		MetricName:    "llm.latency.total_ms",
		Value:         f64(812.4),
		Unit:          "ms",
	}
}

func TestValidEventPasses(t *testing.T) {
	if err := validEvent().Validate(); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
}

func TestSeedZeroIsNotTreatedAsMissing(t *testing.T) {
	e := validEvent()
	e.Seed = i64(0)
	if err := e.Validate(); err != nil {
		t.Fatalf("seed=0 must be valid, got: %v", err)
	}
	// but a nil seed pointer IS missing
	e.Seed = nil
	if err := e.Validate(); err == nil {
		t.Fatal("nil seed must be rejected")
	}
}

func TestEachMissingTagIsRejected(t *testing.T) {
	// Mutate one tag at a time to its zero/absent form and assert rejection naming that tag.
	cases := map[string]func(*Event){
		"variant_id":  func(e *Event) { e.VariantID = "" },
		"run_id":      func(e *Event) { e.RunID = "  " }, // whitespace-only is still missing
		"node_id":     func(e *Event) { e.NodeID = "" },
		"case_id":     func(e *Event) { e.CaseID = "" },
		"seed":        func(e *Event) { e.Seed = nil },
		"timestamp":   func(e *Event) { e.Timestamp = "" },
		"config_hash": func(e *Event) { e.ConfigHash = "" },
	}
	for tag, mut := range cases {
		e := validEvent()
		mut(&e)
		err := e.Validate()
		if err == nil {
			t.Fatalf("missing %s: expected rejection, got nil", tag)
		}
		re, ok := AsRejection(err)
		if !ok {
			t.Fatalf("missing %s: expected *RejectionError, got %T", tag, err)
		}
		if !strings.Contains(strings.Join(re.Problems, "; "), tag) {
			t.Fatalf("missing %s: rejection did not name the tag: %v", tag, re.Problems)
		}
	}
}

func TestPayloadRequired(t *testing.T) {
	for _, mut := range []struct {
		name string
		f    func(*Event)
	}{
		{"metric_name", func(e *Event) { e.MetricName = "" }},
		{"value", func(e *Event) { e.Value = nil }},
		{"unit", func(e *Event) { e.Unit = "" }},
	} {
		e := validEvent()
		mut.f(&e)
		if err := e.Validate(); err == nil {
			t.Fatalf("missing payload %s must be rejected", mut.name)
		}
	}
}

func TestBadFormatsRejected(t *testing.T) {
	e := validEvent()
	e.ConfigHash = "NOTHEX"
	if err := e.Validate(); err == nil {
		t.Fatal("non-hex config_hash must be rejected")
	}
	e = validEvent()
	e.Timestamp = "yesterday"
	if err := e.Validate(); err == nil {
		t.Fatal("non-RFC3339 timestamp must be rejected")
	}
}

func TestValidateMapReportsAllProblemsAtOnce(t *testing.T) {
	m := map[string]any{
		// omit run_id and seed; null node_id; empty case_id
		"variant_id":  "v3",
		"node_id":     nil,
		"case_id":     "",
		"timestamp":   "2026-07-15T14:03:11Z",
		"config_hash": goodHash,
		"metric_name": "m",
		"value":       1.0,
		"unit":        "ms",
	}
	err := ValidateMap(m)
	if err == nil {
		t.Fatal("under-tagged map must be rejected")
	}
	re, _ := AsRejection(err)
	joined := strings.Join(re.Problems, "; ")
	for _, want := range []string{"run_id", "seed", "node_id", "case_id"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected problem for %s, got: %v", want, re.Problems)
		}
	}
}

// The valid fixture the schema-validation job uses MUST also pass the Go boundary — the two layers
// agree on what a good event looks like.
func TestSchemaValidFixturePassesBoundary(t *testing.T) {
	path := filepath.Join("..", "..", "schemas", "samples", "metric-event.valid.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture not found (%v)", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("fixture not JSON: %v", err)
	}
	if err := ValidateMap(m); err != nil {
		t.Fatalf("schema-valid fixture rejected by boundary: %v", err)
	}
}

// The negative fixture (missing run_id) the schema rejects MUST also be rejected by the boundary.
func TestSchemaInvalidFixtureRejectedByBoundary(t *testing.T) {
	path := filepath.Join("..", "..", "schemas", "samples", "metric-event.invalid-missing-tag.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture not found (%v)", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("fixture not JSON: %v", err)
	}
	if err := ValidateMap(m); err == nil {
		t.Fatal("boundary accepted an event the schema rejects (missing run_id)")
	}
}
