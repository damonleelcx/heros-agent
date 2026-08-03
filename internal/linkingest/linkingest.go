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
//
// 🔴 A NIL SINK IS REFUSED AT CONSTRUCTION, and that is deliberate. Ingest calls sink.Attribute and
// sink.Put on the FIRST link of a run only — the already-linked path returns before reaching them. So a
// nil sink produces a defect with a uniquely bad shape: every re-link of an existing run answers 409
// correctly, only a first link panics, and the panic is recovered into an opaque
// `{"code":"PLATFORM_PANIC","trace_id":...}` with nothing in the log. It reads as an intermittent
// server fault rather than a wiring mistake, and the boot log meanwhile says `served p11_run_linking`.
//
// Failing here turns that into an immediate, legible boot failure that names the missing dependency.
// A capability cannot be truthfully "served" without the substrate its ingest path requires.
func New(sink CostSink, links Store, consoleURL func(tenantID, runID string) string) *Ingester {
	if sink == nil {
		panic("linkingest.New: nil CostSink — the ingest path writes cost, latency and token events on " +
			"every first link, so a nil sink is not a degraded mode, it is a panic on the first real " +
			"request. Pass metering.NewMemCostEvents() or a durable substrate.")
	}
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
	//
	// A failure here fails the whole ingest rather than being tolerated: the denominator is what makes
	// the coverage figure a RATIO, and accepting a link while silently dropping it would leave the
	// tenant with a numerator, a stale denominator, and a coverage number that reads as better than it
	// is. Refusing the link is recoverable — the CLI retries — and a wrong coverage figure is not,
	// because nobody knows to go back and check it.
	if err := i.links.ObserveRunsReported(tenantID, p.RunsReported); err != nil {
		return Result{}, err
	}

	// Idempotency (FR14): a run already linked contributes exactly once. We do NOT re-emit its events.
	already, err := i.links.Record(LinkedRun{
		RunID: runID, TenantID: tenantID, WorkflowID: p.RunMetadata.WorkflowID,
		ConfigHash: p.ConfigHash, SourceRevision: p.SourceRevision,
		ToolVersion: p.RunMetadata.ToolVersion, LinkedAt: i.now().UTC(),
		// Scores are recorded AS COMPUTED (task 4.3): the console shows the developer's own numbers.
		Scores: scoresFrom(p.Scores),
		// The evidence behind those scores, recorded for the same reason and with the same rule: AS
		// REPORTED, never re-derived. The eval board's denominator and the gate verdict are facts about
		// what ran on the developer's machine, and the platform is not in a position to recompute either.
		Eval:    evalFrom(p.Eval),
		PerNode: perNodeFrom(p.Metrics.PerNode),
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
func (i *Ingester) Coverage(tenantID string) (LinkCoverage, error) { return i.links.Coverage(tenantID) }

// LinkedRun returns one linked run for a tenant, or ok=false when that run is not linked.
//
// # Why the read side needed a door at all
//
// The store has had `Get` since it was durable, and nothing called it. Everything a linked run knows —
// the scores the developer saw locally, the config hash, the revision — went in and could not come back
// out, so the console's run page reported **no such run** for a run the platform had accepted, stored,
// and answered `409 already_linked` for on a re-link. The failure was invisible from either end alone:
// ingest returned 200, the store held the row, and the only reader was a link-coverage COUNT.
//
// ok=false is NOT LINKED and nothing else; a read failure is the error. That distinction is the store's
// and it is preserved rather than flattened here, because "we could not read" and "you never linked it"
// send a user to different places.
func (i *Ingester) LinkedRun(tenantID, runID string) (LinkedRun, bool, error) {
	return i.links.Get(tenantID, runID)
}

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

// evalFrom projects the transmitted eval summary onto the stored one.
//
// 🔴 An unrecognised gate outcome becomes the ZERO VALUE, not a passed gate. The wire carries a string
// and a future or malformed client could send anything; mapping the unknown onto GatePass would let a
// bad client's run rank on the board as if it had cleared the customer's threshold. The zero value makes
// EvalEvidencePresent report false, and the console then says "no verdict recorded" — which is exactly
// what is true about a value we cannot read.
func evalFrom(w runlink.WireEval) runlink.EvalSummary {
	var outcome runlink.GateOutcome
	switch runlink.GateOutcome(w.GateOutcome) {
	case runlink.GatePass:
		outcome = runlink.GatePass
	case runlink.GateFail:
		outcome = runlink.GateFail
	case runlink.GateNotConfigured:
		outcome = runlink.GateNotConfigured
	}
	return runlink.EvalSummary{
		CaseCount:    w.CaseCount,
		SeedCount:    w.SeedCount,
		GateOutcome:  outcome,
		GateFailures: append([]string(nil), w.GateFailures...),
		SingleSeed:   w.SingleSeed,
	}
}

// perNodeFrom projects transmitted per-node metrics onto the stored shape, field by named field —
// the same discipline BuildPayload applies on the way out, applied on the way in.
func perNodeFrom(w map[string]runlink.WireNodeMetric) map[string]runlink.NodeMetric {
	if len(w) == 0 {
		return nil
	}
	out := make(map[string]runlink.NodeMetric, len(w))
	for id, m := range w {
		out[id] = runlink.NodeMetric{
			CostUSD: m.Cost, LatencyMS: m.Latency, TokensIn: m.TokensIn, TokensOut: m.TokensOut,
		}
	}
	return out
}
