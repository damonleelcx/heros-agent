package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

// gatewayanalyst.go is the ONE implementation of `Analyst` that reaches a provider.
//
// 🔴 It goes through `providergateway` and nothing else, for `herosagent/gatewaymodel.go`'s four
// reasons, unchanged: a direct HTTP call here would compile and work and would bypass the configured
// secrets source, retries carrying an idempotency key (a retry the provider treats as a new request is
// a double charge), the observer that makes a provider outage visible to P2.5, and the normalisation
// that keeps this package from knowing which vendor spells `system` which way.
//
// # 🔴 Why the idempotency key is the CONTENT ADDRESS
//
// `herosagent` uses `(workflow, revision, config_hash)` because that is its pin key. This one is per
// AXIS as well — nine calls share those three parts — so using them here would send an identical
// `Idempotency-Key` for all nine, and a provider that honours the header would answer the memory
// question with the model question's answer. That is `Input.AgentConfigHash`'s defect one level down,
// and it is avoided by keying on the thing that actually differs: the question's content address.
//
// # What the model is asked for, and the one thing it may not do
//
// A JSON object with four fields. It may CONCLUDE or it may ABSTAIN, and abstaining is stated in the
// instruction as a first-class success rather than a fallback — because a model told only how to answer
// will answer.
//
// 🚫 It is never asked for a score, a grade or a rating. There is no field in `rawAnswer` one could
// arrive in, so an obliging model that offers one has it dropped at the parse.

// GatewayAnalyst calls a provider through the gateway to answer one axis.
type GatewayAnalyst struct {
	gw    *providergateway.Gateway
	entry *registry.ModelEntry
	floor float64
}

// NewGatewayAnalyst wires the provider-backed analyst.
//
// The floor is passed in so the INSTRUCTION can state it. A model told "abstain when unsure" and a
// model told "abstain below 0.70 confidence" behave differently, and the second is the one whose
// output the floor check will agree with — otherwise the platform silently discards answers the model
// believed it was supposed to give, and the abstention rate measures a disagreement about the rule
// rather than the difficulty of the repository.
func NewGatewayAnalyst(gw *providergateway.Gateway, entry *registry.ModelEntry, floor float64) (*GatewayAnalyst, error) {
	switch {
	case gw == nil:
		return nil, errors.New("assessment: a provider gateway is required")
	case entry == nil:
		return nil, errors.New("assessment: a resolved model entry is required")
	case floor <= 0 || floor > 1:
		return nil, fmt.Errorf("assessment: the confidence floor is %v", floor)
	}
	return &GatewayAnalyst{gw: gw, entry: entry, floor: floor}, nil
}

// rawAnswer is the agreed shape. Four fields, and no field for a score.
type rawAnswer struct {
	// Abstain is the model saying it cannot tell. First, because it is the field that matters.
	Abstain bool `json:"abstain"`
	// Reason is required when abstaining. "I do not know" without "because the tool list is built at
	// runtime" is a shrug; with it, it is a task.
	Reason string `json:"reason,omitempty"`
	// Claim is one sentence about what the repository DOES. Present tense, no recommendation.
	Claim string `json:"claim,omitempty"`
	// Confidence is the model's own 0..1.
	Confidence float64 `json:"confidence"`
}

// Assess performs one call and parses the answer.
//
// 🔴 It parses and validates NOTHING beyond the shape. The floor, the empty-claim case and the
// construction of the finding are `HerosInference`'s, in one place — so there is one answer to "is
// this output acceptable", and a second Analyst implementation cannot decide differently.
func (a *GatewayAnalyst) Assess(ctx context.Context, q Question) (Answer, error) {
	body, err := json.Marshal(q)
	if err != nil {
		return Answer{}, fmt.Errorf("assessment: encoding the question for %s: %w", q.Axis, err)
	}
	address, err := ContentAddress(q)
	if err != nil {
		return Answer{}, err
	}

	resp, err := a.gw.Complete(ctx, a.entry, providergateway.Request{
		System:         a.instruction(q.Axis),
		Messages:       []providergateway.Message{{Role: "user", Content: string(body)}},
		IdempotencyKey: address,
	}, nil)
	if err != nil {
		return Answer{}, err
	}

	out := Answer{
		// 🔴 The version the provider ECHOED, not the one the configuration asked for. `Response`'s own
		// comment says why it exists: "so a run record can prove which provider answered rather than
		// which one the config asked for" — and design D7's whole point is that a provider serving a
		// different model than the one requested must be visible.
		ProviderModelVersion: resp.Provider + "/" + resp.ModelID,
		Usage:                resp.Usage,
	}

	var raw rawAnswer
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &raw); err != nil {
		// 🚫 NOT repaired and NOT retried with a nudge. An answer this layer cannot parse is an answer
		// the contract was not met on — and it becomes an ABSTENTION rather than an error, because the
		// reader's position is identical ("we could not determine this") and an error here would lose
		// the eight other axes.
		out.Abstained = true
		out.AbstentionReason = "the analysis did not return an answer in the agreed shape"
		return out, nil
	}
	out.Abstained = raw.Abstain
	out.AbstentionReason = raw.Reason
	out.Claim = raw.Claim
	out.Confidence = raw.Confidence
	return out, nil
}

// instruction is the system prompt. It is a function of the AXIS, because the nine questions are nine
// questions and one generic instruction would produce nine generic answers.
//
// 🔴 The abstention clause is stated FIRST and with the floor as a number. A model told only how to
// answer will answer; a model told "abstaining is a correct outcome, and here is the threshold" abstains
// where it should, which is the behaviour §3.5 rewards and the behaviour FR10 requires.
func (a *GatewayAnalyst) instruction(axis Axis) string {
	var b strings.Builder
	b.WriteString("You are reading facts extracted from one repository by a static analyser. ")
	b.WriteString("Answer exactly one question about it, and answer ONLY with a JSON object of the form ")
	b.WriteString(`{"abstain": bool, "reason": string, "claim": string, "confidence": number}.` + "\n\n")

	b.WriteString("ABSTAINING IS A CORRECT ANSWER. If the facts you are given cannot support a claim, ")
	b.WriteString(`set "abstain": true and put the specific missing input in "reason" — for example `)
	b.WriteString(`"the tool list is assembled at runtime, so what the model is offered is not in the source". `)
	b.WriteString(fmt.Sprintf("Abstain rather than answering below %.2f confidence. A wrong claim is worse "+
		"than no claim, because the reader cannot tell it is wrong.\n\n", a.floor))

	b.WriteString("Some facts are marked `ir_floor`. A floor is what the analyser emits for EVERY repository ")
	b.WriteString("when it cannot see the thing at all. It is the absence of evidence, never evidence of ")
	b.WriteString("absence — do not report a floor as a finding.\n\n")

	b.WriteString(`Your "claim" is one sentence in the present tense describing what the repository DOES. `)
	b.WriteString("Do not recommend anything. Do not score, grade or rate anything. Do not compare this ")
	b.WriteString("repository to any other.\n\n")

	b.WriteString("The question is: ")
	b.WriteString(axisQuestion(axis))
	return b.String()
}

// axisQuestion is the nine questions, written out.
//
// A switch with no default arm: a tenth axis added without a question here is a compile-time gap the
// author sees, rather than a generic instruction that silently produces a generic answer.
func axisQuestion(axis Axis) string {
	switch axis {
	case AxisModel:
		return "which models does this repository use at its call sites, and with what parameters?"
	case AxisPrompt:
		return "where do this repository's prompts come from — written at the call site, a template, or built at runtime?"
	case AxisSkills:
		return "what platform skills does this repository bind at its call sites?"
	case AxisContext:
		return "how does each call assemble the message list it sends?"
	case AxisTools:
		return "what tools does this repository offer the model, and are they a fixed list or assembled at runtime?"
	case AxisMemory:
		return "what does this repository carry BETWEEN turns and sessions — what store is read and written, " +
			"and is anything ever pruned or expired? Remember that a single call site cannot show you this; " +
			"abstain unless the facts you were given actually establish it."
	case AxisHarness:
		return "what scaffold surrounds each call — how many turns does it run, and under what stop condition? " +
			"A source loop around a call site is NOT an agent loop: a `for` over a list of tickets fires the " +
			"node many times with no scaffold at all, while an agent loop is the model deciding to take " +
			"another turn. If you cannot tell those apart from what you were given, abstain."
	case AxisLoop:
		return "what control loop does each multi-turn node run in?"
	case AxisGraph:
		return "how are this repository's call sites connected — what data or control flows between them?"
	}
	return ""
}
