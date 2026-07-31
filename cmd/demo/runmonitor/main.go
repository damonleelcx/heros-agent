// Command runmonitor drives the P2.5 live run-monitoring view against a LIVE, stubbed-provider run
// (task 8.3: "verify against a live (stubbed-provider) run before calling the view done").
//
// It exists because the five first-class states (loading / error / empty / streaming / terminal) and
// the failed-vs-timed-out-vs-healthy distinction cannot be verified by asserting on markup — a test
// that greps the HTML for "timed_out" proves the string I wrote is the string I wrote. What has to be
// checked is that a real browser, fed real telemetry from a real (stubbed) provider run streaming in
// over SSE, renders visibly different things. So this stands the whole substrate up — gateway ->
// instrument -> collector -> stores -> monitor -> SSE -> view — and loops a run so a watcher always
// catches a live stream.
//
// Not a shipped service: a demo harness. It uses a stub provider and in-memory stores.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

const configHash = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

// memStatus is the in-memory stand-in for the run record. The monitor reads run status from HERE, never
// derives it — the one rule the view follows (task 8.2). Each demo cycle is a genuinely NEW run with a
// unique run_id (as real runs are — executor.RunIDFor), so telemetry idempotency does not dedup one
// cycle's spans against the previous one, and the view re-streams each cycle.
type memStatus struct {
	mu      sync.RWMutex
	runs    map[string]telemetry.RunStatusInfo
	current string
}

func newMemStatus() *memStatus { return &memStatus{runs: map[string]telemetry.RunStatusInfo{}} }

func (m *memStatus) set(id string, i telemetry.RunStatusInfo) {
	m.mu.Lock()
	m.runs[id] = i
	m.current = id
	m.mu.Unlock()
}

func (m *memStatus) RunStatus(id string) (telemetry.RunStatusInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	i, ok := m.runs[id]
	return i, ok
}

func (m *memStatus) latest() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// node describes one node in the demo graph and how the stub provider should behave for it.
type node struct {
	id      string
	content string // steers the stub: "fail" -> 400, "timeout" -> slow, else ok
	delayMS int    // artificial pre-call delay so the browser sees nodes stream in
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8478", "listen address")
	flag.Parse()

	// A stub provider whose behavior is steered by the message content, so one server drives ok / failed
	// / timed-out nodes.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		switch {
		case strings.Contains(s, "please-fail"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"the demo asked me to fail"}`)
		case strings.Contains(s, "please-timeout"):
			time.Sleep(3 * time.Second) // exceeds the 1 s per-call deadline -> ErrTimeout
			_, _ = io.WriteString(w, okBody(50, 20))
		default:
			time.Sleep(120 * time.Millisecond) // a little latency so numbers are non-trivial
			_, _ = io.WriteString(w, okBody(100, 40))
		}
	}))
	defer stub.Close()

	// The substrate: pricebook -> instrument -> collector -> stores -> monitor.
	spans := telemetry.NewMemSpanStore(10 * time.Minute) // bound growth over many demo cycles
	tsdb := telemetry.NewMemTSDB(10 * time.Minute)
	eval := telemetry.NewMemEvalStore()
	col := telemetry.NewCollector(telemetry.CollectorConfig{Spans: spans, TSDB: tsdb, Eval: eval, Logger: stdLogger{}})
	defer col.Close()

	pb, err := telemetry.NewPriceBook("2026-07-18.demo")
	if err != nil {
		log.Fatal(err)
	}
	pb.Set(providergateway.ProviderOpenAI, "gpt-5", telemetry.ModelInfo{
		InputPerMTok: 2.5, OutputPerMTok: 10, CacheReadPerMTok: 0.25, ContextWindow: 200_000,
	})
	inst, err := telemetry.NewInstrument(col, pb, telemetry.WithLogger(stdLogger{}))
	if err != nil {
		log.Fatal(err)
	}

	one := 1
	gw := providergateway.New(
		providergateway.StaticSecrets{providergateway.ProviderOpenAI: {APIKey: "sk-demo-not-a-real-key"}},
		providergateway.WithBaseURL(providergateway.ProviderOpenAI, stub.URL),
		providergateway.WithObserver(inst),
		providergateway.WithMaxRetries(0),
	)
	entry := &registry.ModelEntry{VersionID: strings.Repeat("d", 64), Name: "gpt-5",
		Spec: registry.ModelSpec{Provider: providergateway.ProviderOpenAI, ModelID: "gpt-5",
			Params: registry.ModelParams{TimeoutSeconds: &one}}}

	status := newMemStatus()
	monitor := telemetry.NewMonitor(status, spans)

	srv := api.New(nil, config.Config{})
	srv.MountMonitor(monitor)
	// A stable watch URL: "/" redirects to the current run's monitor, so a human keeps one tab open and
	// always lands on the run that is streaming now.
	srv.Mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		cur := status.latest()
		if cur == "" {
			_, _ = io.WriteString(w, "warming up — refresh in a moment")
			return
		}
		// The console owns the screen now; this redirect points at its canonical route.
		http.Redirect(w, r, "/app/runs/"+url.PathEscape(cur)+"/live", http.StatusFound)
	})

	// Loop the run so a watcher always catches a live stream.
	go runLoop(gw, inst, entry, status)

	fmt.Printf("live run monitor (stable URL): http://%s/\n", *addr)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler))
}

// runLoop executes the demo graph over and over: healthy nodes, a slow one, a failed one, a timed-out
// one — with delays so the browser sees metrics stream in, then a terminal status, then a reset.
func runLoop(gw *providergateway.Gateway, inst *telemetry.Instrument, entry *registry.ModelEntry, status *memStatus) {
	nodes := []node{
		{id: "n_fetch", content: "hello", delayMS: 400},
		{id: "n_classify", content: "hello", delayMS: 900},
		{id: "n_slow", content: "hello", delayMS: 1600}, // healthy but slow — must NOT look like a failure
		{id: "n_flaky", content: "please-fail", delayMS: 700},
		{id: "n_hang", content: "please-timeout", delayMS: 700},
		{id: "n_finish", content: "hello", delayMS: 500},
	}
	seed := int64(7)
	cycle := 0
	for {
		cycle++
		runID := fmt.Sprintf("run_demo_%d", cycle)
		status.set(runID, telemetry.RunStatusInfo{Status: "running", ConfigHash: configHash})
		rc := telemetry.RunContext{VariantID: "variant_demo", RunID: runID, ConfigHash: configHash, Seed: seed, CaseID: "case_demo"}
		tracer := inst.StartRun(rc)
		anyFailed := false
		for _, n := range nodes {
			time.Sleep(time.Duration(n.delayMS) * time.Millisecond)
			nodeRC := rc.WithNode(n.id, 0)
			ctx := telemetry.NewContext(context.Background(), nodeRC)
			tracer.NodeStarted(ctx, n.id)
			_, err := executor.CallProvider(ctx, gw, entry,
				providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: n.content}}},
				executor.NodeInvocation{RunID: runID, NodeID: n.id, AttemptGroup: 0, Seed: &seed})
			if err != nil {
				anyFailed = true
			}
			tracer.NodeFinished(ctx, n.id)
		}
		tracer.EndRun(context.Background())
		final := "succeeded"
		if anyFailed {
			final = "failed"
		}
		status.set(runID, telemetry.RunStatusInfo{Status: final, ConfigHash: configHash})
		time.Sleep(8 * time.Second) // linger on the terminal state, then start a new run for the next watcher
	}
}

func okBody(in, out int) string {
	return fmt.Sprintf(`{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}],`+
		`"usage":{"prompt_tokens":%d,"completion_tokens":%d}}`, in, out)
}

type stdLogger struct{}

func (stdLogger) Warnf(format string, args ...any) { log.Printf("[telemetry] "+format, args...) }
