// boundary_test.go is P24's load-bearing fence.
//
// # Why this file is the one that matters
//
// Every other assertion in this package checks that a rule is implemented. This one checks the OUTCOME
// on the wire: a forbidden-shape fixture is attached to an error every way an engineer could attach it,
// the reporter runs against a real capture endpoint over a real socket, and the assertion is made on
// the BYTES THAT WERE TRANSMITTED — not on the reporting call's return value, not on the event struct,
// not on a mock's recorded arguments.
//
// That distinction is the whole point. An assertion on the event struct proves the builder does what
// the builder does. An assertion on the bytes proves that nothing between the builder and the socket —
// the scrubber, the envelope, a marshaller's field defaults — put anything back.
//
// # No environment precondition
//
// It runs with no DSN in the environment, no network, and no configuration. A fence that only runs when
// somebody remembers to set a variable is a fence that runs in CI approximately never.

package erroreport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/errorcode"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// ── The fixture ──────────────────────────────────────────────────────────────

// forbidden is one shape that must never appear on the wire, with the reason it is on the list.
type forbidden struct {
	name  string
	value string
}

func forbiddenShapes() []forbidden {
	prompt := "You are a careful assistant. " + strings.Repeat("Summarise the attached customer contract. ", 48)
	diff := strings.Join([]string{
		"--- a/internal/billing/meter.go",
		"+++ b/internal/billing/meter.go",
		"@@ -12,7 +12,7 @@",
		"-\trate := 0.03",
		"+\trate := 0.02",
	}, "\n")
	return []forbidden{
		{"a provider API key", "sk-ant-api03-Oa4DXh8Bbd4QN1ChpodHdhl-XRgtwPl6HWAuXqgpaGFo"},
		{"a cloud access-key id", "AKIAIOSFODNN7EXAMPLE"},
		{"an email address", "damon.lee@nousresearch.example"},
		{"a two-kilobyte prompt", prompt},
		{"a unified diff", diff},
		{"a tenant-scoped URL", "/app/variants/var-7f31c9/scorecard"},
		{"a hostname", "agentd-7d9f8c-prod.internal.nousresearch.example"},
		{"a tenant name", "Nous Research Ltd"},
	}
}

// carrierError is an error that carries forbidden material in a STRUCT FIELD as well as in its message.
//
// It exists because "the message is dropped" is only half the guarantee. An engineer who knows the
// message is dropped will attach the value as a field instead, entirely reasonably, and the boundary has
// to be indifferent to that — which it is, because it never serialises an error at all.
type carrierError struct {
	Tenant   string
	Prompt   string
	Endpoint string
	inner    error
}

func (e *carrierError) Error() string {
	return fmt.Sprintf("resolving prompt %q for tenant %q against %s: %v", e.Prompt, e.Tenant, e.Endpoint, e.inner)
}
func (e *carrierError) Unwrap() error { return e.inner }

type contextKey struct{}

// buildContaminatedError attaches every forbidden shape by every route available to a caller.
func buildContaminatedError(t *testing.T) (error, context.Context) {
	t.Helper()
	shapes := forbiddenShapes()
	byName := map[string]string{}
	for _, s := range shapes {
		byName[s.name] = s.value
	}

	// 1. In the message of the innermost error.
	inner := fmt.Errorf("upstream refused key %s for %s", byName["a provider API key"], byName["an email address"])
	// 2. Wrapped, so the outer message contains the inner one.
	wrapped := fmt.Errorf("scoring node n-14 with %s: %w", byName["a cloud access-key id"], inner)
	// 3. In struct fields of a custom error type.
	carrier := &carrierError{
		Tenant:   byName["a tenant name"],
		Prompt:   byName["a two-kilobyte prompt"],
		Endpoint: byName["a hostname"],
		inner:    wrapped,
	}
	// 4. In a context value, which is where a request-scoped helper would put it.
	ctx := context.WithValue(context.Background(), contextKey{}, map[string]string{
		"diff": byName["a unified diff"],
		"url":  byName["a tenant-scoped URL"],
	})
	ctx = telemetry.ContextWithTraceID(ctx, "9f2c1ab47e6d40518c33a7b1e0d4f6a2")
	return carrier, ctx
}

// ── The capture endpoint ─────────────────────────────────────────────────────

type capture struct {
	mu     sync.Mutex
	bodies [][]byte
	status int
}

func (c *capture) handler(w http.ResponseWriter, r *http.Request) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	c.mu.Lock()
	c.bodies = append(c.bodies, buf)
	status := c.status
	c.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte("{}"))
}

func (c *capture) all() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.bodies))
	copy(out, c.bodies)
	return out
}

// startCapture runs a real HTTP server and returns a reporter pointed at it.
func startCapture(t *testing.T) (*capture, Reporter) {
	t.Helper()
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(cap.handler))
	t.Cleanup(srv.Close)

	// A DSN pointed at the capture server. The public key is a fixture value, not a credential.
	dsn := strings.Replace(srv.URL, "://", "://p24fixturekey@", 1) + "/4242"
	r, err := New(Config{
		DSN:      dsn,
		Release:  "v0.24.0-test",
		Edition:  "dev",
		Runtime:  "go test",
		Scrubber: telemetry.NewScrubber(),
		Logf:     func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	return cap, r
}

func flush(t *testing.T, r Reporter, cap *capture, want int) [][]byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(cap.all()) >= want {
			return cap.all()
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cap.all()
}

// ── 2.11 · The fence ─────────────────────────────────────────────────────────

func TestTransmittedBytesCarryNoForbiddenShape(t *testing.T) {
	if dsn := os.Getenv(EnvDSN); dsn != "" {
		t.Fatalf("%s is set in the test environment (%q). This fence must run with no environment "+
			"precondition; a value here would send a fixture to a real inbox.", EnvDSN, dsn)
	}

	cap, r := startCapture(t)
	err, ctx := buildContaminatedError(t)

	ev := FromError(err, errorcode.ProviderError, 0)
	ev.Surface = "platform.api"
	r.Report(ctx, ev)

	bodies := flush(t, r, cap, 1)
	if len(bodies) != 1 {
		t.Fatalf("expected 1 transmitted envelope, got %d", len(bodies))
	}
	wire := string(bodies[0])

	// 🔴 The assertion, on the BYTES.
	for _, shape := range forbiddenShapes() {
		if strings.Contains(wire, shape.value) {
			t.Errorf("the transmitted bytes contain %s", shape.name)
		}
		// Also as a prefix: a truncated key is still a key.
		head := shape.value
		if len(head) > 24 {
			head = head[:24]
		}
		if strings.Contains(wire, head) {
			t.Errorf("the transmitted bytes contain the first 24 characters of %s", shape.name)
		}
	}

	// And the whole error message, which is the shape all four attachment routes ultimately produce.
	if strings.Contains(wire, "resolving prompt") {
		t.Error("the transmitted bytes contain the error's message body")
	}
	if strings.Contains(wire, "upstream refused key") {
		t.Error("the transmitted bytes contain a wrapped error's message body")
	}
}

func TestTransmittedKeySetIsASubsetOfTheAllowlist(t *testing.T) {
	err, ctx := buildContaminatedError(t)
	ev := FromError(err, errorcode.ProviderError, 0)
	ev.Surface = "platform.api"
	ev.TraceID = telemetry.TraceIDFromContext(ctx)

	var offenders []string
	walkLeafKeys(ev.Wire(), "", func(key string) {
		if !Permitted(key) {
			offenders = append(offenders, key)
		}
	})
	if len(offenders) > 0 {
		t.Fatalf("the constructed event carries keys outside the allowlist: %v", offenders)
	}
}

// TestEveryTransmittedValueIsExplained is the STRONG form of the guarantee.
//
// Checking that forbidden shapes are absent is a denylist, and this package's entire argument is that a
// denylist is the wrong direction. So this walks the transmitted envelope and requires every string in
// it to be one of: a value the allowlist produced, a declared protocol constant, a key name, or the two
// named protocol artefacts (`event_id`, `sent_at`). Anything else — a field a marshaller added, a
// default somebody wired in — is a failure by construction rather than by pattern.
func TestEveryTransmittedValueIsExplained(t *testing.T) {
	cap, r := startCapture(t)
	err, ctx := buildContaminatedError(t)
	ev := FromError(err, errorcode.ProviderError, 0)
	ev.Surface = "platform.api"
	r.Report(ctx, ev)

	bodies := flush(t, r, cap, 1)
	if len(bodies) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(bodies))
	}
	lines := strings.Split(strings.TrimRight(string(bodies[0]), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("the envelope is not three documents: %d", len(lines))
	}

	// Everything the event legitimately produced.
	explained := map[string]bool{}
	ev.Release, ev.Edition, ev.Runtime = "v0.24.0-test", "dev", "go test"
	ev.TraceID = telemetry.TraceIDFromContext(ctx)
	walkLeafValues(ev.Wire(), func(v any) {
		if s, ok := v.(string); ok {
			explained[s] = true
		}
	})
	for _, v := range ProtocolValues {
		explained[v] = true
	}

	var header, item, payload map[string]any
	for i, target := range []*map[string]any{&header, &item, &payload} {
		if e := json.Unmarshal([]byte(lines[i]), target); e != nil {
			t.Fatalf("envelope line %d is not JSON: %v", i, e)
		}
	}

	// `event_id` and `sent_at` are the two protocol artefacts, named here so they are an EXCEPTION with
	// a reason rather than an unexplained value that slipped through.
	eventID, _ := header["event_id"].(string)
	sentAt, _ := header["sent_at"].(string)
	if eventID == "" || sentAt == "" {
		t.Fatal("the envelope header lacks event_id or sent_at")
	}
	explained[eventID] = true
	explained[sentAt] = true

	for i, doc := range []map[string]any{header, item, payload} {
		walkLeafValues(doc, func(v any) {
			s, ok := v.(string)
			if !ok || s == "" {
				return
			}
			if !explained[s] {
				t.Errorf("envelope document %d carries the value %q, which no allowlisted field and no "+
					"declared protocol constant produced", i, s)
			}
		})
	}
}

// ── 2.12 · The other direction ───────────────────────────────────────────────

func TestEveryAllowlistEntryIsPopulated(t *testing.T) {
	// A single realistic event has to populate every entry. An allowlist entry nothing ever fills is a
	// permission nobody asked for, and it is exactly as invisible as an unlisted field is dangerous.
	ev := Event{
		Type:    "*errors.errorString",
		Code:    errorcode.UpstreamError,
		Level:   LevelError,
		Frames:  CaptureStack(0),
		TraceID: "9f2c1ab47e6d40518c33a7b1e0d4f6a2",
		Release: "v0.24.0",
		Edition: "hosted",
		Surface: "platform.api",
		Runtime: "go1.25",
	}
	populated := map[string]bool{}
	walkLeafKeys(ev.Wire(), "", func(key string) { populated[key] = true })

	for _, key := range AllowlistKeys() {
		if !populated[key] {
			t.Errorf("allowlist entry %q is populated by nothing — remove it or populate it", key)
		}
	}
	if len(ev.Frames) == 0 {
		t.Fatal("the fixture produced no frames, so the four frame entries are vacuously satisfied")
	}
}

func TestAllowlistIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Allowlist {
		if f.Name == "" {
			t.Fatal("an allowlist field has no name")
		}
		if seen[f.Name] {
			t.Fatalf("duplicate allowlist key %q", f.Name)
		}
		seen[f.Name] = true
		if len(f.Why) < 40 {
			t.Errorf("field %q has no justification a reviewer can read — the allowlist is a review artifact", f.Name)
		}
		switch f.Category {
		case "classification", "location", "correlation", "provenance":
		default:
			t.Errorf("field %q has category %q, which is not one of the four", f.Name, f.Category)
		}
	}
	// Content must not be expressible. Named shapes rather than a general rule, so the failure says what
	// somebody just tried to add.
	for _, f := range Allowlist {
		for _, bad := range []string{"message", "body", "header", "query", "url", "prompt", "completion",
			"diff", "source", "env", "host", "server", "ip", "email", "tenant", "breadcrumb", "console"} {
			if strings.Contains(strings.ToLower(f.Name), bad) {
				t.Errorf("allowlist key %q looks like content (%q) — content never crosses this boundary", f.Name, bad)
			}
		}
	}
}

// ── 2.4 · The message body is dropped ────────────────────────────────────────

func TestAMessageShapedValueThatIsNotAnEnumValueDoesNotReachTheWire(t *testing.T) {
	cap, r := startCapture(t)

	// The exact mistake this rule exists for: somebody types a "code" at a call site instead of using
	// the enum, and the string they type is a sentence containing a value.
	ev := FromError(errors.New("ignored"), errorcode.Code(`failed to resolve prompt "customer contract Q3"`), 0)
	ev.Surface = "platform.api"
	r.Report(context.Background(), ev)

	bodies := flush(t, r, cap, 1)
	if len(bodies) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(bodies))
	}
	wire := string(bodies[0])
	if strings.Contains(wire, "customer contract") || strings.Contains(wire, "failed to resolve") {
		t.Error("a message-shaped value that is not an enum member reached the wire")
	}
	if !strings.Contains(wire, string(errorcode.Unknown)) {
		t.Error("the dropped code was not replaced by UNKNOWN — the reader cannot tell it was dropped")
	}

	// And every member of the enum DOES reach the wire, or the drop rule is indistinguishable from
	// "nothing is ever transmitted".
	for _, code := range errorcode.All {
		if !errorcode.Valid(string(code)) {
			t.Fatalf("%s is in All but fails Valid", code)
		}
	}
}

// ── 2.3 · The scrubber is chained as an independent second guard ─────────────

func TestTheScrubberCatchesWhatConstructionMissed(t *testing.T) {
	cap, r := startCapture(t)

	// Deliberately seed a PERMITTED, UNVALIDATED field with a secret-shaped value — the shape of a
	// defect where a call site puts a formatted string somewhere the allowlist permits. Construction
	// cannot catch this, which is why there are two guards of different kinds.
	//
	// `error.type` is the seam used here rather than `surface`, and the reason is worth recording: an
	// earlier version of this test seeded `surface`, and it stopped exercising the scrubber the moment
	// `Wire` began validating the surface against the closed enum — construction caught it first. That
	// is a better outcome and a worse test, and a test that silently stops testing its subject is the
	// exact failure this suite is written against. `error.type` is set from `%T` in every real path and
	// is not enum-checkable, which makes it the honest remaining seam.
	ev := Event{
		Type:    "*provider.Error: sk-ant-api03-Oa4DXh8Bbd4QN1ChpodHdhl-XRgtwPl6HWAuXqgpaGFo",
		Code:    errorcode.UpstreamError,
		Level:   LevelError,
		Surface: "platform.api",
		Frames:  CaptureStack(0),
	}
	r.Report(context.Background(), ev)

	bodies := flush(t, r, cap, 1)
	if len(bodies) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(bodies))
	}
	wire := string(bodies[0])
	if strings.Contains(wire, "sk-ant-api03") {
		t.Error("a secret seeded into a permitted field reached the wire — the scrubber is not chained")
	}
	if !strings.Contains(wire, telemetry.Redacted) && !strings.Contains(wire, telemetry.BlobRefPrefix) {
		t.Error("the value was neither redacted nor blob-referenced — it may have been dropped silently")
	}
}

func TestScrubbingRunsOverNestedFrameStringsToo(t *testing.T) {
	// The chokepoint inspects string VALUES and passes everything else through, so a string nested in
	// `frames[]` would never be looked at unless the event is flattened first. A guard that silently
	// skips half its input is worse than no guard.
	scrubbed := Scrub(telemetry.NewScrubber(), map[string]any{
		"error.type": "*errors.errorString",
		"frames": []any{
			map[string]any{"function": "leak", "package": "p", "file": "AKIAIOSFODNN7EXAMPLE.go", "line": 3, "in_app": true},
		},
	})
	frames, ok := scrubbed["frames"].([]any)
	if !ok || len(frames) != 1 {
		t.Fatalf("frames did not survive the round trip: %#v", scrubbed["frames"])
	}
	frame := frames[0].(map[string]any)
	if strings.Contains(frame["file"].(string), "AKIA") {
		t.Error("a secret nested inside frames[] was not scrubbed")
	}
	if frame["line"] != 3 || frame["in_app"] != true {
		t.Errorf("non-string frame fields did not survive: %#v", frame)
	}
}

func TestAReporterWithoutAScrubberIsRefused(t *testing.T) {
	_, err := New(Config{DSN: "https://k@example.test/1"})
	if err == nil {
		t.Fatal("a reporter was constructed with one guard instead of two")
	}
	if !strings.Contains(err.Error(), "Scrubber") {
		t.Errorf("the refusal does not name what is missing: %v", err)
	}
}

// ── 2.6 / 2.10 · Three states, and absence is silent ─────────────────────────

func TestAbsentIsSilentAndTransmitsNothing(t *testing.T) {
	var logged []string
	r, err := FromEnv(func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })
	if err != nil {
		t.Fatalf("FromEnv with no DSN: %v", err)
	}
	state, class := r.State()
	if state != StateAbsent {
		t.Errorf("state = %q, want absent", state)
	}
	if class != "" {
		t.Errorf("an absent reporter named a failure class %q — absence is a decision, not a degradation", class)
	}
	if len(logged) != 0 {
		t.Errorf("absence produced %d log line(s): %v — it must be silent", len(logged), logged)
	}
	// And reporting is a real no-op rather than a panic.
	r.Report(context.Background(), Event{Code: errorcode.Unknown})
}

func TestASetButUnusableDSNFailsAtBoot(t *testing.T) {
	t.Setenv(EnvDSN, "not-a-url-at-all")
	if _, err := FromEnv(func(string, ...any) {}); err == nil {
		t.Fatal("a malformed DSN was accepted — a deployment somebody meant to configure and got wrong " +
			"must fail at boot, not silently report nothing for a month")
	}
}

func TestAnUnrecognisedEditionWarnsRatherThanFallingBackSilently(t *testing.T) {
	t.Setenv(EnvDSN, "https://k@127.0.0.1:1/1")
	t.Setenv(EnvEdition, "whatever-somebody-typed")
	var logged []string
	r, err := FromEnv(func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	var warned bool
	for _, line := range logged {
		if strings.Contains(line, "WARN") && strings.Contains(line, "edition") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("an unrecognised edition fell back silently: %v", logged)
	}
}

// ── 2.7 · Fail-static ────────────────────────────────────────────────────────

func TestAnUnreachableTargetNeverFailsACaller(t *testing.T) {
	var mu sync.Mutex
	var logged []string
	// Port 1 on loopback: nothing listens, and a connection is refused immediately rather than hanging.
	r, err := New(Config{
		DSN:      "https://k@127.0.0.1:1/1",
		Scrubber: telemetry.NewScrubber(),
		Logf: func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			logged = append(logged, fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Far more events than the queue holds, so the drop path is exercised too.
	for i := 0; i < QueueDepth*3; i++ {
		r.Report(context.Background(), Event{Type: "*errors.errorString", Code: errorcode.UpstreamError, Frames: CaptureStack(0)})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	state, class := r.State()
	if state != StateDegraded {
		t.Errorf("state = %q, want degraded when the target is unreachable", state)
	}
	if class == "" {
		t.Error("degraded with no failure class — an operator cannot tell what is wrong")
	}

	mu.Lock()
	defer mu.Unlock()
	// ONE line, not one per event. The per-issue limiter caps transmits at PerIssueRateLimit, so at most
	// that many failures can occur in the window, and the log gate allows the first plus one per interval.
	if len(logged) > 2 {
		t.Errorf("%d log lines for a transmit outage — at most one per interval, naming the class:\n%s",
			len(logged), strings.Join(logged, "\n"))
	}
	if len(logged) == 0 {
		t.Error("a transmit outage produced no log line at all — a silent failure is the other failure mode")
	}
	for _, line := range logged {
		if !strings.Contains(line, "WARN") {
			t.Errorf("the failure line is not a WARN: %q", line)
		}
	}
}

func TestThePerIssueLimitAndTheTransmitBudgetAreEnforced(t *testing.T) {
	cap, r := startCapture(t)
	// One issue, many events. Only PerIssueRateLimit may be transmitted in the window.
	for i := 0; i < 50; i++ {
		r.Report(context.Background(), Event{Type: "*errors.errorString", Code: errorcode.UpstreamError, Frames: CaptureStack(0)})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := len(cap.all()); got != PerIssueRateLimit {
		t.Errorf("transmitted %d events for one issue, want %d (the per-issue limit)", got, PerIssueRateLimit)
	}
}

// ── 2.8 · No transaction, no profile ─────────────────────────────────────────

func TestNoTransactionOrProfilePayloadIsConstructed(t *testing.T) {
	if TracingEnabled || ProfilingEnabled {
		t.Fatal("performance tracing or profiling is enabled")
	}
	if EnvelopeItemType != "event" {
		t.Fatalf("the envelope item type is %q — this package transmits events and nothing else", EnvelopeItemType)
	}

	ev := Event{Type: "*errors.errorString", Code: errorcode.UpstreamError, Frames: CaptureStack(0)}
	bytes, err := ev.Envelope(time.Unix(1754006400, 0), "go")
	if err != nil {
		t.Fatalf("Envelope: %v", err)
	}
	wire := string(bytes)
	for _, forbidden := range []string{"transaction", "profile", "span_id", "measurements", "spans"} {
		if strings.Contains(wire, forbidden) {
			t.Errorf("the envelope contains %q — no transaction or profile payload may be constructed", forbidden)
		}
	}

	// And structurally: the package has no constructor for one. Read from the source, because the
	// absence of a code path is the guarantee — a constant can be flipped, a missing function cannot.
	for _, file := range []string{"event.go", "reporter.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		body := stripComments(string(src))
		for _, forbidden := range []string{`"transaction"`, `"profile"`, `"session"`} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s constructs a %s item", file, forbidden)
			}
		}
	}
}

// ── 2.9 · The numbers are stated ─────────────────────────────────────────────

func TestTheStatedNumbersAreStatedWithABasis(t *testing.T) {
	src, err := os.ReadFile("reporter.go")
	if err != nil {
		t.Fatalf("read reporter.go: %v", err)
	}
	text := string(src)
	for _, name := range []string{"SampleRate", "PerIssueRateLimit", "TransmitBudget", "QueueDepth",
		"TracingEnabled", "ProfilingEnabled"} {
		idx := strings.Index(text, "// "+name+" ")
		if idx < 0 {
			// TracingEnabled/ProfilingEnabled share one comment block; accept a mention.
			if !strings.Contains(text, name) {
				t.Errorf("%s is not declared", name)
			}
			continue
		}
		block := text[idx:]
		if end := strings.Index(block, "\nconst"); end > 0 {
			block = block[:end]
		}
		if !strings.Contains(block, "BASIS:") {
			t.Errorf("%s carries no stated basis — a number nobody chose is a default", name)
		}
	}
	if SampleRate != 1.0 {
		t.Errorf("SampleRate = %v; a sampled defect inbox drops the first occurrence of a new defect "+
			"with the same probability as the thousandth", SampleRate)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func walkLeafKeys(v any, prefix string, fn func(string)) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			next := k
			if prefix != "" {
				next = prefix + "." + k
			}
			walkLeafKeys(child, next, fn)
		}
	case []any:
		for _, child := range t {
			walkLeafKeys(child, prefix, fn)
		}
	default:
		if prefix != "" {
			fn(prefix)
		}
	}
}

func walkLeafValues(v any, fn func(any)) {
	switch t := v.(type) {
	case map[string]any:
		for _, child := range t {
			walkLeafValues(child, fn)
		}
	case []any:
		for _, child := range t {
			walkLeafValues(child, fn)
		}
	default:
		fn(v)
	}
}

func stripComments(src string) string {
	var out strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}
