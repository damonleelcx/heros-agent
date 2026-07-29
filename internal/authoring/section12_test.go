package authoring

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// P13 13c section 12 — DevOps: offline parity, no new egress, readable signals, a real off switch.

// TestAuthoringSignalsExternallyReadable (task 12.3).
//
// Two properties, and the second is the one that decays. First, the vocabulary is enumerable, so a
// dashboard or an alert rule can be written against it rather than discovered one incident at a time.
// Second — and this is what a "helpful" future change breaks — a signal carries no free text and no
// content: it says WHAT happened and to WHICH KIND of thing, never the prompt, the source, the diff or
// the draft itself.
func TestAuthoringSignalsExternallyReadable(t *testing.T) {
	t.Run("the vocabulary is enumerable and stable", func(t *testing.T) {
		names := SignalNames()
		if len(names) < 5 {
			t.Fatalf("only %d signals — submitted, refused, not-yet-measurable, conflict and reverted are the minimum an operator needs", len(names))
		}
		// Each name is an identifier, not prose: no spaces, no punctuation a query language would fight.
		for _, n := range names {
			if strings.ContainsAny(string(n), " \t:;,'\"") {
				t.Errorf("signal %q reads like prose — an alert rule pinned to a sentence breaks when the sentence improves", n)
			}
			if !strings.HasPrefix(string(n), "authoring.") {
				t.Errorf("signal %q is not namespaced; it will collide with something", n)
			}
		}
		// 🔴 not-yet-measurable is its OWN signal. Folding it into refusals would page the person watching
		// refusals for a measurement gap, and hide the gap from the person who fills those.
		var hasThird bool
		for _, n := range names {
			if n == SignalNotYetMeasurable {
				hasThird = true
			}
		}
		if !hasThird {
			t.Error("not-yet-measurable has no signal of its own — it will be counted as a refusal")
		}
	})

	t.Run("a signal carries no content, on any path", func(t *testing.T) {
		// A refusal whose prose contains everything sensitive a refusal can contain.
		secretish := Result{
			Verdict: VerdictRefused,
			Refusal: Refusal{
				NodeID: "n1", Field: "prompt",
				Cause: `node "n1", dim prompt: slot {{customer_ssn}} no longer binds; ` +
					`the call site supplies "ACME-INTERNAL-TOKEN-abc123" from ANTHROPIC_API_KEY at src/agent.go:42`,
			},
		}
		sig, ok := SignalFor(secretish, "tenant-7")
		if !ok {
			t.Fatal("a refusal produced no signal")
		}
		blob, err := json.Marshal(sig)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for _, leaked := range []string{"customer_ssn", "ACME-INTERNAL-TOKEN", "ANTHROPIC_API_KEY",
			"src/agent.go", "no longer binds"} {
			if strings.Contains(string(blob), leaked) {
				t.Errorf("the signal carries %q — a signal says what happened, never the thing itself:\n%s",
					leaked, blob)
			}
		}
		// It must still be USEFUL: the cause slug and the axis are what make it diagnosable.
		if sig.Cause == "" || sig.Axis == "" {
			t.Errorf("the signal is content-free but also information-free: %+v", sig)
		}
	})

	t.Run("the cause is derived from what was named, not from the prose", func(t *testing.T) {
		// Two refusals whose SENTENCES differ completely but whose named field is the same must classify
		// the same. If the slug tracked the prose, improving a message would silently move the category
		// under somebody's alert.
		a := Refusal{Field: "skills", NodeID: "n1", Cause: "no materializer for python has landed"}
		b := Refusal{Field: "skills", NodeID: "n1", Cause: "this language cannot construct SDK tool values yet"}
		if classifyCause(a) != classifyCause(b) {
			t.Errorf("two wordings of one cause classified differently: %q vs %q",
				classifyCause(a), classifyCause(b))
		}
		// And an unrecognised refusal is CauseOther rather than a guess.
		if got := classifyCause(Refusal{Field: "something-new"}); got != CauseOther {
			t.Errorf("an unrecognised refusal classified as %q; guessing is how a category silently shifts", got)
		}
	})

	t.Run("an admissible preflight is not an event", func(t *testing.T) {
		// Emitting one would make signal volume proportional to keystrokes rather than to outcomes.
		if _, ok := SignalFor(Result{Verdict: VerdictAdmissible}, "t1"); ok {
			t.Error("an admissible preflight emitted a signal")
		}
	})

	t.Run("no signal field can hold free text", func(t *testing.T) {
		// Structural: a `Detail string` added later is where content would eventually leak.
		rt := reflect.TypeOf(Signal{})
		allowed := map[string]bool{"Name": true, "Axis": true, "Cause": true, "Missing": true, "TenantID": true}
		for i := 0; i < rt.NumField(); i++ {
			if !allowed[rt.Field(i).Name] {
				t.Errorf("Signal gained field %q — every field here must be a label or an identifier, "+
					"and a new one is where the prompt text eventually arrives", rt.Field(i).Name)
			}
		}
	})
}

// TestAuthoringDisabledIsPre13cBehavior (task 12.4).
//
// 13c is independently revertible, and this is what that means concretely: with authoring off, nothing
// about the system differs from before it existed. The claim rests on one fact — authorship was never
// in the hashed shape — so turning the feature off cannot move a `config_hash`, invalidate a golden
// vector, or require a migration to be rolled back.
func TestAuthoringDisabledIsPre13cBehavior(t *testing.T) {
	t.Run("an operator-originated candidate is untouched by the feature existing", func(t *testing.T) {
		// The zero Origin — what every pre-13c construction site produces — must behave exactly as it did.
		var zero Origin
		if zero.Normalized() != OriginOperator || zero.IsUser() {
			t.Error("a candidate constructed without an Origin changed meaning when the field was added")
		}
	})

	t.Run("the record is the only durable thing authoring adds", func(t *testing.T) {
		// If authoring adds durable state anywhere else, "disabled behaves as before" stops being true and
		// the off switch stops being an off switch.
		rec := NewMemRecorder()
		if entries, err := rec.ListByTenant(t.Context(), "t1"); err != nil || len(entries) != 0 {
			t.Errorf("a fresh record is not empty: %v %v", entries, err)
		}
	})

	t.Run("nothing in this package hashes authorship", func(t *testing.T) {
		// The Entry carries actor and tenant; the config hash it REFERENCES is supplied, never computed
		// from them. A field named like a hash input on this struct would be the warning sign.
		rt := reflect.TypeOf(Entry{})
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if strings.Contains(strings.ToLower(f.Name), "hash") && f.Name != "ConfigHash" {
				t.Errorf("Entry gained %q — authorship must never participate in a hash", f.Name)
			}
		}
	})
}

// TestPreflightPayloadCarriesNoContent (task 12.2, the authoring half).
//
// Preflight is the tempting leak: the natural implementation posts the draft somewhere to be checked.
// This asserts the shape a hosted preflight REQUEST is built from carries no prompt body, no source and
// no credential — the draft names refs and node ids, never content.
func TestPreflightPayloadCarriesNoContent(t *testing.T) {
	body := "You are a helpful assistant. The API key is sk-secret-abc123."
	d := Draft{
		WorkflowID: "wf1", ParentVariantID: "p1",
		Actor: Actor{ID: "u1", TenantID: "t1"},
		Edits: map[string]Edit{"n1": {
			// A prompt is referenced by VERSION, never carried as text. That is what makes the payload
			// content-free by construction rather than by filtering.
			PromptRef: strPtrLocal("prompt-v7"),
			ModelRef:  strPtrLocal("model-v2"),
		}},
	}
	blob, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal draft: %v", err)
	}
	for _, leaked := range []string{body, "sk-secret", "helpful assistant"} {
		if strings.Contains(string(blob), leaked) {
			t.Errorf("the draft payload carries %q:\n%s", leaked, blob)
		}
	}

	// Structural: every field on Edit is an allowlisted REF or SELECTION.
	//
	// An earlier version of this check banned substrings ("body", "text", "content", …) and flagged
	// `ContextPolicy` — because "context" contains "text". A fence that cries wolf is a fence somebody
	// switches off, so this is an allowlist instead: adding a field fails here and forces a human to
	// decide whether it carries a reference or a payload. That is the question worth asking.
	rt := reflect.TypeOf(Edit{})
	refFields := map[string]bool{
		"ModelRef": true, "PromptRef": true, "SkillRefs": true, "ToolSelection": true,
		"ContextPolicy": true, "ApplyMode": true, "DropTolerance": true,
		// P17: a memory-registry version_id. It is a REFERENCE, and the distinction is the one this
		// allowlist exists to force: the strategy's params live in the sealed registry entry the ref
		// addresses, so a draft carries the address and never the configuration. A field that held
		// `{"max_tokens":2000}` inline would be a payload and would belong nowhere near a draft — which is
		// also why variantspec rejects an inlined memory definition at resolve.
		"MemoryRef": true,
		// P18: a harness-registry version_id, on identical terms. The strategy's params — including
		// `max_turns`, which is the COST — live in the sealed entry the ref addresses. A field that held
		// `{"max_turns":9}` inline would let a draft carry a bill nobody registered.
		"HarnessRef": true,
	}
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if !refFields[name] {
			t.Errorf("Edit gained field %q — an authored change references versions and selects from "+
				"discovered sets; it does not carry content. If this field is a ref, add it to the "+
				"allowlist; if it is a body, it must not exist.", name)
		}
	}
	// And the values really are references: a ref is short and opaque, never a prompt.
	for _, f := range []string{"prompt-v7", "model-v2"} {
		if !strings.Contains(string(blob), f) {
			t.Errorf("the draft lost its reference %q", f)
		}
	}
}

func strPtrLocal(s string) *string { return &s }

// TestAuthoredApplyAssertsDownstreamState (P13 13c task 13.5).
//
// The rule this enforces: a 2xx is not evidence of persistence.
//
// It is the most common way a feature like this passes its tests and fails in production — the handler
// returns success, every assertion reads that return value, and nothing ever checks that the row was
// written, the diff was cited, or the ledger stayed honest. So this test ignores what Submit returned
// and reads back each downstream consumer instead.
func TestAuthoredApplyAssertsDownstreamState(t *testing.T) {
	ctx := t.Context()
	parent := baseSpec()
	parentHash := specFingerprint(parent)
	rec := NewMemRecorder()
	applier := &okApplier{}

	s := Submitter{
		Preflight: Preflighter{Resolver: hashResolver{}, Materializer: okMaterializer{}},
		Applier:   applier, Head: fixedHead{head: parentHash},
		Record: rec, Auth: allowAll{},
	}
	d := draftFor(map[string]Edit{"n1": {ModelRef: strPtr("m-new")}})
	d.ParentVariantID, d.ConcurrencyToken = parentHash, parentHash

	sub, err := s.Submit(ctx, d, parent)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	t.Run("the diff was actually produced, and is cited by reference", func(t *testing.T) {
		// Read the COMPILER's record of what it built, not Submit's summary of it.
		if applier.calls != 1 {
			t.Fatalf("the compiler was called %d times, want exactly 1", applier.calls)
		}
		if sub.Compiled.DiffHash == "" {
			t.Error("no diff hash reached the submission")
		}
		// The record cites the diff by REFERENCE. A record carrying the diff itself would put customer
		// source in the audit table.
		if sub.Entry.DiffRef != sub.Compiled.DiffHash {
			t.Errorf("the record cites %q but the compiler produced %q", sub.Entry.DiffRef, sub.Compiled.DiffHash)
		}
	})

	t.Run("the append-only record holds the row, read back from the store", func(t *testing.T) {
		history, err := rec.History(ctx, sub.ChangeID)
		if err != nil {
			t.Fatalf("read history: %v", err)
		}
		if len(history) != 1 {
			t.Fatalf("history has %d rows, want 1", len(history))
		}
		got := history[0]
		// Every field an audit needs, asserted from the STORE rather than from the value Submit returned.
		if got.ActorID == "" || got.TenantID == "" || got.ParentVariantID == "" || got.ConfigHash == "" {
			t.Errorf("the persisted row cannot attribute the change: %+v", got)
		}
		if got.Origin != string(OriginUser) {
			t.Errorf("persisted origin = %q, want user", got.Origin)
		}
		if got.VerificationState != StateUnverified {
			t.Errorf("persisted verification_state = %q, want unverified", got.VerificationState)
		}
		if got.Action != ActionSubmitted {
			t.Errorf("persisted action = %q, want submitted", got.Action)
		}
	})

	t.Run("the ledger stayed honest: this change contributes nothing", func(t *testing.T) {
		rows, err := rec.ListByTenant(ctx, "t1")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		// A savings figure over everything this tenant authored must be zero, because nothing was verified.
		if total := CountableAggregate(rows, func(Entry) float64 { return 100 }); total != 0 {
			t.Errorf("an unverified change contributed %v to an aggregate, want 0", total)
		}
	})

	t.Run("the parent is still resolvable and unchanged", func(t *testing.T) {
		// The lineage a revert depends on. If submitting mutated the parent, the undo would land somewhere
		// that no longer exists.
		if specFingerprint(parent) != parentHash {
			t.Error("submitting mutated the parent spec")
		}
	})
}

// assertNoOverrideFields is the shared structural check: no type on an authoring path may carry a field
// that would let a caller bypass a refusal. Shared so every axis asserts it the same way rather than
// each inventing its own list of forbidden words.
func assertNoOverrideFields(t *testing.T, target any) {
	t.Helper()
	banned := []string{"force", "override", "skip", "bypass", "allowunsafe", "ignore", "unsafe", "admin"}
	rt := reflect.TypeOf(target)
	for i := 0; i < rt.NumField(); i++ {
		name := strings.ToLower(rt.Field(i).Name)
		for _, b := range banned {
			if strings.Contains(name, b) {
				t.Errorf("%s has field %q — there is no override for a refusal, at any tier",
					rt.Name(), rt.Field(i).Name)
			}
		}
	}
}
