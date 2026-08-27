package assessment

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/herosagent"
)

// p36_noun_dictionary_test.go is P36 task 10.3: the nine axes are named IDENTICALLY on the operator
// console, the customer console, the CLI and the docs.
//
// # 🔴 Why this fence lives here
//
// `internal/assessment` is the only package that can see both vocabularies without a cycle. It already
// owns the nine the customer console and the CLI are generated from (`Axes()` → the `AssessmentAxis`
// union in `internal/api/consoletypes.go`), and it may import `internal/herosagent`, which owns the
// nine an operator authors.
//
// # What goes wrong without it
//
// Nothing, loudly. Two vocabularies that drift produce a console calling something `wiring` and a report
// calling it `graph`, and the customer concludes they are two features. The failure is a support
// conversation months later, and by then both names are in screenshots.
//
// P36 is exactly when it can happen: this phase RENAMES `wiring` to `graph` on the operator side and
// adds `loop` to a list that previously had seven members.

// nineAxes is what the assessment vocabulary declares — the source the customer console and the CLI are
// generated from.
func nineAxes(t *testing.T) []string {
	t.Helper()
	out := make([]string, 0, len(Axes()))
	for _, a := range Axes() {
		out = append(out, string(a))
	}
	sort.Strings(out)
	if len(out) != 9 {
		t.Fatalf("the assessment vocabulary declares %d axes (%v), and the product's is nine", len(out), out)
	}
	return out
}

func TestTheNineAxesAreNamedIdenticallyOnEverySurface(t *testing.T) {
	shared := nineAxes(t)

	// ── the OPERATOR side ────────────────────────────────────────────────────────────────────────
	operator := make([]string, 0, 9)
	for _, a := range herosagent.AuthorableAxes() {
		operator = append(operator, string(a))
	}
	sort.Strings(operator)
	if strings.Join(operator, ",") != strings.Join(shared, ",") {
		t.Errorf("the operator vocabulary and the assessment vocabulary disagree.\n  operator:   %v\n"+
			"  assessment: %v\n\n  A console calling something one thing and a report calling it another "+
			"tells a customer they are two features, and the failure is a support conversation months "+
			"later — by which time both names are in screenshots.", operator, shared)
	}

	// ── the DOCS ─────────────────────────────────────────────────────────────────────────────────
	//
	// 🔴 Read from the sales claims doc's noun-dictionary table, because that is the document somebody
	// writes a deck from. A dictionary nobody reads cannot drift; this one is read.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "docs", "sales", "P36-agent-self-configuration-claims.md"))
	if err != nil {
		t.Fatalf("the P36 claims document could not be read (%v). Task 10.3's dictionary lives there, "+
			"and this fence is measuring nothing without it.", err)
	}
	doc := string(b)
	section := doc[strings.Index(doc, "## 5. Noun dictionary"):]
	if section == "" {
		t.Fatal("the claims document has no noun-dictionary section")
	}
	rowAxis := regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|")
	documented := []string{}
	for _, m := range rowAxis.FindAllStringSubmatch(section, -1) {
		documented = append(documented, m[1])
	}
	sort.Strings(documented)
	if strings.Join(documented, ",") != strings.Join(shared, ",") {
		t.Errorf("the documented dictionary and the code disagree.\n  documented: %v\n  code:       %v",
			documented, shared)
	}

	// 🔴 `wiring` is RETIRED and the document says so. A rename that is only in the code is a rename
	// somebody undoes the next time they read an old deck.
	if !strings.Contains(section, "`wiring` is retired") {
		t.Error("the noun dictionary does not record that `wiring` is retired. It was this platform's " +
			"word for the topology axis while that axis was vacuous, and P36 renamed it to the product's " +
			"own word — a rename recorded only in code is one that gets undone from an old deck.")
	}
	for _, axis := range shared {
		if !strings.Contains(section, "`"+axis+"`") {
			t.Errorf("the dictionary does not define %q", axis)
		}
	}
}

// 🔴 THE REFUSED CLAIM IS WRITTEN DOWN, WITH ITS REASON (task 10.2).
//
// "Do not say it optimizes itself" is worth nothing as an instruction. What makes it hold under sales
// pressure is the REASON, stated in a form somebody can repeat — and the reason is more persuasive than
// the feature would have been, which is the whole argument for saying it first.
func TestTheClaimsDocumentRefusesSelfOptimizationAndSaysWhy(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "docs", "sales", "P36-agent-self-configuration-claims.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)

	for _, want := range []string{
		// The refusal itself.
		"Do not say the agent optimizes itself",
		// The reason, in the words that make it repeatable.
		"evaluator grading itself",
		"whatever gates it is running on the",
		// 🔴 And WHY saying it first is the stronger move — without this the refusal reads as an
		// apology for a missing feature rather than as the argument it is.
		"the credibility we spend defending self-optimization",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the claims document does not carry %q.\n\n  A refusal with no reason is an "+
				"instruction, and an instruction does not survive a scoping call. The reason is what "+
				"makes it repeatable — and it is more persuasive than the feature would have been.", want)
		}
	}

	// The sayable claim is there too, in full, or the document is only a list of prohibitions.
	if !strings.Contains(doc, "same nine axes we expose to you") ||
		!strings.Contains(doc, "rehearsed and version-pinned before activation") {
		t.Error("the claims document does not carry task 10.1's sayable claim in full")
	}
}

// 🔴 P36 §11 — THE SIGN-OFF IS IN THE PRD, WITH TASK 1.1'S FINDING AND D5 RE-CONFIRMED.
//
// # Why a test guards a document
//
// Task 11.2 says task 1.1's finding must be *reviewed before the phase is scheduled*, because it decides
// whether this is an additive change or a migration of every pinned inference. A finding that lives only
// in a commit message is one nobody reads at scheduling time.
//
// Task 11.3 says D5 must be re-confirmed *at the end of the phase, when a self-optimizing agent looks
// like the obvious next step* — which is exactly when a decision recorded only at the start gets read as
// having been made without knowing what the phase would produce.
func TestThePRDCarriesTheSignOffAndTheFindingThatSizedThePhase(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "docs", "prd", "P36-agent-self-configuration.md"))
	if err != nil {
		t.Fatalf("the P36 PRD could not be read: %v", err)
	}
	prd := string(b)

	// 11.1 — every question ANSWERED, in the PRD itself rather than only in decisions.md. A reader of
	// the PRD must not have to open a second document to learn what was decided.
	if !strings.Contains(prd, "Open questions — ANSWERED") {
		t.Error("the PRD still presents §14 as open. Task 11.1 is that the answers are FOLDED IN.")
	}
	for _, q := range []string{"**Q1**", "**Q2**", "**Q3**", "**Q4**", "**Q5**"} {
		if !strings.Contains(prd, q) {
			t.Errorf("the PRD's answered table does not carry %s", q)
		}
	}

	// 11.2 — the finding that decided the size of the phase, stated as a finding rather than referenced.
	for _, want := range []string{
		"nested `nodes` array cannot preserve",
		"ADDITIVE",
		"no pin is migrated and no `spec_json` row is",
		// 🔴 And that the evidence was recorded BEFORE the change. A fixture reconstructed afterwards
		// asserts only that the new code is a function of its input, which was never in doubt — and that
		// distinction is the entire value of the evidence.
		"pre-P36 tree",
	} {
		if !strings.Contains(prd, want) {
			t.Errorf("the PRD's sign-off does not carry %q. Task 11.2: this finding decides whether the "+
				"phase is additive or a migration of every pin, and one that lives only in a commit "+
				"message is one nobody reads at scheduling time.", want)
		}
	}

	// 11.3 — D5 re-confirmed AT THE END, and the re-confirmation says what would have to be overturned.
	for _, want := range []string{
		"Re-confirmed on",
		"obvious next step",
		"evaluator grading its own configuration",
		// The most important line: what changed in the phase, and why none of it weakens the argument.
		"Nothing built in this phase weakens the argument",
	} {
		if !strings.Contains(prd, want) {
			t.Errorf("the PRD's sign-off does not carry %q. Task 11.3 asks for D5 re-confirmed at the "+
				"END of the phase — a decision recorded only at the start reads as having been made "+
				"without knowing what the phase would produce.", want)
		}
	}
}
