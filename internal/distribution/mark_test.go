package distribution

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// mark_test.go holds the terminal mark to the three things it can silently stop being: the same picture in
// both installers, the same colour as the product, and printed only where it belongs.
//
// Everything here is a drift gate rather than a rendering test. Nobody will notice a mark that is one column
// short on Windows only, or a rebrand that repainted the SVG and left the installer on last year's green —
// those are seen once, by a user, at the moment the product introduces itself.

// TestMarkGeometry pins the drawing's shape before anything compares copies of it. A width assertion here is
// what makes the copy assertions meaningful: two scripts agreeing on a malformed row is not a property worth
// having.
func TestMarkGeometry(t *testing.T) {
	for name, rows := range map[string][]string{"unicode": MarkUnicode, "ascii": MarkASCII} {
		if len(rows) != MarkHeight {
			t.Errorf("%s mark is %d rows, want %d", name, len(rows), MarkHeight)
		}
		for i, row := range rows {
			if n := utf8.RuneCountInString(row); n != MarkWidth {
				t.Errorf("%s mark row %d is %d columns, want %d: %q", name, i, n, MarkWidth, row)
			}
		}
	}

	// The two pens must draw the same figure, not two figures that happen to be the same size. Compared by
	// ink position: a cell is inked in one drawing exactly when it is inked in the other.
	for i := range MarkUnicode {
		u, a := []rune(MarkUnicode[i]), []rune(MarkASCII[i])
		for j := range u {
			if (u[j] == ' ') != (a[j] == ' ') {
				t.Errorf("row %d column %d: unicode has %q and ascii has %q — the fallback is drawing a "+
					"different figure, not the same one in plainer characters", i, j, u[j], a[j])
			}
		}
	}

	// The halo: the point estimate must have a blank cell on each side. Without it the dot reads as a
	// thick spot in the bar and the H collapses — the failure the source SVG's own comment describes.
	bar := []rune(MarkUnicode[MarkHeight/2])
	mid := MarkWidth / 2
	if bar[mid] == ' ' || bar[mid-1] != ' ' || bar[mid+1] != ' ' {
		t.Errorf("the middle row has no haloed point estimate at its centre: %q", MarkUnicode[MarkHeight/2])
	}
}

// TestInstallScriptsCarryTheSameMark is the drift gate across the two installers.
//
// install.sh can hold the rows literally. install.ps1 cannot: it has no UTF-8 BOM and is normally run
// through `irm | iex`, so on Windows PowerShell 5.1 a box-drawing literal is decoded as the system ANSI code
// page and arrives as mojibake. It composes the glyphs from code points instead — so this test checks the
// code points it composes them FROM, which is the same assertion one decoding later.
func TestInstallScriptsCarryTheSameMark(t *testing.T) {
	sh := readScript(t, "install.sh")
	for _, row := range append(append([]string{}, MarkUnicode...), MarkASCII...) {
		if !strings.Contains(sh, row) {
			t.Errorf("install.sh does not draw the row %q — the installers and internal/distribution/mark.go "+
				"have drifted, and each is correct read on its own", row)
		}
	}

	ps := readScript(t, "install.ps1")
	// The ASCII pen is typed literally on both sides, so it is compared literally.
	for _, row := range MarkASCII {
		if strings.Contains(ps, row) {
			continue
		}
		// install.ps1 builds its rows from $cap/$span/$dot rather than pasting them, so the literal row is
		// not expected to appear; what must appear is the three ASCII pen characters it builds them from.
		for _, pen := range []string{`$cap = '|'`, `$span = '-'`, `$dot = 'o'`} {
			if !strings.Contains(ps, pen) {
				t.Errorf("install.ps1 does not use the ASCII pen %s that install.sh's fallback draws with", pen)
			}
		}
		break
	}
	// The unicode pen, as the code points the script composes. Derived from MarkUnicode rather than written
	// as literals, so a change to the drawing moves this assertion with it.
	bar := []rune(MarkUnicode[MarkHeight/2])
	for what, r := range map[string]rune{"cap": bar[0], "span": bar[1], "dot": bar[MarkWidth/2]} {
		want := fmt.Sprintf("0x%04X", r)
		if !strings.Contains(ps, want) {
			t.Errorf("install.ps1 does not compose the %s from %s (%q) — it is drawing a different glyph "+
				"than install.sh", what, want, r)
		}
	}
	// And it must still choose the ASCII drawing when the console is not in UTF-8. Without this branch the
	// script would emit box-drawing bytes into a code page that cannot show them.
	if !strings.Contains(ps, "OutputEncoding.CodePage -eq 65001") {
		t.Error("install.ps1 does not check the console code page before drawing the unicode mark — on a " +
			"legacy code page it would print mojibake at the end of a successful install")
	}
}

// TestTerminalMarkUsesTheBrandAccent reads the source SVG. The mark in the terminal and the mark in the
// browser tab are the same mark, and the colour is most of what makes them recognisable as such; a rebrand
// that edits the SVG and not the installer is the ordinary way the two come apart.
func TestTerminalMarkUsesTheBrandAccent(t *testing.T) {
	svgPath := filepath.Join("..", "..", "web", "console", "src", "app", "icon.svg")
	b, err := os.ReadFile(svgPath)
	if err != nil {
		t.Fatalf("the source mark is not readable at %s: %v", svgPath, err)
	}
	if !strings.Contains(strings.ToLower(string(b)), "#"+MarkAccentHex) {
		t.Errorf("%s no longer strokes the mark in #%s, so the installer paints it in a colour the product "+
			"stopped using", svgPath, MarkAccentHex)
	}

	// install.sh's truecolor escape must be that same colour, in the decimal form SGR takes.
	sh := readScript(t, "install.sh")
	want := fmt.Sprintf("38;2;%d;%d;%dm", MarkAccentRGB[0], MarkAccentRGB[1], MarkAccentRGB[2])
	if !strings.Contains(sh, want) {
		t.Errorf("install.sh does not paint the mark with the brand accent (expected the escape %q)", want)
	}
}

// TestTerminalMarkIsPrintedOnlyOnSuccess is the honesty gate.
//
// A banner is the one piece of output with no information in it, so the only thing that makes it acceptable
// is WHERE it appears. Printed before the verification steps, it would brand a run that goes on to refuse —
// and it would be the last friendly thing on screen above "Nothing was installed."
func TestTerminalMarkIsPrintedOnlyOnSuccess(t *testing.T) {
	sh := readScript(t, "install.sh")
	body := sh[strings.Index(sh, "set -eu"):]
	iSignature := strings.Index(body, `verify_signature "${TMP}/SHA256SUMS"`)
	iCall := strings.Index(body, "\nprint_mark\n")
	if iCall < 0 {
		t.Fatal("install.sh never calls print_mark")
	}
	if iCall < iSignature {
		t.Errorf("install.sh prints the mark at offset %d, before the signature check at %d: a refused "+
			"install would be branded", iCall, iSignature)
	}
	if strings.Count(body, "\nprint_mark\n") != 1 {
		t.Error("install.sh calls print_mark more than once — one of those calls is on a path that has not " +
			"finished verifying")
	}

	ps := readScript(t, "install.ps1")
	psBody := ps[strings.Index(ps, "Set-StrictMode"):]
	iPsCopy := strings.Index(psBody, "Copy-Item $assetPath $exe")
	iPsCall := strings.Index(psBody, "\n    Show-Mark\n")
	if iPsCall < 0 {
		t.Fatal("install.ps1 never calls Show-Mark")
	}
	if iPsCall < iPsCopy {
		t.Errorf("install.ps1 prints the mark at offset %d, before the binary is placed at %d", iPsCall, iPsCopy)
	}
}

// TestTerminalMarkStaysOutOfLogs — the mark must not reach a stream nobody is watching.
//
// `curl … | sh` pipes stdin, not stdout, so a real interactive install still draws it; what these guards
// exclude is `… | sh > install.log` and CI, where five rows of escapes are recorded as garbage in a file
// someone later greps for the reason an install failed.
func TestTerminalMarkStaysOutOfLogs(t *testing.T) {
	sh := readScript(t, "install.sh")
	fn := sh[strings.Index(sh, "print_mark() {"):]
	fn = fn[:strings.Index(fn, "\n}\n")]
	if !strings.Contains(fn, "[ -t 1 ]") {
		t.Error("install.sh's print_mark does not check that stdout is a terminal — redirected output would " +
			"carry the escapes into a log file")
	}
	if !strings.Contains(fn, "NO_COLOR") {
		t.Error("install.sh's print_mark ignores NO_COLOR")
	}
	if !strings.Contains(fn, `"${TERM:-}" != "dumb"`) {
		t.Error("install.sh's print_mark does not exempt TERM=dumb, where escapes are shown literally")
	}

	ps := readScript(t, "install.ps1")
	psFn := ps[strings.Index(ps, "function Show-Mark {"):]
	psFn = psFn[:strings.Index(psFn, "\n}\n")]
	if !strings.Contains(psFn, "IsOutputRedirected") {
		t.Error("install.ps1's Show-Mark does not check for redirected output")
	}
	if !strings.Contains(psFn, "NO_COLOR") {
		t.Error("install.ps1's Show-Mark ignores NO_COLOR")
	}
}
