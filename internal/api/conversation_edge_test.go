package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/conversation"
)

// conversation_edge_test.go is P31 §5: the stream survives the deployment.
//
// # What can be asserted here and what has to be asserted at the edge
//
// This file proves the two properties that are the PLATFORM'S: that a streamed response leaves this
// process incrementally, and that readiness does not queue behind the streams. It also compares the
// three deployment topologies against each other, because a buffering setting present in one substrate
// and absent in another is the P29 defect wearing a new hat.
//
// It does NOT prove that a response arrives incrementally at a browser through a real proxy. That is
// `make console-edge-proof` — it needs a running edge, and a setting is a request to one hop while the
// assertion is a fact about all of them.

// ── §5.2 · a streamed response leaves this process incrementally ─────────────────────────────────

// TestTheStreamArrivesIncrementallyAndNotAllAtTheEnd is the assertion §5.2 asks for, at the one place a
// unit test can make it: the handler's own writes.
//
// 🔴 A batched-at-the-end delivery MUST fail this. That is why the recorder below timestamps each write
// rather than counting them — a handler that buffered everything and flushed once at close would produce
// the same BYTES as a correct one, and any assertion over the body alone would pass.
func TestTheStreamArrivesIncrementallyAndNotAllAtTheEnd(t *testing.T) {
	gate := &gatedReader{release: make(chan struct{}, 8)}
	f := newConversationServer(t, gate)
	convID := f.newConversation(t, personA, "wf_1")
	owner := conversation.Owner{TenantID: personA.TenantID, UserID: personA.UserID}

	f.do(t, personA, "POST", "/api/v1/conversation-turns",
		fmt.Sprintf(`{"conversation_id":%q,"question":"what does this node remember between calls?"}`, convID))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/v1/conversation-stream?conversation_id="+convID, nil)
	req = req.WithContext(auth.WithPrincipal(ctx, personA))
	rec := &timingRecorder{ResponseRecorder: httptest.NewRecorder(), start: time.Now()}

	done := make(chan struct{})
	go func() { defer close(done); f.srv.Mux.ServeHTTP(rec, req) }()

	// Release the steps one at a time, with a gap between them. A correct stream writes during each gap;
	// a buffering one writes nothing until the handler returns.
	for i := 0; i < 3; i++ {
		time.Sleep(60 * time.Millisecond)
		gate.release <- struct{}{}
	}
	waitFor(t, func() bool {
		msgs, _ := f.store.Messages(convID, owner, 0)
		return len(msgs) > 0 && msgs[len(msgs)-1].Kind.Terminal()
	}, "the turn to finish")
	cancel()
	<-done

	writes := rec.writeTimes()
	if len(writes) < 3 {
		t.Fatalf("the handler wrote %d times; a stream that writes fewer times than it has messages is "+
			"batching", len(writes))
	}
	spread := writes[len(writes)-1] - writes[0]
	if spread < 60*time.Millisecond {
		t.Errorf("every write landed within %v of the first. The stream delivered in one burst, which is "+
			"what a buffering hop produces — and it does not error, does not log, and is "+
			"indistinguishable from slowness at the application layer.", spread)
	}
}

// timingRecorder records WHEN each write happened, not just what was written.
type timingRecorder struct {
	*httptest.ResponseRecorder
	start time.Time
	times []time.Duration
}

func (r *timingRecorder) Write(b []byte) (int, error) {
	r.times = append(r.times, time.Since(r.start))
	return r.ResponseRecorder.Write(b)
}

// Flush satisfies http.Flusher — WITHOUT it the handler answers 501 "streaming unsupported" and this
// whole test passes over a code path the product never takes.
func (r *timingRecorder) Flush() { r.ResponseRecorder.Flush() }

func (r *timingRecorder) writeTimes() []time.Duration { return r.times }

func TestTheStreamAsksEveryHopNotToBuffer(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	convID := f.newConversation(t, personA, "wf_1")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/v1/conversation-stream?conversation_id="+convID, nil)
	req = req.WithContext(auth.WithPrincipal(ctx, personA))
	rec := httptest.NewRecorder()
	f.srv.Mux.ServeHTTP(rec, req)

	for header, want := range map[string]string{
		"Content-Type":      "text/event-stream",
		"X-Accel-Buffering": "no",
		"Cache-Control":     "no-store, no-transform",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// ── §5.3 · readiness is not behind the same exhaustible pool ─────────────────────────────────────

// TestReadinessAnswersWhileEveryStreamSlotIsTaken is the assertion, and it is deliberately brutal: it
// takes EVERY slot and then asks for readiness.
//
// The failure it prevents is specific and is the one that actually happens: descriptor or slot
// exhaustion does not fail the streams, it fails whatever asks NEXT — usually the orchestrator's probe —
// so the box is marked unhealthy for a reason that has nothing to do with its health, while the streams
// that caused it carry on working.
func TestReadinessAnswersWhileEveryStreamSlotIsTaken(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	gauge := f.srv.conversations.streams

	tickets := make([]uint64, 0, maxConcurrentStreams)
	for i := 0; i < maxConcurrentStreams; i++ {
		ticket, ok := gauge.acquire()
		if !ok {
			t.Fatalf("only %d of %d slots could be taken", i, maxConcurrentStreams)
		}
		tickets = append(tickets, ticket)
	}
	t.Cleanup(func() {
		for _, ticket := range tickets {
			gauge.release(ticket)
		}
	})

	// 🔴 With every slot held, readiness must still answer — and answer with the numbers.
	rec := f.do(t, personA, "GET", "/readyz", "")
	if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz returned %d with every stream slot taken; it must answer either way", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readyz body: %v", err)
	}
	streams, ok := body["conversation_streams"].(map[string]any)
	if !ok {
		t.Fatal("/readyz reports nothing about the conversation streams. Task 5.4: connection-count and " +
			"stream-duration go on a readable health endpoint, not only into logs — a log line that " +
			"scrolled past three restarts ago cannot answer 'how many are open right now'.")
	}
	if int(streams["open"].(float64)) != maxConcurrentStreams {
		t.Errorf("open = %v, want %d", streams["open"], maxConcurrentStreams)
	}
	if int(streams["ceiling"].(float64)) != maxConcurrentStreams {
		t.Errorf("ceiling = %v, want %d", streams["ceiling"], maxConcurrentStreams)
	}

	// And a stream asked for now is REFUSED by name rather than hanging or exhausting a descriptor.
	convID := f.newConversation(t, personA, "wf_1")
	stream := f.do(t, personA, "GET", "/api/v1/conversation-stream?conversation_id="+convID, "")
	if stream.Code != http.StatusServiceUnavailable {
		t.Errorf("a stream past the ceiling returned %d; want 503 naming the ceiling", stream.Code)
	}
	if !strings.Contains(stream.Body.String(), "readyz") {
		t.Errorf("the refusal does not say where the number is: %s", strings.TrimSpace(stream.Body.String()))
	}

	after := gauge.Health()
	if after.Refused == 0 {
		t.Error("the refusal was not counted. A refusal nobody counts is a capacity problem nobody can " +
			"see until it becomes a symptom.")
	}
}

func TestTheGaugeReportsTheOldestStreamRatherThanTheMean(t *testing.T) {
	c := &fakeClock{at: time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)}
	gauge := newStreamGauge(c.now)

	old, _ := gauge.acquire()
	c.at = c.at.Add(6 * time.Hour)
	for i := 0; i < 20; i++ {
		gauge.acquire()
	}
	health := gauge.Health()
	if health.LongestSeconds != int((6 * time.Hour).Seconds()) {
		t.Errorf("longest_seconds = %d, want %d.\nThe OLDEST stream is the signal — a mean over twenty "+
			"healthy streams and one that has been open for six hours erases the only interesting one.",
			health.LongestSeconds, int((6 * time.Hour).Seconds()))
	}
	gauge.release(old)
	if again := gauge.Health(); again.LongestSeconds != 0 {
		t.Errorf("longest_seconds = %d after the oldest was released, want 0", again.LongestSeconds)
	}
	if gauge.Health().Peak != 21 {
		t.Errorf("peak = %d, want 21; the high-water mark is what predicts the refusal", gauge.Health().Peak)
	}
}

type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time { return c.at }

// ── §5.1 · every substrate disables buffering for the stream path ────────────────────────────────

// TestEveryStreamPathIsPublishedAndUnbuffered compares the THREE topologies against each other.
//
// 🔴 This is P29's defect generalised. There, a route's reachability lived in a deployment file and its
// existence lived in Go, so the two drifted — and worse, they drifted PER SUBSTRATE: the Kubernetes
// overlay had six rules and a fence, the Compose bootstrap had one rule and no fence, and nothing
// compared them. A buffering setting has exactly the same shape: correct in one substrate, absent in
// another, and the symptom on the wrong one is "the product feels slow".
func TestEveryStreamPathIsPublishedAndUnbuffered(t *testing.T) {
	root := filepath.Join("..", "..")

	bootstrap, err := os.ReadFile(filepath.Join(root, "deploy", "scripts", "bootstrap-vm.sh"))
	if err != nil {
		t.Fatalf("reading the Compose bootstrap: %v", err)
	}
	src := string(bootstrap)

	streamPaths := regexp.MustCompile(`PLATFORM_STREAM_PATHS="([^"]*)"`).FindStringSubmatch(src)
	if streamPaths == nil {
		t.Fatal("the Compose bootstrap declares no PLATFORM_STREAM_PATHS — this fence's scan is broken, " +
			"or the long-lived routes lost their own handler")
	}
	declared := strings.Fields(streamPaths[1])
	if len(declared) == 0 {
		t.Fatal("PLATFORM_STREAM_PATHS is empty")
	}

	publicPaths := regexp.MustCompile(`PLATFORM_PUBLIC_PATHS="([^"]*)"`).FindStringSubmatch(src)
	if publicPaths == nil {
		t.Fatal("the Compose bootstrap declares no PLATFORM_PUBLIC_PATHS")
	}
	published := strings.Fields(publicPaths[1])

	for _, path := range declared {
		// 🔴 A stream path must ALSO be published. It is not a different exposure — it is a public route
		// like any other; what differs is only that its response must not be buffered. A path in the
		// stream list and absent from the public list is a route Caddy routes to agentd on a handler
		// nothing else covers, which works — until somebody removes the stream handler.
		found := false
		for _, p := range published {
			if p == path {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is declared as a stream path and is NOT in PLATFORM_PUBLIC_PATHS", path)
		}
		// And it must be in the platform's own exposure classification.
		if routeExposure[path] != ExposurePublic {
			t.Errorf("%s is published by the Compose bootstrap and publicroutes.go does not declare it public", path)
		}
	}

	// The Caddy handler for the stream paths must disable buffering.
	if !strings.Contains(src, "flush_interval -1") {
		t.Error("the generated Caddyfile does not set `flush_interval -1` on the stream handler.\n" +
			"A reverse proxy that buffers turns streaming into batching: the stream still completes, " +
			"nothing errors, every message arrives at the end in one burst, and the failure is " +
			"indistinguishable from slowness at the application layer.")
	}

	// The Kubernetes overlay must declare the same posture. Traefik does not buffer unless a middleware
	// says so, so what is asserted is the DECLARATION plus the absence of a buffering middleware — a
	// setting nobody wrote down is a setting the next person adds a middleware over.
	ingress, err := os.ReadFile(filepath.Join(root, "deploy", "k8s", "overlays", "prod", "ingress.yaml"))
	if err != nil {
		t.Fatalf("reading the prod ingress: %v", err)
	}
	k8s := string(ingress)
	if !strings.Contains(k8s, `heros.dev/response-buffering: "off"`) {
		t.Error("the Kubernetes ingress does not declare `heros.dev/response-buffering: \"off\"`.\n" +
			"The Compose substrate disables buffering and this one says nothing — which is the P29 " +
			"per-substrate drift with a different symptom.")
	}
	if strings.Contains(k8s, "buffering@") || strings.Contains(k8s, "middlewares: buffering") {
		t.Error("the ingress attaches a Traefik buffering middleware; that is the setting the annotation " +
			"above it exists to forbid")
	}

	// The AIR-GAPPED topology ships no Ingress of its own — the edge is the operator's. So what is
	// asserted is that the requirement is STATED where they will read it, with the concrete directive
	// for the proxies they are likely to be running.
	//
	// 🔴 A requirement we cannot apply is one we must at least write down. On an air-gapped install we
	// cannot see the edge at all, so a buffering hop there surfaces as "the product is slow" and nobody
	// looks at the proxy.
	airgapped, err := os.ReadFile(filepath.Join(root, "deploy", "k8s", "overlays", "airgapped", "kustomization.yaml"))
	if err != nil {
		t.Fatalf("reading the air-gapped overlay: %v", err)
	}
	air := string(airgapped)
	if !strings.Contains(air, "response-buffering") {
		t.Error("the air-gapped overlay says nothing about response buffering. Its edge is the operator's, " +
			"so the requirement has to be stated where they read it — this file.")
	}
	for _, directive := range []string{"proxy_buffering off", "http-no-delay", "conversation-stream"} {
		if !strings.Contains(air, directive) {
			t.Errorf("the air-gapped overlay does not name %q. A requirement stated abstractly is one an "+
				"operator has to translate, and the translation is where it gets dropped.", directive)
		}
	}
}
