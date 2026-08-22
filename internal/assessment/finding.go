package assessment

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// finding.go freezes the shape three consumers will key on (task 1.1) and makes its four conditional
// requirements STRUCTURAL rather than reviewed (task 1.2).
//
// # 🔴 Why every field is unexported
//
// `Finding{Axis: AxisMemory, State: StateNotMeasured}` compiles, is invalid, and is exactly the value
// this phase exists to prevent — a surface reporting absence without naming what is missing, which is
// a shrug rendered as a report. Making the fields unexported removes the composite literal from every
// package but this one, so the ONLY way to obtain a Finding is through a constructor that refuses the
// invalid combinations and returns an error naming which one.
//
// The cost is real and worth stating: nine accessors, a hand-written `MarshalJSON`, and a
// `UnmarshalJSON` that round-trips through the same constructors. That cost buys a guarantee the
// alternative cannot give at any price — `careful-api-creation`'s point that a shape read by three
// consumers is a fact contract, and design's: *"a `not_measured` finding with no `missing_input`
// should be impossible to construct, not merely discouraged"*.
//
// # The conditional requirements, in one place
//
//	state = not_measured  ⇒ missing_input is set          and refusal_cause is NOT
//	state = refused       ⇒ refusal_cause is set          and missing_input is NOT
//	state = measured      ⇒ an EvalSetReport is attached  and no other state carries one
//	origin = inferred     ⇒ provider_model_version AND inference_address are set
//	origin = structural   ⇒ neither is set
//
// The negative halves are as load-bearing as the positive ones. A `refused` finding carrying a
// missing input is a finding a console will render in two different message shapes depending on which
// field it happens to check first, and that ambiguity is what a closed vocabulary was supposed to end.

// ErrInvalidFinding is the sentinel every constructor's refusal wraps, so a caller can tell "this
// finding is malformed" from "the store is down" without matching on message text.
var ErrInvalidFinding = errors.New("assessment: invalid finding")

// Finding is one axis's result. Obtain one from Measured, Observed, NotMeasured or Refused.
//
// 🔴 The zero value is NOT a valid finding, and `Validate` says so. That is deliberate: a Finding read
// out of a `map[Axis]Finding` that has no entry for the axis must not look like a report.
type Finding struct {
	axis                 Axis
	state                State
	origin               Origin
	claim                string
	evidence             EvidenceRef
	missingInput         MissingInput
	refusalCause         RefusalCause
	providerModelVersion string
	inferenceAddress     string

	// eval is the decisiveness payload of exactly one state, the way missingInput is the payload of
	// exactly one other. It is a pointer so that "no eval set" and "an eval set with zero cases" stay
	// distinguishable — the second is a real and alarming thing, and a value type would render it as
	// the first.
	eval *EvalSetReport
}

// ── Accessors ────────────────────────────────────────────────────────────────────────────────────

// Axis returns the surface this finding is about.
func (f Finding) Axis() Axis { return f.axis }

// State returns how the claim was established.
func (f Finding) State() State { return f.state }

// Origin returns what produced the claim.
func (f Finding) Origin() Origin { return f.origin }

// Claim is the sentence a reader reads.
func (f Finding) Claim() string { return f.claim }

// Evidence is the reference into an existing surface.
func (f Finding) Evidence() EvidenceRef { return f.evidence }

// MissingInput is the named input a `not_measured` finding lacked. Empty in every other state.
func (f Finding) MissingInput() MissingInput { return f.missingInput }

// RefusalCause names which of three things this build lacks. Empty in every other state.
func (f Finding) RefusalCause() RefusalCause { return f.refusalCause }

// ProviderModelVersion is the model that produced an inferred finding (design D7). Empty when
// structural.
//
// 🔴 This field is the whole reason a provider's routine upgrade is distinguishable from the
// customer's repository getting worse. Without it, an assessment's numbers move for three reasons and
// a reader can attribute them to only two.
func (f Finding) ProviderModelVersion() string { return f.providerModelVersion }

// InferenceAddress is the content address of the pin behind an inferred finding. Empty when
// structural.
func (f Finding) InferenceAddress() string { return f.inferenceAddress }

// Eval returns the decisiveness report behind a measured finding, or nil.
func (f Finding) Eval() *EvalSetReport { return f.eval }

// ── Constructors ─────────────────────────────────────────────────────────────────────────────────
//
// # 🔴 One constructor per LEGAL CELL, and only six cells are legal
//
// Four states × two origins is eight combinations. Two of them are contradictions, and the way they
// are refused matters: not by a check inside a general-purpose constructor, but by there being NO
// FUNCTION that produces them.
//
//	                 structural        inferred
//	  measured       Measured          ✗  a measurement comes from an eval run, never from a
//	                                      model reading code — otherwise a model reports a
//	                                      confidence interval it did not compute
//	  observed       Observed          Inferred
//	  not_measured   NotMeasured       Abstained
//	  refused        Refused           ✗  a refusal is a fact about THIS BUILD's capability;
//	                                      asking a model whether we can assess something is absurd
//
// This is also why `Inferred` and `Abstained` take the model version and the pin address as ORDINARY
// PARAMETERS rather than as a decoration applied afterwards. Design D7's requirement is then held by
// the call signature — there is no way to obtain an inferred finding without supplying both — instead
// of by a validation a caller discovers at runtime.

// Measured builds a finding backed by an eval run.
//
// The report is required, not optional-with-a-warning: `eval-set-decisiveness`'s first requirement is
// that decisiveness travels with the score WHEREVER it is reported, and a score that could arrive
// without it would eventually arrive without it.
func Measured(axis Axis, claim string, ref EvidenceRef, report EvalSetReport) (Finding, error) {
	return finish(Finding{axis: axis, state: StateMeasured, origin: OriginStructural, claim: claim, evidence: ref, eval: &report})
}

// Observed builds a deterministic structural finding — read from the IR or the tree, true by
// construction.
//
// 🔴 An observation may state an ABSENCE: "no memory reads or writes were found across twelve nodes"
// is an observation, because a total scan of the IR found none (PRD §14 A3). The OPINION that the
// absence is a defect is a proposal and belongs to P35, where verification can decide it.
func Observed(axis Axis, claim string, ref EvidenceRef) (Finding, error) {
	return finish(Finding{axis: axis, state: StateObserved, origin: OriginStructural, claim: claim, evidence: ref})
}

// Inferred builds a finding HEROS concluded by reading source.
//
// It is the same STATE as `Observed` — a claim about the repository has been established — and a
// different ORIGIN, which is exactly the distinction design D2 exists to keep visible. `Ordered` ranks
// it below every structural observation, and the console marks it persistently and without hover.
//
// Both attribution arguments are required by the signature. `inferenceAddress` makes the claim
// replayable; `providerModelVersion` is what makes a provider's routine upgrade distinguishable from
// the customer's repository getting worse (design D7).
func Inferred(axis Axis, claim string, ref EvidenceRef, providerModelVersion, inferenceAddress string) (Finding, error) {
	return finish(Finding{
		axis: axis, state: StateObserved, origin: OriginInferred, claim: claim, evidence: ref,
		providerModelVersion: providerModelVersion, inferenceAddress: inferenceAddress,
	})
}

// NotMeasured builds the phase's central finding: the axis was examined structurally and nothing
// could be established, naming what was missing.
//
// There is no overload without `missing`, and that is the point of task 1.2. A caller who has no
// missing input to name has not finished thinking about the finding.
func NotMeasured(axis Axis, missing MissingInput, claim string, ref EvidenceRef) (Finding, error) {
	return finish(Finding{axis: axis, state: StateNotMeasured, origin: OriginStructural, claim: claim, evidence: ref, missingInput: missing})
}

// Abstained builds the finding for an inference that DECLINED to conclude (FR10).
//
// 🔴 Its own constructor rather than `NotMeasured(..., MissingInferenceAbstained, ...)` for two
// reasons. First, an abstention has a pin — the decision not to conclude is itself the pinned result,
// and replaying it must not cost a second provider call. Second, §3.5 counts abstention as a SUCCESS
// of the discipline rather than as a miss, and a metric can only count what has a name.
//
// The claim still has to be a sentence: "the memory strategy could not be determined from the source"
// is a finding; a blank is not.
func Abstained(axis Axis, claim string, ref EvidenceRef, providerModelVersion, inferenceAddress string) (Finding, error) {
	return finish(Finding{
		axis: axis, state: StateNotMeasured, origin: OriginInferred, claim: claim, evidence: ref,
		missingInput:         MissingInferenceAbstained,
		providerModelVersion: providerModelVersion, inferenceAddress: inferenceAddress,
	})
}

// Refused builds the finding for an axis this BUILD cannot assess, naming which of three things is
// missing.
//
// Always structural: a refusal is a fact about the platform's own capability, established by looking
// at what this build contains. The signature carries no origin, so it cannot become one.
func Refused(axis Axis, cause RefusalCause, claim string, ref EvidenceRef) (Finding, error) {
	return finish(Finding{axis: axis, state: StateRefused, origin: OriginStructural, claim: claim, evidence: ref, refusalCause: cause})
}

// finish validates and returns. Every constructor ends here, so there is exactly one place the rules
// live and exactly one place a new rule has to be added.
func finish(f Finding) (Finding, error) {
	if err := f.Validate(); err != nil {
		return Finding{}, err
	}
	return f, nil
}

// Validate is the whole rule set, and it is exported so the store, the schema proof and the fence all
// ask the SAME question. A second implementation of these rules anywhere is a second answer.
func (f Finding) Validate() error {
	if !f.axis.Valid() {
		return fmt.Errorf("%w: %q is not one of the nine axes", ErrInvalidFinding, f.axis)
	}
	if !f.state.Valid() {
		return fmt.Errorf("%w: %s carries state %q, which is not one of the four", ErrInvalidFinding, f.axis, f.state)
	}
	if !f.origin.Valid() {
		return fmt.Errorf("%w: %s carries origin %q, which is not one of the two", ErrInvalidFinding, f.axis, f.origin)
	}
	if strings.TrimSpace(f.claim) == "" {
		// A finding with no claim is a row with nothing in it. Even `not_measured` has a sentence:
		// "here is what is missing and what you could do", which §8.1 makes the copy requirement.
		return fmt.Errorf("%w: %s states nothing", ErrInvalidFinding, f.axis)
	}
	if err := f.evidence.Validate(); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrInvalidFinding, f.axis, err)
	}

	// ── missing_input ────────────────────────────────────────────────────────────────────────────
	switch {
	case f.state.NeedsMissingInput():
		if f.missingInput == "" {
			return fmt.Errorf("%w: %s is not_measured and names no missing input — "+
				"a report that says \"we could not\" without saying what it lacked is a shrug",
				ErrInvalidFinding, f.axis)
		}
		if !f.missingInput.Valid() {
			return fmt.Errorf("%w: %s names missing input %q, which is outside the closed set",
				ErrInvalidFinding, f.axis, f.missingInput)
		}
	case f.missingInput != "":
		return fmt.Errorf("%w: %s is %s and carries a missing input; only not_measured may",
			ErrInvalidFinding, f.axis, f.state)
	}

	// ── refusal_cause ────────────────────────────────────────────────────────────────────────────
	switch {
	case f.state.NeedsRefusalCause():
		if f.refusalCause == "" {
			return fmt.Errorf("%w: %s is refused and names none of the frontend, the analysis or the language",
				ErrInvalidFinding, f.axis)
		}
		if !f.refusalCause.Valid() {
			return fmt.Errorf("%w: %s names refusal cause %q, which is not one of the three",
				ErrInvalidFinding, f.axis, f.refusalCause)
		}
	case f.refusalCause != "":
		return fmt.Errorf("%w: %s is %s and carries a refusal cause; only refused may",
			ErrInvalidFinding, f.axis, f.state)
	}

	// ── the inference pair ───────────────────────────────────────────────────────────────────────
	//
	// Both or neither, checked as a PAIR. One without the other is the shape design D7 warns about
	// from the other direction: a pin address with no model version records where an answer came from
	// but not what produced it, so a provider's upgrade is still invisible.
	switch {
	case f.origin.NeedsInferenceAddress():
		if strings.TrimSpace(f.inferenceAddress) == "" {
			return fmt.Errorf("%w: %s is inferred and carries no pinned inference address, so the "+
				"claim cannot be replayed or attributed", ErrInvalidFinding, f.axis)
		}
		if strings.TrimSpace(f.providerModelVersion) == "" {
			return fmt.Errorf("%w: %s is inferred and records no provider model version, so a "+
				"provider upgrade would be indistinguishable from the repository changing",
				ErrInvalidFinding, f.axis)
		}
	default:
		if f.inferenceAddress != "" || f.providerModelVersion != "" {
			return fmt.Errorf("%w: %s is structural and carries inference attribution",
				ErrInvalidFinding, f.axis)
		}
	}

	// ── the eval report ──────────────────────────────────────────────────────────────────────────
	switch {
	case f.state == StateMeasured:
		if f.eval == nil {
			return fmt.Errorf("%w: %s is measured and carries no eval-set report, so its number "+
				"would be rendered without the decisiveness that says how to read it",
				ErrInvalidFinding, f.axis)
		}
		if err := f.eval.Validate(); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalidFinding, f.axis, err)
		}
	case f.eval != nil:
		return fmt.Errorf("%w: %s is %s and carries an eval-set report; only measured may",
			ErrInvalidFinding, f.axis, f.state)
	}

	// ── the two illegal cells ────────────────────────────────────────────────────────────────────
	//
	// No constructor produces either, so inside this package they are unreachable. They are checked
	// anyway because `UnmarshalJSON` is a second entrance: a row read back out of a database or a
	// payload posted by a client arrives here without passing a constructor at all.
	if f.state == StateMeasured && f.origin == OriginInferred {
		return fmt.Errorf("%w: %s is measured and inferred; a measurement comes from an eval run, "+
			"never from a model reading code", ErrInvalidFinding, f.axis)
	}
	if f.state == StateRefused && f.origin != OriginStructural {
		return fmt.Errorf("%w: %s is refused and not structural; a refusal is a fact about this build's "+
			"capability, not a conclusion a model can reach", ErrInvalidFinding, f.axis)
	}
	return nil
}

// ── Wire form ────────────────────────────────────────────────────────────────────────────────────

// findingWire is the JSON shape. It is a private mirror rather than the type itself so that the
// unexported fields stay unexported: exporting them for `encoding/json` would hand every package the
// composite literal back and undo the whole guarantee.
//
// 🔴 The conditional fields are `omitempty`. An `observed` finding must not ship `"missing_input": ""`,
// because a console reading a present-but-empty key has to decide what an empty string means, and the
// answer somebody eventually picks is "not applicable" on one screen and "unknown" on another.
type findingWire struct {
	Axis                 Axis           `json:"axis"`
	State                State          `json:"state"`
	Origin               Origin         `json:"origin"`
	Claim                string         `json:"claim"`
	Evidence             EvidenceRef    `json:"evidence_ref"`
	MissingInput         MissingInput   `json:"missing_input,omitempty"`
	RefusalCause         RefusalCause   `json:"refusal_cause,omitempty"`
	ProviderModelVersion string         `json:"provider_model_version,omitempty"`
	InferenceAddress     string         `json:"inference_address,omitempty"`
	Eval                 *EvalSetReport `json:"eval_set,omitempty"`
}

// MarshalJSON writes the wire form.
func (f Finding) MarshalJSON() ([]byte, error) {
	return json.Marshal(findingWire{
		Axis: f.axis, State: f.state, Origin: f.origin, Claim: f.claim, Evidence: f.evidence,
		MissingInput: f.missingInput, RefusalCause: f.refusalCause,
		ProviderModelVersion: f.providerModelVersion, InferenceAddress: f.inferenceAddress,
		Eval: f.eval,
	})
}

// UnmarshalJSON reads the wire form and VALIDATES it.
//
// 🔴 This is the boundary the guarantee would otherwise leak through. Unexported fields stop a Go
// caller writing an invalid literal; they do nothing about a row read back out of a database or a
// payload posted by a client. So decoding runs the same `Validate` a constructor runs, and a stored
// finding that violates a conditional requirement fails to decode rather than rendering.
//
// `DisallowUnknownFields` is deliberate for `careful-api-creation`'s reason: the field a future client
// would add here is a score, and accepting it silently is how R4 is defeated without a review.
func (f *Finding) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	var w findingWire
	if err := dec.Decode(&w); err != nil {
		return fmt.Errorf("assessment: decoding a finding: %w", err)
	}
	out := Finding{
		axis: w.Axis, state: w.State, origin: w.Origin, claim: w.Claim, evidence: w.Evidence,
		missingInput: w.MissingInput, refusalCause: w.RefusalCause,
		providerModelVersion: w.ProviderModelVersion, inferenceAddress: w.InferenceAddress,
		eval: w.Eval,
	}
	if err := out.Validate(); err != nil {
		return err
	}
	*f = out
	return nil
}
