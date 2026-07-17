package transform

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// unifiedDiff renders a deterministic unified diff for one file.
//
// # Why this is hand-rolled
//
// Task 2.3 requires the diff be BYTE-IDENTICAL across regenerations, and the artifact is
// content-hashed. `git diff`'s output depends on the user's git version and on environment config
// (diff.algorithm, diff.context, external drivers), so the same transform would hash differently on
// two machines. A pure function of the inputs cannot.
//
// # Why it diffs from the EDITS rather than from the two byte strings
//
// This engine already knows exactly what it changed — it built the edits. A general text differ has
// to rediscover that by search, and the cheap version of that (trim the common prefix and suffix,
// call the rest one changed block) is actively wrong here: with two edits in one file it spans them
// into a single block and renders every untouched line BETWEEN them as a remove+add pair. That diff
// claims six changed lines when two changed. For a system whose product IS the reviewable diff
// (ADR-001), a diff that over-reports what it touched is not cosmetic — it is the artifact lying
// about the one thing the reviewer is there to check.
//
// Deriving hunks from the known edits is exact and O(edits): every changed region is one an edit
// produced, and nothing else can appear.
//
// # Hunks hold MULTIPLE changed regions
//
// Two nearby edits share one hunk so their context windows do not overlap and print a line twice
// (which would make the patch fail to apply). But sharing a hunk is not the same as being one change:
// inside the hunk, each region prints as -/+ and the gaps BETWEEN them print as context. Collapsing
// the gap into the change is the same over-reporting bug in a smaller window.
const diffContext = 3

// region is one changed span, in 1-based inclusive line numbers, in both files' coordinates.
type region struct {
	oldStart, oldEnd int
	newStart, newEnd int
}

func unifiedDiff(path string, src, out []byte, edits []edit) []byte {
	if bytes.Equal(src, out) {
		return nil
	}
	hunks := groupRegions(editRegions(src, out, edits))
	if len(hunks) == 0 {
		return nil
	}
	oldLines, newLines := splitLines(src), splitLines(out)

	var buf bytes.Buffer
	// Standard a/ b/ prefixes so the output applies with `git apply` / `patch -p1`.
	fmt.Fprintf(&buf, "--- a/%s\n", path)
	fmt.Fprintf(&buf, "+++ b/%s\n", path)
	for _, h := range hunks {
		writeHunk(&buf, oldLines, newLines, h)
	}
	return buf.Bytes()
}

// writeHunk renders one hunk: leading context, then each changed region with the unchanged gaps
// between them as context, then trailing context.
func writeHunk(buf *bytes.Buffer, oldLines, newLines []string, h []region) {
	first, last := h[0], h[len(h)-1]

	lead := min(diffContext, first.oldStart-1)
	oldFrom := first.oldStart - lead
	newFrom := first.newStart - lead
	oldTo := min(len(oldLines), last.oldEnd+diffContext)
	newTo := min(len(newLines), last.newEnd+diffContext)

	fmt.Fprintf(buf, "@@ -%d,%d +%d,%d @@\n",
		oldFrom, oldTo-oldFrom+1, newFrom, newTo-newFrom+1)

	oldCur := oldFrom
	for _, r := range h {
		// Unchanged lines before this region — leading context, or the gap since the last region.
		// Printed once, from the old file; they are identical in both, and they count in both spans.
		for ; oldCur < r.oldStart; oldCur++ {
			buf.WriteString(" " + oldLines[oldCur-1] + "\n")
		}
		for i := r.oldStart; i <= r.oldEnd; i++ {
			buf.WriteString("-" + oldLines[i-1] + "\n")
		}
		for i := r.newStart; i <= r.newEnd; i++ {
			buf.WriteString("+" + newLines[i-1] + "\n")
		}
		oldCur = r.oldEnd + 1
	}
	for ; oldCur <= oldTo; oldCur++ {
		buf.WriteString(" " + oldLines[oldCur-1] + "\n")
	}
}

// editRegions maps each edit to the lines it changed, in both files' coordinates.
//
// The new-file offset of an edit is its old offset plus the net length change of every edit before it
// — the running delta applyEdits avoids needing by working right-to-left, computed here because a
// diff has to speak both coordinate systems at once.
func editRegions(src, out []byte, edits []edit) []region {
	if len(edits) == 0 {
		return nil
	}
	sorted := make([]edit, len(edits))
	copy(sorted, edits)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })

	var regions []region
	delta := 0
	for _, e := range sorted {
		newStartOff := e.Start + delta
		newEndOff := newStartOff + len(e.New)
		r := region{
			oldStart: lineOf(src, e.Start),
			oldEnd:   lineOf(src, maxInt(e.Start, e.End-1)),
			newStart: lineOf(out, newStartOff),
			newEnd:   lineOf(out, maxInt(newStartOff, newEndOff-1)),
		}
		// A pure insertion replaces no old line, but the line it lands in really did change, so a
		// unified diff must still show that line as -/+ rather than as context.
		if e.insertion() {
			r.oldEnd = r.oldStart
			r.newEnd = lineOf(out, newEndOff)
		}
		regions = append(regions, r)
		delta += len(e.New) - (e.End - e.Start)
	}
	return regions
}

// groupRegions batches regions into hunks. Two regions share a hunk when the gap between them is
// small enough that their context windows would touch — otherwise the same line would print in two
// hunks and the patch would not apply. Regions keep their identity inside the hunk; only the printing
// is shared.
func groupRegions(regions []region) [][]region {
	if len(regions) == 0 {
		return nil
	}
	hunks := [][]region{{regions[0]}}
	for _, r := range regions[1:] {
		cur := hunks[len(hunks)-1]
		prev := cur[len(cur)-1]
		if r.oldStart-prev.oldEnd <= 2*diffContext+1 {
			hunks[len(hunks)-1] = append(cur, r)
			continue
		}
		hunks = append(hunks, []region{r})
	}
	return hunks
}

// splitLines splits into lines WITHOUT trailing newlines. A file's trailing newline is not a line, so
// dropping the synthetic empty last element keeps hunk counts honest.
func splitLines(b []byte) []string {
	s := string(b)
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
