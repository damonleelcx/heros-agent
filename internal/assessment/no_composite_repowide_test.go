package assessment

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// no_composite_repowide_test.go is QA task 7.6 at the scope the task actually names: **no code path
// emits a composite score, grade or level.** Not "no function in this package" — `composite_fence_test
// .go` already covers that — but no path in the PRODUCT.
//
// # 🔴 Why this is the fence most likely to be needed later
//
// Task 7.6 says so, and design D3 says why: *"a composite is the single most likely thing to be added
// later by request, in a hurry, by someone who did not read this document."* It will not arrive as a
// pull request titled "add a score". It will arrive as one field on a view type, or one line of
// arithmetic in a component, added to satisfy a demo — and every test in the repository will stay
// green, because a composite is not a bug: it is a feature nobody agreed to.
//
// # What it scans, and why that set
//
// The three places a composite could reach a reader from:
//
//	internal/assessment      the domain. Guarded structurally as well; scanned here for the words.
//	internal/api/assessments.go   the wire. The last place the refusal can be enforced before it is a
//	                              screenshot.
//	web/console/src/…/assess…     the screen.
//
// 🚫 It does NOT scan the whole repository. `scoring`, `evalboard` and `scorecard` legitimately compute
// scores — comparative, verified, multi-seed, per eval set — and a fence that flagged them would be
// switched off within a week. R4 refuses a composite ACROSS THE NINE AXES; it does not refuse
// measurement.

// compositeWord is the vocabulary a composite arrives under. `score` is handled separately, because an
// eval set genuinely has one.
var compositeWord = regexp.MustCompile(`(?i)\b(grade|maturity|rating|overall|percentile|healthscore|health_score|composite)\b`)

// compositeShape is the ARITHMETIC. A word list catches the honest attempt; this catches the one that
// does not name itself — a ratio over the tally, or a division by the axis count.
var compositeShape = []*regexp.Regexp{
	regexp.MustCompile(`[Tt]ally\(\)\.\w+\s*/`),
	regexp.MustCompile(`tally\.\w+\s*/`),
	regexp.MustCompile(`/\s*(?:float64\()?len\(Axes\(\)\)`),
	regexp.MustCompile(`/\s*(?:findings|axes)\.length`),
}

// camelBoundary splits an identifier at its case transitions, so a word regex can see inside one.
//
// 🔴 This exists because the fence's first version missed the most likely shape there is. Go and
// TypeScript identifiers are CamelCase, and `\boverall\b` does not match `OverallGrade`: the word
// boundary needs a non-word character after "Overall", and "G" is a word character. A drill added
// `OverallGrade string` to the wire type and the fence reported clean — which is a fence that would
// have watched R4 be defeated by the naming convention the codebase uses everywhere.
//
// Splitting on the transition rather than dropping the boundary keeps `upgrade` safe: it has no case
// transition, so it stays one word and `\bgrade\b` does not match inside it.
var camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func splitIdentifiers(line string) string {
	return camelBoundary.ReplaceAllString(line, "$1 $2")
}

// refusals are the lines that USE a composite word to REFUSE a composite.
//
// 🔴 An explicit list of exact strings, not a heuristic. The obvious alternative — "allow the word when
// the line also contains a negation" — is a rule that admits `overallGrade > 0.5 ? ... : "not rated"`,
// and a fence that can be satisfied by phrasing is a fence. Listing the strings means a THIRD
// occurrence fails, which is the property worth having: the exemption cannot be inherited.
//
// Both entries are user- or model-VISIBLE text rather than code, and both say the same thing to their
// audience — there is no overall number here.
var refusals = []string{
	// The system prompt. The model is told not to produce one, which is the earliest place the refusal
	// can be enforced: an obliging model that offers a rating has it dropped at the parse, and this
	// stops it being offered.
	"Do not score, grade or rate anything.",
	// The copy on the surface itself. §8.2's answer to the manager, said FIRST rather than discovered:
	// stated proactively the absence reads as rigour, found in a demo it reads as a gap.
	"There is no overall score, and that is deliberate.",
}

func allowedComposite(line string) bool {
	for _, r := range refusals {
		if strings.Contains(line, r) {
			return true
		}
	}
	return false
}

func scanTargets(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	var out []string

	// The domain package.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the assessment package: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			out = append(out, e.Name())
		}
	}

	// The wire.
	out = append(out, filepath.Join(root, "internal", "api", "assessments.go"))

	// The screen. Every file the assessment surface is made of.
	console := filepath.Join(root, "web", "console", "src")
	out = append(out, []string{
		filepath.Join(console, "components", "assessment.tsx"),
		filepath.Join(console, "lib", "assessment.ts"),
		filepath.Join(console, "app", "app", "assess", "page.tsx"),
		filepath.Join(console, "app", "app", "assess", "controls.tsx"),
		filepath.Join(console, "app", "app", "assess", "data.ts"),
		filepath.Join(console, "app", "api", "console", "assessments", "route.ts"),
	}...)
	return out
}

// stripComments removes prose ABOUT a composite so an argument against one is not read as one.
//
// 🔴 Without this the fence is unusable: every file in this phase explains at length why there is no
// score, and those explanations contain the words. A fence that fired on its own rationale is a fence
// somebody deletes on the day they need it.
func stripComments(source string) string {
	source = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(source, "")
	source = regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(source, "")
	source = regexp.MustCompile(`(?m)\s//[^"'\n]*$`).ReplaceAllString(source, "")
	// JSX prose is not code either, but it IS user-visible — so it stays in scope for the word scan,
	// which is correct: a heading reading "Overall grade" is exactly what R4 forbids.
	return source
}

// TestNoPathInTheProductEmitsAComposite is the fence.
func TestNoPathInTheProductEmitsAComposite(t *testing.T) {
	targets := scanTargets(t)
	if len(targets) < 12 {
		t.Fatalf("the scan assembled only %d target(s) — it is not seeing the surface, so a clean "+
			"report means nothing", len(targets))
	}
	scanned := 0
	for _, path := range targets {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v — the fence cannot skip a file it was told to scan, because a moved file "+
				"would silently drop out of its scope", path, err)
		}
		scanned++
		body := stripComments(string(raw))

		for _, line := range strings.Split(body, "\n") {
			m := compositeWord.FindString(splitIdentifiers(line))
			if m == "" || allowedComposite(line) {
				continue
			}
			t.Errorf("%s names %q outside a comment:\n  %s\n"+
				"Nine axes do not reduce to one word any more than they reduce to one number — R4 refuses "+
				"a composite ACROSS THE AXES, and this is how the second attempt arrives. If this line is "+
				"a REFUSAL rather than an emission, add it to `refusals` with the argument for it.",
				path, m, strings.TrimSpace(line))
		}
		for _, shape := range compositeShape {
			if m := shape.FindString(body); m != "" {
				t.Errorf("%s contains %q — arithmetic over the tally spans the axes. The honest summary "+
					"is the distribution itself: nine numbers that sum to nine, from which no ordering of "+
					"one repository against another can be computed.", path, m)
			}
		}
	}
	if scanned != len(targets) {
		t.Fatalf("scanned %d of %d targets", scanned, len(targets))
	}
}

// TestTheOnlyScoreOnTheWireBelongsToAnEvalSet is the `score` half, which needs its own rule because an
// eval set legitimately has one.
//
// A score on a FINDING, on the ASSESSMENT, or on the tally is a composite. A score inside
// `EvalSetReport` is a measurement with an interval and a case list behind it, and it is the one number
// this phase produces that R4 does not refuse.
func TestTheOnlyScoreOnTheWireBelongsToAnEvalSet(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "api", "assessments.go"))
	if err != nil {
		t.Fatalf("reading the wire: %v", err)
	}
	body := stripComments(string(raw))
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(strings.ToLower(line), "score") {
			continue
		}
		// The only permitted mentions: the type reference and the JSON key of the eval-set payload.
		if strings.Contains(line, "EvalSetReport") || strings.Contains(line, "eval_set") ||
			strings.Contains(line, "EvalSetCannotFail") || strings.Contains(line, "EvalSet ") ||
			strings.Contains(line, "Scorecard") || strings.Contains(line, "scorecard") {
			continue
		}
		t.Errorf("the wire mentions a score outside an eval set:\n  %s", strings.TrimSpace(line))
	}
}
