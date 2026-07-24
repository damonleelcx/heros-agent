package optimizer

import "testing"

// Section 4.3: the change ledger is append-only and records each decision keyed by run, secret-free
// (payloads content-hashed, never inlined).
func TestMemLedger_AppendOnlyContentAddressed(t *testing.T) {
	l := NewMemLedger()
	// A payload with a secret-looking body is content-addressed; the event carries only the hash.
	secret := []byte(`{"prompt":"SYSTEM KEY sk-live-xyz","case":"pii"}`)
	hash := l.Put(secret)
	seq1, err := l.Append(LedgerEvent{RunID: "r", Type: EventVerify, PayloadHash: hash, Summary: "held-out +0.3"})
	if err != nil {
		t.Fatal(err)
	}
	seq2, _ := l.Append(LedgerEvent{RunID: "r", Type: EventApply, PayloadHash: hash})
	if seq2 <= seq1 {
		t.Fatal("sequence numbers must be monotonically increasing (append-only)")
	}
	events := l.Events("r")
	for _, ev := range events {
		if len(ev.Summary) > 0 && containsSubstr(ev.Summary, "sk-live") {
			t.Fatal("a secret leaked into a ledger summary")
		}
		if ev.PayloadHash != hash {
			continue
		}
	}
	got, ok := l.Get(hash)
	if !ok || ContentHash(got) != hash {
		t.Fatal("payload not retrievable by content hash")
	}
}

// Section 6.3: a downed ledger fails Append, which the loop treats as "do not merge".
func TestMemLedger_DownFailsClosed(t *testing.T) {
	l := NewMemLedger()
	l.SetDown(true)
	if _, err := l.Append(LedgerEvent{RunID: "r", Type: EventApply}); err != ErrLedgerUnavailable {
		t.Fatalf("a downed ledger must fail closed, got %v", err)
	}
	l.SetDown(false)
	if _, err := l.Append(LedgerEvent{RunID: "r", Type: EventGrant}); err != nil {
		t.Fatalf("a restored ledger should append, got %v", err)
	}
}

// Section 4.3: a full run's ledger replays the whole decision sequence — grant, consider, verify,
// apply, stop — keyed by run.
func TestLedger_FullRunReplay(t *testing.T) {
	cand := mkCand(`{"v":"good"}`, "d3", "node3", 0.5)
	verifier := StaticVerifier{ByConfig: map[string]VerifyResult{cand.ConfigHash: goodResult(0.8, 0.5)}}
	ledger := NewMemLedger()
	c := newController(verifier, NewFakeRepo([]byte(testBaseline)), ledger, NewKillSwitch(), cand)
	_, _ = c.Run(t.Context(), baseInput(testAuthority(true)))

	events := ledger.Events("run-1")
	for _, want := range []EventType{EventGrant, EventConsider, EventVerify, EventApply, EventStop} {
		if !hasEvent(events, want) {
			t.Errorf("audit trail is missing a %s event", want)
		}
	}
	// The grant is the first event (spec: the grant is recorded in the audit trail).
	if events[0].Type != EventGrant {
		t.Errorf("first event should be the grant, got %s", events[0].Type)
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
