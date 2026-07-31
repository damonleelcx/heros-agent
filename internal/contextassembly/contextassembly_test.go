package contextassembly

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// P16 task 5.2 / FR7 — a materialized lossy policy's observed drop is RECORDED, per node per run,
// through the telemetry that already exists.
//
// The guarantee under test is not "the emitter works" (telemetry's own tests cover that). It is that
// the assembly path cannot produce a run without producing the measurement — because a rule that says
// "remember to record it" fails silently, and the thing that goes missing is exactly the signal the
// drop gate and the context-overflow diagnosis both read.

type recordingTSDB struct {
	mu     sync.Mutex
	events []metricevent.Event
}

func (r *recordingTSDB) PutMetric(_ context.Context, ev metricevent.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}
func (r *recordingTSDB) Query(map[string]string) ([]telemetry.Sample, error) { return nil, nil }

func (r *recordingTSDB) byName() map[string]metricevent.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := map[string]metricevent.Event{}
	for _, e := range r.events {
		m[e.MetricName] = e
	}
	return m
}

func (r *recordingTSDB) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		out = append(out, e.MetricName)
	}
	return out
}

func newCollector(t *testing.T) (*telemetry.Collector, *recordingTSDB) {
	t.Helper()
	tsdb := &recordingTSDB{}
	col := telemetry.NewCollector(telemetry.CollectorConfig{
		Spans: telemetry.NewMemSpanStore(0), TSDB: tsdb, Eval: telemetry.NewMemEvalStore()})
	t.Cleanup(func() { col.Close() })
	return col, tsdb
}

func tags() telemetry.P0Tags {
	return telemetry.P0Tags{
		VariantID: "var_1", RunID: "run_1", NodeID: "summarize", CaseID: "case_1", Seed: 7,
		ConfigHash: "b6d81b360a5672d80c27430f39153e2c04e9a1a5b3d3a1c0e2b8f7a6c5d4e3f2",
	}
}

func entry(t *testing.T, policy, params string) *registry.ContextEntry {
	t.Helper()
	for _, p := range registry.BuiltinPolicies() {
		if p.Name() == policy {
			return &registry.ContextEntry{
				VersionID: strings.Repeat("e", 64), Name: "ctx",
				Spec:   registry.ContextSpec{Policy: policy, Params: json.RawMessage(params)},
				Policy: p,
			}
		}
	}
	t.Fatalf("no builtin policy named %q", policy)
	return nil
}

func longConversation() registry.Conversation {
	return registry.Conversation{Messages: []registry.Message{
		{Role: "user", Content: strings.Repeat("a question about the invoice ", 20)},
		{Role: "assistant", Content: strings.Repeat("a long detailed answer ", 20)},
		{Role: "user", Content: strings.Repeat("a follow-up ", 20)},
		{Role: "assistant", Content: "the final short answer"},
	}}
}

// ── task 5.2 — the drop is recorded, on the existing metric, with the run's tags ──────────────────

func TestMaterializedDropRecordedAsSignal(t *testing.T) {
	col, tsdb := newCollector(t)
	r := Runner{Collector: col}

	got, err := r.Assemble(context.Background(), Request{
		Tags:         tags(),
		Entry:        entry(t, "semantic-compaction", `{"target_tokens":20}`),
		Conversation: longConversation(),
		Seed:         7,
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if got.DropRatio <= 0 {
		t.Fatalf("compacting a long conversation to 20 tokens dropped nothing? %+v", got)
	}
	col.Flush()

	ev, ok := tsdb.byName()[telemetry.MetricContextDropRatio]
	if !ok {
		t.Fatalf("no %s event was published for a lossy assembly; the drop gate and the context-overflow "+
			"diagnosis both read this metric, so a missing event is a signal that silently stops existing. "+
			"published: %v", telemetry.MetricContextDropRatio, tsdb.names())
	}
	if ev.Value == nil || *ev.Value != got.DropRatio {
		t.Errorf("the published drop %v is not the measured one %v", ev.Value, got.DropRatio)
	}
	// Per NODE per RUN: the measurement is worthless if it cannot be attributed back to a node of a run
	// under a configuration.
	if ev.NodeID != "summarize" || ev.RunID != "run_1" || ev.ConfigHash == "" || ev.Seed == nil {
		t.Errorf("the drop event is missing its P0 attribution: %+v", ev)
	}
	if ev.Dimensions[telemetry.AttrContextPolicy] != "semantic-compaction" {
		t.Errorf("the event must carry the policy that dropped, got %v", ev.Dimensions)
	}

	// 🚫 NO NEW METRIC FAMILY (NFR6). Everything published is a name telemetry already defined.
	known := map[string]bool{
		telemetry.MetricContextAssembledTokens: true,
		telemetry.MetricContextSourceMessages:  true,
		telemetry.MetricContextDropRatio:       true,
		telemetry.MetricContextRetrievedChunks: true,
	}
	for _, n := range tsdb.names() {
		if !known[n] {
			t.Errorf("the assembly published %q, which is not part of the existing context-assembly family; "+
				"P16 adds no metric name", n)
		}
	}
}

// 🔴 A measured 0.0 and an unmeasured 0.0 are different facts, and the distinction survives all the way
// to the wire: a LOSSLESS policy publishes no drop event at all, so a consumer never reads its implicit
// zero as "measured no drop".
func TestLosslessPolicyPublishesNoDropEvent(t *testing.T) {
	col, tsdb := newCollector(t)
	r := Runner{Collector: col}

	if _, err := r.Assemble(context.Background(), Request{
		Tags: tags(), Entry: entry(t, "sliding-window", `{"window_size":2}`),
		Conversation: longConversation(), Seed: 1,
	}); err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	col.Flush()

	if _, ok := tsdb.byName()[telemetry.MetricContextDropRatio]; ok {
		t.Error("a windowing policy keeps whole messages and drops nothing lossily, so it must publish NO " +
			"drop-ratio event; publishing a 0 would make 'this policy cannot drop' indistinguishable from " +
			"'this run measured no drop'")
	}
	// It still publishes what it DID measure — the assembly is recorded either way.
	if _, ok := tsdb.byName()[telemetry.MetricContextAssembledTokens]; !ok {
		t.Error("a lossless assembly still records its assembled tokens")
	}

	// And the same distinction is carried in the projection scoring reads.
	lossless := Observe("n", "sliding-window", registry.AssembledContext{DropRatio: 0, Lossy: false})
	measuredZero := Observe("n", "summarization", registry.AssembledContext{DropRatio: 0, Lossy: true})
	if lossless.Measured || !measuredZero.Measured {
		t.Errorf("ObservedDrop must distinguish an unmeasured zero from a measured one: %+v vs %+v",
			lossless, measuredZero)
	}
}

// 🔴 A FAILED assembly records nothing. A drop ratio on the board for a run that never assembled is
// worse than no number: it is a measurement of something that did not happen.
func TestFailedAssemblyRecordsNothing(t *testing.T) {
	col, tsdb := newCollector(t)
	r := Runner{Collector: col} // no Host: a summarization policy fails closed

	_, err := r.Assemble(context.Background(), Request{
		Tags: tags(), Entry: entry(t, "summarization", `{"summarizer_model_ref":"m"}`),
		Conversation: longConversation(), Seed: 1,
	})
	if err == nil {
		t.Fatal("a host-calling policy with no host services must fail closed")
	}
	col.Flush()
	if names := tsdb.names(); len(names) != 0 {
		t.Errorf("a failed assembly published %v; nothing may be recorded for a run that did not assemble", names)
	}
}

// The seam fails closed on an entry that never went through resolution, rather than assembling nothing
// and looking like a node with no context.
func TestUnboundPolicyFailsClosed(t *testing.T) {
	r := Runner{}
	_, err := r.Assemble(context.Background(), Request{
		Tags:  tags(),
		Entry: &registry.ContextEntry{VersionID: "v", Spec: registry.ContextSpec{Policy: "sliding-window"}},
	})
	if err == nil || !strings.Contains(err.Error(), "no bound implementation") {
		t.Fatalf("want a fail-closed error for an unresolved entry, got %v", err)
	}

	if _, err := (Runner{}).Assemble(context.Background(), Request{Tags: tags()}); err == nil {
		t.Error("a request with no context entry must be rejected, not assembled as 'no context'")
	}
}

// The host-calling path runs on the trusted host and its resolved request is captured — the same
// credential-isolation and determinism claims the policies make, asserted through the seam that
// actually wires them.
func TestHostCallingPolicyRunsThroughTheSeam(t *testing.T) {
	col, tsdb := newCollector(t)
	host := &fakeHost{summary: "the gist"}
	r := Runner{Host: host, Collector: col}

	got, err := r.Assemble(context.Background(), Request{
		Tags: tags(), Entry: entry(t, "summarization", `{"summarizer_model_ref":"anthropic/claude-sonnet-5"}`),
		Conversation: longConversation(), Seed: 42,
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(host.reqs) != 1 || host.reqs[0].Seed != 42 {
		t.Fatalf("the summarizer must be reached exactly once, host-side, with the run's seed: %+v", host.reqs)
	}
	if got.ResolvedRequest == nil {
		t.Error("the resolved request is the determinism handle and must be captured")
	}
	col.Flush()
	if _, ok := tsdb.byName()[telemetry.MetricContextDropRatio]; !ok {
		t.Error("a summarization run must record what it dropped")
	}
}

type fakeHost struct {
	summary string
	reqs    []registry.ResolvedRequest
	err     error
}

func (h *fakeHost) Summarize(_ context.Context, req registry.ResolvedRequest) (string, error) {
	h.reqs = append(h.reqs, req)
	return h.summary, h.err
}

func (h *fakeHost) Retrieve(_ context.Context, req registry.ResolvedRequest) ([]registry.Chunk, error) {
	h.reqs = append(h.reqs, req)
	return nil, h.err
}

// ── task 6.4 🔴 — a measurement run pins the retriever, its params, and the seed ──────────────────

func TestRetrievalMeasurementDeterministic(t *testing.T) {
	host := &fakeChunkHost{chunks: []registry.Chunk{{ID: "c1", Text: "alpha"}, {ID: "c2", Text: "beta"}}}
	r := Runner{Host: host}
	pin := MeasurementPin{
		ConfigHash:     "b6d81b360a5672d80c27430f39153e2c04e9a1a5b3d3a1c0e2b8f7a6c5d4e3f2",
		SourceRevision: "rev-abc",
		Seed:           7,
	}
	req := func() Request {
		return Request{
			Tags:         tags(),
			Entry:        entry(t, "rag-retrieval", `{"top_k":3,"retriever_ref":"kb","rerank":true}`),
			Conversation: longConversation(),
		}
	}

	first, err := r.Measure(context.Background(), req(), pin)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	second, err := r.Measure(context.Background(), req(), pin)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if !SameRequest(first, second) {
		t.Fatalf("the same config_hash at the same source_revision and seed issued two different resolved "+
			"retrieval requests:\n%+v\n%+v", first.Request, second.Request)
	}
	// The claim is at the RESOLVED REQUEST, including the rerank — never at the provider's output bytes,
	// which are outside anything this platform controls.
	if first.Request == nil || first.Request.Op != "rerank" {
		t.Fatalf("the rerank must be part of the pinned request: %+v", first.Request)
	}
	if first.Request.Seed != pin.Seed || first.Request.TopK != 3 || first.Request.Ref != "kb" {
		t.Errorf("the request must pin retriever, params and seed: %+v", first.Request)
	}

	// A DIFFERENT seed is a different measurement. If it were not, the seed would not be pinning anything.
	other, err := r.Measure(context.Background(), req(), MeasurementPin{
		ConfigHash: pin.ConfigHash, SourceRevision: pin.SourceRevision, Seed: 8})
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if SameRequest(first, other) {
		t.Error("two seeds produced the identical resolved request; the seed is not reaching the retriever")
	}
}

// 🔴 An unpinned run is REFUSED as a measurement rather than measured and labelled somehow. An
// unreproducible number is one careless join away from the verified-delta ledger.
func TestUnpinnedRetrievalIsNotAMeasurementRun(t *testing.T) {
	r := Runner{Host: &fakeChunkHost{}}
	req := Request{
		Tags:         tags(),
		Entry:        entry(t, "rag-retrieval", `{"top_k":3,"retriever_ref":"kb"}`),
		Conversation: longConversation(),
	}
	for name, pin := range map[string]MeasurementPin{
		"no config_hash":     {SourceRevision: "rev", Seed: 1},
		"no source_revision": {ConfigHash: "cfg", Seed: 1},
	} {
		if _, err := r.Measure(context.Background(), req, pin); !errors.Is(err, ErrUnpinnedMeasurement) {
			t.Errorf("%s: want ErrUnpinnedMeasurement, got %v", name, err)
		}
	}
	// Seed 0 is a VALUE, not an absence. Rejecting it would reject the most obvious seed anyone picks.
	if _, err := r.Measure(context.Background(), req,
		MeasurementPin{ConfigHash: "cfg", SourceRevision: "rev", Seed: 0}); err != nil {
		t.Errorf("seed 0 is a legitimate pinned seed: %v", err)
	}
}

// ── task 6.6 — augmentation is RETRIEVAL, not loss ───────────────────────────────────────────────

func TestAugmentationIsNotDrop(t *testing.T) {
	col, tsdb := newCollector(t)
	host := &fakeChunkHost{chunks: []registry.Chunk{{ID: "c1", Text: "alpha"}, {ID: "c2", Text: "beta"}}}
	r := Runner{Host: host, Collector: col}

	conv := longConversation()
	got, err := r.Assemble(context.Background(), Request{
		Tags: tags(), Entry: entry(t, "rag-retrieval", `{"top_k":2,"retriever_ref":"kb"}`),
		Conversation: conv, Seed: 1,
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// Chunks PREPENDED, conversation preserved: nothing was dropped, so the drop is zero and the
	// retrieved-chunk count is positive. Reporting augmentation as loss would make the drop gate reject
	// retrieval — the axis's own best operator — for doing exactly what it is for.
	if got.DropRatio != 0 {
		t.Errorf("pure augmentation reported a drop of %v; it added chunks and kept every turn", got.DropRatio)
	}
	if got.RetrievedChunks != 2 {
		t.Errorf("retrieved-chunk count = %d, want 2", got.RetrievedChunks)
	}
	if len(got.Messages) != len(conv.Messages)+2 {
		t.Errorf("augmentation must preserve the conversation: %d messages in, %d out",
			len(conv.Messages), len(got.Messages))
	}
	for i, m := range conv.Messages {
		if got.Messages[i+2] != m {
			t.Errorf("turn %d was altered by augmentation: %q → %q", i, m.Content, got.Messages[i+2].Content)
		}
	}
	col.Flush()

	// On the wire, the same distinction: a chunk count is published, and NO drop event — because
	// rag-retrieval is not a lossy policy and a published 0 would read as "measured no drop".
	names := tsdb.byName()
	if _, ok := names[telemetry.MetricContextRetrievedChunks]; !ok {
		t.Error("augmentation must record its retrieved-chunk count; that is what makes it legible as " +
			"retrieval rather than as an unexplained token increase")
	}
	if _, ok := names[telemetry.MetricContextDropRatio]; ok {
		t.Error("augmentation published a drop-ratio event; it drops nothing, and the event would make the " +
			"drop gate judge retrieval on a number that means nothing")
	}
}

type fakeChunkHost struct {
	chunks []registry.Chunk
	reqs   []registry.ResolvedRequest
}

func (h *fakeChunkHost) Summarize(_ context.Context, req registry.ResolvedRequest) (string, error) {
	h.reqs = append(h.reqs, req)
	return "", nil
}

func (h *fakeChunkHost) Retrieve(_ context.Context, req registry.ResolvedRequest) ([]registry.Chunk, error) {
	h.reqs = append(h.reqs, req)
	return h.chunks, nil
}
