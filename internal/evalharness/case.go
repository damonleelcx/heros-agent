package evalharness

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ReferenceLabel records how much a case's reference output can be trusted. It is carried on the
// case (not inferred at scoring time) because "how did we get this expected answer?" is a fact about
// provenance that cannot be recovered later.
type ReferenceLabel string

const (
	// LabelGold is an oracle-derived or human-reviewed reference: exact-match, JSON-schema, a
	// deterministic tool result, or a human sign-off. Gold references may drive gates.
	LabelGold ReferenceLabel = "gold"
	// LabelWeak is an LLM-generated, unreviewed reference. A weak reference is allowed to inform a
	// score, but never to silently drive one — every consumer must surface it as weak.
	LabelWeak ReferenceLabel = "weak"
	// LabelNone is a case with no reference at all: it is scored by reference-free metrics only.
	LabelNone ReferenceLabel = "none"
)

// Valid reports whether l is one of the three defined labels. An unknown label is rejected at
// construction rather than defaulted to weak — defaulting would let a typo quietly downgrade or
// upgrade a reference's trust.
func (l ReferenceLabel) Valid() bool {
	return l == LabelGold || l == LabelWeak || l == LabelNone
}

// EdgeCaseKind is the fixed failure-taxonomy slot a case exercises. The vocabulary is closed so the
// coverage report can state "tool-returns-nothing: not covered" rather than counting free-text tags
// that never line up between two generators.
type EdgeCaseKind string

const (
	EdgeCaseNone            EdgeCaseKind = ""
	EdgeCaseEmptyInput      EdgeCaseKind = "empty_input"
	EdgeCaseMalformedInput  EdgeCaseKind = "malformed_input"
	EdgeCaseToolNoResult    EdgeCaseKind = "tool_returns_nothing"
	EdgeCaseRetrievalMiss   EdgeCaseKind = "retrieval_miss"
	EdgeCaseContextOverflow EdgeCaseKind = "context_overflow"
	EdgeCaseAdversarial     EdgeCaseKind = "adversarial_injection"
	EdgeCaseBoundary        EdgeCaseKind = "boundary_value"
)

// EdgeCaseKinds is the closed taxonomy, in report order. The coverage measurer walks exactly this
// slice, so adding a kind here is what adds it to every report — there is no second list.
var EdgeCaseKinds = []EdgeCaseKind{
	EdgeCaseEmptyInput,
	EdgeCaseMalformedInput,
	EdgeCaseToolNoResult,
	EdgeCaseRetrievalMiss,
	EdgeCaseContextOverflow,
	EdgeCaseAdversarial,
	EdgeCaseBoundary,
}

// ValidEdgeCaseKind reports whether k is in the closed taxonomy (EdgeCaseNone included).
func ValidEdgeCaseKind(k EdgeCaseKind) bool {
	if k == EdgeCaseNone {
		return true
	}
	for _, known := range EdgeCaseKinds {
		if k == known {
			return true
		}
	}
	return false
}

// Origin records which generator layer produced a case, so a coverage report can say WHERE the set
// came from and a reviewer can tell a hand-authored case from a fuzzed one.
type Origin string

const (
	OriginHandAuthored Origin = "hand_authored"
	OriginSeedTrace    Origin = "seed_trace"
	OriginSchema       Origin = "schema"
	OriginLLM          Origin = "llm"
	OriginAdversarial  Origin = "adversarial"
)

// Case is one item of an eval set: an input to run the workflow on, an optional reference output to
// score against, and the tags that make coverage measurable.
type Case struct {
	CaseID     string `json:"case_id"`
	WorkflowID string `json:"workflow_id"`
	Suite      string `json:"suite"`

	// Input is the workflow input this case drives. Stored by content hash in Postgres; carried
	// inline here because an evaluator needs the bytes.
	Input json.RawMessage `json:"input"`
	// Reference is the expected output, if one exists. nil means reference-free.
	Reference json.RawMessage `json:"reference,omitempty"`
	// Label states how much Reference can be trusted. A case with a non-nil Reference and
	// LabelNone is invalid: it would let an unattributed reference drive a score.
	Label ReferenceLabel `json:"label"`

	// OutputSchema is the contract the JSON-schema evaluator validates the output against.
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	// Pattern is the regex the regex evaluator matches the output against.
	Pattern string `json:"pattern,omitempty"`
	// Rubric is the judging instruction the LLM-judge evaluator renders into its prompt.
	Rubric string `json:"rubric,omitempty"`
	// SuccessOracle names the evaluator that decides task_success for this case. Empty means "use
	// the default precedence" (see SuccessOracleFor). It is explicit because a case carrying both a
	// reference and a rubric is genuinely ambiguous, and guessing which one the author meant is how
	// a cheap deterministic oracle silently replaces the judge the author paid for.
	SuccessOracle string `json:"success_oracle,omitempty"`

	// PathTags are the IR coordinates this case is EXPECTED to exercise (edge ids, branch outcomes,
	// loop-iteration bounds). They are a claim, not evidence: coverage is measured from the observed
	// trace, and PathTags only tell the generator what it was aiming at.
	PathTags []string `json:"path_tags,omitempty"`
	// EdgeCase is the failure-taxonomy slot this case occupies.
	EdgeCase EdgeCaseKind `json:"edge_case,omitempty"`
	// Origin is the generator layer that produced the case.
	Origin Origin `json:"origin"`

	// Difficulty is the measured baseline-model failure rate for this case in [0,1]: 0 = every
	// baseline run passes (trivial), 1 = none does. Zero value means "not yet measured".
	Difficulty float64 `json:"difficulty"`
}

// Validate rejects a structurally dishonest case. It is called at eval-set construction so a bad
// case can never reach the scorer, where its dishonesty would be indistinguishable from a result.
func (c Case) Validate() error {
	var problems []string
	if strings.TrimSpace(c.CaseID) == "" {
		problems = append(problems, "case_id must not be empty")
	}
	if strings.TrimSpace(c.WorkflowID) == "" {
		problems = append(problems, "workflow_id must not be empty")
	}
	if len(c.Input) == 0 {
		problems = append(problems, "input must not be empty")
	} else if !json.Valid(c.Input) {
		problems = append(problems, "input is not valid JSON")
	}
	if len(c.Reference) > 0 && !json.Valid(c.Reference) {
		problems = append(problems, "reference is not valid JSON")
	}
	if !c.Label.Valid() {
		problems = append(problems, fmt.Sprintf("label %q is not one of gold|weak|none", c.Label))
	}
	// The label describes the case's ORACLE, and an oracle is a reference, a decidable output
	// schema, or a regex — not only a reference. Requiring a reference specifically was a real
	// defect: it forced the schema-driven generator to stuff the SCHEMA into the reference field to
	// get a gold label, and exact-match then compared every output against a JSON Schema document
	// and scored zero for every variant. The rule below is the fix, and it is the reason
	// HasOracle exists as one predicate rather than three checks at three call sites.
	if c.HasOracle() && c.Label == LabelNone {
		problems = append(problems, "a case carrying an oracle (reference, output_schema or pattern) must be labeled gold or weak")
	}
	if !c.HasOracle() && c.Label != LabelNone {
		problems = append(problems, "a case with no oracle must be labeled none")
	}
	if !ValidEdgeCaseKind(c.EdgeCase) {
		problems = append(problems, fmt.Sprintf("edge_case %q is not in the taxonomy", c.EdgeCase))
	}
	if c.Difficulty < 0 || c.Difficulty > 1 {
		problems = append(problems, "difficulty must be in [0,1]")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("evalharness: case %q is invalid: %s", c.CaseID, strings.Join(problems, "; "))
	}
	return nil
}
