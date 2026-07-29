package transform

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"sort"
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
			// The provider is named WITHOUT an indefinite article: %s is a provider name, and "an bedrock"
			// / "a openai" are both reachable from one hard-coded article. Refusal copy is read by the
			// person who has to act on it, and a sentence that reads as machine-assembled invites them to
			// stop reading it.
			"call site targets the %s SDK but the override selects provider %q; swapping providers means "+
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

// rewriteContext MATERIALIZES a resolved context policy at a Go call site (P16 task 2.2).
//
// Go is the first — and, in 16a, the only — language whose context refusal is replaced by construction.
// The construction, the per-policy coverage table it is gated on, and the argument that makes a
// message-list rewrite safe live in contextmaterialize.go; this function is the dispatch table's entry
// for the context dimension and nothing more.
//
// 🔴 It still refuses, by name, for a policy that assembles at RUN TIME (summarization, retrieval) and
// for a call site whose message list is built at runtime. "Go is supported" is not "every Go call site
// is supported", and a refusal must say which half is missing.
func rewriteContext(site discovery.GoCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	return materializeContext(site, src, o)
}

// refuseContext is the INTERIM refusal, for every language whose context materializer has not landed
// (P16 task 3.2, design.md Decision 1). Today that is every tree-sitter language; Go no longer reaches it.
//
// 🔴 P16 OWNS this rewrite. The previous text named P3 as the owner, which was inaccurate in the way
// that costs a reader real time: P3 shipped the policies and their host-side `Assemble` and never the
// call-site rewrite, so a user who followed that pointer found a phase that had already shipped and no
// rewrite. A refusal that sends the reader to the wrong place is a worse refusal than one that says
// nothing, because it looks actionable.
//
// The reason itself is unchanged and still correct: context assembly is not an argument in any
// language — it is how the surrounding code builds the message list — so materializing a policy is a
// REGION rewrite, per language. Go's landed (contextmaterialize.go); the rest state that plainly rather
// than no-op the override, because a silently-dropped context override is resolved, hashed, and scored
// as the BASE configuration under the variant's hash. That is a false result, the worst thing an eval
// platform can produce, and it is why this refusal is a specified, tested requirement rather than a
// placeholder.
func refuseContext(nodeID, language string, o variantspec.ResolvedOverride) error {
	policy := "the resolved policy"
	if o.Context != nil {
		policy = strconv.Quote(o.Context.Spec.Policy)
	}
	return refuseNoMaterializer(nodeID, string(variantspec.DimContext),
		"context assembly is not a call-site argument — it is how the surrounding code builds the "+
			"message list — so materializing policy %s is a REGION rewrite of that code, per language. "+
			"P16 owns that rewrite (docs/prd/P16-context-strategy-optimization.md) and has landed it for "+
			"Go; the %s materializer is still being built, so this override is REFUSED rather than "+
			"dropped — applying it as the base configuration would score a configuration that never ran",
		policy, languageDisplay(language))
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
// checkWiring is MATERIALIZE-OR-REFUSE, over the wiring the SOURCE ACTUALLY STATES (P15 15c, corrected).
//
// # What the source states, and what it does not
//
// This is the whole of the correctness argument, and the first version of it was wrong in a way CI
// caught: it compared the spec's `Order` against the IR's NODE-EMISSION order and treated any
// difference as "the spec asks to rearrange the graph". Emission order is a discovery walk artifact —
// which file was read first — not a claim about execution. Twelve pre-existing specs that override only
// a model or a prompt were refused for wiring because their author listed the nodes in the workflow's
// logical sequence rather than in the order Discovery happened to emit them.
//
// The source states a relative order between two calls only when it actually orders them: when their
// call sites are CONSECUTIVE SIBLING STATEMENTS in one block. Two calls in different functions, or
// different files, have no source-stated order at all — `Order` there is the author's declaration of
// the workflow's shape, which is what a Variant Spec's ordering has always been for, and it contradicts
// nothing in the tree.
//
// Edges are out of the comparison entirely, for the reason P14 learned with `Tools == nil`: an IR that
// records no edge means NOT RECORDED, never "no edge". A syntactic frontend infers few edges and a spec
// legitimately declares the ones the coherence gate needs; treating a declared edge as a request to
// rewire source refuses a change nobody asked for.
//
// So exactly three outcomes remain:
//
//	the node SET differs (a merge or a prune)   → REFUSE. The source demonstrably still contains the
//	                                              call, so scoring this config_hash would be false.
//	a source-ordered PAIR is inverted           → materialize it if it is the one shape we can
//	                                              (an adjacent transposition), else REFUSE.
//	anything else                               → nothing to do: the source states no order to contradict.
func checkWiring(r *variantspec.Resolved, spec *variantspec.VariantSpec, sites map[string]boundSite, root string) (*swapPlan, error) {
	if r == nil || !r.DiscoveredWiring.Recorded() {
		return nil, nil
	}
	order, _ := desiredWiring(r, spec)
	if len(order) == 0 {
		return nil, nil // nothing was asked for
	}

	// 1. The node SET. A node the spec drops is still in the tree, and a node it adds is not — either
	// way the running code is not the graph the hash records.
	if missing, extra, ok := nodeSetDiff(r.DiscoveredWiring.Order, order); !ok {
		return nil, refuseWiring(firstNonEmpty(missing, extra), describeNodeSetDiff(missing, extra))
	}

	// 2. The pairs the SOURCE orders. Nothing else is evidence of a rearrangement.
	pairs, err := sourceOrderedPairs(r.Language, sites, root)
	if err != nil {
		return nil, err
	}
	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}
	var inverted []sourcePair
	for _, p := range pairs {
		if pos[p.first] > pos[p.second] {
			inverted = append(inverted, p)
		}
	}
	if len(inverted) == 0 {
		return nil, nil
	}
	if len(inverted) == 1 {
		if plan, ok := planAdjacentSwap(order, inverted[0]); ok {
			return plan, nil
		}
	}
	p := inverted[0]
	return nil, refuseWiring(p.first, fmt.Sprintf(
		"the source runs %s then %s as consecutive statements in %s, and this spec asks for the opposite "+
			"order (%d source-ordered pair(s) inverted)", p.first, p.second, p.file, len(inverted)))
}

// sourcePair is two nodes the SOURCE puts in a definite order: consecutive sibling statements.
type sourcePair struct {
	first, second string
	file          string
	firstLine     int
	secondLine    int
}

// sourceOrderedPairs finds every pair of call sites the source itself orders — same file, consecutive
// sibling statements with nothing but blank lines between them.
//
// It deliberately reuses the SAME admissibility the materializer requires (`admitSwap`'s structural
// half). That is not a coincidence to be refactored away: the set of pairs whose order the source
// states IS the set of pairs a transposition could exchange, so one predicate answers both questions
// and they can never drift apart.
func sourceOrderedPairs(language string, sites map[string]boundSite, root string) ([]sourcePair, error) {
	resolve, ok := statementResolvers[strings.ToLower(strings.TrimSpace(language))]
	if !ok {
		// A language with no statement resolver states no order this engine can read. That is not a
		// refusal: it means the spec's ordering contradicts nothing we can see, which is the honest
		// position for a frontend that never parsed statements.
		return nil, nil
	}

	byFile := map[string][]string{}
	for id, s := range sites {
		byFile[s.fileRel] = append(byFile[s.fileRel], id)
	}
	var out []sourcePair
	for _, rel := range sortedStringKeys(byFile) {
		ids := byFile[rel]
		if len(ids) < 2 {
			continue
		}
		src, err := readFile(root, rel)
		if err != nil {
			return nil, err
		}
		sort.Slice(ids, func(i, j int) bool { return sites[ids[i]].lineStart < sites[ids[j]].lineStart })
		for i := 0; i+1 < len(ids); i++ {
			a, errA := resolve(src, ids[i], sites[ids[i]].lineStart)
			b, errB := resolve(src, ids[i+1], sites[ids[i+1]].lineStart)
			if errA != nil || errB != nil {
				continue // a statement this engine cannot read states no order it can act on
			}
			if admitSwap(src, a, b) != nil {
				continue // not consecutive siblings: the source does not order these two
			}
			out = append(out, sourcePair{first: ids[i], second: ids[i+1], file: rel,
				firstLine: a.startLine, secondLine: b.startLine})
		}
	}
	return out, nil
}

// planAdjacentSwap admits the inverted pair only when the spec places the two nodes NEXT TO each other:
// an exchange of neighbours. A pair inverted across other nodes is a move, which this engine does not do.
func planAdjacentSwap(order []string, p sourcePair) (*swapPlan, bool) {
	i, j := -1, -1
	for k, id := range order {
		if id == p.first {
			i = k
		}
		if id == p.second {
			j = k
		}
	}
	if i < 0 || j < 0 || i != j+1 {
		return nil, false
	}
	return &swapPlan{First: p.first, Second: p.second}, true
}

// nodeSetDiff reports whether two orderings name the same nodes, and if not, one concrete node from
// each side. Sorted, so the same defect always produces the same message.
func nodeSetDiff(discovered, desired []string) (missing, extra string, ok bool) {
	have := map[string]bool{}
	for _, id := range discovered {
		have[id] = true
	}
	want := map[string]bool{}
	for _, id := range desired {
		want[id] = true
	}
	var miss, ext []string
	for id := range have {
		if !want[id] {
			miss = append(miss, id)
		}
	}
	for id := range want {
		if !have[id] {
			ext = append(ext, id)
		}
	}
	if len(miss) == 0 && len(ext) == 0 {
		return "", "", true
	}
	sort.Strings(miss)
	sort.Strings(ext)
	if len(miss) > 0 {
		missing = miss[0]
	}
	if len(ext) > 0 {
		extra = ext[0]
	}
	return missing, extra, false
}

func describeNodeSetDiff(missing, extra string) string {
	switch {
	case missing != "" && extra != "":
		return fmt.Sprintf("this spec drops node %s and adds node %s; the source still contains the call "+
			"the spec dropped", missing, extra)
	case missing != "":
		return fmt.Sprintf("this spec drops node %s — a merge or a prune — but the source still contains "+
			"that call, so the graph this config_hash records is not the graph that would run", missing)
	default:
		return fmt.Sprintf("this spec adds node %s, which the source does not contain", extra)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func sortedStringKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
			edges = append(edges, variantspec.ResolvedEdge(e))
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
