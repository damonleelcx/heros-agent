package distribution

import "strings"

// mark.go is the Heros wordmark as a terminal drawing — the one the install scripts and the CLI print.
//
// # What the drawing is
//
// The word HEROS, whose H is the mark: two end caps, the span between them, and a point estimate sitting on
// the span with a blank cell of halo on each side. That H is the figure this product always draws — a
// confidence interval with an estimate on it — and the figure it refuses to draw is a bare point with no
// interval around it. The other four letters are set in the same pen so the word reads as one object rather
// than as a logo with text stuck next to it.
//
// The tile from web/console/src/app/icon.svg does NOT survive the trip. That tile is opaque and dark because
// a favicon has to look the same against any browser chrome; a terminal already supplies a background, and
// painting one would turn a five-row wordmark into a five-row block that fights every theme it lands in.
//
// # One skeleton, two pens
//
// The drawing is written ONCE, in MarkSkeleton, in characters that are not the ones printed. Each pen renders
// it: box-drawing where the terminal can show it, ASCII everywhere else.
//
// The reason is scripts/install.ps1. That file has no UTF-8 BOM and is normally run through `irm | iex`, so on
// Windows PowerShell 5.1 its bytes are decoded as the system ANSI code page — a box-drawing character typed
// into it arrives as mojibake on exactly the platform it exists to serve. It therefore holds the SKELETON,
// which is pure ASCII, and maps it through the same tables below at runtime. A skeleton also makes the two
// pens incapable of disagreeing about the shape: they are the same string with different glyphs substituted,
// so the ASCII fallback can never quietly become a different picture.
//
// Digits in the skeleton are corner POSITIONS, never drawn as digits: 1 ┏, 2 ┓, 3 ┗, 4 ┛, 5 ┣.
const (
	// MarkWidth and MarkHeight are the drawing's extent, asserted rather than assumed: a row one column
	// short leaves a letter out of line, and that is not visible in a diff.
	MarkWidth  = 31
	MarkHeight = 5

	// MarkAccentHex is the stroke colour of the source SVG, without the `#`. The installers and the CLI paint
	// the wordmark in it, so a rebrand that changed the icon and not the terminal would leave a user watching
	// the old colour at the one moment the product introduces itself.
	MarkAccentHex = "2ecfa8"
)

// MarkAccentRGB is MarkAccentHex as the three decimal components an SGR truecolor escape takes. Kept beside
// the hex rather than derived at each call site, because a shell script doing its own hex arithmetic is a
// second place the colour can be wrong.
var MarkAccentRGB = [3]int{46, 207, 168}

// MarkSkeleton is the drawing, pen-neutral. H is seven columns because it is the mark and needs room for the
// halo; the other four are five, separated by a single column.
var MarkSkeleton = []string{
	`|     | 1---- 1---2 1---2 1---2`,
	`|     | |     |   | |   | |    `,
	`|- o -| 5---  5---4 |   | 3---2`,
	`|     | |     |  \  |   |     |`,
	`|     | 3---- |   \ 3---4 3---4`,
}

// markPens are the two renderings of the skeleton. Every skeleton character must appear in both, or
// TestBothPensRenderEverySkeletonGlyph fails: a missing entry would silently drop ink from one pen only.
var markPens = map[string]map[rune]rune{
	"unicode": {
		'|': '┃', '-': '━', 'o': '●', '\\': '╲',
		'1': '┏', '2': '┓', '3': '┗', '4': '┛', '5': '┣',
		' ': ' ',
	},
	// ASCII is a fallback, not a lesser brand: it is what a user on a non-UTF-8 locale or a legacy Windows
	// code page actually sees, and mojibake at the end of an install reads as a broken install. Every corner
	// becomes `+`, which is why the skeleton distinguishes them and the ASCII drawing does not need to.
	"ascii": {
		'|': '|', '-': '-', 'o': 'o', '\\': '\\',
		'1': '+', '2': '+', '3': '+', '4': '+', '5': '+',
		' ': ' ',
	},
}

// MarkUnicode is the wordmark for a terminal that can render box-drawing characters.
var MarkUnicode = renderMark("unicode")

// MarkASCII is the same wordmark in characters that survive any encoding.
var MarkASCII = renderMark("ascii")

// MarkGlyphs returns the pen's substitution table. install.ps1 carries the unicode table as code points and
// is held to this one, so the drawing Windows composes cannot drift from the drawing everyone else prints.
func MarkGlyphs(pen string) map[rune]rune { return markPens[pen] }

func renderMark(pen string) []string {
	table := markPens[pen]
	out := make([]string, 0, len(MarkSkeleton))
	for _, row := range MarkSkeleton {
		var b strings.Builder
		for _, r := range row {
			if g, ok := table[r]; ok {
				b.WriteRune(g)
				continue
			}
			// Unreachable while the pen tables are complete, and the test that keeps them complete is the
			// reason this does not panic: dropping to a space keeps a malformed pen from taking down an
			// install over a banner.
			b.WriteRune(' ')
		}
		out = append(out, b.String())
	}
	return out
}
