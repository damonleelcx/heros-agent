package transform

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"strconv"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// The rule that decides what this engine will and will not rewrite
// ────────────────────────────────────────────────────────────────
// A dimension is rewritable when the configured value can REPLACE AN EXPRESSION THE CALL SITE
// ALREADY WROTE, without synthesizing any SDK-shaped code around it.
//
// That is the whole rule, and the word "already" is what carries it. It covers:
//
//   - the model argument (`Model: anthropic.ModelClaudeOpus4_6`) — discovery points at one
//     expression; swapping it changes exactly the configured dimension.
//   - the prompt TEXT inside a message construction
//     (`Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("…"))}`).
//     The construction is NOT synthesized — it stays exactly as the author wrote it, down to the
//     byte. Only the string expression inside it is replaced, string-for-string. That is why this
//     needs no per-SDK knowledge and cannot break a build: a string literal swapped for a string
//     literal type-checks wherever the original did.
//
// It does NOT cover a dimension the call site does not already express, because supplying it means
// CONSTRUCTING SDK-shaped code from nothing: a `Tools:` slice of `anthropic.ToolParam{Name: …,
// InputSchema: …}` values, whose shape differs per SDK and per SDK version. Getting that subtly
// wrong is precisely ADR-001's named top risk ("a bad codemod can break a build or subtly change
// behavior"), and — worse for an eval platform — the wrong-but-compiling version degrades quality
// invisibly. Emitting a diff we cannot stand behind is worse than emitting none.
//
// So those reject with ErrUnsafeRewrite, which is not a gap being papered over — it is the outcome
// FR5 specifies: "reject ... a call site the transform cannot rewrite safely — before any transform
// is generated". The spec anticipated exactly this case and asked for a loud refusal.
//
// The corollary the refusals below all follow: when the call site is AMBIGUOUS about which
// expression the configured value replaces, refuse. Picking one is a guess, and a guess that
// compiles is the failure mode with no downstream net.

// rewriter turns one node's one overridden dimension into byte edits at its call site.
type rewriter func(site discovery.GoCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error)

// rewriters is the per-dimension table. A dimension absent from an override is never looked up here,
// which is how FR2's independence stays mechanical: there is no code path from "model was overridden"
// to "the prompt got touched".
var rewriters = map[variantspec.Dimension]rewriter{
	variantspec.DimModel:   rewriteModel,
	variantspec.DimPrompt:  rewritePrompt,
	variantspec.DimSkills:  rewriteSkills,
	variantspec.DimContext: rewriteContext,
	variantspec.DimTools:   rewriteTools,
}

// rewriteModel rewrites the model argument to the override's model id.
//
// Two shapes, both normal:
//   - the field is written (`MessageNewParams{Model: X}`) — replace X's bytes.
//   - the field is absent (`MessageNewParams{}`) — insert `Model: "..."` into the literal. A call site
//     that never pinned a model still has a model (the SDK's default), so "absent" is a thing to
//     override, not a reason to refuse.
func rewriteModel(site discovery.GoCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimModel)
	entry := o.Model

	// A provider swap cannot be a single-argument rewrite: an anthropic SDK call site does not become
	// an OpenAI call by changing its model string — the client, the params type, and the response
	// shape are all different. ADR-002 ratifies this refusal: the provider gateway is the call path
	// for models the PLATFORM invokes, while the user's transformed program keeps its own SDK calls,
	// so a cross-provider swap at a user call site means rewriting the SDK call itself — a real
	// codemod, deliberately out of P2 scope, not something the gateway makes transparent.
	//
	// Refusing loudly here is what stops a spec from producing a diff that compiles and then talks to
	// the wrong provider.
	if hint := providerHintFor(site); hint != "" && entry.Spec.Provider != hint {
		return nil, unsafeRewrite(site.NodeID, dim,
			"call site is an %s SDK call but the override selects provider %q; swapping providers means "+
				"rewriting the SDK call itself (different client, params type, and response shape), which "+
				"this engine does not do (ADR-002)", hint, entry.Spec.Provider)
	}

	loc := site.ArgMap.Model
	if loc == nil {
		return nil, unsafeRewrite(site.NodeID, dim,
			"the registry row for this call site declares no model locator, so there is no argument to rewrite")
	}

	fset := site.File.Fset
	newLit := strconv.Quote(entry.Spec.ModelID)

	if expr, ok := discovery.LocateArg(site.Call, loc, fset); ok && expr != nil {
		start, end, err := byteRange(fset, expr, len(src))
		if err != nil {
			return nil, unsafeRewrite(site.NodeID, dim, "%v", err)
		}
		return []edit{{Start: start, End: end, New: newLit, NodeID: site.NodeID, Dim: dim}}, nil
	}

	// Absent field: insert it into the options struct literal.
	cl := discovery.FindStructArg(site.Call)
	if cl == nil {
		return nil, unsafeRewrite(site.NodeID, dim,
			"the model argument is not present and this call site has no options struct to add it to")
	}
	fieldName := lastSegment(loc.Field)
	if fieldName == "" {
		return nil, unsafeRewrite(site.NodeID, dim, "the registry row's model locator names no field")
	}
	ins, err := insertStructField(fset, cl, src, fieldName, newLit)
	if err != nil {
		return nil, unsafeRewrite(site.NodeID, dim, "%v", err)
	}
	ins.NodeID, ins.Dim = site.NodeID, dim
	return []edit{ins}, nil
}

// rewritePrompt rewrites the prompt TEXT at the call site to the override's template.
//
// # What it rewrites, and what it deliberately leaves alone
//
// The registry's prompt locator points at the whole prompt argument — for every real Go SDK that is
// a message CONSTRUCTION (`[]anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(
// "…"))}`), not a bare string. This rewriter does not touch the construction. It descends into it,
// finds the one string-valued expression inside, and replaces just that. The message slice, the role
// helper, the SDK types and every byte of their spelling survive untouched, which is what makes the
// rewrite type-preserving by construction: a string expression replaced by a string expression
// type-checks exactly where the original did, on any SDK, without this engine knowing any of them.
//
// # Slots bind to the call site's own expressions, and are VERIFIED, never guessed
//
// A slot is a RUNTIME value; a Variant Spec pins a template VERSION and carries no bindings, so
// there is no string to render a slot from at codemod time — and inventing one would ship a prompt
// with a hole in it. But the call site already supplies those values as Go expressions
// (`"Triage this ticket: " + ticket`), so the slot's binding is the expression already there.
//
// The match is by name and is CHECKED against the call site: slot `{{ticket}}` binds to the operand
// whose source text is exactly `ticket`. Anything less than an exact 1:1 match between the
// template's slots and the call site's runtime operands is refused. In particular, positional
// matching is not used and must not be: the template's slot order need not match the call site's
// operand order, so binding by position would silently swap two values into each other's holes —
// a diff that compiles and quietly corrupts every eval run under it.
func rewritePrompt(site discovery.GoCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimPrompt)
	entry := o.Prompt

	loc := site.ArgMap.Prompt
	if loc == nil {
		return nil, unsafeRewrite(site.NodeID, dim,
			"the registry row for this call site declares no prompt locator, so there is no argument to rewrite")
	}
	// An opaque body (Bedrock InvokeModel's serialized `Body`) is not an expression at all — the
	// prompt is bytes inside a blob the codemod cannot see into. Say that, rather than let it fall
	// through to the generic "not present", which would send the user looking for a missing argument.
	if loc.Form == discovery.LocOpaque {
		return nil, unsafeRewrite(site.NodeID, dim,
			"this call site carries its prompt in an opaque serialized body, so there is no prompt "+
				"expression at the call site to rewrite")
	}
	expr, ok := discovery.LocateArg(site.Call, loc, site.File.Fset)
	if !ok || expr == nil {
		return nil, unsafeRewrite(site.NodeID, dim, "the prompt argument is not present at this call site")
	}

	text, err := solePromptText(expr)
	if err != nil {
		return nil, unsafeRewrite(site.NodeID, dim, "%v", err)
	}
	newExpr, err := promptExprFor(site.File.Fset, entry, text)
	if err != nil {
		return nil, unsafeRewrite(site.NodeID, dim, "%v", err)
	}

	start, end, err := byteRange(site.File.Fset, text.expr, len(src))
	if err != nil {
		return nil, unsafeRewrite(site.NodeID, dim, "%v", err)
	}
	return []edit{{Start: start, End: end, New: newExpr, NodeID: site.NodeID, Dim: dim}}, nil
}

// promptText is the one string-valued expression inside a call site's prompt construction, split
// into the parts a rewrite has to reason about.
type promptText struct {
	// expr is the whole string-valued expression — the byte range a rewrite replaces.
	expr ast.Expr
	// operands are its non-literal parts, in source order: the runtime values the surrounding code
	// concatenates into the prompt. These are the only expressions a slot may bind to.
	operands []ast.Expr
}

// solePromptText finds the single string-valued expression inside a prompt argument.
//
// "Single" is the safety property, not an implementation limit. The registry says WHERE the prompt
// is; it does not say which string inside a message list is the one a template replaces — and
// nothing else does either. So when a call site offers more than one candidate (a two-turn
// conversation, a system message plus a user message), the only honest answers are "refuse" and
// "guess". This refuses.
//
// It finds exactly one on every real SDK shape, and not by luck: real SDKs carry the message ROLE in
// a function (`NewUserMessage`) or a typed constant (`openai.ChatMessageRoleUser`), never a bare
// string literal, so a single-turn construction contains exactly one string — the prompt.
func solePromptText(arg ast.Expr) (promptText, error) {
	var found []ast.Expr
	ast.Inspect(arg, func(n ast.Node) bool {
		e, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		if !isStringValued(e) {
			return true // not it — keep descending into the construction
		}
		found = append(found, e) // maximal: do not descend into a match and find its own literals
		return false
	})

	switch len(found) {
	case 1:
		leaves := flattenConcat(found[0])
		var operands []ast.Expr
		for _, l := range leaves {
			if !isStringLit(l) {
				operands = append(operands, l)
			}
		}
		return promptText{expr: found[0], operands: operands}, nil
	case 0:
		return promptText{}, fmt.Errorf(
			"the prompt at this call site (%s) contains no string expression to rewrite; its text is "+
				"assembled somewhere this codemod cannot see", describeExpr(arg))
	default:
		return promptText{}, fmt.Errorf(
			"the prompt at this call site is built from %d separate string expressions (a multi-turn "+
				"message list); a prompt entry names one text and cannot say which of them it replaces",
			len(found))
	}
}

// promptExprFor renders the override's template into the Go expression that will replace the call
// site's prompt text.
func promptExprFor(fset *token.FileSet, entry *registry.PromptEntry, text promptText) (string, error) {
	operands := make([]string, 0, len(text.operands))
	for _, o := range text.operands {
		operands = append(operands, renderExpr(fset, o))
	}
	slots := entry.Template.Slots()

	// A slotless template is a fixed prompt. If the call site feeds runtime values into its prompt,
	// applying one would DROP them — the request would silently stop carrying the ticket, the user's
	// question, whatever it was, and still return a plausible completion that still gets scored.
	// That is a silent eval corruption, so it is a refusal, not a rewrite.
	if len(slots) == 0 {
		if len(operands) > 0 {
			return "", fmt.Errorf(
				"prompt %q declares no slots, but this call site feeds runtime value(s) %s into its "+
					"prompt; rewriting it to a fixed string would silently discard them",
				entry.Name, strings.Join(operands, ", "))
		}
		rendered, err := entry.Template.Render(nil)
		if err != nil {
			return "", fmt.Errorf("render prompt %q: %v", entry.Name, err)
		}
		return strconv.Quote(rendered), nil
	}

	if len(operands) == 0 {
		return "", fmt.Errorf(
			"prompt %q declares slot(s) %v, but this call site's prompt is a fixed string with no "+
				"runtime value to bind them to", entry.Name, slots)
	}

	// Every slot must match exactly one operand, and every operand must be claimed by a slot. The
	// second half matters as much as the first: an unclaimed operand is a runtime value the rewrite
	// would drop on the floor.
	used := make([]bool, len(operands))
	for _, s := range slots {
		matches := 0
		for i, op := range operands {
			if op == s {
				matches++
				used[i] = true
			}
		}
		if matches != 1 {
			return "", fmt.Errorf(
				"prompt %q's slot {{%s}} matches %d of this call site's runtime value(s) %s; a slot "+
					"binds to the call-site expression spelled exactly like it, and this engine will not "+
					"guess which value belongs in a slot",
				entry.Name, s, matches, strings.Join(operands, ", "))
		}
	}
	for i, op := range operands {
		if !used[i] {
			return "", fmt.Errorf(
				"this call site feeds runtime value %s into its prompt, but no slot of prompt %q "+
					"(slots %v) binds it; rewriting would silently discard it", op, entry.Name, slots)
		}
	}

	// Build the concatenation from the template's own segment order — the body's structure, not the
	// sorted slot set, decides where each value lands.
	var parts []string
	for _, seg := range entry.Template.Segments() {
		if seg.Slot == "" {
			parts = append(parts, strconv.Quote(seg.Literal))
		} else {
			parts = append(parts, seg.Slot) // verified above to be exactly one call-site operand
		}
	}
	return strings.Join(parts, " + "), nil
}

// isStringValued reports whether an expression is one this engine treats as "the prompt text": a
// string literal, or a `+` concatenation that has one somewhere inside it.
//
// A concatenation qualifies as a whole rather than as its parts because its literals and its runtime
// operands are one prompt, and a rewrite has to replace all of it at once — replacing only the
// literal half of `"Triage: " + ticket` would leave a dangling `+ ticket` welded onto the new text.
func isStringValued(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.BasicLit:
		return x.Kind == token.STRING
	case *ast.BinaryExpr:
		return x.Op == token.ADD && containsStringLit(x)
	}
	return false
}

func isStringLit(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

func containsStringLit(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		if found {
			return false
		}
		if lit, ok := x.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			found = true
			return false
		}
		return true
	})
	return found
}

// flattenConcat returns a `+` chain's leaves in source order. A non-chain is its own only leaf.
func flattenConcat(e ast.Expr) []ast.Expr {
	if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.ADD {
		return append(flattenConcat(b.X), flattenConcat(b.Y)...)
	}
	return []ast.Expr{e}
}

// renderExpr prints an expression back to source text. Used to compare a call-site operand against a
// slot name and to reuse it verbatim in the rewritten expression.
func renderExpr(fset *token.FileSet, e ast.Expr) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, e); err != nil {
		return ""
	}
	return b.String()
}

// rewriteSkills MATERIALIZES a bound skill at a Go call site (P14 task 2.1, decisions.md D-14.4).
//
// Go is the first — and, in 14a, the only — language whose skill refusal is replaced by construction.
// The construction itself, the per-provider coverage table it is gated on, and the argument this whole
// change rests on live in skillbind.go; this function is the dispatch table's entry for the skills
// dimension and nothing more.
//
// 🔴 It still refuses, by name, for a Go call site whose PROVIDER has no declared tool-value form, and
// for a tool set the call site assembles at runtime. "Go is supported" is not "every Go call site is
// supported", and a refusal must say which half is missing.
func rewriteSkills(site discovery.GoCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	return materializeSkills(site, src, o)
}

// refuseSkills is the INTERIM refusal, for every language whose materializer has not landed (P14 task
// 2.2, decisions.md D-14.3). Today that is every tree-sitter language; Go no longer reaches it.
//
// One implementation, shared by every span engine, because the reason is not a fact about any one of
// them: constructing an SDK's tool values from a JSON schema is code generation, and at the syntactic
// floor there is no typed evidence to check the construction against — the same blindness rewrite_span.go
// documents for prompts, worse here because a construction has no original expression to compare to.
// Two copies of this sentence would be two things to keep true (禁止分裂 source-of-truth).
//
// 🚫 This is never a silent drop and never a partial diff. A test asserting that a dropped SkillRef
// still "succeeds" is itself a failing test (task 2.3): the node would run without the binding while
// its config_hash still claimed it, and the eval would score a configuration that never existed.
func refuseSkills(nodeID string, o variantspec.ResolvedOverride) error {
	names := make([]string, 0, len(o.Skills))
	for _, s := range o.Skills {
		names = append(names, s.Name)
	}
	return unsafeRewrite(nodeID, string(variantspec.DimSkills),
		"binding skills %v requires constructing SDK-specific tool values at the call site, and no "+
			"materializer for this language has landed yet (Go is the first — decisions.md D-14.4); this "+
			"engine only replaces value expressions here, so the binding is REFUSED rather than dropped",
		names)
}

// rewriteContext refuses, and P3 is the named owner of the rewrite that would not refuse.
//
// The registry rows carry no context locator at all — context assembly is not an argument, it is how
// the surrounding code builds the message list, which is why the IR records it as a description
// ("static message list at the call site") rather than a position. P2 ships only the `full` policy
// (PRD §3), and `full` is by definition what an un-rewritten call site already does, so refusing
// costs P2 nothing: there is no context configuration expressible today that this refusal denies.
//
// This is scope, not a gap. Context assembly becomes rewritable when there are real policies to
// rewrite it TO, and those arrive with P3 (docs/prd/P3-context-skills-sandbox.md) — which owns both
// the policies and the transform that realizes them.
func rewriteContext(site discovery.GoCallSite, _ []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	return nil, refuseContext(site.NodeID, o)
}

// refuseContext is the context refusal, shared by both engines — and it is if anything MORE
// language-neutral than the skills one. No frontend's registry rows carry a context locator, in any
// language, because context assembly is not an argument anywhere: it is how the surrounding code builds
// the message list. P3 owns the rewrite that would not refuse, for every language at once.
func refuseContext(nodeID string, o variantspec.ResolvedOverride) error {
	return unsafeRewrite(nodeID, string(variantspec.DimContext),
		"context assembly is not a call-site argument — it is how the surrounding code builds the "+
			"message list, so the registry rows declare no context locator to rewrite. P2 ships only "+
			"the `full` policy, which is by definition what this call site already does; policy %q "+
			"needs the context-assembly rewrite owned by P3 (docs/prd/P3-context-skills-sandbox.md), "+
			"not an argument swap", o.Context.Spec.Policy)
}

// ── P15: the interim refusal for un-materializable wiring ────────────────────────────────────────
//
// The wiring axis (`Order`/`Edges`) is fully MODELED and fully HASHED: a reorder, a merge, or a prune
// produces a Variant Spec that resolves to a different `config_hash`. What does not exist is a
// rewriter that turns that graph into source — moving a call, fusing two calls, deleting a call are
// AST surgery on the user's control flow, not the value replacement this engine performs.
//
// 🔴 So the honest outcome is a REFUSAL, and the alternative is the worst failure this system has.
// Silently no-op'ing the wiring would let the engine "succeed": the diff would rewrite the node
// CONTENTS, the build would pass, the eval would run — and the score would be attributed to a
// `config_hash` claiming a graph the source never had. That is a FALSE MEASUREMENT, not a missing
// feature, and it would poison the verified-delta ledger that every later decision reads. A platform
// whose principle is "verification decides" cannot afford one measurement that means nothing.
//
// This is the same refuse-until-safe shape as refuseSkills (P14) and refuseContext (P3-owned):
// modeled, resolvable, hashable, materialization deferred and STATED. When a wiring rewriter lands it
// replaces this function and nothing upstream changes.
//
// An inserted ADAPTER is not refused: it IS materializable — EmitAdapter generates its source into the
// same diff (decisions.md D-2) — so the comparison below collapses adapter hops before deciding, and a
// coherent-but-adapted spec passes.

// wiringRefusalDim is the axis name carried on the refusal. It is a plain string and deliberately NOT
// a variantspec.Dimension: the wiring axis is Order/Edges, and minting a Dimension const for it would
// be the second representation task 1.4 forbids. The error still names the axis, which is what a
// reader needs.
const wiringRefusalDim = "wiring"

// refuseWiring is the typed refusal: an ErrUnsafeRewrite naming the wiring axis and the specific
// difference, so the reader learns WHICH rewire was asked for, not just that something was refused.
func refuseWiring(nodeID, difference string) error {
	return unsafeRewrite(nodeID, wiringRefusalDim,
		"this spec asks for a wiring change (%s), but no call-site rewriter materializes a node "+
			"rearrangement as source — moving, fusing, or deleting a call is control-flow surgery, not the "+
			"value replacement this engine performs. It is REFUSED rather than applied as a no-op: a no-op "+
			"would let this spec's config_hash, which already records the new graph, be scored against "+
			"source that was never rewired — a false measurement, not a missing feature", difference)
}

// checkWiring compares the wiring a spec ASKS for against the wiring the source HAS, and returns the
// refusal when they differ. spec may be nil (the plain Generate path, where the resolved config is the
// only statement of the desired wiring).
//
// It concludes nothing when no discovered wiring was recorded: a Resolved assembled by hand has no IR
// behind it, and refusing on absent evidence would block every caller that never had a graph to
// compare against. The production path (variantspec.Resolve) always records it.
// checkWiring is now MATERIALIZE-OR-REFUSE (P15 15c task 15.1). It returns:
//
//	(nil, nil)    the spec's wiring is the source's wiring — nothing to do;
//	(plan, nil)   the difference is the ONE shape this engine materializes (an adjacent transposition);
//	(nil, err)    every other difference — the 15b refusal, unchanged, naming the axis.
//
// The narrowing is deliberate and literal. `planWiringSwap` admits exactly one adjacent pair exchanged
// with the edge set untouched; a merge, a prune, an edge change, a non-adjacent move, or two
// transpositions at once all fall through to the same refusal they got before 15c. Widening this test
// is how a rewriter acquires reach it cannot justify, so the test is written to be read, not extended.
func checkWiring(r *variantspec.Resolved, spec *variantspec.VariantSpec) (*swapPlan, error) {
	if r == nil || !r.DiscoveredWiring.Recorded() {
		return nil, nil
	}
	order, edges := desiredWiring(r, spec)
	if len(order) == 0 {
		return nil, nil // nothing was asked for
	}
	diff := wiringDifference(r.DiscoveredWiring, order, edges)
	if diff == "" {
		return nil, nil
	}
	if plan, ok := planWiringSwap(r.DiscoveredWiring, order, edges); ok {
		return plan, nil
	}
	return nil, refuseWiring(firstDifferingNode(r.DiscoveredWiring.Order, order), diff)
}

// desiredWiring is the wiring the spec asks for, with inserted-adapter hops collapsed back to the edge
// they bridge — an adapter is generated source in the same diff, so it is not an un-materializable
// rewire and must not read as one.
func desiredWiring(r *variantspec.Resolved, spec *variantspec.VariantSpec) ([]string, []variantspec.ResolvedEdge) {
	var order []string
	var edges []variantspec.ResolvedEdge
	if spec != nil && len(spec.Order) > 0 {
		order = append(order, spec.Order...)
		for _, e := range spec.Edges {
			edges = append(edges, variantspec.ResolvedEdge{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: e.Kind})
		}
	} else {
		for _, n := range r.Config.Nodes {
			order = append(order, n.NodeID)
		}
		edges = append(edges, r.Config.Edges...)
	}

	adapters := map[string]variantspec.InsertedAdapter{}
	if spec != nil {
		for _, a := range spec.InsertedAdapters {
			adapters[a.AdapterNodeID] = a
		}
	}
	if len(adapters) == 0 {
		return order, edges
	}

	kept := order[:0:0]
	for _, id := range order {
		if _, isAdapter := adapters[id]; !isAdapter {
			kept = append(kept, id)
		}
	}
	// Collapse producer→adapter→consumer back to producer→consumer; drop the two hops.
	collapsed := make([]variantspec.ResolvedEdge, 0, len(edges))
	for _, e := range edges {
		if a, ok := adapters[e.ToNodeID]; ok && e.FromNodeID == a.FromNodeID {
			collapsed = append(collapsed, variantspec.ResolvedEdge{
				FromNodeID: a.FromNodeID, ToNodeID: a.ToNodeID, Kind: e.Kind})
			continue
		}
		if _, ok := adapters[e.FromNodeID]; ok {
			continue // the adapter→consumer half of a hop already accounted for
		}
		collapsed = append(collapsed, e)
	}
	return kept, collapsed
}

// wiringDifference reports the first difference between the discovered wiring and the desired one, in
// a sentence a user can act on, or "" when they denote the same graph. Node ORDER is compared as a
// sequence (it is identity-bearing) and edges as a SET (two specs listing the same edges in another
// order are one graph).
func wiringDifference(discovered variantspec.Wiring, order []string, edges []variantspec.ResolvedEdge) string {
	if len(discovered.Order) != len(order) {
		return fmt.Sprintf("the source wires %d node(s) %v but the spec orders %d %v",
			len(discovered.Order), discovered.Order, len(order), order)
	}
	for i := range order {
		if discovered.Order[i] != order[i] {
			return fmt.Sprintf("node order differs at position %d: the source runs %q there, the spec asks for %q "+
				"(source %v, spec %v)", i, discovered.Order[i], order[i], discovered.Order, order)
		}
	}
	have := edgeSet(discovered.Edges)
	want := edgeSet(edges)
	for k := range want {
		if !have[k] {
			return fmt.Sprintf("the spec adds the edge %s, which the source does not wire", k)
		}
	}
	for k := range have {
		if !want[k] {
			return fmt.Sprintf("the spec drops the edge %s, which the source wires", k)
		}
	}
	return ""
}

func edgeSet(edges []variantspec.ResolvedEdge) map[string]bool {
	out := make(map[string]bool, len(edges))
	for _, e := range edges {
		out[e.FromNodeID+" -"+e.Kind+"-> "+e.ToNodeID] = true
	}
	return out
}

// firstDifferingNode names the node the refusal is anchored to: the first position where the two
// orders disagree, or the first node either side declares. A refusal with no node reads as "something
// about the graph"; naming one gives the reader somewhere to look.
func firstDifferingNode(discovered, desired []string) string {
	for i := range desired {
		if i >= len(discovered) {
			return desired[i]
		}
		if discovered[i] != desired[i] {
			return desired[i]
		}
	}
	if len(discovered) > len(desired) {
		return discovered[len(desired)]
	}
	if len(desired) > 0 {
		return desired[0]
	}
	return ""
}

// byteRange converts an AST node's positions to byte offsets in the original file.
func byteRange(fset *token.FileSet, n ast.Node, srcLen int) (int, int, error) {
	start := fset.Position(n.Pos()).Offset
	end := fset.Position(n.End()).Offset
	if start < 0 || end > srcLen || start > end {
		return 0, 0, fmt.Errorf("AST position [%d,%d) is outside the %d-byte source; "+
			"the tree and the file on disk disagree", start, end, srcLen)
	}
	return start, end, nil
}

// insertStructField builds an edit that adds `Name: value` to a composite literal.
//
// Inserted immediately after the opening brace, so the edit's byte range is a single point and the
// diff is one line. Placement is not a formatting decision worth agonizing over — the build gate
// (task 3.5) and gofmt-in-CI on the target repo are what judge the result, and a reviewer reads the
// argument, not its position in the literal.
func insertStructField(fset *token.FileSet, cl *ast.CompositeLit, src []byte, name, value string) (edit, error) {
	at := fset.Position(cl.Lbrace).Offset + 1
	if at < 0 || at > len(src) {
		return edit{}, fmt.Errorf("the options struct literal's brace is outside the source")
	}
	text := name + ": " + value
	if len(cl.Elts) > 0 {
		text += ", " // an existing element follows; keep it a valid literal
	}
	return edit{Start: at, End: at, New: text}, nil
}

// refuseInlineParamTune is P13 task 3.6 / Decision 7: a parameter tune (temperature/max-tokens) on an
// INLINE node has no call-site rewriter. This engine replaces value EXPRESSIONS the call site already
// wrote; a params struct field the author may never have written is not one of them, so synthesizing it
// would be SDK-shaped code generation (the ADR-001 top risk), and dropping it would hash one thing and
// run another (P10 reconciliation fails such a run). Both are refused; only BOUND mode carries a param
// tune, where it lives in the binding document as data.
func refuseInlineParamTune(nodeID string) error {
	return unsafeRewrite(nodeID, string(variantspec.DimModel),
		"this override is a parameter tune (temperature/max-tokens) on an inline node, but there is no "+
			"call-site parameter rewriter; a param tune can only be materialized in bound apply mode "+
			"(ADR-004), so it is refused here rather than silently dropped")
}

// providerHintFor returns the provider the matched registry row declares for this call site, if any.
func providerHintFor(site discovery.GoCallSite) string { return site.ProviderHint }

func lastSegment(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return s[i+1:]
		}
	}
	return s
}

// describeExpr names an expression's shape for an error a human has to act on. "is a composite
// literal" tells a user why their override was refused; "is *ast.CompositeLit" tells them we leaked
// our implementation.
func describeExpr(e ast.Expr) string {
	switch e.(type) {
	case *ast.CompositeLit:
		return "a composite literal (a constructed value)"
	case *ast.Ident:
		return "a variable"
	case *ast.CallExpr:
		return "a function call"
	case *ast.SelectorExpr:
		return "a qualified identifier (a constant or field)"
	case *ast.BinaryExpr:
		return "an expression"
	default:
		return "not a literal"
	}
}
