package discovery

import (
	"fmt"
	"sort"
	"strings"
)

// Index is a corpus with its call sites computed once.
//
// # 🔴 Why this type exists, and the bug that produced it
//
// Ranking axis evidence by proximity to a call site means `ForAxis` needs the set of files that call a
// model. The first version computed it inside `ForAxis`, which is correct and quietly quadratic: the
// server asks for nine axes, each call rescanned every line of every file, and loading a 2,541-file
// repository took 26 seconds — for work whose answer is identical all nine times.
//
// The fix is not a cache hidden inside `ForAxis`. A hidden cache on a value type is a lie about
// aliasing, and the next person to copy a Corpus gets a stale index with no way to notice. So the reuse
// is a TYPE: build it once when a repository is loaded, hand it to everything that reads axes, and let
// the compiler make the sharing visible.
type Index struct {
	Corpus    Corpus
	nodes     []Node
	callFiles map[string]bool
	evidence  map[string]Evidence
}

// NewIndex scans the corpus ONCE, matching every axis in a single traversal.
//
// 🔴 One pass, not one per axis. Extracting each axis separately re-reads every line of every file nine
// times, applying two or three regexes each — 16 seconds on a 2,541-file repository after the call-site
// scan was already hoisted out. The patterns are independent of each other, so the traversal is the only
// thing that has to be shared, and sharing it is the whole of the fix.
func NewIndex(c Corpus) *Index {
	ix := &Index{Corpus: c, nodes: Nodes(c), callFiles: map[string]bool{},
		evidence: make(map[string]Evidence, len(axisPatterns))}
	for _, n := range ix.nodes {
		ix.callFiles[n.Span.Path] = true
	}

	spans := make(map[string][]Span, len(axisPatterns))
	seen := make(map[string]map[string]bool, len(axisPatterns))
	for axis := range axisPatterns {
		seen[axis] = map[string]bool{}
	}

	for _, f := range c.Files {
		for i, line := range f.Lines {
			if isComment(line, f.Language) {
				continue
			}
			// The gate: one regex instead of thirty on the lines that match nothing, which is most of them.
			if !anySignal.MatchString(line) {
				continue
			}
			for axis, pats := range axisPatterns {
				for _, p := range pats {
					if !p.re.MatchString(line) {
						continue
					}
					start := max(0, i-p.before)
					end := min(len(f.Lines), i+p.after+1)
					key := fmt.Sprintf("%s:%d", f.Path, start)
					if seen[axis][key] {
						break
					}
					seen[axis][key] = true
					spans[axis] = append(spans[axis], Span{
						Path: f.Path, Line: start + 1,
						Text: strings.Join(f.Lines[start:end], "\n"), Why: p.why,
					})
					break // one span per line per axis; the first matching pattern names it
				}
			}
		}
	}

	for axis := range axisPatterns {
		ix.evidence[axis] = finishAxis(c, axis, spans[axis], ix.callFiles)
	}
	return ix
}

// finishAxis turns raw matches into the reported evidence: absence note, ranking, and cap.
func finishAxis(c Corpus, axis string, spans []Span, callFiles map[string]bool) Evidence {
	ev := Evidence{Axis: axis, Spans: spans}
	if len(spans) == 0 {
		// 🔴 The note distinguishes the two absences a reader must never confuse: nothing governs this
		// axis, versus we could not read this repository. The corpus knows which, so it says.
		ev.Note = fmt.Sprintf(
			"No code matching any %s signal was found in %d files. That is a finding: this agent may "+
				"have no explicit %s handling at all.", axis, len(c.Files), axis)
		if c.Truncated {
			ev.Note = fmt.Sprintf(
				"No %s signal was found, but the walk hit its limits and read only %d files — so this "+
					"is an absence of evidence rather than evidence of absence.", axis, len(c.Files))
		}
		return ev
	}
	ev.Found = true
	rank(ev.Spans, callFiles)
	if len(ev.Spans) > maxSpansPerAxis {
		near := 0
		for _, s := range ev.Spans[:maxSpansPerAxis] {
			if callFiles[s.Path] {
				near++
			}
		}
		ev.Note = fmt.Sprintf(
			"%d spans matched; showing %d, ranked by proximity to a call site (%d of those are in a "+
				"file that calls a model).", len(ev.Spans), maxSpansPerAxis, near)
		ev.Spans = ev.Spans[:maxSpansPerAxis]
	}
	return ev
}

// Nodes returns the call sites, already computed.
func (ix *Index) Nodes() []Node { return ix.nodes }

// LooksLikeAnAgent reports whether the repository calls a model at all in non-test code.
//
// A first-class answer rather than something a caller infers from an empty node list, because it is the
// one conclusion that changes what every other conclusion MEANS. A nine-axis report over a repository
// that never calls a model is nine paragraphs about nothing — and every axis would honestly report "no
// signal found", which reads as nine weaknesses rather than one wrong subject.
func (ix *Index) LooksLikeAnAgent() (bool, string) {
	if len(ix.nodes) > 0 {
		return true, fmt.Sprintf("%d call sites across %d files", len(ix.nodes), len(ix.callFiles))
	}
	note := fmt.Sprintf("No code in %d non-test files calls a model.", len(ix.Corpus.Files))
	if n := ix.Corpus.Skipped["test-file"]; n > 0 {
		note += fmt.Sprintf(" %d test files were excluded; if the only model calls live in tests, "+
			"there is no agent in production code to assess.", n)
	}
	return false, note
}

// ForAxis returns one axis's evidence, computed at NewIndex time.
func (ix *Index) ForAxis(axis string) Evidence {
	ev, ok := ix.evidence[axis]
	if !ok {
		return Evidence{Axis: axis, Note: fmt.Sprintf("%q is not one of the nine axes", axis)}
	}
	return ev
}

// Excerpt renders an axis's evidence as the text a model reads, satisfying the AxisSource contract.
//
// The second return is false when there is nothing to assess, which is what makes an unreadable axis
// fail loudly instead of quietly assessing an empty string.
func (ix *Index) Excerpt(axis string) (string, bool) {
	ev := ix.ForAxis(axis)
	if !ev.Found {
		return "", false
	}
	var b strings.Builder
	for _, s := range ev.Spans {
		fmt.Fprintf(&b, "%s  (%s)\n%s\n\n", s.Ref(), s.Why, s.Text)
	}
	if ev.Note != "" {
		fmt.Fprintf(&b, "[%s]\n", ev.Note)
	}
	return strings.TrimSpace(b.String()), true
}

// rank orders spans so files that call a model come first, then by path for determinism.
func rank(spans []Span, callFiles map[string]bool) {
	sort.SliceStable(spans, func(i, j int) bool {
		a, b := callFiles[spans[i].Path], callFiles[spans[j].Path]
		if a != b {
			return a
		}
		return spans[i].Path < spans[j].Path
	})
}
