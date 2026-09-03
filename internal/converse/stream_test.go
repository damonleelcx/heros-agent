package converse

import (
	"strings"
	"testing"
)

// feed splits a document into chunks of n bytes and returns everything the scanner emitted. Splitting
// at every width matters: the network chooses the boundaries, so a bug that only appears when an escape
// straddles two frames is a bug that appears intermittently in production and never in a demo.
func feed(doc string, n int) string {
	var t textFieldStream
	var out strings.Builder
	for i := 0; i < len(doc); i += n {
		end := i + n
		if end > len(doc) {
			end = len(doc)
		}
		out.WriteString(t.Write(doc[i:end]))
	}
	return out.String()
}

func TestTextIsExtractedAtEveryChunkBoundary(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{"say", `{"action":"say","text":"Hello there."}`, "Hello there."},
		{"ask", `{"action":"ask","text":"Which repository?"}`, "Which repository?"},
		{"text first", `{"text":"Leading field.","action":"say"}`, "Leading field."},
		{"spaces", `{"action":"say", "text" : "Spaced out."}`, "Spaced out."},
		{"escaped quote", `{"text":"He said \"no\" firmly."}`, `He said "no" firmly.`},
		{"newline", `{"text":"One.\nTwo."}`, "One.\nTwo."},
		{"backslash", `{"text":"C:\\path"}`, `C:\path`},
		{"unicode", `{"text":"caf\u00e9 \u2014 done"}`, "café — done"},
		{"empty", `{"text":""}`, ""},
		// The decision shape carries no text field at all, so nothing must be emitted — including from
		// the prose that IS present in `why`.
		{"do has nothing to stream",
			`{"action":"do","capability":"assess","axis":"loop","why":"You asked about retries."}`, ""},
	} {
		for _, width := range []int{1, 2, 3, 5, 7, 13, 1000} {
			if got := feed(tc.doc, width); got != tc.want {
				t.Errorf("%s at chunk width %d: got %q, want %q", tc.name, width, got, tc.want)
			}
		}
	}
}

// TestNothingIsEmittedAfterTheValueCloses — everything after the closing quote is structure, and a
// scanner that kept going would paste `","action":"say"}` onto the end of somebody's reply.
func TestNothingIsEmittedAfterTheValueCloses(t *testing.T) {
	var s textFieldStream
	got := s.Write(`{"text":"Done."`)
	if got != "Done." {
		t.Fatalf("got %q", got)
	}
	if more := s.Write(`,"action":"say","capability":"assess"}`); more != "" {
		t.Errorf("emitted %q after the value ended", more)
	}
}

// TestAKeyCalledTextInsideAValueIsNotMistakenForTheField.
func TestAKeyCalledTextInsideAValueIsNotMistakenForTheField(t *testing.T) {
	doc := `{"action":"say","text":"the word \"text\" appears here"}`
	if got := feed(doc, 4); got != `the word "text" appears here` {
		t.Errorf("got %q", got)
	}
}

// TestTheScannerNeverGrowsWithoutBound. A long reply that never contains the key must not make the
// scanner hold the whole document: this runs per token, on every turn.
func TestTheScannerNeverGrowsWithoutBound(t *testing.T) {
	var s textFieldStream
	for i := 0; i < 2000; i++ {
		s.Write("some prose that is not the key ")
	}
	if n := s.buf.Len(); n > len(key) {
		t.Errorf("buffer grew to %d bytes with no key in sight; it should hold at most %d", n, len(key))
	}
}

// TestGarbageIsSurvived — this is a display path. Anything unparseable must yield nothing rather than
// panic, because the real Outcome is still built by validating the complete object.
func TestGarbageIsSurvived(t *testing.T) {
	for _, doc := range []string{
		``, `{`, `{"text"`, `{"text":`, `{"text":1234}`, `not json at all`,
		`{"text":"unterminated`, `{"text":"bad escape \u12"}`, `{"text":"\`,
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panicked on %q: %v", doc, r)
				}
			}()
			feed(doc, 3)
		}()
	}
}
