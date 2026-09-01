package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/heros-foreal/heros/internal/edit"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/provider"
	"github.com/heros-foreal/heros/internal/toolcontract"
)

// propose.go holds ONE implementation of "rewrite a span of the customer's source", used by both paths
// that need it: the in-turn Tier-C intents, and the propose_change task inside an improvement run.
//
// # 🔴 Why one implementation and not two
//
// They look like different features — one is a conversation answering in two seconds, the other is a
// task in a durable graph — but they are the same act with the same blast radius, and the safety rules
// are the whole substance. Two copies means the rules are enforced twice and will eventually be
// enforced differently, and the copy that drifts is whichever one somebody edits under time pressure.
//
// So the shared core is here and neither caller reimplements it. What differs between them is only how
// the result is delivered: a diff on a screen, or a task result another task reads.

const proposeSystemPrompt = `You rewrite ONE span of a developer's source code to fix a specific weakness.

Rules:
- Return the replacement for the given span ONLY. Not the whole file, not an explanation.
- Preserve the EXACT leading whitespace of the first line. Indentation is block structure.
- Change as little as possible. A smaller diff is a better one.
- If the span cannot be improved without seeing more code, say so with "can_change": false.
- Reply with a JSON object only: {"can_change": boolean, "replacement": string, "rationale": string}`

type proposeReply struct {
	CanChange   bool   `json:"can_change"`
	Replacement string `json:"replacement"`
	Rationale   string `json:"rationale"`
}

// ProposeSpanChange asks a model to rewrite one span and validates the answer against the file on disk.
//
// 🔴 The model's output is never trusted as an edit. It is a candidate replacement string; `Validate`
// decides whether it can be applied and refuses ambiguity, re-indentation and no-ops. The model
// suggests; `internal/edit` decides.
func ProposeSpanChange(
	ctx context.Context, p provider.Provider, model string, src SpanSource, root, axis, instruction string,
) (edit.Proposal, provider.Response, error) {
	var zero edit.Proposal

	span, ok := src.TopSpan(axis)
	if !ok {
		return zero, provider.Response{}, fmt.Errorf(
			"there is no %s code in this repository to change", axis)
	}
	ref := fmt.Sprintf("%s:%d", span.Path, span.Line)

	temp := 0.0
	resp, err := p.Complete(ctx, provider.Request{
		Model: model, MaxTokens: 600, Reasoning: provider.NoReasoning, Temperature: &temp,
		JSONObject: true,
		Messages: []provider.Message{
			{Role: "system", Content: proposeSystemPrompt},
			{Role: "user", Content: fmt.Sprintf(
				"Axis: %s\nFile: %s\nWhat to change: %s\n\nSpan to rewrite:\n%s\n\n"+
					"Reply as JSON: {\"can_change\":boolean,\"replacement\":string,\"rationale\":string}",
				axis, ref, instruction, span.Text)},
		},
	})
	if err != nil {
		return zero, resp, err
	}
	if resp.Truncated() {
		return zero, resp, fmt.Errorf(
			"the proposed change was cut off; the span at %s is too large to rewrite in one step", ref)
	}
	var w proposeReply
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &w); err != nil {
		return zero, resp, fmt.Errorf("the model did not return a usable change: %w", err)
	}
	if !w.CanChange || strings.TrimSpace(w.Replacement) == "" {
		reason := w.Rationale
		if reason == "" {
			reason = "the model could not improve this span from what it was shown"
		}
		return zero, resp, fmt.Errorf("no change proposed for %s at %s: %s", axis, ref, reason)
	}

	prop := edit.Proposal{
		Path: span.Path, Line: span.Line, Axis: axis,
		Before: span.Text, After: strings.TrimRight(w.Replacement, "\n"), Rationale: w.Rationale,
	}
	// 🔴 Validated against the file on disk BEFORE anybody is shown a diff or a downstream task reads it.
	// Producing a change that cannot be applied wastes a person's decision, and they do not find out
	// until they approve it.
	if err := prop.Validate(root); err != nil {
		return zero, resp, fmt.Errorf("the proposed change cannot be applied safely: %w", err)
	}
	return prop, resp, nil
}

// ── the worker tools ─────────────────────────────────────────────────────────────────────────────

// ProposeChange is the improvement run's proposal task.
type ProposeChange struct {
	Provider provider.Provider
	Model    string
	Source   SpanSource
	Root     string
	Timeout  time.Duration
}

func (p ProposeChange) Spec() toolcontract.Spec {
	t := p.Timeout
	if t == 0 {
		t = 90 * time.Second
	}
	return toolcontract.Spec{
		Kind:        planner.KindProposeChange,
		Permissions: []toolcontract.Permission{toolcontract.ReadSource, toolcontract.CallModel},
		Timeout:     t, RetrySafe: true, EffectBearing: false,
	}
}

// Execute reads the finding this proposal answers, and rewrites the span it names.
func (p ProposeChange) Execute(ctx context.Context, c toolcontract.Call) (toolcontract.Result, error) {
	axis, instruction, err := findingFor(c)
	if err != nil {
		return toolcontract.Result{}, err
	}
	prop, resp, err := ProposeSpanChange(ctx, p.Provider, p.Model, p.Source, p.Root, axis, instruction)
	res := toolcontract.Result{ToolCalls: 1, Tokens: resp.Usage.Total(), CostMicroCents: resp.CostMicroCents}
	if err != nil {
		return res, err
	}
	b, err := json.Marshal(prop)
	if err != nil {
		return res, err
	}
	res.Output = b
	return res, nil
}

// findingFor recovers the axis and weakness this proposal answers, from the assessment it depends on.
//
// 🔴 The task id carries the axis and the finding index — `propose-memory-0` — because the planner
// derives ids from the finding so replanning converges. Parsing it here rather than passing the finding
// through a side channel keeps the DAG the only place that says what depends on what.
func findingFor(c toolcontract.Call) (axis, instruction string, err error) {
	rest := strings.TrimPrefix(c.TaskID, "propose-")
	rest = strings.TrimPrefix(rest, "verify-")
	i := strings.LastIndex(rest, "-")
	if i <= 0 {
		return "", "", fmt.Errorf("task id %q does not name an axis and a finding", c.TaskID)
	}
	axis = rest[:i]

	// The finding itself comes from the dependency, so the instruction is the customer's actual weakness
	// rather than a generic "improve this".
	findings, _ := collectFindings(c.Inputs)
	for _, f := range findings {
		if f.Axis == axis && f.Actionable {
			return axis, f.Weakness, nil
		}
	}
	return "", "", fmt.Errorf(
		"the %s assessment produced no actionable finding for this proposal to answer", axis)
}
