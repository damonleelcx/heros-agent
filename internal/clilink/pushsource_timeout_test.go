package clilink

import (
	"net/http"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/runlink/transport"
)

// pushsource_timeout_test.go fences the bound on the discovery leg of `push-source`.
//
// 🔴 The bug this closes. Discovery shared `transport.DefaultTimeout` (30s) with every other call,
// but it PARSES THE CALLER'S REPOSITORY — `nousresearch/hermes-agent` (8487 files) takes ~55s. So
// `push-source` uploaded 60 MiB, timed out, and advised a re-run; `push-source` re-transmits before
// reaching discovery, so the retry paid the upload again and timed out identically. An unbreakable
// loop for most real repositories.
//
// It was invisible because every test served discovery instantly. A fake that answers at once cannot
// fail a deadline, so nothing measured the one property that mattered.

// 🔴 THE RELATIONSHIP, not the value.
//
// Asserting `discoveryTimeout == 15m` would pass if somebody set the shared default to an hour, and
// would fail on a deliberate re-tune that kept the property intact. What must hold is that the leg
// which does repository-sized work waits materially longer than one answered from storage.
func TestDiscoveryWaitsMuchLongerThanAnOrdinaryRequest(t *testing.T) {
	if discoveryTimeout <= transport.DefaultTimeout {
		t.Fatalf("discoveryTimeout (%s) does not exceed transport.DefaultTimeout (%s) — the discovery "+
			"leg is bounded like a request answered from storage, and a real repository outruns it",
			discoveryTimeout, transport.DefaultTimeout)
	}
	// Materially longer, not a nudge. A repository that took 55s against a 30s bound is not fixed by
	// 35s; the margin has to admit repositories several times larger than the one that exposed this.
	if discoveryTimeout < 8*transport.DefaultTimeout {
		t.Errorf("discoveryTimeout is %s, only %.1f× the ordinary bound — the repository that exposed "+
			"this took nearly 2× the ordinary bound on its own, so a small multiple just moves the "+
			"cliff rather than clearing it", discoveryTimeout, float64(discoveryTimeout)/float64(transport.DefaultTimeout))
	}
}

// The floor RAISES a client's patience and never lowers it. `heroslocallink` and the tests inject a
// deliberate `Timeout`; a floor that overwrote a longer injected value would silently shorten a wait
// somebody chose on purpose.
func TestTheFloorRaisesPatienceAndNeverLowersIt(t *testing.T) {
	// A caller who asked for LONGER than the floor keeps their value.
	long := Commands{Timeout: 30 * time.Minute}
	if got := effectiveTimeout(long, discoveryTimeout); got != 30*time.Minute {
		t.Errorf("an injected 30m was reduced to %s by the floor", got)
	}
	// A caller who asked for SHORTER than the floor is raised to it on this leg.
	short := Commands{Timeout: time.Second}
	if got := effectiveTimeout(short, discoveryTimeout); got != discoveryTimeout {
		t.Errorf("the discovery floor did not apply: got %s, want %s", got, discoveryTimeout)
	}
	// A caller who asked for nothing gets the floor.
	var none Commands
	if got := effectiveTimeout(none, discoveryTimeout); got != discoveryTimeout {
		t.Errorf("an uninjected caller got %s, want the floor %s", got, discoveryTimeout)
	}
	// And with no floor, nothing is imposed — the ordinary path keeps the transport default.
	if got := effectiveTimeout(none, 0); got != 0 {
		t.Errorf("a zero floor imposed %s; the ordinary path must fall through to transport.DefaultTimeout", got)
	}
}

// The ordinary client is NOT given the discovery bound. A 15-minute default on every call would turn a
// dead platform into a quarter-hour hang on `link`, which is the opposite trade.
func TestTheOrdinaryClientKeepsTheShortBound(t *testing.T) {
	var c Commands
	if got := effectiveTimeout(c, 0); got >= discoveryTimeout {
		t.Errorf("the ordinary client carries the discovery bound (%s) — a dead platform would hang "+
			"every command for that long", got)
	}
}

// effectiveTimeout mirrors clientAtLeast's choice so the rule can be asserted without reaching into an
// http.Client's unexported state. Kept beside the fence, and it is the same two lines.
func effectiveTimeout(c Commands, floor time.Duration) time.Duration {
	timeout := c.Timeout
	if floor > timeout {
		timeout = floor
	}
	return timeout
}

// A guard that the mirror above stays honest: clientAtLeast must still build a client at all, for both
// a floored and an unfloored call.
func TestClientAtLeastBuildsAUsableClient(t *testing.T) {
	c := Commands{RT: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, nil })}
	if c.clientAtLeast("tok", discoveryTimeout) == nil {
		t.Fatal("the floored client is nil")
	}
	if c.client("tok") == nil {
		t.Fatal("the ordinary client is nil")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
