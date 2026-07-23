// Package reportstore is the P4.5 read-only report store (§8). It persists the six append-only report
// kinds — attribution, failure_cluster, ablation_result, bottleneck_flag, diagnosis, analyst_cal —
// each keyed {variant_id, eval_set_hash, config_hash}, and NOTHING else.
//
// The read-only guarantee is STRUCTURAL, not a convention (Decision 1). This is enforced two ways:
//   - The Store interface has only report-write and report-read methods. There is no method that
//     writes a Variant Spec, a registry entry, or a node config, and no method that emits a proposal.
//     A caller therefore cannot mutate a config THROUGH this package even by mistake — the mutation is
//     inexpressible.
//   - Payloads (trace excerpts, analyst prompts/rubrics, cluster embeddings) are referenced by content
//     hash; the rows hold hashes, never inline user data (task 8.1), mirroring the P4 blob discipline.
//
// The in-memory store here is the reference implementation the load-bearing read-only test drives; the
// Postgres schema in db/migrations/postgres/0010_p45_attribution_diagnosis.up.sql is its durable twin,
// and the engine's DB grant against it is WRITE only on these tables, READ on traces + eval_result.
package reportstore

import (
	"context"
	"sort"
	"sync"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/diagnosis"
)

// ReportKey is the {variant_id, eval_set_hash, config_hash} triple every report row is keyed by. A
// report that cannot say which config it describes is not attributable, and config_hash on the key is
// also what makes the load-bearing test's "same config_hash" assertion a property of the data, not a
// hope.
type ReportKey struct {
	VariantID   string
	EvalSetHash string
	ConfigHash  string
}

// Store is the report persistence contract. Every method WRITES a report or READS one back. There is
// deliberately no WriteVariant, WriteConfig, PutProposal, or Apply — the interface cannot express a
// mutation to anything but its own append-only report tables.
type Store interface {
	PutAttribution(ctx context.Context, key ReportKey, rows []attribution.AttributionRow) error
	PutClusters(ctx context.Context, key ReportKey, clusters []attribution.FailureCluster) error
	PutAblation(ctx context.Context, key ReportKey, results []attribution.AblationResult) error
	PutBottleneckFlags(ctx context.Context, key ReportKey, flags []attribution.BottleneckFlag) error
	PutDiagnoses(ctx context.Context, key ReportKey, diags []diagnosis.Diagnosis) error
	PutAnalystCalibration(ctx context.Context, cal diagnosis.AnalystCalibration) error

	Attribution(ctx context.Context, key ReportKey) []attribution.AttributionRow
	Clusters(ctx context.Context, key ReportKey) []attribution.FailureCluster
	Ablation(ctx context.Context, key ReportKey) []attribution.AblationResult
	BottleneckFlags(ctx context.Context, key ReportKey) []attribution.BottleneckFlag
	Diagnoses(ctx context.Context, key ReportKey) []diagnosis.Diagnosis
	AnalystCalibration(ctx context.Context, metric string) (diagnosis.AnalystCalibration, bool)
}

// MemStore is the in-memory reference Store. Writes are append-only-idempotent: re-persisting the same
// content-keyed row overwrites with identical content, so a redelivered ablation or a re-run
// attribution never duplicates (the P2 idempotency discipline, applied to reports).
type MemStore struct {
	mu          sync.Mutex
	attribution map[ReportKey]map[string]attribution.AttributionRow // inner key: node|case
	clusters    map[ReportKey]map[string]attribution.FailureCluster // inner key: cluster_id
	ablation    map[ReportKey]map[string]attribution.AblationResult // inner key: node|swapped_ref|metric
	bottleneck  map[ReportKey]map[string]attribution.BottleneckFlag // inner key: node|dimension
	diagnoses   map[ReportKey]map[string]diagnosis.Diagnosis        // inner key: diag_id
	analystCal  map[string]diagnosis.AnalystCalibration             // key: analyst_metric
}

// NewMemStore builds an empty in-memory report store.
func NewMemStore() *MemStore {
	return &MemStore{
		attribution: map[ReportKey]map[string]attribution.AttributionRow{},
		clusters:    map[ReportKey]map[string]attribution.FailureCluster{},
		ablation:    map[ReportKey]map[string]attribution.AblationResult{},
		bottleneck:  map[ReportKey]map[string]attribution.BottleneckFlag{},
		diagnoses:   map[ReportKey]map[string]diagnosis.Diagnosis{},
		analystCal:  map[string]diagnosis.AnalystCalibration{},
	}
}

func (m *MemStore) PutAttribution(_ context.Context, key ReportKey, rows []attribution.AttributionRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.attribution[key] == nil {
		m.attribution[key] = map[string]attribution.AttributionRow{}
	}
	for _, r := range rows {
		m.attribution[key][r.NodeID+"\x00"+r.CaseID] = r
	}
	return nil
}

func (m *MemStore) PutClusters(_ context.Context, key ReportKey, clusters []attribution.FailureCluster) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.clusters[key] == nil {
		m.clusters[key] = map[string]attribution.FailureCluster{}
	}
	for _, c := range clusters {
		m.clusters[key][c.ClusterID] = c
	}
	return nil
}

func (m *MemStore) PutAblation(_ context.Context, key ReportKey, results []attribution.AblationResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ablation[key] == nil {
		m.ablation[key] = map[string]attribution.AblationResult{}
	}
	for _, r := range results {
		m.ablation[key][r.NodeID+"\x00"+r.SwappedConfigRef+"\x00"+r.Metric] = r
	}
	return nil
}

func (m *MemStore) PutBottleneckFlags(_ context.Context, key ReportKey, flags []attribution.BottleneckFlag) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bottleneck[key] == nil {
		m.bottleneck[key] = map[string]attribution.BottleneckFlag{}
	}
	for _, f := range flags {
		m.bottleneck[key][f.NodeID+"\x00"+string(f.Dimension)] = f
	}
	return nil
}

func (m *MemStore) PutDiagnoses(_ context.Context, key ReportKey, diags []diagnosis.Diagnosis) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.diagnoses[key] == nil {
		m.diagnoses[key] = map[string]diagnosis.Diagnosis{}
	}
	for _, d := range diags {
		m.diagnoses[key][d.DiagID] = d
	}
	return nil
}

func (m *MemStore) PutAnalystCalibration(_ context.Context, cal diagnosis.AnalystCalibration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.analystCal[cal.AnalystMetric] = cal
	return nil
}

func (m *MemStore) Attribution(_ context.Context, key ReportKey) []attribution.AttributionRow {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]attribution.AttributionRow, 0, len(m.attribution[key]))
	for _, r := range m.attribution[key] {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CaseID != out[j].CaseID {
			return out[i].CaseID < out[j].CaseID
		}
		return out[i].NodeID < out[j].NodeID
	})
	return out
}

func (m *MemStore) Clusters(_ context.Context, key ReportKey) []attribution.FailureCluster {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]attribution.FailureCluster, 0, len(m.clusters[key]))
	for _, c := range m.clusters[key] {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Signature < out[j].Signature
	})
	return out
}

func (m *MemStore) Ablation(_ context.Context, key ReportKey) []attribution.AblationResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]attribution.AblationResult, 0, len(m.ablation[key]))
	for _, r := range m.ablation[key] {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

func (m *MemStore) BottleneckFlags(_ context.Context, key ReportKey) []attribution.BottleneckFlag {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]attribution.BottleneckFlag, 0, len(m.bottleneck[key]))
	for _, f := range m.bottleneck[key] {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dimension != out[j].Dimension {
			return out[i].Dimension < out[j].Dimension
		}
		return out[i].NodeID < out[j].NodeID
	})
	return out
}

func (m *MemStore) Diagnoses(_ context.Context, key ReportKey) []diagnosis.Diagnosis {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]diagnosis.Diagnosis, 0, len(m.diagnoses[key]))
	for _, d := range m.diagnoses[key] {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DiagID < out[j].DiagID })
	return out
}

func (m *MemStore) AnalystCalibration(_ context.Context, metric string) (diagnosis.AnalystCalibration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.analystCal[metric]
	return c, ok
}

// compile-time assertion that MemStore satisfies the read-only Store contract.
var _ Store = (*MemStore)(nil)
