package evalharness

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/heros-foreal/agentd/internal/confighash"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

// JudgeConfig is everything about a judge invocation that must be reproducible. It is hashed and
// recorded on every judge score, so a stored number can always be traced back to the exact model,
// sampling settings and rubric that produced it. Same discipline as the P3.5 fallback config: a
// hand-maintained version is a version that eventually lies, so PromptVersion is DERIVED.
type JudgeConfig struct {
	Model         string  `json:"model"`
	Seed          int64   `json:"seed"`
	Temperature   float64 `json:"temperature"`
	PromptVersion string  `json:"prompt_version"`
	// ScaleMax is the top of the rubric scale the judge answers on (e.g. 5 for a 1-5 rubric). The
	// score is divided by it to land in [0,1]; it is explicit because a judge asked for 1-5 and read
	// as 0-1 silently reports every answer as a pass.
	ScaleMax float64 `json:"scale_max"`
}

// ConfigHash is the content-defined identifier a judge score's reproducibility record is keyed by.
func (c JudgeConfig) ConfigHash() (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return confighash.SumBytes(b)
}

// JudgeStanding is a judge metric's calibration standing — the answer to "should I believe this
// number?". It rides on EVERY value the judge produces (MetricValue.Judge), because a judge score
// reported without its agreement is decoration presented as measurement.
type JudgeStanding struct {
	// Metric is the judge metric this standing describes.
	Metric string `json:"metric"`
	// Agreement is Cohen's kappa against the human-labeled subset, in [-1,1]. Meaningful only when
	// Calibrated is true.
	Agreement float64 `json:"agreement"`
	// PercentAgreement is raw % agreement, reported alongside kappa because kappa alone is
	// unreadable when the label distribution is skewed.
	PercentAgreement float64 `json:"percent_agreement"`
	// NHuman is how many human labels the agreement was computed over. An agreement of 0.9 over
	// n=3 is not evidence, and hiding n is how it gets treated as if it were.
	NHuman int `json:"n_human"`
	// Floor is the configured agreement floor for gate eligibility.
	Floor float64 `json:"floor"`
	// Calibrated is true only when a calibration subset was actually supplied and scored.
	Calibrated bool `json:"calibrated"`
}

// GateEligible reports whether this judge may be used as a hard-constraint gate input. An
// uncalibrated judge, or one below the floor, is never eligible — this is the single predicate the
// gate layer consults, so there is no second place the rule could be relaxed.
func (s JudgeStanding) GateEligible() bool {
	return s.Calibrated && s.NHuman > 0 && s.Agreement >= s.Floor
}

// Reason explains a refusal in words a UI can render verbatim.
func (s JudgeStanding) Reason() string {
	switch {
	case !s.Calibrated || s.NHuman == 0:
		return fmt.Sprintf("judge metric %q is uncalibrated (no human-labeled subset)", s.Metric)
	case s.Agreement < s.Floor:
		return fmt.Sprintf("judge metric %q agreement %.3f is below the floor %.3f (n_human=%d)",
			s.Metric, s.Agreement, s.Floor, s.NHuman)
	default:
		return ""
	}
}

// JudgeEvaluator is an Evaluator that carries a calibration standing. Compute attaches the standing
// to every value such an evaluator produces; the gate layer refuses any gate whose input reports
// GateEligible() == false.
type JudgeEvaluator interface {
	Evaluator
	Standing() JudgeStanding
}

// JudgeRequest is one case presented to the judge model.
type JudgeRequest struct {
	// Prompt is the fully-rendered rubric instruction (rubric + input + output + optional reference).
	Prompt string
	// Schema is the JSON Schema the response must satisfy, passed so a provider supporting
	// structured output constrains generation rather than merely being asked nicely.
	Schema json.RawMessage
	Config JudgeConfig
}

// RawVerdict is what the model returns BEFORE validation. Score is a pointer so a MISSING score is
// distinguishable from a score of zero: a model that answers without a score has not met the
// contract and must not be read as "confidently the worst possible answer".
type RawVerdict struct {
	Score     *float64 `json:"score"`
	Rationale string   `json:"rationale"`
}

// JudgeModel is the provider seam. It is an interface so the harness has zero I/O in tests and so
// the judge's spend flows through the same metered gateway every other provider call does.
type JudgeModel interface {
	Judge(ctx context.Context, req JudgeRequest) (RawVerdict, error)
}

// judgeResponseSchema constrains the judge to a numeric score plus a rationale.
var judgeResponseSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["score", "rationale"],
  "properties": {
    "score": {"type": "number"},
    "rationale": {"type": "string"}
  }
}`)

// LLMJudge is the rubric-driven judge evaluator. It is constructed with an explicit standing, which
// is what makes "this judge is uncalibrated" a value the type system carries rather than a fact
// someone must remember to look up.
type LLMJudge struct {
	name     string
	metric   string
	model    JudgeModel
	cfg      JudgeConfig
	patterns []patternclassifier.Pattern

	// standing is guarded because calibration can REPLACE it while the judge is already registered
	// and scoring. That is the intended lifecycle: a judge ships uncalibrated (informing soft terms
	// only), a human-labeled subset arrives, Calibrate measures the agreement, and from that moment
	// every score the judge produces carries the new standing.
	mu       sync.RWMutex
	standing JudgeStanding
}

// NewLLMJudge builds a judge. standing is supplied by the caller (from the calibration store); an
// uncalibrated judge is representable on purpose — it can still inform a soft weighted term with its
// standing shown, it simply cannot gate.
func NewLLMJudge(name, metric string, model JudgeModel, cfg JudgeConfig, standing JudgeStanding, patterns ...patternclassifier.Pattern) (*LLMJudge, error) {
	if model == nil {
		return nil, fmt.Errorf("%w: judge %q has no model", ErrInvalidEvaluator, name)
	}
	if cfg.ScaleMax <= 0 {
		return nil, fmt.Errorf("%w: judge %q must declare a positive scale_max", ErrInvalidEvaluator, name)
	}
	if standing.Metric == "" {
		standing.Metric = metric
	}
	return &LLMJudge{name: name, metric: metric, model: model, cfg: cfg, standing: standing, patterns: patterns}, nil
}

func (j *LLMJudge) Name() string                                    { return j.name }
func (j *LLMJudge) Metric() string                                  { return j.metric }
func (j *LLMJudge) Range() Range                                    { return UnitRange() }
func (j *LLMJudge) AdmissiblePatterns() []patternclassifier.Pattern { return j.patterns }

// Standing returns the judge's current calibration standing.
func (j *LLMJudge) Standing() JudgeStanding {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.standing
}

// SetStanding installs a measured standing (task 3.3). It is the only way a judge becomes
// calibrated: there is no constructor argument that asserts calibration without evidence, because
// the standing must always be something Calibrate MEASURED.
func (j *LLMJudge) SetStanding(st JudgeStanding) {
	if st.Metric == "" {
		st.Metric = j.metric
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.standing = st
}

// Evaluate renders the rubric prompt, asks the model for a structured verdict, and normalizes the
// score onto [0,1]. A missing or out-of-scale score is an ERROR, never a defaulted value.
func (j *LLMJudge) Evaluate(ctx context.Context, tr Trace, c Case, tgt Target) (float64, error) {
	if strings.TrimSpace(c.Rubric) == "" {
		return 0, fmt.Errorf("%w: case %q has no rubric for judge %q", ErrNotApplicable, c.CaseID, j.name)
	}
	out, ok := tr.OutputFor(tgt)
	if !ok {
		return 0, nil // no output is a measured failure the judge does not need to be paid to confirm
	}
	cfg := j.cfg
	prompt := RenderJudgePrompt(c, out, cfg.ScaleMax)
	cfg.PromptVersion = promptVersion(prompt)
	v, err := j.model.Judge(ctx, JudgeRequest{Prompt: prompt, Schema: judgeResponseSchema, Config: cfg})
	if err != nil {
		return 0, fmt.Errorf("judge %q: %w", j.name, err)
	}
	if v.Score == nil {
		return 0, fmt.Errorf("judge %q returned no score for case %q", j.name, c.CaseID)
	}
	norm := *v.Score / cfg.ScaleMax
	if norm < 0 || norm > 1 {
		// Surfaced as out-of-range rather than clamped: a judge answering 7 on a 1-5 rubric is not
		// "a perfect score", it is a broken contract, and clamping would hide that forever.
		return 0, fmt.Errorf("%w: judge %q returned %v on a 0-%v scale", ErrOutOfRange, j.name, *v.Score, cfg.ScaleMax)
	}
	return norm, nil
}

// RenderJudgePrompt builds the judge instruction. It is exported so the prompt can be content-hashed
// and stored as a blob (never inlined in a log, DevOps task 6.5) and so a reviewer can read exactly
// what the judge was asked.
func RenderJudgePrompt(c Case, output json.RawMessage, scaleMax float64) string {
	var b strings.Builder
	b.WriteString("You are grading one output of an automated workflow against a rubric.\n\n")
	b.WriteString("RUBRIC:\n")
	b.WriteString(c.Rubric)
	b.WriteString("\n\nINPUT:\n")
	b.Write(c.Input)
	if len(c.Reference) > 0 {
		b.WriteString("\n\nREFERENCE ANSWER (label: ")
		b.WriteString(string(c.Label))
		b.WriteString("):\n")
		b.Write(c.Reference)
	}
	b.WriteString("\n\nOUTPUT UNDER TEST:\n")
	b.Write(output)
	fmt.Fprintf(&b, "\n\nReturn a JSON object with a numeric \"score\" between 0 and %v and a short "+
		"\"rationale\". Do not return any other field.\n", scaleMax)
	return b.String()
}

// promptVersion derives a stable version id from the prompt text itself.
func promptVersion(prompt string) string {
	h, err := confighash.SumBytes([]byte(strconv.Quote(prompt)))
	if err != nil {
		return ""
	}
	return h[:16]
}
