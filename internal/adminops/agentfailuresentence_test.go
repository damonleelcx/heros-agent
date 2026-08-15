package adminops_test

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/herosagent"
)

// agentfailuresentence_test.go defends the correction to what `/agent` SAYS when a rehearsal did not
// pass.
//
// # The defect, found in production
//
// `RehearsalFailed` carries two unrelated facts: the gate measured the definition and it scored below
// the floor, or the run never reached the model and nothing was measured at all. The overview asserted
// the FIRST for both — "ran against the pinned fixtures and did not meet the floor on every one", and
// "the per-fixture report below names which failed and by how much".
//
// On the first real activation on admin.heros-agent.space the provider account had no credits, every
// attempt returned `429 insufficient_quota`, and the console reported a definition that had been
// measured and scored badly. It had run against nothing. An operator reading that goes looking for
// per-fixture scores that do not exist and concludes the model is bad, when the answer is a billing
// page.
//
// This is the same rule the file already enforces elsewhere turned on itself: `unpriced` must not
// render as `0` because an absence is not a measurement. Neither is an unreachable provider.

// overviewFor builds the operator overview for one version row in a chosen rehearsal state.
func overviewFor(t *testing.T, state herosagent.RehearsalState, report string) adminops.AgentOverview {
	t.Helper()
	h := newHarness(t)
	v := herosagent.Version{
		ConfigHash:      strings.Repeat("a", 64),
		ModelRef:        "model-version-id",
		CredentialRef:   "openai",
		RehearsalState:  state,
		RehearsalReport: report,
	}
	svc := agentServiceFor(t, h, v, &fakePublisher{}, nil)
	view, err := svc.Overview(h.ctx(adminrbac.RoleSuperadmin))
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	return view
}

// 🔴 THE PRODUCTION CASE. A run that never reached the model must not be described as a measurement.
func TestAnUnreachableProviderIsNotReportedAsAMeasuredFailure(t *testing.T) {
	// Verbatim shape of what GateActivation stores when the run itself errors.
	const report = `{"error":"herosagent: fixture \"go_chain\" (go) could not be analysed: ` +
		`providergateway: provider unavailable after retries: after 4 attempts: openai returned ` +
		`429 Too Many Requests: insufficient_quota"}`

	view := overviewFor(t, herosagent.RehearsalFailed, report)

	// The state itself is unchanged — the rehearsal DID fail, and `rehearsal_failed` is the closed-set
	// value the console switches on. Only the claim about what was measured was wrong.
	if view.State != "rehearsal_failed" {
		t.Fatalf("state = %q, want rehearsal_failed — the closed set the console switches on must not "+
			"change; the defect was the sentence, not the state", view.State)
	}

	// 🔴 The four claims that must NOT be made about a run that never happened. Each is a phrase from
	// the sentence this test exists to have deleted.
	for _, forbidden := range []string{
		"ran against the pinned fixtures",
		"did not meet the floor",
		"names which failed and by how much",
		"per-fixture report below names",
	} {
		if strings.Contains(view.Sentence, forbidden) {
			t.Errorf("the overview claims %q for a rehearsal that never reached the model.\n"+
				"Nothing was measured: there are no per-fixture scores, and the stored report is a "+
				"provider error. An operator sent to read scores that do not exist blames the model "+
				"for a billing failure.\nsentence: %s", forbidden, view.Sentence)
		}
	}

	// And it must say, positively, that nothing was measured — an absence of the wrong claim is not
	// the same as making the right one.
	if !strings.Contains(view.Sentence, "NOT measured") {
		t.Errorf("the overview does not state that the definition was not measured.\nsentence: %s",
			view.Sentence)
	}
	if !strings.Contains(view.Sentence, "Nothing is serving inference.") {
		t.Errorf("the overview stopped saying nothing is serving.\nsentence: %s", view.Sentence)
	}
}

// The other half of the same branch: a real measured failure must STILL say so. A fix that made every
// failure read as "not measured" would trade one wrong sentence for its mirror image.
func TestAMeasuredFailureIsStillReportedAsMeasured(t *testing.T) {
	const report = `{"passed":false,"scores":[{"fixture":"go_chain","language":"go","recall":0.42}],` +
		`"failures":["go_chain: recall 0.42 below floor 0.80"]}`

	view := overviewFor(t, herosagent.RehearsalFailed, report)

	if view.State != "rehearsal_failed" {
		t.Fatalf("state = %q, want rehearsal_failed", view.State)
	}
	for _, required := range []string{"ran against the pinned fixtures", "did not meet the floor"} {
		if !strings.Contains(view.Sentence, required) {
			t.Errorf("a MEASURED failure no longer says %q — the correction for unreachable providers "+
				"must not swallow the case where the gate really did measure and refuse.\nsentence: %s",
				required, view.Sentence)
		}
	}
}

// 🚫 A missing report is an UNKNOWN, not a bad score. This is the branch that keeps the fix from
// becoming the original defect again by another route: defaulting to "it scored badly" whenever the
// report cannot be read is exactly the assumption being removed.
func TestAnUnreadableReportIsReportedAsUnknownRatherThanAsABadScore(t *testing.T) {
	for name, report := range map[string]string{
		"empty":      "",
		"whitespace": "   \n ",
		"not json":   "<html>502 Bad Gateway</html>",
	} {
		t.Run(name, func(t *testing.T) {
			view := overviewFor(t, herosagent.RehearsalFailed, report)
			for _, forbidden := range []string{"ran against the pinned fixtures", "did not meet the floor"} {
				if strings.Contains(view.Sentence, forbidden) {
					t.Errorf("a %s report is described as a measured failure (%q). Whether anything was "+
						"measured is unknown, and an unknown reported as a bad score is the defect this "+
						"file exists for.\nsentence: %s", name, forbidden, view.Sentence)
				}
			}
			if !strings.Contains(view.Sentence, "cannot be told from here") &&
				!strings.Contains(view.Sentence, "unknown") {
				t.Errorf("a %s report does not report the uncertainty.\nsentence: %s", name, view.Sentence)
			}
		})
	}
}
