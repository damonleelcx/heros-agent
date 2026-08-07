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
	for name, rows := range map[string][]string{"unicode": MarkUnicode, "ascii": MarkASCII, "skeleton": MarkSkeleton} {
		if len(rows) != MarkHeight {
			t.Errorf("%s mark is %d rows, want %d", name, len(rows), MarkHeight)
		}
		for i, row := range rows {
			if n := utf8.RuneCountInString(row); n != MarkWidth {
				t.Errorf("%s mark row %d is %d columns, want %d: %q", name, i, n, MarkWidth, row)
			}
		}
	}

	// Both pens render the SAME skeleton, so they cannot disagree about the figure — but only if every pen
	// renders every glyph. Compared by ink position: a cell is inked in one drawing exactly when it is inked
	// in the other. A pen missing an entry would drop ink on one platform only.
	for i := range MarkUnicode {
		u, a := []rune(MarkUnicode[i]), []rune(MarkASCII[i])
		for j := range u {
			if (u[j] == ' ') != (a[j] == ' ') {
				t.Errorf("row %d column %d: unicode has %q and ascii has %q — the fallback is drawing a "+
					"different figure, not the same one in plainer characters", i, j, u[j], a[j])
			}
		}
	}
}

// TestBothPensRenderEverySkeletonGlyph is what keeps the comparison above from passing vacuously.
//
// renderMark falls back to a space for a glyph a pen does not define, so a missing table entry does not crash
// — it silently erases part of the drawing, and erases it in BOTH pens if the glyph is missing from both,
// which the ink-parity check would then happily accept.
func TestBothPensRenderEverySkeletonGlyph(t *testing.T) {
	used := map[rune]bool{}
	for _, row := range MarkSkeleton {
		for _, r := range row {
			used[r] = true
		}
	}
	for _, pen := range []string{"unicode", "ascii"} {
		table := MarkGlyphs(pen)
		if table == nil {
			t.Fatalf("no %s pen", pen)
		}
		for r := range used {
			if _, ok := table[r]; !ok {
				t.Errorf("the %s pen has no glyph for skeleton character %q, so that ink is silently dropped",
					pen, r)
			}
		}
	}
}

// TestTheMarkIsTheWordHEROS — the drawing must be the word, and its H must still be the interval.
//
// A wordmark can decay in two directions and both look fine in a diff: a letter loses a stroke and reads as
// something else, or the H is replaced by an ordinary H and the figure that means something is gone.
func TestTheMarkIsTheWordHEROS(t *testing.T) {
	// The H is the first seven columns: two end caps, a span, and a point estimate with a blank cell of halo
	// on each side. The halo is not decoration — a dot set directly into the span reads as a defect in the
	// bar rather than as a mark of its own, which is the failure the source SVG's own comment describes.
	mid := []rune(MarkUnicode[MarkHeight/2])
	dot := -1
	for i, r := range mid {
		if r == '●' {
			dot = i
			break
		}
	}
	if dot < 0 {
		t.Fatalf("the middle row carries no point estimate: %q", string(mid))
	}
	if dot >= 7 {
		t.Errorf("the point estimate is at column %d, outside the H — the mark is supposed to BE the H of "+
			"the word, not sit beside it", dot)
	}
	if mid[dot-1] != ' ' || mid[dot+1] != ' ' {
		t.Errorf("the point estimate has no halo (%q): at this size a dot set into the span reads as a thick "+
			"spot in the bar and the H collapses", string(mid[:7]))
	}
	if mid[0] != '┃' || mid[6] != '┃' {
		t.Errorf("the H has lost an end cap: %q", string(mid[:7]))
	}

	// And the other four letters must still be there. Counted by columns of ink rather than read as glyphs:
	// each letter occupies a five-column cell after the H, and an empty one means a letter went missing.
	for n, letter := range []string{"E", "R", "O", "S"} {
		start := 8 + n*6
		inked := false
		for _, row := range MarkUnicode {
			r := []rune(row)
			for c := start; c < start+5 && c < len(r); c++ {
				if r[c] != ' ' {
					inked = true
				}
			}
		}
		if !inked {
			t.Errorf("columns %d-%d carry no ink, so the %s of HEROS is missing", start, start+4, letter)
		}
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
	// install.ps1 cannot hold either drawing: it has no UTF-8 BOM and is normally run through `irm | iex`,
	// so a box-drawing literal is decoded as the system ANSI code page and arrives as mojibake. It carries
	// the pen-neutral SKELETON, which is pure ASCII, plus both pen tables — so what is checked here is the
	// skeleton it draws and the code points it maps to, which is the same assertion one substitution later.
	for _, row := range MarkSkeleton {
		if !strings.Contains(ps, row) {
			t.Errorf("install.ps1 does not carry the skeleton row %q — Windows is drawing a different "+
				"picture than every other platform", row)
		}
	}
	for from, to := range MarkGlyphs("unicode") {
		if to == ' ' {
			continue
		}
		want := fmt.Sprintf("0x%04X", to)
		if !strings.Contains(ps, want) {
			t.Errorf("install.ps1 maps no skeleton %q to %s (%q) — the Windows drawing has drifted from "+
				"the one internal/distribution defines", from, want, to)
		}
	}
	for from, to := range MarkGlyphs("ascii") {
		if to == ' ' {
			continue
		}
		if !strings.Contains(ps, fmt.Sprintf("'%c' = '%c'", from, to)) {
			t.Errorf("install.ps1's ASCII pen does not map %q to %q, so a console that is not in UTF-8 draws "+
				"a different figure than the one internal/distribution defines", from, to)
		}
	}
	// And it must still choose the ASCII drawing when the console is not in UTF-8. Without this branch the
	// script would emit box-drawing bytes into a code page that cannot show them.
	if !strings.Contains(ps, "OutputEncoding.CodePage -eq 65001") {
		t.Error("install.ps1 does not check the console code page before drawing the unicode wordmark — on " +
			"a legacy code page it would print mojibake at the end of a successful install")
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
