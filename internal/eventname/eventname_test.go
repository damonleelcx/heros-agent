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
func TestP31NamesAreTheFourTheContractPromises(t *testing.T) {
	want := []Name{
		"console.conversation.approval_recorded",
		"console.conversation.refused",
		"console.conversation.stream_resumed",
		"console.conversation.turn_started",
	}
	got := Names()
	if len(got) != len(want) {
		t.Fatalf("the enum has %d names; P31 declares %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("name[%d] = %q, want %q (Names() must be sorted)", i, got[i], want[i])
		}
	}
}
