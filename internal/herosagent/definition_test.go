package herosagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/providergateway"
)

// P30 workstream 3 — the agent definition.

func goodDefinition() Definition {
	return Definition{
		PromptRef:     "prompt-v1",
		ModelRef:      "claude-opus-5",
		CredentialRef: "anthropic",
		ContextRef:    "ctx-v1",
		HarnessRef:    "harness-single-shot-v1",
	}
}

// fakeCatalogue is the operator model registry.
type fakeCatalogue struct {
	models []RegisteredModel
	err    error
}

func (f fakeCatalogue) Models(context.Context) ([]RegisteredModel, error) {
	return f.models, f.err
}

// fakeSecrets resolves a fixed set of provider names.
type fakeSecrets struct {
	known map[string]bool
	// asked records every provider a caller tried to resolve, so a test can assert ZERO attempts.
	asked *[]string
}

func (f fakeSecrets) Credential(_ context.Context, provider string) (providergateway.Credential, error) {
	if f.asked != nil {
		*f.asked = append(*f.asked, provider)
	}
	if !f.known[provider] {
		return providergateway.Credential{}, errors.New("no such secret")
	}
	return providergateway.Credential{APIKey: "not-a-real-key"}, nil
}

func (f fakeSecrets) Describe() providergateway.SourceInfo {
	return providergateway.SourceInfo{Kind: "static", Detail: "test"}
}

func testPublisher(t *testing.T, cat ModelCatalogue, sec providergateway.Secrets) (*Publisher, *MemVersionStore) {
	t.Helper()
	store := NewMemVersionStore()
	ms := int64(1_700_000_000_000)
	p, err := NewPublisher(cat, sec, store, RunnerHosts{}, func() int64 { ms++; return ms })
	if err != nil {
		t.Fatal(err)
	}
	return p, store
}

func registered(ids ...string) fakeCatalogue {
	var out []RegisteredModel
	for _, id := range ids {
		out = append(out, RegisteredModel{ModelID: id, Provider: "anthropic"})
	}
	return fakeCatalogue{models: out}
}

func anthropicOnly() fakeSecrets { return fakeSecrets{known: map[string]bool{"anthropic": true}} }

// Task 3.1 — the definition IS a Variant Spec over the six axes, hashed by internal/confighash.
func TestTheDefinitionProjectsAsAVariantSpecOverTheSixAxes(t *testing.T) {
	d := goodDefinition()
	d.SkillRefs = []string{"skill-b", "skill-a"}
	d.ToolNames = []string{"z-tool", "a-tool"}
	d.MemoryRef = "mem-v1"

	spec := d.Spec()
	node, ok := spec.Nodes[NodeID]
	if !ok {
		t.Fatalf("the spec has no %s node: %+v", NodeID, spec.Nodes)
	}
	if node.ModelRef != d.ModelRef || node.PromptRef != d.PromptRef ||
		node.ContextPolicy != d.ContextRef || node.MemoryRef != d.MemoryRef ||
		node.HarnessRef != d.HarnessRef {
		t.Errorf("an axis did not reach the node override: %+v", node)
	}
	// Skill ORDER is identity-bearing (the call site binds them in that order); tools are a SET.
	if node.SkillRefs[0] != "skill-b" {
		t.Errorf("skill order was not preserved: %v", node.SkillRefs)
	}
	if node.ToolSelection[0] != "a-tool" {
		t.Errorf("tools were not normalised to a set: %v", node.ToolSelection)
	}
}

// Content determines identity: two definitions denoting one configuration share a hash, and any axis
// change moves it.
func TestConfigHashIsContentAddressed(t *testing.T) {
	a := goodDefinition()
	a.ToolNames = []string{"one", "two"}
	b := goodDefinition()
	b.ToolNames = []string{"two", "one"} // same SET, different authoring order

	ha, err := a.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}
	hb, err := b.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Errorf("two definitions denoting one configuration have different hashes:\n  %s\n  %s", ha, hb)
	}

	// 🔴 The credential reference is part of what this agent IS. A definition pointed at another
	// provider is a different agent, and it must not share a hash.
	c := goodDefinition()
	c.ToolNames = []string{"one", "two"}
	c.CredentialRef = "openai"
	hc, err := c.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}
	if hc == ha {
		t.Error("swapping the credential reference did not change the config_hash — a definition " +
			"spending a different provider's credential would be indistinguishable from this one")
	}

	// Task 10.16 — a definition records the VERSION of the closed sets it references, and that version
	// is hashed. Otherwise `single-shot` before and after the set gains a member are one identity.
	d := goodDefinition()
	d.ToolNames = []string{"one", "two"}
	d.SetVersions = map[string]string{"harness": "2"}
	hd, err := d.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}
	if hd == ha {
		t.Error("the set version is not hashed, so a stored config_hash stops being interpretable the " +
			"moment a closed vocabulary is versioned forward")
	}
}

// 🔴 TASK 3.2 — a wiring override is refused AT PUBLISH, naming the axis.
func TestAWiringOverrideIsRefusedByName(t *testing.T) {
	_, err := DefinitionFromAxes(AxisEdit{AxisModel: "m", AxisWiring: "n1,n2"}, ListEdit{}, "anthropic")
	if err == nil {
		t.Fatal("a wiring override was accepted. HEROS is one node — accepting an ordering hashes a " +
			"configuration nothing can execute, and dropping it silently lets an operator believe they " +
			"changed something.")
	}
	if !errors.Is(err, ErrWiringOverride) {
		t.Errorf("the refusal is not ErrWiringOverride: %v", err)
	}
	if !strings.Contains(err.Error(), "wiring") {
		t.Errorf("the refusal does not NAME the axis: %v", err)
	}
}

// An axis this platform does not author is refused rather than dropped.
func TestAnUnknownAxisIsRefusedRatherThanDropped(t *testing.T) {
	_, err := DefinitionFromAxes(AxisEdit{Axis("temperature"): "0.7"}, ListEdit{}, "anthropic")
	if err == nil {
		t.Fatal("an unknown axis was silently dropped — the operator believes they set a temperature " +
			"and the published definition has none")
	}
	if !strings.Contains(err.Error(), "temperature") {
		t.Errorf("the refusal does not name the axis: %v", err)
	}
}

// Task 3.4 — an unregistered model is refused, by name, with the registered set listed.
func TestAnUnregisteredModelIsRefusedAtPublish(t *testing.T) {
	p, _ := testPublisher(t, registered("claude-opus-5"), anthropicOnly())
	d := goodDefinition()
	d.ModelRef = "gpt-9-turbo"

	_, err := p.Publish(context.Background(), d)
	if !errors.Is(err, ErrModelUnregistered) {
		t.Fatalf("an unregistered model published: %v", err)
	}
	if !strings.Contains(err.Error(), "gpt-9-turbo") || !strings.Contains(err.Error(), "claude-opus-5") {
		t.Errorf("the refusal names neither the rejected model nor what IS registered: %v", err)
	}
}

// 🚫 An UNREACHABLE registry is not "the model is fine". Publishing through an outage would record a
// definition nobody validated.
func TestAnUnreachableModelRegistryDoesNotPublish(t *testing.T) {
	p, _ := testPublisher(t, fakeCatalogue{err: errors.New("connection refused")}, anthropicOnly())
	if _, err := p.Publish(context.Background(), goodDefinition()); err == nil {
		t.Fatal("a definition published while the model registry was unreadable")
	}
}

// 🔴 TASK 3.6 — an unresolvable credential fails CLOSED: the publish is refused, and no substitute
// provider is tried.
func TestAnUnresolvableCredentialFailsClosedWithNoSubstitution(t *testing.T) {
	var asked []string
	sec := fakeSecrets{known: map[string]bool{"openai": true}, asked: &asked}
	p, store := testPublisher(t, registered("claude-opus-5"), sec)

	d := goodDefinition() // CredentialRef: anthropic, which this source does not carry
	_, err := p.Publish(context.Background(), d)
	if !errors.Is(err, ErrCredentialUnresolved) {
		t.Fatalf("an unresolvable credential published: %v", err)
	}
	// 🚫 NO SUBSTITUTION. It asked for `anthropic` and nothing else — a fallback to the provider that
	// DOES resolve would spend somebody else's credential on this analysis.
	for _, a := range asked {
		if a != "anthropic" {
			t.Errorf("a substitute provider %q was tried after the bound one failed to resolve", a)
		}
	}
	if vs, _ := store.List(context.Background()); len(vs) != 0 {
		t.Errorf("a version was recorded despite the refusal: %+v", vs)
	}
}

// Task 3.3 — no mutation API, and re-publishing an identical definition is the SAME version.
func TestPublishingAnIdenticalDefinitionCreatesNoSecondVersion(t *testing.T) {
	p, store := testPublisher(t, registered("claude-opus-5"), anthropicOnly())
	ctx := context.Background()

	first, err := p.Publish(ctx, goodDefinition())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created {
		t.Fatal("the first publish created nothing")
	}
	second, err := p.Publish(ctx, goodDefinition())
	if err != nil {
		t.Fatal(err)
	}
	if second.Created {
		t.Error("an identical definition created a SECOND version — an operator now has two identities " +
			"for one configuration to reason about")
	}
	if second.ConfigHash != first.ConfigHash {
		t.Errorf("identical definitions hashed differently: %s vs %s", first.ConfigHash, second.ConfigHash)
	}
	vs, _ := store.List(ctx)
	if len(vs) != 1 {
		t.Errorf("%d versions stored, want 1", len(vs))
	}
}

// 🔴 A published definition lands PENDING. It must never be rendered as active before the gate (D7).
func TestAPublishedDefinitionLandsPendingRehearsal(t *testing.T) {
	p, store := testPublisher(t, registered("claude-opus-5"), anthropicOnly())
	ctx := context.Background()
	res, err := p.Publish(ctx, goodDefinition())
	if err != nil {
		t.Fatal(err)
	}
	v, _, _ := store.Get(ctx, res.ConfigHash)
	if v.RehearsalState != RehearsalPending {
		t.Errorf("a fresh publish is %q, want %q", v.RehearsalState, RehearsalPending)
	}
	if v.Active() {
		t.Error("a fresh publish is ACTIVE — it has been measured by nothing")
	}
	// And it cannot be activated.
	if err := p.Activate(ctx, res.ConfigHash); !errors.Is(err, ErrRehearsalNotPassed) {
		t.Errorf("an unrehearsed definition activated: %v", err)
	}
}

// Task 3.7 — exactly one active version.
func TestExactlyOneVersionIsActive(t *testing.T) {
	p, store := testPublisher(t, registered("claude-opus-5", "claude-sonnet-5"), anthropicOnly())
	ctx := context.Background()

	var hashes []string
	for _, m := range []string{"claude-opus-5", "claude-sonnet-5"} {
		d := goodDefinition()
		d.ModelRef = m
		res, err := p.Publish(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetRehearsal(ctx, res.ConfigHash, RehearsalPassed, "{}"); err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, res.ConfigHash)
	}
	for _, h := range hashes {
		if err := p.Activate(ctx, h); err != nil {
			t.Fatalf("activating %s: %v", h, err)
		}
	}
	vs, _ := store.List(ctx)
	active := 0
	for _, v := range vs {
		if v.Active() {
			active++
		}
	}
	if active != 1 {
		t.Errorf("%d versions are active, want exactly 1 — `which definition is serving inference` is "+
			"the one question this surface must always be able to answer", active)
	}
	v, ok, _ := p.ActiveDefinition(ctx)
	if !ok || v.ConfigHash != hashes[1] {
		t.Errorf("the active version is %v (ok=%v), want the most recently activated %s",
			v.ConfigHash, ok, hashes[1])
	}
}

// Task 3.8 — a deprecated model produces a NOTICE, never a refusal and never an auto-switch.
func TestADeprecatedModelIsANoticeAndIsNeverAutoSwitched(t *testing.T) {
	cat := fakeCatalogue{models: []RegisteredModel{
		{ModelID: "claude-opus-5", Provider: "anthropic", Deprecated: true},
		{ModelID: "claude-sonnet-5", Provider: "anthropic"},
	}}
	p, store := testPublisher(t, cat, anthropicOnly())
	ctx := context.Background()

	res, err := p.Publish(ctx, goodDefinition())
	if err != nil {
		t.Fatalf("a deprecated model REFUSED the publish: %v", err)
	}
	if res.DeprecatedModel != "claude-opus-5" {
		t.Errorf("no deprecation notice was raised: %+v", res)
	}
	v, _, _ := store.Get(ctx, res.ConfigHash)
	if v.Definition.ModelRef != "claude-opus-5" {
		t.Errorf("the model was AUTO-SWITCHED to %q. Swapping it changes the config_hash, so every "+
			"stored inference would name a definition that ran something else.", v.Definition.ModelRef)
	}
}

// 🔴 TASK 3.5 — a key-shaped value is refused where a provider NAME belongs.
func TestAKeyShapedCredentialIsRefused(t *testing.T) {
	for name, value := range map[string]string{
		"a vendor-prefixed key": "sk-ant-api03-abcdefghijklmnop",
		"a long opaque secret":  strings.Repeat("a", 80),
		"a value with spaces":   "anthropic key here",
	} {
		t.Run(name, func(t *testing.T) {
			d := goodDefinition()
			d.CredentialRef = value
			err := d.Validate()
			if !errors.Is(err, ErrKeyValueOffered) {
				t.Fatalf("a key-shaped credential was accepted: %v", err)
			}
			// 🔴 The refusal must not ECHO the value — an error message is a log line.
			if strings.Contains(err.Error(), value) {
				t.Errorf("the refusal echoed the offered value into an error message: %v", err)
			}
		})
	}
	// A real provider name passes.
	if err := goodDefinition().Validate(); err != nil {
		t.Errorf("a legitimate provider name was refused: %v", err)
	}
}
