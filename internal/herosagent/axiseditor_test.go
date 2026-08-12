package herosagent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// P30 §6b — the six axis editors. 🔴 TASK 10.15 and 10.20: every refusal below is an AXIS-AUTHORING
// FENCE, and each is stated as "this save must not succeed" rather than as "this run must fail".
//
// The whole of D12 is that validating at RUN moves the discovery of a configuration error from the
// person who made it to the person who did not. So these are save-time, and each names the axis.

// ── 6b.1 · Prompt ───────────────────────────────────────────────────────────────────────────────

// Slots are DERIVED from the template body, never typed. A typed list eventually disagrees with the
// template it describes.
func TestPromptSlotsAreDerivedFromTheBody(t *testing.T) {
	tpl := ParsePrompt("Classify {{region}} using {{taxonomy}}. Ignore {{region}} duplicates.")
	if len(tpl.Slots) != 2 || tpl.Slots[0] != "region" || tpl.Slots[1] != "taxonomy" {
		t.Errorf("slots = %v, want [region taxonomy] — derived, deduplicated and sorted", tpl.Slots)
	}
}

// 🔴 A bound slot the template does not reference is refused BY NAME. Quiet otherwise: the prompt
// renders perfectly well and silently ignores an input somebody configured.
func TestABoundSlotMissingFromTheTemplateIsRefusedByName(t *testing.T) {
	tpl := ParsePrompt("Classify {{region}}.")
	err := ValidateBindings(tpl, []string{"region", "temperature"})
	if err == nil {
		t.Fatal("a binding for a slot the template never uses was accepted. The prompt would render " +
			"without it and nothing at run time would report the dropped input.")
	}
	if !strings.Contains(err.Error(), "temperature") {
		t.Errorf("the refusal does not name the slot: %v", err)
	}
	// And a correct binding passes, so the fence discriminates rather than merely refusing.
	if err := ValidateBindings(tpl, []string{"region"}); err != nil {
		t.Errorf("a valid binding was refused: %v", err)
	}
}

// ── 6b.4 · Skills ───────────────────────────────────────────────────────────────────────────────

func schema(s string) json.RawMessage { return json.RawMessage(s) }

// 🚫 A skill whose schema does not compile is NOT selectable, and 🚫 a REMOTE `$ref` is REJECTED
// rather than fetched.
func TestUnselectableSkillsAreRefusedWithTheirReason(t *testing.T) {
	all := []SkillEntry{
		{VersionID: "ok", ImplHandle: "h", Schema: schema(`{"type":"object"}`), SchemaCompiles: true},
		{VersionID: "no-impl", Schema: schema(`{"type":"object"}`), SchemaCompiles: true},
		{VersionID: "bad-schema", ImplHandle: "h", Schema: schema(`{`), CompileError: "unexpected EOF"},
		{VersionID: "remote", ImplHandle: "h", SchemaCompiles: true,
			Schema: schema(`{"properties":{"x":{"$ref":"https://example.com/s.json"}}}`)},
		{VersionID: "local-ref", ImplHandle: "h", SchemaCompiles: true,
			Schema: schema(`{"$defs":{"a":{"type":"string"}},"properties":{"x":{"$ref":"#/$defs/a"}}}`)},
	}
	selectable, refused := SelectableSkills(all)

	ok := map[string]bool{}
	for _, s := range selectable {
		ok[s.VersionID] = true
	}
	if !ok["ok"] || !ok["local-ref"] {
		t.Errorf("a valid skill was refused: selectable=%v", ok)
	}
	for _, want := range []string{"no-impl", "bad-schema", "remote"} {
		if ok[want] {
			t.Errorf("%q is selectable and must not be", want)
		}
		if refused[want] == "" {
			t.Errorf("%q was refused with no reason", want)
		}
	}
	if !strings.Contains(refused["remote"], "rejected rather") {
		t.Errorf("the remote-$ref refusal does not say it is rejected rather than FETCHED: %q",
			refused["remote"])
	}

	// And the save-time half: selecting one is refused.
	if err := RequireSelectableSkills([]string{"remote"}, all); !errors.Is(err, ErrInvalidDefinition) {
		t.Errorf("selecting a remote-$ref skill was accepted at save: %v", err)
	}
}

// 🔴 The remote-ref detector must not trip on a schema that merely MENTIONS `$ref` in a string. A fence
// that trips on prose is a fence somebody deletes.
func TestTheRemoteRefDetectorDoesNotTripOnProse(t *testing.T) {
	s := SkillEntry{VersionID: "prose", ImplHandle: "h", SchemaCompiles: true,
		Schema: schema(`{"description":"do not use a remote $ref here","type":"object"}`)}
	selectable, refused := SelectableSkills([]SkillEntry{s})
	if len(selectable) != 1 {
		t.Errorf("a schema that only mentions $ref in prose was refused: %v", refused)
	}
}

// ── 6b.5, 6b.6 · Tools ──────────────────────────────────────────────────────────────────────────

// 🚫 A tool declaring outbound network access is NOT bindable, and the refusal is not overridable.
// 🚫 An unapproved tool is not bindable.
func TestNetworkDeclaringAndUnapprovedToolsAreNotBindable(t *testing.T) {
	all := []ToolEntry{
		{Name: "grep", TenantScope: "_global", Approved: true},
		{Name: "fetch", TenantScope: "_global", Approved: true, DeclaresNetwork: true},
		{Name: "draft", TenantScope: "acme", Approved: false},
	}
	bindable, refused := BindableTools(all)
	if len(bindable) != 1 || bindable[0].Name != "grep" {
		t.Errorf("bindable = %+v, want only the approved non-network tool", bindable)
	}
	if !strings.Contains(refused["_global/fetch"], "not overridable") {
		t.Errorf("the network refusal does not say it is not overridable: %q", refused["_global/fetch"])
	}
	if !strings.Contains(refused["_global/fetch"], "egress surface created by a dropdown") {
		t.Errorf("the network refusal does not say WHY: %q", refused["_global/fetch"])
	}
	if refused["acme/draft"] == "" {
		t.Error("an unapproved tool was refused with no reason")
	}

	// Save-time: selecting either is refused, naming both.
	err := RequireBindableTools([]string{"_global/fetch", "acme/draft"}, all)
	if !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("a network-declaring and an unapproved tool were accepted at save: %v", err)
	}
	for _, want := range []string{"_global/fetch", "acme/draft"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
}

// 🔴 SCOPE IS PART OF THE IDENTITY. `_global/search` and `acme/search` are different bindings, and a
// check on the name alone would let a tenant-scoped tool be approved into a global one.
func TestToolScopeIsPartOfTheIdentity(t *testing.T) {
	all := []ToolEntry{
		{Name: "search", TenantScope: "acme", Approved: true},
	}
	// The global one does not exist; only the tenant-scoped one does.
	if err := RequireBindableTools([]string{"_global/search"}, all); err == nil {
		t.Fatal("`_global/search` bound successfully against a tenant-scoped tool of the same NAME. " +
			"They are different bindings, and conflating them binds a capability nobody approved globally.")
	}
	if err := RequireBindableTools([]string{"acme/search"}, all); err != nil {
		t.Errorf("the tenant-scoped tool was refused: %v", err)
	}
}

// ── 6b.9 · Harness params ───────────────────────────────────────────────────────────────────────

func TestHarnessParamsAreValidatedAtSave(t *testing.T) {
	cases := map[string]struct {
		p      HarnessParams
		reject bool
		names  []string
	}{
		"single-shot needs no turns": {HarnessParams{Strategy: "single-shot"}, false, nil},
		"multi-turn requires max_turns": {
			HarnessParams{Strategy: "reflexion"}, true, []string{"REQUIRED", "unbounded loop"},
		},
		"out of ceiling": {
			HarnessParams{Strategy: "reflexion", MaxTurns: 40}, true, []string{"ceiling", "16"},
		},
		"at the ceiling is fine": {HarnessParams{Strategy: "reflexion", MaxTurns: 16}, false, nil},
		// 🔴 THE SUBTLE ONE. Eight turns with three retries EACH is up to 32 provider calls — four
		// attempts per turn, not three total — and both numbers look reasonable alone. The arithmetic
		// is worth stating precisely here, because getting it wrong in the other direction would
		// under-report the ceiling breach.
		"retry budget multiplies past the ceiling": {
			HarnessParams{Strategy: "reflexion", MaxTurns: 8, RetryBudget: 3}, true,
			[]string{"multiplication", "32"},
		},
		"a turn count on a single-turn strategy": {
			HarnessParams{Strategy: "single-shot", MaxTurns: 5}, true, []string{"single-turn"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateHarnessParams(c.p)
			if c.reject && err == nil {
				t.Fatalf("%+v was accepted at save", c.p)
			}
			if !c.reject && err != nil {
				t.Fatalf("%+v was refused: %v", c.p, err)
			}
			for _, want := range c.names {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %q: %v", want, err)
				}
			}
		})
	}
}

// ── 6b.7 · Policy params ────────────────────────────────────────────────────────────────────────

func f(v float64) *float64 { return &v }

// Malformed policy params are refused at SAVE, naming the POLICY and the PARAMETER — and 🔴 no version
// row is written, which is the half that matters: a `config_hash` pointing at a configuration nothing
// can run is worse than a refused save.
func TestMalformedPolicyParamsAreRefusedAtSaveNamingPolicyAndParameter(t *testing.T) {
	schema := map[string]ParamSpec{
		"window":   {Type: "int", Required: true, Min: f(1), Max: f(100)},
		"strategy": {Type: "string", Enum: []string{"recency", "salience"}},
	}
	cases := map[string]struct {
		params map[string]any
		want   string
	}{
		"missing required": {map[string]any{}, "window is required"},
		"below minimum":    {map[string]any{"window": 0}, "below the minimum"},
		"above maximum":    {map[string]any{"window": 1000}, "above the maximum"},
		"wrong type":       {map[string]any{"window": "eight"}, "must be a number"},
		"not in enum":      {map[string]any{"window": 4, "strategy": "vibes"}, "not one of"},
		"undeclared param": {map[string]any{"window": 4, "temperature": 0.7}, "not a parameter"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidatePolicyParams("summary-buffer", c.params, schema)
			if err == nil {
				t.Fatalf("%v was accepted at save", c.params)
			}
			if !strings.Contains(err.Error(), "summary-buffer") {
				t.Errorf("the refusal does not name the POLICY: %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the refusal does not say %q: %v", c.want, err)
			}
		})
	}
	// And a valid set passes.
	if err := ValidatePolicyParams("summary-buffer",
		map[string]any{"window": 8, "strategy": "recency"}, schema); err != nil {
		t.Errorf("valid params were refused: %v", err)
	}
}

// ── 6b.10, 6b.11 · Host-service availability ────────────────────────────────────────────────────

// 🔴 An unsupplied-service selection is refused at SAVE, naming the service AND what supplying it
// means — and 🚫 NEVER offering a neighbouring strategy as a substitute.
func TestAnUnsuppliedHarnessServiceIsRefusedAtSaveNamingTheService(t *testing.T) {
	// P30's runner supplies none of the three.
	hosts := RunnerHosts{}
	avail := HarnessAvailability(hosts)

	err := RequireAvailable("harness", "react-loop", avail)
	if !errors.Is(err, ErrHostServiceMissing) {
		t.Fatalf("react-loop was accepted with no tool executor: %v", err)
	}
	if !strings.Contains(err.Error(), "tool executor") {
		t.Errorf("the refusal does not name the SERVICE: %v", err)
	}
	// 🚫 It must not suggest a neighbour. The whole point of the runtime's refusal is that the
	// neighbouring strategy is a DIFFERENT strategy.
	for _, neighbour := range []string{"try reflexion", "use single-shot instead", "falls back"} {
		if strings.Contains(strings.ToLower(err.Error()), neighbour) {
			t.Errorf("the refusal offers a substitute (%q): %v", neighbour, err)
		}
	}

	// The two that need nothing are available.
	for _, name := range []string{"single-shot", "reflexion"} {
		if err := RequireAvailable("harness", name, avail); err != nil {
			t.Errorf("%s needs no host service and was refused: %v", name, err)
		}
	}

	// And giving the runner a critic makes critic-loop available — availability is COMPUTED from the
	// runner, so this changes with no edit to a list.
	withCritic := HarnessAvailability(RunnerHosts{Critic: true})
	if err := RequireAvailable("harness", "critic-loop", withCritic); err != nil {
		t.Errorf("critic-loop was refused despite a critic being supplied: %v", err)
	}
}

// 🔴 TASK 6b.20 — the memory host-service fences. An unsupplied summarizer or embedder is refused at
// SAVE; a similarity-recall strategy without a pinned embedding_ref is refused; NEITHER degrades.
func TestMemoryHostServiceFencesRefuseAtSaveAndNeverDegrade(t *testing.T) {
	none := MemoryAvailability(RunnerHosts{})

	// The three that need nothing.
	for _, name := range []string{"none", "scratchpad", "entity-memory"} {
		if err := RequireAvailable("memory", name, none); err != nil {
			t.Errorf("%s needs no host service and was refused: %v", name, err)
		}
	}

	// summary-buffer without a summarizer.
	err := RequireAvailable("memory", "summary-buffer", none)
	if !errors.Is(err, ErrHostServiceMissing) {
		t.Fatalf("summary-buffer was accepted with no summarizer: %v", err)
	}
	if !strings.Contains(err.Error(), "summarizer") {
		t.Errorf("the refusal does not name the service: %v", err)
	}
	// 🚫 It must say it never degrades — "a summary-buffer that quietly truncates IS scratchpad".
	if !strings.Contains(err.Error(), "scratchpad") {
		t.Errorf("the refusal does not say a truncating summary-buffer IS scratchpad: %v", err)
	}

	// vector-recall without an embedder.
	err = RequireAvailable("memory", "vector-recall", none)
	if !errors.Is(err, ErrHostServiceMissing) {
		t.Fatalf("vector-recall was accepted with no embedder: %v", err)
	}

	// 🔴 AND WITH AN EMBEDDER BUT NO PINNED embedding_ref — the case that would otherwise slip through,
	// because the service IS supplied and only the pin is missing.
	withEmbedder := MemoryAvailability(RunnerHosts{Embedder: true})
	err = RequireAvailable("memory", "vector-recall", withEmbedder)
	if !errors.Is(err, ErrHostServiceMissing) {
		t.Fatalf("vector-recall was accepted with an embedder and NO pinned embedding_ref: %v", err)
	}
	if !strings.Contains(err.Error(), "pinned") {
		t.Errorf("the refusal does not name the missing pin: %v", err)
	}

	// Both together: available.
	both := MemoryAvailability(RunnerHosts{Embedder: true, EmbeddingRef: "text-embedding-3-small@1"})
	if err := RequireAvailable("memory", "vector-recall", both); err != nil {
		t.Errorf("vector-recall was refused with an embedder AND a pin: %v", err)
	}
}

// 🔴 The SECOND SPEND LINE is marked. A dropdown that quietly doubles the cost of an analysis is the
// failure FR1.9 exists to prevent.
func TestStrategiesThatCostASecondModelCallSaySo(t *testing.T) {
	marked := map[string]bool{}
	for _, a := range HarnessAvailability(RunnerHosts{Critic: true}) {
		if a.SecondSpendLine {
			marked[a.Name] = true
		}
	}
	for _, a := range MemoryAvailability(RunnerHosts{Summarizer: true}) {
		if a.SecondSpendLine {
			marked[a.Name] = true
		}
	}
	for _, want := range []string{"critic-loop", "summary-buffer"} {
		if !marked[want] {
			t.Errorf("%s costs a second metered model call and is not marked as a second spend line", want)
		}
	}
}

// 🚫 An UNAVAILABLE strategy is still LISTED. A hidden option is indistinguishable from one that does
// not exist, and an operator who cannot see `react-loop` cannot ask for the tool executor.
func TestUnavailableStrategiesAreShownNotHidden(t *testing.T) {
	avail := HarnessAvailability(RunnerHosts{})
	names := map[string]Availability{}
	for _, a := range avail {
		names[a.Name] = a
	}
	for _, want := range []string{"react-loop", "plan-execute", "critic-loop"} {
		a, listed := names[want]
		if !listed {
			t.Fatalf("%s is HIDDEN. A hidden option is indistinguishable from one that does not exist.", want)
		}
		if a.Available {
			t.Errorf("%s reports available with no host services supplied", want)
		}
		if a.Needs == "" || a.Reason == "" {
			t.Errorf("%s is shown unavailable without naming what it needs: %+v", want, a)
		}
	}
}
