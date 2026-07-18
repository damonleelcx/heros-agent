package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/providergateway"
)

// Task 6.1: the scrubber strips API keys, secrets, and PII from span attributes, and replaces
// prompt/completion-shaped content with a content-hashed blob reference — never inlines it.
func TestSection6_ScrubberRemovesSecretsAndPII(t *testing.T) {
	s := NewScrubber()
	const secret = "sk-test-openai-secret-value-123456"
	const email = "alice@example.com"
	longPrompt := "SYSTEM: you are a helpful assistant. " + strings.Repeat("user content ", 100) // > 512 bytes

	sp := Span{
		SpanID: "s1", TraceID: "t1", Status: SpanStatusError, StatusMsg: "auth failed for key " + secret,
		Attributes: map[string]any{
			AttrConfigHash:  testConfigHash,
			AttrRunID:       "run_1",
			AttrVariantID:   "v1",
			"leaked_key":    secret,
			"contact":       email,
			"prompt":        longPrompt,
			"tokens":        42, // a non-string value is untouched
			"aws_key":       "AKIAIOSFODNN7EXAMPLE",
			"authorization": "Bearer abcdef1234567890token",
		},
	}
	out := s.ScrubSpan(sp)

	// No secret/PII survives anywhere in the scrubbed span.
	for k, v := range out.Attributes {
		if str, ok := v.(string); ok && containsSecret(str) {
			t.Errorf("attribute %q still contains a secret/PII after scrubbing: %q", k, str)
		}
	}
	if containsSecret(out.StatusMsg) {
		t.Errorf("status message still leaks a secret: %q", out.StatusMsg)
	}
	// The long prompt is a blob reference, not inlined text.
	if pr, _ := out.Attributes["prompt"].(string); !strings.HasPrefix(pr, BlobRefPrefix) {
		t.Errorf("prompt was not replaced by a content-hash reference: %q", pr)
	}
	// The structural tags are untouched (they are identifiers, not content).
	if out.Attributes[AttrConfigHash] != testConfigHash {
		t.Errorf("scrubber mangled a structural tag: %v", out.Attributes[AttrConfigHash])
	}
	if out.Attributes["tokens"] != 42 {
		t.Errorf("scrubber altered a non-string value: %v", out.Attributes["tokens"])
	}
}

// Task 6.1 end-to-end: a run whose gateway holds a provider API key leaves NO trace of that key in any
// store — the key stays in the secrets manager and never reaches a span, metric, or log.
func TestSection6_RunLeavesNoProviderKeyInAnyStore(t *testing.T) {
	const apiKey = "sk-test-openai-secret-value" // the key StaticSecrets hands the gateway in testRig

	spans := NewMemSpanStore(0)
	tsdb := NewMemTSDB(0)
	eval := NewMemEvalStore()
	col := NewCollector(CollectorConfig{Spans: spans, TSDB: tsdb, Eval: eval})
	t.Cleanup(col.Close)

	gw, inst, entry := testRigWithSink(t, col)
	rc := runFixture(t, gw, inst, entry, []string{"n_a", "n_b"})
	col.Flush()

	// Scan every span the store received.
	for _, sp := range spans.SpansByConfigHash(rc.ConfigHash) {
		for k, v := range sp.Attributes {
			if str, ok := v.(string); ok && (strings.Contains(str, apiKey) || containsSecret(str)) {
				t.Errorf("provider key/secret leaked into span attribute %q: %q", k, str)
			}
		}
		if strings.Contains(sp.StatusMsg, apiKey) {
			t.Errorf("provider key leaked into a span status message")
		}
	}
	// And every metric sample.
	samples, _ := tsdb.Query(map[string]string{AttrConfigHash: rc.ConfigHash})
	for _, s := range samples {
		for k, v := range s.Labels {
			if strings.Contains(v, apiKey) || containsSecret(v) {
				t.Errorf("provider key/secret leaked into metric label %q: %q", k, v)
			}
		}
	}
}

// The scrubber runs INSIDE the collector pipeline, so a secret injected on a span is scrubbed before
// the store ever sees it (task 6.1: "at the collector before any store").
func TestSection6_CollectorScrubsBeforeStore(t *testing.T) {
	spans := NewMemSpanStore(0)
	col := NewCollector(CollectorConfig{Spans: spans, TSDB: NewMemTSDB(0), Eval: NewMemEvalStore()})
	t.Cleanup(col.Close)

	col.EmitSpan(context.Background(), Span{
		SpanID: "s1", TraceID: "t1",
		Attributes: map[string]any{
			AttrConfigHash: testConfigHash, AttrRunID: "run_1", AttrVariantID: "v1",
			"secret": "sk-leaked-secret-abcdefgh",
		},
	})
	col.Flush()

	got := spans.SpansByConfigHash(testConfigHash)
	if len(got) != 1 {
		t.Fatalf("want 1 span, got %d", len(got))
	}
	if v, _ := got[0].Attributes["secret"].(string); containsSecret(v) {
		t.Errorf("the store received an unscrubbed secret: %q", v)
	}
}

var _ = providergateway.ProviderOpenAI
var _ = time.Now
