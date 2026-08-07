package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/distribution"
)

// mark_test.go covers the greeting's mark: that it never reaches the machine stream, that it is suppressed
// everywhere it would be noise, and that it is the same drawing the installers are held to.

func envOf(pairs map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := pairs[k]
		return v, ok
	}
}

// TestGreetingMachineOutputIsUnaffectedByTheMark is the stream-split fence.
//
// The greeting emits a parseable envelope on stdout AND prose on stderr. A banner is precisely the
// "just add a line of output" change output.go warns about: put it on the wrong stream and every consumer
// that json.Unmarshals `heros` breaks, with a parse error that names no cause.
func TestGreetingMachineOutputIsUnaffectedByTheMark(t *testing.T) {
	// The draw is FORCED here. Written against the ordinary buffered path this test would be vacuous: a
	// bytes.Buffer is not a terminal, so nothing is drawn at all, and "the mark never reaches stdout" would
	// hold just as green with the mark wired directly to stdout.
	var out, errb bytes.Buffer
	pen := envOf(map[string]string{"LANG": "en_US.UTF-8", "COLORTERM": "truecolor"})
	Streams{Out: &out, Err: &errb}.narrateMark(true, "linux", pen)
	if !strings.Contains(errb.String(), distribution.MarkUnicode[2]) {
		t.Fatalf("the forced draw produced no mark on stderr, so nothing below is being tested:\n%q", errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("the mark wrote %d bytes to the MACHINE stream: %q", out.Len(), out.String())
	}

	_, stdout, _ := runEnv(t, map[string]string{
		"LANG": "en_US.UTF-8", "COLORTERM": "truecolor", "TERM": "xterm-256color",
	})
	var env Envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is no longer a single JSON document: %v\n%s", err, stdout)
	}
	if env.Command != "greeting" {
		t.Fatalf("stdout envelope is %q", env.Command)
	}
	for _, row := range append(append([]string{}, distribution.MarkUnicode...), distribution.MarkASCII...) {
		if strings.Contains(stdout, row) {
			t.Errorf("the mark reached STDOUT (%q) — machine output must carry the result and nothing else", row)
		}
	}
	if strings.Contains(stdout, "\x1b[") {
		t.Error("stdout carries an ANSI escape")
	}
}

// TestMarkIsSuppressedWhenTheDestinationIsNotATerminal — the default for every non-interactive caller.
//
// This is the case that actually happens: `heros > out.txt`, a Dockerfile, a CI step. It is also why every
// other test in this package sees no mark at all, since they write into a bytes.Buffer.
func TestMarkIsSuppressedWhenTheDestinationIsNotATerminal(t *testing.T) {
	if _, ok := resolveMark(false, "linux", envOf(map[string]string{"LANG": "en_US.UTF-8"})); ok {
		t.Error("resolveMark draws into a non-terminal")
	}
	_, _, stderr := runEnv(t, map[string]string{"LANG": "en_US.UTF-8", "COLORTERM": "truecolor"})
	for _, row := range distribution.MarkUnicode {
		if strings.Contains(stderr, row) {
			t.Errorf("the greeting drew the mark into a buffer that is not a terminal: %q", row)
		}
	}
	// And the greeting itself must still be there. A suppression that swallowed the prose too would pass
	// the assertion above for the wrong reason.
	if !strings.Contains(stderr, "cd your-repo && heros discover") {
		t.Error("suppressing the mark also suppressed the greeting")
	}
}

// TestMarkPenLadder walks every branch of the decision. Each row is a real terminal a user has.
func TestMarkPenLadder(t *testing.T) {
	utf8 := distribution.MarkUnicode
	ascii := distribution.MarkASCII
	for _, tc := range []struct {
		name   string
		goos   string
		env    map[string]string
		rows   []string
		wantOn string
	}{
		{
			name: "truecolor terminal draws the brand accent",
			goos: "darwin",
			env:  map[string]string{"LANG": "en_US.UTF-8", "COLORTERM": "truecolor", "TERM": "xterm-256color"},
			rows: utf8, wantOn: "\x1b[38;2;46;207;168m",
		},
		{
			name: "256-colour terminal degrades to the nearest indexed teal",
			goos: "linux",
			env:  map[string]string{"LANG": "C.UTF-8", "TERM": "screen-256color"},
			rows: utf8, wantOn: "\x1b[38;5;43m",
		},
		{
			name: "a plain terminal still gets colour, just not ours",
			goos: "linux",
			env:  map[string]string{"LANG": "en_GB.UTF-8", "TERM": "xterm"},
			rows: utf8, wantOn: "\x1b[36m",
		},
		{
			name: "NO_COLOR keeps the drawing and drops the colour",
			goos: "linux",
			env:  map[string]string{"LANG": "en_US.UTF-8", "TERM": "xterm-256color", "NO_COLOR": "1"},
			rows: utf8, wantOn: "",
		},
		{
			name: "TERM=dumb shows escapes literally, so it gets none",
			goos: "linux",
			env:  map[string]string{"LANG": "en_US.UTF-8", "TERM": "dumb"},
			rows: utf8, wantOn: "",
		},
		{
			name: "a non-UTF-8 locale gets the ASCII drawing of the same figure",
			goos: "linux",
			env:  map[string]string{"LANG": "en_US.ISO-8859-1", "TERM": "xterm-256color"},
			rows: ascii, wantOn: "\x1b[38;5;43m",
		},
		{
			name: "no locale set at all is not assumed to be UTF-8",
			goos: "linux",
			env:  map[string]string{"TERM": "xterm-256color"},
			rows: ascii, wantOn: "\x1b[38;5;43m",
		},
		{
			name: "LC_ALL wins over LANG, as the locale precedence says",
			goos: "linux",
			env:  map[string]string{"LC_ALL": "en_US.UTF-8", "LANG": "C", "TERM": "xterm"},
			rows: utf8, wantOn: "\x1b[36m",
		},
		{
			// The one that would ship visible garbage: a Go process does not enable VT processing on a
			// Windows console, so an escape sent to plain conhost is PRINTED rather than interpreted.
			name: "plain Windows console gets the drawing with no escapes at all",
			goos: "windows",
			env:  map[string]string{"LANG": "en_US.UTF-8", "TERM": "xterm-256color", "COLORTERM": "truecolor"},
			rows: utf8, wantOn: "",
		},
		{
			name: "Windows Terminal supports VT and says so",
			goos: "windows",
			env:  map[string]string{"LANG": "en_US.UTF-8", "COLORTERM": "truecolor", "WT_SESSION": "abc"},
			rows: utf8, wantOn: "\x1b[38;2;46;207;168m",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pen, ok := resolveMark(true, tc.goos, envOf(tc.env))
			if !ok {
				t.Fatal("nothing drawn")
			}
			if len(pen.rows) != len(tc.rows) || pen.rows[len(pen.rows)/2] != tc.rows[len(tc.rows)/2] {
				t.Errorf("drew %q, want %q", pen.rows, tc.rows)
			}
			if pen.on != tc.wantOn {
				t.Errorf("colour escape %q, want %q", pen.on, tc.wantOn)
			}
			if (pen.off == "") != (tc.wantOn == "") {
				t.Errorf("on=%q but off=%q — a colour left on bleeds into everything printed after it",
					pen.on, pen.off)
			}
		})
	}
}

// TestMarkIsTheSharedDrawing is the anti-third-copy fence.
//
// internal/distribution/mark.go is the one definition, and mark_test.go there holds both install scripts to
// it. This asserts the CLI reads the same constants rather than growing its own rows, which is the shape
// this would drift into: someone tweaks the greeting's spacing, and now three surfaces disagree.
func TestMarkIsTheSharedDrawing(t *testing.T) {
	// The truecolor rung specifically: it is the only one that carries the brand colour, so it is the only
	// one where "is this built from MarkAccentRGB" is a question with an answer. The indexed and plain-cyan
	// rungs are approximations by construction.
	pen, ok := resolveMark(true, "linux", envOf(map[string]string{
		"LANG": "en_US.UTF-8", "COLORTERM": "truecolor",
	}))
	if !ok {
		t.Fatal("nothing drawn")
	}
	for i, row := range pen.rows {
		if row != distribution.MarkUnicode[i] {
			t.Errorf("row %d is %q, but the shared drawing is %q — the CLI has its own copy of the mark",
				i, row, distribution.MarkUnicode[i])
		}
	}
	rgb := distribution.MarkAccentRGB
	want := "\x1b[38;2;" + itoa(rgb[0]) + ";" + itoa(rgb[1]) + ";" + itoa(rgb[2]) + "m"
	if pen.on != want {
		t.Errorf("the colour escape is %q, but distribution.MarkAccentRGB says %q — the CLI is painting the "+
			"mark a different colour than the installers and the favicon", pen.on, want)
	}
}
