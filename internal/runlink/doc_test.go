package runlink

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doc_test.go closes a gap this package's own comment created.
//
// allowlist.go says "the contract doc docs/decisions/p11-contracts.md renders from this list". It does
// not render from it — the doc carries a hand-maintained table — and nothing checked the two agreed.
// internal/erroreport/doc_test.go had already noticed the identical claim about its own allowlist and
// written it down; this is that observation applied here, after the eval widening drifted the table by
// six rows in a single change.
//
// 🔴 Why this matters more than ordinary doc drift: that table is the CUSTOMER-FACING PRIVACY CONTRACT.
// A field that crosses the boundary and is missing from it is a field the customer was never told about,
// and "we publish exactly what we send" is only true if something enforces it. A comment claiming a doc
// is generated, over a doc that is written by hand, is the most confident possible way to be wrong.

func contractDoc(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "decisions", "p11-contracts.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestEveryAllowlistedKeyIsDocumented: a field may not cross the boundary without appearing in the
// published contract.
func TestEveryAllowlistedKeyIsDocumented(t *testing.T) {
	doc := contractDoc(t)
	for _, f := range Allowlist {
		// Backticked, as the table renders it. Matching the rendered form rather than the bare string
		// stops a passing match against prose that merely mentions the word "metrics".
		if !strings.Contains(doc, "`"+f.Name+"`") {
			t.Errorf("allowlist key %q crosses the boundary but does not appear in the published "+
				"contract doc. The doc is the customer's copy of what we send; a field missing from it "+
				"is one they were never told about.", f.Name)
		}
	}
}

// TestDocumentedKeysAreOnTheAllowlist is the other direction, and the one that catches a doc promising
// less than the code does — or more.
//
// It checks the wire keys the table claims, not every backticked token in the file: the doc legitimately
// names internal identifiers, file paths and refused fields in prose. The table rows are the contract.
func TestDocumentedKeysAreOnTheAllowlist(t *testing.T) {
	doc := contractDoc(t)
	permitted := map[string]bool{}
	for _, f := range Allowlist {
		permitted[f.Name] = true
	}
	// Keys the doc names as REFUSED must not be on the allowlist; they are checked by their absence
	// below rather than by being listed here.
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 3 {
			continue
		}
		// Only the ALLOWLIST table, identified by its category column. The doc contains other tables —
		// the exit-code one has rows like "| `0` | ok |", and an unscoped scan reported exit code 0 as
		// an undocumented wire key. Keying on the category is what makes this test about the boundary
		// rather than about every backtick in the file.
		if !isAllowlistCategory(strings.TrimSpace(cells[2])) {
			continue
		}
		// The first cell may name two keys ("`scores.ci_low` / `scores.ci_high`").
		for _, key := range backticked(cells[1]) {
			if !permitted[key] {
				t.Errorf("the contract doc documents wire key %q, which is NOT on the allowlist. Either "+
					"the doc promises a field we do not send, or a field was removed from the boundary "+
					"without the customer's copy being updated.", key)
			}
		}
	}
}

// TestRefusedContentIsStillRefused pins the sentence the whole boundary exists for. A widening that
// quietly admitted one of these would pass every other test in this package.
func TestRefusedContentIsStillRefused(t *testing.T) {
	for _, forbidden := range []string{
		"prompt", "prompt_text", "source", "source_code", "file_contents", "diff",
		"env", "environment", "credential", "api_key", "token_value",
		"eval.cases", "eval.expected", "eval.judge_prompt", "eval.gate_thresholds",
	} {
		for _, f := range Allowlist {
			if f.Name == forbidden {
				t.Errorf("allowlist admits %q — content, not structure or measurement. This is the one "+
					"thing the package was built to make impossible.", forbidden)
			}
		}
	}
}

// backticked returns the `quoted` tokens in a table cell.
func backticked(cell string) []string {
	var out []string
	parts := strings.Split(cell, "`")
	// Odd indices are inside backticks.
	for i := 1; i < len(parts); i += 2 {
		if tok := strings.TrimSpace(parts[i]); tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

// isAllowlistCategory reports whether a table cell names one of the allowlist's categories.
//
// Derived from Allowlist rather than hard-coded: a new category (as `eval` was) must not silently
// exclude its own rows from the check that keeps them documented.
func isAllowlistCategory(s string) bool {
	for _, f := range Allowlist {
		if f.Category == s {
			return true
		}
	}
	return false
}
