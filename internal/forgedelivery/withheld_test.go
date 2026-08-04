package forgedelivery_test

import (
	"context"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	"github.com/heros-foreal/agentd/internal/entitlement"
	fd "github.com/heros-foreal/agentd/internal/forgedelivery"
	"github.com/heros-foreal/agentd/internal/verification"
)

// withheld_test.go pins the property this file's subject exists for: a verified proposal that is not
// served is REPORTED, with a named reason, rather than dropped into an empty array.

func githubRoute() *fd.Route {
	return &fd.Route{Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub,
		Target: fd.Target{Owner: "o", Repo: "r", Base: "main"}}
}

func pendingProposal(id string) fd.Proposal {
	return fd.Proposal{
		ProposalID: id, ConfigHash: "ch1", SourceRevision: "rev1",
		Title: "Optimize the extraction node", DiffPatch: "--- a\n+++ b\n",
		Level: entitlement.LevelAssisted,
	}
}

// passingGate clears the one proposal these tests deliver, so a withholding under test is the ONLY
// reason anything is withheld.
func passingGate() *fakeGate {
	return &fakeGate{verdicts: map[string]verification.Verdict{"ch1|rev1": passingVerdict("ch1", "rev1")}}
}

// service builds a Service whose deliverer is wired from the caller's gate/entitlement/halt.
func service(t *testing.T, r *fd.Route, gate fd.GateOracle, ents fd.Entitlements, halt fd.HaltReaderFunc, props ...fd.Proposal) *fd.Service {
	t.Helper()
	del := fd.NewDeliverer(gate, ents, halt, deliveryrecord.NewMemStore(), 3)
	return fd.NewService(del, &stubRoutes{route: r}, &stubPending{proposals: props}, "https://c.example")
}

// 🔴 THE ONE THAT MATTERS. A route naming a declared-but-unimplemented forge used to produce an empty
// list — indistinguishable from "nothing to deliver". Migration 0026's CHECK admits gitlab on purpose,
// so this is a configuration a customer can legitimately reach, and it must say so.
func TestAnUnimplementedForgeIsReportedNotSilent(t *testing.T) {
	gitlab := &fd.Route{Mode: fd.ModeCI, ForgeKind: fd.ForgeGitLab,
		Target: fd.Target{Owner: "o", Repo: "r", Base: "main"}}
	svc := service(t, gitlab, &fakeGate{}, &fakeEnts{deliver: true}, okHalt(), pendingProposal("p1"))

	prepared, withheld, err := svc.Pending(context.Background(), "t1", gitlab.Target)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(prepared) != 0 {
		t.Fatalf("a gitlab route delivered something: %+v", prepared)
	}
	if len(withheld) != 1 {
		t.Fatalf("the proposal vanished instead of being reported: %+v — an empty list here reads as "+
			"'nothing to deliver', which is exactly the silence this reports", withheld)
	}
	w := withheld[0]
	if w.Kind != fd.WithheldForgeNotImplemented {
		t.Errorf("kind = %q, want %q", w.Kind, fd.WithheldForgeNotImplemented)
	}
	if w.ProposalID != "p1" {
		t.Errorf("the withheld entry does not name its proposal: %+v", w)
	}
	if w.NextAction == "" || w.Detail == "" {
		t.Errorf("a reported condition must carry a detail and a next action: %+v", w)
	}
}

// Every proposal lands in exactly one list. Nothing is absent from both.
func TestEveryProposalIsPreparedOrReported(t *testing.T) {
	svc := service(t, githubRoute(),
		&fakeGate{}, // no verdict for anything → every proposal fails the gate
		&fakeEnts{deliver: true}, okHalt(),
		pendingProposal("p1"), pendingProposal("p2"), pendingProposal("p3"))

	prepared, withheld, err := svc.Pending(context.Background(), "t1", githubRoute().Target)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if got := len(prepared) + len(withheld); got != 3 {
		t.Fatalf("3 proposals in, %d accounted for (%d prepared, %d withheld) — the rest were dropped",
			got, len(prepared), len(withheld))
	}
	for _, w := range withheld {
		if w.Kind != fd.WithheldNotVerified {
			t.Errorf("an unverified proposal reported as %q", w.Kind)
		}
		if w.Detail == "" {
			t.Errorf("a withheld entry with no detail: %+v", w)
		}
	}
}

// An entitlement refusal carries the platform's own words about which plan lifts it — that is an action
// the customer can take, and it was previously invisible.
func TestNotEntitledCarriesTheUpgradePath(t *testing.T) {
	// fakeEnts denies with a named reason and names Team as the plan that lifts it — the same shape the
	// real gate produces (entitlement.Decision requires a reason on every denial).
	svc := service(t, githubRoute(), passingGate(), &fakeEnts{deliver: false}, okHalt(), pendingProposal("p1"))

	_, withheld, err := svc.Pending(context.Background(), "t1", githubRoute().Target)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(withheld) != 1 || withheld[0].Kind != fd.WithheldNotEntitled {
		t.Fatalf("withheld = %+v", withheld)
	}
	w := withheld[0]
	if w.Entitlement == nil || w.Entitlement.UpgradePlanName != "Team" {
		t.Errorf("the upgrade path did not survive: %+v", w.Entitlement)
	}
	if !strings.Contains(w.NextAction, "Team") {
		t.Errorf("the next action must name the plan that lifts it, got %q", w.NextAction)
	}
}

// 🔴 The operator's halt note is NOT echoed to the CI fetch. It is written for internal readers during
// an incident, and this response lands in a customer's build log.
func TestTheOperatorsHaltNoteDoesNotReachTheCustomer(t *testing.T) {
	const internalNote = "paused: incident INC-4412, suspected credential compromise at acme-corp"
	halted := fd.HaltReaderFunc(func(string) (bool, string, error) { return true, internalNote, nil })
	svc := service(t, githubRoute(), passingGate(), &fakeEnts{deliver: true}, halted, pendingProposal("p1"))

	_, withheld, err := svc.Pending(context.Background(), "t1", githubRoute().Target)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(withheld) != 1 || withheld[0].Kind != fd.WithheldHalted {
		t.Fatalf("withheld = %+v", withheld)
	}
	w := withheld[0]
	for _, leak := range []string{"INC-4412", "credential compromise", "acme-corp", internalNote} {
		if strings.Contains(w.Detail, leak) || strings.Contains(w.NextAction, leak) {
			t.Errorf("the operator's halt note reached the customer's CI (%q): %+v", leak, w)
		}
	}
	if w.Detail == "" {
		t.Error("refusing to echo the note is not the same as saying nothing")
	}
}

// An internal read failure is reported as UNAVAILABLE, never as a verdict about the change. Reporting an
// unreadable halt state as "not verified" would tell a customer their change was measured and rejected
// on a day the database was merely unreachable.
func TestAFailClosedReadIsNotAVerdict(t *testing.T) {
	unreadable := fd.HaltReaderFunc(func(string) (bool, string, error) {
		return false, "", context.DeadlineExceeded
	})
	svc := service(t, githubRoute(), passingGate(), &fakeEnts{deliver: true}, unreadable, pendingProposal("p1"))

	_, withheld, err := svc.Pending(context.Background(), "t1", githubRoute().Target)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(withheld) != 1 {
		t.Fatalf("withheld = %+v", withheld)
	}
	w := withheld[0]
	if w.Kind != fd.WithheldUnavailable {
		t.Errorf("kind = %q, want %q — an unreadable dependency is not a judgement about the change",
			w.Kind, fd.WithheldUnavailable)
	}
	// And the internal error's text is not the customer's to read.
	if strings.Contains(strings.ToLower(w.Detail), "deadline") ||
		strings.Contains(strings.ToLower(w.Detail), "context") {
		t.Errorf("the internal error leaked into the reported detail: %q", w.Detail)
	}
}

// IsReportedCondition and the classifier must not be two lists that drift. The old switch named five
// errors and knew nothing about an unimplemented forge, so one path called it a condition and the other
// a server fault.
func TestIsReportedConditionAgreesWithTheClassifier(t *testing.T) {
	conditions := []error{
		fd.ErrNoRoute, fd.ErrNotVerified, fd.ErrBoundReached,
		fd.ErrForgeNotImplemented, fd.ErrRouteInvalid,
		&fd.HaltedError{Reason: "paused"},
	}
	for _, err := range conditions {
		if !fd.IsReportedCondition(err) {
			t.Errorf("%v is a legible condition but IsReportedCondition says it is a fault", err)
		}
	}
	if fd.IsReportedCondition(context.DeadlineExceeded) {
		t.Error("an internal failure was reported as a legible customer-facing condition")
	}
	if fd.IsReportedCondition(nil) {
		t.Error("nil is not a condition")
	}
}

// A proposal with no compiled diff is refused BEFORE the gate, and reported by name.
//
// 🔴 Without ErrNoDiff, Prepare rendered a Prepared with an empty DiffPatch and the CI runner opened a
// pull request containing no changes — in a customer's repository, titled as an optimization, with a
// body citing a verdict. Every proposal a hosted deployment generates is `unbuilt`, so this is not an
// edge case there; it is the normal path.
func TestAProposalWithNoDiffIsRefusedAndNamed(t *testing.T) {
	p := pendingProposal("p1")
	p.DiffPatch = "" // as the platform-side generator records it: proposed, not compiled
	svc := service(t, githubRoute(), passingGate(), &fakeEnts{deliver: true}, okHalt(), p)

	prepared, withheld, err := svc.Pending(context.Background(), "t1", githubRoute().Target)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(prepared) != 0 {
		t.Fatalf("a proposal with no diff was prepared for delivery: %+v — the runner would open a "+
			"pull request with no changes in it", prepared)
	}
	if len(withheld) != 1 || withheld[0].Kind != fd.WithheldNoDiff {
		t.Fatalf("withheld = %+v, want one entry of kind %q", withheld, fd.WithheldNoDiff)
	}
	// And it is NOT reported as a verdict about the change: this proposal passed its gate.
	if withheld[0].Kind == fd.WithheldNotVerified {
		t.Error("a verified change with no diff was reported as unverified")
	}
}

// Whitespace is not a diff. A patch of blanks would pass a `!= ""` check and produce the same empty
// pull request.
func TestAWhitespaceDiffIsNotADiff(t *testing.T) {
	p := pendingProposal("p1")
	p.DiffPatch = "  \n\t\n "
	svc := service(t, githubRoute(), passingGate(), &fakeEnts{deliver: true}, okHalt(), p)

	prepared, withheld, err := svc.Pending(context.Background(), "t1", githubRoute().Target)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(prepared) != 0 || len(withheld) != 1 || withheld[0].Kind != fd.WithheldNoDiff {
		t.Fatalf("prepared=%+v withheld=%+v", prepared, withheld)
	}
}
