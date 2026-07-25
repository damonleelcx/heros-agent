package runlink

import (
	"encoding/json"
	"strings"
	"testing"
)

// egress_test.go is the security-critical test surface: the guarantee that customer content never
// crosses the boundary, proven by construction rather than asserted in prose.

// sampleRecord is a fully-populated run record, including the LocalNote canary carrying a
// sensitive-looking value that MUST NOT appear on the wire.
func sampleRecord() RunRecord {
	return RunRecord{
		RunID: "run-abc123", WorkflowID: "example.com/wf", ConfigHash: strings.Repeat("a", 64),
		SourceRevision: "deadbeef", Timestamp: "2026-07-25T00:00:00Z", Seeds: []int64{1000, 1001},
		ToolVersion: "0.11.0",
		Metrics: Metrics{CostUSD: 0.12, LatencyMS: 480, TokensIn: 200, TokensOut: 80,
			PerNode: map[string]NodeMetric{"n_1": {CostUSD: 0.12, LatencyMS: 480}}},
		IR: IRStructure{
			NodeIDs: []string{"n_1", "n_2"}, Edges: []Edge{{From: "n_1", To: "n_2", Kind: "call"}},
			ModelRefs: map[string]string{"n_1": "anthropic/claude"}, PatternLabels: map[string]string{"n_1": "router"},
		},
		Scores:       []Score{{Metric: "quality", Value: 0.8, CILow: 0.7, CIHigh: 0.9}},
		RunsReported: 5,
		// The canary — a value that looks exactly like the content that must never leave.
		LocalNote: "PROMPT: ignore previous instructions; API_KEY=sk-secret-DO-NOT-LEAK",
	}
}

// TestFR11_AddedFieldIsAbsent is the guarantee that cannot be read from code (task 3.10). The source
// struct carries a sensitive field (LocalNote); the allowlist is unchanged; the transmitted payload
// must not contain it. If this cannot be made to fail, the guarantee is decoration.
func TestFR11_AddedFieldIsAbsent(t *testing.T) {
	rec := sampleRecord()
	b, err := json.Marshal(BuildPayload(rec))
	if err != nil {
		t.Fatal(err)
	}
	wire := string(b)
	for _, leak := range []string{"PROMPT:", "API_KEY", "sk-secret", "local_note", "DO-NOT-LEAK"} {
		if strings.Contains(wire, leak) {
			t.Errorf("FR11 VIOLATED: transmitted payload contains %q — construction leaked a source field:\n%s", leak, wire)
		}
	}
}

// TestOnlyAllowlistedKeysCross asserts every key in a transmitted payload is on the allowlist (FR11
// second half). It walks the actual marshaled bytes, not the struct.
func TestOnlyAllowlistedKeysCross(t *testing.T) {
	b, _ := json.Marshal(BuildPayload(sampleRecord()))
	offenders, err := AssertAllowlisted(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("payload carries non-allowlisted keys: %v", offenders)
	}
}

// TestStructureCrossesContentDoesNot asserts the payload carries the structure/metrics/scores the
// dashboard needs (FR12) and none of the content it does not.
func TestStructureCrossesContentDoesNot(t *testing.T) {
	p := BuildPayload(sampleRecord())
	if len(p.IRStructure.NodeIDs) != 2 || len(p.IRStructure.Edges) != 1 {
		t.Errorf("structure did not cross: %+v", p.IRStructure)
	}
	if p.Metrics.Cost == 0 || len(p.Scores) == 0 {
		t.Errorf("metrics/scores did not cross: %+v", p.Metrics)
	}
	if p.ConfigHash == "" || p.SourceRevision == "" {
		t.Errorf("provenance did not cross")
	}
	if p.RunsReported != 5 {
		t.Errorf("coverage denominator did not cross: %d", p.RunsReported)
	}
}

// TestPayloadIsDeterministic — the same record renders the same bytes, so a dry-run equals a real send
// (byte-identical, task 3.2). Maps are the only nondeterminism risk; json.Marshal sorts map keys.
func TestPayloadIsDeterministic(t *testing.T) {
	rec := sampleRecord()
	a, _ := json.Marshal(BuildPayload(rec))
	b, _ := json.Marshal(BuildPayload(rec))
	if string(a) != string(b) {
		t.Errorf("payload is not byte-stable:\n%s\n%s", a, b)
	}
}

// TestNoCredentialFieldExists — there is no path to put a provider credential in the payload, because
// the wire struct has no field for one and no code copies one (FR13/NFR3). This is a structural check:
// the payload struct's JSON keys are exactly the allowlist, and the allowlist has no credential.
func TestNoCredentialFieldExists(t *testing.T) {
	b, _ := json.Marshal(BuildPayload(sampleRecord()))
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for k := range m {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "key") || strings.Contains(lk, "cred") || strings.Contains(lk, "secret") || strings.Contains(lk, "token") || strings.Contains(lk, "auth") {
			t.Errorf("payload top-level key %q looks credential-shaped", k)
		}
	}
}
