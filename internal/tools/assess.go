// Package tools implements the tool contracts a worker invokes, backed by a real model.
//
// # Why the prompt lives in code rather than in a registry, for now
//
// It will move. It is here so that the FIRST real model call in this system is readable beside the
// parsing that consumes it — a prompt that asks for one shape and a parser that expects another is the
// most common failure in this kind of code, and separating them at birth hides it.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/provider"
	"github.com/heros-foreal/heros/internal/toolcontract"
)

// AxisSource is the material an assessment reads for one axis: what the subject repository actually
// does at that axis, already extracted.
//
// 🔴 An interface, because subject-repository discovery does not exist yet. The seam is drawn HERE so
// that when it does, the model-facing half of assessment does not change — and so that the honest
// current state (fixtures) is visible as a named implementation rather than hidden inside a tool.
type AxisSource interface {
	// Excerpt returns what the repository does at this axis, or false when it cannot be determined.
	// 🔴 The boolean matters: "we could not read this" is a finding, and inventing an excerpt to avoid
	// returning false would make the model assess a repository that does not exist.
	Excerpt(axis string) (string, bool)
}

// axisBudgets is the output ceiling for each axis's reply, in tokens.
//
// # 🔴 What these numbers are, and what actually fixed the truncation
//
// They are MEASURED, not guessed: with reasoning disabled, a reply to this prompt is the fixed
// three-field JSON object and runs 50-120 output tokens regardless of how much code went in. The
// budgets below are roughly 4x that, which leaves room for an unusually wordy weakness without leaving
// room for an essay.
//
// The truncation these replaced was NOT a budgeting problem. The provider enables chain-of-thought by
// default at `high` effort, reasoning tokens are billed as output and consume MaxTokens, and on a real
// repository they were 82% of the output — 243 of 296 tokens on a measured call, leaving 53 for the
// answer. Raising the ceiling would have bought more thinking, not more answer. Turning thinking off cut
// output 5.3x, and these budgets sit comfortably above what remains.
//
// 🔴 So the table is a bound, not a prediction. Where an axis genuinely needs more, Execute escalates
// per attempt rather than failing — see below — because a static number will eventually be wrong about
// somebody's repository and nobody will be watching when it is.
var axisBudgets = map[string]int{
	// The axes whose evidence tends to be densest, so their weaknesses run longest.
	"context": 500,
	"loop":    500,
	"prompt":  500,
	"harness": 500,
	// Everything else answers more briefly.
	"model": 400, "skills": 400, "tools": 400, "memory": 400, "graph": 400,
}

// defaultAxisBudget covers an axis not in the table, which should not happen but must not crash.
const defaultAxisBudget = 500

// maxBudgetEscalations bounds the doubling below, so an axis that truncates for some other reason
// cannot walk its budget up indefinitely across a long retry ladder.
const maxBudgetEscalations = 2

// AssessAxis asks a model to find one weakness on one axis.
type AssessAxis struct {
	Provider provider.Provider
	Model    string
	Source   AxisSource
	// MaxTokens overrides the per-axis budget when non-zero. Left unset in production; a test that wants
	// to force truncation sets it small.
	MaxTokens int
	Timeout   time.Duration
}

// budgetFor returns the output ceiling for one axis on one attempt.
//
// # 🔴 Why the budget grows with the attempt
//
// Because a retry that repeats the identical request is not a retry — it is the same failure, purchased
// twice. The retry ladder already tracks which attempt this is; using it to change something is what
// makes the second attempt worth its cost.
//
// Doubling is bounded: two escalations, then the ladder fails the task with the truncation message,
// which names the axis and says the budget was raised and still ran out. That is a different report from
// "it truncated", and it is the one that tells somebody the prompt is wrong rather than the number.
func (a AssessAxis) budgetFor(axis string, attempt int) int {
	base := a.MaxTokens
	if base == 0 {
		var ok bool
		if base, ok = axisBudgets[axis]; !ok {
			base = defaultAxisBudget
		}
	}
	escalations := attempt - 1
	if escalations < 0 {
		escalations = 0
	}
	if escalations > maxBudgetEscalations {
		escalations = maxBudgetEscalations
	}
	return base << escalations
}

func (a AssessAxis) Spec() toolcontract.Spec {
	timeout := a.Timeout
	if timeout == 0 {
		timeout = 90 * time.Second
	}
	return toolcontract.Spec{
		Kind:        planner.KindAssessAxis,
		Permissions: []toolcontract.Permission{toolcontract.ReadSource, toolcontract.CallModel},
		Timeout:     timeout,
		// Retry-safe: reading and judging changes nothing outside the platform.
		RetrySafe:     true,
		EffectBearing: false,
	}
}

const assessSystem = `You assess one axis of an AI agent's implementation for weaknesses that a developer could act on.

Rules:
- Judge ONLY the code excerpt given. Never speculate about code you were not shown.
- "actionable" means a specific change could be made to this excerpt. A true observation that implies no
  change is actionable=false.
- If the excerpt shows no weakness on this axis, say so with actionable=false rather than inventing one.
- Reply with a JSON object only, no prose around it.
- Keep "weakness" to at most two sentences. You are naming one problem, not writing a report.`

// findingWire is the shape the model is asked for. Kept beside the prompt that requests it.
type findingWire struct {
	Axis       string `json:"axis"`
	Weakness   string `json:"weakness"`
	Actionable bool   `json:"actionable"`
}

// Execute assesses one axis.
func (a AssessAxis) Execute(ctx context.Context, c toolcontract.Call) (toolcontract.Result, error) {
	axis := strings.TrimPrefix(c.TaskID, "assess-")
	excerpt, ok := a.Source.Excerpt(axis)
	if !ok {
		// 🔴 An axis whose source cannot be read FAILS rather than returning an empty finding. An empty
		// finding is indistinguishable from "we looked and it is fine", and the difference between those
		// two is most of what an assessment is for.
		return toolcontract.Result{}, fmt.Errorf(
			"the %s axis could not be read from this repository, so there is nothing to assess", axis)
	}

	budget := a.budgetFor(axis, c.Attempt)
	temp := 0.0
	req := provider.Request{
		Model:     a.Model,
		MaxTokens: budget,
		// 🔴 No chain of thought. This call fills a fixed three-field shape from text it was handed; it
		// is extraction, not problem-solving. With thinking on at the provider's default `high` effort,
		// reasoning tokens are billed as output and consume MaxTokens before the JSON begins — which is
		// exactly why real repositories truncated where fixtures did not.
		//
		// Turning it off also makes `temperature` effective again, which is what makes a second run of
		// the same assessment comparable to the first.
		Reasoning:   provider.NoReasoning,
		Temperature: &temp,
		JSONObject:  true,
		Messages: []provider.Message{
			{Role: "system", Content: assessSystem},
			{Role: "user", Content: fmt.Sprintf(
				"Axis: %s\n\nWhat the agent does here:\n%s\n\nReply as JSON: "+
					`{"axis":string,"weakness":string,"actionable":boolean}`, axis, excerpt)},
		},
	}
	resp, err := a.Provider.Complete(ctx, req)
	if err != nil {
		return toolcontract.Result{}, err
	}

	// Usage is recorded even when parsing fails below: the call happened and it cost money, and a
	// ceiling that only counts successful parses is one a malformed-output loop walks straight through.
	res := toolcontract.Result{
		Tokens:         resp.Usage.Total(),
		CostMicroCents: resp.CostMicroCents,
		ToolCalls:      1,
	}

	// 🔴 A truncated response is a DIFFERENT failure from a malformed one, and saying so is the
	// difference between "raise MaxTokens" and "the model is misbehaving". Reported before parsing,
	// because a response cut mid-object always fails to parse and the parse error would mask the cause.
	if resp.Truncated() {
		// The message distinguishes "the budget was too small" from "the budget was raised and it still
		// ran out", because those need different fixes: a bigger number, versus a prompt that is asking
		// for the wrong thing.
		if c.Attempt > maxBudgetEscalations {
			return res, fmt.Errorf(
				"the %s axis truncated at %d tokens after %d escalations; the reply is not getting "+
					"shorter, so the prompt is asking for more than a finding", axis, budget, c.Attempt-1)
		}
		return res, fmt.Errorf("the %s axis truncated at %d tokens (%d of them reasoning); the next "+
			"attempt gets %d", axis, budget, resp.Usage.ReasoningTokens, a.budgetFor(axis, c.Attempt+1))
	}

	var w findingWire
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &w); err != nil {
		return res, fmt.Errorf("the model's reply was not the requested JSON object: %w", err)
	}
	if strings.TrimSpace(w.Weakness) == "" {
		return res, fmt.Errorf("the model returned a finding with no weakness described")
	}
	// The model is asked for the axis and is not trusted with it: the task knows which axis it is, and a
	// mislabelled finding would be filed against the wrong row of a nine-row report.
	w.Axis = axis

	out, err := json.Marshal([]findingWire{w})
	if err != nil {
		return res, err
	}
	res.Output = out
	return res, nil
}

// FixtureSource is an AxisSource backed by literal excerpts.
//
// 🔴 It existed because subject-repository discovery was not built. Discovery now exists —
// `discovery.Corpus` satisfies AxisSource directly — so this is TEST SCAFFOLDING, kept because a unit
// test of the model-facing half should not need a repository on disk. It is named for what it is so
// that a search for "fixture" still finds every place the system is pretending.
type FixtureSource struct{ Excerpts map[string]string }

func (f FixtureSource) Excerpt(axis string) (string, bool) {
	e, ok := f.Excerpts[axis]
	return e, ok
}
