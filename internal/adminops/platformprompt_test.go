package adminops_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/registry"
)

// platformprompt_test.go covers the authoring path for the PLATFORM's own agent instruction.
//
// The gap it closes: `Publisher.Publish` refuses a definition whose prompt_ref does not resolve, and
// the only write route into the prompt registry namespaces by TENANT. So the operator console could
// compose a definition and never publish one — and nothing failed, because no test asked where an
// operator gets a prompt_ref from.

// recordingPrompts records what was published and can serve a prior timeline.
type recordingPrompts struct {
	names  []string
	bodies []string
	// existing maps a stored name to the version ids already published under it.
	existing map[string][]string
	// nextID is returned by RegisterPrompt; content-addressing is modelled by the test choosing it.
	nextID      string
	timelineErr error
	registerErr error
}

func (p *recordingPrompts) RegisterPrompt(_ context.Context, name, body string) (string, error) {
	if p.registerErr != nil {
		return "", p.registerErr
	}
	p.names = append(p.names, name)
	p.bodies = append(p.bodies, body)
	return p.nextID, nil
}

func (p *recordingPrompts) PromptTimeline(_ context.Context, name string) ([]registry.PromptTimelineEntry, error) {
	if p.timelineErr != nil {
		return nil, p.timelineErr
	}
	out := []registry.PromptTimelineEntry{}
	for _, v := range p.existing[name] {
		out = append(out, registry.PromptTimelineEntry{Name: name, VersionID: v})
	}
	return out, nil
}

func promptServiceFor(t *testing.T, h *harness, p adminops.PlatformPromptRegistrar) *adminops.AgentService {
	t.Helper()
	svc, err := adminops.NewAgentService(h.exec, &fakeVersions{}, &fakePublisher{}, nil, nil, nil,
		herosagent.RunnerHosts{})
	if err != nil {
		t.Fatalf("NewAgentService: %v", err)
	}
	if p != nil {
		svc = svc.WithPlatformPrompts(p)
	}
	return svc
}

// 🔴 THE PROPERTY, not the spelling.
//
// Isolation from tenant prompts rests entirely on the stored name being disjoint from `t:<tenant>/…`
// for EVERY possible tenant id. Asserting the literal "platform/" would pass if somebody changed the
// prefix to "t:platform/" — which is exactly the mistake that would silently put the platform's own
// instruction inside a tenant namespace called "platform".
func TestAPlatformPromptCanNeverCollideWithATenants(t *testing.T) {
	h := newHarness(t)
	p := &recordingPrompts{nextID: "v1"}
	svc := promptServiceFor(t, h, p)

	if _, err := svc.PublishPlatformPrompt(h.ctx(adminrbac.RoleSuperadmin), "agent-instruction", "you are an analyst"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(p.names) != 1 {
		t.Fatalf("published %d prompts, want 1", len(p.names))
	}
	stored := p.names[0]
	if strings.HasPrefix(stored, "t:") {
		t.Fatalf("the platform prompt stored as %q, which is inside the TENANT namespace — a tenant "+
			"named in that prefix could collide with, or enumerate, the platform's own instruction", stored)
	}
	// And it must still carry the operator's name, or two platform prompts would overwrite each other.
	if !strings.Contains(stored, "agent-instruction") {
		t.Errorf("the stored name %q does not contain the operator's name", stored)
	}
}

// The body reaches the registry unmodified. A surface that trimmed or normalised the instruction would
// change what the agent is told without telling anybody — and the config_hash would record the change
// as if the operator had made it.
func TestTheInstructionReachesTheRegistryVerbatim(t *testing.T) {
	h := newHarness(t)
	p := &recordingPrompts{nextID: "v1"}
	svc := promptServiceFor(t, h, p)

	body := "  You are HEROS.\n\n  Emit only edges you can justify.\n"
	if _, err := svc.PublishPlatformPrompt(h.ctx(adminrbac.RoleSuperadmin), "agent-instruction", body); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if p.bodies[0] != body {
		t.Errorf("the instruction was modified before storage:\n got %q\nwant %q", p.bodies[0], body)
	}
}

// 🔴 A re-publish of identical text is reported as NO CHANGE.
//
// Content-addressing makes it a no-op in the registry. Reporting it as a publish would tell an operator
// they had edited the platform's instruction when they had not — and the next thing they would do is
// look for the new version in a timeline that does not contain one.
func TestRepublishingIdenticalTextIsReportedAsNoChange(t *testing.T) {
	h := newHarness(t)
	p := &recordingPrompts{
		nextID:   "v-same",
		existing: map[string][]string{"platform/agent-instruction": {"v-same"}},
	}
	svc := promptServiceFor(t, h, p)

	res, err := svc.PublishPlatformPrompt(h.ctx(adminrbac.RoleSuperadmin), "agent-instruction", "unchanged")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if res.Created {
		t.Error("a re-publish of identical text was reported as a new version")
	}
	if res.VersionID != "v-same" {
		t.Errorf("version_id = %q, want the id that already existed", res.VersionID)
	}
}

func TestANewBodyIsReportedAsCreated(t *testing.T) {
	h := newHarness(t)
	p := &recordingPrompts{
		nextID:   "v-new",
		existing: map[string][]string{"platform/agent-instruction": {"v-old"}},
	}
	svc := promptServiceFor(t, h, p)

	res, err := svc.PublishPlatformPrompt(h.ctx(adminrbac.RoleSuperadmin), "agent-instruction", "edited")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !res.Created {
		t.Error("a genuinely new version was reported as no change")
	}
}

// The name the surface hands back is the OPERATOR's, without the isolation prefix. The prefix is a
// storage mechanism; echoing it back would teach operators to type it, and a name typed with the prefix
// would then be stored doubly prefixed.
func TestTheOperatorFacingNameCarriesNoStoragePrefix(t *testing.T) {
	h := newHarness(t)
	svc := promptServiceFor(t, h, &recordingPrompts{nextID: "v1"})
	res, err := svc.PublishPlatformPrompt(h.ctx(adminrbac.RoleSuperadmin), "agent-instruction", "body")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if res.Name != "agent-instruction" {
		t.Errorf("Name = %q, want the operator's own name", res.Name)
	}
}

// A name aimed at the tenant namespace is REFUSED, not rewritten. Sanitising it would publish under a
// name the operator did not choose and hand back a ref for it.
func TestANameAimedAtTheTenantNamespaceIsRefused(t *testing.T) {
	h := newHarness(t)
	p := &recordingPrompts{nextID: "v1"}
	svc := promptServiceFor(t, h, p)

	_, err := svc.PublishPlatformPrompt(h.ctx(adminrbac.RoleSuperadmin), "t:acme/instruction", "body")
	if err == nil {
		t.Fatal("a name inside the tenant namespace was accepted")
	}
	if len(p.names) != 0 {
		t.Errorf("it was published anyway as %v", p.names)
	}
}

// An empty instruction is refused. It would resolve and render cleanly, so nothing downstream would
// catch it — the agent would simply run with no instruction and the only symptom would be a rehearsal
// that scores badly for no stated reason.
func TestAnEmptyInstructionIsRefused(t *testing.T) {
	h := newHarness(t)
	p := &recordingPrompts{nextID: "v1"}
	svc := promptServiceFor(t, h, p)

	if _, err := svc.PublishPlatformPrompt(h.ctx(adminrbac.RoleSuperadmin), "agent-instruction", "   \n "); err == nil {
		t.Fatal("an empty instruction was published")
	}
	if len(p.names) != 0 {
		t.Errorf("it reached the registry anyway: %v", p.names)
	}
}

// A deployment with no prompt registry REFUSES and says which dependency is missing. Before this
// surface existed the console simply had no control; the failure mode to avoid now is a control that
// answers with something an operator cannot act on.
func TestWithoutARegistryTheSurfaceRefusesAndNamesTheDependency(t *testing.T) {
	h := newHarness(t)
	svc := promptServiceFor(t, h, nil)

	_, err := svc.PublishPlatformPrompt(h.ctx(adminrbac.RoleSuperadmin), "agent-instruction", "body")
	if err == nil {
		t.Fatal("a deployment with no prompt registry published a prompt")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("the refusal does not name the missing dependency: %v", err)
	}
}

// Authoring the platform's instruction requires the agent-admin capability — it IS what the platform
// infers with. A reader must not be able to change it.
func TestAuthoringThePlatformPromptRequiresAgentAdmin(t *testing.T) {
	h := newHarness(t)
	p := &recordingPrompts{nextID: "v1"}
	svc := promptServiceFor(t, h, p)

	if _, err := svc.PublishPlatformPrompt(h.ctx(adminrbac.RoleSupport), "agent-instruction", "body"); err == nil {
		t.Fatal("a read-only operator authored the platform agent's instruction")
	}
	if len(p.names) != 0 {
		t.Errorf("it was published anyway: %v", p.names)
	}
}

// A timeline read that fails must not lose the publish. The operator wants the ref; "I could not tell
// you whether this was new" is a smaller loss than refusing to publish at all.
func TestATimelineFailureDoesNotLoseThePublish(t *testing.T) {
	h := newHarness(t)
	p := &recordingPrompts{nextID: "v1", timelineErr: errors.New("postgres is down")}
	svc := promptServiceFor(t, h, p)

	res, err := svc.PublishPlatformPrompt(h.ctx(adminrbac.RoleSuperadmin), "agent-instruction", "body")
	if err != nil {
		t.Fatalf("a failed timeline read refused the publish: %v", err)
	}
	if res.VersionID != "v1" {
		t.Errorf("version_id = %q, want v1", res.VersionID)
	}
}
