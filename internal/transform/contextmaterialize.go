package transform

import (
	"fmt"
	"go/ast"
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Context MATERIALIZATION — the region rewrite P3 modeled and nobody owned
// ────────────────────────────────────────────────────────────────────────
//
// Everything left of the transform already worked for context: `DimContext` is in the closed enum, a
// node's `ContextPolicy` resolves to a versioned registry entry, and it participates in `config_hash`
// structurally. Then `rewriteContext` refused, and its reason named P3 — a phase that shipped the
// policies and their host-side `Assemble` but never the call-site rewrite. **The rewrite had no owner
// in code. P16 is it** (design.md, Context).
//
// # Why this is a REGION rewrite and not an argument swap
//
// Every other rewriter in this package replaces one expression the call site already wrote: a model
// constant, a prompt string. Context is not an argument anywhere, in any language — it is *how the
// surrounding code builds the message list*. Discovery records that as a description
// (`inline_messages`), not a position, precisely because there is nothing to point at. So materializing
// a policy means changing the region that constructs `[]Message`, which is why design.md Decision 2
// calls it the hard part and why it stayed behind a refusal for three phases.
//
// # The one shape this engine will materialize, and why it is not a construction
//
// 🔴 A SELECTION policy — one whose assembly is a SUBSET of the messages the author wrote — can be
// materialized by DELETING the messages it does not retain. That lands the rewrite squarely back
// inside rewrite.go's rule ("replace an expression the call site already wrote"), because a deletion is
// the degenerate case of a replacement, with an empty replacement. It constructs nothing, invents no
// SDK shape, needs no import, and cannot produce a value whose type the author did not already write.
// It is the same argument rewritetools.go makes for a tool prune, applied to the message list.
//
// 🔴 And WHICH messages survive is not this file's opinion. `registry.SelectionPolicy.Retain` is the
// single definition, read here and by the host-side `Assemble` alike. Two implementations of "what does
// sliding-window window to" would be two answers to one question, and the failure mode is the worst
// this platform has: a diff that windows differently than the policy the `config_hash` names, scored as
// that policy.
//
// # What is REFUSED, by name, and why each refusal is a fact rather than a gap
//
//   - `summarization` / `hierarchical-summary` — assembled by CALLING a model, host-side through
//     HostServices, at run time. There is no summary in the source to select, and writing one in would
//     freeze a model's output into a diff and claim a provider call the platform never made.
//   - `rag-retrieval` — assembled by RETRIEVING against the live query at run time. The retrieved chunks
//     are a function of the conversation, not of the source.
//   - `structured-extraction` — rewrites message CONTENT into an extracted form, which means
//     constructing new message values rather than selecting among the ones the author wrote.
//   - every language other than Go — see refuseContext in rewrite.go.
//
// None of those is a silent no-op. A refusal is a correct answer ("not applicable here"); a silent drop
// is an incorrect one, because the override would be resolved, hashed, and scored as the BASE
// configuration under the variant's hash (design.md Decision 1).

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Per-policy materializer coverage — THE single source of truth (NFR7, mirroring skillbind.go)
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// contextFormKind is how one policy relates to a call site's written message list.
type contextFormKind string

const (
	// ctxIdentity: the policy's assembly IS the un-rewritten static message list. `full` and
	// `full-history` pass the conversation through, and a call site that writes its messages out is
	// already doing exactly that — so "no edit" here is a PROOF OF EQUIVALENCE, not an omission.
	ctxIdentity contextFormKind = "identity"
	// ctxSelect: the policy retains a subset of the written messages; the rewrite deletes the rest.
	ctxSelect contextFormKind = "select"
	// ctxNotAtCallSite: the policy assembles at RUN TIME (a model or retriever call) or by rewriting
	// content. There is nothing at the call site to select, so it is refused with the reason below.
	ctxNotAtCallSite contextFormKind = "not-at-call-site"
)

// contextForm is one policy's row: what it does to a written message list, and the sentence a refusal
// and the capability doc must share.
type contextForm struct {
	kind contextFormKind
	// reason states, for a ctxNotAtCallSite row, WHY this policy has no call-site materialization. It is
	// a fact about the policy, not about how much work was done, so it is written once here rather than
	// re-worded at each refusal site.
	reason string
}

// contextForms is the coverage table: policy → how it materializes at a Go call site.
//
// 🔴 It is THE source of truth for per-policy context-materializer coverage. A refusal, the console,
// and any capability doc read ContextMaterializerCoverage(); nothing else may state coverage.
//
// 🚫 A policy ABSENT from this table is not "unimplemented pending effort" — it is a policy this engine
// has no evidence about, and the honest outcome is the refusal below, not a guess at what its assembly
// would look like in source.
var contextForms = map[string]contextForm{
	"full":                {kind: ctxIdentity},
	"full-history":        {kind: ctxIdentity},
	"sliding-window":      {kind: ctxSelect},
	"semantic-compaction": {kind: ctxSelect},
	"summarization": {kind: ctxNotAtCallSite,
		reason: "assembles by CALLING a summarizer model, host-side through HostServices, at run time; " +
			"there is no summary in the source to select, and writing one in would freeze a model's output " +
			"into the diff and claim a provider call this engine never made"},
	"hierarchical-summary": {kind: ctxNotAtCallSite,
		reason: "assembles by CALLING a summarizer model once per tier, host-side through HostServices, at " +
			"run time; its tiers are model output, not source the author wrote"},
	"rag-retrieval": {kind: ctxNotAtCallSite,
		reason: "assembles by RETRIEVING chunks for the live query at run time, host-side; the retrieved " +
			"chunks are a function of the conversation, not of the source, so there is nothing at the call " +
			"site to materialize them from"},
	"structured-extraction": {kind: ctxNotAtCallSite,
		reason: "rewrites message CONTENT into an extracted structure, which means CONSTRUCTING new message " +
			"values rather than selecting among the ones the call site wrote; a constructed message whose " +
			"shape this engine guessed is the failure mode with no downstream net"},
}

// ContextCoverage is one (language, policy) pair and what this engine does with it.
type ContextCoverage struct {
	Language string `json:"language"`
	Policy   string `json:"policy"`
	// Mode is "identity" (equivalent to the un-rewritten call site), "select" (materialized by deleting
	// the messages the policy does not retain), or "not-at-call-site" (refused, with Reason).
	Mode string `json:"mode"`
	// Reason is the refusal sentence for a not-at-call-site policy; empty otherwise.
	Reason string `json:"reason,omitempty"`
}

// ContextMaterializerCoverage reports what each context policy does at a call site, per language,
// sorted. It reads the SAME tables the rewriters refuse from, so a documented capability, a console
// badge, and an emitted refusal cannot drift apart.
//
// The per-policy row is language-INDEPENDENT for everything except the `select` mode: whether a policy
// can be written into source at all is a fact about the policy (a summary does not exist until a model
// writes it), while whether THIS language can perform the selection is a fact about the engine. So a
// language with no splitter reports `select` policies as not-yet-materializable and carries the reason.
func ContextMaterializerCoverage() []ContextCoverage {
	langs := ContextMaterializerLanguages()
	out := make([]ContextCoverage, 0, len(contextForms)*len(langs))
	for _, lang := range langs {
		for name, f := range contextForms {
			out = append(out, ContextCoverage{
				Language: lang, Policy: name, Mode: string(f.kind), Reason: f.reason})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Language != out[j].Language {
			return out[i].Language < out[j].Language
		}
		return out[i].Policy < out[j].Policy
	})
	return out
}

// ContextMaterializerLanguages lists the languages whose SELECTION rewriter has landed, sorted. Go is
// native (contextmaterialize.go); the rest come from the syntactic engines' splitter table
// (contextmaterialize_span.go), so adding a language is adding a splitter and nothing else.
func ContextMaterializerLanguages() []string {
	out := []string{"go"}
	for lang := range spanContextMaterializers {
		out = append(out, lang)
	}
	sort.Strings(out)
	return out
}

// materializablePolicies lists the policies a Go call site CAN materialize, for a refusal that tells the
// reader what would have worked.
func materializablePolicies() []string {
	var out []string
	for _, c := range ContextMaterializerCoverage() {
		if c.Mode == string(ctxSelect) || c.Mode == string(ctxIdentity) {
			out = append(out, c.Policy)
		}
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// The Go rewriter
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// materializeContext rewrites a Go call site's message-assembly region so the assembled message list is
// the one the resolved policy defines (P16 task 2.2).
//
// 🔴 Every path below ends in exactly one of three outcomes, and the enumeration is the guarantee
// (task 2.4 / FR1): an EDIT, a typed REFUSAL, or a stated PROOF OF EQUIVALENCE. There is no fall-through
// that returns "nothing happened" for a policy that differs from what the call site does — that outcome
// is what would let a variant be scored as its base configuration under its own hash.
func materializeContext(site discovery.GoCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimContext)
	entry := o.Context
	if entry == nil {
		// Not reachable through Dimensions() (a nil Context is not an overridden dimension). Checked
		// anyway: proceeding would rewrite against a policy nobody named.
		return nil, unsafeRewrite(site.NodeID, dim,
			"the context dimension was dispatched with no resolved context entry, so there is no policy to materialize")
	}
	policy := entry.Spec.Policy

	form, known := contextForms[policy]
	if !known {
		return nil, unsafeRewrite(site.NodeID, dim,
			"context policy %q has no declared call-site form in this engine, so there is no evidence for what "+
				"its assembly should look like in source; the policies it can materialize are %v. Guessing an "+
				"assembly that compiles is the failure mode with no downstream net", policy, materializablePolicies())
	}

	switch form.kind {
	case ctxIdentity:
		// PROOF OF EQUIVALENCE, not an omission: `full`/`full-history` pass the conversation through, and
		// a call site that writes its messages out already assembles exactly that. There is nothing to
		// change, and saying so is the honest diff.
		return nil, nil
	case ctxNotAtCallSite:
		return nil, unsafeRewrite(site.NodeID, dim,
			"context policy %q %s. It is REFUSED at the call site rather than dropped; the policy still runs "+
				"host-side where it belongs, and the policies this engine materializes into source are %v",
			policy, form.reason, materializablePolicies())
	}

	// ── ctxSelect: delete the messages the policy does not retain ──────────────────────────────────
	sel, ok := entry.Policy.(registry.SelectionPolicy)
	if !ok {
		// The table says "select" but the implementation cannot say what it retains. Fail closed rather
		// than fall back to a local re-derivation, which would be the second answer this file exists to
		// prevent.
		return nil, unsafeRewrite(site.NodeID, dim,
			"context policy %q is declared materializable-by-selection but its implementation does not "+
				"declare which messages it retains, so this engine has no definition to rewrite against", policy)
	}

	cl, err := staticMessageList(site, o)
	if err != nil {
		return nil, err
	}

	// The call site's messages, as the policy sees them. A written element has no role/content split —
	// it is one expression — so it is measured as its own source text. That is deterministic and
	// monotonic in length, which is all the compaction rule needs, and it is measured by the SAME
	// estimator the host-side policy uses.
	msgs := make([]registry.Message, 0, len(cl.Elts))
	for _, el := range cl.Elts {
		msgs = append(msgs, registry.Message{Content: renderExpr(site.File.Fset, el)})
	}

	keep, err := sel.Retain(entry.Spec.Params, msgs)
	if err != nil {
		return nil, unsafeRewrite(site.NodeID, dim,
			"context policy %q cannot select over this call site's %d written message(s): %v",
			policy, len(msgs), err)
	}
	kept := map[int]bool{}
	for _, i := range keep {
		kept[i] = true
	}

	if len(keep) == len(msgs) {
		// PROOF OF EQUIVALENCE again: the window is at least as wide as the conversation the call site
		// writes, so the policy's assembly IS that list. No edit is the correct diff — and it is stated,
		// not fallen into.
		return nil, nil
	}
	if len(keep) == 0 {
		// Assembling an empty context is a worse failure than an over-budget one — the policies themselves
		// refuse to do it (semantic-compaction keeps one message even when it exceeds the target). A
		// selection that emptied the call site's message list is a bug in the params, not a diff to emit.
		return nil, unsafeRewrite(site.NodeID, dim,
			"context policy %q would retain NONE of this call site's %d written message(s), which would "+
				"assemble an empty context; that is a parameter mistake, not a rewrite", policy, len(msgs))
	}

	var edits []edit
	for i, el := range cl.Elts {
		if kept[i] {
			continue
		}
		start, end, err := byteRange(site.File.Fset, el, len(src))
		if err != nil {
			return nil, unsafeRewrite(site.NodeID, dim, "%v", err)
		}
		// The element's list separator goes with it, and the range stops at the newline in both
		// directions — a deletion may not change the file's line count (see rewritetools.go's header for
		// why that rule is load-bearing).
		start, end = absorbSeparator(src, start, end)
		if lineOf(src, start) != lineOf(src, end) {
			return nil, unsafeRewrite(site.NodeID, dim,
				"materializing %q means dropping the message written at %s:%d-%d, and deleting a multi-line "+
					"element would remove line(s) and shift every line below it, which this engine does not do",
				policy, site.FileRel, lineOf(src, start), lineOf(src, end))
		}
		edits = append(edits, edit{Start: start, End: end, New: "", NodeID: site.NodeID, Dim: dim})
	}
	return edits, nil
}

// staticMessageList locates the call site's message-assembly region and insists it is a list the author
// WROTE. Each refusal below names a different way the region is not selectable, because they have
// different fixes: a missing registry locator is a row to add, an opaque body is an SDK that carries its
// messages as bytes, and a runtime-assembled list is a call site no selection can be checked against.
func staticMessageList(site discovery.GoCallSite, o variantspec.ResolvedOverride) (*ast.CompositeLit, error) {
	const dim = string(variantspec.DimContext)
	policy := o.Context.Spec.Policy

	// The registry's prompt locator points at the whole message argument (`params.Messages`) — the
	// message-assembly region itself. Reusing it rather than adding a "context locator" is deliberate:
	// one row, one locator, both readers (discovery/codemod.go's seam argument).
	loc := site.ArgMap.Prompt
	if loc == nil {
		return nil, unsafeRewrite(site.NodeID, dim,
			"the registry row for this call site declares no message locator, so there is no message-assembly "+
				"region to materialize policy %q into", policy)
	}
	if loc.Form == discovery.LocOpaque {
		return nil, unsafeRewrite(site.NodeID, dim,
			"this call site carries its messages inside an opaque serialized body, so the message list policy "+
				"%q would select over is bytes this engine cannot see into", policy)
	}
	expr, ok := discovery.LocateArg(site.Call, loc, site.File.Fset)
	if !ok || expr == nil {
		return nil, unsafeRewrite(site.NodeID, dim,
			"this call site writes no message argument, so policy %q has no message list to assemble; the "+
				"messages reach the SDK some other way and a selection here would select over nothing while "+
				"claiming to", policy)
	}
	cl, isStatic := unwrapValueExpr(expr).(*ast.CompositeLit)
	if !isStatic {
		// 🔴 The same refusal a runtime-assembled tool set gets, for the same reason: a list built at run
		// time has no written elements to select among, and rewriting it would mean guessing what it builds.
		return nil, unsafeRewrite(site.NodeID, dim,
			"this call site assembles its messages at runtime (%s), not as a written list, so policy %q has no "+
				"declared messages to select among; materializing it would mean guessing what the runtime "+
				"assembly contains, and a guess that compiles is the failure mode with no downstream net",
			describeExpr(expr), policy)
	}
	return cl, nil
}

// ContextMaterializes reports whether a resolved context policy has a Go call-site materialization at
// all — the same table the rewriter reads. Exported for the proposal layer and the console, so
// "will this materialize or refuse?" is answered in one place.
func ContextMaterializes(policy string) bool {
	f, ok := contextForms[policy]
	return ok && f.kind != ctxNotAtCallSite
}

// ContextRefusalReason returns why a policy has no call-site materialization, or "" when it has one.
func ContextRefusalReason(policy string) string {
	f, ok := contextForms[policy]
	if !ok {
		return fmt.Sprintf("policy %q has no declared call-site form in this engine", policy)
	}
	return f.reason
}
