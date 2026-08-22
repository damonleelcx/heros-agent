package assessment

import (
	"strings"
	"testing"
)

// diff_test.go is §3.7's control-variable run, executed rather than described.
//
// # 🔴 Why this is a test and not a runbook
//
// The task says: *"same repository, vary only the agent config; then vary only the provider model.
// Both deltas must be attributable."* "Attributable" is a property of the DATA — of what the findings
// carry — not of how carefully somebody performed the comparison. So each variable is varied here in
// isolation and the attribution is asserted to come back naming it. A procedure in a document is
// followed once; this runs on every commit.

// twoAssessments builds a pair differing in exactly the ways the caller names.
func twoAssessments(t *testing.T, revA, revB, cfgA, cfgB, modelA, modelB string) (Assessment, Assessment) {
	t.Helper()
	ev := EvidenceRef{Surface: SurfaceGraph, Locator: "wf-1"}
	build := func(id, rev, cfg, model, claim string) Assessment {
		a := Assessment{
			AssessmentID: id, TenantID: "tn-1", WorkflowID: "wf-1",
			SourceRevision: rev, AgentConfigHash: cfg,
		}
		for _, axis := range Axes() {
			var f Finding
			var err error
			if axis == AxisMemory {
				f, err = Inferred(axis, claim, ev, model, "sha256:memory")
			} else {
				f, err = NotMeasured(axis, MissingNoNodes, "no call sites were discovered", ev)
			}
			if err != nil {
				t.Fatalf("building %s: %v", axis, err)
			}
			a.Findings = append(a.Findings, f)
		}
		return a
	}
	return build("as-a", revA, cfgA, modelA, "a per-session store that is never pruned"),
		build("as-b", revB, cfgB, modelB, "a per-session store pruned after twenty turns")
}

// TestVaryingOnlyTheProviderModelAttributesToTheProvider is the one that matters most.
//
// Without it, *"a provider's routine upgrade is rendered as the customer's repository getting worse,
// and nobody has any way to tell."*
func TestVaryingOnlyTheProviderModelAttributesToTheProvider(t *testing.T) {
	before, after := twoAssessments(t, "rev-1", "rev-1", "cfg-1", "cfg-1",
		"anthropic/claude-opus-5-20260501", "anthropic/claude-opus-5-20260812")
	diff, err := Diff(before, after)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diff) != 1 {
		t.Fatalf("got %d changed axes, want 1 (only memory carries a model)", len(diff))
	}
	d := diff[0]
	if d.Cause != CauseProviderModel {
		t.Fatalf("the delta is attributed to %q, want %q. The revision and the config are identical, so "+
			"the only thing that moved is the provider — and telling a customer their repository got "+
			"worse is the specific harm design D7 exists to prevent", d.Cause, CauseProviderModel)
	}
	if !strings.Contains(d.Why, "Neither your repository nor our") {
		t.Fatalf("the explanation does not tell the customer their repository is unchanged: %q", d.Why)
	}
}

// TestVaryingOnlyTheAgentConfigAttributesToUs — we changed how we ask.
func TestVaryingOnlyTheAgentConfigAttributesToUs(t *testing.T) {
	before, after := twoAssessments(t, "rev-1", "rev-1", "cfg-1", "cfg-2", "m-1", "m-1")
	diff, err := Diff(before, after)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diff) != 1 || diff[0].Cause != CauseAgentConfig {
		t.Fatalf("got %+v, want one delta attributed to %q", diff, CauseAgentConfig)
	}
	if !strings.Contains(diff[0].Why, "Your repository did not change") {
		t.Fatalf("the explanation does not exonerate the repository: %q", diff[0].Why)
	}
}

// TestVaryingOnlyTheRevisionAttributesToTheCustomer — the only cause that is a finding about them.
func TestVaryingOnlyTheRevisionAttributesToTheCustomer(t *testing.T) {
	before, after := twoAssessments(t, "rev-1", "rev-2", "cfg-1", "cfg-1", "m-1", "m-1")
	diff, err := Diff(before, after)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diff) != 1 || diff[0].Cause != CauseSource {
		t.Fatalf("got %+v, want one delta attributed to %q", diff, CauseSource)
	}
}

// TestTheSourcePrecedenceHoldsWhenTwoInputsMove is why `attribute` has a fixed order.
//
// Attributing a change to a provider upgrade when the code ALSO changed would tell a customer their
// repository is fine when it is not — the most expensive way this can be wrong.
func TestTheSourcePrecedenceHoldsWhenTwoInputsMove(t *testing.T) {
	before, after := twoAssessments(t, "rev-1", "rev-2", "cfg-1", "cfg-1", "m-1", "m-2")
	diff, err := Diff(before, after)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff[0].Cause != CauseSource {
		t.Fatalf("with BOTH the revision and the model moved, the delta is attributed to %q. The source "+
			"must win: telling a customer their code is unchanged when it is not is the most expensive "+
			"way this can be wrong", diff[0].Cause)
	}
}

// TestAChangeWithNothingMovedIsReportedAsADefect is the alarming case, and it is deliberately not a
// leftover bucket. FR15 says two assessments of one key are identical; every occurrence of this value
// is that guarantee failing.
func TestAChangeWithNothingMovedIsReportedAsADefect(t *testing.T) {
	before, after := twoAssessments(t, "rev-1", "rev-1", "cfg-1", "cfg-1", "m-1", "m-1")
	diff, err := Diff(before, after)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diff) != 1 || diff[0].Cause != CauseUnattributable {
		t.Fatalf("got %+v, want one delta attributed to %q", diff, CauseUnattributable)
	}
	if !strings.Contains(diff[0].Why, "defect on our side") {
		t.Fatalf("an unattributable change does not name itself as our defect: %q", diff[0].Why)
	}
}

// TestAnIdenticalRerunProducesAnEmptyDiff is FR15 stated as the diff's own behaviour: a re-run that
// changed nothing renders nothing, rather than nine rows saying "unchanged".
func TestAnIdenticalRerunProducesAnEmptyDiff(t *testing.T) {
	before, _ := twoAssessments(t, "rev-1", "rev-1", "cfg-1", "cfg-1", "m-1", "m-1")
	diff, err := Diff(before, before)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(diff) != 0 {
		t.Fatalf("an identical pair produced %d rows; a diff that lists unchanged axes is a report", len(diff))
	}
}

// TestADiffAcrossWorkflowsIsRefused — two facts wearing one heading.
func TestADiffAcrossWorkflowsIsRefused(t *testing.T) {
	before, after := twoAssessments(t, "rev-1", "rev-1", "cfg-1", "cfg-1", "m-1", "m-1")
	after.WorkflowID = "wf-2"
	if _, err := Diff(before, after); err == nil {
		t.Fatal("a diff across two workflows was produced")
	}
}
