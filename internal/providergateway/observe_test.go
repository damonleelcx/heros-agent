package providergateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// recordingObserver stands in for P2.5's instrument.
type recordingObserver struct {
	mu    sync.Mutex
	calls []CallInfo
}

func (o *recordingObserver) OnCall(_ context.Context, i CallInfo) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, i)
}

func (o *recordingObserver) get() []CallInfo {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]CallInfo(nil), o.calls...)
}

// Task 6.4: P2.5 attaches "with zero application change". That is a claim about THIS package — it
// holds only if an instrument can see a call without anyone editing Complete. This is that, proved
// from the outside: attach an Observer, make a call, see it.
func TestObserver_SeesASuccessfulCallWithTheTagsP25Needs(t *testing.T) {
	obs := &recordingObserver{}
	g, _ := testGateway(t, ProviderOpenAI, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, openAIBody)
	}, WithObserver(obs))

	e := entry(ProviderOpenAI, "gpt-5")
	seed := int64(9)
	req := simpleReq()
	req.IdempotencyKey = "run1:n_a:0"
	if _, err := g.Complete(context.Background(), e, req, &seed); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	calls := obs.get()
	if len(calls) != 1 {
		t.Fatalf("the observer saw %d calls, want exactly 1 per logical call", len(calls))
	}
	c := calls[0]
	// The tags only the gateway knows.
	if c.Provider != ProviderOpenAI || c.ModelID != "gpt-5" || c.ModelVersionID != e.VersionID {
		t.Errorf("CallInfo does not identify the model version: %+v", c)
	}
	if c.Usage.InputTokens != 10 || c.Usage.OutputTokens != 5 {
		t.Errorf("Usage = %+v; P2.5 computes cost from this", c.Usage)
	}
	if c.StopReason != StopEndTurn || c.Attempts != 1 || c.Duration <= 0 {
		t.Errorf("CallInfo = %+v", c)
	}
	// The coordinates that let an event carry P0's seven tags without re-resolving anything.
	if c.IdempotencyKey != "run1:n_a:0" {
		t.Errorf("IdempotencyKey = %q; run_id and node_id are recoverable from it", c.IdempotencyKey)
	}
	if c.Seed == nil || *c.Seed != 9 {
		t.Errorf("Seed = %v, want 9", c.Seed)
	}
	if c.Err != nil {
		t.Errorf("Err = %v on a success", c.Err)
	}
}

// An instrument that only sees successes reports a provider outage as SILENCE — precisely when
// someone is looking at it. The hook fires on every exit path.
func TestObserver_SeesFailuresToo(t *testing.T) {
	for _, tc := range []struct {
		name     string
		handler  http.HandlerFunc
		attempts int
	}{
		{"non-retryable 4xx", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}, 0},
		{"exhausted retries", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obs := &recordingObserver{}
			g, _ := testGateway(t, ProviderOpenAI, tc.handler, WithObserver(obs))
			if _, err := g.Complete(context.Background(), entry(ProviderOpenAI, "gpt-5"), simpleReq(), nil); err == nil {
				t.Fatal("expected an error")
			}
			calls := obs.get()
			if len(calls) != 1 {
				t.Fatalf("the observer saw %d calls on a failure, want 1", len(calls))
			}
			if calls[0].Err == nil {
				t.Error("CallInfo.Err is nil on a failed call; the outage would be invisible")
			}
		})
	}
}

// Retries are ONE logical call and must be ONE observation — that is what a span is. Attempts carries
// the retries, so an instrument can report them without the hook firing three times and inflating
// every count P2.5 derives.
func TestObserver_RetriesAreOneObservationCarryingTheAttemptCount(t *testing.T) {
	obs := &recordingObserver{}
	var n int
	g, _ := testGateway(t, ProviderOpenAI, func(w http.ResponseWriter, r *http.Request) {
		n++
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, openAIBody)
	}, WithObserver(obs))

	if _, err := g.Complete(context.Background(), entry(ProviderOpenAI, "gpt-5"), simpleReq(), nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	calls := obs.get()
	if len(calls) != 1 {
		t.Fatalf("3 HTTP attempts produced %d observations, want 1", len(calls))
	}
	if calls[0].Attempts != 3 {
		t.Errorf("Attempts = %d, want 3 — the retries must be reported, not lost", calls[0].Attempts)
	}
}

// The error an observer logs must already be scrubbed: logging it is what an observer is FOR, so a
// leak here would be a leak by design.
func TestObserver_ErrorHandedToTheInstrumentIsAlreadyScrubbed(t *testing.T) {
	const secret = "sk-test-openai-secret-value"
	obs := &recordingObserver{}
	g, _ := testGateway(t, ProviderOpenAI, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"bad key `+secret+`"}`)
	}, WithObserver(obs))

	if _, err := g.Complete(context.Background(), entry(ProviderOpenAI, "gpt-5"), simpleReq(), nil); err == nil {
		t.Fatal("expected an error")
	}
	calls := obs.get()
	if len(calls) != 1 || calls[0].Err == nil {
		t.Fatalf("observer calls = %+v", calls)
	}
	if strings.Contains(calls[0].Err.Error(), secret) {
		t.Errorf("the credential reached the instrument: %v", calls[0].Err)
	}
}

// The zero Gateway has no observer and must not panic. P2 ships no instrumentation.
func TestObserver_NilIsSafe(t *testing.T) {
	g, _ := testGateway(t, ProviderOpenAI, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, openAIBody)
	})
	if _, err := g.Complete(context.Background(), entry(ProviderOpenAI, "gpt-5"), simpleReq(), nil); err != nil {
		t.Fatalf("a gateway with no observer failed: %v", err)
	}
}

var _ = httptest.NewServer
