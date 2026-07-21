package patternclassifier

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// stubModel is a TEST-ONLY model. It lives in a _test.go file on purpose: nothing in the shipped
// package can reach it, so it cannot become the thing that quietly answers in production.
type stubModel struct {
	// reply is returned for every subgraph.
	reply []RawLabel
	err   error
	// calls records what was asked, so "the LLM was not consulted about a rule-covered subgraph" is
	// checkable rather than assumed.
	calls []FallbackRequest
}

func (m *stubModel) ClassifySubgraph(_ context.Context, req FallbackRequest) ([]RawLabel, error) {
	m.calls = append(m.calls, req)
	if m.err != nil {
		return nil, m.err
	}
	return m.reply, nil
}

func conf(v float64) *float64 { return &v }

func fallbackOpts(f fixture, m FallbackModel) Options {
	o := f.opts()
	o.Fallback = m
	o.FallbackConfig = FallbackConfig{Model: "stub-classifier-1", Seed: 7, Temperature: 0}
	return o
}

// Task 4.1 + the determinism guarantee: a fully rule-covered IR makes ZERO LLM calls. This is the
// countable form of "the common case never pays for, or varies with, a model".
func TestFullyRuleCoveredIRMakesZeroLLMCalls(t *testing.T) {
	m := &stubModel{reply: []RawLabel{{Pattern: string(GuardrailsSafety), Confidence: conf(0.9)}}}
	f := fxComposite()
	res, err := Classify(context.Background(), f.ir, fallbackOpts(f, m))
	if err != nil {
		t.Fatal(err)
	}
	if res.LLMCalls != 0 {
		t.Fatalf("LLMCalls = %d, want 0 on a fully rule-covered IR", res.LLMCalls)
	}
	if len(m.calls) != 0 {
		t.Fatalf("the model was consulted %d times about a rule-covered IR", len(m.calls))
	}
	for _, l := range res.Labels {
		if l.Source != SourceRule {
			t.Errorf("label %q has source %q on a fully rule-covered IR", l.Pattern, l.Source)
		}
	}
}

// Task 4.1/4.2: the fallback fires on the ambiguous residue, is constrained to the taxonomy, and its
// label carries source=llm, a confidence, and a reproducibility reference.
func TestFallbackClassifiesOnlyTheAmbiguousResidue(t *testing.T) {
	m := &stubModel{reply: []RawLabel{{Pattern: string(GuardrailsSafety), Confidence: conf(0.55)}}}
	f := fxAmbiguous()
	res, err := Classify(context.Background(), f.ir, fallbackOpts(f, m))
	if err != nil {
		t.Fatal(err)
	}
	if res.LLMCalls != 1 || len(m.calls) != 1 {
		t.Fatalf("want exactly one fallback call, got LLMCalls=%d calls=%d", res.LLMCalls, len(m.calls))
	}
	if len(res.Labels) != 1 {
		t.Fatalf("want one llm label, got %s", dumpLabels(res))
	}
	l := res.Labels[0]
	if l.Source != SourceLLM || l.Pattern != GuardrailsSafety {
		t.Errorf("got %+v", l)
	}
	if l.LLMRunRef == "" || l.TaxonomyVersion != TaxonomyVersion {
		t.Errorf("an llm label must carry a run ref and pin the taxonomy: %+v", l)
	}
	// The labeled residue region must now be a defined subgraph — a label may not reference a region
	// nothing defines.
	found := false
	for _, sg := range res.Subgraphs {
		if sg.SubgraphID == l.SubgraphRef {
			found = true
		}
	}
	if !found {
		t.Errorf("llm label references subgraph %q, which is not defined in the result", l.SubgraphRef)
	}
}

// Task 4.2: the request actually constrains the model — it enumerates the taxonomy and carries a
// closed-enum schema. Asking politely without a schema is not a constraint.
func TestFallbackRequestEnumeratesTheTaxonomyAndCarriesAClosedSchema(t *testing.T) {
	m := &stubModel{}
	f := fxAmbiguous()
	if _, err := Classify(context.Background(), f.ir, fallbackOpts(f, m)); err != nil {
		t.Fatal(err)
	}
	req := m.calls[0]
	for _, i := range Patterns() {
		if !strings.Contains(req.Prompt, string(i.Pattern)) {
			t.Errorf("prompt does not enumerate %q", i.Pattern)
		}
	}
	if !strings.Contains(req.Prompt, "MUST NOT invent a pattern name") {
		t.Error("prompt does not forbid free text")
	}
	var schema map[string]any
	if err := json.Unmarshal(req.Schema, &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	items := schema["properties"].(map[string]any)["labels"].(map[string]any)["items"].(map[string]any)
	enum := items["properties"].(map[string]any)["pattern"].(map[string]any)["enum"].([]any)
	if len(enum) != TaxonomySize {
		t.Errorf("schema enum has %d values, want the full closed taxonomy of %d", len(enum), TaxonomySize)
	}
	req0 := items["required"].([]any)
	if len(req0) != 2 {
		t.Errorf("schema must require both pattern and confidence, got %v", req0)
	}
}

// Task 4.4 — THE adversarial test. A model that ignores the schema and answers with free text, an
// invented pattern, a missing confidence, or an out-of-range one must have every such answer
// rejected, dropped, and DIAGNOSED. A schema is a request to a cooperative provider; this is the
// guarantee.
func TestOutOfTaxonomyAndMalformedFallbackOutputIsRejectedAndDiagnosed(t *testing.T) {
	adversarial := &stubModel{reply: []RawLabel{
		{Pattern: "self_healing_swarm", Confidence: conf(0.99)},              // invented
		{Pattern: "It looks like a guardrail to me!", Confidence: conf(0.8)}, // free text
		{Pattern: string(Routing), Confidence: nil},                          // no confidence
		{Pattern: string(Routing), Confidence: conf(1.7)},                    // out of range
		{Pattern: "ROUTING", Confidence: conf(0.9)},                          // wrong case
		{Pattern: string(GuardrailsSafety), Confidence: conf(0.42)},          // the one good answer
	}}
	f := fxAmbiguous()
	res, err := Classify(context.Background(), f.ir, fallbackOpts(f, adversarial))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Labels) != 1 || res.Labels[0].Pattern != GuardrailsSafety {
		t.Fatalf("only the in-taxonomy, well-formed answer may survive, got %s", dumpLabels(res))
	}
	if len(res.Diagnostics) != 5 {
		t.Fatalf("want 5 rejections diagnosed, got %d: %v", len(res.Diagnostics), res.Diagnostics)
	}
	// The rejected VALUE is kept verbatim — the whole point of the record.
	joined := ""
	for _, d := range res.Diagnostics {
		joined += d.String() + "\n"
	}
	for _, want := range []string{"self_healing_swarm", "It looks like a guardrail to me!", "ROUTING", "no confidence returned"} {
		if !strings.Contains(joined, want) {
			t.Errorf("diagnostics do not record %q:\n%s", want, joined)
		}
	}
}

// Task 4.3: the LLM never overrides a rule label. The primary guarantee is structural (a rule-covered
// subgraph is never in the residue), and this asserts the belt-and-braces refusal too — if the guard
// is ever reached, a rule label was about to be overwritten.
func TestFallbackRefusesToOverrideARuleLabel(t *testing.T) {
	var diags diagSink
	f := fxRouting()
	g := newGraph(f.ir)
	sg := Subgraph{SubgraphID: "sg_already_labeled", NodeIDs: []string{"n_router"}}
	m := &stubModel{reply: []RawLabel{{Pattern: string(Planning), Confidence: conf(0.9)}}}
	labels, _, calls, err := runFallback(context.Background(), m, FallbackConfig{Model: "x"}, g,
		[]Subgraph{sg}, map[string]bool{"sg_already_labeled": true}, &diags)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || len(labels) != 0 {
		t.Fatalf("the model must not be consulted about a rule-labeled subgraph: calls=%d labels=%d", calls, len(labels))
	}
	if len(diags.sorted()) != 1 || !strings.Contains(diags.sorted()[0].Reason, "must not override") {
		t.Fatalf("the refusal must be recorded: %v", diags.sorted())
	}
}

// A behavioral pattern named by the MODEL is still only a candidate: the model saw the same topology
// the detectors did, and topology cannot confirm a runtime fact whoever is reading it.
func TestFallbackBehavioralAnswerIsClampedToCandidate(t *testing.T) {
	m := &stubModel{reply: []RawLabel{{Pattern: string(HumanInTheLoop), Confidence: conf(0.97)}}}
	f := fxAmbiguous()
	res, err := Classify(context.Background(), f.ir, fallbackOpts(f, m))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Labels) != 1 {
		t.Fatalf("got %s", dumpLabels(res))
	}
	l := res.Labels[0]
	if !l.Candidate || l.Confidence != BehavioralCandidateCap {
		t.Errorf("a behavioral answer must be a capped candidate, got candidate=%v conf=%.2f", l.Candidate, l.Confidence)
	}
	if len(res.Diagnostics) != 1 || !strings.Contains(res.Diagnostics[0].Reason, "clamped") {
		t.Errorf("the clamp must be recorded, not silent: %v", res.Diagnostics)
	}
}

// Task 4.5: every fallback run records {model, seed, temperature, prompt_version, taxonomy_version}
// keyed by config_hash, and the label points at it.
func TestFallbackRunIsRecordedForReproducibility(t *testing.T) {
	m := &stubModel{reply: []RawLabel{{Pattern: string(GuardrailsSafety), Confidence: conf(0.5)}}}
	f := fxAmbiguous()
	res, err := Classify(context.Background(), f.ir, fallbackOpts(f, m))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.LLMRuns) != 1 {
		t.Fatalf("want one run record, got %d", len(res.LLMRuns))
	}
	run := res.LLMRuns[0]
	if run.Config.Model != "stub-classifier-1" || run.Config.Seed != 7 {
		t.Errorf("run does not record the model/seed: %+v", run.Config)
	}
	if run.Config.PromptVersion == "" || run.Config.TaxonomyVersion != TaxonomyVersion {
		t.Errorf("run does not pin the prompt/taxonomy: %+v", run.Config)
	}
	if len(run.ConfigHash) != 64 {
		t.Errorf("config_hash should be a full sha256: %q", run.ConfigHash)
	}
	if res.Labels[0].LLMRunRef != run.RunRef {
		t.Errorf("the label does not point at its run record: %q vs %q", res.Labels[0].LLMRunRef, run.RunRef)
	}
	// Same config → same hash. Different config → different hash. Otherwise the key is decorative.
	again, _ := run.Config.ConfigHash()
	if again != run.ConfigHash {
		t.Error("config_hash is not stable for the same config")
	}
	other := run.Config
	other.Seed = 8
	if h, _ := other.ConfigHash(); h == run.ConfigHash {
		t.Error("config_hash does not distinguish a different seed")
	}
}

// The prompt version is DERIVED from the prompt text. An edit to the wording must change it, or
// stored labels would point at a prompt that no longer exists.
func TestPromptVersionIsDerivedFromThePromptText(t *testing.T) {
	g := newGraph(fxAmbiguous().ir)
	sgA := Subgraph{SubgraphID: "a", NodeIDs: []string{"n_guard"}}
	sgB := Subgraph{SubgraphID: "b", NodeIDs: []string{"n_solo"}}
	_, vA := buildPrompt(g, sgA)
	_, vB := buildPrompt(g, sgB)
	if vA != vB {
		t.Errorf("the version identifies the CLASSIFIER, not the region: %q vs %q", vA, vB)
	}
	if !strings.HasPrefix(vA, "p35-classify-") {
		t.Errorf("unexpected version form %q", vA)
	}
}

// A non-zero temperature makes llm labels non-reproducible. That must be VISIBLE, not discovered
// later when two runs disagree.
func TestNonZeroTemperatureIsDiagnosed(t *testing.T) {
	m := &stubModel{reply: []RawLabel{{Pattern: string(GuardrailsSafety), Confidence: conf(0.5)}}}
	f := fxAmbiguous()
	o := fallbackOpts(f, m)
	o.FallbackConfig.Temperature = 0.7
	res, err := Classify(context.Background(), f.ir, o)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range res.Diagnostics {
		if strings.Contains(d.Reason, "NOT reproducible") {
			found = true
		}
	}
	if !found {
		t.Errorf("a non-zero temperature must be flagged: %v", res.Diagnostics)
	}
}

// A model outage leaves the region honestly unclassified — it does not fabricate a label, and it does
// not fail the whole classification and throw away the rule labels that are perfectly good.
func TestFallbackModelErrorLeavesRegionUnclassified(t *testing.T) {
	m := &stubModel{err: errors.New("upstream 503")}
	f := fxAmbiguous()
	res, err := Classify(context.Background(), f.ir, fallbackOpts(f, m))
	if err != nil {
		t.Fatalf("a model outage must not fail the whole classification: %v", err)
	}
	if len(res.Labels) != 0 {
		t.Fatalf("no label may be invented when the model failed: %s", dumpLabels(res))
	}
	if len(res.Diagnostics) != 1 || !strings.Contains(res.Diagnostics[0].Reason, "503") {
		t.Errorf("the outage must be recorded: %v", res.Diagnostics)
	}
	if len(res.LLMRuns) != 0 {
		t.Error("a failed call has no reproducibility record to write")
	}
}

// With no model configured there is no fallback at all — and, critically, no built-in stub silently
// answering in its place.
func TestNoFallbackConfiguredMeansNoLabelsAndNoStub(t *testing.T) {
	f := fxAmbiguous()
	res, err := Classify(context.Background(), f.ir, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	if res.LLMCalls != 0 || len(res.Labels) != 0 {
		t.Fatalf("no model configured must mean no labels: %s", dumpLabels(res))
	}
	if len(res.Residue) != 1 {
		t.Fatal("the region must remain visible as unclassified residue")
	}
}
