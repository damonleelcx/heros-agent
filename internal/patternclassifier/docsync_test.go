package patternclassifier

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// DOC↔CODE DRIFT FENCE.
//
// The defect this exists for was not a missing pattern — the taxonomy always held all 20. It was two
// NUMBERINGS of the same 20: the PRD numbered them group-by-group while everyone else numbers them
// canonically, so "Pattern 5" meant Planning in one document and Tool Use in another. Nothing looked
// wrong, no test failed, and the set was complete the whole time.
//
// A prose table cannot be kept in sync by intention, so it is checked. These tests parse the SHIPPED
// documents and assert their numbering IS the code's numbering — which means the check fails if
// either side moves without the other, in either direction.

func readDoc(t *testing.T, rel string) string {
	t.Helper()
	path, err := filepath.Abs(rel)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}

// normTitle makes "Retrieval / RAG", "Retrieval/RAG" and "Reasoning Techniques (CoT/ToT/…)"
// comparable. The docs legitimately vary punctuation and add parenthetical gloss; what must not vary
// is WHICH pattern sits at WHICH number.
func normTitle(s string) string {
	if i := strings.Index(s, "("); i > 0 {
		s = s[:i]
	}
	s = strings.ToLower(strings.TrimSpace(s))
	for _, r := range []string{" ", "-", "&", "/", "·", ".", ","} {
		s = strings.ReplaceAll(s, r, "")
	}
	return s
}

// The PRD §8.3 table is the canonical published list. Its row numbers must be the code's ordinals.
func TestPRDTaxonomyTableMatchesTheCode(t *testing.T) {
	doc := readDoc(t, "../../docs/prd/P3.5-pattern-classifier.md")
	sec := doc[strings.Index(doc, "### 8.3 The fixed 20-pattern taxonomy"):]
	sec = sec[:strings.Index(sec, "### 8.4")]

	row := regexp.MustCompile(`(?m)^\| (\d+) \| ([^|]+)\|`)
	found := map[int]string{}
	for _, m := range row.FindAllStringSubmatch(sec, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		found[n] = strings.TrimSpace(m[2])
	}
	if len(found) != TaxonomySize {
		t.Fatalf("PRD §8.3 lists %d numbered patterns, taxonomy has %d", len(found), TaxonomySize)
	}
	for _, info := range Patterns() {
		title, ok := found[info.Ordinal]
		if !ok {
			t.Errorf("PRD §8.3 has no row #%d (should be %q)", info.Ordinal, info.Title)
			continue
		}
		if normTitle(title) != normTitle(info.Title) {
			t.Errorf("PRD §8.3 row #%d is %q, code says #%d is %q — the two numberings have drifted",
				info.Ordinal, title, info.Ordinal, info.Title)
		}
	}
	// The eight structural detectors must be marked ✅ and the twelve behavioral ones ⏳, so the
	// published table cannot promise a detector that does not ship.
	for _, m := range row.FindAllStringSubmatch(sec, -1) {
		n, _ := strconv.Atoi(m[1])
		info, ok := ByOrdinal(n)
		if !ok {
			continue
		}
		line := m[0]
		fullLine := sec[strings.Index(sec, line):]
		fullLine = fullLine[:strings.Index(fullLine, "\n")]
		shipsDetector := strings.Contains(fullLine, "✅")
		// Reflection ships a detector but is behavioral to CONFIRM; it is ✅ (as a candidate).
		want := info.Detection == DetectionStructural || info.Pattern == Reflection
		if shipsDetector != want {
			t.Errorf("PRD §8.3 row #%d (%s): table says ships-detector=%v, code says %v",
				n, info.Title, shipsDetector, want)
		}
	}
}

// The OpenSpec delta enumerates the taxonomy in prose with the same numbers. Prose is exactly where a
// second numbering was free to grow last time, so it is checked too.
func TestOpenSpecTaxonomyListMatchesTheCode(t *testing.T) {
	doc := readDoc(t, "../../openspec/changes/p3.5-pattern-classifier/specs/pattern-classifier/spec.md")
	entry := regexp.MustCompile(`(\d+)\. ([A-Z][A-Za-z&/ -]+?) \((control-flow|capability|coordination|governance)\)`)
	found := map[int][2]string{}
	for _, m := range entry.FindAllStringSubmatch(doc, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 || n > TaxonomySize {
			continue
		}
		found[n] = [2]string{strings.TrimSpace(m[2]), m[3]}
	}
	if len(found) != TaxonomySize {
		t.Fatalf("the spec enumerates %d numbered patterns, taxonomy has %d", len(found), TaxonomySize)
	}
	groupName := map[Group]string{
		GroupControlFlow: "control-flow", GroupCapability: "capability",
		GroupCoordination: "coordination", GroupGovernance: "governance",
	}
	for _, info := range Patterns() {
		got, ok := found[info.Ordinal]
		if !ok {
			t.Errorf("spec has no entry #%d (should be %q)", info.Ordinal, info.Title)
			continue
		}
		if normTitle(got[0]) != normTitle(info.Title) {
			t.Errorf("spec #%d is %q, code says %q", info.Ordinal, got[0], info.Title)
		}
		if got[1] != groupName[info.Group] {
			t.Errorf("spec #%d (%s) is in group %q, code says %q", info.Ordinal, info.Title, got[1], groupName[info.Group])
		}
	}
}

// The LLM prompt enumerates the taxonomy for the model. If it used a different numbering from the
// docs, a human reading the prompt and a human reading the PRD would be looking at different lists.
func TestFallbackPromptEnumeratesTheCanonicalNumbering(t *testing.T) {
	g := newGraph(fxAmbiguous().ir)
	prompt, _ := buildPrompt(g, Subgraph{SubgraphID: "x", NodeIDs: []string{"n_guard"}})
	for _, info := range Patterns() {
		want := strconv.Itoa(info.Ordinal) + ". " + string(info.Pattern)
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt does not enumerate %q at its canonical number", want)
		}
	}
}
