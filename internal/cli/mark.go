package cli

import (
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/heros-foreal/agentd/internal/distribution"
)

// mark.go draws the Heros mark above the greeting — the first thing a new user sees from the CLI itself,
// and the only thing they see from it if they installed through a package manager.
//
// # Why it is here and not only in the installer
//
// scripts/install.sh and scripts/install.ps1 draw the mark when they finish. Nobody who typed
// `brew install`, `dpkg -i`, or `docker run` ever executes those scripts, so for those users the greeting is
// the introduction. The rows come from internal/distribution, the same constants the installers are held to,
// so this is a third surface for one drawing rather than a third copy of it.
//
// # Where it is written, and why that is not a detail
//
// STDERR, through Narratef, like every other word a human reads. The greeting also emits a machine envelope
// on stdout, and a consumer that parses it must never receive five rows of box-drawing characters — the
// stream split is the contract output.go describes, and a banner is exactly the "just add a log line" change
// that breaks a downstream parser. TestGreetingMachineOutputIsUnaffectedByTheMark holds it.

// markPen is a resolved decision about how to draw: which characters, and what to wrap them in.
type markPen struct {
	rows []string
	// on and off are the colour escapes, both empty when the mark is drawn without colour.
	on, off string
}

// resolveMark decides whether and how to draw. It is a pure function of the things that vary — whether the
// destination is a terminal, the platform, and the environment — so every branch is testable without a pty.
//
// The three suppressions are the same ones install.sh applies, for the same reasons:
//
//   - not a terminal: `heros > out.txt`, a CI job, or a pipe. Escapes recorded in a log file are noise in
//     the artifact someone later reads to find out why a build failed.
//   - NO_COLOR, or TERM=dumb: the user asked for no colour, or the terminal shows escapes literally. The
//     mark is still DRAWN in that case — refusing colour is not refusing the drawing.
//   - a locale that cannot carry box-drawing characters: the ASCII drawing of the same figure is used
//     instead, because mojibake reads as a broken tool.
func resolveMark(tty bool, goos string, env func(string) (string, bool)) (markPen, bool) {
	if !tty {
		return markPen{}, false
	}
	get := func(k string) string {
		if env == nil {
			return ""
		}
		v, _ := env(k)
		return v
	}

	pen := markPen{rows: distribution.MarkASCII}
	locale := get("LC_ALL")
	if locale == "" {
		locale = get("LC_CTYPE")
	}
	if locale == "" {
		locale = get("LANG")
	}
	if l := strings.ToLower(locale); strings.Contains(l, "utf-8") || strings.Contains(l, "utf8") {
		pen.rows = distribution.MarkUnicode
	}

	if get("NO_COLOR") != "" {
		return pen, true
	}
	termName := get("TERM")
	if termName == "dumb" {
		return pen, true
	}
	// Windows is the one platform where emitting an escape can be worse than emitting none. A Go process
	// does not turn on ENABLE_VIRTUAL_TERMINAL_PROCESSING for its console, so on plain conhost the escape
	// is printed as literal characters — the mark would arrive wearing its own control codes. Windows
	// Terminal always supports VT and always sets WT_SESSION, so that is the signal used; everywhere else
	// on Windows the mark is drawn uncoloured, which is the same trade install.ps1 makes.
	if goos == "windows" {
		if get("WT_SESSION") == "" {
			return pen, true
		}
	}

	switch colorTerm := get("COLORTERM"); {
	case colorTerm == "truecolor" || colorTerm == "24bit":
		rgb := distribution.MarkAccentRGB
		pen.on = "\x1b[38;2;" + itoa(rgb[0]) + ";" + itoa(rgb[1]) + ";" + itoa(rgb[2]) + "m"
	case strings.Contains(termName, "256color") || strings.Contains(termName, "direct"):
		pen.on = "\x1b[38;5;43m"
	default:
		pen.on = "\x1b[36m"
	}
	pen.off = "\x1b[0m"
	return pen, true
}

// itoa is strconv.Itoa without the import, kept local so the colour escape is assembled from
// distribution.MarkAccentRGB rather than from a second literal copy of the brand colour.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// narrateMark draws the mark on the human stream, or draws nothing.
//
// tty is a PARAMETER rather than something this reads off s.Err, so a test can force the drawing and then
// assert where it landed. That is not a convenience: with a bytes.Buffer the mark is suppressed anyway, so a
// "the mark never reaches stdout" test written against the buffered path passes without ever drawing
// anything — it would hold just as green with the mark wired to stdout.
func (s Streams) narrateMark(tty bool, goos string, env func(string) (string, bool)) {
	pen, ok := resolveMark(tty, goos, env)
	if !ok {
		return
	}
	for _, row := range pen.rows {
		s.Narratef("  %s%s%s", pen.on, row, pen.off)
	}
	s.Narratef("")
}

// isTerminal reports whether w is a real terminal. A writer that is not an *os.File — the bytes.Buffer every
// test uses, or a pipe — is never one, which is why the tests observe the suppressed path by default and the
// drawing has to be asked for explicitly.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
