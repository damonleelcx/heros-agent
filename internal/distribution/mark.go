package distribution

// mark.go is the Heros mark as a terminal drawing — the one the install scripts print when an install
// succeeds.
//
// # Why this lives in Go rather than in the scripts that print it
//
// The mark is drawn twice, once in `scripts/install.sh` and once in `scripts/install.ps1`, because neither
// script can call the other and neither can call this package. Two hand-maintained copies of the same
// picture drift, and the drift is silent in the way installers_test.go already describes: each script is
// correct read on its own, and nobody sees the two side by side. So the rows are written here once, and
// TestInstallScriptsCarryTheSameMark holds both copies to them.
//
// # What the drawing is, and what it drops
//
// The source mark (web/console/src/app/icon.svg, the console favicon) is an H that is also a confidence
// interval: two end caps, the span between them, and a point estimate sitting on the span, haloed so it
// reads as its own mark rather than as a thick spot in the bar. All three parts survive the trip to a
// terminal — the caps, the span, and the haloed dot — because they are the figure, and the figure is the
// claim the product makes.
//
// The tile does NOT survive, and that is deliberate rather than an omission. The SVG's tile is opaque and
// dark because a favicon has to look the same against any browser chrome. A terminal already supplies that
// background, and painting one would turn a five-row mark into a five-row block that fights every theme it
// is printed into.
//
// # Why eleven columns
//
// Terminal cells are about twice as tall as they are wide, so 11×5 is roughly the square the SVG tile is.
// Wider reads as a banner, and the point estimate stops being centred on anything.
const (
	// MarkWidth and MarkHeight are the drawing's extent. Asserted rather than assumed, because a row that
	// is one column short leaves the right-hand cap out of line and it is not obvious in a diff.
	MarkWidth  = 11
	MarkHeight = 5

	// MarkAccentHex is the stroke colour of the source SVG, without the `#`. The installer paints the mark
	// in it, so a rebrand that changed the SVG and not the installer would leave a user watching the old
	// colour at the one moment the product introduces itself. TestTerminalMarkUsesTheBrandAccent reads the
	// SVG and holds this to it.
	MarkAccentHex = "2ecfa8"
)

// MarkAccentRGB is MarkAccentHex as the three decimal components an SGR truecolor escape takes. Kept beside
// the hex rather than derived in the script, because a shell script that does its own hex arithmetic is a
// second place the colour can be wrong.
var MarkAccentRGB = [3]int{46, 207, 168}

// MarkUnicode is the drawing for a terminal that can render box-drawing characters: heavy verticals for the
// end caps, a heavy horizontal for the span, and a filled circle for the point estimate with a blank cell of
// halo on each side.
//
// The halo is not decoration. At this size, a dot set directly into the span reads as a defect in the bar
// rather than as a mark of its own — the same failure the SVG's comment describes at 16px.
var MarkUnicode = []string{
	"┃         ┃",
	"┃         ┃",
	"┃━━━ ● ━━━┃",
	"┃         ┃",
	"┃         ┃",
}

// MarkASCII is the same figure in characters that survive any encoding.
//
// This is a fallback, not a lesser brand: it is what a user on a non-UTF-8 locale or a legacy Windows code
// page actually sees, and mojibake at the end of an install reads as a broken install. The geometry is
// identical — same width, same height, same halo — so the two are the same picture drawn with different pens.
var MarkASCII = []string{
	"|         |",
	"|         |",
	"|--- o ---|",
	"|         |",
	"|         |",
}
