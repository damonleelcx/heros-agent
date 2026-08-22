package assessment

import (
	"fmt"
	"sort"
)

// diff.go answers the question design D7 exists for: **an assessment's numbers moved — whose fault is
// it?**
//
// # Three reasons a report changes, and only one of them is about the customer
//
//	the SOURCE moved            → a fact about the customer's repository. The only one they can act on.
//	the AGENT CONFIG moved      → we changed how we ask. Held by the pin; visible as a hash change.
//	the PROVIDER MODEL moved    → the provider shipped an upgrade. Nothing about the customer at all.
//
// Without the third recorded, *"a provider's routine upgrade is rendered as the customer's repository
// getting worse, and nobody has any way to tell"*. This file is where that stops being a stored field
// and becomes an answer.
//
// # Why the attribution is computed and not asserted
//
// Task 3.7 asks for a control-variable run: vary only the agent config, then vary only the provider
// model, and both deltas must be ATTRIBUTABLE. "Attributable" is a property of the data, not of a
// procedure somebody follows carefully — so it is computed here from the fields the findings already
// carry, and a test varies one input at a time and asserts the attribution comes back naming it. That
// makes the control-variable discipline checkable rather than a paragraph in a runbook.

// Cause names which input moved.
type Cause string

const (
	// CauseSource — the repository changed. The only cause that is a finding about the customer.
	CauseSource Cause = "source"
	// CauseAgentConfig — the agent definition changed.
	CauseAgentConfig Cause = "agent_config"
	// CauseProviderModel — the provider model that produced the finding changed, with everything else
	// held constant.
	CauseProviderModel Cause = "provider_model"
	// CauseUnattributable — the claim changed with all three inputs identical.
	//
	// 🔴 This is NOT a bucket for leftovers. It is a REAL and alarming state: the same source, the same
	// configuration and the same model produced a different answer, which means something in the
	// pipeline is not deterministic. FR15 says two assessments of the same key are identical, so every
	// occurrence of this value is that guarantee failing — and naming it is how anyone finds out.
	CauseUnattributable Cause = "unattributable"
)

var causes = []Cause{CauseSource, CauseAgentConfig, CauseProviderModel, CauseUnattributable}

// Causes returns the four. A copy.
func Causes() []Cause { return append([]Cause(nil), causes...) }

// Valid reports membership.
func (c Cause) Valid() bool {
	for _, v := range causes {
		if v == c {
			return true
		}
	}
	return false
}

// String makes Cause printable.
func (c Cause) String() string { return string(c) }

// AxisDiff is one axis's change between two assessments.
type AxisDiff struct {
	Axis Axis `json:"axis"`

	BeforeState State `json:"before_state"`
	AfterState  State `json:"after_state"`

	BeforeOrigin Origin `json:"before_origin"`
	AfterOrigin  Origin `json:"after_origin"`

	BeforeClaim string `json:"before_claim"`
	AfterClaim  string `json:"after_claim"`

	// The two attribution fields, carried on the diff so a reader does not have to open two reports to
	// see which of them moved. Empty on both sides for a structural finding.
	BeforeProviderModelVersion string `json:"before_provider_model_version,omitempty"`
	AfterProviderModelVersion  string `json:"after_provider_model_version,omitempty"`

	// Cause names which input moved. 🔴 It is a FIELD rather than something a console derives, for
	// `evalboard`'s reason: "did this get worse because of us or because of them" has exactly one
	// correct answer and a second implementation in JavaScript would drift from this one.
	Cause Cause `json:"cause"`
	// Why is the sentence a reader reads, naming the cause in words.
	Why string `json:"why"`
}

// Changed reports whether anything about this axis is different.
func (d AxisDiff) Changed() bool {
	return d.BeforeState != d.AfterState || d.BeforeOrigin != d.AfterOrigin ||
		d.BeforeClaim != d.AfterClaim ||
		d.BeforeProviderModelVersion != d.AfterProviderModelVersion
}

// Diff compares two assessments of the same workflow and returns ONLY the axes that changed.
//
// 🔴 Only the changed ones, and that is a rendering decision made here rather than in the console:
// task 3.2 says a re-inference "renders as a diff", and a diff that lists nine rows of which seven say
// "unchanged" is a report, not a diff. The unchanged axes are still in the assessment beside it.
//
// It refuses two assessments of different workflows, because a diff across workflows is two facts
// wearing one heading.
func Diff(before, after Assessment) ([]AxisDiff, error) {
	if before.WorkflowID != after.WorkflowID {
		return nil, fmt.Errorf("assessment: cannot diff %s against %s — they describe different workflows",
			before.WorkflowID, after.WorkflowID)
	}
	prior := map[Axis]Finding{}
	for _, f := range before.Findings {
		prior[f.Axis()] = f
	}

	out := []AxisDiff{}
	for _, f := range after.Findings {
		old, ok := prior[f.Axis()]
		if !ok {
			continue
		}
		d := AxisDiff{
			Axis:                       f.Axis(),
			BeforeState:                old.State(),
			AfterState:                 f.State(),
			BeforeOrigin:               old.Origin(),
			AfterOrigin:                f.Origin(),
			BeforeClaim:                old.Claim(),
			AfterClaim:                 f.Claim(),
			BeforeProviderModelVersion: old.ProviderModelVersion(),
			AfterProviderModelVersion:  f.ProviderModelVersion(),
		}
		if !d.Changed() {
			continue
		}
		d.Cause, d.Why = attribute(before, after, old, f)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return axisOrder(out[i].Axis) < axisOrder(out[j].Axis) })
	return out, nil
}

// attribute decides which input moved, in a fixed precedence.
//
// # 🔴 The precedence is the whole design, and it runs from LEAST to MOST alarming
//
//	source        first, because a different revision explains everything downstream of it and there
//	              is no point attributing to a model when the code under it changed;
//	agent config  next, because we changed the question;
//	model version next, because the provider changed the answerer;
//	unattributable last, and it is a defect rather than a category.
//
// The order matters when two inputs moved at once: attributing a source change to a provider upgrade
// would tell a customer their repository is fine when it is not.
func attribute(before, after Assessment, old, now Finding) (Cause, string) {
	switch {
	case before.SourceRevision != after.SourceRevision:
		return CauseSource, fmt.Sprintf(
			"your repository moved from %s to %s. This is a change in your code",
			short(before.SourceRevision), short(after.SourceRevision))
	case before.AgentConfigHash != after.AgentConfigHash:
		return CauseAgentConfig, fmt.Sprintf(
			"we changed how we analyse (%s → %s). Your repository did not change",
			short(before.AgentConfigHash), short(after.AgentConfigHash))
	case old.ProviderModelVersion() != now.ProviderModelVersion():
		return CauseProviderModel, fmt.Sprintf(
			"the model behind this finding changed from %s to %s. Neither your repository nor our "+
				"configuration changed",
			orNone(old.ProviderModelVersion()), orNone(now.ProviderModelVersion()))
	default:
		return CauseUnattributable, "this changed with the revision, the configuration and the model all " +
			"identical. Two assessments of one revision are supposed to be identical, so this is a " +
			"defect on our side and not a finding about your repository"
	}
}

func axisOrder(a Axis) int {
	for i, v := range Axes() {
		if v == a {
			return i
		}
	}
	return len(Axes())
}

func short(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

func orNone(v string) string {
	if v == "" {
		return "no model (read from your code)"
	}
	return v
}
