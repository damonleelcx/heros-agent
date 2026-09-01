// Package edit is the only place this system rewrites a customer's source, and it is built to refuse.
//
// # The asymmetry that shapes everything here
//
// A refused edit costs a person one exchange: they are told what could not be changed safely and why.
// A wrong edit costs them a corrupted file in their repository, discovered later, attributed to us.
// Those are not comparable, so every ambiguity below resolves to a refusal.
//
// # 🔴 What "safe" means concretely
//
// An edit is safe when it is UNAMBIGUOUS and REVERSIBLE by inspection:
//
//   - the text being replaced occurs exactly once in the file, so there is no question which occurrence
//     was meant;
//   - it is a contiguous span, so the change can be read as a diff rather than reconstructed;
//   - it does not span a structural boundary the transform cannot reason about;
//   - and applying it and re-reading the file reproduces exactly the text that was approved.
//
// The last one is the one people skip. A write that returns no error is not evidence the file says what
// you think — and this package is the last thing standing between a model's suggestion and somebody
// else's repository.
package edit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Proposal is one contiguous replacement in one file.
type Proposal struct {
	// Path is repository-relative, forward-slashed.
	Path string
	// Line is where Before starts, 1-indexed. Carried for display and for the diff header; the ANCHOR is
	// Before's text, never the number.
	//
	// 🔴 Line numbers are not identity. Between proposing and applying, an unrelated edit above can move
	// every line below it, and an edit applied by line number would then rewrite whatever now sits there
	// — silently, plausibly, in the right file.
	Line int
	// Before is the exact text to replace. Its uniqueness in the file is what makes the edit addressable.
	Before string
	// After is the replacement.
	After string
	// Axis and Rationale say which surface this changes and why, for the approval screen.
	Axis      string
	Rationale string
}

// Sentinel refusals. Typed because each leads to a different next action, and a caller that cannot tell
// them apart can only say "that did not work".
var (
	ErrNotFound    = errors.New("edit: the text to change is not in that file any more")
	ErrAmbiguous   = errors.New("edit: the text to change occurs more than once, so which one is meant is a guess")
	ErrNoChange    = errors.New("edit: the replacement is identical to what is already there")
	ErrOutsideRoot = errors.New("edit: the path leaves the repository")
	ErrEmpty       = errors.New("edit: the proposal is incomplete")
	ErrIndentation = errors.New("edit: the replacement's indentation does not match the code it replaces")
	ErrNotVerified = errors.New("edit: the file does not contain the approved text after writing")
)

// Validate checks a proposal against the file as it is NOW.
//
// Called before showing a diff and again immediately before applying. Twice on purpose: a person may
// take minutes or days to approve, and the file can change underneath in between. An approval is
// consent to a specific change to specific text, not a standing licence to edit that file.
func (p Proposal) Validate(root string) error {
	if p.Path == "" || p.Before == "" || p.After == "" {
		return fmt.Errorf("%w: path, before and after are all required", ErrEmpty)
	}
	if p.Before == p.After {
		return ErrNoChange
	}
	full, err := p.resolve(root)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("edit: reading %s: %w", p.Path, err)
	}
	text := string(body)

	switch n := strings.Count(text, p.Before); {
	case n == 0:
		return fmt.Errorf("%w: %s", ErrNotFound, p.Path)
	case n > 1:
		return fmt.Errorf("%w: %d occurrences in %s", ErrAmbiguous, n, p.Path)
	}
	if err := checkIndent(p.Before, p.After); err != nil {
		return err
	}
	return nil
}

// resolve turns a repository-relative path into an absolute one, refusing anything outside the root.
//
// 🔴 The check is on the RESOLVED paths, after symlinks. A prefix test against an unresolved path
// answers about a different file than the one that will be opened, which is the whole trick behind
// path-traversal.
func (p Proposal) resolve(root string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("edit: %s: %w", root, err)
	}
	full := filepath.Join(realRoot, filepath.FromSlash(p.Path))
	// The file must exist for a rewrite, so resolving it is legitimate; a missing file is a refusal.
	realFull, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNotFound, p.Path)
	}
	rel, err := filepath.Rel(realRoot, realFull)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrOutsideRoot, p.Path)
	}
	return realFull, nil
}

// checkIndent refuses a replacement whose first line is indented differently from the text it replaces.
//
// 🔴 In Python this is not cosmetic — indentation is the block structure, and a replacement that starts
// one level out moves the statement into a different scope while remaining valid, parseable code. The
// diff looks fine. The behaviour changes. This is the single most likely way a plausible-looking edit
// silently breaks an agent.
func checkIndent(before, after string) error {
	b, a := leadingSpace(firstLine(before)), leadingSpace(firstLine(after))
	if b != a {
		return fmt.Errorf("%w: replaced text starts with %d space(s), replacement with %d",
			ErrIndentation, len(b), len(a))
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func leadingSpace(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return s[:i]
		}
	}
	return s
}

// Apply writes the change and then VERIFIES it by re-reading the file.
//
// 🔴 The re-read is not belt and braces. A successful write means the call returned, not that the file
// says what you intended: a partial write, an encoding surprise, or a racing process all produce a nil
// error and a wrong file. This package is the last thing between a model's suggestion and somebody's
// repository, so it goes and looks.
func (p Proposal) Apply(root string) error {
	if err := p.Validate(root); err != nil {
		return err
	}
	full, err := p.resolve(root)
	if err != nil {
		return err
	}
	info, err := os.Stat(full)
	if err != nil {
		return fmt.Errorf("edit: %s: %w", p.Path, err)
	}
	body, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("edit: reading %s: %w", p.Path, err)
	}
	updated := strings.Replace(string(body), p.Before, p.After, 1)

	// Written through a temporary file in the same directory and renamed, so a crash mid-write leaves
	// the original intact rather than a truncated source file. The mode is carried over: a rewrite that
	// silently makes a file world-readable is a change nobody asked for.
	tmp, err := os.CreateTemp(filepath.Dir(full), ".heros-edit-*")
	if err != nil {
		return fmt.Errorf("edit: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if _, err := tmp.WriteString(updated); err != nil {
		tmp.Close()
		return fmt.Errorf("edit: writing %s: %w", p.Path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("edit: closing %s: %w", p.Path, err)
	}
	if err := os.Chmod(tmpName, info.Mode().Perm()); err != nil {
		return fmt.Errorf("edit: %w", err)
	}
	if err := os.Rename(tmpName, full); err != nil {
		return fmt.Errorf("edit: replacing %s: %w", p.Path, err)
	}

	// Go and look.
	//
	// 🔴 EXACT equality against the bytes we meant to write, not "does it contain the new text and not
	// the old". That second form is wrong whenever the replacement contains the original as a substring —
	// `MODEL=gpt-4o` becoming `MODEL=gpt-4o-mini` still contains `MODEL=gpt-4o`, so a correct edit reports
	// itself unverified. Its own test caught it here.
	//
	// The substring trap has now appeared four times in this codebase: "remember" contains "member" in the
	// router, "paragraph" contains "graph" in a synthesis test, an axis name inside another word in
	// validate, and this. Whenever a check asks "does this text appear in that text", the answer is
	// almost always the wrong question — compare whole values, or compare on boundaries.
	after, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("edit: re-reading %s: %w", p.Path, err)
	}
	if string(after) != updated {
		return fmt.Errorf("%w: %s does not match the approved change after writing", ErrNotVerified, p.Path)
	}
	return nil
}

// Diff renders a unified diff of the proposal, for a person to read before approving.
//
// Deliberately minimal and hand-rolled: what a reader needs is the exact lines removed and added with
// their file and position, and a diff library would add dependency surface for a presentation detail.
func (p Proposal) Diff() string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", p.Path, p.Path)
	before, after := strings.Split(p.Before, "\n"), strings.Split(p.After, "\n")
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", p.Line, len(before), p.Line, len(after))
	for _, l := range before {
		fmt.Fprintf(&b, "-%s\n", l)
	}
	for _, l := range after {
		fmt.Fprintf(&b, "+%s\n", l)
	}
	return b.String()
}

// IdempotencyKey identifies this change, so a retried application cannot make it twice.
//
// 🔴 Derived from the CONTENT — repository revision, path, and the exact text being replaced — never
// from a clock or a counter. A retry must produce the same key, and two different changes to the same
// file must produce different ones.
func (p Proposal) IdempotencyKey(revision string) string {
	return fmt.Sprintf("%s:%s:%d:%s", revision, p.Path, len(p.Before), hashOf(p.Before+"\x00"+p.After))
}

// hashOf is FNV-1a, sufficient for distinguishing proposals and not used for anything security-bearing.
func hashOf(s string) string {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return fmt.Sprintf("%016x", h)
}
