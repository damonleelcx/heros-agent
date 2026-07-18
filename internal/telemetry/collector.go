package telemetry

import (
	"context"
	"sync"

	"github.com/heros-foreal/agentd/internal/metricevent"
)

// Collector is the pipeline chokepoint (Decision 2): every event and span passes through
// tag-completeness gate -> cardinality/label filter -> secret/PII scrubber -> fan-out to the three
// stores (task 5.1). It is a Sink, so the instrument hands it telemetry with no knowledge of what is
// downstream.
//
// # Async and degrade-safe (Decision 7)
//
// Emission is enqueued and processed on a background worker, NEVER on the caller's path. A store outage
// or a slow backend degrades telemetry — events are dropped when the queue is full and logged — but it
// cannot block, delay, or fail a paid run. The enqueue is a non-blocking channel send: worst case is a
// dropped event, never a blocked provider call. This is what makes "< 5 ms p50 overhead" (NFR5) and
// "a collector outage does not fail a run" (task 6.4) hold.
type Collector struct {
	spans   SpanStore
	tsdb    TSDB
	eval    EvalStore
	scrub   Scrubber
	sampler *SpanSampler
	gate    *Gate
	log     Logger

	queue   chan work
	wg      sync.WaitGroup
	closeMu sync.Mutex
	closed  bool

	dropsMu sync.Mutex
	drops   int64
}

// CollectorConfig wires the collector. The stores are required; the scrubber and sampler default to
// the secure/bounded defaults if nil (a nil scrubber would be a silent leak, so it is filled, never
// skipped).
type CollectorConfig struct {
	Spans     SpanStore
	TSDB      TSDB
	Eval      EvalStore
	Scrubber  Scrubber
	Sampler   *SpanSampler
	Logger    Logger
	QueueSize int
}

type workKind int

const (
	workMetric workKind = iota
	workSpan
	workEval
	workBarrier
)

type work struct {
	kind    workKind
	ev      metricevent.Event
	sp      Span
	qm      QualityMetricEvent
	barrier chan struct{}
}

// NewCollector builds and starts the collector's background worker.
func NewCollector(cfg CollectorConfig) *Collector {
	log := cfg.Logger
	if log == nil {
		log = nopLogger{}
	}
	scrub := cfg.Scrubber
	if scrub == nil {
		scrub = NewScrubber() // never nil: a missing scrubber is a silent leak
	}
	sampler := cfg.Sampler
	if sampler == nil {
		sampler = NewSpanSampler(1, true) // keep all by default; a deployment tunes the ratio down
	}
	qsize := cfg.QueueSize
	if qsize <= 0 {
		qsize = 4096
	}
	c := &Collector{
		spans: cfg.Spans, tsdb: cfg.TSDB, eval: cfg.Eval,
		scrub: scrub, sampler: sampler, log: log,
		gate:  NewGate(nil, WithGateLogger(log)),
		queue: make(chan work, qsize),
	}
	c.wg.Add(1)
	go c.run()
	return c
}

// EmitMetric enqueues an operational metric (routes to the TSDB). Non-blocking.
func (c *Collector) EmitMetric(_ context.Context, ev metricevent.Event) {
	c.enqueue(work{kind: workMetric, ev: ev})
}

// EmitSpan enqueues a span (routes to the span store). Non-blocking.
func (c *Collector) EmitSpan(_ context.Context, sp Span) {
	c.enqueue(work{kind: workSpan, sp: sp})
}

// EmitEval enqueues a quality-metric event (routes to Postgres). Non-blocking.
func (c *Collector) EmitEval(_ context.Context, qm QualityMetricEvent) {
	c.enqueue(work{kind: workEval, qm: qm})
}

// enqueue is the non-blocking hand-off. If the queue is full the event is DROPPED and counted, never
// blocked on — a full telemetry queue must not stall a run (Decision 7). A recover guards the one race
// left: an event arriving after Close (send on a closed channel) degrades to a drop, never a panic.
func (c *Collector) enqueue(w work) {
	c.closeMu.Lock()
	closed := c.closed
	c.closeMu.Unlock()
	if closed {
		c.countDrop(w)
		return
	}
	defer func() {
		if recover() != nil {
			c.countDrop(w) // lost the race with Close: dropped, not crashed
		}
	}()
	select {
	case c.queue <- w:
	default:
		c.countDrop(w)
	}
}

func (c *Collector) countDrop(w work) {
	c.dropsMu.Lock()
	c.drops++
	c.dropsMu.Unlock()
	c.log.Warnf("telemetry: dropped a %s event (telemetry degraded, run unaffected)", w.kindName())
}

func (w work) kindName() string {
	switch w.kind {
	case workMetric:
		return "metric"
	case workSpan:
		return "span"
	case workEval:
		return "eval"
	default:
		return "barrier"
	}
}

// run is the single background worker draining the queue and running the pipeline per item.
func (c *Collector) run() {
	defer c.wg.Done()
	for w := range c.queue {
		c.process(w)
	}
}

// process runs one item through gate -> scrub -> route, guarding each store call so a panicking or
// failing store degrades telemetry rather than crashing the worker.
func (c *Collector) process(w work) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Warnf("telemetry: collector recovered from a store panic (telemetry degraded): %v", r)
		}
	}()
	switch w.kind {
	case workMetric:
		if !c.gate.admitMetric(w.ev) {
			return
		}
		ev := c.scrub.ScrubEvent(w.ev)
		c.tsdb.PutMetric(context.Background(), ev)
	case workSpan:
		if !c.gate.admitSpan(w.sp) {
			return
		}
		sp := c.scrub.ScrubSpan(w.sp)
		if c.sampler.Keep(sp) {
			c.spans.PutSpan(context.Background(), sp)
		}
	case workEval:
		if !c.gate.admitMetric(w.qm.Event) {
			return
		}
		w.qm.Event = c.scrub.ScrubEvent(w.qm.Event)
		if err := c.eval.PutEval(context.Background(), w.qm); err != nil {
			// A failed eval write degrades telemetry; it never fails the run (the run already completed
			// before an evaluator ran). Logged, not swallowed.
			c.log.Warnf("telemetry: eval store write failed (telemetry degraded): %v", err)
		}
	case workBarrier:
		close(w.barrier)
	}
}

// Flush blocks until every event enqueued before the call has been processed. Test-only: it uses a
// blocking barrier, which the data path never does. Production code never needs it — telemetry is
// fire-and-forget.
func (c *Collector) Flush() {
	c.closeMu.Lock()
	closed := c.closed
	c.closeMu.Unlock()
	if closed {
		return
	}
	done := make(chan struct{})
	c.queue <- work{kind: workBarrier, barrier: done} // blocking on purpose: a flush must not be dropped
	<-done
}

// Drops is the count of events dropped because the queue was full — externally readable so a monitor
// can see telemetry degrading rather than guessing (health-signal-surface).
func (c *Collector) Drops() int64 {
	c.dropsMu.Lock()
	defer c.dropsMu.Unlock()
	return c.drops
}

// GateStats exposes the boundary gate's reject/dedup counters.
func (c *Collector) GateStats() Stats { return c.gate.Stats() }

// Close drains and stops the worker. After Close, emits are dropped (logged) rather than panicking on a
// closed channel — a late event during shutdown must not crash the process.
func (c *Collector) Close() {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return
	}
	c.closed = true
	c.closeMu.Unlock()
	close(c.queue)
	c.wg.Wait()
}
