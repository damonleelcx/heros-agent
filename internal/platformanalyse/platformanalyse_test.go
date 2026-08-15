package platformanalyse

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

// platformanalyse_test.go fences the caller that did not exist.
//
// The pieces were all built: a platform-side runner, a snapshot source, a durable store, a ceiling.
// Nothing joined them, so `platform` placement started nothing. These assert the properties that make
// the join safe — not that it runs, which any smoke test would show.

type fakeDefs struct {
	def runlink.AgentDefinition
	ok  bool
	err error
	// calls counts resolutions, so a test can prove one did NOT happen.
	calls int
}

func (f *fakeDefs) ActiveDefinition(context.Context) (runlink.AgentDefinition, bool, error) {
	f.calls++
	return f.def, f.ok, f.err
}

type fakePlaces struct {
	place herosagent.Placement
	err   error
}

func (f fakePlaces) PlacementFor(context.Context, string) (herosagent.Placement, error) {
	return f.place, f.err
}

type fakeSource struct {
	ir  *discovery.IR
	err error
	// calls counts materializations. The point of several tests is that this stays ZERO.
	calls int
	// gotRef records what was asked for, so identity can be asserted.
	gotRef sourceingest.Ref
}

func (f *fakeSource) WithSource(_ context.Context, ref sourceingest.Ref, fn func(string, *discovery.IR) error) error {
	f.calls++
	f.gotRef = ref
	if f.err != nil {
		return f.err
	}
	return fn("/tmp/does-not-matter", f.ir)
}

func runnableDef() runlink.AgentDefinition {
	return runlink.AgentDefinition{
		ContractVersion: runlink.AgentDefinitionContractVersion,
		Placement:       string(herosagent.PlacementCustomer),
		ConfigHash:      "cfg-1",
		Prompt:          "you are the analyst",
		Provider:        "openai",
		ModelID:         "gpt-5",
		ConfidenceFloor: 0.8,
	}
}

func svc(t *testing.T, defs Definitions, places Placements, src Source) *Service {
	t.Helper()
	s, err := New(Config{
		Definitions: defs, Placements: places, Source: src,
		Gateway:    providergateway.New(nil),
		Inferences: herosagent.NewMemInferenceStore(),
		Floor:      0.8,
		Budget:     herosagent.Budget{MaxTokens: 100_000, MaxWall: herosagent.DefaultBudget().MaxWall},
		NowMS:      func() int64 { return 1 },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func ref() sourceingest.Ref {
	return sourceingest.Ref{TenantID: "t-1", WorkflowID: "wf-1", SourceRevision: "rev-1"}
}

// 🔴 A tenant that is not platform-placed has NOTHING materialized.
//
// The ordering is the assertion, not the refusal. `disabled` is every tenant's default, so the common
// path must not extract somebody's repository onto our disk in order to discover we were not allowed to
// look at it. A version that checked placement after materializing would still refuse, and would still
// be wrong.
func TestANonPlatformTenantHasNothingMaterialised(t *testing.T) {
	for _, place := range []herosagent.Placement{
		herosagent.PlacementDisabled,
		herosagent.PlacementCustomer,
	} {
		defs := &fakeDefs{def: runnableDef(), ok: true}
		src := &fakeSource{ir: &discovery.IR{}}
		out, err := svc(t, defs, fakePlaces{place: place}, src).Analyse(context.Background(), ref())
		if err != nil {
			t.Fatalf("%s: %v", place, err)
		}
		if out.Reason != ReasonNotPlaced {
			t.Errorf("%s: reason = %q, want %q", place, out.Reason, ReasonNotPlaced)
		}
		if src.calls != 0 {
			t.Errorf("%s: the snapshot was materialised %d time(s) for a tenant we may not analyse",
				place, src.calls)
		}
		if defs.calls != 0 {
			t.Errorf("%s: the definition was resolved before the placement was honoured", place)
		}
	}
}

// 🔴 The tenant analysed is the REF's tenant.
//
// A Ref carries a TenantID and every store keyed by tenant reads it from there. Accepting a second
// tenant beside it would let the two disagree, and the disagreement resolves as "tenant A's permission
// over tenant B's source". Asserted structurally: the signature takes no tenant to disagree with.
func TestTheTenantComesFromTheRef(t *testing.T) {
	defs := &fakeDefs{def: runnableDef(), ok: true}
	src := &fakeSource{ir: &discovery.IR{}}
	r := ref()
	if _, err := svc(t, defs, fakePlaces{place: herosagent.PlacementPlatform}, src).
		Analyse(context.Background(), r); err != nil {
		t.Fatalf("analyse: %v", err)
	}
	if src.gotRef.TenantID != r.TenantID || src.gotRef.WorkflowID != r.WorkflowID ||
		src.gotRef.SourceRevision != r.SourceRevision {
		t.Errorf("the source was asked for %+v, want %+v", src.gotRef, r)
	}
}

// An incomplete ref is refused before anything is read. Ref.Validate exists because a Ref with no
// tenant "reads as a legitimate lookup against a store keyed by tenant, and returns another tenant's
// row" — this path must not be the one that skips it.
func TestAnIncompleteRefIsRefusedBeforeAnythingIsRead(t *testing.T) {
	src := &fakeSource{ir: &discovery.IR{}}
	_, err := svc(t, &fakeDefs{def: runnableDef(), ok: true},
		fakePlaces{place: herosagent.PlacementPlatform}, src).
		Analyse(context.Background(), sourceingest.Ref{WorkflowID: "wf-1", SourceRevision: "r"})
	if err == nil {
		t.Fatal("a ref with no tenant was accepted")
	}
	if src.calls != 0 {
		t.Errorf("it materialised anyway")
	}
}

// No active definition is a REASON, not an error. An operator publishes and activates one; a 500 would
// send somebody looking for an outage that is not happening.
func TestNoActiveDefinitionIsAReasonNotAFailure(t *testing.T) {
	src := &fakeSource{ir: &discovery.IR{}}
	out, err := svc(t, &fakeDefs{ok: false}, fakePlaces{place: herosagent.PlacementPlatform}, src).
		Analyse(context.Background(), ref())
	if err != nil {
		t.Fatalf("analyse returned an error for a normal state: %v", err)
	}
	if out.Reason != ReasonNoDefinition {
		t.Errorf("reason = %q, want %q", out.Reason, ReasonNoDefinition)
	}
	if src.calls != 0 {
		t.Errorf("the snapshot was materialised with no definition to run over it")
	}
}

// A definition that is present but NOT runnable is refused too. `Runnable()` checks the fields rather
// than the placement precisely so a platform that answered with an empty prompt cannot have one
// executed — and that check has to be on this path, not only the customer's.
func TestAnUnrunnableDefinitionIsRefused(t *testing.T) {
	bad := runnableDef()
	bad.Prompt = "" // resolves, renders, and instructs the agent in nothing
	src := &fakeSource{ir: &discovery.IR{}}
	out, err := svc(t, &fakeDefs{def: bad, ok: true}, fakePlaces{place: herosagent.PlacementPlatform}, src).
		Analyse(context.Background(), ref())
	if err != nil {
		t.Fatalf("analyse: %v", err)
	}
	if out.Reason != ReasonNoDefinition {
		t.Errorf("reason = %q, want %q — an empty instruction must not reach a provider", out.Reason, ReasonNoDefinition)
	}
	if src.calls != 0 {
		t.Errorf("the snapshot was materialised for an unrunnable definition")
	}
}

// 🔴 An Anthropic definition with no pinned max_tokens is refused BEFORE the snapshot is materialised.
//
// The gateway would refuse the call anyway, but only after the repository had been extracted and
// parsed. Refusing here spends nothing, and the message names the operator's own fix rather than
// reporting a fault in a model entry.
func TestAnAnthropicDefinitionWithNoMaxTokensIsRefusedBeforeAnyWork(t *testing.T) {
	def := runnableDef()
	def.Provider, def.ModelID = "anthropic", "claude-sonnet-5"
	src := &fakeSource{ir: &discovery.IR{}}
	out, err := svc(t, &fakeDefs{def: def, ok: true}, fakePlaces{place: herosagent.PlacementPlatform}, src).
		Analyse(context.Background(), ref())
	if err != nil {
		t.Fatalf("analyse: %v", err)
	}
	if out.Reason != ReasonFailed {
		t.Fatalf("reason = %q, want %q", out.Reason, ReasonFailed)
	}
	if !strings.Contains(out.Detail, "max_tokens") {
		t.Errorf("the detail does not name max_tokens: %s", out.Detail)
	}
	if src.calls != 0 {
		t.Errorf("the repository was extracted before a call that could never have been made")
	}
	if out.ConfigHash != def.ConfigHash {
		t.Errorf("the outcome does not name the definition it refused: %q", out.ConfigHash)
	}
}

// The pinned parameters DO get through when they are there — the negative test above passes trivially
// if the mapping is broken in the other direction.
func TestPinnedParametersAreAcceptedForAnthropic(t *testing.T) {
	four := 4096
	def := runnableDef()
	def.Provider, def.ModelID = "anthropic", "claude-sonnet-5"
	def.ModelParams = &runlink.ModelParams{MaxTokens: &four}
	src := &fakeSource{ir: &discovery.IR{}}
	out, err := svc(t, &fakeDefs{def: def, ok: true}, fakePlaces{place: herosagent.PlacementPlatform}, src).
		Analyse(context.Background(), ref())
	if err != nil {
		t.Fatalf("analyse: %v", err)
	}
	if out.Reason == ReasonFailed && strings.Contains(out.Detail, "max_tokens") {
		t.Fatalf("a definition pinning max_tokens was refused for not pinning it: %s", out.Detail)
	}
	if src.calls != 1 {
		t.Errorf("the snapshot was materialised %d time(s), want 1", src.calls)
	}
}

// No pushed source is its own reason, carried through unwrapped from sourceingest.
func TestNoPushedSourceIsItsOwnReason(t *testing.T) {
	src := &fakeSource{err: sourceingest.ErrNoSource}
	out, err := svc(t, &fakeDefs{def: runnableDef(), ok: true},
		fakePlaces{place: herosagent.PlacementPlatform}, src).Analyse(context.Background(), ref())
	if err != nil {
		t.Fatalf("analyse: %v", err)
	}
	if out.Reason != ReasonNoSource {
		t.Errorf("reason = %q, want %q", out.Reason, ReasonNoSource)
	}
}

// A materialisation failure is a REASON, never a returned error — the caller is discovery, and a failed
// enrichment must not fail the graph that succeeded.
func TestAMaterialisationFailureDoesNotEscapeAsAnError(t *testing.T) {
	src := &fakeSource{err: errors.New("disk is full")}
	out, err := svc(t, &fakeDefs{def: runnableDef(), ok: true},
		fakePlaces{place: herosagent.PlacementPlatform}, src).Analyse(context.Background(), ref())
	if err != nil {
		t.Fatalf("a failed analysis escaped as an error, which would fail the discovery it enriches: %v", err)
	}
	if out.Reason != ReasonFailed {
		t.Errorf("reason = %q, want %q", out.Reason, ReasonFailed)
	}
	if out.Analysed() {
		t.Error("a failed run reports itself as analysed")
	}
}

// A durable store is REQUIRED at construction. The two pre-existing callers of herosagent.NewRunner
// both pass a memory store, which is why a platform analysis could not have been kept even if one had
// started — this refuses the shape rather than repeating it.
func TestTheServiceRefusesToBeBuiltWithoutItsCollaborators(t *testing.T) {
	base := func() Config {
		return Config{
			Definitions: &fakeDefs{}, Placements: fakePlaces{}, Source: &fakeSource{},
			Gateway: providergateway.New(nil), Inferences: herosagent.NewMemInferenceStore(),
			Budget: herosagent.DefaultBudget(), NowMS: func() int64 { return 1 },
		}
	}
	for name, mutate := range map[string]func(*Config){
		"definitions": func(c *Config) { c.Definitions = nil },
		"placements":  func(c *Config) { c.Placements = nil },
		"source":      func(c *Config) { c.Source = nil },
		"gateway":     func(c *Config) { c.Gateway = nil },
		"inferences":  func(c *Config) { c.Inferences = nil },
		"clock":       func(c *Config) { c.NowMS = nil },
	} {
		cfg := base()
		mutate(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("a service was built with no %s", name)
		}
	}
	// And an unbounded budget is refused: a zero is not "unlimited", it is the version of unlimited
	// nobody chose.
	cfg := base()
	cfg.Budget = herosagent.Budget{}
	if _, err := New(cfg); err == nil {
		t.Error("a service was built with no budget ceiling")
	}
}
