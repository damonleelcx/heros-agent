package patternclassifier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/confighash"
)

// FallbackConfig is everything about a fallback run that must be reproducible. It is hashed into a
// config_hash and recorded verbatim on every run, so a stored llm-sourced label can always be traced
// back to the exact model, sampling settings, prompt, and vocabulary that produced it.
type FallbackConfig struct {
	Model       string  `json:"model"`
	Seed        int64   `json:"seed"`
	Temperature float64 `json:"temperature"`
	// PromptVersion is DERIVED from the prompt text (see buildPrompt), never hand-maintained. A
	// hand-set version is a version that eventually lies: someone edits the prompt and forgets to
	// bump it, and every stored label then points at a prompt that no longer exists.
	PromptVersion   string `json:"prompt_version"`
	TaxonomyVersion string `json:"taxonomy_version"`
}

// ConfigHash is the content-defined identifier the reproducibility records are keyed by.
func (c FallbackConfig) ConfigHash() (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return confighash.SumBytes(b)
}

// LLMRun is the reproducibility record for one fallback invocation (task 4.5). One per classified
// residue subgraph, keyed by config_hash.
type LLMRun struct {
	RunRef      string         `json:"llm_run_ref"`
	ConfigHash  string         `json:"config_hash"`
	SubgraphRef string         `json:"subgraph_ref"`
	Config      FallbackConfig `json:"config"`
}

// RawLabel is what the model returns, BEFORE any validation: the pattern as a plain string, so an
// out-of-taxonomy or free-text answer is representable and can be explicitly rejected rather than
// failing to parse into something that looks legitimate.
type RawLabel struct {
	Pattern string `json:"pattern"`
	// Pointer so a MISSING confidence is distinguishable from 0.0. A model that answers without a
	// confidence has not met the contract, and must not be read as "confidently zero".
	Confidence *float64 `json:"confidence"`
}

// FallbackRequest is one ambiguous subgraph presented to the model.
type FallbackRequest struct {
	SubgraphID string
	// NodeIDs are the region's members.
	NodeIDs []string
	// Prompt is the fully-rendered, taxonomy-enumerated instruction. The model is asked to SELECT
	// from the enumeration; it is not asked to name a pattern freely.
	Prompt string
	// Schema is the JSON Schema the response must satisfy — a closed enum over the taxonomy. Passed
	// so a provider supporting structured output can constrain generation rather than merely being
	// asked nicely.
	Schema json.RawMessage
	Config FallbackConfig
}

// FallbackModel is the seam the constrained LLM-as-classifier is invoked through.
//
// It is an INTERFACE with no default implementation on purpose. There is no built-in stub that
// returns plausible labels: a stub wired in by default is how a classifier comes to look like it is
// working while classifying nothing, and the resulting labels would be indistinguishable from real
// ones. A caller that wants a fallback must supply a real model; a caller that supplies none simply
// gets no fallback, and the residue stays honestly unclassified.
type FallbackModel interface {
	ClassifySubgraph(ctx context.Context, req FallbackRequest) ([]RawLabel, error)
}

// promptTemplate is the instruction. It is a constant so that PromptVersion, derived from the
// rendered text, changes if and only if the instruction changes.
const promptTemplate = `You are classifying one region of a static workflow graph against a FIXED taxonomy.

Select zero or more patterns from the enumerated list below. You MUST NOT invent a pattern name,
return free text, or answer with anything outside the enumeration. If no pattern in the list applies,
return an empty list — an empty answer is correct and expected, and is strictly better than a guess.

For each pattern you select, return a confidence in [0,1] reflecting how strongly the region's
STRUCTURE supports it. You are given topology only: no execution traces exist yet, so you cannot
know how many times a loop iterates or whether a human ever approves anything.

THE TAXONOMY (taxonomy_version %s) — these are the ONLY permitted values of "pattern":
%s

REGION UNDER CLASSIFICATION
%s

Respond with JSON matching the provided schema.`

// buildPrompt renders the instruction for one subgraph and returns it with its derived version.
//
// The version is the SHA of the rendered prompt (minus the per-region section, which varies by
// input). Deriving it means an edit to the wording automatically invalidates every label claiming
// the old version, instead of silently reinterpreting them — the same discipline as pinning a probe
// set by its content hash.
func buildPrompt(g *graph, sg Subgraph) (prompt, version string) {
	var enum strings.Builder
	for _, i := range Patterns() { // canonical ordinal order → stable prompt → stable version
		fmt.Fprintf(&enum, "  %2d. %s (%s, %s): %s\n", i.Ordinal, i.Pattern, i.Group, i.Detection, i.Title)
	}

	var region strings.Builder
	for _, id := range sg.NodeIDs {
		n := g.nodes[id]
		if n == nil {
			continue
		}
		fmt.Fprintf(&region, "  node %s: model=%s context_policy=%s invocation=%s tools=%v\n",
			id, modelKey(n), n.ContextAssembly.Policy, n.InvocationSemantics.Type, n.ToolsSkills)
		fmt.Fprintf(&region, "    data_in=%v data_out=%v control_in=%v control_out=%v\n",
			g.dataIn[id], g.dataOut[id], g.controlIn[id], g.controlOut[id])
	}

	// The version covers the instruction and the enumeration — the parts that are the same for every
	// region — so it identifies the CLASSIFIER, not the input.
	stable := fmt.Sprintf(promptTemplate, TaxonomyVersion, enum.String(), "<region>")
	sum := sha256.Sum256([]byte(stable))
	version = "p35-classify-" + hex.EncodeToString(sum[:])[:12]
	prompt = fmt.Sprintf(promptTemplate, TaxonomyVersion, enum.String(), region.String())
	return prompt, version
}

// responseSchema is the structured-output contract: an array of {pattern, confidence} where pattern
// is a CLOSED ENUM over the taxonomy and confidence is required. Generated from the taxonomy rather
// than written out, so the schema cannot drift from the vocabulary it is meant to close.
func responseSchema() json.RawMessage {
	values := make([]string, 0, TaxonomySize)
	for _, i := range Patterns() {
		values = append(values, string(i.Pattern))
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"labels": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"required":             []string{"pattern", "confidence"},
					"additionalProperties": false,
					"properties": map[string]any{
						"pattern":    map[string]any{"enum": values},
						"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
					},
				},
			},
		},
		"required":             []string{"labels"},
		"additionalProperties": false,
	}
	b, _ := json.Marshal(schema)
	return b
}

// runFallback classifies the ambiguous residue and returns validated labels plus their
// reproducibility records.
//
// Every guarantee this layer owes is enforced HERE, not assumed of the model:
//   - it is called only with the residue (rules-first precedence, structurally guaranteed);
//   - it refuses a subgraph a rule already labeled, even if one were somehow passed in;
//   - any out-of-taxonomy, free-text, missing-confidence, or out-of-range answer is rejected,
//     dropped, and diagnosed.
//
// A schema constrains a cooperative provider; it does not constrain an uncooperative one, and it
// does not constrain a provider whose structured-output support silently degrades. So the schema is
// the request and this validation is the guarantee.
func runFallback(
	ctx context.Context, model FallbackModel, cfg FallbackConfig, g *graph,
	residue []Subgraph, ruleLabeled map[string]bool, diags *diagSink,
) (labels []Label, runs []LLMRun, calls int, err error) {
	if model == nil {
		return nil, nil, 0, nil
	}
	if cfg.Temperature != 0 {
		// Recorded, not blocked: the caller may have a reason. But a non-zero temperature means two
		// runs of the same IR can differ, and that must be visible rather than discovered later.
		diags.add(Diagnostic{Stage: StageLLMFallback, Source: SourceLLM,
			Reason: fmt.Sprintf("fallback temperature is %v, not 0: llm-sourced labels are NOT reproducible run-to-run", cfg.Temperature)})
	}
	schema := responseSchema()

	for _, sg := range residue {
		if ruleLabeled[sg.SubgraphID] {
			// Rules-first precedence, belt and braces. The residue is uncovered by construction, so
			// this cannot normally trigger — which is exactly why it is worth asserting: if it ever
			// does, a rule label was about to be overridden.
			diags.add(Diagnostic{Stage: StageLLMFallback, SubgraphRef: sg.SubgraphID, Source: SourceLLM,
				Reason: "refused: this subgraph already carries a rule label and the LLM must not override it"})
			continue
		}
		prompt, promptVersion := buildPrompt(g, sg)
		runCfg := cfg
		runCfg.PromptVersion = promptVersion
		runCfg.TaxonomyVersion = TaxonomyVersion
		hash, herr := runCfg.ConfigHash()
		if herr != nil {
			return nil, nil, calls, fmt.Errorf("patternclassifier: hashing fallback config: %w", herr)
		}
		runRef := "llmrun_" + hash[:12] + "_" + sg.SubgraphID

		calls++
		raw, cerr := model.ClassifySubgraph(ctx, FallbackRequest{
			SubgraphID: sg.SubgraphID, NodeIDs: sg.NodeIDs, Prompt: prompt, Schema: schema, Config: runCfg,
		})
		if cerr != nil {
			// A model error leaves the subgraph unclassified — which is a legitimate, visible state
			// ("not yet classified"), not a reason to fabricate a label or to fail the whole run.
			diags.add(Diagnostic{Stage: StageLLMFallback, SubgraphRef: sg.SubgraphID, Source: SourceLLM,
				Reason: fmt.Sprintf("fallback model error, subgraph left unclassified: %v", cerr)})
			continue
		}
		runs = append(runs, LLMRun{RunRef: runRef, ConfigHash: hash, SubgraphRef: sg.SubgraphID, Config: runCfg})

		for _, r := range raw {
			if r.Confidence == nil {
				diags.add(Diagnostic{Stage: StageLLMFallback, SubgraphRef: sg.SubgraphID, RawPattern: r.Pattern,
					Source: SourceLLM, Reason: "rejected: no confidence returned"})
				continue
			}
			l := Label{
				Pattern: Pattern(r.Pattern), Confidence: *r.Confidence, Source: SourceLLM,
				SubgraphRef: sg.SubgraphID, LLMRunRef: runRef, TaxonomyVersion: TaxonomyVersion,
				// A behavioral pattern named by the model is still a candidate: the model saw the same
				// topology the detectors did, and topology cannot confirm behavior whoever reads it.
				Candidate: IsBehavioral(Pattern(r.Pattern)),
			}
			if IsBehavioral(l.Pattern) && l.Confidence > BehavioralCandidateCap {
				// Clamp rather than reject: the model's SELECTION may well be right, but its
				// certainty about a runtime fact it cannot observe is not. Recorded, not silent.
				diags.add(Diagnostic{Stage: StageLLMFallback, SubgraphRef: sg.SubgraphID, RawPattern: r.Pattern,
					Source: SourceLLM,
					Reason: fmt.Sprintf("confidence %.2f clamped to the structural-candidate cap %.2f: behavioral confirmation is P5",
						l.Confidence, BehavioralCandidateCap)})
				l.Confidence = BehavioralCandidateCap
			}
			if verr := l.Validate(); verr != nil {
				// THE constraint enforcement point: free text, an invented pattern, a confidence
				// outside [0,1] — all rejected and dropped here, with the offending value kept
				// verbatim in the diagnostic.
				diags.add(Diagnostic{Stage: StageLLMFallback, SubgraphRef: sg.SubgraphID, RawPattern: r.Pattern,
					Source: SourceLLM, Reason: "rejected and dropped: " + verr.Error()})
				continue
			}
			labels = append(labels, l)
		}
	}
	return labels, runs, calls, nil
}
