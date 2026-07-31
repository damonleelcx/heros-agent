package transform

import (
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// The harness call-site rewriter for Python (P18 §11, decisions.md D-9)
// ─────────────────────────────────────────────────────────────────────
//
// §5 refused every harness change and named what was missing. The runtime is one half
// (internal/harnessruntime); this file is the other: the rewriter that drives it at a call site.
//
// # ONE edit, and what it does
//
//	resp = client.chat.completions.create(model=M, messages=MSGS)
//	  ->
//	resp = agentharness.run("<node>", lambda _m: client.chat.completions.create(model=M, messages=_m), MSGS)
//
// The author's own call survives verbatim inside the lambda, down to the byte, except that the message
// argument becomes the loop's parameter — so each turn is the SAME call with the list the loop built.
// Nothing about the call is synthesized: the wrapper is a prefix, a parameter substitution, and a suffix.
//
// 🔴 It preserves the line count EXACTLY, which is what lets it pass the existing minimality gate rather
// than needing it loosened. The written message text moves from inside the call to after it, so whatever
// newlines it contained are removed once and re-added once: newlines(new) == newlines(old), always. That
// arithmetic is asserted, not assumed (TestPythonHarnessEditIsMinimalAndReparses).
//
// # DRIVE AND DECIDE, or refuse (decisions.md D-9)
//
// A loop is two capabilities, and both must be available or the call site is refused WHOLE:
//
//	DRIVE   the call must be re-invocable as a thunk — it needs its own span, which the analyzer now
//	        records (discovery.SpanCallSite.CallSpan).
//	DECIDE  the loop must be able to build the next turn's input, which means the call must WRITE its
//	        message list. A `**kwargs` call assembles the request elsewhere; there is no list to append
//	        the previous answer to, so the loop could only ever re-ask the identical question — a
//	        fixed-N loop, which is `single-shot` priced N times under another strategy's config_hash.
//
// Emitting the drive half alone is the failure this ordering exists to prevent, and it is always
// available and always sounds like progress. It is not progress: it is the same call, N times, billed.
//
// # The refusal ladder, most-specific first
//
//	identity?                     → no edit, no refusal
//	needs a host service?         → permanent, in every language (harnessHostService)
//	does this language cover it?  → ours; asked before the call site, because without an emitted module
//	                                there is nothing for a rewritten call to call
//	does the call have a span?    → theirs
//	does it write its messages?   → theirs, and it stays true after every rewriter lands

// harnessLoopParam is the lambda parameter the loop passes its message list through. Deliberately
// underscore-prefixed and short: it is generated, it lives inside a single lambda, and a longer name
// would be likelier to collide with something the author wrote in the enclosing scope.
const harnessLoopParam = "_heros_msgs"

// harnessImportName is the module the materialized call site drives. One constant, read by the import
// edit and by the emitted call text, so the two cannot name different modules.
const harnessImportName = "agentharness"

// spanHarnessLoop materializes the loop at a Python call site, or refuses whole.
func spanHarnessLoop(site discovery.SpanCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimHarness)
	strategy := o.Harness.Spec.Strategy

	// DRIVE. The call's own extent, read from the analyzer rather than derived by scanning for balanced
	// parens — which is the guess rewrite_span.go declines and which the memory rewriter named as the
	// clean follow-up this span is.
	call := site.CallSpan
	if call.Start < 0 || call.End <= call.Start || call.End > len(src) {
		return nil, refuseShape(site.NodeID, dim,
			"this call site's own extent was not recorded, so harness strategy %q has no call to re-invoke. "+
				"A loop must wrap the whole call, and this engine will not guess where a call ends",
			strategy)
	}

	// DECIDE. The written message list — both the loop's input and the thing its continuation appends to.
	arg, err := harnessMessagesArg(site, src, dim, strategy)
	if err != nil {
		return nil, err
	}
	if arg.Value.Start < call.Start || arg.Value.End > call.End {
		// The located argument is outside the call this engine believes it is wrapping. Refusing is the
		// only safe answer: splicing on that assumption would corrupt the file.
		return nil, refuseShape(site.NodeID, dim,
			"this call site's message argument lies outside its recorded call extent, so harness strategy "+
				"%q cannot be materialized without guessing which bytes belong to the call", strategy)
	}

	callText := string(src[call.Start:call.End])
	msgText := string(src[arg.Value.Start:arg.Value.End])
	relStart := arg.Value.Start - call.Start
	relEnd := arg.Value.End - call.Start

	// The thunk: the author's call verbatim, with the message argument replaced by the loop's parameter.
	thunk := callText[:relStart] + harnessLoopParam + callText[relEnd:]

	newText := fmt.Sprintf("%s.run(%s, lambda %s: %s, %s)",
		harnessImportName, pyQuote(site.NodeID), harnessLoopParam, thunk, msgText)

	// 🔴 The line-count invariant, checked rather than argued. The arithmetic says newlines are preserved
	// because the message text moved rather than disappeared; if a future change broke that, this is
	// where it stops — before an edit reaches the minimality gate with a worse error.
	if strings.Count(newText, "\n") != strings.Count(callText, "\n") {
		return nil, refuseShape(site.NodeID, dim,
			"materializing harness strategy %q here would change this call's line count (%d newline(s) -> "+
				"%d), which would shift every line below it and break attribution", strategy,
			strings.Count(callText, "\n"), strings.Count(newText, "\n"))
	}

	return []edit{{Start: call.Start, End: call.End, New: newText, NodeID: site.NodeID, Dim: dim}}, nil
}

// harnessMessagesArg locates the written message list, or refuses with the most specific true reason.
//
// 🔴 The DECIDE half's precondition at a call site. A loop's continuation appends the previous answer and
// the strategy's own instruction to the message list; without a written list there is nothing to append
// to, so the only loop that could be emitted is the identical call repeated — which is `single-shot` at
// N times the price, running under a multi-turn config_hash.
func harnessMessagesArg(site discovery.SpanCallSite, src []byte, dim, strategy string) (discovery.ArgValue, error) {
	if u := site.KeywordUnpacking; u != nil {
		if _, written := site.Keywords["messages"]; !written {
			return discovery.ArgValue{}, refuseShape(site.NodeID, dim,
				"this call site passes %s, so the request — including its message list — is assembled "+
					"elsewhere in your program. Harness strategy %q takes another turn by APPENDING the "+
					"previous answer to that list, and there is no written list here to append to; the only "+
					"loop this engine could emit would re-ask the identical question %d times, which is a "+
					"single shot at %d times the price under a multi-turn name. Write the messages at this "+
					"call site, or apply the strategy where the mapping is built",
				u.Text, strategy, 2, 2)
		}
	}

	arg, ok := site.Keywords["messages"]
	if !ok {
		return discovery.ArgValue{}, refuseShape(site.NodeID, dim,
			"this call site names no `messages` argument, so harness strategy %q has nothing to append the "+
				"previous answer to. A loop augments a written message list; a call that does not write one "+
				"can only repeat itself", strategy)
	}
	// 🔴 A WRITTEN LIST, checked at the syntactic floor. The loop concatenates onto whatever it is given,
	// so wrapping a non-list emits code that parses and then type-errors at run time — a diff that passes
	// review and breaks the customer's agent.
	if val := strings.TrimSpace(string(src[arg.Value.Start:arg.Value.End])); !strings.HasPrefix(val, "[") {
		return discovery.ArgValue{}, refuseShape(site.NodeID, dim,
			"this call site's `messages` argument is `%s` rather than a written list, so harness strategy "+
				"%q has no list to append the previous answer to. Wrapping a non-list would emit code that "+
				"parses and then fails at run time. Write the messages as a list literal here, or apply the "+
				"strategy where the list is built",
			truncateForMessage(val, 60), strategy)
	}
	return arg, nil
}

// harnessImportEdit builds the single import line a materialized Python file needs, or refuses.
//
// The anchor rule is pyMemoryImportEdit's, for the same two reasons: above a module DOCSTRING an import
// silently stops the docstring being `__doc__`, and above a `from __future__` import the file does not
// compile. So the line goes immediately before the first top-level non-future import.
func harnessImportEdit(src []byte, nodeID, dim string) (edit, error) {
	lines := strings.Split(string(src), "\n")
	offset := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isImport := strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "from ")
		topLevel := len(line) == len(strings.TrimLeft(line, " \t"))
		future := strings.HasPrefix(trimmed, "from __future__")
		if isImport && topLevel && !future {
			return edit{
				Start: offset, End: offset,
				New:    "import " + harnessImportName + "\n",
				NodeID: nodeID, Dim: dim, Import: true,
			}, nil
		}
		offset += len(line) + 1
	}
	return edit{}, refuseShape(nodeID, dim,
		"this file declares no top-level import, so there is no position where `import %s` is certainly "+
			"legal: above a module docstring it would silently stop being one, and above a `__future__` "+
			"import it would not compile. A file with no imports is also not calling an SDK, so there is "+
			"nothing here for a harness to wrap",
		harnessImportName)
}
