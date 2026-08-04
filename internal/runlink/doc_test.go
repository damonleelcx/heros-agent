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

// runLinkSection returns just the run-link allowlist section (§1) of the contract doc. Each payload has
// its own section, and a check about one payload must read only its own.
func runLinkSection(t *testing.T) string {
	t.Helper()
	doc := contractDoc(t)
	_, after, found := strings.Cut(doc, "## 1. The egress allowlist")
	if !found {
		t.Fatal("the contract doc has no run-link allowlist section")
	}
	before, _, found := strings.Cut(after, "## 1b. The reported-verdict allowlist")
	if !found {
		t.Fatal("the contract doc has no reported-verdict section to bound §1 at")
	}
	return before
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
	// 🔴 Scoped to section 1, and this is the SECOND time this test has been narrowed for the same
	// reason. Keying on the category cell was the first fix — it stopped the exit-code table's `0` being
	// reported as an undocumented wire key. That fix holds only while one table uses those categories.
	// Section 1b (the reported verdict) uses `scores`, `metrics` and `eval` too, entirely legitimately,
	// and an unscoped scan then reports every one of its rows as a field the run-link allowlist does not
	// admit — which is true and irrelevant. A per-payload check needs a per-payload section.
	doc := runLinkSection(t)
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

// TestEveryVerdictKeyIsDocumented extends the same guarantee to the reported-verdict payload.
//
// It did not exist for the opt-in structure payload either, and that gap is why this one is written now
// rather than later: `WorkflowIRAllowlist` has crossed the boundary since P11 with no published table
// at all, which the two tests above would have caught had they been written against every allowlist
// instead of against `Allowlist`. See TestTheWorkflowIRContractIsUndocumented below, which records that
// gap as a failing-by-default fact rather than leaving it to be rediscovered.
func TestEveryVerdictKeyIsDocumented(t *testing.T) {
	doc := contractDoc(t)
	for _, f := range VerdictAllowlist {
		if !strings.Contains(doc, "`"+f.Name+"`") {
			t.Errorf("verdict allowlist key %q crosses the boundary but does not appear in the published "+
				"contract doc. A reported verdict is still customer data leaving a customer's machine.", f.Name)
		}
	}
}

// TestDocumentedVerdictKeysAreOnTheAllowlist is the other direction: the doc must not promise a field
// the code does not send, or keep one it no longer does.
func TestDocumentedVerdictKeysAreOnTheAllowlist(t *testing.T) {
	doc := contractDoc(t)
	section, _, found := strings.Cut(doc, "## 2. Machine output format")
	if !found {
		t.Fatal("could not find the end of the allowlist sections in the contract doc")
	}
	_, section, found = strings.Cut(section, "## 1b. The reported-verdict allowlist")
	if !found {
		t.Fatal("the contract doc has no reported-verdict section")
	}
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 3 {
			continue
		}
		for _, key := range backticked(cells[1]) {
			if !VerdictPermitted(key) {
				t.Errorf("the contract doc documents verdict wire key %q, which is NOT on "+
					"VerdictAllowlist — the doc promises a field we do not send", key)
			}
		}
	}
}

// The opt-in workflow-structure payload has crossed the boundary since P11 with no published table.
//
// 🔴 This is a REAL GAP, recorded here rather than fixed in the same change that found it: writing that
// table is a customer-facing privacy statement about fifteen fields, and it deserves its own review
// rather than riding along with a verdict contract. What this test does is make the gap impossible to
// forget — it fails the moment somebody publishes the section, at which point the check flips into the
// same drift fence the other two payloads have.
func TestTheWorkflowIRContractIsUndocumented(t *testing.T) {
	doc := contractDoc(t)
	documented := 0
	for _, f := range WorkflowIRAllowlist {
		if strings.Contains(doc, "`"+f.Name+"`") {
			documented++
		}
	}
	// `workflow_id` and `source_revision` appear in the run-link table under their own justification, so
	// a small overlap is expected and is not the section being written.
	if documented > 3 {
		t.Fatalf("%d of %d workflow-IR keys now appear in the contract doc — the section is being "+
			"written. Replace this test with the two-directional drift fence the other payloads have "+
			"(TestEveryVerdictKeyIsDocumented / TestDocumentedVerdictKeysAreOnTheAllowlist).",
			documented, len(WorkflowIRAllowlist))
	}
	t.Logf("KNOWN GAP: the opt-in workflow-structure payload (%d fields, contract %s) has no published "+
		"table in docs/decisions/p11-contracts.md. It transmits symbol names, file paths and line spans "+
		"from customer source, each justified in code, and customers have no published copy of that list.",
		len(WorkflowIRAllowlist), WorkflowIRContractVersion)
}
