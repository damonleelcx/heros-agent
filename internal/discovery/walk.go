// Package discovery reads a pinned repository and finds the agent inside it: where it calls a model, and
// what its code says on each of the nine axes.
//
// # What discovery is responsible for, and the line it does not cross
//
// It finds EVIDENCE — real spans of the customer's own code, with file and line — and it stops there.
// It does not decide whether the code is good; a model does that, reading what this package extracted.
//
// The split matters because the two halves fail differently. Extraction fails deterministically and the
// same way every time, so it can be tested against fixtures and fixed. Judgement fails by being wrong
// in a plausible sentence. Keeping them apart means an assessment that says something false can be
// traced to which half produced it — and it means "I could not find any code governing memory" is a
// finding this package can state, rather than a gap the model papers over.
//
// # 🔴 The walk is bounded in four ways, and each bound has a failure it prevents
//
//   - Containment: never read outside the resolved root. An agent that can be pointed at `/` and will
//     dutifully read it is a data-exfiltration tool with a friendly name.
//   - Skipped directories: vendored and generated trees are almost all of the bytes and almost none of
//     the customer's decisions. Reading them buys nothing and costs the file budget.
//   - File size: one enormous minified bundle can exhaust the budget alone.
//   - File count: a monorepo is not a reason to read for ten minutes.
package discovery

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Limits bound a walk. Zero values are replaced by DefaultLimits at Walk time, because a zero limit here
// means "unset" rather than "read nothing", and the caller most likely to leave them zero is a test.
type Limits struct {
	MaxFileBytes  int64
	MaxFiles      int
	MaxTotalBytes int64
}

// DefaultLimits are sized for a normal application repository.
func DefaultLimits() Limits {
	return Limits{MaxFileBytes: 512 << 10, MaxFiles: 4000, MaxTotalBytes: 64 << 20}
}

// skipDirs are never entered. Vendored and generated trees are most of the bytes and none of the
// decisions; `.git` additionally holds every version of everything.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "__pycache__": true,
	".venv": true, "venv": true, "env": true, ".tox": true, ".mypy_cache": true,
	"dist": true, "build": true, "target": true, ".next": true, ".nuxt": true,
	"site-packages": true, ".pytest_cache": true, "coverage": true, ".idea": true,
	".gradle": true, "Pods": true, ".terraform": true, ".cache": true,
}

// Language is a source language discovery can read.
type Language string

const (
	Python     Language = "python"
	TypeScript Language = "typescript"
	JavaScript Language = "javascript"
	Go         Language = "go"
	Unknown    Language = ""
)

// languageOf maps an extension to a language. Extensions rather than content sniffing: sniffing is
// slower, wrong on short files, and the mistake it makes is silent.
func languageOf(path string) Language {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".py":
		return Python
	case ".ts", ".tsx":
		return TypeScript
	case ".js", ".jsx", ".mjs":
		return JavaScript
	case ".go":
		return Go
	}
	return Unknown
}

// File is one readable source file.
type File struct {
	// Path is relative to the root, always with forward slashes, so a finding reads the same on every
	// platform and can be pasted into a link.
	Path     string
	Language Language
	Lines    []string
}

// Corpus is everything discovery read, plus an honest account of what it did not.
type Corpus struct {
	Root  string
	Files []File
	// Skipped counts files not read, by reason. 🔴 Reported rather than dropped: "I read 12 files" and
	// "I read 12 of 4,000 files because the rest were too large" support very different conclusions, and
	// only one of them is honest about a report that found nothing.
	Skipped map[string]int
	// Truncated says a limit was reached, so the corpus is a sample rather than the repository.
	Truncated bool
}

// LanguageCounts summarises what the repository is written in.
func (c Corpus) LanguageCounts() map[Language]int {
	out := map[Language]int{}
	for _, f := range c.Files {
		out[f.Language]++
	}
	return out
}

var ErrEmptyCorpus = errors.New("discovery: no readable source files")

// Walk reads a repository within its limits.
func Walk(root string, lim Limits) (Corpus, error) {
	d := DefaultLimits()
	if lim.MaxFileBytes <= 0 {
		lim.MaxFileBytes = d.MaxFileBytes
	}
	if lim.MaxFiles <= 0 {
		lim.MaxFiles = d.MaxFiles
	}
	if lim.MaxTotalBytes <= 0 {
		lim.MaxTotalBytes = d.MaxTotalBytes
	}

	// 🔴 The root is resolved once, here, and every candidate path is checked against THIS value. A
	// containment check against an unresolved root answers about a different directory than the one
	// being walked.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Corpus{}, fmt.Errorf("discovery: %s: %w", root, err)
	}

	c := Corpus{Root: realRoot, Skipped: map[string]int{}}
	var total int64

	err = filepath.WalkDir(realRoot, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			c.Skipped["unreadable"]++
			return nil //nolint:nilerr // one unreadable entry must not abort the walk
		}
		if e.IsDir() {
			if path != realRoot && (skipDirs[e.Name()] || strings.HasPrefix(e.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		// Never follow a symlink: it is the one entry that can leave the tree while looking like it did
		// not. Counted rather than silently ignored.
		if e.Type()&os.ModeSymlink != 0 {
			c.Skipped["symlink"]++
			return nil
		}
		lang := languageOf(path)
		if lang == Unknown {
			return nil
		}
		// 🔴 Tests are excluded, and this was learned from real repositories rather than reasoned about.
		//
		// Run against two real codebases, nearly every call site and nearly every axis span landed in a
		// test file — because tests instantiate models, set temperatures, build message lists and loop,
		// and they do it more explicitly than production code does. An assessment built from them
		// describes the test suite while appearing to describe the agent, with real file:line references
		// the reader can follow to real code. That is the most convincing way to be wrong.
		//
		// Counted rather than silently dropped: a repository whose only model calls are in tests is a
		// real thing, and the report has to be able to say so.
		if isTestPath(rel(realRoot, path)) {
			c.Skipped["test-file"]++
			return nil
		}
		if !within(realRoot, path) {
			c.Skipped["outside-root"]++
			return nil
		}
		if len(c.Files) >= lim.MaxFiles || total >= lim.MaxTotalBytes {
			c.Truncated = true
			c.Skipped["over-budget"]++
			return nil
		}
		info, err := e.Info()
		if err != nil {
			c.Skipped["unreadable"]++
			return nil //nolint:nilerr
		}
		if info.Size() > lim.MaxFileBytes {
			c.Skipped["too-large"]++
			return nil
		}
		lines, err := readLines(path)
		if err != nil {
			c.Skipped["unreadable"]++
			return nil //nolint:nilerr
		}
		rel, err := filepath.Rel(realRoot, path)
		if err != nil {
			c.Skipped["unreadable"]++
			return nil //nolint:nilerr
		}
		total += info.Size()
		c.Files = append(c.Files, File{Path: filepath.ToSlash(rel), Language: lang, Lines: lines})
		return nil
	})
	if err != nil {
		return c, fmt.Errorf("discovery: walking %s: %w", realRoot, err)
	}
	sort.Slice(c.Files, func(i, j int) bool { return c.Files[i].Path < c.Files[j].Path })
	if len(c.Files) == 0 {
		return c, fmt.Errorf("%w under %s", ErrEmptyCorpus, realRoot)
	}
	return c, nil
}

// rel is the repository-relative slash path, or the raw path when it cannot be made relative.
func rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(r)
}

// testSuffixes and testDirs identify test code across the languages discovery reads.
var testSuffixes = []string{
	"_test.go", "_test.py", "_tests.py", ".test.ts", ".test.tsx", ".test.js", ".test.jsx",
	".spec.ts", ".spec.tsx", ".spec.js", ".spec.jsx",
}

var testDirs = map[string]bool{
	"tests": true, "test": true, "__tests__": true, "testdata": true,
	"e2e": true, "spec": true, "specs": true, "fixtures": true, "conftest": true,
}

// isTestPath reports whether a repository-relative path is test code.
func isTestPath(p string) bool {
	base := path0(p)
	for _, suf := range testSuffixes {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	if strings.HasPrefix(base, "test_") || strings.HasPrefix(base, "conftest") {
		return true
	}
	for _, seg := range strings.Split(p, "/") {
		if testDirs[strings.ToLower(seg)] {
			return true
		}
	}
	return false
}

func path0(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// within reports whether path is inside root. String prefixes alone are not enough — `/a/bc` has the
// prefix `/a/b` — so the separator is required.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// readLines reads a file as lines, refusing anything that looks binary.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	// A generated bundle can be one line of several megabytes; the default scanner buffer would return
	// an error the caller would read as "unreadable file" rather than "one absurd line".
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.IndexByte(line, 0) >= 0 {
			return nil, errors.New("binary content")
		}
		lines = append(lines, line)
	}
	return lines, sc.Err()
}
