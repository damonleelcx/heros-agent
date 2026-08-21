package sourceingest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// localpair_test.go covers P32 §4: the pairing flow, the expiry, and FR15's stated availability.
//
// The EGRESS CAPTURE (task 4.2, §7.9) lives in `internal/clilink` rather than here, because the thing
// that must be captured is a real HTTP request from the real command — a payload-level assertion in
// this package would be checking a struct nobody sends. See TestPairingTransmitsNothingFromTheTree.

func newTestPairing(t *testing.T) (*PairingService, *MemStore, *int64) {
	t.Helper()
	store := NewMemStore()
	now := int64(1_000)
	n := int64(0)
	svc, err := NewPairingService(PairingConfig{
		Store: store,
		// A counter the test moves by hand, not a clock. Four tests in this repository once went red
		// on the calendar alone.
		NowMS: func() int64 { return now },
		IDFor: func(p string) string { n++; return fmt.Sprintf("%s-%d", p, n) },
		// A code from the REAL alphabet. The first version of this fixture used `CODE-0001`, which
		// normalises to `CDE` — and that exposed a real defect: `Start` stored whatever the generator
		// returned while `Claim` normalizes, so a non-conforming generator issued codes that could
		// never be claimed. `Start` now normalizes and refuses an empty result.
		Code: func() string { n++; return fmt.Sprintf("ACDE-FGH%c", "34679"[n%5]) },
	})
	if err != nil {
		t.Fatalf("pairing service: %v", err)
	}
	return svc, store, &now
}

// TestAPairingIsClaimedOnceAndAttributesTheMachine is the flow's happy path and its uniqueness.
func TestAPairingIsClaimedOnceAndAttributesTheMachine(t *testing.T) {
	ctx := context.Background()
	svc, _, now := newTestPairing(t)

	p, err := svc.Start(ctx, "t1", "wf1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if p.State != PairingPending || p.UserCode == "" {
		t.Fatalf("start produced %+v, want a pending pairing with a code", p)
	}
	if p.ExpiresAtMS != *now+PairingTTL.Milliseconds() {
		t.Errorf("ExpiresAtMS = %d, want now+TTL", p.ExpiresAtMS)
	}

	claimed, err := svc.Claim(ctx, p.UserCode, "laptop-01", "abc123")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.State != PairingPaired || claimed.MachineName != "laptop-01" || claimed.Revision != "abc123" {
		t.Fatalf("claim produced %+v, want a paired pairing naming the machine and revision", claimed)
	}
	if claimed.TenantID != "t1" {
		t.Errorf("TenantID = %q — the tenant must come from the pairing the CONSOLE created, never from the agent", claimed.TenantID)
	}

	// 🔴 Single use. A code that can be claimed twice attributes a workflow to two machines, in the
	// one table whose whole purpose is saying which machine reads which workflow.
	if _, err := svc.Claim(ctx, p.UserCode, "laptop-02", "abc123"); !errors.Is(err, ErrNoPairing) {
		t.Fatalf("a second claim of one code = %v, want ErrNoPairing", err)
	}

	// The console's read sees the machine.
	got, err := svc.Get(ctx, "t1", p.PairingID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.MachineName != "laptop-01" {
		t.Errorf("the console reads %+v, want the paired machine", got)
	}
	// And another tenant sees nothing.
	if _, err := svc.Get(ctx, "other", p.PairingID); !errors.Is(err, ErrNoPairing) {
		t.Errorf("another tenant read the pairing: %v", err)
	}
}

// TestTwoAgentsRacingOnOneCodeProduceOneSuccess.
//
// 🔴 CONCURRENT, because a claim implemented as read-then-write is invisible to a test that never
// contends — two sequential claims exercise the writer's own check and never reach the race. This
// repository has already paid for that lesson once, on a unique index.
func TestTwoAgentsRacingOnOneCodeProduceOneSuccess(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestPairing(t)
	p, err := svc.Start(ctx, "t1", "wf1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	const agents = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var winners []string
	for i := 0; i < agents; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := svc.Claim(ctx, p.UserCode, fmt.Sprintf("machine-%d", i), "abc")
			if err == nil {
				mu.Lock()
				winners = append(winners, res.MachineName)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if len(winners) != 1 {
		t.Fatalf("%d agents claimed one code (%v); exactly one must win, or the console attributes a "+
			"workflow to a machine that is not reading it", len(winners), winners)
	}
}

// TestAnExpiredCodeIsRefusedAsExpiredAndNotAsUnknown.
//
// The two are distinguishable to the CALLER because they already proved they hold the code, and they
// send a person to two different places: "start a new one" versus "check what you typed".
func TestAnExpiredCodeIsRefusedAsExpiredAndNotAsUnknown(t *testing.T) {
	ctx := context.Background()
	svc, _, now := newTestPairing(t)
	p, err := svc.Start(ctx, "t1", "wf1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	*now = p.ExpiresAtMS // exactly at the deadline: the window is closed, not still open
	if _, err := svc.Claim(ctx, p.UserCode, "laptop", "abc"); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("claim at the deadline = %v, want ErrPairingExpired", err)
	}
	// And the console's read reports the expiry too, without a sweeper having run.
	got, err := svc.Get(ctx, "t1", p.PairingID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != PairingExpired {
		t.Errorf("State = %q, want %q — expiry is computed at READ so a pairing stops being claimable "+
			"whether or not a background job has run", got.State, PairingExpired)
	}
	// An unknown code is the OTHER answer.
	if _, err := svc.Claim(ctx, "ZZZZ-ZZZZ", "laptop", "abc"); !errors.Is(err, ErrNoPairing) {
		t.Errorf("an unknown code = %v, want ErrNoPairing", err)
	}
}

// TestPairingCodesAreTypeableAndDoNotCollideOnLookalikes.
//
// A flow whose failure mode is "it says my code is wrong" teaches a person the product is broken
// rather than that they typed an O for a zero. This asserts the alphabet has no lookalike pair at all,
// which is stronger than testing one example.
func TestPairingCodesAreTypeableAndDoNotCollideOnLookalikes(t *testing.T) {
	for _, pair := range [][2]rune{{'O', '0'}, {'I', '1'}, {'L', '1'}, {'S', '5'}, {'Z', '2'}, {'B', '8'}} {
		a := strings.ContainsRune(pairingCodeAlphabet, pair[0])
		b := strings.ContainsRune(pairingCodeAlphabet, pair[1])
		if a && b {
			t.Errorf("the code alphabet contains both %q and %q — a person reading one screen and typing "+
				"into another will confuse them", string(pair[0]), string(pair[1]))
		}
	}
	// Normalisation accepts the renderings of ONE code and nothing else.
	code := NewPairingCode()
	for _, variant := range []string{code, strings.ToLower(code), strings.ReplaceAll(code, "-", ""),
		strings.ReplaceAll(code, "-", " "), " " + code + " "} {
		if got := NormalizePairingCode(variant); got != code {
			t.Errorf("NormalizePairingCode(%q) = %q, want %q", variant, got, code)
		}
	}
	// 🔴 Distinct codes stay distinct after normalisation. Without this, a normalisation that stripped
	// too much would silently collapse the code space and the fence above would still pass.
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		c := NormalizePairingCode(NewPairingCode())
		if len(c) != 9 { // XXXX-XXXX
			t.Fatalf("a generated code normalises to %q (%d chars), want the XXXX-XXXX shape", c, len(c))
		}
		seen[c] = true
	}
	if len(seen) < 1990 {
		t.Errorf("2000 generated codes produced only %d distinct values — the code space is far smaller "+
			"than it looks, and a pairing can be guessed", len(seen))
	}
}

// TestAClaimMustNameItsMachine.
//
// "unknown" answers nothing while looking like an answer, and the console's whole reason for holding
// this row is to say WHICH machine.
func TestAClaimMustNameItsMachine(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestPairing(t)
	p, err := svc.Start(ctx, "t1", "wf1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := svc.Claim(ctx, p.UserCode, "   ", "abc"); err == nil {
		t.Fatal("a claim with no machine name was accepted")
	}
	// And the pairing is still claimable afterwards — a refused claim must not spend the code.
	if _, err := svc.Claim(ctx, p.UserCode, "laptop", "abc"); err != nil {
		t.Fatalf("the code was spent by a refused claim: %v", err)
	}
}

// TestLocalModeAvailabilityIsStatedBeforeTheFlow is FR15 and PRD §14 A1.
//
// 🔴 Every unavailable answer NAMES A REASON. A capability that is off with no reason given is
// indistinguishable from one that is broken, and the whole requirement is that a customer learns the
// limit at step zero instead of at the last step.
func TestLocalModeAvailabilityIsStatedBeforeTheFlow(t *testing.T) {
	const pinned = "https://heros-agent.space"

	t.Run("the pinned deployment", func(t *testing.T) {
		a := Availability(pinned, pinned)
		if !a.Available {
			t.Fatalf("the pinned deployment reports unavailable: %+v", a)
		}
		if len(a.Deployments) != 1 || a.Deployments[0] != pinned {
			t.Errorf("Deployments = %v, want exactly the pinned host", a.Deployments)
		}
	})

	t.Run("a self-hosted deployment", func(t *testing.T) {
		a := Availability(pinned, "https://heros.internal.acme.example")
		if a.Available {
			t.Fatal("a self-hosted deployment reports the local bridge as available — the flow would fail at its last step")
		}
		if a.Why == "" {
			t.Fatal("unavailable with no reason given")
		}
		if !strings.Contains(a.Why, pinned) {
			t.Errorf("Why = %q, want it to NAME the deployment the bridge does work against", a.Why)
		}
		// And it names what to do instead, so the answer is a next action rather than a refusal.
		if !strings.Contains(a.Why, "bundle") || !strings.Contains(a.Why, "connect") {
			t.Errorf("Why = %q, want it to name the two things that DO work here", a.Why)
		}
	})

	t.Run("a deployment that does not know its own address", func(t *testing.T) {
		a := Availability(pinned, "")
		if a.Available {
			t.Fatal("a deployment with no configured address assumed it was the pinned one")
		}
		if a.Why == "" {
			t.Fatal("unavailable with no reason given")
		}
	})

	t.Run("a trailing slash is the same deployment", func(t *testing.T) {
		// A URL that differs only in a trailing slash is the same host, and refusing it would be a
		// configuration trap whose symptom is "the local mode is not available here" with no clue.
		if a := Availability(pinned, pinned+"/"); !a.Available {
			t.Errorf("%q was treated as a different deployment from %q", pinned+"/", pinned)
		}
	})
}

// TestNoPairingFieldCanCarryAPathOrContent.
//
// The field somebody would add is `RepositoryPath`. A local filesystem path is the customer's own
// layout, it tells the platform nothing it needs, and having somewhere to put it is how it ends up
// transmitted.
func TestNoPairingFieldCanCarryAPathOrContent(t *testing.T) {
	forbidden := map[string]bool{
		"path": true, "dir": true, "directory": true, "file": true, "files": true,
		"content": true, "contents": true, "source": true, "tree": true, "diff": true,
		"prompt": true, "token": true, "secret": true, "credential": true,
	}
	ty := reflect.TypeOf(Pairing{})
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		for _, w := range camelWords(f.Name) {
			if forbidden[w] {
				t.Errorf("Pairing.%s has the word %q in it — a pairing carries a code, a machine name and a "+
					"commit id, and nothing from the customer's tree", f.Name, w)
			}
		}
	}
}
