package transform

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// The rule that decides what the SYNTACTIC engines will and will not rewrite
// ─────────────────────────────────────────────────────────────────────────
//
// Same rule as the Go engine's (see rewrite.go): a dimension is rewritable when the configured value
// can REPLACE AN EXPRESSION THE CALL SITE ALREADY WROTE, without synthesizing SDK-shaped code. What
// changes is how much of that rule these languages can actually establish, and the answer is: less. The
// boundary below is drawn where the evidence stops, not where the implementation got tired.
//
// 🔴 The one place Go's rule DOES NOT TRANSPLANT, and why it matters more than anything else here
//
// Go's rewritePrompt descends into a message construction and rewrites the single string it finds
// inside. That is safe in Go for a reason that is easy to mistake for an implementation detail and is
// not: real Go SDKs carry the message ROLE in a typed constant or a helper function
// (`anthropic.NewUserMessage`, `openai.ChatMessageRoleUser`), never a bare string literal. So a
// single-turn construction contains exactly one string, and "exactly one" is a proof that the string is
// the prompt.
//
// In Python and JS/TS the role is a bare string:
//
//	messages=[{"role": "user", "content": "Summarize this"}]
//
// At the syntactic floor — no types, no SDK knowledge — `"user"` and `"Summarize this"` are the same
// kind of thing. A "count the strings" rule does not merely fail to help here; it MISLEADS, and it does
// so in exactly the languages it would have been introduced for. It finds two strings in the one-turn
// call above and would refuse it as multi-turn, and it finds one string in `messages=[{"role": r,
// "content": "Summarize this"}]` and would happily rewrite... whichever one it found first.
//
// So: `messages=[…]` is REFUSED here, with its own reason. That is not a gap to close later by trying
// harder at the same level — it is the honest boundary of a type-free parse, and an engine that guessed
// past it would produce a diff that parses, passes a syntax-checked gate, and silently swaps a role
// string for a prompt. ADR-001's named top risk is "a bad codemod can break a build or subtly change
// behavior"; this is the second one, which has no downstream net at all.
//
// What survives the boundary is narrow and real: a keyword argument whose value is ONE STATIC STRING
// LITERAL. That covers langchain's `input:` and Vercel AI SDK's `prompt:` — both of which the registry
// rows already point at — and nothing it cannot stand behind. An honest narrow boundary beats a broad
// guess.

// spanRewriter turns one node's one overridden dimension into byte edits at a span-located call site.
type spanRewriter func(site discovery.SpanCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error)

// spanRewriters is the per-dimension table for every tree-sitter language, mirroring `rewriters`.
//
// skills and context are the SAME refusals the Go engine gives, and they share one implementation
// rather than a re-worded copy (禁止分裂 source-of-truth): the reasons are not facts about Go. Binding
// skills means constructing SDK-specific tool values in any language; context assembly is not a call
// argument in any language, and P3 owns it everywhere.
var spanRewriters = map[variantspec.Dimension]spanRewriter{
	variantspec.DimModel:  spanRewriteModel,
	variantspec.DimPrompt: spanRewritePrompt,
	variantspec.DimSkills: func(s discovery.SpanCallSite, _ []byte, o variantspec.ResolvedOverride) ([]edit, error) {
		return nil, refuseSkills(s.NodeID, o)
	},
	variantspec.DimContext: func(s discovery.SpanCallSite, _ []byte, o variantspec.ResolvedOverride) ([]edit, error) {
		return nil, refuseContext(s.NodeID, o)
	},
	// P14 tool selection. It refuses here too, and for a reason of the same kind as the skills refusal
	// but not identical to it, so it gets its own sentence rather than sharing one (see spanRewriteTools).
	variantspec.DimTools: spanRewriteTools,
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// What each language's call sites can express — the source of every "why not" below
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// argumentForm records, per language, the two independent facts that decide whether ANY argument at a
// call site can be rewritten — and they really are independent, which is why this is a struct and not a
// bool:
//
//	namedArgs   — a fact about the LANGUAGE. Can a call site name an argument at all?
//	              No named-argument form => there is nothing to replace and nowhere to insert, ever, in
//	              any SDK. The analyzers say so structurally: javaAnalyzer and rustAnalyzer emit an empty
//	              Keywords map and a nil KeywordInsert, and that is a statement about Java and Rust, not
//	              unimplemented work.
//	registryNote — a fact about the REGISTRY ROWS. Even where the language allows a named argument, a
//	              rewrite needs a row saying WHICH name holds the model or the prompt (arg_map). No
//	              row, no locator, nothing to point at.
//
// 🔴 Kotlin is why these are separate. Kotlin HAS named arguments; kotlinAnalyzer populates real spans
// and a real insert point with Assign=" = "; the rewriter below would rewrite a Kotlin call site
// correctly today. It never gets the chance, because all five Kotlin rows in registry.yaml declare no
// arg_map — and NOT by omission: langchain4j, Spring AI and Bedrock bind the model on a BUILDER at
// construction (`OpenAiChatModel.builder().modelName("gpt-4o").build()`), so `generate(...)` genuinely
// has no model argument to point a locator at. Collapsing Kotlin's reason into Java's ("no named
// arguments") would be false, and collapsing it into "unsupported" would hide that the fix is a
// registry row for an SDK that binds at the call site, not engine work.
//
// This table is the single place those reasons are written down, so a refusal a user reads and the
// capability doc a salesperson reads cannot drift apart.
type argumentForm struct {
	// namedArgs reports whether the language has a named/keyword-argument form at a call site.
	namedArgs bool
	// registryNote explains the state of this language's rows, for a refusal a human has to act on.
	//
	// Empty where the rows are unremarkable. Python, TypeScript and JavaScript DO declare arg_map on
	// the rows whose SDKs name their arguments at the call site, so a nil locator there means only "not
	// this row" (e.g. Vercel's `generateText` takes a model OBJECT, not a string, and its row correctly
	// points at no model) — a language-level sentence would say nothing and would read as if the
	// language were the problem. Java, Kotlin and Rust have a language-level sentence because for them
	// it is the whole answer: no row has one, and the reason is the same for all of them.
	registryNote string
}

var argumentForms = map[string]argumentForm{
	"python":     {namedArgs: true},
	"typescript": {namedArgs: true},
	"javascript": {namedArgs: true},
	"kotlin": {namedArgs: true,
		registryNote: "no Kotlin row in the signature registry declares an arg_map, because the SDKs those " +
			"rows cover (langchain4j, Spring AI, Bedrock) bind the model on a BUILDER at construction " +
			"rather than at the call site, so there is no model or prompt argument here to point at — " +
			"Kotlin's named arguments themselves ARE rewritable, and a row (or an llm-eval.yaml " +
			"entrypoint) whose SDK names its arguments at the call site would be rewritten"},
	"java": {namedArgs: false,
		registryNote: "and no Java row in the signature registry declares an arg_map either, because " +
			"langchain4j, Spring AI and Bedrock bind the model on a builder at construction rather than " +
			"at the call site"},
	"rust": {namedArgs: false,
		registryNote: "and no Rust row in the signature registry declares an arg_map either, because " +
			"async-openai and the anthropic crate bind the model in a request struct built before the call"},
}

// noArgumentReason is the honest sentence for "this dimension has no argument to rewrite at this call
// site", assembled from the two facts above rather than flattened into "unsupported".
//
// 🚫 "unsupported language" would be false and would send the reader to the wrong place: Discovery
// supports all six, finds these call sites, and reports them in the IR. What is missing is specific,
// nameable, and different per language.
func noArgumentReason(language, dim string) string {
	base := fmt.Sprintf("the registry row matched for this call site declares no %s locator, so there is "+
		"no argument to rewrite", dim)
	f, ok := argumentForms[language]
	if !ok {
		return base
	}
	if !f.namedArgs {
		return fmt.Sprintf("%s has no named-argument form, so a call site carries no keyword argument to "+
			"replace and no place to insert one; %s. This engine rewrites arguments the call site already "+
			"wrote, and there is no %s argument here to rewrite",
			languageDisplay(language), f.registryNote, dim)
	}
	if f.registryNote != "" {
		return base + "; " + f.registryNote
	}
	return base
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// model
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// spanRewriteModel rewrites the model argument to the override's model id, mirroring the Go rewriter's
// two shapes: replace the value the call site wrote, or insert the argument it never wrote.
//
// # Why every ArgKind is replaceable here, when the prompt rewriter refuses two of them
//
// ArgValue.Kind's doc says an interpolated string is "NOT replaceable" — and that is a rule about
// PROMPTS, where the interpolated value is DATA the eval depends on and dropping it silently corrupts
// every run. A model is the opposite: `model=f"{tier}-turbo"` is the call site CHOOSING a model at
// runtime, and an override that pins the model means precisely "stop choosing, use this one". Replacing
// it is the override being applied, not a value being lost.
//
// This is also exactly what the Go engine does — rewriteModel replaces whatever expression the locator
// finds, including `prefix + "-turbo"` — so the two engines agree on what a model override means. They
// must: a rule that held only in Go would make the same spec mean two things.
func spanRewriteModel(site discovery.SpanCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimModel)
	entry := o.Model

	// A provider swap cannot be a single-argument rewrite, for the same reason it cannot be in Go: a
	// call site's SDK is not something a model string can change (ADR-002). Same check, same refusal —
	// it is a property of SDKs, not of Go.
	if hint := site.ProviderHint; hint != "" && entry.Spec.Provider != hint {
		return nil, unsafeRewrite(site.NodeID, dim,
			"call site is an %s SDK call but the override selects provider %q; swapping providers means "+
				"rewriting the SDK call itself (different client, params type, and response shape), which "+
				"this engine does not do (ADR-002)", hint, entry.Spec.Provider)
	}

	loc, err := spanLocator(site, site.ArgMap.Model, dim)
	if err != nil {
		return nil, err
	}
	lit, err := spellString(site.Language, entry.Spec.ModelID)
	if err != nil {
		return nil, unsafeRewrite(site.NodeID, dim, "%v", err)
	}

	if v, ok := site.Keywords[loc.Name]; ok {
		if err := checkSpan(site.NodeID, dim, v.Value, len(src)); err != nil {
			return nil, err
		}
		return []edit{{Start: v.Value.Start, End: v.Value.End, New: lit, NodeID: site.NodeID, Dim: dim}}, nil
	}

	// Absent argument: insert it. A call site that never pinned a model still HAS one — the SDK's
	// default — so "absent" is a thing to override, not a reason to refuse. This is the same judgment
	// rewriteModel makes for a Go options struct with no Model field; without it, every unpinned Python
	// and TS call site would be un-overridable, which is most of them.
	//
	// 🔴 That premise has ONE precondition, and it is not "the argument is absent from the map" — it is
	// "the argument is absent from the CALL". Those are the same statement only when the call site
	// writes out everything it passes. Under an unpacking (`create(**kwargs)`) they come apart, the
	// insertion stops being an override, and discovery hands back a nil ArgInsert with the unpacking
	// that caused it (see discovery.ArgUnpacking).
	//
	// Note this is the SAME reasoning spanRewritePrompt already applies one function down — "adding a
	// prompt argument would not replace it, it would ADD a second one". The asymmetry was the bug: the
	// prompt path reasoned about what the call actually passes, and the model path reasoned about what
	// the source text spells. The model path is the one that inserts, so it is the one that needed it.
	ins := site.KeywordInsert
	if ins == nil {
		return nil, unsafeRewrite(site.NodeID, dim,
			"the %s argument is not written at this call site and there is no place to add one: %s",
			loc.Name, insertRefusalReason(site, loc.Name))
	}
	if ins.At < 0 || ins.At > len(src) {
		return nil, unsafeRewrite(site.NodeID, dim,
			"the insertion point for %s is at byte %d, outside the %d-byte source; the parse tree and the "+
				"file on disk disagree", loc.Name, ins.At, len(src))
	}
	return []edit{{
		Start: ins.At, End: ins.At, New: ins.Prefix + loc.Name + ins.Assign + lit,
		NodeID: site.NodeID, Dim: dim,
	}}, nil
}

// insertRefusalReason says why a call site offers nowhere to add a keyword argument. Three very
// different causes, and a user can act on two of them.
//
// 🔴 The unpacking case comes first because it is the only one where the call site DOES have a
// perfectly good argument list — the generic sentence below would be flatly false about
// `create(**kwargs)` and would send a reader looking for a missing paren.
func insertRefusalReason(site discovery.SpanCallSite, name string) string {
	if u := site.KeywordUnpacking; u != nil {
		return fmt.Sprintf("this call site passes %s, so the arguments it actually sends are assembled at "+
			"runtime and are not written here. `%s` may ALREADY be among them — in %s it is a dict built "+
			"somewhere else in the program, and a type-free parse cannot prove what is or is not in it. "+
			"Adding `%s=` would therefore not reliably override the model: if the unpacking already "+
			"carries it, the call raises TypeError (\"got multiple values for keyword argument '%s'\") at "+
			"runtime, and %s's gate cannot catch that — the file still PARSES, so the transform would be "+
			"recorded `built` and shown to a reviewer with a green badge over a call that cannot execute. "+
			"Set the model where %s is assembled, or pass it as a written argument this engine can replace",
			u.Text, name, languageDisplay(site.Language), name, name, languageDisplay(site.Language), u.Text)
	}
	if f, ok := argumentForms[site.Language]; ok && !f.namedArgs {
		return fmt.Sprintf("%s has no named-argument form", languageDisplay(site.Language))
	}
	return "this call site has no argument list or options object the argument could be added to, or the " +
		"parser RECOVERED its argument list rather than parsing it (an insertion invents a position, and " +
		"a position invented inside source the parser was guessing at is how a codemod corrupts a file)"
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// prompt
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// spanRewritePrompt rewrites a prompt argument that is ONE STATIC STRING LITERAL, and refuses
// everything else with the reason it refused.
//
// The refusals are the substance of this function; see the file header for why the boundary is here.
func spanRewritePrompt(site discovery.SpanCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimPrompt)
	entry := o.Prompt

	if site.ArgMap.Prompt != nil && site.ArgMap.Prompt.Form == discovery.LocOpaque {
		return nil, unsafeRewrite(site.NodeID, dim,
			"this call site carries its prompt in an opaque serialized body, so there is no prompt "+
				"expression at the call site to rewrite")
	}
	loc, err := spanLocator(site, site.ArgMap.Prompt, dim)
	if err != nil {
		return nil, err
	}

	v, ok := site.Keywords[loc.Name]
	if !ok {
		// 🚫 No insert branch, deliberately — the asymmetry with model is a decision, not an omission.
		// An absent model means "the SDK's default applies", and the default is a thing an override can
		// legitimately replace. An absent prompt does not mean the call has no prompt; it means the
		// prompt reaches the SDK some other way (a message list built above, a chain, a template object).
		// Adding `%s=` would not override that — it would ADD a second source of prompt text next to the
		// one already there, and which of them the SDK honors is a question this engine cannot answer.
		return nil, unsafeRewrite(site.NodeID, dim,
			"the %s argument is not present at this call site, so there is no prompt text to replace; "+
				"the prompt reaches the SDK some other way (a message list assembled before the call, a "+
				"chain, or a template object), and adding a %s argument would not replace it — it would "+
				"add a second one", loc.Name, loc.Name)
	}

	switch v.Kind {
	case discovery.ArgLiteralString:
		// The one rewritable shape. Fall through.
	case discovery.ArgInterpolatedString:
		return nil, unsafeRewrite(site.NodeID, dim,
			"the %s at this call site is an interpolated string, which splices runtime value(s) into the "+
				"prompt; replacing it with a fixed string would silently DROP them — the request would stop "+
				"carrying the data it was built to carry and still return a plausible completion that still "+
				"gets scored. This engine will not do that", loc.Name)
	default:
		return nil, unsafeRewrite(site.NodeID, dim,
			"the %s at this call site is not a static string literal — it is an expression (a message "+
				"list, a variable, or a call). In %s the message role is a bare string, so "+
				`messages=[{"role": "user", "content": "…"}] is, to a parser with no type information, `+
				"indistinguishable from prompt text: nothing at this call site says which string a prompt "+
				"entry replaces. The Go engine descends into a message construction only because Go SDKs "+
				"carry the role in a TYPED constant, which is evidence this language does not offer. "+
				"Rewriting would be a guess, and a guess that parses is the failure mode with no "+
				"downstream net", loc.Name, languageDisplay(site.Language))
	}

	if err := checkSpan(site.NodeID, dim, v.Value, len(src)); err != nil {
		return nil, err
	}

	// A static string literal has no runtime operands, so a slotted template has nothing to bind to.
	// The Go engine binds slots to the call site's own expressions; here there are none — the value is
	// static by definition, which is the only reason it was rewritable at all. Rendering a slot from
	// nothing would ship a prompt with a hole in it.
	if slots := entry.Template.Slots(); len(slots) > 0 {
		return nil, unsafeRewrite(site.NodeID, dim,
			"prompt %q declares slot(s) %v, but this call site's %s is a fixed string with no runtime "+
				"value to bind them to", entry.Name, slots, loc.Name)
	}
	rendered, err := entry.Template.Render(nil)
	if err != nil {
		return nil, unsafeRewrite(site.NodeID, dim, "render prompt %q: %v", entry.Name, err)
	}
	lit, err := spellString(site.Language, rendered)
	if err != nil {
		return nil, unsafeRewrite(site.NodeID, dim, "prompt %q: %v", entry.Name, err)
	}
	return []edit{{Start: v.Value.Start, End: v.Value.End, New: lit, NodeID: site.NodeID, Dim: dim}}, nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// shared
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// spanLocator resolves the registry's locator for a dimension into the keyword NAME to rewrite, or
// refuses with the reason there is none.
//
// Only LocParamName is honored, and the rejection of every other form is a real gate rather than a
// TODO: `field:`/`index:`/`option:` locators describe positions a type-free parse cannot resolve (a Go
// row's `field: "params.Model"` means "the Model field of the struct in argument N", which needs the
// struct's type). Accepting one here would mean matching it against a keyword map it does not describe —
// finding nothing, and reporting "the argument is not present" about an argument that is right there.
func spanLocator(site discovery.SpanCallSite, loc *discovery.ArgLocator, dim string) (*discovery.ArgLocator, error) {
	if loc == nil {
		return nil, unsafeRewrite(site.NodeID, dim, "%s", noArgumentReason(site.Language, dim))
	}
	if loc.Form != discovery.LocParamName || loc.Name == "" {
		return nil, unsafeRewrite(site.NodeID, dim,
			"the registry row for this call site locates %s by %q, a form that needs type information to "+
				"resolve; the %s frontend is a type-free parse and can resolve only a named argument, so "+
				"this engine will not guess which expression the locator means",
			dim, loc.Form, languageDisplay(site.Language))
	}
	return loc, nil
}

// checkSpan proves a span really addresses bytes of the file being edited.
//
// The spans came from a parse of these same bytes, so this cannot fire through any path in the package —
// and it is checked anyway, for the same reason byteRange is on the Go side: if it ever CAN fire, the
// alternative is splicing at a wrong offset and shipping a corrupted file to a reviewer.
func checkSpan(nodeID, dim string, s discovery.ArgSpan, srcLen int) error {
	if s.Start < 0 || s.End > srcLen || s.Start > s.End {
		return unsafeRewrite(nodeID, dim,
			"the located argument's span [%d,%d) is outside the %d-byte source; the parse tree and the "+
				"file on disk disagree", s.Start, s.End, srcLen)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Spelling a string literal in the target language
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// stringSyntax is how a language spells a double-quoted string literal.
//
// # Why not strconv.Quote
//
// Because strconv.Quote spells GO, and the differences are not cosmetic — they are wrong-code bugs
// waiting for a prompt body that contains the wrong byte:
//
//   - 🔴 `$` in Kotlin. `"you owe $total"` is a Kotlin STRING TEMPLATE referencing a variable `total`.
//     Spliced from Go's quoter it is a compile error at best (`unresolved reference: total`) and, when
//     a variable of that name happens to be in scope, a silent interpolation of the wrong value into
//     the prompt — a diff that type-checks and corrupts the eval. Kotlin needs `\$`.
//   - `\x41` (strconv.Quote's escape for a non-printable byte) is valid in Python and JS and does not
//     exist in Kotlin.
//
// So the spelling is per-language DATA, and the table is the one place it lives.
//
// # Why the escape set is small, and refuses rather than improvises
//
// The five escapes below (`\\ \" \n \r \t`) mean the same thing in Python, JavaScript, TypeScript and
// Kotlin. Everything else — other control characters, invalid UTF-8 — has a spelling that DIFFERS per
// language, and this engine does not guess at those: it refuses, loudly, naming the byte. A model id or
// a prompt body containing a raw NUL is not a case worth improvising a per-language octal escape for;
// it is a case worth a human looking at.
type stringSyntax struct {
	// escapeDollar spells `$` as `\$`. Kotlin only: `$` starts a string template there and is an
	// ordinary character everywhere else.
	escapeDollar bool
}

var stringSyntaxes = map[string]stringSyntax{
	"python":     {},
	"typescript": {},
	"javascript": {},
	"kotlin":     {escapeDollar: true},
	// 🚫 Java and Rust are deliberately absent, not "missing". Neither can reach a spelling: they have
	// no named-argument form, so every dimension refuses at spanLocator/insert long before a literal is
	// built. A row here would be an entry no code path can use — 建了等未来用 — and it would quietly
	// suggest those languages are one step from working, which is the impression this whole file exists
	// to avoid giving.
}

// spellString renders s as a string literal in the target language, or refuses.
func spellString(language, s string) (string, error) {
	syn, ok := stringSyntaxes[strings.ToLower(strings.TrimSpace(language))]
	if !ok {
		return "", fmt.Errorf("this engine has no string-literal spelling for %s, so it cannot write the "+
			"configured value into a %s source file", languageDisplay(language), languageDisplay(language))
	}
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("the configured value is not valid UTF-8, so it cannot be written as a "+
			"%s string literal", languageDisplay(language))
	}
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '$':
			if syn.escapeDollar {
				b.WriteString(`\$`)
			} else {
				b.WriteByte('$')
			}
		default:
			if r < 0x20 || r == 0x7f {
				return "", fmt.Errorf("the configured value contains the control character U+%04X, whose "+
					"escape spelling differs between languages; this engine will not guess how to write it "+
					"as a %s string literal", r, languageDisplay(language))
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}
