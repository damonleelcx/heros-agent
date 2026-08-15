package modelcatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
)

// seed_test.go fences the gap that left every real deployment with an empty model registry.
//
// 🔴 `Menu` JOINS published judgements onto REGISTERED models by name. The only caller of
// `registry.RegisterModel` in this repository was `cmd/demo/configui`, so on a deployment stood up by
// `make deploy-up` the registry was empty and three things were empty with it: `/api/v1/models`, the
// Studio matrix's ROWS (it rendered a workflow's node columns over nothing), and the proposal menu —
// the last of which reads as "we looked at your workflow and found nothing to improve".
//
// Found by opening /app/studio against a real linked workflow. No test failed, because every test that
// needed models registered them itself.

// recordingRegistrar records what was asked of it and can be told to fail one name.
type recordingRegistrar struct {
	got      []registry.ModelSpec
	names    []string
	failName string
}

func (r *recordingRegistrar) RegisterModel(_ context.Context, name string, spec registry.ModelSpec) (string, error) {
	if name == r.failName {
		return "", errors.New("registry is down for this one")
	}
	r.names = append(r.names, name)
	r.got = append(r.got, spec)
	return "v-" + name, nil
}

func catalogFileWith(t *testing.T, body string) *FileSource {
	t.Helper()
	p := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return NewFileSource(p)
}

func TestSeedRegistersEveryDeclaredModel(t *testing.T) {
	src := catalogFileWith(t, `{"models":[
	  {"name":"Claude Sonnet 5","tier":3,"cost_per_run":0.05,"latency_ms":900,"provider":"anthropic","model_id":"claude-sonnet-5","params":{"max_tokens":4096}},
	  {"name":"GPT-5","tier":4,"cost_per_run":0.14,"latency_ms":2100,"provider":"openai","model_id":"gpt-5"}
	]}`)
	reg := &recordingRegistrar{}
	rep, err := SeedRegistry(context.Background(), src, reg)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if rep.Registered != 2 || rep.Undeclared != 0 || len(rep.Failed) != 0 {
		t.Fatalf("report = %+v, want 2 registered", rep)
	}
	// The NAME must be the catalog's name verbatim, because that is the join key. Registering under a
	// normalised or prettified name would leave the menu empty for a reason nothing could report.
	if reg.names[0] != "Claude Sonnet 5" {
		t.Errorf("registered as %q; the join key is the published name, exactly", reg.names[0])
	}
	if reg.got[0].Provider != "anthropic" || reg.got[0].ModelID != "claude-sonnet-5" {
		t.Errorf("spec = %+v, want the DECLARED provider/model_id", reg.got[0])
	}
}

// 🚫 The one thing seeding must never do: invent an upstream identifier.
func TestAnEntryWithNoDeclaredIdentifiersIsNotInvented(t *testing.T) {
	src := catalogFileWith(t, `{"models":[
	  {"name":"Claude Opus 5","tier":4,"cost_per_run":0.18,"latency_ms":2400}
	]}`)
	reg := &recordingRegistrar{}
	rep, err := SeedRegistry(context.Background(), src, reg)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if rep.Registered != 0 || rep.Undeclared != 1 {
		t.Fatalf("report = %+v, want it counted as UNDECLARED and not registered", rep)
	}
	if len(reg.got) != 0 {
		t.Fatalf("seeding invented %+v for a model that declared no provider/model_id. "+
			"A guessed upstream identifier resolves to a model the customer never chose.", reg.got)
	}
}

// One bad row must not empty the whole matrix.
func TestAFailedEntryDoesNotAbortTheRest(t *testing.T) {
	src := catalogFileWith(t, `{"models":[
	  {"name":"bad","tier":1,"cost_per_run":0.1,"latency_ms":100,"provider":"p","model_id":"m"},
	  {"name":"good","tier":2,"cost_per_run":0.2,"latency_ms":200,"provider":"p","model_id":"m2"}
	]}`)
	reg := &recordingRegistrar{failName: "bad"}
	rep, err := SeedRegistry(context.Background(), src, reg)
	if err != nil {
		t.Fatalf("seed returned an error for ONE bad row: %v — the other models are still worth having", err)
	}
	if rep.Registered != 1 || len(rep.Failed) != 1 {
		t.Fatalf("report = %+v, want 1 registered and 1 named as failed", rep)
	}
	if rep.Failed["bad"] == "" {
		t.Error("the failure is not NAMED; an operator cannot act on a count")
	}
}

func TestSeedingIsIdempotentAcrossBoots(t *testing.T) {
	src := catalogFileWith(t, `{"models":[
	  {"name":"Claude Haiku 4.5","tier":1,"cost_per_run":0.004,"latency_ms":200,"provider":"anthropic","model_id":"claude-haiku-4-5","params":{"max_tokens":4096}}
	]}`)
	reg := &recordingRegistrar{}
	for boot := 0; boot < 3; boot++ {
		if _, err := SeedRegistry(context.Background(), src, reg); err != nil {
			t.Fatalf("boot %d: %v", boot, err)
		}
	}
	// Content-addressed ids make the repeat publish nothing; what this asserts is that seeding carries no
	// "have I run before" state of its own, which is the thing that would be wrong after a restore.
	for i, s := range reg.got {
		if s.Provider != "anthropic" || s.ModelID != "claude-haiku-4-5" {
			t.Fatalf("call %d asked for %+v; every boot must ask for the same content", i, s)
		}
	}
}

// 🔴 The end-to-end property, stated against the DOCUMENTATION rather than against a file.
//
// The first version of this test read `deploy/config/models.json` directly and asserted every entry
// declared a provider and a model id. That was wrong twice over, and both ways matter:
//
//  1. That path is **git-ignored on purpose** — it carries prices, and `gitfence_test.go` FAILS THE
//     BUILD if it ever reaches the index. So the test passed on the machine that wrote the file and
//     would have failed on CI, where it does not exist.
//  2. Worse, the remedy it implied was "commit the catalog", which is the exact thing the other fence
//     exists to prevent. A fence whose failure message argues for breaking another fence is a trap.
//
// What IS shippable is the OPERATOR'S INSTRUCTIONS. An operator writes this file by hand from
// `deploy/config/README.md`; if that document does not tell them to declare a provider and a model id,
// they will not, and the registry stays empty however correct SeedRegistry is.
func TestTheOperatorIsToldToDeclareTheIdentifiers(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "deploy", "config", "README.md"))
	if err != nil {
		t.Fatalf("read the operator's catalog documentation: %v", err)
	}
	doc := string(b)
	for _, field := range []string{"provider", "model_id"} {
		if !strings.Contains(doc, field) {
			t.Errorf("deploy/config/README.md never mentions %q. An operator writes models.json from this "+
				"document; an entry with no provider/model_id registers nothing, and the studio matrix "+
				"renders columns with no rows for a reason no screen states.", field)
		}
	}
	// The sample is what people copy. If IT omits them, the prose above it will not save anybody.
	sample := doc[strings.Index(doc, `{"models":`):]
	if i := strings.Index(sample, "```"); i > 0 {
		sample = sample[:i]
	}
	if !strings.Contains(sample, "provider") || !strings.Contains(sample, "model_id") {
		t.Error("the models.json sample in deploy/config/README.md omits provider/model_id. The sample is " +
			"what gets copied; prose beside it is not.")
	}
}

// A catalog that IS present locally must be self-consistent. Skipped rather than failed when absent,
// because absent is the normal state of a git-ignored operator file on any machine but the operator's.
func TestALocalCatalogIfPresentDeclaresItsIdentifiers(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "config", "models.json")
	if _, err := os.Stat(path); err != nil {
		t.Skip("no local models.json (git-ignored operator file); nothing to check on this machine")
	}
	entries, err := NewFileSource(path).Load()
	if err != nil {
		t.Fatalf("the local catalog does not load: %v", err)
	}
	for _, e := range entries {
		if !e.Registrable() {
			t.Errorf("the local deploy/config/models.json publishes %q with no provider/model_id. Nothing "+
				"will register it, so `Menu` joins onto nothing and the studio matrix has no rows.", e.Name)
		}
	}
}

// ── Params carriage (the entry that registered and could not be called) ─────────────────────────────
//
// Seeding built `registry.ModelSpec{Provider, ModelID}` and left Params zero, so every entry this
// package created had a nil MaxTokens. The Anthropic adapter refuses a call with no max_tokens by
// design. So a published Anthropic model registered cleanly, joined onto the menu, resolved from a
// Variant Spec — and failed at the FIRST inference. Both halves were correct in isolation; nothing
// tested the pair, because every seeding test asserted on Provider and ModelID only.

func TestDeclaredParamsReachTheRegisteredSpec(t *testing.T) {
	src := catalogFileWith(t, `{"models":[
		{"name":"Claude Sonnet 5","tier":3,"cost_per_run":0.9,"latency_ms":1200,
		 "provider":"anthropic","model_id":"claude-sonnet-5",
		 "params":{"max_tokens":4096,"temperature":0.2}}]}`)
	reg := &recordingRegistrar{}
	rep, err := SeedRegistry(context.Background(), src, reg)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if rep.Registered != 1 {
		t.Fatalf("registered %d, want 1", rep.Registered)
	}
	got := reg.got[0]
	// The fence is on the PARAMS, not on provider/model_id — those already had coverage, and their
	// passing is exactly what made the gap invisible.
	if got.Params.MaxTokens == nil {
		t.Fatal("max_tokens did not reach the registered spec: the gateway will refuse every call to " +
			"this entry with \"anthropic requires max_tokens\", at the first inference")
	}
	if *got.Params.MaxTokens != 4096 {
		t.Errorf("max_tokens = %d, want 4096", *got.Params.MaxTokens)
	}
	if got.Params.Temperature == nil || *got.Params.Temperature != 0.2 {
		t.Errorf("temperature did not round-trip: %+v", got.Params.Temperature)
	}
}

func TestAnAnthropicEntryWithNoMaxTokensIsRefusedAtLoad(t *testing.T) {
	src := catalogFileWith(t, `{"models":[
		{"name":"Claude Sonnet 5","tier":3,"cost_per_run":0.9,"latency_ms":1200,
		 "provider":"anthropic","model_id":"claude-sonnet-5"}]}`)
	_, err := src.Load()
	if err == nil {
		t.Fatal("an anthropic entry with no max_tokens loaded cleanly — it will register, appear on the " +
			"menu, and fail at the first inference")
	}
	// The message must name the remedy, not just the condition: an operator reading it is holding the
	// file that needs the edit.
	if !strings.Contains(err.Error(), "max_tokens") {
		t.Errorf("the refusal does not name max_tokens: %v", err)
	}
	if !strings.Contains(err.Error(), "Claude Sonnet 5") {
		t.Errorf("the refusal does not name which entry is wrong: %v", err)
	}
}

func TestANonPositiveMaxTokensIsRefused(t *testing.T) {
	src := catalogFileWith(t, `{"models":[
		{"name":"C","tier":1,"cost_per_run":0.1,"latency_ms":10,
		 "provider":"anthropic","model_id":"claude-sonnet-5","params":{"max_tokens":0}}]}`)
	if _, err := src.Load(); err == nil {
		t.Fatal("max_tokens 0 loaded cleanly; it bounds one answer and cannot be zero")
	}
}

// A judgement-only entry (no provider/model_id) describes a model registered by some other route, and
// that registration owns its params. Checking it here would refuse a legitimate publication.
func TestAJudgementOnlyEntryIsNotHeldToProviderRequirements(t *testing.T) {
	src := catalogFileWith(t, `{"models":[
		{"name":"Claude Sonnet 5","tier":3,"cost_per_run":0.9,"latency_ms":1200}]}`)
	if _, err := src.Load(); err != nil {
		t.Fatalf("a judgement-only entry was refused: %v", err)
	}
}
