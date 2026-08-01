package erroreport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/errorcode"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// State is an integration's honest state. THREE values, never a bool.
//
// A boolean forces two different facts into one bit: "we chose not to configure this" and "we
// configured it and it is failing" both render as `false`, and an operator reading a dashboard cannot
// tell a deliberate absence from an outage. That distinction is the whole reason this type exists —
// on most substrates the correct state is `absent`, and `absent` must be quiet.
type State string

const (
	// StateAbsent means no DSN was configured. It is a DECISION, not a degradation, and it is silent:
	// no warning, no log line, no readiness noise. Every substrate except the platform's own hosted
	// deployment is expected to be here.
	StateAbsent State = "absent"
	// StateConfigured means a target is configured and transmission is working.
	StateConfigured State = "configured"
	// StateDegraded means configured and failing. It NAMES the failure class, because "degraded" with
	// no subject sends an operator to read three dashboards to learn what the signal already knew.
	StateDegraded State = "degraded"
)

// Reporter is fail-static and non-blocking.
//
// `Report` never returns an error. That is deliberate and it is a safety property, not an ergonomic
// one: an error return invites a call site to check it, and a call site that checks it eventually
// becomes a call site that fails a customer's request because an incident inbox was unreachable. The
// only thing a caller can do about a failed transmit is nothing, so the signature says so.
type Reporter interface {
	// Report enqueues an event. It never blocks, never retries into an unbounded queue, never panics,
	// and never fails a served request. `ctx` carries the existing trace id.
	Report(ctx context.Context, ev Event)
	// State returns the state and, when degraded, the failure class.
	State() (State, string)
	// Close flushes what is already queued, within a bound, and stops the worker.
	Close(ctx context.Context) error
}

// ── The stated numbers (task 2.9) ────────────────────────────────────────────
//
// Each carries its basis. A default inherited from a library is a number nobody chose, and the first
// time it matters is the incident where somebody asks why half the events are missing.

// SampleRate is 1.0 — every event is transmitted.
//
// BASIS: this integration is a defect INBOX, not a rate source. Every frequency question ("how often",
// "how many tenants", "is it getting worse") is answered from the telemetry substrate, where events are
// complete and joined to runs. Sampling would buy nothing here and would cost the one thing an inbox is
// for: the first occurrence of a new defect is the occurrence that matters, and a sampled inbox drops it
// with the same probability as the thousandth.
const SampleRate = 1.0

// PerIssueRateLimit is the maximum number of events transmitted per issue per interval, where an
// "issue" is the (error.code, error.type, top platform frame) triple.
//
// BASIS: a failing hot path produces thousands of identical events per minute. Beyond the first few,
// each additional copy tells an operator nothing and costs transmit budget that a DIFFERENT, rarer
// defect needs. Five per interval keeps the first occurrence, keeps enough to see a burst, and cannot
// crowd out a neighbour.
const PerIssueRateLimit = 5

// TransmitBudget is the maximum number of events transmitted per interval across all issues.
//
// BASIS: a bound that holds even when the per-issue limiter is defeated by an error that varies its
// type or frame each time — which is what a failure inside a loop over heterogeneous input looks like.
// 60 per minute is one per second sustained, well below anything that competes with served traffic for
// sockets, and far above the rate at which a human can read an inbox.
const TransmitBudget = 60

// RateInterval is the window both limits are stated over, and also the interval at which at most ONE
// failure line is logged.
const RateInterval = time.Minute

// TransmitTimeout bounds a single transmit. Short on purpose: this happens out of band, and a slow
// ingest endpoint must not accumulate goroutines behind it.
const TransmitTimeout = 3 * time.Second

// TracingEnabled and ProfilingEnabled are OFF, stated rather than omitted (task 2.8).
//
// Stating them matters more than it looks. "We did not enable tracing" and "tracing is off" read the
// same in a green build and differ completely in a review: the first is a property of what somebody
// happened not to write, and the next person adding a feature has no reason to think it was a decision.
//
// The refusal is also structural, not merely configured. This package speaks the ingest envelope
// directly and has exactly one item type — `event`. There is no code path that constructs a
// transaction or a profile item, so there is nothing to accidentally re-enable, and a test asserts
// that the package contains no such constructor rather than trusting these two constants.
//
// BASIS: latency is already measured by the telemetry substrate, per call, joined to runs, complete and
// unsampled. A second latency source held by a third party, sampled, and ad-blocked would be a
// systematically wrong number that nobody can quantify — and the platform's rule is that a claim you
// cannot join to its evidence is not a claim. Profiling is refused for a stronger reason: a profile
// carries function-level timing of code paths operating on customer content, which is structure this
// boundary has no way to bound.
const (
	TracingEnabled   = false
	ProfilingEnabled = false
)

// EnvelopeItemType is the ONE item type this package transmits.
const EnvelopeItemType = "event"

// QueueDepth is the bounded hand-off between the request path and the transmitter.
//
// BASIS: bounded, and OVERFLOW IS A DROP rather than a block or a growing buffer. Blocking would put
// an incident inbox in the request path; growing would turn a transmit outage into a memory leak that
// ends as a crash — which is the failure this integration is supposed to report, caused by the
// reporter. 256 is comfortably more than the per-interval transmit budget, so the queue is never the
// binding constraint under normal operation and is only reached when transmission has already stopped.
const QueueDepth = 256

// ── Absent ───────────────────────────────────────────────────────────────────

// absentReporter is what every substrate except the platform's own hosted deployment gets.
//
// It is a real implementation rather than a nil check at each call site, so a caller cannot forget one
// and so `absent` is exercised by the same code path as `configured`.
type absentReporter struct{}

// Absent returns the reporter for a deployment with no configured target. Reporting is a no-op and the
// absence is silent.
func Absent() Reporter { return absentReporter{} }

func (absentReporter) Report(context.Context, Event) {}
func (absentReporter) State() (State, string)        { return StateAbsent, "" }
func (absentReporter) Close(context.Context) error   { return nil }

// ── Configured ───────────────────────────────────────────────────────────────

// Config is everything a live reporter needs. Nothing is read from ambient state at report time.
type Config struct {
	// DSN is the ingest target. Empty means absent, which is the default on every substrate except the
	// platform's own hosted deployment.
	DSN string
	// Release, Edition and Runtime are stamped onto every event.
	Release string
	Edition string
	Runtime string
	// Scrubber is the independent second guard. Required: a reporter constructed without one is a
	// reporter with one guard, and the constructor refuses rather than defaulting to nil.
	Scrubber telemetry.Scrubber
	// Client is the HTTP client used to transmit. Injected so a test can capture the exact bytes.
	Client *http.Client
	// Now is the clock, injected for the same reason.
	Now func() time.Time
	// Logf is where the at-most-one-per-interval failure line goes.
	Logf func(format string, args ...any)
}

// parsedDSN is the ingest target, decomposed.
type parsedDSN struct {
	endpoint  string // the full envelope URL
	publicKey string
}

// parseDSN decomposes an ingest DSN into the envelope endpoint and its public key.
//
// A DSN is `https://<public key>@<host>/<project id>`. It is parsed rather than pattern-matched so a
// malformed value fails at construction — at boot, loudly — instead of at the first exception, which is
// the worst possible moment to discover a configuration error.
func parseDSN(raw string) (parsedDSN, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return parsedDSN{}, fmt.Errorf("malformed ingest DSN: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return parsedDSN{}, errors.New("ingest DSN must be an http(s) URL")
	}
	if u.User == nil || u.User.Username() == "" {
		return parsedDSN{}, errors.New("ingest DSN carries no public key")
	}
	project := strings.Trim(u.Path, "/")
	if project == "" {
		return parsedDSN{}, errors.New("ingest DSN carries no project id")
	}
	return parsedDSN{
		endpoint:  fmt.Sprintf("%s://%s/api/%s/envelope/", u.Scheme, u.Host, project),
		publicKey: u.User.Username(),
	}, nil
}

// httpReporter is the live reporter.
type httpReporter struct {
	cfg      Config
	dsn      parsedDSN
	queue    chan Event
	done     chan struct{}
	closeOne sync.Once

	mu           sync.Mutex
	failureClass string
	lastLogged   time.Time
	windowStart  time.Time
	windowCount  int
	perIssue     map[string]int
}

// New builds a reporter from a configuration.
//
// An empty DSN returns the ABSENT reporter with no error and no log line: absence is a supported,
// tested, silent configuration rather than a degraded one. A NON-empty but malformed DSN is an error,
// because that is a deployment somebody meant to configure and got wrong.
func New(cfg Config) (Reporter, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return Absent(), nil
	}
	if cfg.Scrubber == nil {
		return nil, errors.New("erroreport: a reporter needs a Scrubber — construction is the first guard, " +
			"scrubbing is the independent second one, and one guard is not the design")
	}
	dsn, err := parseDSN(cfg.DSN)
	if err != nil {
		return nil, err
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: TransmitTimeout}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	r := &httpReporter{
		cfg:      cfg,
		dsn:      dsn,
		queue:    make(chan Event, QueueDepth),
		done:     make(chan struct{}),
		perIssue: map[string]int{},
	}
	go r.run()
	return r, nil
}

// Report stamps the event with provenance and hands it to the transmitter without blocking.
func (r *httpReporter) Report(ctx context.Context, ev Event) {
	ev.Release = r.cfg.Release
	ev.Edition = r.cfg.Edition
	ev.Runtime = r.cfg.Runtime
	if ev.TraceID == "" {
		// The EXISTING identity, taken from the request context. Nothing is minted when it is absent:
		// an event with no trace id is honestly uncorrelated, and inventing one would produce a value
		// that resolves nothing while looking exactly like a value that does.
		ev.TraceID = telemetry.TraceIDFromContext(ctx)
	}
	if !errorcode.Valid(string(ev.Code)) {
		ev.Code = errorcode.Unknown
	}
	select {
	case r.queue <- ev:
	default:
		// The queue is full. Dropping is the correct answer and the only fail-static one — see
		// QueueDepth. The drop is counted as a failure class so readiness can say so; it is not logged
		// per event, because a full queue means thousands of events and a per-event line would turn a
		// transmit outage into a log outage.
		r.degrade("queue_full")
	}
}

func (r *httpReporter) State() (State, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failureClass != "" {
		return StateDegraded, r.failureClass
	}
	return StateConfigured, ""
}

func (r *httpReporter) Close(ctx context.Context) error {
	r.closeOne.Do(func() { close(r.queue) })
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run is the single out-of-band transmitter.
//
// ONE goroutine, so the number of in-flight transmits is one no matter how many requests are failing —
// a per-event goroutine would let a failing hot path spawn thousands of sockets against an ingest
// endpoint that is already unhappy.
func (r *httpReporter) run() {
	defer close(r.done)
	for ev := range r.queue {
		if !r.admit(ev) {
			continue
		}
		r.transmit(ev)
	}
}

// admit applies the per-issue limit and the transmit budget.
func (r *httpReporter) admit(ev Event) bool {
	now := r.cfg.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Sub(r.windowStart) >= RateInterval {
		r.windowStart = now
		r.windowCount = 0
		r.perIssue = map[string]int{}
	}
	if r.windowCount >= TransmitBudget {
		return false
	}
	issue := issueKey(ev)
	if r.perIssue[issue] >= PerIssueRateLimit {
		return false
	}
	r.perIssue[issue]++
	r.windowCount++
	return true
}

// issueKey is what "the same issue" means for the per-issue limit: the classification plus the top
// platform frame. Two failures with the same code from different places are different issues, because
// the second one is a defect the first would otherwise hide.
func issueKey(ev Event) string {
	top := ""
	for _, f := range ev.Frames {
		if f.InApp {
			top = f.Package + "." + f.Function
			break
		}
	}
	return string(ev.Code) + "|" + ev.Type + "|" + top
}

func (r *httpReporter) transmit(ev Event) {
	// 🔴 The order is CONSTRUCT, then SCRUB, then encode. The scrubber runs over the constructed event
	// as the last stage before the bytes exist — not over the error, and not after transmission.
	scrubbed := Scrub(r.cfg.Scrubber, ev.Wire())
	body, err := envelopeFromWire(scrubbed, ev, r.cfg.Now(), "go")
	if err != nil {
		r.degrade("encode_failed")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), TransmitTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.dsn.endpoint, bytes.NewReader(body))
	if err != nil {
		r.degrade("request_failed")
		return
	}
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	req.Header.Set("X-Sentry-Auth", fmt.Sprintf("Sentry sentry_version=7, sentry_key=%s", r.dsn.publicKey))

	resp, err := r.cfg.Client.Do(req)
	if err != nil {
		r.degrade("transport_unreachable")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		r.degrade("ingest_rejected_" + fmt.Sprint(resp.StatusCode))
		return
	}
	r.recover()
}

// degrade records a failure class and logs AT MOST ONE line per interval.
//
// One line per interval rather than per event, because a transmit outage produces one failure per
// failing request and a per-event line would flood the log a human reads to diagnose the outage.
func (r *httpReporter) degrade(class string) {
	r.mu.Lock()
	first := r.failureClass == ""
	r.failureClass = class
	now := r.cfg.Now()
	should := first || now.Sub(r.lastLogged) >= RateInterval
	if should {
		r.lastLogged = now
	}
	logf := r.cfg.Logf
	r.mu.Unlock()
	if should && logf != nil {
		// WARN with the failure CLASS, not the error text: an ingest endpoint's error body is not ours
		// and has no business in our logs.
		logf("WARN error_reporting.transmit.degraded class=%s (at most one line per %s)", class, RateInterval)
	}
}

func (r *httpReporter) recover() {
	r.mu.Lock()
	was := r.failureClass
	r.failureClass = ""
	logf := r.cfg.Logf
	r.mu.Unlock()
	if was != "" && logf != nil {
		logf("INFO error_reporting.transmit.recovered previous_class=%s", was)
	}
}

// envelopeFromWire renders bytes from an ALREADY-SCRUBBED wire map.
//
// It takes the map rather than the Event so that the scrubber's output — not the original event — is
// what reaches the encoder. A version that re-rendered from the Event would run the scrubber and then
// throw its result away, which is the kind of defect that leaves every test green.
func envelopeFromWire(wire map[string]any, original Event, sentAt time.Time, platform string) ([]byte, error) {
	rebuilt := Event{
		Type:    str(wire["error.type"]),
		Code:    errorcode.Code(str(wire["error.code"])),
		Level:   Level(str(wire["level"])),
		TraceID: str(wire["trace_id"]),
		Release: str(wire["release"]),
		Edition: str(wire["edition"]),
		Surface: str(wire["surface"]),
		Runtime: str(wire["runtime"]),
	}
	if frames, ok := wire["frames"].([]any); ok {
		for i, raw := range frames {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			f := Frame{
				Function: str(m["function"]),
				Package:  str(m["package"]),
				File:     str(m["file"]),
			}
			if i < len(original.Frames) {
				// Line and in_app are numbers and bools; the scrubber passes them through untouched, and
				// reading them from the original avoids a float64 round-trip through `any`.
				f.Line = original.Frames[i].Line
				f.InApp = original.Frames[i].InApp
			}
			rebuilt.Frames = append(rebuilt.Frames, f)
		}
	}
	return rebuilt.Envelope(sentAt, platform)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
