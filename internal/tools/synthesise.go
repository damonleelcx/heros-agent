package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/provider"
	"github.com/heros-foreal/heros/internal/toolcontract"
	"strconv"
)

// synthesise.go answers the question the whole assessment exists for: what is weak here?
//
// # 🔴 The division of labour, which is the entire design
//
// The ORDERING, the counts, and the list of what was not measured are COMPUTED. Only the connective
// prose — the sentence that says what these findings have in common — comes from a model.
//
// That split exists because the two halves have different failure modes, and only one of them is
// survivable. A miscomputed ranking is wrong the same way every time and can be fixed. A generated
// summary is wrong by being plausible: it will happily state that an agent has no memory strategy when
// the memory axis was never assessed, in a well-formed sentence, next to eight true ones. A reader has
// no way to tell which sentence was invented.
//
// So the model is given the findings and is structurally prevented from adding to them: `validate`
// rejects a synthesis naming any axis that did not produce a finding, and the run fails rather than
// publishing it. The model may connect what is there. It may not contribute.
//
// # 🔴 What was NOT measured is part of the product
//
// An assessment's admissions are half its value. A report over eight axes that does not say the ninth
// failed is a report that reads as complete and is not — and the axis most likely to fail is the one the
// repository is strangest about, which is exactly the one worth knowing about.

// Synthesis is the assessment's conclusion.
type Synthesis struct {
	// Overall is the model's connective sentence. Constrained to the findings it was given.
	Overall string `json:"overall"`
	// Ranked is every finding, most actionable first. Computed, not generated.
	Ranked []RankedFinding `json:"ranked"`
	// Assessed and Unmeasured partition the nine axes. 🔴 Unmeasured is never omitted when empty in the
	// rendering, because "nothing was unmeasured" is itself a claim worth reading.
	Assessed   []string `json:"assessed"`
	Unmeasured []string `json:"unmeasured"`
	// ActionableCount is how many findings could become a change.
	ActionableCount int `json:"actionable_count"`
}

// RankedFinding is one finding with its place in the order.
type RankedFinding struct {
	Axis       string `json:"axis"`
	Weakness   string `json:"weakness"`
	Actionable bool   `json:"actionable"`
}

// SynthesiseAssessment joins the per-axis findings into one answer.
type SynthesiseAssessment struct {
	Provider provider.Provider
	Model    string
	// MaxTokens bounds the connective sentence. Small on purpose: the model is writing one paragraph,
	// not a report, and the report is already computed by the time it is asked.
	MaxTokens int
	Timeout   time.Duration
}

func (s SynthesiseAssessment) Spec() toolcontract.Spec {
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 90 * time.Second
	}
	return toolcontract.Spec{
		Kind:          planner.KindSynthesise,
		Permissions:   []toolcontract.Permission{toolcontract.CallModel},
		Timeout:       timeout,
		RetrySafe:     true,
		EffectBearing: false,
	}
}

const synthesiseSystem = `You are given findings from an assessment of an AI agent, one per axis.

Write ONE short paragraph, at most three sentences, saying what these findings have in common and which
would matter most to fix first.

Rules:
- Refer ONLY to axes present in the findings. Never mention an axis that is not listed.
- 🔴 Axis names are ordinary English words, so this rule catches everyday usage too. Do not name ANY
  aspect of the agent with a single-word label unless that exact word appears in the findings above.
  Describe it instead: "the surrounding information it is given" rather than a one-word noun. One stray
  word gets the whole answer rejected.
- Never invent a weakness. If two findings share a root cause, say so; if they do not, say that.
- No preamble, no restating the list, no recommendations beyond naming which to fix first.
- Reply with a JSON object only: {"overall": string}`

type synthesisWire struct {
	Overall string `json:"overall"`
}

// Execute builds the synthesis.
func (s SynthesiseAssessment) Execute(ctx context.Context, c toolcontract.Call) (toolcontract.Result, error) {
	findings, assessed := collectFindings(c.Inputs)

	// 🔴 An empty join is a failure, not an empty report. Reaching here with no findings means every
	// dependency was skipped or produced nothing, and publishing "no weaknesses found" would be the most
	// misleading possible reading of that.
	if len(findings) == 0 {
		return toolcontract.Result{}, fmt.Errorf(
			"no axis produced a finding, so there is nothing to synthesise; %d dependency result(s) were "+
				"present", len(c.Inputs))
	}

	syn := Synthesis{
		Ranked:     rankFindings(findings),
		Assessed:   assessed,
		Unmeasured: unmeasured(assessed),
	}
	for _, f := range findings {
		if f.Actionable {
			syn.ActionableCount++
		}
	}

	budget := s.MaxTokens
	if budget == 0 {
		budget = 400
	}
	temp := 0.0
	resp, err := s.Provider.Complete(ctx, provider.Request{
		Model:     s.Model,
		MaxTokens: budget,
		// No chain of thought: the model is writing one paragraph over a list it was handed.
		Reasoning:   provider.NoReasoning,
		Temperature: &temp,
		JSONObject:  true,
		Messages: []provider.Message{
			{Role: "system", Content: synthesiseSystem},
			{Role: "user", Content: renderFindings(syn)},
		},
	})
	res := toolcontract.Result{ToolCalls: 1}
	if err != nil {
		return res, err
	}
	res.Tokens = resp.Usage.Total()
	res.CostMicroCents = resp.CostMicroCents

	if resp.Truncated() {
		return res, fmt.Errorf("the synthesis was cut off at %d tokens; it is being asked for more than "+
			"a paragraph", budget)
	}
	var w synthesisWire
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &w); err != nil {
		return res, fmt.Errorf("the synthesis was not the requested JSON object: %w", err)
	}
	syn.Overall = strings.TrimSpace(w.Overall)

	// 🔴 ONE repair attempt, and the reason it has to exist.
	//
	// validate rejects a synthesis that names an axis which produced no finding. That fence is right,
	// and it stays. But the axis names are ordinary English words, and the findings themselves use them:
	// a prompt finding that says "can silently degrade context" invites a synthesis that says "context",
	// which is then rejected for commenting on an axis it was not shown.
	//
	// Observed in production on a run scoped to ONE axis: assess-prompt succeeded, synthesise failed
	// twice and the goal died. Both attempts sent byte-identical input, so the second could only fail
	// the same way — a retry that cannot succeed is not a retry, it is the same spend twice.
	//
	// So the complaint is fed back once and the model is asked to rewrite. If it fails again the fence
	// holds and the error is returned unchanged: the repair widens what can succeed, never what is
	// accepted.
	if err := validate(syn); err != nil {
		repaired, rerr := s.repair(ctx, syn, err, budget, &res)
		if rerr != nil {
			return res, err // the ORIGINAL complaint; the repair's own trouble is not the useful one
		}
		syn.Overall = repaired
		if err := validate(syn); err != nil {
			return res, err
		}
	}
	out, err := json.Marshal(syn)
	if err != nil {
		return res, err
	}
	res.Output = out
	return res, nil
}

// collectFindings decodes every dependency's findings and records which axes reported.
func collectFindings(inputs map[string][]byte) ([]RankedFinding, []string) {
	var out []RankedFinding
	seen := map[string]bool{}
	// Sorted so the prompt and the ranking are identical across runs; a synthesis that changes because a
	// map iterated differently is not comparable to the one before it.
	keys := make([]string, 0, len(inputs))
	for k := range inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		var fs []struct {
			Axis       string `json:"axis"`
			Weakness   string `json:"weakness"`
			Actionable bool   `json:"actionable"`
		}
		if len(inputs[k]) == 0 || json.Unmarshal(inputs[k], &fs) != nil {
			continue
		}
		for _, f := range fs {
			if f.Axis == "" || strings.TrimSpace(f.Weakness) == "" {
				continue
			}
			out = append(out, RankedFinding{Axis: f.Axis, Weakness: f.Weakness, Actionable: f.Actionable})
			seen[f.Axis] = true
		}
	}
	assessed := make([]string, 0, len(seen))
	for a := range seen {
		assessed = append(assessed, a)
	}
	sort.Strings(assessed)
	return out, assessed
}

// rankFindings orders actionable findings first, then by axis, deterministically.
//
// 🔴 Computed rather than asked for. "Which of these matters most" is the one judgement a reader will
// act on directly, and a generated ordering is unauditable — it cannot be checked, reproduced, or
// argued with. Actionability is a property the axis assessment already decided; this only sorts on it.
func rankFindings(fs []RankedFinding) []RankedFinding {
	out := append([]RankedFinding(nil), fs...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Actionable != out[j].Actionable {
			return out[i].Actionable
		}
		return out[i].Axis < out[j].Axis
	})
	return out
}

// unmeasured returns the axes that produced no finding.
func unmeasured(assessed []string) []string {
	have := map[string]bool{}
	for _, a := range assessed {
		have[a] = true
	}
	var out []string
	for _, a := range intent.Axes() {
		if !have[a] {
			out = append(out, a)
		}
	}
	return out
}

// renderFindings is the prompt. It lists ONLY what was found; the unmeasured axes are deliberately not
// shown, so the model cannot be tempted to comment on them.
func renderFindings(s Synthesis) string {
	var b strings.Builder
	b.WriteString("Findings:\n")
	for _, f := range s.Ranked {
		tag := "noted"
		if f.Actionable {
			tag = "actionable"
		}
		fmt.Fprintf(&b, "- %s (%s): %s\n", f.Axis, tag, f.Weakness)
	}
	b.WriteString("\nReply as JSON: {\"overall\": string}")
	return b.String()
}

// repair asks once for a rewrite that does not trip the fence, telling the model exactly what tripped
// it. It returns the new paragraph, or an error if the model could not be reached.
//
// The cost is added to the same Result, so the run's spend still reports every token this task used —
// a repair that were free to the ledger would make the ceiling a lie.
func (s SynthesiseAssessment) repair(ctx context.Context, syn Synthesis, complaint error, budget int,
	res *toolcontract.Result) (string, error) {

	temp := 0.0
	resp, err := s.Provider.Complete(ctx, provider.Request{
		Model:       s.Model,
		MaxTokens:   budget,
		Reasoning:   provider.NoReasoning,
		Temperature: &temp,
		JSONObject:  true,
		Messages: []provider.Message{
			{Role: "system", Content: synthesiseSystem},
			{Role: "user", Content: renderFindings(syn)},
			{Role: "assistant", Content: `{"overall": ` + strconv.Quote(syn.Overall) + `}`},
			{Role: "user", Content: "That was rejected: " + complaint.Error() +
				".\n\nRewrite the paragraph so it says the same thing without using that word at all. " +
				"Keep to the findings you were given. Reply as JSON: {\"overall\": string}"},
		},
	})
	res.ToolCalls++
	if err != nil {
		return "", err
	}
	res.Tokens += resp.Usage.Total()
	res.CostMicroCents += resp.CostMicroCents
	if resp.Truncated() {
		return "", fmt.Errorf("the rewritten synthesis was cut off at %d tokens", budget)
	}
	var w synthesisWire
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &w); err != nil {
		return "", fmt.Errorf("the rewritten synthesis was not the requested JSON object: %w", err)
	}
	return strings.TrimSpace(w.Overall), nil
}

// validate refuses a synthesis that talks about an axis nobody assessed.
//
// # 🔴 This is the fence that makes the model's contribution safe
//
// The model may connect the findings it was given. It may not add one. A sentence naming an axis that
// produced no finding is either an invention or a comment on an absence it was not shown — and both
// read identically to a person, sitting in a paragraph where every other clause is true.
//
// Refusing costs one retry. Publishing costs a customer acting on a weakness that was never observed in
// their code.
func validate(s Synthesis) error {
	if s.Overall == "" {
		return fmt.Errorf("the synthesis is empty")
	}
	assessed := map[string]bool{}
	for _, a := range s.Assessed {
		assessed[a] = true
	}
	lower := strings.ToLower(s.Overall)
	for _, axis := range intent.Axes() {
		if assessed[axis] {
			continue
		}
		// Word-boundary, for the reason "remember" contains "member": a substring match here would reject
		// valid syntheses and be very hard to explain.
		if mentionsWord(lower, axis) {
			return fmt.Errorf("the synthesis mentions the %q axis, which produced no finding — it is "+
				"commenting on something it was not shown", axis)
		}
	}
	return nil
}

// mentionsWord reports whether a lowercased text contains a word on boundaries.
func mentionsWord(text, word string) bool {
	for i := 0; i+len(word) <= len(text); i++ {
		if text[i:i+len(word)] != word {
			continue
		}
		beforeOK := i == 0 || !isWordByte(text[i-1])
		afterOK := i+len(word) == len(text) || !isWordByte(text[i+len(word)])
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}
