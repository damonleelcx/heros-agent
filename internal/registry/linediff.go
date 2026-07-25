package registry

import (
	"sort"
	"strings"
)

// Deterministic line diff and set diff for the prompt version diff read model (task 2.5).
//
// Hand-rolled rather than shelling out to `git diff` or pulling a diff dependency, for the same reason
// internal/transform/diff.go is hand-rolled: the output must be a pure, deterministic function of the
// two inputs so a diff computed on two machines is identical. git's output depends on the user's git
// version and config; an LCS over lines does not.

// lineDiff returns the line-level difference between two bodies as an ordered sequence of operations.
// It is a classic longest-common-subsequence diff over lines: shared lines are context, lines only in
// a are removes, lines only in b are adds. Byte-stable: no map iteration, no randomness.
func lineDiff(a, b string) []DiffLine {
	// A body with no trailing newline and one with a trailing newline differ, and that difference is
	// real (it changes the bytes sent). SplitAfter keeps the newlines so the split is loss-free, but
	// for line-level display we trim the trailing newline per line and diff on logical lines.
	al := splitLines(a)
	bl := splitLines(b)

	// LCS length table. Bodies are small (a prompt template), so O(n*m) is fine and simplest to keep
	// correct.
	n, m := len(al), len(bl)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	out := []DiffLine{}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case al[i] == bl[j]:
			out = append(out, DiffLine{Op: DiffContext, Text: al[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, DiffLine{Op: DiffRemove, Text: al[i]})
			i++
		default:
			out = append(out, DiffLine{Op: DiffAdd, Text: bl[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, DiffLine{Op: DiffRemove, Text: al[i]})
	}
	for ; j < m; j++ {
		out = append(out, DiffLine{Op: DiffAdd, Text: bl[j]})
	}
	return out
}

// splitLines splits a body into logical lines, dropping a single trailing empty line so that a body
// and "that body plus a final newline" do not diff as a spurious empty-line add.
func splitLines(s string) []string {
	if s == "" {
		return []string{}
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// setDiff returns (added, removed): elements in b but not a, and in a but not b. Both sorted, both
// non-nil so a "no change" answer serialises as [] rather than null.
func setDiff(a, b []string) (added, removed []string) {
	as := map[string]bool{}
	for _, x := range a {
		as[x] = true
	}
	bs := map[string]bool{}
	for _, x := range b {
		bs[x] = true
	}
	added, removed = []string{}, []string{}
	for _, x := range b {
		if !as[x] {
			added = append(added, x)
		}
	}
	for _, x := range a {
		if !bs[x] {
			removed = append(removed, x)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}
