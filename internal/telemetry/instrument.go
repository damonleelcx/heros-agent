package telemetry

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/providercall"
)

// Instrument is the substrate's attach point at the provider gateway (Decision 1). It implements
// providercall.Observer — which IS providergateway.Observer, an alias of the same interface — so wiring it
// is still `gateway.New(secrets, providergateway.WithObserver(inst))`
// — one line at deploy time, ZERO lines in any workflow. From then on every Gateway.Complete emits the
// full per-call operational taxonomy and a node span, and there is no execution path through the
// Runtime that produces an un-instrumented provider call (the Requirement's second scenario).
type Instrument struct {
	sink   Sink
	prices *PriceBook
	log    Logger
	// now is injected so latency/throughput are testable without wall-clock flakiness.
	now func() time.Time
}

// Options configure an Instrument.
type Option func(*Instrument)

// WithLogger sets where dropped-event and pricing-gap warnings go.
func WithLogger(l Logger) Option { return func(i *Instrument) { i.log = l } }

// NewInstrument builds the gateway instrument over a Sink and a pinned price source.
func NewInstrument(sink Sink, prices *PriceBook, opts ...Option) (*Instrument, error) {
	if sink == nil {
		return nil, errors.New("telemetry: NewInstrument requires a Sink")
	}
	if prices == nil {
		return nil, errors.New("telemetry: NewInstrument requires a PriceBook so cost is attributable")
	}
	i := &Instrument{sink: sink, prices: prices, log: nopLogger{}, now: time.Now}
	for _, o := range opts {
		o(i)
	}
	return i, nil
}

// OnCall is the gateway hook, fired exactly once per logical provider call (after all retries). It is
// the whole of "operational metrics with zero user code": everything below is derived from what the
// gateway already knows (CallInfo) and the run context the run path already attached (RunContext).
func (i *Instrument) OnCall(ctx context.Context, info providercall.CallInfo) {
	rc, ok := FromContext(ctx)
	if !ok {
		// A provider call with no run context is not attributable to the seven tags. Emitting
		// under-tagged events would only be rejected at the gate; log the gap loudly instead. In a real
		// run the path always attaches the context, so this fires only for calls outside a run.
		i.log.Warnf("telemetry: provider call to %s/%s has no run context; operational metrics not attributable",
			info.Provider, info.ModelID)
		return
	}
	now := i.now()

	d := callDetail{
		provider:       info.Provider,
		modelID:        info.ModelID,
		modelVersionID: info.ModelVersionID,
		usage: tokenUsage{
			input:      info.Usage.InputTokens,
			output:     info.Usage.OutputTokens,
			thinking:   info.Usage.ThinkingTokens,
			cacheRead:  info.Usage.CacheReadTokens,
			cacheWrite: info.Usage.CacheWriteTokens,
		},
		duration:       info.Duration,
		attempts:       info.Attempts,
		rateLimited:    info.RateLimited,
		isError:        info.Err != nil,
		isTimeout:      errors.Is(info.Err, providercall.ErrTimeout),
		idempotencyKey: rc.IdempotencyKey(),
	}

	events, gaps := MetricSet(rc, d, now, i.prices)
	for _, ev := range events {
		safeMetric(ctx, i.sink, i.log, ev)
	}
	for _, g := range gaps {
		i.log.Warnf("telemetry: metric %q not emitted for %s/%s (unpriced model or unknown context window)",
			g, info.Provider, info.ModelID)
	}

	// The node span. Emitted here (not by the run tracer) because OnCall is where the provider-call
	// timing and gen_ai usage live; the run tracer supplies only the deterministic parent linkage, which
	// needs no shared handle (span ids are derived from coordinates, span.go).
	safeSpan(ctx, i.sink, i.log, i.nodeSpan(rc, d, info, now))
}

// nodeSpan builds one node execution's span under the run span, following the GenAI conventions.
func (i *Instrument) nodeSpan(rc RunContext, d callDetail, info providercall.CallInfo, now time.Time) Span {
	status, msg := SpanStatusOK, ""
	if info.Err != nil {
		status, msg = SpanStatusError, info.Err.Error() // already scrubbed by the gateway
	}
	cost, _, _ := i.prices.costUSD(info.Provider, info.ModelID, d.usage)
	idem := rc.IdempotencyKey()
	attrs := map[string]any{
		// GenAI semantic conventions — the shape a GenAI-aware backend already understands.
		AttrGenAISystem:         info.Provider,
		AttrGenAIOperationName:  GenAIOperationChat,
		AttrGenAIRequestModel:   info.ModelID,
		AttrGenAIResponseModel:  info.ModelID,
		AttrGenAIModelVersionID: info.ModelVersionID,
		AttrGenAIUsageInput:     d.usage.input,
		AttrGenAIUsageOutput:    d.usage.output,
		AttrGenAIFinishReasons:  string(info.StopReason),
		// The seven tags as OTel attributes (Requirement: "the seven tags SHALL be carried as OTel
		// attributes"). node_id/case_id/run_id ride here as span attributes — high-card lives on the
		// span, never as a TSDB label (Decision 4).
		AttrVariantID:  rc.VariantID,
		AttrRunID:      rc.RunID,
		AttrNodeID:     rc.NodeID,
		AttrCaseID:     rc.CaseID,
		AttrSeed:       rc.Seed,
		AttrConfigHash: rc.ConfigHash,
		// High-cardinality identifiers that are span attributes precisely so they are not labels.
		AttrInvocationID: idem,
		AttrAttemptGroup: rc.AttemptGroup,
		// Per-run drill-down conveniences for the live monitor (§8), read from this one store.
		AttrCostUSD:    cost,
		AttrLatencyMS:  ms(d.duration),
		AttrTimedOut:   d.isTimeout,
		AttrNodeFailed: info.Err != nil,
	}
	return Span{
		TraceID:      TraceID(rc.RunID),
		SpanID:       NodeSpanID(idem),
		ParentSpanID: RunSpanID(rc.RunID),
		Name:         GenAIOperationChat + " " + info.ModelID, // GenAI span-name convention
		Kind:         SpanKindNode,
		StartTime:    now.Add(-d.duration),
		EndTime:      now,
		Attributes:   attrs,
		Status:       status,
		StatusMsg:    msg,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// RunTracer — the run span bracket + run-scoped throughput/concurrency (task 1.6).
//
// The run path (executor / run driver) opens a tracer for the run, brackets each node's provider call
// with NodeStarted/NodeFinished, and marks tool calls. This is SUBSTRATE code: a workflow author never
// touches it. It owns what the per-call Observer cannot see — the run span, the live count of in-flight
// calls, and the run's call rate.
// ─────────────────────────────────────────────────────────────────────────────
type RunTracer struct {
	inst    *Instrument
	rc      RunContext // run-scoped tags (NodeID empty)
	started time.Time

	mu        sync.Mutex
	inFlight  int
	completed int
	ended     bool
}

// StartRun opens a run tracer. The run span itself is emitted on EndRun (with its end time), so a
// consumer that reads the span store sees complete spans; the live monitor (§8) reads in-progress
// state from the run record, not from a half-open span.
func (i *Instrument) StartRun(rc RunContext) *RunTracer {
	return &RunTracer{inst: i, rc: rc, started: i.now()}
}

// NodeStarted marks a node's provider call beginning. It increments the in-flight gauge and emits the
// current concurrency as a run-scoped metric so the live view can render it as calls arrive.
func (t *RunTracer) NodeStarted(ctx context.Context, nodeID string) {
	t.mu.Lock()
	t.inFlight++
	inFlight := t.inFlight
	t.mu.Unlock()
	t.emitRunScoped(ctx, MetricCallsInFlight, float64(inFlight), UnitCalls)
}

// NodeFinished marks a node's provider call ending. It decrements in-flight, counts a completed call,
// and emits the run's call rate.
func (t *RunTracer) NodeFinished(ctx context.Context, nodeID string) {
	t.mu.Lock()
	if t.inFlight > 0 {
		t.inFlight--
	}
	t.completed++
	inFlight, completed := t.inFlight, t.completed
	t.mu.Unlock()
	t.emitRunScoped(ctx, MetricCallsInFlight, float64(inFlight), UnitCalls)
	t.emitRunScoped(ctx, MetricCallsPerSec, t.rate(completed), UnitCallsPS)
}

// ToolCall emits a tool-call child span under a node (Requirement: "tool calls as child spans"). index
// distinguishes repeated calls to the same tool within one node; ok reports call success.
func (t *RunTracer) ToolCall(ctx context.Context, node RunContext, toolName string, index int, start, end time.Time, ok bool) {
	idem := node.IdempotencyKey()
	status := SpanStatusOK
	if !ok {
		status = SpanStatusError
	}
	sp := Span{
		TraceID:      TraceID(node.RunID),
		SpanID:       ToolSpanID(idem, toolName, index),
		ParentSpanID: NodeSpanID(idem),
		Name:         "tool " + toolName,
		Kind:         SpanKindTool,
		StartTime:    start,
		EndTime:      end,
		Attributes: map[string]any{
			AttrToolName:     toolName,
			AttrVariantID:    node.VariantID,
			AttrRunID:        node.RunID,
			AttrNodeID:       node.NodeID,
			AttrCaseID:       node.CaseID,
			AttrSeed:         node.Seed,
			AttrConfigHash:   node.ConfigHash,
			AttrInvocationID: idem,
		},
		Status: status,
	}
	safeSpan(ctx, t.inst.sink, t.inst.log, sp)
}

// EndRun closes the run span and emits the final call rate. Idempotent: a second EndRun is a no-op, so
// a defer plus an explicit call cannot double-emit.
func (t *RunTracer) EndRun(ctx context.Context) {
	t.mu.Lock()
	if t.ended {
		t.mu.Unlock()
		return
	}
	t.ended = true
	completed := t.completed
	t.mu.Unlock()

	now := t.inst.now()
	sp := Span{
		TraceID:   TraceID(t.rc.RunID),
		SpanID:    RunSpanID(t.rc.RunID),
		Name:      "run " + t.rc.RunID,
		Kind:      SpanKindRun,
		StartTime: t.started,
		EndTime:   now,
		Attributes: map[string]any{
			AttrVariantID:  t.rc.VariantID,
			AttrRunID:      t.rc.RunID,
			AttrCaseID:     t.rc.CaseID,
			AttrSeed:       t.rc.Seed,
			AttrConfigHash: t.rc.ConfigHash,
		},
		Status: SpanStatusOK,
	}
	safeSpan(ctx, t.inst.sink, t.inst.log, sp)
	t.emitRunScoped(ctx, MetricCallsPerSec, t.rate(completed), UnitCallsPS)
}

// emitRunScoped emits a throughput/concurrency metric with the run's tags and the run-scope node
// sentinel, so it stays fully tagged without pretending to belong to a single node.
func (t *RunTracer) emitRunScoped(ctx context.Context, name string, value float64, unit string) {
	rc := t.rc
	rc.NodeID = NodeIDRun
	ev := rc.event(name, value, unit, t.inst.now(), nil)
	safeMetric(ctx, t.inst.sink, t.inst.log, ev)
}

func (t *RunTracer) rate(completed int) float64 {
	elapsed := t.inst.now().Sub(t.started).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return float64(completed) / elapsed
}
