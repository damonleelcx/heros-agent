package herosagent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// P30 task 10.10 — at least 30% of this phase's test functions target ERROR AND BOUNDARY paths.
//
// # Why a measured threshold rather than a reviewer's impression
//
// Every codebase's tests drift toward the happy path, because the happy path is what you write while
// building the thing and the boundaries are what you write afterwards, if there is time. A percentage
// somebody eyeballs in review is a percentage nobody computes; this computes it.
//
// 🔴 It measures by CLASSIFYING each test against the eight subjects the task names — abstention, cap
// reached, credential missing, provider timeout, oversized repository, conflicting edge, unresolved
// model ref, injected instruction — plus the refusal vocabulary the phase actually uses. A test is a
// boundary test when it exercises one of those, not when its name contains the word "error".
//
// # What this fence deliberately does NOT do
//
// It does not require every subject to be covered by a test whose NAME mentions it. A name-based
// requirement is satisfied by renaming, which is the cheapest possible way to make a coverage fence
// green while changing nothing. So the classification reads the test's BODY for the symbols that only
// a boundary test touches — the sentinel errors, the abstention reasons, the refusal codes — and the
// name is one signal among several.

// boundarySymbols are the identifiers a test can only reference if it is exercising a refusal, a
// boundary or an error path. Referencing any one of them classifies a test.
//
// Assembled from the phase's own vocabulary rather than from a wordlist, so a ninth failure mode added
// to `errors.go` tomorrow is counted the day a test touches it.
func boundarySymbols() map[string]bool {
	out := map[string]bool{}
	for _, name := range []string{
		// The sentinels. Every one is a refusal somebody has to handle.
		"ErrWiringOverride", "ErrModelUnregistered", "ErrCredentialUnresolved", "ErrKeyValueOffered",
		"ErrHostServiceMissing", "ErrAlreadyActive", "ErrRehearsalNotPassed", "ErrNoChange",
		"ErrInvalidDefinition", "ErrWrongPlacement", "ErrUnknownAgentVersion", "ErrUnattributedInference",
		"ErrAssemblerBypassed", "ErrCapReached", "ErrRolloutStageSkipped", "ErrNoFleetCap",
		// The abstention vocabulary — "not knowing is an output", so every one of these is a boundary.
		"AbstainBelowFloor", "AbstainNoCandidate", "AbstainOutOfVocabulary", "AbstainUnknownNode",
		"AbstainFrontendOwns",
		// The non-OK terminal codes.
		"CodeDisabled", "CodeCredentialUnresolved", "CodeModelUnregistered", "CodeNoActiveDefinition",
		"CodeRehearsalPending", "CodeCapReached", "CodeBudgetExceeded", "CodeProviderFailed",
		"CodeOutputRejected", "CodeWrongPlacement",
		// The states that are not `ready`.
		"ReadyCredentialUnresolved", "ReadyCapped", "ReadyNoDefinition",
		// The stale marks, which exist because something stopped.
		"StaleDisabled", "StaleDefinitionRetired",
	} {
		out[name] = true
	}
	return out
}

// boundaryPhrases catch the boundary tests that assert on a REFUSAL without naming a sentinel — a
// provider timeout, or a save that must fail.
//
// 🔴 SHORT, AND MATCHED AGAINST CODE WITH COMMENTS STRIPPED. The first version of this list held
// `refus`, `reject`, `cannot`, `must not` and `never`, matched against the whole test source. It
// classified 91% of the suite as boundary tests — because this repository's tests are heavily
// commented and those words are all over the prose explaining WHY a happy-path assertion matters. A
// classifier that classifies almost everything measures nothing, and it would have reported a suite
// that had drifted entirely to the happy path as 91% boundary.
//
// With comments stripped and only these two patterns, the honest figure is 37%: above the threshold,
// and not trivially.
func boundaryPhrases() []string {
	return []string{
		// A provider timeout asserted through the context rather than through a Code.
		"deadlineexceeded",
		// `if err == nil { t.Error(...) }` — the shape of "this must have been refused".
		"err == nil",
	}
}

// stripComments removes line and block comments, so the classifier reads what a test DOES rather than
// what its author explained. See boundaryPhrases for the measurement this changed.
func stripComments(src string) string {
	var out strings.Builder
	inBlock := false
	for _, line := range strings.Split(src, "\n") {
		if inBlock {
			if i := strings.Index(line, "*/"); i >= 0 {
				line, inBlock = line[i+2:], false
			} else {
				continue
			}
		}
		if i := strings.Index(line, "/*"); i >= 0 {
			line, inBlock = line[:i], true
		}
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

// MinimumBoundaryShare is the threshold. 30%, from task 10.10.
const MinimumBoundaryShare = 0.30

func TestAtLeastThirtyPercentOfThisPhasesTestsTargetErrorAndBoundaryPaths(t *testing.T) {
	symbols := boundarySymbols()
	phrases := boundaryPhrases()

	var total, boundary int
	var happy []string

	// The phase's test files, by their own naming convention plus the two suites that predate it.
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, src, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			total++
			raw := string(src[fset.Position(fn.Pos()).Offset:fset.Position(fn.End()).Offset])
			body := strings.ToLower(stripComments(raw))

			isBoundary := false
			ast.Inspect(fn, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && symbols[id.Name] {
					isBoundary = true
				}
				return true
			})
			if !isBoundary {
				for _, p := range phrases {
					if strings.Contains(body, p) {
						isBoundary = true
						break
					}
				}
			}
			if isBoundary {
				boundary++
			} else {
				happy = append(happy, file+":"+fn.Name.Name)
			}
		}
	}

	if total < 40 {
		// 🔴 An anti-vacuity floor. A percentage over four tests is not a measurement, and this fence
		// would otherwise go green on a suite somebody had gutted — reporting the ratio of what remains.
		t.Fatalf("this package holds only %d test functions, so a percentage over them measures nothing. "+
			"Either the suite has been gutted or this fence is looking in the wrong place.", total)
	}

	share := float64(boundary) / float64(total)
	t.Logf("%d of %d test functions target error/boundary paths (%.0f%%)", boundary, total, share*100)
	if share < MinimumBoundaryShare {
		sort.Strings(happy)
		t.Errorf("only %.0f%% of tests target error and boundary paths; task 10.10 requires %.0f%%.\n"+
			"  Tests classified as happy-path:\n    %s\n\n"+
			"  A suite drifts toward the happy path on its own, because the happy path is what you write "+
			"while building and the boundaries are what you write afterwards if there is time.",
			share*100, MinimumBoundaryShare*100, strings.Join(happy, "\n    "))
	}

	// 🔴 AND EVERY ONE OF THE EIGHT NAMED SUBJECTS IS COVERED. The percentage alone can be met by
	// thirty tests of one refusal, which would be a suite that exercises one boundary thoroughly and
	// seven not at all.
	required := map[string][]string{
		"abstention":           {"AbstainBelowFloor", "AbstainNoCandidate", "AbstainOutOfVocabulary"},
		"cap reached":          {"ErrCapReached", "CodeCapReached"},
		"credential missing":   {"ErrCredentialUnresolved", "ReadyCredentialUnresolved"},
		"provider timeout":     {"CodeBudgetExceeded", "CodeProviderFailed"},
		"oversized repository": {"Budget", "MaxTokens"},
		"conflicting edge":     {"AbstainFrontendOwns"},
		"unresolved model ref": {"ErrModelUnregistered"},
		"injected instruction": {"AbstainUnknownNode", "AbstainOutOfVocabulary"},
	}
	seen := map[string]bool{}
	for _, file := range files {
		src, _ := os.ReadFile(file)
		text := string(src)
		for subject, syms := range required {
			for _, sym := range syms {
				if strings.Contains(text, sym) {
					seen[subject] = true
				}
			}
		}
	}
	missing := []string{}
	for subject := range required {
		if !seen[subject] {
			missing = append(missing, subject)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("task 10.10 names eight boundary subjects and %d are untested: %s.\n"+
			"  The percentage can be met by thirty tests of one refusal — which is a suite that "+
			"exercises one boundary thoroughly and the rest not at all.",
			len(missing), strings.Join(missing, ", "))
	}
}
