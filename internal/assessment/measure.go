package assessment

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalboard"
	"github.com/heros-foreal/agentd/internal/evalgen"
	"github.com/heros-foreal/agentd/internal/evalharness"
)

// measure.go is the third source of findings: a NUMBER, from an eval run, carrying its interval and
// the properties that say how to read it.
//
// # 🔴 P4 unchanged — no bespoke oracle, no new scorer (task 4.1)
//
// Nothing in this file decides whether a case passed. `evalharness.Case.DecisiveOracle` decides
// whether an oracle can fail; `evalgen.MeasureQuality` computes the set's properties;
// `evalboard.CoverageView` aggregates them. This file GENERATES a set with P4 and REPORTS what P4
// says about it.
//
// The temptation is a "simple" oracle for the assessment's own purposes, and it is the phase's most
// dangerous shortcut: a second scorer means the assessment's number and the board's number are two
// answers to one question, and the reader has no way to know which they are looking at. It is the same
// prohibition design D5 states for evidence, one layer down.
//
// # The four reasons, and why they stay four
//
// `eval-set-decisiveness`'s last requirement: *"the named missing input is exactly one of: no runnable
// entry point, missing credential, sandbox refusal, or unsupported language — and each renders a
// distinct message."* A reader does four different things about them — write an entry point, supply a
// credential, ask us about the sandbox, learn the language is out of scope — and one message tells
// them to do none.
//
// 🚫 There is deliberately NO fifth reason and no "other". `Runnability` returns a member of the
// closed set or reports the workflow runnable; a case nobody has classified would have to be added to
// the set, which is a decision somebody makes rather than a default they inherit.

// Runnability reports whether a workflow can be executed well enough to measure, and when it cannot,
// which of exactly four reasons applies.
type Runnability struct {
	// Runnable is the answer. When false, Reason is one of the four.
	Runnable bool         `json:"runnable"`
	Reason   MissingInput `json:"reason,omitempty"`
	// Detail names the specific thing — WHICH node the sandbox refused, WHICH credential is absent.
	// It is what turns a category into a task, and it is why `Claim` on the finding is prose rather
	// than a lookup of the reason's name.
	Detail string `json:"detail,omitempty"`
}

// Sandbox is the P3 posture, unchanged (task 6.3).
//
// 🔴 An interface with ONE method that answers a question, not one that runs anything. This package
// executes no customer code: it asks whether the sandbox WOULD, and the running happens inside
// `internal/sandbox` where the posture lives. A `Run` method here would be an invitation to execute
// beside the sandbox rather than under it, which is precisely what task 6.3 forbids.
type Sandbox interface {
	// Admits reports whether the sandbox would execute this workflow, and why not when it would not.
	Admits(ctx context.Context, workflowID string) (ok bool, why string, err error)
}

// Credentials reports whether the provider credentials a workflow's call sites need are available.
type Credentials interface {
	// Missing returns the provider names a workflow needs and this deployment cannot resolve. Empty
	// means every one resolves.
	Missing(ctx context.Context, providers []string) ([]string, error)
}

// AssessRunnability applies the four reasons in a FIXED ORDER, and the order is the design.
//
// It runs from the most fundamental to the most incidental, so a reader is told the thing they must
// fix FIRST rather than the thing we happened to check first:
//
//	unsupported language  → nothing we ship can run this at all. Everything below is moot.
//	no runnable entry     → we support the language and there is nothing to start.
//	sandbox refusal       → there is something to start and our sandbox declines it. Ours to explain.
//	missing credential    → everything is in place except a key. The cheapest to fix, so it is last.
//
// Checking them in the other order would tell a customer to supply a credential for a workflow written
// in a language we cannot run.
func AssessRunnability(ctx context.Context, s Subject, box Sandbox, creds Credentials) (Runnability, error) {
	if s.IR == nil || len(s.IR.Nodes) == 0 {
		// Not one of the four: there is no workflow here to be runnable or not, and answering with one
		// of the four would send a reader to fix something that is not the problem. The caller degrades
		// this to the structural pass's own missing input.
		return Runnability{}, fmt.Errorf("assessment: %s has no discovered call sites to measure", s.WorkflowID)
	}

	// ── 1 · unsupported language ─────────────────────────────────────────────────────────────────
	//
	// Read from the discovery report's own record of which frontends CONTRIBUTED, not from a list of
	// languages held here. A list here would be a second copy of the frontend registry, and the copy
	// is what goes stale the day a frontend is added.
	if len(s.Report.Frontends) == 0 {
		lang := s.IR.Workflow.Language
		if lang == "" {
			lang = "this repository's language"
		}
		return Runnability{
			Reason: MissingLanguageSupport,
			Detail: fmt.Sprintf("no frontend in this build contributed to %s's graph, so there is no "+
				"runner for it either", lang),
		}, nil
	}

	// ── 2 · no runnable entry point ──────────────────────────────────────────────────────────────
	//
	// 🔴 An entry point is a DECLARED one (`llm-eval.yaml`) or a call site the frontend resolved
	// enough to invoke. A node whose model is `unresolved` cannot be run: we would not know what to
	// call. Reporting "no entry point" for a repository full of call sites would be wrong, so the
	// check is per node and the detail names how many were rejected and why.
	runnable, unresolved := 0, 0
	for _, n := range s.IR.Nodes {
		if n.Model.ModelID == discovery.UnresolvedSentinel || n.Model.Provider == discovery.UnresolvedSentinel {
			unresolved++
			continue
		}
		runnable++
	}
	if runnable == 0 {
		return Runnability{
			Reason: MissingEntryPoint,
			Detail: fmt.Sprintf("all %d discovered call sites choose their model at runtime, so there is "+
				"nothing this build can invoke with a known configuration. Declaring an entry point in "+
				"llm-eval.yaml is what makes them runnable", unresolved),
		}, nil
	}

	// ── 3 · sandbox refusal ──────────────────────────────────────────────────────────────────────
	if box == nil {
		// 🔴 A deployment with no sandbox reports a REFUSAL, not a success. P3's posture is that
		// customer code runs under the sandbox; a nil one here means there is nothing to run it under,
		// and treating that as "fine" is exactly how "assessment executes customer code under the
		// sandbox, not beside it" stops being true.
		return Runnability{
			Reason: MissingSandboxRefusal,
			Detail: "this deployment ships no execution sandbox, so no customer code is run here at all",
		}, nil
	}
	ok, why, err := box.Admits(ctx, s.WorkflowID)
	if err != nil {
		return Runnability{}, fmt.Errorf("assessment: asking the sandbox about %s: %w", s.WorkflowID, err)
	}
	if !ok {
		if strings.TrimSpace(why) == "" {
			why = "the sandbox declined to execute it and gave no reason"
		}
		return Runnability{Reason: MissingSandboxRefusal, Detail: why}, nil
	}

	// ── 4 · missing credential ───────────────────────────────────────────────────────────────────
	if creds != nil {
		providers := map[string]bool{}
		for _, n := range s.IR.Nodes {
			if p := n.Model.Provider; p != "" && p != discovery.UnresolvedSentinel {
				providers[p] = true
			}
		}
		names := make([]string, 0, len(providers))
		for p := range providers {
			names = append(names, p)
		}
		sort.Strings(names)

		missing, err := creds.Missing(ctx, names)
		if err != nil {
			return Runnability{}, fmt.Errorf("assessment: resolving credentials for %s: %w", s.WorkflowID, err)
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return Runnability{
				Reason: MissingCredential,
				Detail: fmt.Sprintf("this workflow calls %s and this deployment cannot resolve a "+
					"credential for %s", phrase(names), phrase(missing)),
			}, nil
		}
	}
	return Runnability{Runnable: true}, nil
}

// Claim renders the four reasons as four DISTINCT sentences (task 4.4).
//
// 🔴 Four functions' worth of copy in one switch with no default arm. A default would be a fifth
// message that says nothing, arriving the first time somebody adds a reason — and the requirement is
// that four stay four.
func (r Runnability) Claim() string {
	switch r.Reason {
	case MissingEntryPoint:
		return "no measurement was taken because there is no entry point this build can run: " + r.Detail +
			". Declaring one in llm-eval.yaml is what makes these call sites runnable"
	case MissingCredential:
		return "no measurement was taken because a provider credential is missing: " + r.Detail +
			". Supplying it makes this axis measurable"
	case MissingSandboxRefusal:
		return "no measurement was taken because our sandbox would not execute this workflow: " + r.Detail +
			". This is a limit on our side — tell us about it and we will look"
	case MissingLanguageSupport:
		return "no measurement was taken because this build has no runner for this language: " + r.Detail +
			". The rest of the assessment is unaffected"
	default:
		return ""
	}
}

// EvalRun is the seam that actually runs a generated set. It is `evalharness` shaped and this package
// supplies no implementation, for `Analyst`'s reason: a stub that returns plausible scores is
// indistinguishable from a working harness.
type EvalRun interface {
	// MeasurableAxes declares which axes this runner can produce a NUMBER for.
	//
	// 🔴 Declared rather than attempted, and this is the guard against the phase's most attractive
	// over-claim. An eval set measures the WORKFLOW's behaviour; attributing that score to one axis is
	// P4.5's attribution problem and is not solved by running the eval. A runner that answered for
	// every axis would put nine measured numbers on a report where one measurement was taken, and each
	// of them would be the same number wearing a different label.
	//
	// An empty list is the honest answer for a deployment that measures nothing, and it is the default:
	// rollout stages 1 and 2 have no measurement at all (PRD §12).
	MeasurableAxes() []Axis
	// Run generates and executes an eval set for one workflow, returning the coverage view P4
	// computed and the cases behind it.
	//
	// 🔴 It returns `evalboard.CoverageView` — the EXISTING aggregate — rather than numbers of its
	// own. `FromCoverage` then copies them; nothing here recomputes oracle coverage or the indecisive
	// count, so the assessment and the board cannot disagree about the set they are both describing.
	Run(ctx context.Context, axis Axis, s Subject) (score Interval, cv evalboard.CoverageView, cases []evalharness.Case, hash string, err error)
}

// Measurement produces measured findings.
type Measurement struct {
	run   EvalRun
	box   Sandbox
	creds Credentials
}

// NewMeasurement wires the measurement stage. `run` is required; the sandbox and the credential source
// may be nil, and each nil is a REASON rather than a skipped check — see AssessRunnability.
func NewMeasurement(run EvalRun, box Sandbox, creds Credentials) (*Measurement, error) {
	if run == nil {
		return nil, fmt.Errorf("assessment: a measurement needs an eval runner — there is deliberately " +
			"no stub, because a stub returning plausible scores is indistinguishable from a working harness")
	}
	return &Measurement{run: run, box: box, creds: creds}, nil
}

// Axes declares which axes this measurement can produce a number for, read from the eval runner's own
// declaration rather than from a list here — one answer to "what can be measured".
func (m *Measurement) Axes() []Axis { return m.run.MeasurableAxes() }

// Measure attempts to produce a measured finding for one axis.
//
// It returns a FINDING in every path: a measured one when the eval ran, and a `not_measured` one
// naming which of the four reasons applied when it did not. There is no path that returns nothing,
// because an axis that produced no finding is an axis missing from a nine-axis report.
func (m *Measurement) Measure(ctx context.Context, axis Axis, s Subject) (Finding, error) {
	can, err := AssessRunnability(ctx, s, m.box, m.creds)
	if err != nil {
		return Finding{}, err
	}
	if !can.Runnable {
		return NotMeasured(axis, can.Reason, can.Claim(), s.Evidence())
	}

	score, cv, cases, hash, err := m.run.Run(ctx, axis, s)
	if err != nil {
		return Finding{}, fmt.Errorf("assessment: running the eval for %s: %w", axis, err)
	}
	report := FromCoverage(hash, score, cv, cases)

	// 🔴 The claim states the number AND how to read it, in the same sentence. §D4: *"a property that
	// changes how a number should be read must be visible at the same time as the number."* The
	// structured fields travel with the finding as well — this is the copy half, and it exists because
	// a sentence is what somebody pastes into a message where the fields do not follow.
	claim := fmt.Sprintf("%.2f (%.2f–%.2f) over %d seeds and %d cases",
		score.Mean, score.Low, score.High, score.NSeeds, report.NCases)
	if report.CannotFail() {
		// The most important sentence in this file. A set whose every oracle is indecisive scores 1.0,
		// and a reader shown 1.0 without this reads it as a strong result.
		claim = fmt.Sprintf("%s — but EVERY case in this set carries an oracle that can never fail, "+
			"so this number is not evidence of quality", claim)
	} else if report.NIndecisive > 0 {
		claim = fmt.Sprintf("%s, of which %d carry an oracle that can never fail", claim, report.NIndecisive)
	}
	if !report.CoverageMeasured {
		claim += ". Oracle coverage was not measured for this set, so how much of it can decide anything is unknown"
	}
	return Measured(axis, claim, EvidenceRef{Surface: SurfaceBoard, Locator: s.WorkflowID, Fragment: hash}, report)
}

// quality is a thin pass-through kept so the dependency on `evalgen` is visible in this file rather
// than implied. `FromCoverage` reads the aggregate from `evalboard.CoverageView`, which `evalgen`
// produced — the chain is generator → quality → coverage view → report, and no link recomputes the
// previous one.
var _ = evalgen.MeasureQuality
