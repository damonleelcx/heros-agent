package converse

import "strings"

// textFieldStream pulls the value of the "text" field out of a JSON object while that object is still
// arriving, so a reply can be shown as it is written instead of after it is finished.
//
// # 🔴 Why this exists at all, rather than the model simply streaming prose
//
// The agent is asked for a JSON object, because its ACTION SURFACE IS CLOSED — the reply names one of
// `intent.All()` or it names nothing, and that is what every spend ceiling in the system hangs off.
// Streaming the raw completion therefore streams `{"action":"say","text":"Hel`, which is not something
// anyone can read. Asking the model for prose first and JSON afterwards would fix the display and break
// the guarantee: the field that constrains the capability would arrive last, after the console had
// already drawn something.
//
// So the shape stays, and the DISPLAY learns to read it early.
//
// # 🔴 This is best-effort and must stay that way
//
// Nothing here decides anything. The Outcome is still built by unmarshalling the COMPLETE object and
// validating every field, exactly as before — see Agent.apply. If this scanner misreads, gets confused
// by an unexpected shape, or emits nothing at all, the turn still lands correctly and the console still
// renders the finished reply. A display aid that can cost somebody their answer is not one.
//
// # Why there is no "action" handling
//
// It needs none. `{"action":"do",…}` carries no `text` field — it carries `capability` and `why` — so a
// decision turn simply never matches and nothing is emitted, which is exactly right: there is no prose
// to show yet, and the console keeps waiting. That falls out of looking for one field rather than
// having to be special-cased.
type textFieldStream struct {
	// buf holds only what has not yet been resolved: the part of the key we may be in the middle of
	// matching, or a trailing escape sequence that is not yet complete.
	buf   strings.Builder
	state streamState
	// pending is a partial escape (`\`, or `\u` with fewer than four digits) held back until the rest
	// arrives. Emitting it raw would print a backslash into somebody's answer.
	pending string
	done    bool
}

type streamState int

const (
	seekingKey streamState = iota // before `"text"` has been seen
	inValue                       // inside the string value, emitting
	finished                      // the value's closing quote was reached
)

// key is what is searched for. The leading quote matters: it prevents matching `text` inside a value
// that happens to contain the word.
const key = `"text"`

// Write feeds the next slice of raw completion and returns whatever became readable because of it.
//
// The return is the DECODED text — escapes resolved — because the caller is going to put it on a
// screen, and a surface that has to understand JSON escaping to display a sentence is a surface that
// will eventually show somebody a `\n`.
func (t *textFieldStream) Write(chunk string) string {
	if t.done || chunk == "" {
		return ""
	}
	switch t.state {
	case finished:
		return ""
	case seekingKey:
		t.buf.WriteString(chunk)
		s := t.buf.String()
		i := strings.Index(s, key)
		if i < 0 {
			// Keep only what could still be the start of the key. Holding the whole document would grow
			// without bound on a long reply for no benefit.
			if n := len(s); n > len(key) {
				keep := s[n-len(key):]
				t.buf.Reset()
				t.buf.WriteString(keep)
			}
			return ""
		}
		rest := s[i+len(key):]
		// Between the key and its value: optional space, a colon, optional space, then the opening quote.
		j := 0
		for j < len(rest) && (rest[j] == ' ' || rest[j] == '\t' || rest[j] == '\n' || rest[j] == '\r') {
			j++
		}
		if j >= len(rest) {
			t.buf.Reset()
			t.buf.WriteString(s[i:])
			return ""
		}
		if rest[j] != ':' {
			// Not the field we want after all — a key called "text" somewhere else. Skip past it.
			t.buf.Reset()
			t.buf.WriteString(rest)
			return ""
		}
		j++
		for j < len(rest) && (rest[j] == ' ' || rest[j] == '\t' || rest[j] == '\n' || rest[j] == '\r') {
			j++
		}
		if j >= len(rest) {
			t.buf.Reset()
			t.buf.WriteString(s[i:])
			return ""
		}
		if rest[j] != '"' {
			// A non-string value where prose was expected. Give up quietly rather than guess.
			t.state = finished
			return ""
		}
		t.state = inValue
		t.buf.Reset()
		return t.consume(rest[j+1:])
	default:
		return t.consume(chunk)
	}
}

// consume decodes as much of the string value as is unambiguously complete, holding back any partial
// escape for the next chunk.
func (t *textFieldStream) consume(s string) string {
	s = t.pending + s
	t.pending = ""
	var out strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			// Unescaped quote: the value is over.
			t.state = finished
			return out.String()
		case c == '\\':
			// 🔴 An escape that straddles a chunk boundary is held, not emitted. Without this a reply
			// containing a newline shows a stray backslash the instant the chunk splits there — and
			// chunk boundaries are chosen by the network, so it would be intermittent.
			if i+1 >= len(s) {
				t.pending = s[i:]
				return out.String()
			}
			e := s[i+1]
			if e == 'u' {
				if i+6 > len(s) {
					t.pending = s[i:]
					return out.String()
				}
				out.WriteRune(decodeHex4(s[i+2 : i+6]))
				i += 6
				continue
			}
			switch e {
			case 'n':
				out.WriteByte('\n')
			case 't':
				out.WriteByte('\t')
			case 'r':
				out.WriteByte('\r')
			case 'b':
				out.WriteByte('\b')
			case 'f':
				out.WriteByte('\f')
			default:
				// Covers \" \\ \/ and anything unexpected, which is passed through rather than dropped.
				out.WriteByte(e)
			}
			i += 2
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String()
}

// decodeHex4 turns four hex digits into a rune. An unparseable escape yields U+FFFD rather than an
// error: this is a display path, and one bad character must not cost the whole reply.
func decodeHex4(h string) rune {
	var v rune
	for i := 0; i < len(h); i++ {
		c := h[i]
		var d rune
		switch {
		case c >= '0' && c <= '9':
			d = rune(c - '0')
		case c >= 'a' && c <= 'f':
			d = rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = rune(c-'A') + 10
		default:
			return '�'
		}
		v = v*16 + d
	}
	return v
}
