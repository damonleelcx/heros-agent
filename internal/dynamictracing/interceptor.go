// Package dynamictracing implements P5's OTel-style interceptor (Decision 4): it wraps the
// signature-registry SDK entrypoints and logs EVERY real LLM call, its inputs, and its call stack,
// tagged with the P0 tag set and correlated to a P2.5 span. Static analysis produced a *candidate*
// graph; this interceptor instruments a real run so the reconciler (§5) can confirm the graph and
// resolve runtime-dynamic dispatch (loops, conditional routing) concretely.
//
// # Passive, async, best-effort — the interceptor must not change the run
//
// If instrumentation altered the run, the evidence would be worthless (Decision 4: "assert identical
// outputs traced vs. untraced"). So the interceptor is:
//
//   - PASSIVE: it only observes; it holds no provider client and makes ZERO provider calls, so it can
//     neither change an output nor add a call to the bill.
//   - ASYNC: recording (hashing inputs, writing the blob, appending the record) runs off the caller's
//     path on a worker goroutine, so the run's latency is bounded by a cheap copy, not by the store.
//   - BEST-EFFORT: every recording step is wrapped so a logging failure — a full disk, a slow blob
//     store, a panic in the sink — is swallowed and never fails the run.
//
// # Secrets and PII never enter a trace artifact
//
// Inputs are REDACTED (secret/bearer shapes stripped) and stored as CONTENT-HASHED blobs; the record
// the DB holds carries only the hash and the P0 tags. Secrets come from the manager and are set as
// request headers by the gateway — they are not in the request body the interceptor sees — and the
// redactor is a second fence in case a workflow put one in a prompt. The call stack is captured as
// function + file:line frames only, never argument values, so it cannot leak a secret either.
package dynamictracing

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/telemetry"
)

// Tags is the P0 tag set every traced call carries: {variant_id, run_id, node_id, case_id, seed,
// timestamp, config_hash}. Named, not a free map, so an under-tagged record is a compile error, not a
// silently-dropped one.
type Tags struct {
	VariantID  string
	RunID      string
	NodeID     string
	CaseID     string
	Seed       int64
	ConfigHash string
	Timestamp  time.Time
}

// LLMCall is the observation the interceptor receives at a wrapped SDK entrypoint. Inputs is the
// request payload (messages/prompt/params) exactly as the workflow built it — the interceptor redacts
// and content-hashes it, and never stores it raw.
type LLMCall struct {
	Provider string
	ModelID  string
	// Inputs is the raw request payload bytes (already JSON, as the SDK would send). It is treated as
	// opaque and never logged inline.
	Inputs []byte
	// InvocationIndex is 0-based within the run for THIS node — a loop firing 7 times reports 0..6 for
	// one static definition (P0's static-definition↔invocation distinction, confirmed by §5).
	InvocationIndex int
}

// Frame is one redacted call-stack frame: the function and its file:line, never any argument value.
type Frame struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// TracedCall is the recorded event. The blob hashes point at the object store; the DB row holds only
// these hashes and the P0 tags (Decision 4 / task 8.4).
type TracedCall struct {
	Tags            Tags    `json:"tags"`
	Provider        string  `json:"provider"`
	ModelID         string  `json:"model_id"`
	InvocationIndex int     `json:"invocation_index"`
	InputsBlobHash  string  `json:"inputs_blob_hash"`
	StackBlobHash   string  `json:"stack_blob_hash"`
	Stack           []Frame `json:"stack"`
	// TraceID/SpanID correlate this call to its P2.5 span, derived from the same coordinates the
	// telemetry substrate uses, so a trace backend joins them for free.
	TraceID string `json:"trace_id"`
	SpanID  string `json:"span_id"`
}

// BlobStore content-addresses inputs and stacks. Same shape as the executor's BlobStore, so a
// deployment can share one object store.
type BlobStore interface {
	Put(ctx context.Context, data []byte) (contentHash string, err error)
}

// Sink receives traced calls. Best-effort: an error is logged and dropped, never propagated to the run.
type Sink interface {
	Record(ctx context.Context, call TracedCall) error
}

// Logger is where best-effort failures go.
type Logger interface {
	Warnf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Warnf(string, ...any) {}

// Interceptor is the passive, async, redacting call logger.
type Interceptor struct {
	blobs BlobStore
	sink  Sink
	log   Logger
	now   func() time.Time

	// redact strips secret/bearer shapes from input bytes before they are hashed and stored.
	redact func([]byte) []byte

	// stackDepth bounds how many frames are captured. A cap, not the whole stack, because a deep stack
	// is mostly framework plumbing and the reconciler only needs the workflow-side frames to attribute a
	// call to a definition.
	stackDepth int

	wg sync.WaitGroup

	// counters observe overhead (task 4.5). providerCalls stays 0 by construction — the interceptor has
	// no provider client — and is asserted in tests.
	mu            sync.Mutex
	observed      int
	recordErrors  int
	providerCalls int
}

// Option configures an Interceptor.
type Option func(*Interceptor)

// WithLogger sets where best-effort failures are logged.
func WithLogger(l Logger) Option { return func(i *Interceptor) { i.log = l } }

// WithClock injects the clock so timestamps are testable.
func WithClock(now func() time.Time) Option { return func(i *Interceptor) { i.now = now } }

// WithStackDepth overrides the captured-frame cap.
func WithStackDepth(n int) Option {
	return func(i *Interceptor) {
		if n > 0 {
			i.stackDepth = n
		}
	}
}

// New builds an interceptor over a blob store and a sink.
func New(blobs BlobStore, sink Sink, opts ...Option) *Interceptor {
	i := &Interceptor{
		blobs: blobs, sink: sink, log: nopLogger{}, now: time.Now,
		redact: RedactSecrets, stackDepth: 32,
	}
	for _, o := range opts {
		o(i)
	}
	return i
}

// Observe records one LLM call. It is the passive hook a wrapped SDK entrypoint calls. It returns
// nothing and never blocks on the store: the caller's LLM call proceeds unchanged whether or not
// tracing succeeds.
//
// The stack is captured HERE, synchronously, because it must be the caller's stack — capturing it on
// the worker goroutine would record the worker's stack instead. Everything expensive (redact, hash,
// store, record) is deferred to the worker.
func (i *Interceptor) Observe(ctx context.Context, tags Tags, call LLMCall) {
	if i == nil {
		return
	}
	i.mu.Lock()
	i.observed++
	i.mu.Unlock()

	if tags.Timestamp.IsZero() {
		tags.Timestamp = i.now()
	}
	stack := captureStack(i.stackDepth) // caller's stack, captured synchronously
	// Copy the inputs so a caller reusing its buffer cannot race the worker.
	inputs := append([]byte(nil), call.Inputs...)

	i.wg.Add(1)
	go func() {
		defer i.wg.Done()
		defer func() {
			// Best-effort: a panic in the recording path never escapes to the run.
			if r := recover(); r != nil {
				i.bump(&i.recordErrors)
				i.log.Warnf("dynamictracing: recording panicked: %v", r)
			}
		}()
		i.record(ctx, tags, call, inputs, stack)
	}()
}

// record does the deferred work: redact → hash inputs → hash stack → emit. Every store call is
// best-effort; a failure increments recordErrors and is logged, but the traced call is still emitted
// with whatever hashes succeeded (a missing blob is better than a failed run).
func (i *Interceptor) record(ctx context.Context, tags Tags, call LLMCall, inputs []byte, stack []Frame) {
	redacted := i.redact(inputs)
	inputsHash := i.put(ctx, redacted)
	stackHash := i.put(ctx, encodeStack(stack))

	tc := TracedCall{
		Tags: tags, Provider: call.Provider, ModelID: call.ModelID,
		InvocationIndex: call.InvocationIndex,
		InputsBlobHash:  inputsHash, StackBlobHash: stackHash, Stack: stack,
		TraceID: telemetry.TraceID(tags.RunID),
		SpanID:  telemetry.NodeSpanID(idempotencyKey(tags, call.InvocationIndex)),
	}
	if i.sink != nil {
		if err := i.sink.Record(ctx, tc); err != nil {
			i.bump(&i.recordErrors)
			i.log.Warnf("dynamictracing: sink.Record failed (dropped, run unaffected): %v", err)
		}
	}
}

func (i *Interceptor) put(ctx context.Context, data []byte) string {
	if i.blobs == nil {
		return ""
	}
	h, err := i.blobs.Put(ctx, data)
	if err != nil {
		i.bump(&i.recordErrors)
		i.log.Warnf("dynamictracing: blob put failed (dropped): %v", err)
		return ""
	}
	return h
}

// Flush waits for all pending records to be written. For tests and for a run's end-of-life drain; a
// production caller may ignore it (best-effort means a lost tail is acceptable).
func (i *Interceptor) Flush() { i.wg.Wait() }

// Stats returns overhead counters. ProviderCalls is always 0 — the interceptor makes none — which is
// task 4.5's "no added provider calls", checked structurally.
func (i *Interceptor) Stats() (observed, recordErrors, providerCalls int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.observed, i.recordErrors, i.providerCalls
}

func (i *Interceptor) bump(p *int) {
	i.mu.Lock()
	*p++
	i.mu.Unlock()
}

// idempotencyKey mirrors executor.IdempotencyKey so a traced call's span id matches the P2.5 node span.
// AttemptGroup is not in Tags (a traced call is one invocation), so invocation_index stands in as the
// per-node discriminator for the span correlation.
func idempotencyKey(t Tags, invocationIndex int) string {
	return t.RunID + ":" + t.NodeID + ":" + itoa(invocationIndex)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	pos := len(b)
	for n > 0 {
		pos--
		b[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

// captureStack records the caller's stack as redacted frames (function + file:line only). It skips the
// interceptor's own frames so the top frame is the workflow code that made the call.
func captureStack(depth int) []Frame {
	pcs := make([]uintptr, depth)
	// Skip runtime.Callers, captureStack, Observe → 3.
	n := runtime.Callers(3, pcs)
	if n == 0 {
		return nil
	}
	frames := runtime.CallersFrames(pcs[:n])
	var out []Frame
	for {
		fr, more := frames.Next()
		out = append(out, Frame{Function: fr.Function, File: fr.File, Line: fr.Line})
		if !more {
			break
		}
	}
	return out
}
