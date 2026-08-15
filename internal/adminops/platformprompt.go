package adminops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/registry"
)

// platformprompt.go is the operator's authoring path for the PLATFORM's own agent instruction.
//
// # Why this surface has to exist at all
//
// `herosagent.Definition.PromptRef` is a prompt-registry version id, and `Publisher.Publish` refuses a
// definition whose ref does not resolve — correctly, because a definition that cannot render its
// instruction cannot be measured or served. So publishing an agent definition requires a published
// prompt version FIRST.
//
// The registry had exactly one write route, `POST /api/v1/prompts/publish`, and it is TENANT-scoped by
// construction: it stores under `t:<tenant>/<name>` so that two tenants publishing byte-identical text
// get different version ids and neither can enumerate the other's. That is right for tenant prompts and
// wrong for this one. The platform is not a tenant. Publishing its own instruction inside some
// customer's namespace would make the platform's behaviour a property of a tenant row — deletable with
// that tenant, and confusing to anybody auditing whose prompt is whose.
//
// # Why a name prefix is enough to keep the two apart
//
// Scope in this registry is NAME, not a column: `version_id = sha256({kind,name,spec})`. Tenant names
// are always written `t:<tenant>/<name>`, so any name that cannot begin with `t:` is disjoint from
// every tenant's namespace for every possible tenant id — no coordination, no reserved-word list, and
// no new table. `platformPromptPrefix` is that name, and `TestAPlatformPromptCanNeverCollideWithATenants`
// holds the property rather than the spelling.
//
// # Why it is idempotent for free
//
// `RegisterPrompt` is content-addressed: publishing the same body twice returns the same version id and
// writes nothing. So an operator who presses publish twice does not mint a second version, and the
// surface can say which of the two happened rather than implying an edit that did not occur.

// platformPromptPrefix namespaces the platform's own prompts away from every tenant's.
//
// 🔴 It must never begin with `t:`. That is the whole isolation argument — see the fence.
const platformPromptPrefix = "platform/"

// PlatformPromptRegistrar is the registry access this surface needs: publish, and read back the
// versions of ONE name. Deliberately not the whole store — nothing here can delete, and the timeline
// read is scoped to a name this package builds, so it cannot reach a tenant's.
type PlatformPromptRegistrar interface {
	RegisterPrompt(ctx context.Context, name, body string) (string, error)
	PromptTimeline(ctx context.Context, name string) ([]registry.PromptTimelineEntry, error)
}

// PromptPublishResult is what the console renders after a publish.
type PromptPublishResult struct {
	// VersionID is the value that goes in a definition's prompt_ref. The whole point of the surface.
	VersionID string `json:"version_id"`
	// Name is the operator-facing name, WITHOUT the platform prefix — the prefix is an isolation
	// mechanism, not something to make an operator retype.
	Name string `json:"name"`
	// Created distinguishes a new version from a re-publish of identical text. Content-addressing makes
	// the second a no-op, and reporting it as a publish would imply an edit that did not happen.
	Created bool `json:"created"`
}

// PublishPlatformPrompt publishes the platform agent's instruction and returns the ref a definition
// binds.
//
// Requires the same `agent.admin` capability as publishing a definition: this text IS what the platform
// infers with, and the only reason it is a separate call is that a ref must exist before a definition
// can name it.
//
// 🚫 No reason string is required, unlike Publish. Publishing a prompt version changes NOTHING on its
// own — it mints a ref that must then be bound into a definition and pass the activation gate before it
// affects any customer. Demanding a justification for an inert act trains operators to type one, which
// is exactly how the reason on the act that DOES change something becomes noise. It is still audited.
func (s *AgentService) PublishPlatformPrompt(ctx context.Context, name, body string) (PromptPublishResult, error) {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapAgentAdmin, TargetGlobal)
	if err != nil {
		return PromptPublishResult{}, err
	}
	if s.prompts == nil {
		return PromptPublishResult{}, errors.New("adminops: this deployment mounts no prompt registry, " +
			"so the platform agent's instruction cannot be authored here — it needs a platform database " +
			"(DATABASE_URL), which is the same dependency the definition list has")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return PromptPublishResult{}, errors.New("adminops: a prompt needs a name — it is how an " +
			"operator finds this instruction again, and how its versions line up into a timeline")
	}
	// 🔴 Refused rather than sanitised. An operator who types a name starting with `t:` is either
	// confused about which surface they are on or trying to write into a tenant's namespace; quietly
	// rewriting it would publish under a name they did not choose and hand back a ref for it.
	if strings.HasPrefix(name, "t:") {
		return PromptPublishResult{}, fmt.Errorf("adminops: a platform prompt name may not begin with "+
			"%q — that prefix is how tenant-scoped prompts are stored, and this surface publishes the "+
			"platform's own instruction, which belongs to no tenant", "t:")
	}
	if strings.TrimSpace(body) == "" {
		// An empty instruction resolves and renders, so nothing downstream would refuse it: the agent
		// would simply run with no instruction and the failure would surface as bad measurements.
		return PromptPublishResult{}, errors.New("adminops: the instruction is empty — an empty prompt " +
			"publishes and resolves cleanly, so the agent would run with no instruction and the only " +
			"symptom would be a rehearsal that scores badly for no stated reason")
	}

	stored := platformPromptPrefix + name

	// Read the existing versions BEFORE publishing, so `Created` is derived from the registry rather
	// than remembered in this process. An in-process memo would answer wrongly after any restart and
	// would differ between replicas — and the thing it reports on is content-addressed, so the registry
	// already knows the answer.
	//
	// A failure here is NOT fatal to the publish: the operator wants the ref, and "I could not tell you
	// whether this was new" is a smaller loss than refusing to publish. It downgrades to reporting the
	// version as created, which is the claim the ref itself already implies.
	before := map[string]bool{}
	if prior, terr := s.prompts.PromptTimeline(ctx, stored); terr == nil {
		for _, e := range prior {
			before[e.VersionID] = true
		}
	}

	versionID, err := s.prompts.RegisterPrompt(ctx, stored, body)
	if err != nil {
		return PromptPublishResult{}, fmt.Errorf("adminops: publishing the platform prompt: %w", err)
	}
	created := !before[versionID]

	if _, aerr := s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID: sess.AdminID, Target: TargetGlobal, Action: adminaudit.ActionCrossTenantView,
		Reason: "authored the platform agent instruction", Result: "published",
		// 🚫 The BODY is never evidence. An audit row is read by more people than the prompt is, and the
		// instruction is the platform's own intellectual property; the ref identifies it exactly.
		Evidence:  map[string]string{"prompt_ref": versionID, "prompt_name": name},
		CreatedAt: s.exec.Now(),
	}); aerr != nil {
		return PromptPublishResult{VersionID: versionID, Name: name, Created: created},
			errors.New("adminops: the prompt was published and the action could not be logged: " + aerr.Error())
	}
	return PromptPublishResult{VersionID: versionID, Name: name, Created: created}, nil
}
