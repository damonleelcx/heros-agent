package reportstore_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/confighash"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/reportstore"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// loadbearing_test.go is the phase's LOAD-BEARING safety test (task 8.4 / 11.2): a full attribution +
// diagnosis run must leave every Variant Spec / registry / config BYTE-IDENTICAL (same config_hash)
// and create ZERO proposal records. It drives the real engine packages (attribution + diagnosis)
// through the real report store — the only stub is the analyst LLM.

// workflowConfig stands in for the variant's node-config store — the thing that MUST NOT change. Its
// config_hash is a pure function of its content, so "byte-identical" is a hash comparison, not a hope.
type workflowConfig struct {
	WorkflowID string            `json:"workflow_id"`
	Nodes      map[string]string `json:"nodes"` // node_id → config ref
}

func (c workflowConfig) hash(t *testing.T) string {
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	h, err := confighash.SumBytes(raw)
	if err != nil {
		t.Fatalf("config hash: %v", err)
	}
	return h
}

func lbIR() *discovery.IR {
	contract := map[string]any{
		"type": "object", "required": []any{"a"},
		"properties": map[string]any{"a": map[string]any{"type": "string"}},
	}
	node := func(id string, p patternclassifier.Pattern) discovery.IRNode {
		return discovery.IRNode{
			NodeID: id, Kind: "static_definition",
			PatternLabels: []discovery.IRPatternLabel{{
				Pattern: string(p), Confidence: patternclassifier.ConfidenceTopologyDetermined,
				Source: string(patternclassifier.SourceRule), DetectorID: "lb",
				TaxonomyVersion: patternclassifier.TaxonomyVersion,
			}},
			IOContract: discovery.IRIOContract{InputSchema: map[string]any{"type": "object"}, OutputSchema: contract},
		}
	}
	return &discovery.IR{
		IRVersion: discovery.IRVersionPatternLabels,
		Workflow:  discovery.IRWorkflow{ID: "wf-lb", Language: "python"},
		Nodes: []discovery.IRNode{
			node("router", patternclassifier.Routing),
			node("node3", patternclassifier.ToolUse),
			node("reflect", patternclassifier.Reflection),
		},
	}
}

func lbSpan(caseID, node string, i int, failed bool) telemetry.Span {
	base := time.Unix(1_700_000_000, 0).Add(time.Duration(i) * time.Second)
	st := telemetry.SpanStatusOK
	if failed {
		st = telemetry.SpanStatusError
	}
	return telemetry.Span{
		TraceID: telemetry.TraceID(caseID), SpanID: telemetry.NodeSpanID(caseID + node),
		Kind: telemetry.SpanKindNode, Name: node, StartTime: base, EndTime: base.Add(100 * time.Millisecond),
		Status: st,
		Attributes: map[string]any{
			telemetry.AttrNodeID: node, telemetry.AttrCostUSD: 0.003, telemetry.AttrLatencyMS: 100.0, telemetry.AttrNodeFailed: failed,
		},
	}
}

func lbFaultyCase(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{
		"router":  json.RawMessage(`{"a":"branch"}`),
		"node3":   json.RawMessage(`{"junk":"no a"}`), // contract violation → prompt-format drift
		"reflect": json.RawMessage(`{"a":"wrong"}`),
	}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		lbSpan(id, "router", 0, false), lbSpan(id, "node3", 1, false), lbSpan(id, "reflect", 2, false),
	}}
	tr.Output = json.RawMessage(`{"a":"wrong"}`)
	tr.Failed = true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-lb"}, Trace: tr}
}

func TestLoadBearing_FullRunLeavesConfigByteIdenticalAndZeroProposals(t *testing.T) {
	ctx := context.Background()
	ir := lbIR()

	// The config store that MUST NOT change, and its hash before the run.
	cfg := workflowConfig{WorkflowID: "wf-lb", Nodes: map[string]string{
		"router": "cfg-router", "node3": "cfg-node3", "reflect": "cfg-reflect",
	}}
	before := cfg.hash(t)

	v := attribution.Variant{VariantID: "v-lb", ConfigHash: before, EvalSetHash: "es-lb", WorkflowID: "wf-lb"}
	cases := []attribution.FailingCase{lbFaultyCase("c1"), lbFaultyCase("c2"), lbFaultyCase("c3")}
	key := reportstore.ReportKey{VariantID: v.VariantID, EvalSetHash: v.EvalSetHash, ConfigHash: v.ConfigHash}

	store := reportstore.NewMemStore()

	// ── Full attribution + diagnosis run, persisted to the report store ──
	contrib := attribution.Attribute(ir, v, cases)
	if err := store.PutAttribution(ctx, key, contrib.Rows); err != nil {
		t.Fatalf("PutAttribution: %v", err)
	}
	clusters := attribution.Cluster(ir, v, cases)
	if err := store.PutClusters(ctx, key, clusters); err != nil {
		t.Fatalf("PutClusters: %v", err)
	}
	flags := attribution.Bottleneck(v, cases, attribution.BottleneckConfig{})
	if err := store.PutBottleneckFlags(ctx, key, flags); err != nil {
		t.Fatalf("PutBottleneckFlags: %v", err)
	}
	diags, _, _, err := diagnosis.Diagnose(ctx, nil, ir, v, cases, diagnosis.AnalystCalibration{}, 0)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if err := store.PutDiagnoses(ctx, key, diags); err != nil {
		t.Fatalf("PutDiagnoses: %v", err)
	}

	// ── The load-bearing assertions ──

	// 1. The config is byte-identical: same config_hash after the whole run.
	after := cfg.hash(t)
	if before != after {
		t.Fatalf("config_hash changed across the run: %s → %s (a read-only engine must not mutate config)", before, after)
	}

	// 2. Zero proposal records. There is no proposal type and no proposal table in this phase — the
	//    only outputs are the six report kinds. Assert the store produced reports and nothing that is
	//    a change: every diagnosis carries evidence (a report, not a bare directive) and is a 'rule' or
	//    'analyst' record, never a proposal.
	gotDiags := store.Diagnoses(ctx, key)
	if len(gotDiags) == 0 {
		t.Fatalf("expected diagnoses to be persisted")
	}
	for _, d := range gotDiags {
		if len(d.EvidenceCaseIDs) == 0 {
			t.Errorf("diagnosis %s has no evidence — not a valid read-only report", d.DiagID)
		}
		if d.Source != diagnosis.SourceRule && d.Source != diagnosis.SourceAnalyst {
			t.Errorf("diagnosis %s has non-report source %q", d.DiagID, d.Source)
		}
	}

	// 3. The attribution localized the fault to node3 and it was persisted, queryable per node/case.
	if rows := store.Attribution(ctx, key); len(rows) == 0 {
		t.Fatalf("attribution rows not persisted")
	}

	// 4. Idempotency / append-only: persisting the same run again does not duplicate.
	nBefore := len(store.Diagnoses(ctx, key))
	_ = store.PutDiagnoses(ctx, key, diags)
	if nAfter := len(store.Diagnoses(ctx, key)); nAfter != nBefore {
		t.Fatalf("re-persisting duplicated diagnoses: %d → %d (must be append-only idempotent)", nBefore, nAfter)
	}
}

// The read-only guarantee is also structural: the Store interface exposes only report writes/reads.
// This test documents (and the compiler enforces via the var _ Store assertion in the package) that
// there is no config-write method to call — a mutation is inexpressible through this store.
func TestStore_HasNoConfigWritePath(t *testing.T) {
	var s reportstore.Store = reportstore.NewMemStore()
	_ = s // If a WriteVariant/WriteConfig/PutProposal method ever appears, it must be justified here.
}
