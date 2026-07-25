// Package linkingest is the SERVER side of run linking: it receives an allowlist-constructed payload
// from the CLI, attributes it to the authenticated tenant SERVER-SIDE (never from the request body),
// makes it idempotent by run identity, and lands its cost/latency/token events in the EXISTING P2.5
// substrate with the standard tag set (PRD FR14/FR15, tasks 3.4–3.6). No second ingestion service, no
// second cost model — the events it emits are indistinguishable in kind from platform-executed ones, so
// SUM derives from them with the same DeriveSUM every other event uses.
package linkingest

import (
	"fmt"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// CostSink is the P2.5 substrate seen from the ingester: it accepts cost events and records the
// run→customer attribution. *metering.MemCostEvents satisfies it, which is the reference substrate SUM
// reads from — so a linked run and a hosted run share one store and one derivation.
type CostSink interface {
	Put(ev metricevent.Event)
	Attribute(runID, customerID string)
}

// Result is what an ingest produces, mirrored back to the CLI.
type Result struct {
	Accepted        bool
	AlreadyLinked   bool
	RunURL          string
	ContractVersion string
}

// Ingester ingests linked payloads. now and periodClosed are injected for testability.
type Ingester struct {
	sink   CostSink
	links  Store
	now    func() time.Time
	closed func(ts time.Time) bool
	// consoleURL builds the dashboard route for a linked run (FR18). Resolves to P9's canonical route.
	consoleURL func(tenantID, runID string) string
}

// New builds an ingester over a substrate and a link store.
func New(sink CostSink, links Store, consoleURL func(tenantID, runID string) string) *Ingester {
	return &Ingester{
		sink: sink, links: links,
		now:        time.Now,
		closed:     func(time.Time) bool { return false }, // open by default; a period service sets this
		consoleURL: consoleURL,
	}
}

// SetClock / SetPeriodClosed are test seams.
func (i *Ingester) SetClock(f func() time.Time)            { i.now = f }
func (i *Ingester) SetPeriodClosed(f func(time.Time) bool) { i.closed = f }

// Ingest attributes and lands a linked payload for tenantID. tenantID comes from the authenticated
// identity — a client-supplied tenant in the payload would be ignored, but the payload has no tenant
// field at all (the allowlist has none), which is the strongest form of "cannot widen scope" (FR/NFR11).
func (i *Ingester) Ingest(tenantID string, p runlink.Payload) (Result, error) {
	if tenantID == "" {
		return Result{}, fmt.Errorf("linkingest: no authenticated tenant")
	}
	if p.ContractVersion != runlink.ContractVersion {
		return Result{ContractVersion: runlink.ContractVersion},
			&ContractMismatch{Got: p.ContractVersion, Want: runlink.ContractVersion}
	}
	runID := p.RunMetadata.RunID
	if runID == "" || p.ConfigHash == "" {
		return Result{}, fmt.Errorf("linkingest: payload missing run_id or config_hash")
	}

	ts, err := time.Parse(time.RFC3339, p.RunMetadata.Timestamp)
	if err != nil {
		// Fall back to nano; a bad timestamp is a rejected payload, not a defaulted one.
		if ts, err = time.Parse(time.RFC3339Nano, p.RunMetadata.Timestamp); err != nil {
			return Result{}, fmt.Errorf("linkingest: run timestamp %q is not RFC3339: %w", p.RunMetadata.Timestamp, err)
		}
	}
	if i.closed(ts) {
		// Q6: a run linked after its period closed must not reopen a closed meter. Rejected distinctly.
		return Result{}, &ClosedPeriod{RunID: runID, Timestamp: ts}
	}

	// Always record the coverage denominator the CLI reported (§4 / task 1.7), even on a re-link.
	i.links.ObserveRunsReported(tenantID, p.RunsReported)

	// Idempotency (FR14): a run already linked contributes exactly once. We do NOT re-emit its events.
	already, err := i.links.Record(LinkedRun{
		RunID: runID, TenantID: tenantID, WorkflowID: p.RunMetadata.WorkflowID,
		ConfigHash: p.ConfigHash, SourceRevision: p.SourceRevision,
		ToolVersion: p.RunMetadata.ToolVersion, LinkedAt: i.now().UTC(),
		// Scores are recorded AS COMPUTED (task 4.3): the console shows the developer's own numbers.
		Scores: scoresFrom(p.Scores),
	})
	if err != nil {
		return Result{}, fmt.Errorf("linkingest: record linked run: %w", err)
	}
	if already {
		return Result{Accepted: true, AlreadyLinked: true, RunURL: i.route(tenantID, runID), ContractVersion: runlink.ContractVersion}, nil
	}

	// Land the events in P2.5. The invocation_id dimension is the run id, which is exactly the key
	// DeriveSUM de-dupes on — so even a duplicate emission cannot double-count.
	period := metering.MonthPeriod(ts).ID
	seed := int64(0)
	if len(p.RunMetadata.Seed) > 0 {
		seed = p.RunMetadata.Seed[0]
	}
	base := func(metric, unit string, value float64) metricevent.Event {
		v := value
		s := seed
		return metricevent.Event{
			SchemaVersion: metricevent.SchemaVersion,
			VariantID:     variantIDFor(p),
			RunID:         runID,
			NodeID:        telemetry.NodeIDRun,
			CaseID:        caseIDLinked,
			Seed:          &s,
			Timestamp:     ts.UTC().Format(time.RFC3339),
			ConfigHash:    p.ConfigHash,
			MetricName:    metric,
			Value:         &v,
			Unit:          unit,
			Dimensions: map[string]any{
				telemetry.AttrInvocationID:  runID,
				telemetry.AttrCustomerID:    tenantID,
				telemetry.AttrBillingPeriod: period,
			},
		}
	}

	// The cost event is the one SUM consumes. Latency and tokens ride the same substrate for the console.
	events := []metricevent.Event{
		base(telemetry.MetricCostUSD, telemetry.UnitUSD, p.Metrics.Cost),
		base(telemetry.MetricLatencyTotalMS, telemetry.UnitMS, p.Metrics.Latency),
		base(telemetry.MetricTokensPrompt, telemetry.UnitTokens, float64(p.Metrics.Tokens.In)),
		base(telemetry.MetricTokensCompletion, telemetry.UnitTokens, float64(p.Metrics.Tokens.Out)),
	}
	for _, ev := range events {
		if err := ev.Validate(); err != nil {
			return Result{}, fmt.Errorf("linkingest: constructed event failed the P2.5 boundary: %w", err)
		}
	}
	// Attribute FIRST so a concurrent SUM derivation cannot see an event before its owner is known.
	i.sink.Attribute(runID, tenantID)
	for _, ev := range events {
		i.sink.Put(ev)
	}

	return Result{Accepted: true, RunURL: i.route(tenantID, runID), ContractVersion: runlink.ContractVersion}, nil
}

// Coverage returns the link coverage for a tenant — the read model the console shows wherever a
// linked-derived spend figure appears (FR17).
func (i *Ingester) Coverage(tenantID string) LinkCoverage { return i.links.Coverage(tenantID) }

func (i *Ingester) route(tenantID, runID string) string {
	if i.consoleURL != nil {
		return i.consoleURL(tenantID, runID)
	}
	return runlink.PlatformBaseURL + "/app/runs/" + runID
}

const caseIDLinked = "__linked__"

// scoresFrom converts the wire scores to the recorded shape (they carry the same numbers).
func scoresFrom(ws []runlink.WireScore) []runlink.Score {
	if len(ws) == 0 {
		return nil
	}
	out := make([]runlink.Score, 0, len(ws))
	for _, w := range ws {
		out = append(out, runlink.Score(w)) // WireScore and Score are the same shape
	}
	return out
}

// variantIDFor derives a stable, non-empty variant id for a linked run. A linked run has no variant
// registry entry, so its identity is the workflow + config hash it was computed under.
func variantIDFor(p runlink.Payload) string {
	wf := p.RunMetadata.WorkflowID
	if wf == "" {
		wf = "workflow"
	}
	ch := p.ConfigHash
	if len(ch) > 12 {
		ch = ch[:12]
	}
	return "cli:" + sanitize(wf) + ":" + ch
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "workflow"
	}
	return s
}

// ContractMismatch is returned when the payload's contract version is not the one the platform speaks.
type ContractMismatch struct{ Got, Want string }

func (e *ContractMismatch) Error() string {
	return fmt.Sprintf("contract mismatch: payload declares %q, platform requires %q", e.Got, e.Want)
}

// ClosedPeriod is returned when a run would land in a closed billing period.
type ClosedPeriod struct {
	RunID     string
	Timestamp time.Time
}

func (e *ClosedPeriod) Error() string {
	return fmt.Sprintf("run %s falls in a closed period (%s) and cannot be linked", e.RunID, e.Timestamp.Format("2006-01"))
}
