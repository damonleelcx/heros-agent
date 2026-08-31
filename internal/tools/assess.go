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

// AssessAxis asks a model to find one weakness on one axis.
type AssessAxis struct {
	Provider provider.Provider
	Model    string
	Source   AxisSource
	// MaxTokens bounds the response. Required by the provider; named here so a caller sees the bound.
	MaxTokens int
	Timeout   time.Duration
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
- Reply with a JSON object only, no prose around it.`

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

	temp := 0.0
	req := provider.Request{
		Model:       a.Model,
		MaxTokens:   a.MaxTokens,
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
		return res, fmt.Errorf("the model's reply was cut off at the %d-token limit; raise MaxTokens "+
			"for the %s axis", a.MaxTokens, axis)
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
// 🔴 It exists because subject-repository discovery is NOT BUILT. It is named for what it is rather than
// called something like `DefaultSource`, so that no reader mistakes the current state for a finished
// one, and so a search for "fixture" finds every place the system is still pretending.
type FixtureSource struct{ Excerpts map[string]string }

func (f FixtureSource) Excerpt(axis string) (string, bool) {
	e, ok := f.Excerpts[axis]
	return e, ok
}
