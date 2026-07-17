package registry

import (
	"errors"
	"strings"
	"testing"
)

func TestParseTemplate_ExtractsSortedUniqueSlots(t *testing.T) {
	tmpl, err := ParseTemplate("Answer {{query}} in a {{tone}} tone. Again: {{query}}.")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := tmpl.Slots()
	want := []string{"query", "tone"}
	if len(got) != len(want) {
		t.Fatalf("slots = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slots = %v, want %v (sorted, deduplicated)", got, want)
		}
	}
}

func TestParseTemplate_SlotlessBodyHasNoSlots(t *testing.T) {
	tmpl, err := ParseTemplate("A constant prompt.")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(tmpl.Slots()) != 0 {
		t.Errorf("slots = %v, want none", tmpl.Slots())
	}
	out, err := tmpl.Render(nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "A constant prompt." {
		t.Errorf("render = %q", out)
	}
}

// A malformed slot must be a loud error, never silently passed through as literal text. A prompt
// that reaches a provider with a stray `{{querry}}` in it still returns a plausible completion and
// still gets scored — it looks like a bad model, not a bug.
func TestParseTemplate_MalformedSlotIsAnErrorNotALiteral(t *testing.T) {
	cases := []struct{ name, body string }{
		{"inner spaces", "Answer {{ query }}"},
		{"leading digit", "Answer {{1st}}"},
		{"hyphen", "Answer {{my-slot}}"},
		{"empty name", "Answer {{}}"},
		{"unterminated", "Answer {{query"},
		{"dotted path", "Answer {{user.name}}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseTemplate(tc.body); err == nil {
				t.Fatalf("ParseTemplate(%q) succeeded; a malformed slot must be rejected", tc.body)
			} else if !errors.Is(err, ErrInvalidEntry) {
				t.Errorf("want ErrInvalidEntry, got %v", err)
			}
		})
	}
}

// FR7: identical bindings render byte-identically.
func TestRender_IsDeterministic(t *testing.T) {
	tmpl, err := ParseTemplate("{{a}}-{{b}}-{{a}}-{{c}}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	bindings := map[string]string{"a": "1", "b": "2", "c": "3"}
	first, err := tmpl.Render(bindings)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Repeat enough times that any map-iteration order dependence would surface.
	for i := 0; i < 100; i++ {
		got, err := tmpl.Render(bindings)
		if err != nil {
			t.Fatalf("render %d: %v", i, err)
		}
		if got != first {
			t.Fatalf("render %d = %q, first = %q — rendering is not deterministic", i, got, first)
		}
	}
	if first != "1-2-1-3" {
		t.Errorf("render = %q, want %q", first, "1-2-1-3")
	}
}

// The registries spec: "Missing binding is an error, not a silent blank" — and no partially-rendered
// prompt is passed to a provider.
func TestRender_MissingBindingErrorsAndEmitsNothing(t *testing.T) {
	tmpl, err := ParseTemplate("Answer {{query}} in a {{tone}} tone.")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := tmpl.Render(map[string]string{"tone": "curt"})
	if err == nil {
		t.Fatal("rendering without a required binding succeeded; it must fail")
	}
	if !errors.Is(err, ErrInvalidEntry) {
		t.Errorf("want ErrInvalidEntry, got %v", err)
	}
	if !strings.Contains(err.Error(), "query") {
		t.Errorf("error must name the missing slot, got: %v", err)
	}
	if out != "" {
		t.Errorf("a failed render must emit nothing, got %q", out)
	}
}

// An empty string IS a binding. Only an ABSENT binding is an error — otherwise a legitimately empty
// value would be indistinguishable from a forgotten one.
func TestRender_EmptyStringIsABindingNotAMissingOne(t *testing.T) {
	tmpl, err := ParseTemplate("[{{query}}]")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := tmpl.Render(map[string]string{"query": ""})
	if err != nil {
		t.Fatalf("an empty binding must be accepted: %v", err)
	}
	if out != "[]" {
		t.Errorf("render = %q, want %q", out, "[]")
	}
}

// An unknown binding is usually a typo for a real slot. Ignoring it would render the typo'd slot as
// "missing" (confusing) or, worse, feed config_hash a value that changes nothing.
func TestRender_UnknownBindingIsRejected(t *testing.T) {
	tmpl, err := ParseTemplate("Answer {{query}}.")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = tmpl.Render(map[string]string{"query": "x", "querry": "typo"})
	if err == nil {
		t.Fatal("a binding matching no slot must be rejected")
	}
	if !strings.Contains(err.Error(), "querry") {
		t.Errorf("error must name the offending binding, got: %v", err)
	}
}

func TestRender_ErrorMessageIsDeterministic(t *testing.T) {
	tmpl, err := ParseTemplate("{{a}}{{b}}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var first string
	for i := 0; i < 50; i++ {
		_, err := tmpl.Render(map[string]string{"z": "1", "y": "2"})
		if err == nil {
			t.Fatal("expected an error")
		}
		if i == 0 {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("error text varies between runs (map order leaked):\n %s\n %s", first, err.Error())
		}
	}
}

// Rendering is pure concatenation, so a binding whose value looks like a slot is inert data — it is
// never re-scanned. Otherwise a user-supplied query could inject a slot into the prompt.
func TestRender_BindingValuesAreNotReScanned(t *testing.T) {
	tmpl, err := ParseTemplate("Q: {{query}}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := tmpl.Render(map[string]string{"query": "{{secret}}"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "Q: {{secret}}" {
		t.Errorf("render = %q; a binding's value must be inserted literally", out)
	}
}
