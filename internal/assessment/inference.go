package assessment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/providercall"
)

// inference.go is the AI-engineer stage: HEROS answering ONE axis, on the residue only, with an
// abstention that is a first-class success rather than a failure.
//
// # 🔴 The three properties this file exists to hold
//
//  1. **It sees only the residue.** `Question` has no field for a whole repository and no field for a
//     source file. It carries the IR facts for one axis and the frontends that produced them. A caller
//     cannot ask for a whole-repository pass because there is nowhere to put the request — the same
//     shape `herosagent.Residue` uses, for the same reason.
//
//  2. **It abstains rather than guessing.** FR10: *"an inference that cannot reach a conclusion returns
//     `not_measured` with a named missing input; it does not return a low-confidence conclusion."* The
//     confidence floor is enforced HERE, after the model has answered, so a confident-sounding sentence
//     below the floor becomes an abstention rather than a finding. §3.5 then counts that abstention as
//     a SUCCESS — which is the only thing that makes FR10 real, because a discipline the evaluation
//     punishes erodes.
//
//  3. **It records what produced the answer.** The provider model version and the content address are
//     required by the `Inferred`/`Abstained` constructors, so there is no path through this file that
//     produces an attributed-by-nothing finding.
//
// # Why the address is content-addressed rather than a row id
//
// Design D7 and task 3.2. An address derived from the INPUT means two runs that saw the same thing
// produce the same address, and a re-inference that produces a different answer for the SAME address is
// visibly a change in the model rather than a change in the repository. A row id would make every
// re-run a different address and the diff meaningless.

// ErrNoModel is returned when an inference is asked for and no provider seam is wired.
var ErrNoModel = errors.New("assessment: no model is wired for inference")

// DefaultConfidenceFloor is the confidence below which an answer becomes an abstention.
//
// # Why 0.7 and why a floor at all
//
// `herosagent.DefaultConfidenceFloor` sets the same boundary for edge inference and this matches it
// deliberately: two floors in one product means two different definitions of "confident enough" and a
// reader has no way to know which one a given claim passed. It is re-declared rather than imported so
// that a future divergence is a visible edit rather than an inherited surprise, and
// `TestTheFloorMatchesTheAgentsFloor` fails if they drift apart without one.
const DefaultConfidenceFloor = 0.7

// Question is everything a per-axis inference is allowed to see.
//
// 🚫 THERE IS NO FIELD FOR SOURCE TEXT. §7.4 keeps source on the platform's side of the boundary and
// out of this phase's storage; a `Question` with nowhere to put a source line cannot put one in a
// prompt that is then logged, cached by a provider, or echoed into a claim.
type Question struct {
	Axis           Axis   `json:"axis"`
	WorkflowID     string `json:"workflow_id"`
	SourceRevision string `json:"source_revision"`
	// Language is the workflow's language, so a model can say "this is a Python idiom" rather than
	// guessing from identifier shapes.
	Language string `json:"language"`
	// Frontends is discovery's record of WHICH analysers produced this IR and how deeply they analyse.
	//
	// 🔴 Carried into the question deliberately, and it is the single most useful thing an analyser can
	// be told about why a gap exists: "the python frontend is syntactic and cannot follow a value
	// across a statement" turns "why is this field empty" from a guess into a fact. `herosagent
	// .Residue` carries the same field for the same reason.
	Frontends []discovery.FrontendRun `json:"frontends"`
	// Facts are the per-node IR facts relevant to THIS axis, and nothing else. The axis decides what
	// is in here; see `factsFor`.
	Facts []NodeFact `json:"facts"`
	// StructuralGap is the missing input the structural pass named. It tells the model what it is
	// being asked to resolve rather than making it infer the question from the data.
	StructuralGap MissingInput `json:"structural_gap"`
}

// NodeFact is one call site, reduced to what one axis needs.
type NodeFact struct {
	NodeID string `json:"node_id"`
	// File and Line locate the call site. A LOCATION, never its contents: a reader needs to be able to
	// open the place a claim is about, and that needs a path and a number, not a snippet.
	File string `json:"file"`
	Line int    `json:"line"`
	// Fields are the axis-relevant IR values, already reduced to strings. `unresolved` appears here
	// verbatim when discovery emitted its sentinel, because "we could not read this" is the fact.
	Fields map[string]string `json:"fields"`
}

// Answer is what a model returns.
type Answer struct {
	// Claim is the sentence. Empty when abstaining.
	Claim string
	// Confidence is the model's own confidence in the claim, 0..1.
	Confidence float64
	// Abstained is the model DECLINING. 🔴 A first-class output, not an error: §3.5 counts it as a
	// success, and a model that could only fail or answer would answer.
	Abstained bool
	// AbstentionReason is why, in the model's words. Carried into the finding's claim so the reader
	// gets "we could not determine X because Y" rather than a bare state.
	AbstentionReason string
	// ProviderModelVersion is the EXACT model that produced this — design D7. 🔴 Required: without it
	// a provider's routine upgrade renders as the customer's repository getting worse.
	ProviderModelVersion string
	// CostUSD is what this call cost. The runner adds it to the assessment's spend before checking the
	// cap for the next axis.
	CostUSD float64
	Usage   providercall.Usage
}

// Analyst is the provider seam for a per-axis inference.
//
// Named `Analyst` rather than `Model` because `AxisModel` is already this package's constant for the
// `model` AXIS, and a type that shares a name with a value in the same package is a name that reads
// wrong at every use site. `Model` alone would collide with the same idea one level up.
//
// 🚫 There is deliberately NO default implementation and no stub in this package. `herosagent
// .newRunner` refuses a nil model with the reason, and it is the same reason here: *"a stub returning
// plausible edges is indistinguishable from a working agent"*. A deployment with no model wires no
// inference at all, which is rollout stage 1 and reports honestly.
type Analyst interface {
	Assess(ctx context.Context, q Question) (Answer, error)
}

// HerosInference implements `Inference` over an Analyst.
type HerosInference struct {
	model Analyst
	floor float64
}

// NewHerosInference wires the inference stage.
func NewHerosInference(m Analyst, floor float64) (*HerosInference, error) {
	if m == nil {
		return nil, ErrNoModel
	}
	if floor <= 0 || floor > 1 {
		// A zero floor accepts everything, and a floor nobody set is a floor that is zero. `herosagent`
		// refuses the same value for the same reason.
		return nil, fmt.Errorf("assessment: the confidence floor is %v; a zero floor accepts every "+
			"answer, which is FR10 switched off", floor)
	}
	return &HerosInference{model: m, floor: floor}, nil
}

// Infer answers one axis.
func (h *HerosInference) Infer(ctx context.Context, axis Axis, s Subject) (Finding, float64, error) {
	q, err := questionFor(axis, s)
	if err != nil {
		return Finding{}, 0, err
	}
	address, err := ContentAddress(q)
	if err != nil {
		return Finding{}, 0, err
	}

	ans, err := h.model.Assess(ctx, q)
	if err != nil {
		return Finding{}, 0, fmt.Errorf("assessment: asking about %s: %w", axis, err)
	}
	if strings.TrimSpace(ans.ProviderModelVersion) == "" {
		// 🔴 Refused rather than defaulted. A finding attributed to no model version makes a provider
		// upgrade indistinguishable from the repository changing, which is exactly the confusion D7
		// exists to prevent — and a placeholder here would make the field LOOK recorded.
		return Finding{}, ans.CostUSD, fmt.Errorf("assessment: the model answered about %s and named no "+
			"version, so the answer could not be attributed to what produced it", axis)
	}

	// ── The floor (FR10, task 3.3) ───────────────────────────────────────────────────────────────
	//
	// Two ways to abstain and they collapse to one outcome: the model declined, or it answered below
	// the floor. The SECOND is the one this check exists for — a low-confidence conclusion reads
	// exactly like a confident one once it is a sentence on a screen, and the number that would have
	// warned the reader is not rendered anywhere.
	if ans.Abstained || ans.Confidence < h.floor {
		reason := strings.TrimSpace(ans.AbstentionReason)
		if reason == "" {
			reason = fmt.Sprintf("the analysis reached %.2f confidence, below the %.2f floor this "+
				"build requires before stating a claim", ans.Confidence, h.floor)
		}
		f, ferr := Abstained(axis,
			"this could not be determined from the source: "+reason,
			s.Evidence(), ans.ProviderModelVersion, address)
		return f, ans.CostUSD, ferr
	}

	claim := strings.TrimSpace(ans.Claim)
	if claim == "" {
		// A conclusion above the floor with nothing in it. Treated as an abstention rather than
		// refused, because the reader's position is identical either way and an error here would lose
		// the eight other axes.
		f, ferr := Abstained(axis,
			"the analysis returned no statement about this surface", s.Evidence(),
			ans.ProviderModelVersion, address)
		return f, ans.CostUSD, ferr
	}
	f, ferr := Inferred(axis, claim, s.Evidence(), ans.ProviderModelVersion, address)
	return f, ans.CostUSD, ferr
}

// ContentAddress is the pin's address: a hash over the QUESTION.
//
// 🔴 Over the question and not over the answer. Two runs that saw the same thing share an address, so
// a re-inference producing a different claim AT THE SAME ADDRESS is visibly a change in the model
// rather than a change in the repository — which is what makes task 3.2's "explicit re-inference
// renders as a diff" a diff of one thing rather than of two unrelated things.
//
// It is stable across processes and across Go versions because it hashes canonical JSON with sorted
// keys rather than a Go value: `map` iteration order and struct layout are not a hashing input anybody
// should depend on.
func ContentAddress(q Question) (string, error) {
	b, err := canonicalJSON(q)
	if err != nil {
		return "", fmt.Errorf("assessment: addressing the question for %s: %w", q.Axis, err)
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func canonicalJSON(v any) ([]byte, error) {
	// `encoding/json` already sorts map keys, and every field here is either a scalar, a slice in a
	// deterministic order, or such a map. The sort below is what makes the SLICES deterministic; the
	// marshaller handles the rest.
	return json.Marshal(v)
}

// questionFor reduces a Subject to what one axis needs.
//
// 🔴 Per axis, not one blob. An inference handed every field of every node is an inference whose cost
// is proportional to the repository rather than to the gap (design D2's consequence), and it is also an
// inference that can answer about an axis it was not asked about — which the runner then refuses,
// wasting the call.
func questionFor(axis Axis, s Subject) (Question, error) {
	if s.IR == nil {
		return Question{}, fmt.Errorf("assessment: %s cannot be inferred with no IR", axis)
	}
	q := Question{
		Axis:           axis,
		WorkflowID:     s.WorkflowID,
		SourceRevision: s.IR.Workflow.Repo.CommitSHA,
		Language:       s.IR.Workflow.Language,
		Frontends:      append([]discovery.FrontendRun{}, s.Report.Frontends...),
		Facts:          factsFor(axis, s.IR),
	}
	sort.Slice(q.Frontends, func(i, j int) bool { return q.Frontends[i].Language < q.Frontends[j].Language })
	return q, nil
}

// factsFor selects the IR fields one axis needs.
//
// The switch is exhaustive over the nine axes and has NO default arm that returns everything. A
// default here would be the "one blob" this function exists to avoid, arriving quietly the first time
// somebody adds a tenth axis.
func factsFor(axis Axis, ir *discovery.IR) []NodeFact {
	out := make([]NodeFact, 0, len(ir.Nodes))
	for _, n := range ir.Nodes {
		f := NodeFact{NodeID: n.NodeID, File: n.CallSite.File, Line: n.CallSite.LineStart, Fields: map[string]string{}}
		switch axis {
		case AxisModel:
			f.Fields["provider"] = n.Model.Provider
			f.Fields["model_id"] = n.Model.ModelID
		case AxisPrompt:
			// The prompt's SHAPE, never its text (§7.4). Whether it is resolved, and how many variables
			// it interpolates, is what an inference about prompt STRATEGY needs; the words are not.
			f.Fields["resolved"] = fmt.Sprint(n.Prompt.Inline != discovery.UnresolvedSentinel)
			f.Fields["variables"] = strings.Join(n.Prompt.Variables, ",")
		case AxisSkills:
			f.Fields["skills"] = strings.Join(n.Skills, ",")
		case AxisContext:
			f.Fields["policy"] = n.ContextAssembly.Policy
			f.Fields["description"] = n.ContextAssembly.Description
		case AxisTools:
			names := make([]string, 0, len(n.Tools))
			locatable := 0
			for _, t := range n.Tools {
				names = append(names, t.Name)
				if t.Locatable() {
					locatable++
				}
			}
			sort.Strings(names)
			f.Fields["tools"] = strings.Join(names, ",")
			f.Fields["statically_declared"] = fmt.Sprint(locatable)
		case AxisMemory:
			// 🔴 The floor is passed through AS THE FLOOR, labelled. The model must not be handed
			// `memory: none` as though it were an observation — that is the inversion design D6 names,
			// and a model told "none" will happily conclude "this repository has no memory strategy".
			f.Fields["ir_floor"] = n.MemoryDefault()
			f.Fields["ir_floor_means"] = "discovery emits this for every node; it is the absence of " +
				"evidence, not evidence of absence"
			f.Fields["symbol"] = n.CallSite.Symbol
		case AxisHarness:
			f.Fields["ir_floor"] = n.HarnessDefault()
			f.Fields["ir_floor_means"] = "discovery emits this for every node; it is the absence of " +
				"evidence, not evidence of absence"
			f.Fields["invocation"] = n.InvocationSemantics.Type
			f.Fields["symbol"] = n.CallSite.Symbol
		case AxisLoop, AxisGraph:
			// Unreachable: both are `refused` before the residue is computed, so the runner never asks.
			// Returning nothing rather than panicking keeps a future caller's mistake a poor question
			// rather than a crashed assessment.
			return nil
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}
