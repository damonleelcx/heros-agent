package adminops_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/herosagent"
)

// agentrehearsal_test.go covers P30's activation gate as the OPERATOR reaches it.
//
// The gate itself is tested in `internal/herosagent`. What is tested here is the thing that was
// missing: that pressing activate RUNS it, that a refusal stops the activation, and that a definition
// which already passed is not re-measured — because re-measuring spends a provider call per fixture to
// reproduce a number that is already on the row.

// fakePublisher records whether Activate was reached.
type fakePublisher struct {
	activated []string
	err       error
}

func (p *fakePublisher) Publish(context.Context, herosagent.Definition) (herosagent.PublishResult, error) {
	return herosagent.PublishResult{}, nil
}

func (p *fakePublisher) Activate(_ context.Context, configHash string) error {
	if p.err != nil {
		return p.err
	}
	p.activated = append(p.activated, configHash)
	return nil
}

// fakeVersions serves one version row in a state the test chooses.
type fakeVersions struct{ v herosagent.Version }

func (f *fakeVersions) Active(context.Context) (herosagent.Version, bool, error) {
	return herosagent.Version{}, false, nil
}
func (f *fakeVersions) List(context.Context) ([]herosagent.Version, error) {
	return []herosagent.Version{f.v}, nil
}
func (f *fakeVersions) Get(_ context.Context, hash string) (herosagent.Version, bool, error) {
	if hash != f.v.ConfigHash {
		return herosagent.Version{}, false, nil
	}
	return f.v, true, nil
}

func agentServiceFor(t *testing.T, h *harness, v herosagent.Version, pub *fakePublisher,
	rehearse adminops.RehearseFunc) *adminops.AgentService {
	t.Helper()
	svc, err := adminops.NewAgentService(h.exec, &fakeVersions{v: v}, pub, nil, nil, nil,
		herosagent.RunnerHosts{})
	if err != nil {
		t.Fatalf("NewAgentService: %v", err)
	}
	if rehearse != nil {
		svc = svc.WithRehearsal(rehearse)
	}
	return svc
}

// 🔴 A FAILING REHEARSAL STOPS THE ACTIVATION, and the publisher is never reached.
//
// This is the assertion that would have caught the shipped state from the other side: before the gate
// was wired, `Activate` went straight to the publisher, which refused on the stored `pending` state.
// That refusal was correct and it was not a measurement — so an operator could not tell "this
// definition failed the calibration set" from "nothing has ever measured it".
func TestAFailingRehearsalRefusesActivationAndNeverReachesThePublisher(t *testing.T) {
	h := newHarness(t)
	pub := &fakePublisher{}
	v := herosagent.Version{ConfigHash: "cfg-failing", RehearsalState: herosagent.RehearsalPending}

	var ran int
	svc := agentServiceFor(t, h, v, pub, func(context.Context, string) error {
		ran++
		return errors.New("herosagent: py_independent_calls (python): precision 0.00 (floor 0.90)")
	})

	err := svc.Activate(h.ctx(adminrbac.RoleSuperadmin), "cfg-failing", "activating for the test")
	if err == nil {
		t.Fatal("a definition whose rehearsal FAILED was activated")
	}
	if !strings.Contains(err.Error(), "py_independent_calls") {
		t.Errorf("the refusal does not name the failing fixture, so an operator cannot act on it: %v", err)
	}
	if ran != 1 {
		t.Errorf("the rehearsal ran %d times, want 1", ran)
	}
	if len(pub.activated) != 0 {
		t.Errorf("the publisher was reached anyway: %v — the gate is decoration if the activation "+
			"proceeds past a failed measurement", pub.activated)
	}
}

// A passing rehearsal lets the activation through. The positive case matters: a gate that only ever
// refuses is indistinguishable from one that is broken.
func TestAPassingRehearsalActivates(t *testing.T) {
	h := newHarness(t)
	pub := &fakePublisher{}
	v := herosagent.Version{ConfigHash: "cfg-good", RehearsalState: herosagent.RehearsalPending}
	svc := agentServiceFor(t, h, v, pub, func(context.Context, string) error { return nil })

	if err := svc.Activate(h.ctx(adminrbac.RoleSuperadmin), "cfg-good", "measured and activating"); err != nil {
		t.Fatalf("a definition whose rehearsal PASSED was refused: %v", err)
	}
	if len(pub.activated) != 1 || pub.activated[0] != "cfg-good" {
		t.Errorf("activated = %v, want [cfg-good]", pub.activated)
	}
}

// 🔴 A definition that already PASSED is not measured again.
//
// Each rehearsal is one provider call per calibration fixture, on the deployment's own credential. Two
// runs of one `config_hash` differ by noise rather than signal (D2), so re-measuring spends money to
// produce a number that is already stored — and would overwrite a stored report with a noisier one.
func TestAnAlreadyPassedDefinitionIsNotRemeasured(t *testing.T) {
	h := newHarness(t)
	pub := &fakePublisher{}
	v := herosagent.Version{ConfigHash: "cfg-passed", RehearsalState: herosagent.RehearsalPassed}

	var ran int
	svc := agentServiceFor(t, h, v, pub, func(context.Context, string) error {
		ran++
		return nil
	})
	if err := svc.Activate(h.ctx(adminrbac.RoleSuperadmin), "cfg-passed", "activating a measured definition"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if ran != 0 {
		t.Errorf("the rehearsal ran %d time(s) for a definition that had already passed — that is one "+
			"provider call per fixture, spent to reproduce a stored number", ran)
	}
	if len(pub.activated) != 1 {
		t.Errorf("activated = %v, want one", pub.activated)
	}
}

// 🚫 A deployment with NO gate does not activate freely. It is the publisher that refuses, and this
// asserts the absence closes the door rather than opening it.
func TestWithoutAGateTheProcessStillRefusesAnUnmeasuredDefinition(t *testing.T) {
	h := newHarness(t)
	pub := &fakePublisher{err: herosagent.ErrRehearsalNotPassed}
	v := herosagent.Version{ConfigHash: "cfg-unmeasured", RehearsalState: herosagent.RehearsalPending}
	svc := agentServiceFor(t, h, v, pub, nil) // no rehearsal wired

	err := svc.Activate(h.ctx(adminrbac.RoleSuperadmin), "cfg-unmeasured", "trying without a gate")
	if err == nil {
		t.Fatal("a deployment with no rehearsal gate ACTIVATED an unmeasured definition. The absence " +
			"of a way to measure must make activation harder, never automatic")
	}
	if !errors.Is(err, herosagent.ErrRehearsalNotPassed) {
		t.Errorf("refused for the wrong reason: %v", err)
	}
}

// The gate reads the version row before it spends anything: a hash nobody published is refused without
// a provider call.
func TestActivatingAnUnpublishedHashSpendsNothing(t *testing.T) {
	h := newHarness(t)
	pub := &fakePublisher{}
	v := herosagent.Version{ConfigHash: "cfg-real", RehearsalState: herosagent.RehearsalPending}

	var ran int
	svc := agentServiceFor(t, h, v, pub, func(context.Context, string) error {
		ran++
		return nil
	})
	err := svc.Activate(h.ctx(adminrbac.RoleSuperadmin), "cfg-typo", "activating a hash nobody published")
	if err == nil {
		t.Fatal("activating an unpublished config_hash succeeded")
	}
	if ran != 0 {
		t.Errorf("the rehearsal ran for a hash with no version row — %d provider run(s) spent on "+
			"nothing", ran)
	}
}
