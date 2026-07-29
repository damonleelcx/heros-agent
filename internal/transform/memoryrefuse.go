package transform

import (
	"strconv"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// The memory dimension's INTERIM REFUSAL (P17 tasks 7.1–7.4, decisions.md D4).
//
// # Why one file, shared by both engines
//
// The reason memory cannot be materialized is not a fact about Go, or about tree-sitter, or about any
// SDK. It is a fact about what a memory strategy IS: a store the surrounding program reads and writes
// BETWEEN invocations. There is no expression at the call site that holds it — there is not even a
// region, which is what context assembly at least had (contextmaterialize.go). Materializing one means
// introducing a store, a lifetime, a key scheme, and read/write points across code the user owns, in
// whatever framework they own it in. That is a memory RUNTIME plus a codemod, not a value replacement.
//
// So both engines refuse, through this one implementation. Two copies of the sentence would be two
// things to keep true (禁止分裂 source-of-truth), and — worse — they could drift into disagreeing about
// WHICH dimension is refused, which is the one thing a caller branches on.
//
// # Why a refusal and not a silent drop — the failure this prevents
//
// 🔴 A silently-dropped memory override is the worst outcome this system can produce, and it is worth
// naming precisely. The spec would resolve. The transform would "succeed" and emit a diff for the node's
// OTHER dimensions. The build would pass. The eval would run. And the score would be filed under a
// `config_hash` that claims a memory strategy the source never had — a FALSE MEASUREMENT, not a missing
// feature, poisoning the verified-delta ledger every later decision reads. The user would discover it,
// if ever, as someone puzzling over why a memory change had no effect.
//
// Refusing loudly costs a user one clear sentence. Dropping silently costs the platform its evidence.
//
// # Why `none` is NOT refused
//
// The identity strategy changes nothing, so there is nothing to materialize and nothing to refuse. A
// node that explicitly selects `none` produces no edits and no error — the same outcome as a node that
// never mentioned memory, which is exactly what "none ≡ absent" (decisions.md D3) means at the transform
// layer. Refusing it would be refusing a no-op, which would make the identity strategy unusable and would
// draw the refusal boundary on "was memory mentioned" rather than on "would anything have to change".

// rewriteMemory is the dispatch table's entry for the memory dimension in the Go AST engine (task 7.1).
// It applies `none` as the no-op it is and refuses everything else.
func rewriteMemory(site discovery.GoCallSite, _ []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	return refuseMemory(site.NodeID, "go", o)
}

// spanRewriteMemory is the same entry for every tree-sitter language (task 7.2). Its existence — rather
// than an absent table entry — is the point: an absent entry would fall through to engines.go's generic
// "no rewriter for this dimension", which is CauseCallSiteShape and reads as "your call site cannot carry
// this". That would be false. The call site is fine; the PLATFORM has not built the artifact, which is
// CauseNoMaterializer, and only that class may name work we owe.
func spanRewriteMemory(site discovery.SpanCallSite, _ []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	return refuseMemory(site.NodeID, site.Language, o)
}

// refuseMemory is the one refusal, for both engines.
//
// It returns (nil, nil) — no edits, no error — for the identity strategy, and a typed *RewriteError for
// everything else. The language is named in the message but does NOT change the outcome: unlike skills
// and context, where a per-language materializer landing flips a cell from refused to materialized, no
// language has one here, and saying "your language's rewriter is pending" would imply another one's is
// not. What is pending is the memory runtime, and it is pending everywhere.
func refuseMemory(nodeID, language string, o variantspec.ResolvedOverride) ([]edit, error) {
	// No override at all: nothing to do. Reachable only if a caller dispatched the dimension without the
	// override that names it — defensive, and it fails in the safe direction (no edit, no claim).
	if o.Memory == nil {
		return nil, nil
	}
	// `none` is the identity. No edits, no error — the same observable outcome as no override, which is
	// what D3 requires at this layer.
	if o.Memory.IsNone() {
		return nil, nil
	}

	strategy := strconv.Quote(o.Memory.Spec.Strategy)
	return nil, refuseNoMaterializer(nodeID, string(variantspec.DimMemory),
		"memory strategy %s is a store this node would read and write BETWEEN invocations, so there is no "+
			"expression — and no region — at this %s call site that holds it: materializing one means "+
			"introducing a store, a lifetime, a key scheme, and read/write points across code you own. "+
			"That is a memory runtime plus a codemod, and neither has landed in ANY language, so this "+
			"override is REFUSED rather than dropped. Your configuration is still real: it resolves, it "+
			"hashes, and it materializes unchanged once the rewriter lands. What it does not do is reach "+
			"your source today",
		strategy, languageDisplay(language))
}
