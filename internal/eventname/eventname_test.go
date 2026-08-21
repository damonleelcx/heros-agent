package eventname

import (
	"regexp"
	"testing"
)

// shape is `<service>.<area>.<state>` — three lowercase dot-separated segments, underscores inside a
// segment. Written as a pattern rather than checked by eye because the drift this catches
// (`console.conversation.turnStarted`) is invisible in a diff of forty constants.
var shape = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*\.[a-z0-9]+(_[a-z0-9]+)*\.[a-z0-9]+(_[a-z0-9]+)*$`)

func TestEveryNameFollowsTheServiceAreaStateShape(t *testing.T) {
	for _, n := range Names() {
		if !shape.MatchString(string(n)) {
			t.Errorf("%q does not match <service>.<area>.<state>", n)
		}
	}
}

func TestTheEnumIsClosed(t *testing.T) {
	if Name("").Valid() {
		t.Error("an empty event name is valid; an event nobody named is an event nobody can find")
	}
	for _, bad := range []Name{
		"console.conversation.turn_started_for_tenant_hermes", // an identifier smuggled into the name
		"turn_started",
		"console.conversation.turnStarted",
	} {
		if bad.Valid() {
			t.Errorf("Valid() admitted %q", bad)
		}
	}
}

// TestP31NamesAreTheFourTheContractPromises reads the four back against PRD §9.3 and task 2.9. Written
// out rather than derived: a test that ranged over Names() would pass for any set, including an empty one.
//
// # 🔴 Why this asserts a SUBSET and no longer asserts the whole enum
//
// It used to compare `Names()` against P31's four, which was the same thing while P31 was the only
// contributor. P32 added seven, and the honest reading of that failure is that the test was measuring
// TWO things with one assertion: "P31's four are present and correctly spelled" (its actual subject)
// and "nobody else has added anything" (not its business, and a rule that would make every later phase
// edit this file).
//
// So the subject is now stated precisely: P31's four are present, spelled exactly, and the enum stays
// sorted. `TestEveryPhaseOwnsItsNames` below carries the other half — that every name belongs to a
// declared phase — which is the property the length check was crudely standing in for.
func TestP31NamesAreTheFourTheContractPromises(t *testing.T) {
	want := []Name{
		"console.conversation.approval_recorded",
		"console.conversation.refused",
		"console.conversation.stream_resumed",
		"console.conversation.turn_started",
	}
	have := map[Name]bool{}
	for _, n := range Names() {
		have[n] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("P31 declares %q and the enum does not carry it", w)
		}
	}
}

// TestNamesAreSorted pins Names()'s contract, which the removed length check was also covering.
func TestNamesAreSorted(t *testing.T) {
	got := Names()
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("Names() is not sorted: %q comes before %q", got[i-1], got[i])
		}
	}
}

// TestEveryPhaseOwnsItsNames is the half the old length check was standing in for: every name in the
// enum belongs to a declared phase's prefix.
//
// 🔴 A name with no owning phase is the shape this whole file exists to prevent — a string somebody
// added in a hurry that no dashboard, runbook or contract mentions. Adding a phase here is one line and
// is a decision; adding a name under no phase is a red build.
func TestEveryPhaseOwnsItsNames(t *testing.T) {
	// prefix → the phase that owns it.
	owners := map[string]string{
		"console.conversation.": "P31 — the conversational console",
		"agentd.ingest.":        "P32 — repository intake",
	}
	for _, n := range Names() {
		owned := false
		for prefix := range owners {
			if len(n) > len(prefix) && string(n)[:len(prefix)] == prefix {
				owned = true
				break
			}
		}
		if !owned {
			t.Errorf("%q belongs to no declared phase. Add its prefix to `owners` with the phase that "+
				"owns it, so a reader of this file can get from a name to the contract that promised it.", n)
		}
	}
}

// TestP32NamesAreTheSevenTheContractPromises is P32 task 2.12.
//
// Written out, for the same reason P31's are: a test that ranged over the enum would pass for any set.
// The task names four of them explicitly (`ingest.connection.created`, `.revoked`,
// `ingest.clone.succeeded`, `.failed`); the three extras — the refusal and the two retention states —
// are named here because they are emitted, and an emitted name that no test knows about is exactly the
// free-text field this enum exists to make impossible.
func TestP32NamesAreTheSevenTheContractPromises(t *testing.T) {
	want := []Name{
		IngestCloneFailed,
		IngestCloneSucceeded,
		IngestConnectionCreated,
		IngestConnectionRefused,
		IngestConnectionRevoked,
		IngestRetentionFailed,
		IngestRetentionSwept,
	}
	have := map[Name]bool{}
	for _, n := range Names() {
		have[n] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("P32 emits %q and the enum does not carry it", w)
		}
		if !w.Valid() {
			t.Errorf("%q is not Valid(), so the emitting helper would write nothing for it", w)
		}
	}
}
