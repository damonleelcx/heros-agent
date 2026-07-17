package registry

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// PromptSpec is a prompt entry's content (FR7).
//
// The template BODY is not here — only the hash of it. Bodies live in the blob store because a
// prompt may contain PII and PRD §7 requires such content content-hashed and referenced, never
// inlined in a DB row or a log line. The slot list IS here, and is hashed into the version_id, so a
// template's declared interface is pinned by its version rather than re-derived by whoever reads it
// next.
type PromptSpec struct {
	BodyBlobHash string   `json:"body_blob_hash"`
	Slots        []string `json:"slots"`
}

// PromptEntry is a resolved prompt version. It carries the parsed Template, so a caller that
// resolves a prompt_ref can render it without re-fetching or re-parsing.
type PromptEntry struct {
	VersionID string
	Name      string
	Spec      PromptSpec
	Template  *Template
	Envelope  []byte
}

// Template is a parsed prompt template: a sequence of literal and slot segments.
//
// Rendering is pure string concatenation over that sequence, which is what makes FR7's determinism
// structural rather than a property to be tested for: there is no map iteration, no time, no
// randomness, and no formatting choice anywhere in Render.
type Template struct {
	segments []segment
	slots    []string // sorted, deduplicated — the declared interface
}

type segment struct {
	literal string
	slot    string // "" for a literal segment
}

// Slots returns the template's declared slot names, sorted and deduplicated.
func (t *Template) Slots() []string {
	out := make([]string, len(t.slots))
	copy(out, t.slots)
	return out
}

// Segment is one piece of a parsed template: a literal run of text, or a named slot.
type Segment struct {
	Literal string
	Slot    string // "" for a literal segment
}

// Segments returns the template's literal/slot sequence in template order.
//
// Render is the right API when every slot's value is a STRING. The Transform Engine has the other
// case and cannot use it: at a Go call site a slot's value is an EXPRESSION supplied by the
// surrounding code (`"Triage: " + ticket`), so the codemod must interleave source text between the
// literal runs. A finished string cannot express "this hole is code, not a value".
//
// Exposing the sequence rather than letting the codemod re-parse the body keeps ParseTemplate the
// only parser of template syntax. A second parser would be a second source of truth for what
// `{{name}}` means, and the two would drift the first time the syntax gains an escape.
//
// Slot order here is TEXTUAL (the order the slots appear in the body), unlike Slots(), which is
// sorted. Both are deliberate: Slots() is the declared interface (a set), Segments() is the body's
// structure (a sequence), and rendering has to follow the body.
func (t *Template) Segments() []Segment {
	out := make([]Segment, 0, len(t.segments))
	for _, s := range t.segments {
		out = append(out, Segment{Literal: s.literal, Slot: s.slot})
	}
	return out
}

// ParseTemplate parses a template body with named `{{slot}}` variable slots.
//
// Slot syntax is exactly `{{name}}` where name is [A-Za-z_][A-Za-z0-9_]*. No inner spaces: `{{ q }}`
// is an error, not a synonym for `{{q}}`. Two spellings of one slot would be two different bodies,
// hence two blobs and two version_ids for one logical template — an ambiguity with no upside.
//
// Any `{{` that does not open a well-formed slot is an ERROR naming its offset, never a literal.
// Silently treating a malformed slot as text is the failure mode that matters here: it would ship a
// prompt containing a stray `{{querry}}` to a provider and look like a bad model day, not a bug. The
// cost is that a literal `{{` cannot currently appear in a template — a loud, documented limitation,
// and an expand-safe one: no template that exists today can contain `{{`, so a future escape (say
// `{{{{`) cannot change how any already-published version renders.
func ParseTemplate(body string) (*Template, error) {
	t := &Template{}
	seen := map[string]bool{}
	var lit strings.Builder

	for i := 0; i < len(body); {
		if !strings.HasPrefix(body[i:], "{{") {
			lit.WriteByte(body[i])
			i++
			continue
		}
		end := strings.Index(body[i+2:], "}}")
		if end < 0 {
			return nil, errInvalid("prompt template: unterminated %q at byte %d (slots are {{name}})", "{{", i)
		}
		name := body[i+2 : i+2+end]
		if !isSlotName(name) {
			return nil, errInvalid(
				"prompt template: %q at byte %d is not a valid slot; expected {{name}} with name matching [A-Za-z_][A-Za-z0-9_]*",
				body[i:i+2+end+2], i)
		}
		if lit.Len() > 0 {
			t.segments = append(t.segments, segment{literal: lit.String()})
			lit.Reset()
		}
		t.segments = append(t.segments, segment{slot: name})
		if !seen[name] {
			seen[name] = true
			t.slots = append(t.slots, name)
		}
		i += 2 + end + 2
	}
	if lit.Len() > 0 {
		t.segments = append(t.segments, segment{literal: lit.String()})
	}
	sort.Strings(t.slots)
	return t, nil
}

func isSlotName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// Render substitutes bindings into the template. Identical bindings always produce byte-identical
// output (FR7).
//
// Both halves of the binding contract fail loud, and both fail BEFORE any output exists:
//
//   - A missing binding is an error naming the slot — never an empty string. A blank where the user's
//     query should be is a prompt that still reaches the provider, still returns a plausible
//     completion, and still gets scored: a silent default here would corrupt an eval rather than
//     break it.
//   - An unknown binding is also an error. A binding that renders nothing still feeds config_hash
//     (PRD OQ3), so tolerating it would let two different config_hashes denote the identical prompt —
//     and it is usually a typo for a real slot, which the missing-slot error alone would report as a
//     confusing "missing" for a name the caller believes they passed.
//
// Nothing is written until every binding is checked, so a failed render cannot emit a partial prompt.
func (t *Template) Render(bindings map[string]string) (string, error) {
	var missing []string
	for _, s := range t.slots {
		if _, ok := bindings[s]; !ok {
			missing = append(missing, s)
		}
	}
	var unknown []string
	for k := range bindings {
		if !containsStr(t.slots, k) {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown) // map iteration order must not leak into the error message
	if len(missing) > 0 || len(unknown) > 0 {
		var parts []string
		if len(missing) > 0 {
			parts = append(parts, fmt.Sprintf("missing binding(s) for slot(s) %s", strings.Join(missing, ", ")))
		}
		if len(unknown) > 0 {
			parts = append(parts, fmt.Sprintf("binding(s) %s match no slot", strings.Join(unknown, ", ")))
		}
		return "", errInvalid("prompt render: %s", strings.Join(parts, "; "))
	}

	var b strings.Builder
	for _, seg := range t.segments {
		if seg.slot == "" {
			b.WriteString(seg.literal)
		} else {
			b.WriteString(bindings[seg.slot])
		}
	}
	return b.String(), nil
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// RegisterPrompt parses and publishes a prompt template, storing the body as a content-hashed blob,
// and returns the entry's version_id (task 1.3).
//
// The template is parsed at REGISTRATION, so a malformed template is rejected when someone publishes
// it — not later, mid-run, with a config_hash already recorded and provider budget already spent.
func (s *Store) RegisterPrompt(ctx context.Context, name, body string) (versionID string, err error) {
	tmpl, err := ParseTemplate(body)
	if err != nil {
		return "", fmt.Errorf("prompt entry %q: %w", name, err)
	}
	blobHash, err := s.putBlob(ctx, []byte(body), "text/plain; charset=utf-8")
	if err != nil {
		return "", err
	}
	spec := PromptSpec{BodyBlobHash: blobHash, Slots: tmpl.Slots()}
	if spec.Slots == nil {
		spec.Slots = []string{} // a slotless template hashes "slots":[], never "slots":null
	}
	versionID, env, err := seal(KindPrompt, name, spec)
	if err != nil {
		return "", err
	}
	// body_blob_hash is denormalized out of the envelope so the FK to blob(content_hash) can exist;
	// 0002's prompt_entry_coherent trigger rejects any disagreement between the two.
	if err := s.publish(ctx, tablePrompt, versionID, name, env, "body_blob_hash", blobHash); err != nil {
		return "", err
	}
	return versionID, nil
}

// ResolvePrompt returns the prompt version a prompt_ref names, with its body fetched and parsed.
func (s *Store) ResolvePrompt(ctx context.Context, versionID string) (*PromptEntry, error) {
	env, err := s.resolve(ctx, tablePrompt, versionID)
	if err != nil {
		return nil, err
	}
	var spec PromptSpec
	name, err := decodeEnvelope(KindPrompt, versionID, env, &spec)
	if err != nil {
		return nil, err
	}
	body, err := s.blobs.Get(ctx, spec.BodyBlobHash)
	if err != nil {
		return nil, fmt.Errorf("prompt entry %s: %w", versionID, err)
	}
	tmpl, err := ParseTemplate(string(body))
	if err != nil {
		// The body hashes to what the version pins, so it is the template that was published — a
		// parse failure here means the parser's own rules changed under a published version.
		return nil, fmt.Errorf("%w: prompt %s body no longer parses: %v", ErrCorruptEntry, versionID, err)
	}
	return &PromptEntry{VersionID: versionID, Name: name, Spec: spec, Template: tmpl, Envelope: env}, nil
}
