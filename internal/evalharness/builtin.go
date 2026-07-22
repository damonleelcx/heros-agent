package evalharness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/heros-foreal/agentd/internal/confighash"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// nowFunc is the package clock, indirected so the tests are not wall-clock flaky.
var nowFunc = time.Now

// Built-in evaluator names. They are the registry keys and the eval_result.evaluator_name values, so
// they are constants rather than literals typed at three call sites.
const (
	EvaluatorExactMatch = "exact_match"
	EvaluatorJSONSchema = "json_schema_validity"
	EvaluatorRegex      = "regex"
	EvaluatorLLMJudge   = "llm_judge"
)

// Builtins returns the deterministic built-in evaluators. The LLM judge is deliberately absent: it
// requires a model and a calibration record, and a judge that registers itself with neither is the
// "uncalibrated judge silently gating" failure Decision 3 exists to prevent.
func Builtins() []Evaluator {
	return []Evaluator{
		NewExactMatch(),
		NewJSONSchemaValidity(),
		NewRegex(),
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// exact-match
// ─────────────────────────────────────────────────────────────────────────────

// ExactMatch scores 1 when the output equals the case's reference and 0 otherwise. Equality is
// CANONICAL-JSON equality (the same canonicalizer config_hash uses), not byte equality: two objects
// differing only in key order are the same answer, and calling them different would report a
// formatting difference as a quality regression.
type ExactMatch struct{}

// NewExactMatch builds the exact-match oracle.
func NewExactMatch() *ExactMatch { return &ExactMatch{} }

func (e *ExactMatch) Name() string                                    { return EvaluatorExactMatch }
func (e *ExactMatch) Metric() string                                  { return MetricExactMatch }
func (e *ExactMatch) Range() Range                                    { return UnitRange() }
func (e *ExactMatch) AdmissiblePatterns() []patternclassifier.Pattern { return nil }

func (e *ExactMatch) Evaluate(_ context.Context, tr Trace, c Case, tgt Target) (float64, error) {
	if len(c.Reference) == 0 {
		return 0, fmt.Errorf("%w: case %q has no reference for exact-match", ErrNotApplicable, c.CaseID)
	}
	out, ok := tr.OutputFor(tgt)
	if !ok {
		return 0, nil // the run produced nothing: that is a measured failure, not a skip
	}
	gotCanon, err := confighash.CanonicalizeBytes(out)
	if err != nil {
		return 0, nil // unparseable output cannot equal a reference
	}
	wantCanon, err := confighash.CanonicalizeBytes(c.Reference)
	if err != nil {
		return 0, fmt.Errorf("%w: case %q reference is not canonicalizable: %v", ErrNotApplicable, c.CaseID, err)
	}
	if bytes.Equal(gotCanon, wantCanon) {
		return 1, nil
	}
	return 0, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON-schema validity
// ─────────────────────────────────────────────────────────────────────────────

// JSONSchemaValidity scores 1 when the output validates against the case's output schema. It is a
// GOLD oracle: schema validity is decidable, so a case scored this way needs no human review.
type JSONSchemaValidity struct{}

// NewJSONSchemaValidity builds the schema-validity oracle.
func NewJSONSchemaValidity() *JSONSchemaValidity { return &JSONSchemaValidity{} }

func (e *JSONSchemaValidity) Name() string                                    { return EvaluatorJSONSchema }
func (e *JSONSchemaValidity) Metric() string                                  { return MetricSchemaValid }
func (e *JSONSchemaValidity) Range() Range                                    { return UnitRange() }
func (e *JSONSchemaValidity) AdmissiblePatterns() []patternclassifier.Pattern { return nil }

func (e *JSONSchemaValidity) Evaluate(_ context.Context, tr Trace, c Case, tgt Target) (float64, error) {
	if len(c.OutputSchema) == 0 {
		return 0, fmt.Errorf("%w: case %q declares no output schema", ErrNotApplicable, c.CaseID)
	}
	sch, err := CompileSchema(c.OutputSchema)
	if err != nil {
		// An unusable schema is the CASE's defect, not the variant's. Scoring 0 would blame the
		// variant for a broken eval set.
		return 0, fmt.Errorf("%w: case %q output_schema: %v", ErrNotApplicable, c.CaseID, err)
	}
	out, ok := tr.OutputFor(tgt)
	if !ok {
		return 0, nil
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(out))
	if err != nil {
		return 0, nil
	}
	if err := sch.Validate(doc); err != nil {
		return 0, nil
	}
	return 1, nil
}

// CompileSchema compiles a self-contained JSON Schema. Remote $ref is refused for the same reason a
// skill contract refuses it: a schema that reaches out over the network is not reproducible, and an
// eval set whose oracle changes when a remote document changes is not an eval set.
func CompileSchema(raw json.RawMessage) (*jsonschema.Schema, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("schema must not be empty")
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("schema is not valid JSON: %v", err)
	}
	c := jsonschema.NewCompiler()
	c.UseLoader(offlineLoader{})
	const loc = "heros:eval-case-contract"
	if err := c.AddResource(loc, doc); err != nil {
		return nil, err
	}
	return c.Compile(loc)
}

type offlineLoader struct{}

func (offlineLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("remote $ref %q is not allowed: an eval-set oracle must be self-contained "+
		"so its content hash addresses the whole contract", url)
}

// ─────────────────────────────────────────────────────────────────────────────
// regex
// ─────────────────────────────────────────────────────────────────────────────

// Regex scores 1 when the case's pattern matches the output. The output is matched as its RAW bytes
// (a JSON string's own quoting included) because the pattern is authored against what the workflow
// actually emits.
type Regex struct{}

// NewRegex builds the regex oracle.
func NewRegex() *Regex { return &Regex{} }

func (e *Regex) Name() string                                    { return EvaluatorRegex }
func (e *Regex) Metric() string                                  { return MetricRegexMatch }
func (e *Regex) Range() Range                                    { return UnitRange() }
func (e *Regex) AdmissiblePatterns() []patternclassifier.Pattern { return nil }

func (e *Regex) Evaluate(_ context.Context, tr Trace, c Case, tgt Target) (float64, error) {
	if c.Pattern == "" {
		return 0, fmt.Errorf("%w: case %q declares no regex pattern", ErrNotApplicable, c.CaseID)
	}
	re, err := regexp.Compile(c.Pattern)
	if err != nil {
		return 0, fmt.Errorf("%w: case %q pattern %q does not compile: %v", ErrNotApplicable, c.CaseID, c.Pattern, err)
	}
	out, ok := tr.OutputFor(tgt)
	if !ok {
		return 0, nil
	}
	if re.Match(out) {
		return 1, nil
	}
	return 0, nil
}
