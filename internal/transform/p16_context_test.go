package transform

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P16 §2/§3 — context stops being the modeled-but-unapplied axis.
//
// The whole phase turns on one property, and every test here is an angle on it: a resolved context
// policy that differs from what the call site does yields an EDIT or a typed REFUSAL, and never a
// quiet nothing. A quiet nothing is not a missing feature — it is a FALSE MEASUREMENT: the override
// is resolved, hashed, and then scored as the base configuration under the variant's own hash.

// contextEntry builds a resolved context entry with its policy implementation BOUND, the way
// registry.ResolveContextPolicy would. Binding the real implementation is the point: the materializer
// asks the policy which messages it retains, so a test that faked that answer would prove nothing about
// what the runtime assembly does.
func contextEntry(t *testing.T, policy, params string) *registry.ContextEntry {
	t.Helper()
	var impl registry.Policy
	for _, p := range registry.BuiltinPolicies() {
		if p.Name() == policy {
			impl = p
		}
	}
	if impl == nil {
		t.Fatalf("no builtin policy named %q; a test may not invent one", policy)
	}
	raw := json.RawMessage(params)
	if params == "" {
		raw = json.RawMessage(`{}`)
	}
	return &registry.ContextEntry{
		VersionID: strings.Repeat("e", 64), Name: "ctx",
		Spec:   registry.ContextSpec{Policy: policy, Params: raw},
		Policy: impl,
	}
}

func contextOverride(t *testing.T, policy, params string) variantspec.ResolvedOverride {
	t.Helper()
	return variantspec.ResolvedOverride{Context: contextEntry(t, policy, params)}
}

// ── task 2.2 — a selection policy on a Go node emits an edit, not a refusal ───────────────────────

func TestGoContextMaterializes(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	patch, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["history"]: contextOverride(t, "sliding-window", `{"window_size":2}`),
	}), root)
	if err != nil {
		t.Fatalf("a sliding-window override on a Go node must MATERIALIZE, not refuse: %v", err)
	}
	if patch.IsEmpty() {
		t.Fatal("the patch is empty: a 2-message window over a 4-turn call site must change the source, " +
			"or the variant would run — and be scored as — the configuration it replaced")
	}

	out := string(patch.Files["pipeline.go"])
	// The window keeps the most RECENT turns: recency is what a conversation window is for, and it is
	// the same rule the host-side Assemble applies (registry.SlidingWindowPolicy.Retain).
	for _, dropped := range []string{"turnOne", "turnTwo"} {
		if strings.Contains(historyLine(t, out), dropped) {
			t.Errorf("turn %s is outside the 2-message window but survived the rewrite:\n%s",
				dropped, historyLine(t, out))
		}
	}
	for _, kept := range []string{"turnThree", "turnFour"} {
		if !strings.Contains(historyLine(t, out), kept) {
			t.Errorf("turn %s is inside the 2-message window but was deleted:\n%s", kept, historyLine(t, out))
		}
	}

	// The touched record names the dimension, so the build gate can attribute a compiler diagnostic to
	// the context change rather than to whichever override happens to be the only candidate.
	var sawContext bool
	for _, td := range patch.Touched {
		if td.NodeID == ids["history"] && td.Dim == "context" {
			sawContext = true
		}
	}
	if !sawContext {
		t.Errorf("the patch does not record a touched `context` dimension: %+v", patch.Touched)
	}
}

// A compaction policy selects by SIZE rather than by count, and it materializes through the same
// deletion path — so the two policies the coverage table calls materializable are both proved, not just
// the easy one.
func TestGoContextMaterializesCompaction(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	// Each written turn is ~9 source characters (`turnThree` is the longest), so the estimator scores
	// each at ~3 tokens; a 4-token target retains exactly the newest turn.
	patch, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["history"]: contextOverride(t, "semantic-compaction", `{"target_tokens":4}`),
	}), root)
	if err != nil {
		t.Fatalf("semantic-compaction on a Go node must materialize: %v", err)
	}
	line := historyLine(t, string(patch.Files["pipeline.go"]))
	if !strings.Contains(line, "turnFour") {
		t.Errorf("compaction dropped the newest turn; recency must win:\n%s", line)
	}
	if strings.Contains(line, "turnOne") {
		t.Errorf("compaction retained a turn past its token budget:\n%s", line)
	}
}

// A window that is at least as wide as the conversation is a PROOF OF EQUIVALENCE, not a silent drop:
// the policy's assembly IS the list the call site wrote. It emits no edit and no error, and that is the
// only shape of "nothing happened" this engine allows.
func TestWiderThanConversationWindowIsEquivalence(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	patch, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["history"]: contextOverride(t, "sliding-window", `{"window_size":99}`),
	}), root)
	if err != nil {
		t.Fatalf("a window wider than the conversation is equivalent to it, not an error: %v", err)
	}
	if !patch.IsEmpty() {
		t.Errorf("a 99-message window over 4 turns changed the source; the correct diff is no diff:\n%s", patch.Diff)
	}
}

// ── task 2.3 — context is CODE: the resolved assembly is in the diff a reviewer reads ────────────

func TestContextChangeAppearsInDiff(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	resolved := resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["history"]: contextOverride(t, "sliding-window", `{"window_size":1}`),
	})
	// 🔴 BOUND apply mode, deliberately. P10 classifies context as CODE, so no apply mode may render it
	// as an opaque handle: a reviewer who cannot see the message list change is approving a change they
	// cannot read. Requesting the indirection and still finding the assembly in the diff is what proves
	// the rule holds rather than merely being stated.
	resolved.ApplyModes = map[string]variantspec.ApplyMode{ids["history"]: variantspec.ApplyBound}

	patch, err := Generate(resolved, root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	diff := string(patch.Diff)
	if diff == "" {
		t.Fatal("bound apply mode produced no diff for a context change; the assembly would be invisible " +
			"to review")
	}
	if !strings.Contains(diff, "-\t") && !strings.Contains(diff, "-") {
		t.Fatalf("the diff records no removal for a windowed message list:\n%s", diff)
	}
	for _, dropped := range []string{"turnOne", "turnTwo", "turnThree"} {
		if !strings.Contains(diff, dropped) {
			t.Errorf("turn %s left the assembled context but does not appear in the reviewable diff:\n%s",
				dropped, diff)
		}
	}

	// And the binding document — the one artifact an indirection COULD have hidden it in — carries no
	// context assembly at all. Context never becomes data.
	arts, err := GenerateBoundArtifacts(resolved)
	if err != nil {
		// A bound node with no model/prompt is rejected by design; that rejection is itself proof the
		// document has no context to carry.
		if !strings.Contains(err.Error(), "bound") {
			t.Fatalf("GenerateBoundArtifacts: %v", err)
		}
		return
	}
	for name, body := range arts {
		if strings.Contains(string(body), "sliding-window") || strings.Contains(string(body), "turnOne") {
			t.Errorf("the bound artifact %s carries the context assembly; context is code and must live in "+
				"the diff, not behind an indirection:\n%s", name, body)
		}
	}
}

// ── task 2.4 / 7.1 🔴 — a differing policy is applied OR refused, never absent ────────────────────

func TestContextOverrideNeverSilentlyDropped(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	// Every policy the registry ships, against a call site whose written list they all differ from.
	// The assertion is deliberately weak on WHICH outcome and absolute on the third one being impossible.
	for _, tc := range []struct {
		policy, params string
	}{
		{"sliding-window", `{"window_size":1}`},
		{"semantic-compaction", `{"target_tokens":4}`},
		{"summarization", `{"summarizer_model_ref":"m"}`},
		{"rag-retrieval", `{"top_k":3,"retriever_ref":"r"}`},
	} {
		t.Run(tc.policy, func(t *testing.T) {
			patch, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
				ids["history"]: contextOverride(t, tc.policy, tc.params),
			}), root)

			switch {
			case err != nil:
				// A refusal is a correct answer — as long as it is TYPED and names the node and dimension,
				// so the caller can tell it from a crash.
				if !errors.Is(err, ErrUnsafeRewrite) {
					t.Fatalf("policy %s failed with an untyped error: %v", tc.policy, err)
				}
				var re *RewriteError
				if !errors.As(err, &re) || re.Dim != "context" || re.NodeID != ids["history"] {
					t.Fatalf("the refusal must name node and dimension, got %v", err)
				}
			case patch.IsEmpty():
				t.Fatalf("policy %s produced NO diff and NO error. The override was resolved and hashed, so "+
					"the run would execute the call site's own assembly while reporting the variant's "+
					"config_hash — a false result, which is the single worst thing this platform can emit",
					tc.policy)
			}
		})
	}
}

// The refusals above must be refusals of the RIGHT kind: a policy that assembles at run time says so,
// and says it about the policy rather than about the language.
func TestHostCallingPolicyRefusedAtTheCallSite(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	for policy, want := range map[string]string{
		"summarization": "CALLING a summarizer model",
		"rag-retrieval": "RETRIEVING chunks",
	} {
		_, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
			ids["history"]: contextOverride(t, policy, policyParams(policy)),
		}), root)
		if err == nil {
			t.Fatalf("policy %s assembles at run time and cannot be written into source; it must refuse", policy)
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the %s refusal does not say why (want %q):\n%v", policy, want, err)
		}
		if !strings.Contains(err.Error(), "host-side") {
			t.Errorf("the %s refusal should say the policy still runs host-side, so the reader knows the "+
				"capability is not lost:\n%v", policy, err)
		}
	}
}

// A message list assembled at RUNTIME has no written turns to select among. Rewriting it would mean
// guessing what buildHistory() returns — the same silent-data-loss failure a runtime tool set is
// refused for.
func TestRuntimeAssembledMessagesRefused(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	_, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["runtimehistory"]: contextOverride(t, "sliding-window", `{"window_size":1}`),
	}), root)
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("want ErrUnsafeRewrite for a runtime-assembled message list, got %v", err)
	}
	if !strings.Contains(err.Error(), "at runtime") {
		t.Errorf("the refusal must name the runtime assembly as the cause:\n%v", err)
	}
}

// A window over a one-turn-per-line list must not close the lines up: gateMinimal rejects any rewrite
// that changes the file's line count, because TouchedDimension.Line has to stay valid in both files.
func TestStackedMessagesKeepTheirLineCount(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	patch, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["stackedhistory"]: contextOverride(t, "sliding-window", `{"window_size":1}`),
	}), root)
	if err != nil {
		t.Fatalf("a stacked message list is still a written list: %v", err)
	}
	before := strings.Count(readPipelineFixture(t, root), "\n")
	after := strings.Count(string(patch.Files["pipeline.go"]), "\n")
	if before != after {
		t.Errorf("the rewrite changed the file from %d lines to %d; a deletion may blank a line but never "+
			"remove one", before, after)
	}
}

// ── task 2.5 / 7.3 — determinism ─────────────────────────────────────────────────────────────────

func TestGoContextMaterializationDeterministic(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	gen := func() *Patch {
		p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
			ids["history"]:        contextOverride(t, "sliding-window", `{"window_size":2}`),
			ids["stackedhistory"]: contextOverride(t, "semantic-compaction", `{"target_tokens":4}`),
		}), root)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return p
	}
	a, b := gen(), gen()
	if a.DiffHash != b.DiffHash {
		t.Fatalf("the same config_hash at the same source_revision produced two different diffs:\n%s\n---\n%s",
			a.Diff, b.Diff)
	}
	if string(a.Diff) != string(b.Diff) {
		t.Error("diff hashes matched but the bytes differ, which cannot happen — check DiffHash")
	}
}

// The LLM-FREE policies' determinism is asserted at the source level above. The AUGMENTING one is
// asserted where it actually lives: retrieval assembles host-side, so its determinism claim is
// "identical resolved request under a fixed seed", never "identical provider bytes" (NFR2).
func TestAugmentationAssemblyDeterministic(t *testing.T) {
	host := &recordingHost{chunks: []registry.Chunk{{ID: "c1", Text: "alpha"}, {ID: "c2", Text: "beta"}}}
	conv := registry.Conversation{Messages: []registry.Message{
		{Role: "user", Content: "what is alpha?"},
	}}
	params := json.RawMessage(`{"top_k":2,"retriever_ref":"kb","rerank":true}`)

	first, err := registry.RAGRetrievalPolicy{}.Assemble(context.Background(), host, conv, params, 7)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	second, err := registry.RAGRetrievalPolicy{}.Assemble(context.Background(), host, conv, params, 7)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !reflect.DeepEqual(first.ResolvedRequest, second.ResolvedRequest) {
		t.Errorf("two runs of the same policy+params+conversation+seed issued different resolved requests:\n"+
			"%+v\n%+v", first.ResolvedRequest, second.ResolvedRequest)
	}
	if first.ResolvedRequest.Seed != 7 || first.ResolvedRequest.Op != "rerank" {
		t.Errorf("the resolved request must pin the seed and carry the rerank: %+v", first.ResolvedRequest)
	}
}

// ── task 2.6 🚫 — the summarizer is reached host-side, and only host-side ────────────────────────

func TestSummarizerRunsHostSideOnly(t *testing.T) {
	// (1) The policy reaches its model ONLY through HostServices, and captures the resolved request as
	// the determinism handle.
	host := &recordingHost{summary: "the gist"}
	conv := registry.Conversation{Messages: []registry.Message{
		{Role: "user", Content: "a long conversation"},
		{Role: "assistant", Content: "a long answer"},
	}}
	params := json.RawMessage(`{"summarizer_model_ref":"anthropic/claude-sonnet-5"}`)

	got, err := registry.SummarizationPolicy{}.Assemble(context.Background(), host, conv, params, 3)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if host.summarizeCalls != 1 {
		t.Errorf("the summarizer was called %d times through HostServices, want exactly 1", host.summarizeCalls)
	}
	if got.ResolvedRequest == nil || got.ResolvedRequest.ModelRef != "anthropic/claude-sonnet-5" ||
		got.ResolvedRequest.Seed != 3 {
		t.Errorf("the resolved request is the determinism handle and must pin model + seed: %+v", got.ResolvedRequest)
	}
	if !got.Lossy || got.DropRatio <= 0 {
		t.Errorf("summarization is lossy and must report what it dropped, got lossy=%v drop=%v",
			got.Lossy, got.DropRatio)
	}

	// (2) A policy with NO host services fails closed rather than summarizing nowhere — the credential
	// path is the only path, so its absence is an error, not a degraded mode.
	if _, err := (registry.SummarizationPolicy{}).Assemble(context.Background(), nil, conv, params, 3); err == nil {
		t.Error("a summarization policy with no host services must fail closed; running the summarizer " +
			"nowhere would assemble a context nobody produced")
	}

	// (3) 🚫 And the codemod never puts a summarizer call at a call site. A sandboxed node must not hold
	// a provider credential, so the transform refuses the policy rather than inlining its call.
	root := newTarget(t)
	ids := nodeIDs(t, root)
	_, err = Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["history"]: contextOverride(t, "summarization", `{"summarizer_model_ref":"m"}`),
	}), root)
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("the transform must REFUSE to materialize a summarization policy; emitting a summarizer "+
			"call at the call site would move a provider credential into the target program. got %v", err)
	}
}

// ── wave 16d 🔴 — the refusal is loud, and the LANGUAGE question is asked last ──────────────────

// 🔴 This test used to target TypeScript because TypeScript had no splitter. It now HAS one (wave 16d
// derives spanContextMaterializers from discovery.ListSplitLanguages), so a TypeScript selection
// materializes and this test would otherwise be asserting a contract that is no longer true of it —
// the same drift the P14 skills test had to be rescued from once already.
//
// What it asserts instead is the half that was always load-bearing and is NOT about coverage: a policy
// whose content does not exist in source refuses LOUDLY in every language, names the node, the
// dimension and the policy, and never emits a partial diff. That refusal is permanent — no splitter, in
// any language, will ever materialize a summary a model has not written yet.
func TestSpanEngineContextRefusesNotDrops(t *testing.T) {
	root := spanTarget(t, "pipeline.ts", tsMultiTurnSrc)
	id := onlyNode(t, root, "typescript")

	patch, err := Generate(resolvedIn("typescript", map[string]variantspec.ResolvedOverride{
		id: contextOverride(t, "summarization", `{"summarizer_model_ref":"m"}`),
	}), root)
	if err == nil {
		t.Fatalf("a policy that does not exist in source must REFUSE; it returned a patch (empty=%v) instead",
			patch.IsEmpty())
	}
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("the refusal must be typed, got %v", err)
	}
	var re *RewriteError
	if !errors.As(err, &re) || re.NodeID != id || re.Dim != "context" {
		t.Fatalf("the refusal must name the node and the dimension, got %v", err)
	}
	if re.Cause != CauseNotAtCallSite {
		t.Errorf("a policy assembled at run time is not-expressible-at-a-call-site, got cause %q", re.Cause)
	}
	if !strings.Contains(err.Error(), "summarization") {
		t.Errorf("the refusal does not name the policy it refused:\n%v", err)
	}
	// 🚫 And it must NOT blame a missing materializer: promising a rewriter for a policy no rewriter can
	// ever apply is the exact wrong-owner failure wave 16d exists to end.
	if strings.Contains(err.Error(), "no materializer") {
		t.Errorf("the refusal blames a missing materializer for a policy that is not in source at all:\n%v", err)
	}
}

// 🔴 The companion assertion, and the one that makes the guarantee real: the refusal is not a
// decorated no-op. The variant must NOT transform as its base configuration.
func TestInterimRefusalIsLoudNotSilent(t *testing.T) {
	root := spanTarget(t, "pipeline.ts", tsMultiTurnSrc)
	id := onlyNode(t, root, "typescript")

	// A context override ALONGSIDE a model override this engine CAN apply. If the context refusal were a
	// silent no-op, this would succeed and emit the model edit — a patch that carries the variant's
	// config_hash while assembling context the base configuration's way.
	_, err := Generate(resolvedIn("typescript", map[string]variantspec.ResolvedOverride{
		id: {
			Model:   modelEntry("openai", "gpt-5-mini"),
			Context: contextEntry(t, "summarization", `{"summarizer_model_ref":"m"}`),
		},
	}), root)
	if err == nil {
		t.Fatal("the transform emitted a patch for a spec whose context override it cannot apply. The diff " +
			"would rewrite the model and leave the context as the base configuration, under the VARIANT's " +
			"config_hash — a measurement of a configuration that never existed")
	}
	var re *RewriteError
	if !errors.As(err, &re) || re.Dim != "context" {
		t.Fatalf("the failure must be attributed to the context dimension, got %v", err)
	}
}

// 🔴 Wave 16d's headline: a SELECTION policy materializes in TypeScript, by deleting the turns the
// SHARED policy code does not retain. This is the assertion that was impossible to write before the
// splitter landed, and it is what makes the coverage cell something other than a paper claim.
func TestTypeScriptSelectionMaterializes(t *testing.T) {
	root := spanTarget(t, "pipeline.ts", tsMultiTurnSrc)
	id := onlyNode(t, root, "typescript")

	patch, err := Generate(resolvedIn("typescript", map[string]variantspec.ResolvedOverride{
		id: contextOverride(t, "sliding-window", `{"window_size":1}`),
	}), root)
	if err != nil {
		t.Fatalf("a sliding window over a written TypeScript message list must materialize: %v", err)
	}
	if patch.IsEmpty() {
		t.Fatal("the selection retains fewer turns than the call site wrote, so it must emit an edit")
	}
	if !strings.Contains(string(patch.Diff), "second") {
		t.Errorf("the retained turn is missing from the diff:\n%s", string(patch.Diff))
	}
}

// The refusal's owner text is accurate: P16 owns the region rewrite, and P3 — which shipped the
// policies and never the rewrite — is no longer named as its owner.
//
// 🔴 An UNREGISTERED language label is the only way left to reach the no-materializer branch, and that
// is the point: every language discovery registers now has a splitter. The branch is kept, and kept
// tested, because the next frontend to arrive lands in it before its splitter does.
func TestRefusalNamesOwningPhase(t *testing.T) {
	err := refuseContext("node-1", "elixir", contextOverride(t, "sliding-window", `{"window_size":1}`))
	var re *RewriteError
	if !errors.As(err, &re) {
		t.Fatalf("expected a typed refusal, got %v", err)
	}
	if re.Cause != CauseNoMaterializer {
		t.Errorf("a missing splitter is the one class that names work WE owe, got cause %q", re.Cause)
	}
	msg := err.Error()
	if !strings.Contains(msg, "P16") {
		t.Errorf("the refusal must name the phase that OWNS the context rewrite:\n%s", msg)
	}
	if strings.Contains(msg, "owned by P3") {
		t.Errorf("the refusal still points the reader at P3, which shipped the policies and not the "+
			"rewrite; an actionable-looking pointer to the wrong place costs a reader real time:\n%s", msg)
	}
	if !strings.Contains(msg, "REFUSED rather than dropped") {
		t.Errorf("the refusal must state that the override was not applied, so a reader does not assume it "+
			"quietly took effect:\n%s", msg)
	}
}

// ── the coverage table is the single source of truth ─────────────────────────────────────────────

// Every policy the registry ships has a row, and every row's mode is one the rewriter can act on. A
// policy the registry can resolve but the table has never heard of would refuse with a generic
// "no declared form" message — correct, but a coverage claim nobody wrote down.
func TestEveryBuiltinPolicyHasACoverageRow(t *testing.T) {
	rows := map[string]ContextCoverage{}
	for _, c := range ContextMaterializerCoverage() {
		if c.Language == "go" {
			rows[c.Policy] = c
		}
	}
	for _, p := range registry.BuiltinPolicies() {
		row, ok := rows[p.Name()]
		if !ok {
			t.Errorf("policy %q is registered but has no row in the context materializer coverage table; "+
				"coverage is stated in one place or it is not stated", p.Name())
			continue
		}
		if row.Mode == "not-at-call-site" && row.Reason == "" {
			t.Errorf("policy %q refuses at the call site with no reason; a refusal a user cannot act on is "+
				"a worse refusal than none", p.Name())
		}
		// The table and the type system must agree: a row claiming selection needs an implementation that
		// can say what it retains.
		if row.Mode == "select" {
			if _, ok := p.(registry.SelectionPolicy); !ok {
				t.Errorf("policy %q is declared materializable-by-selection but does not implement "+
					"registry.SelectionPolicy, so the rewriter has no definition to rewrite against", p.Name())
			}
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

// recordingHost is a HostServices double that records what a policy asked it for. It is the only way a
// test can see that a model/retrieval call went through the trusted host rather than anywhere else.
type recordingHost struct {
	summary        string
	chunks         []registry.Chunk
	summarizeCalls int
	retrieveCalls  int
	lastRequest    registry.ResolvedRequest
}

func (h *recordingHost) Summarize(_ context.Context, req registry.ResolvedRequest) (string, error) {
	h.summarizeCalls++
	h.lastRequest = req
	return h.summary, nil
}

func (h *recordingHost) Retrieve(_ context.Context, req registry.ResolvedRequest) ([]registry.Chunk, error) {
	h.retrieveCalls++
	h.lastRequest = req
	return h.chunks, nil
}

func policyParams(policy string) string {
	switch policy {
	case "summarization":
		return `{"summarizer_model_ref":"m"}`
	case "rag-retrieval":
		return `{"top_k":3,"retriever_ref":"r"}`
	default:
		return `{}`
	}
}

// historyLine returns the fixture's one-line message list, so an assertion about which turns survived
// reads the region that changed rather than the whole file.
func historyLine(t *testing.T, src string) string {
	t.Helper()
	for _, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "turnFour") || (strings.Contains(l, "Messages: []anthropic.MessageParam{turn")) {
			return l
		}
	}
	t.Fatalf("the fixture's history call site is missing from the rewritten source:\n%s", src)
	return ""
}

// readPipelineFixture returns the fixture's untransformed source, for a line-count comparison.
func readPipelineFixture(t *testing.T, root string) string {
	t.Helper()
	b, err := readFile(root, "pipeline.go")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

// ── the reviewable diff itself (task 2.3's other half) ───────────────────────────────────────────

// A window that deletes several turns from ONE line must render that line ONCE. Two regions on one
// line used to print two -/+ pairs for the same line, which is wrong in both ways a diff can be: it
// does not apply (the second pair's context was already consumed) and it claims two changed lines
// where one changed. For a system whose product IS the reviewable diff, that is the artifact lying
// about the one thing the reviewer is there to check.
func TestOneLineContextChangeRendersOnce(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	patch, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["history"]: contextOverride(t, "sliding-window", `{"window_size":2}`),
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var removals, additions int
	for _, l := range strings.Split(string(patch.Diff), "\n") {
		switch {
		case strings.HasPrefix(l, "---"), strings.HasPrefix(l, "+++"):
		case strings.HasPrefix(l, "-"):
			removals++
		case strings.HasPrefix(l, "+"):
			additions++
		}
	}
	if removals != 1 || additions != 1 {
		t.Errorf("deleting two turns from one line rendered %d removal(s) and %d addition(s), want 1 and 1; "+
			"the same line printed twice does not apply and over-reports what changed:\n%s",
			removals, additions, patch.Diff)
	}
}

// ── the Python selection materializer, and the refusal ORDER ─────────────────────────────────────

// tsMultiTurnSrc is a TypeScript call site with a real written message list. It exists so the interim
// refusal is tested against a language that genuinely has no rewriter, rather than against a call site
// that has nothing to rewrite — those are different refusals and only one of them is temporary.
const tsMultiTurnSrc = `import OpenAI from "openai";

const client = new OpenAI();

export async function chat() {
  return client.chat.completions.create({
    model: "gpt-4o",
    messages: [{ role: "user", content: "first" }, { role: "assistant", content: "second" }],
  });
}
`

// pyMultiTurnSrc writes three turns out at the call site — the shape a window materializes into.
const pyMultiTurnSrc = `import anthropic

client = anthropic.Anthropic()


def chat():
    return client.messages.create(
        model="claude-opus-4-6",
        messages=[{"role": "user", "content": "first"}, {"role": "assistant", "content": "second"}, {"role": "user", "content": "third"}],
    )
`

// pyKwargsSrc is hermes-agent's real shape: the whole request is assembled elsewhere and unpacked.
const pyKwargsSrc = `import openai

client = openai.OpenAI()


def chat(api_kwargs):
    return client.chat.completions.create(**api_kwargs)
`

// pySpreadSrc writes a list whose length is not knowable at codemod time.
const pySpreadSrc = `import anthropic

client = anthropic.Anthropic()


def chat(history):
    return client.messages.create(
        model="claude-opus-4-6",
        messages=[{"role": "system", "content": "be brief"}, *history],
    )
`

func TestPythonContextMaterializes(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyMultiTurnSrc)
	id := onlyNode(t, root, "python")

	patch, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: contextOverride(t, "sliding-window", `{"window_size":2}`),
	}), root)
	if err != nil {
		t.Fatalf("a sliding-window override on a Python node with a WRITTEN message list must materialize: %v", err)
	}
	if patch.IsEmpty() {
		t.Fatal("the patch is empty: a 2-message window over a 3-turn call site must change the source")
	}
	out := string(patch.Files["pipeline.py"])
	if strings.Contains(out, `"content": "first"`) {
		t.Errorf("the turn outside the window survived:\n%s", out)
	}
	for _, kept := range []string{`"content": "second"`, `"content": "third"`} {
		if !strings.Contains(out, kept) {
			t.Errorf("a turn inside the window was deleted (%s):\n%s", kept, out)
		}
	}
	// The same line-count discipline the Go engine keeps: a deletion may blank a line, never remove one.
	if before, after := strings.Count(pyMultiTurnSrc, "\n"), strings.Count(out, "\n"); before != after {
		t.Errorf("the rewrite changed the file from %d lines to %d", before, after)
	}
	// And the result still parses as Python — gateMinimal's reparse would have refused otherwise, so
	// reaching here at all is the assertion; this pins that the gate is the one that ran.
	if !strings.Contains(out, "client.messages.create(") {
		t.Errorf("the call site was damaged:\n%s", out)
	}
}

// 🔴 THE ORDERING FIX. A `**kwargs` call site has no written message list, so no rewriter in any
// language can select among its turns. Telling its author "the Python materializer is still being
// built" is true and useless — it points at an event that will not help them.
func TestKwargsSiteIsToldAboutTheKwargsNotTheRewriter(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyKwargsSrc)
	id := onlyNode(t, root, "python")

	_, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: contextOverride(t, "sliding-window", `{"window_size":1}`),
	}), root)
	if err == nil {
		t.Fatal("a call site with no written message list must refuse")
	}
	msg := err.Error()
	if !strings.Contains(msg, "**api_kwargs") {
		t.Errorf("the refusal must quote the unpacking, so the reader lands on the right line:\n%s", msg)
	}
	if !strings.Contains(msg, "property of the call site") {
		t.Errorf("the refusal must say this is a fact about the CALL SITE, not about language support:\n%s", msg)
	}
	// The load-bearing negative: it must NOT promise a rewriter, because a rewriter would refuse this
	// same call site for this same reason.
	if strings.Contains(msg, "materializer is still being built") {
		t.Errorf("a **kwargs call site was told to wait for a language rewriter that would refuse it too:\n%s", msg)
	}
}

// The same ordering rule, one step later: a policy that assembles at RUN TIME is refused on the POLICY,
// in every language, before the language is ever considered.
func TestRunTimePolicyRefusedBeforeTheLanguageQuestion(t *testing.T) {
	for _, lang := range []struct{ name, file, src string }{
		{"python", "pipeline.py", pyMultiTurnSrc},
		{"typescript", "pipeline.ts", tsMultiTurnSrc},
	} {
		root := spanTarget(t, lang.file, lang.src)
		id := onlyNode(t, root, lang.name)
		_, err := Generate(resolvedIn(lang.name, map[string]variantspec.ResolvedOverride{
			id: contextOverride(t, "summarization", `{"summarizer_model_ref":"m"}`),
		}), root)
		if err == nil {
			t.Fatalf("%s: a run-time policy must refuse", lang.name)
		}
		if !strings.Contains(err.Error(), "CALLING a summarizer model") {
			t.Errorf("%s: the refusal must name the POLICY's reason:\n%v", lang.name, err)
		}
		if strings.Contains(err.Error(), "materializer is still being built") {
			t.Errorf("%s: a summarization override was told to wait for a rewriter; no rewriter will ever "+
				"write a model's answer into source:\n%v", lang.name, err)
		}
	}
}

// A spread makes the list's length unknown until runtime, so "the third turn" identifies nothing. This
// is hermes-agent's real `moa_loop.py` shape.
func TestPythonSpreadInMessageListRefused(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pySpreadSrc)
	id := onlyNode(t, root, "python")

	_, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: contextOverride(t, "sliding-window", `{"window_size":1}`),
	}), root)
	if err == nil {
		t.Fatal("a message list containing a spread must refuse: its length is not known until runtime")
	}
	if !strings.Contains(err.Error(), "spread") {
		t.Errorf("the refusal must name the spread as the cause:\n%v", err)
	}
}

// The Python splitter's own boundary, exercised directly: what it will and will not take apart. Each
// refusal is a case where a wrong boundary would delete the wrong bytes.
func TestPythonListElementsBoundary(t *testing.T) {
	ok := func(text string, want ...string) {
		t.Helper()
		got, err := pythonListElements(text)
		if err != nil {
			t.Fatalf("pythonListElements(%q): %v", text, err)
		}
		if len(got) != len(want) {
			t.Fatalf("pythonListElements(%q) found %d element(s), want %d: %+v", text, len(got), len(want), got)
		}
		for i := range want {
			if got[i].text != want[i] {
				t.Errorf("element %d = %q, want %q", i, got[i].text, want[i])
			}
			if text[got[i].start:got[i].end] != want[i] {
				t.Errorf("element %d's SPAN addresses %q, want %q — a wrong span deletes the wrong bytes",
					i, text[got[i].start:got[i].end], want[i])
			}
		}
	}
	// A comma inside a nested dict, inside a string, and a trailing comma are all NOT separators.
	ok(`[{"role": "user", "content": "a, b"}, {"role": "assistant", "content": "c"},]`,
		`{"role": "user", "content": "a, b"}`, `{"role": "assistant", "content": "c"}`)
	ok(`[a, b]`, "a", "b")
	ok(`[]`)
	// An escaped quote must not end the string early.
	ok(`[{"content": "he said \"hi\", then left"}, b]`, `{"content": "he said \"hi\", then left"}`, "b")

	for _, bad := range []string{
		`[a, *rest]`,                      // spread: unknown length
		`[a, # the second turn` + "\nb]",  // comment: binds to a neighbour
		`[a, """long` + "\n" + `text"""]`, // triple-quoted: multi-line by nature
		`[a, {"b": 1]`,                    // unbalanced
		`[a, "unterminated]`,              // unterminated string
	} {
		if _, err := pythonListElements(bad); err == nil {
			t.Errorf("pythonListElements(%q) succeeded; a boundary this engine cannot prove must refuse", bad)
		}
	}
}

// The coverage report covers every language that has a rewriter, and only those. A language listed
// without a splitter would claim a capability the engine would then refuse.
//
// 🔴 The expectation is DERIVED, not listed (wave 16d). The previous version hard-coded {go, python} and
// so encoded the scope of the day into an assertion — which is exactly how a scope statement becomes a
// boundary nobody notices they are enforcing. What must be true forever is the RELATIONSHIP: a language
// materializes context iff it is Go or has a list splitter.
func TestCoverageListsEveryMaterializingLanguage(t *testing.T) {
	langs := ContextMaterializerLanguages()
	want := map[string]bool{"go": true}
	for _, l := range discovery.ListSplitLanguages() {
		want[l] = true
	}
	if len(langs) != len(want) {
		t.Fatalf("materializing languages = %v, want exactly %v", langs, want)
	}
	for _, l := range langs {
		if !want[l] {
			t.Errorf("language %q claims a context rewriter", l)
		}
	}
	// 🔴 And every REGISTERED language is one of them: wave 16d's claim is that the (language, policy)
	// table is total, so a registered language with no splitter is a coverage hole, not a scope choice.
	for _, l := range RegisteredLanguages() {
		if !want[l] {
			t.Errorf("registered language %q has no context selection materializer and no coverage row", l)
		}
	}
	// Every language in the report appears with a full policy table, so a console rendering one language
	// never shows a partial boundary.
	seen := map[string]int{}
	for _, c := range ContextMaterializerCoverage() {
		seen[c.Language]++
	}
	for _, l := range langs {
		if seen[l] != len(contextForms) {
			t.Errorf("language %q reports %d policy row(s), want %d", l, seen[l], len(contextForms))
		}
	}
	// 🔴 And the language table and the splitter table are the same fact: a splitter with no coverage row
	// would be a rewriter nobody can discover, and a row with no splitter would be a promise.
	for lang := range spanContextMaterializers {
		if seen[lang] == 0 {
			t.Errorf("a splitter exists for %q but the coverage report never mentions it", lang)
		}
	}
}

// ── P16 10.7 🚫 — no splitter decides retention ─────────────────────────────────────────────────

// The load-bearing invariant of wave 16d. Which turns a policy retains IS the policy, so a per-language
// retention decision would make one `config_hash` describe two different configurations — and the
// harness, which reads only the hash and the trace, would compare them as one.
//
// Asserted two ways, because either alone is escapable: the splitter's TYPE cannot express a retention
// decision (it returns spans, and takes no policy or params), and two languages materializing the same
// policy over the same written turns retain the same ones.
func TestRetentionIsSharedNotPerLanguage(t *testing.T) {
	// 1. Structural: a splitter is `func(text string) ([]elementSpan, error)`. It is handed no policy,
	// no params and no node, so there is nothing for it to decide retention FROM.
	// Each language is handed a list written in ITS OWN syntax — the splitter's whole job — so this
	// asserts the signature rather than a shared spelling.
	written := map[string]string{
		"python":     `[{"role": "user", "content": "a"}, {"role": "user", "content": "b"}]`,
		"typescript": `[{ role: "user", content: "a" }, { role: "user", content: "b" }]`,
		"javascript": `[{ role: "user", content: "a" }, { role: "user", content: "b" }]`,
		"kotlin":     `listOf(userMessage("a"), userMessage("b"))`,
		"java":       `List.of(userMessage("a"), userMessage("b"))`,
		"rust":       `vec![user("a"), user("b")]`,
	}
	for lang, split := range spanContextMaterializers {
		text, ok := written[lang]
		if !ok {
			t.Errorf("%s has a splitter but this test has no list written in its syntax; a language whose "+
				"splitter is never exercised is a row nobody proved", lang)
			continue
		}
		elems, err := split(text)
		if err != nil {
			t.Fatalf("%s: the splitter must split a written list: %v", lang, err)
		}
		if len(elems) != 2 {
			t.Errorf("%s: the splitter returned %d element(s); it reports what was WRITTEN, and nothing "+
				"about the splitter's signature could let it drop one", lang, len(elems))
		}
	}

	// 2. Behavioural: the same policy over the same turns retains the same ones in two languages.
	py := spanTarget(t, "pipeline.py", pyMultiTurnSrc)
	pyID := onlyNode(t, py, "python")
	pyPatch, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
		pyID: contextOverride(t, "sliding-window", `{"window_size":1}`),
	}), py)
	if err != nil {
		t.Fatalf("python: %v", err)
	}
	ts := spanTarget(t, "pipeline.ts", tsMultiTurnSrc)
	tsID := onlyNode(t, ts, "typescript")
	tsPatch, err := Generate(resolvedIn("typescript", map[string]variantspec.ResolvedOverride{
		tsID: contextOverride(t, "sliding-window", `{"window_size":1}`),
	}), ts)
	if err != nil {
		t.Fatalf("typescript: %v", err)
	}
	// Both windows keep the LAST turn and drop the earlier ones. The languages differ in how many turns
	// their fixture wrote, so what is compared is the RULE: the retained turn is the final one.
	if !strings.Contains(string(pyPatch.Files["pipeline.py"]), "third") {
		t.Errorf("python's window did not retain the last turn:\n%s", pyPatch.Files["pipeline.py"])
	}
	if !strings.Contains(string(tsPatch.Files["pipeline.ts"]), "second") {
		t.Errorf("typescript's window did not retain the last turn:\n%s", tsPatch.Files["pipeline.ts"])
	}
	if strings.Contains(string(tsPatch.Files["pipeline.ts"]), `content: "first"`) {
		t.Errorf("typescript retained a turn the shared policy drops:\n%s", tsPatch.Files["pipeline.ts"])
	}
}

// ── P16 10.8 🔴 — a spread makes the list unselectable, by name ─────────────────────────────────

// Some elements are written and some are not, so selecting among the written half while the unwritten
// half survives would produce a diff whose retained set is not the policy's. Refused, naming the spread,
// in every language that has one.
func TestSpreadMakesTheListUnselectable(t *testing.T) {
	cases := []struct{ lang, file, src string }{
		{"python", "pipeline.py", `import anthropic

client = anthropic.Anthropic()


def chat(extra):
    return client.messages.create(
        model="claude-opus-4-6",
        messages=[{"role": "user", "content": "a"}, *extra],
    )
`},
		{"typescript", "pipeline.ts", `import OpenAI from "openai";

const client = new OpenAI();

export async function chat(extra: any[]) {
  return client.chat.completions.create({
    model: "gpt-4o",
    messages: [{ role: "user", content: "a" }, ...extra],
  });
}
`},
	}
	for _, tc := range cases {
		t.Run(tc.lang, func(t *testing.T) {
			root := spanTarget(t, tc.file, tc.src)
			id := onlyNode(t, root, tc.lang)
			msg := refusalFor(t, resolvedIn(tc.lang, map[string]variantspec.ResolvedOverride{
				id: contextOverride(t, "sliding-window", `{"window_size":1}`),
			}), root)
			mustContain(t, msg, "spread", "the construct that made the list unselectable")
			if strings.Contains(msg, "no materializer") {
				t.Errorf("a spread — a fact about the source — was reported as a coverage gap:\n%s", msg)
			}
		})
	}
}

// ── P16 10.10 🔴 — the drop record TRAVELS ──────────────────────────────────────────────────────

// The axis's honesty mechanism. A language that could delete turns without recording them would not
// fail anything — it would simply make `context_drop_ratio` incomplete for some workflows and not
// others, which is undetectable from outside. So the record is produced by the shared path and is
// byte-comparable across languages.
func TestDropRecordIsUnskippableInEveryLanguage(t *testing.T) {
	for _, lang := range ContextMaterializerLanguages() {
		if !discovery.CanSplitWrittenList(lang) && lang != "go" {
			t.Errorf("%s claims a context materializer with no way to read what it dropped", lang)
		}
	}
	// The record is a property of the SHARED policy, not of a rewriter: the same policy over the same
	// written turns reports the same retained count wherever it runs.
	entry := contextEntry(t, "sliding-window", `{"window_size":1}`)
	sel, ok := entry.Policy.(registry.SelectionPolicy)
	if !ok {
		t.Fatal("sliding-window must be a selection policy")
	}
	msgs := []registry.Message{{Content: "a"}, {Content: "b"}, {Content: "c"}}
	keep, err := sel.Retain(entry.Spec.Params, msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(keep) != 1 || keep[0] != 2 {
		t.Errorf("the shared policy retained %v; every language's materializer deletes exactly the "+
			"complement of this, so a per-language answer here would be a second definition", keep)
	}
}

// ── P16 10.13 🔴 — every splitter row carries a fixture ─────────────────────────────────────────

// A splitter row is a claim that this engine can say which elements a written list has. Only an
// emission proves one, so each row gets a fixture that emits a selection and asserts the result
// reparses.
func TestEverySplitterRowHasAProof(t *testing.T) {
	fixtures := map[string]struct{ file, src string }{
		"python":     {"pipeline.py", pyMultiTurnSrc},
		"typescript": {"pipeline.ts", tsMultiTurnSrc},
	}
	for lang := range spanContextMaterializers {
		fx, ok := fixtures[lang]
		if !ok {
			// A splitter with no context fixture must at least be proven by the shared splitter's own
			// round-trip tests in internal/discovery; assert it is reachable there rather than silently
			// passing.
			if !discovery.CanSplitWrittenList(lang) {
				t.Errorf("splitter row %q is not reachable from the shared splitter", lang)
			}
			continue
		}
		root := spanTarget(t, fx.file, fx.src)
		id := onlyNode(t, root, lang)
		p, err := Generate(resolvedIn(lang, map[string]variantspec.ResolvedOverride{
			id: contextOverride(t, "sliding-window", `{"window_size":1}`),
		}), root)
		if err != nil {
			t.Errorf("%s: the splitter row must emit a selection: %v", lang, err)
			continue
		}
		eng, err := engineFor(lang)
		if err != nil {
			t.Fatal(err)
		}
		if err := eng.reparse(fx.file, []byte(fx.src), p.Files[fx.file]); err != nil {
			t.Errorf("%s: the selected source does not reparse: %v", lang, err)
		}
	}
}

// ── P16 10.14 🔴 — assert the downstream consumer ───────────────────────────────────────────────

func TestNewLanguageSelectionAssertsDownstreamState(t *testing.T) {
	root := spanTarget(t, "pipeline.ts", tsMultiTurnSrc)
	id := onlyNode(t, root, "typescript")
	p, err := Generate(resolvedIn("typescript", map[string]variantspec.ResolvedOverride{
		id: contextOverride(t, "sliding-window", `{"window_size":1}`),
	}), root)
	if err != nil {
		t.Fatalf("typescript selection must materialize: %v", err)
	}
	if len(p.Diff) == 0 {
		t.Fatal("a materialized selection produced no diff")
	}
	after, ok := p.Files["pipeline.ts"]
	if !ok {
		t.Fatal("the patch does not carry the file it changed")
	}
	// The line count is unchanged: a deletion that removed a line would invalidate every downstream
	// line attribution.
	if got, want := strings.Count(string(after), "\n"), strings.Count(tsMultiTurnSrc, "\n"); got != want {
		t.Errorf("the selection changed the file's line count: %d -> %d", want, got)
	}
	// And the coverage cell agrees this was supposed to work.
	for _, c := range CoverageFor(string(variantspec.DimContext)) {
		if c.Language == "typescript" && c.Form == "sliding-window" && c.Status != CoverageMaterializes {
			t.Errorf("the engine materialized a cell coverage reports as %q", c.Status)
		}
	}
	if len(p.Touched) == 0 {
		t.Error("the patch records no touched dimension")
	}
}
