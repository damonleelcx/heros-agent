package transform

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strconv"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Skill MATERIALIZATION — the one place this engine constructs code instead of replacing it
// ─────────────────────────────────────────────────────────────────────────────────────────
//
// Every other rewriter in this package obeys one rule: replace an expression the call site ALREADY
// WROTE (see rewrite.go's header). Binding a skill cannot obey it, because a call site that binds no
// skill has no tool expression to replace — the value has to be CONSTRUCTED. That is code generation,
// and rewrite.go spent a paragraph explaining why this engine refused to do it: "a subtly-wrong tool
// schema compiles and then degrades quality invisibly — the worst possible failure for an eval
// platform."
//
// P14 does not weaken that argument; it answers it, with two properties the refusal did not have:
//
//  1. 🔴 THE SHAPE COMES FROM THE SEALED CONTRACT, NEVER FROM A VALUE. The tool's input schema is read
//     out of registry.SkillEntry.Spec.InputSchema — the bytes the skill's version_id content-addresses
//     (registry/skill.go: "Hashing the SCHEMAS into the version_id … is what makes a skill_ref pin a
//     contract"). So the pinned version pins the materialized shape, and two skills sharing a name but
//     pinning different schemas materialize differently. Nothing is inferred from the surrounding call
//     site, which is exactly the inference that would let a wrong shape look right.
//
//  2. 🔴 A MATERIALIZED SKILL IS A PROPOSAL, NOT A MERGE. The construction is gated twice downstream —
//     the build gate (internal/proposal.BuildChecker) rejects a shape that does not compile, and the
//     verification fan-out rejects a shape that compiles and scores worse. "Diagnosis proposes,
//     verification decides" is what makes construction safe here and unsafe as a bare codemod.
//
// What is still refused, and refused BY NAME (decisions.md D-14.3 / D-14.4):
//
//   - every language other than Go — the tree-sitter engines keep refuseSkills (rewrite_span.go);
//   - every provider with no declared tool-value form in the table below;
//   - a call site whose existing tool value is assembled at RUNTIME, because replacing it would drop
//     whatever it built — the same reason a prompt interpolation is refused.
//
// A refusal is never a silent drop and never a partial diff. That is the whole of D-14.3.

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Per-(language, provider) materializer coverage — THE single source of truth (NFR7 / task 9.4)
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// toolValueForm is how ONE provider's Go SDK spells a tool list at a call site.
//
// It is a struct of DATA for the same reason argumentForm (rewrite_span.go) is: the reason a binding
// cannot be materialized differs per provider, and a user reading a refusal and a reader of the
// capability doc must be reading the same sentence. Adding a provider is adding a row here — and a row
// is a claim that this spelling compiles against the named SDK generation, which the build gate is what
// actually proves.
//
// # Why the spelling is pinned to an SDK GENERATION and says so
//
// `[]anthropic.ToolParam{{Name: anthropic.F("x")}}` (v0, F-wrapped) and
// `[]anthropic.ToolUnionParam{{OfTool: &anthropic.ToolParam{Name: "x"}}}` (v1) are both "the anthropic
// tool list", and a codemod that guessed between them would emit a diff that does not compile — the
// LOUD failure, caught by the build gate — or, worse, one that compiles against a shim and means
// something else. So the row names the generation it targets, and the build gate is the arbiter. The
// registry's own rows already make the same distinction (registry.yaml row 1: "v1 drops the F()
// wrapper"), and this table follows the v1 spelling, which is what the checked-in fixtures use.
type toolValueForm struct {
	// openList / closeList wrap the elements in this language's list syntax: "[]anthropic.ToolUnionParam{"
	// … "}" in Go, "[" … "]" in Python. Two strings rather than a "sliceType" because outside Go the
	// list is not named by a type.
	openList, closeList string
	// element renders ONE bound skill as an element of that slice. name is the skill's registered name;
	// schema is the skill's sealed input schema, already decoded.
	element func(name string, schema jsonSchemaDoc) (string, error)
	// sdkNote names the SDK generation this spelling targets, for a refusal and for the capability doc.
	sdkNote string
}

// toolValueCell is one coverage cell: a provider's tool-value spelling IN ONE LANGUAGE.
//
// 🔴 The key gained a language (P14 FR23). The old map was keyed by provider alone and read "Go" from
// the fact that nothing else had a materializer — an assumption that was true exactly once and would
// have been silently wrong the moment a second language landed. What is per language here is only the
// SPELLING: the SHAPE still comes from the pinned skill's sealed schema, which is why a new language is
// a set of rows and not a second source of truth about what a bound skill means.
type toolValueCell struct{ language, provider string }

func toolValueKey(language, provider string) toolValueCell {
	return toolValueCell{language: strings.ToLower(strings.TrimSpace(language)), provider: provider}
}

// toolValueForms is the coverage table: (language, provider) → how to spell its tool list there.
//
// 🔴 It is THE source of truth for per-(language, provider) skill-materializer coverage (NFR7).
// docs/decisions/p14-materializer-coverage.md is a human-readable copy of it, and
// TestCoverageDocMatchesTheFormTable fails if the two stop agreeing — a copy with a gate is not a
// second source of truth, a copy without one is. Nothing else may state coverage: everything that
// needs to (a refusal, the console, the doc) reads MaterializerCoverage().
//
// 🚫 A provider ABSENT from this table is not "unimplemented pending effort" — it is a provider whose
// tool-value spelling this engine has no evidence for, and the honest outcome is the refusal below, not
// a guess. Bedrock is the clearest case: its tools live inside an opaque serialized body (the registry
// row marks `input.ToolConfig`), so there is no Go tool value to construct at all.
var toolValueForms = map[toolValueCell]toolValueForm{
	{"go", "anthropic"}: {
		openList: "[]anthropic.ToolUnionParam{", closeList: "}",
		sdkNote: "anthropic-sdk-go v1 (the generation that drops the F() wrapper)",
		element: func(name string, schema jsonSchemaDoc) (string, error) {
			props, err := schema.propertiesLiteral()
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("{OfTool: &anthropic.ToolParam{Name: %s, InputSchema: anthropic.ToolInputSchemaParam{Properties: %s}}}",
				strconv.Quote(name), props), nil
		},
	},
	{"go", "openai"}: {
		openList: "[]openai.ChatCompletionToolUnionParam{", closeList: "}",
		sdkNote: "openai-go v1 (the generation with ChatCompletionToolUnionParam)",
		element: func(name string, schema jsonSchemaDoc) (string, error) {
			whole, err := schema.wholeLiteral()
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("{OfFunction: &openai.ChatCompletionFunctionToolParam{Function: openai.FunctionDefinitionParam{Name: %s, Parameters: openai.FunctionParameters%s}}}",
				strconv.Quote(name), strings.TrimPrefix(whole, "map[string]any")), nil
		},
	},
}

// SkillMaterializerCoverage is one (language, provider) pair whose skill binding can be materialized,
// and the SDK generation its spelling targets.
type SkillMaterializerCoverage struct {
	Language string `json:"language"`
	Provider string `json:"provider"`
	// SDK names the SDK generation the emitted spelling compiles against. It is part of the coverage
	// claim, not a footnote: "anthropic is supported" is not true of an anthropic call site on a v0 SDK.
	SDK string `json:"sdk"`
}

// MaterializerCoverage reports which (language, provider) pairs can materialize a skill binding today,
// sorted. It reads the SAME table the rewriter refuses from, so a documented capability, a console
// badge, and an emitted refusal cannot drift apart (NFR7, task 9.4).
func MaterializerCoverage() []SkillMaterializerCoverage {
	out := make([]SkillMaterializerCoverage, 0, len(toolValueForms))
	for cell, f := range toolValueForms {
		out = append(out, SkillMaterializerCoverage{Language: cell.language, Provider: cell.provider, SDK: f.sdkNote})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Language != out[j].Language {
			return out[i].Language < out[j].Language
		}
		return out[i].Provider < out[j].Provider
	})
	return out
}

// coveredProviders lists the providers ONE language can materialize into, for a refusal that tells the
// reader what WOULD have worked in the language they are actually writing in. Naming every provider in
// every language would answer a question nobody asked and hide the one they did.
func coveredProviders(language string) []string {
	lang := strings.ToLower(strings.TrimSpace(language))
	var out []string
	for _, c := range MaterializerCoverage() {
		if c.Language == lang {
			out = append(out, c.Provider)
		}
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// The Go rewriter
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// materializeSkills constructs the SDK tool value for a node's bound skills at a Go call site.
//
// Two shapes, mirroring rewriteModel — because a call site that offers no tools and one that offers
// some are both configurations a skill override legitimately replaces:
//
//   - the tools field is written  -> replace its value, but ONLY when what it wrote is a static list.
//   - the tools field is absent   -> insert `Tools: <constructed>` into the options struct literal.
func materializeSkills(site discovery.GoCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimSkills)

	form, ok := toolValueForms[toolValueKey("go", site.ProviderHint)]
	if !ok {
		// Refused BY NAME, per D-14.4/D-14.5: the boundary is per (language, provider), and a reader of
		// this message must be able to tell which half is missing.
		return nil, refuseNoMaterializer(site.NodeID, dim,
			"this Go call site's provider is %s, and this engine has no declared tool-value spelling for "+
				"(go, %s), so there is no SDK shape to construct a bound skill into; the providers it can "+
				"materialize in Go are %v (decisions.md D-14.5). Binding %s here would mean guessing an SDK's "+
				"tool type, and a guess that compiles is the failure mode with no downstream net",
			providerDisplay(site.ProviderHint), site.ProviderHint, coveredProviders("go"), skillNames(o))
	}

	value, err := toolListLiteral(form, o.Skills)
	if err != nil {
		return nil, unsafeRewrite(site.NodeID, dim, "%v", err)
	}

	loc := site.ArgMap.Tools
	if loc == nil {
		return nil, unsafeRewrite(site.NodeID, dim,
			"the registry row for this call site declares no tools locator, so there is nowhere to bind "+
				"skill(s) %s: the SDK this row covers does not take tools at the call site", skillNames(o))
	}

	fset := site.File.Fset
	if expr, ok := discovery.LocateArg(site.Call, loc, fset); ok && expr != nil {
		// 🔴 The existing value is replaced only when it is a STATIC list. `Tools: buildTools(ctx)` is a
		// set assembled at runtime; overwriting it would silently discard every tool it built, which is
		// the same silent-data-loss failure spanRewritePrompt refuses for an interpolated prompt. D-14.3
		// names this case and requires a loud refusal, not a guessed replacement.
		if !isStaticToolList(expr) {
			return nil, unsafeRewrite(site.NodeID, dim,
				"this call site's tool set is assembled at runtime (%s), not written as a static list, so "+
					"replacing it with the bound skill(s) %s would silently discard whatever it builds; this "+
					"engine refuses rather than guess what the runtime set contains",
				describeExpr(expr), skillNames(o))
		}
		start, end, err := byteRange(fset, expr, len(src))
		if err != nil {
			return nil, unsafeRewrite(site.NodeID, dim, "%v", err)
		}
		return []edit{{Start: start, End: end, New: value, NodeID: site.NodeID, Dim: dim}}, nil
	}

	cl := discovery.FindStructArg(site.Call)
	if cl == nil {
		return nil, unsafeRewrite(site.NodeID, dim,
			"skill(s) %s must be constructed at this call site, but it has no options struct literal to "+
				"add a tools argument to", skillNames(o))
	}
	fieldName := lastSegment(loc.Field)
	if fieldName == "" {
		return nil, unsafeRewrite(site.NodeID, dim, "the registry row's tools locator names no field")
	}
	ins, err := insertStructField(fset, cl, src, fieldName, value)
	if err != nil {
		return nil, unsafeRewrite(site.NodeID, dim, "%v", err)
	}
	ins.NodeID, ins.Dim = site.NodeID, dim
	return []edit{ins}, nil
}

// toolListLiteral renders the whole tools slice for a node's bound skills, in the spec's declared
// order — which is identity-bearing (ResolvedNode.SkillRefs is never sorted), so the emitted bytes
// must follow it rather than any convenience ordering.
func toolListLiteral(form toolValueForm, skills []*registry.SkillEntry) (string, error) {
	if len(skills) == 0 {
		// Not reachable through Dimensions() (an empty Skills slice is not an overridden dimension), and
		// checked anyway: an empty construction would silently unbind every tool the call site had.
		return "", fmt.Errorf("no skill was resolved for this binding, so there is nothing to materialize")
	}
	elems := make([]string, 0, len(skills))
	for _, s := range skills {
		schema, err := decodeSealedSchema(s)
		if err != nil {
			return "", err
		}
		e, err := form.element(s.Name, schema)
		if err != nil {
			return "", fmt.Errorf("skill %q: %v", s.Name, err)
		}
		elems = append(elems, e)
	}
	return form.openList + strings.Join(elems, ", ") + form.closeList, nil
}

// isStaticToolList reports whether a tools expression is a list written out at the call site — the
// only shape a materialization may replace.
func isStaticToolList(expr ast.Expr) bool {
	_, ok := unwrapValueExpr(expr).(*ast.CompositeLit)
	return ok
}

// unwrapValueExpr strips parentheses and a leading address-of so the underlying construction is
// visible. `&[]T{…}` and `([]T{…})` are the same static list as `[]T{…}`; refusing them for their
// spelling would be a false refusal.
func unwrapValueExpr(e ast.Expr) ast.Expr {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.UnaryExpr:
			if x.Op == token.AND {
				e = x.X
				continue
			}
			return e
		default:
			return e
		}
	}
}

func skillNames(o variantspec.ResolvedOverride) string {
	names := make([]string, 0, len(o.Skills))
	for _, s := range o.Skills {
		names = append(names, s.Name)
	}
	return fmt.Sprintf("%v", names)
}

func providerDisplay(p string) string {
	if p == "" {
		return "not declared by any registry row"
	}
	return strconv.Quote(p)
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// The sealed schema → a Go literal
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// jsonSchemaDoc is a skill's SEALED input schema, decoded. It is deliberately a decoded document
// rather than the raw bytes: the raw bytes carry the author's whitespace and key order, and splicing
// them into Go source would make the emitted diff depend on how the schema happened to be formatted
// when it was registered. Two registrations of the same contract must produce the same diff.
type jsonSchemaDoc struct {
	doc map[string]any
}

// maxSchemaDepth bounds the recursion. A skill contract nested deeper than this is not a contract this
// engine will render into a single line of Go; it is refused, loudly, rather than truncated.
const maxSchemaDepth = 12

func decodeSealedSchema(s *registry.SkillEntry) (jsonSchemaDoc, error) {
	raw := s.Spec.InputSchema
	if len(raw) == 0 {
		return jsonSchemaDoc{}, fmt.Errorf("skill %q has an empty sealed input schema", s.Name)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	// UseNumber so 1 stays 1 and does not become 1e+00 through a float round-trip: the emitted literal
	// must be a function of the sealed bytes, not of Go's default float formatting.
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return jsonSchemaDoc{}, fmt.Errorf("skill %q: sealed input schema is not valid JSON: %v", s.Name, err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return jsonSchemaDoc{}, fmt.Errorf("skill %q: sealed input schema is not a JSON object", s.Name)
	}
	return jsonSchemaDoc{doc: obj}, nil
}

// propertiesLiteral renders the schema's `properties` as a Go map literal — the shape an SDK that
// takes only the property bag (anthropic's ToolInputSchemaParam) needs.
//
// A schema with no `properties` is REFUSED rather than rendered as an empty bag: an empty bag is a
// valid tool that accepts nothing, so emitting one for a contract that simply is not an object schema
// would ship a tool the model can never call successfully — a change that compiles and degrades
// quality invisibly, which is the exact failure this whole file exists to avoid.
func (d jsonSchemaDoc) propertiesLiteral() (string, error) {
	props, ok := d.doc["properties"]
	if !ok {
		return "", fmt.Errorf("the sealed input schema declares no `properties`, so there is no argument "+
			"shape to construct a tool value from (schema keys: %v)", sortedMapKeys(d.doc))
	}
	if _, ok := props.(map[string]any); !ok {
		return "", fmt.Errorf("the sealed input schema's `properties` is not an object")
	}
	return schemaGoLiteral(props, 0)
}

// wholeLiteral renders the whole schema as a Go map literal — the shape an SDK that takes the entire
// JSON Schema (openai's FunctionParameters) needs.
func (d jsonSchemaDoc) wholeLiteral() (string, error) {
	if _, ok := d.doc["properties"]; !ok {
		return "", fmt.Errorf("the sealed input schema declares no `properties`, so there is no argument "+
			"shape to construct a tool value from (schema keys: %v)", sortedMapKeys(d.doc))
	}
	return schemaGoLiteral(d.doc, 0)
}

// goLiteral renders a decoded JSON value as a single-line, deterministic Go literal.
//
// Single-line is not a style choice: gateMinimal rejects any edit containing a newline, because a
// rewrite that changes the file's line count invalidates TouchedDimension.Line and makes "only the
// targeted lines changed" uncheckable. Map keys are SORTED for the same reason nothing else in this
// package iterates a map — task 2.3's byte-identical output cannot depend on hash seeding.
func schemaGoLiteral(v any, depth int) (string, error) {
	if depth > maxSchemaDepth {
		return "", fmt.Errorf("the sealed input schema nests deeper than %d levels; this engine will not "+
			"render it into a call-site literal", maxSchemaDepth)
	}
	switch x := v.(type) {
	case nil:
		return "nil", nil
	case bool:
		return strconv.FormatBool(x), nil
	case string:
		return strconv.Quote(x), nil
	case json.Number:
		// Rendered verbatim: the sealed bytes said "10" or "0.5", and the literal says the same thing.
		// Re-formatting through float64 would turn a schema's `1` into `1` sometimes and `1e+00` others.
		if !validGoNumber(x.String()) {
			return "", fmt.Errorf("the sealed input schema carries the number %q, whose Go spelling this "+
				"engine will not guess", x.String())
		}
		return x.String(), nil
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			p, err := schemaGoLiteral(e, depth+1)
			if err != nil {
				return "", err
			}
			parts = append(parts, p)
		}
		return "[]any{" + strings.Join(parts, ", ") + "}", nil
	case map[string]any:
		keys := sortedMapKeys(x)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			p, err := schemaGoLiteral(x[k], depth+1)
			if err != nil {
				return "", err
			}
			parts = append(parts, strconv.Quote(k)+": "+p)
		}
		return "map[string]any{" + strings.Join(parts, ", ") + "}", nil
	default:
		return "", fmt.Errorf("the sealed input schema carries a %T, which has no Go literal spelling here", v)
	}
}

// validGoNumber reports whether a JSON number's text is a Go numeric literal as written. JSON and Go
// agree on integers and on plain decimals; they disagree on nothing this engine emits, but a number
// that fails to parse is refused rather than spliced in and left for the compiler.
func validGoNumber(s string) bool {
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func sortedMapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// The BIND SITE — a materialized skill's arguments are validated before its implementation runs
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// BoundSkill is one skill materialized at a call site, paired with the compiled contract its
// version_id sealed (task 2.4).
//
// It exists so that "validated before execution" is STRUCTURAL rather than a rule someone has to
// remember: the only way to run a bound skill through this type is Invoke, and Invoke validates first.
// A caller that wanted to skip the check would have to reach for the implementation directly, which is
// a visible thing to do in review rather than an omission nobody notices.
type BoundSkill struct {
	// NodeID and Name identify which node bound which skill, so a rejection can name both.
	NodeID string
	Name   string
	// VersionID is the sealed version the binding pinned — the same id that pinned the materialized shape.
	VersionID string

	entry *registry.SkillEntry
}

// BoundSkills lists the skills a resolved override materializes at one node, in the spec's declared
// (identity-bearing) order.
func BoundSkills(nodeID string, o variantspec.ResolvedOverride) []BoundSkill {
	out := make([]BoundSkill, 0, len(o.Skills))
	for _, s := range o.Skills {
		out = append(out, BoundSkill{NodeID: nodeID, Name: s.Name, VersionID: s.VersionID, entry: s})
	}
	return out
}

// ValidateArgs checks an argument object against the skill's COMPILED input contract — the same
// contract the materialized tool value was constructed from, so the shape the model was offered and
// the shape the implementation will accept are one thing, not two.
func (b BoundSkill) ValidateArgs(args any) error {
	if b.entry == nil {
		return fmt.Errorf("bound skill %q at node %q carries no compiled contract", b.Name, b.NodeID)
	}
	return b.entry.ValidateInput(args)
}

// Invoke validates args against the sealed input contract and, only then, runs impl.
//
// # Why the failure envelope is toolcontract's, and only toolcontract's (task 2.5)
//
// `tool_error_rate` is one of the two metrics P14 promises will score a skill or tool change without
// any eval change (design Decision 6). A metric over error codes is only well-defined if the code set
// is closed, so every failure here — a contract violation, an implementation error, a panic-shaped
// nil — is reported through toolcontract.Error with a code from ErrorCodeWhitelist. A bound skill that
// invented its own code would make the denominator of a shipped metric depend on which skill happened
// to fail, which is how a measurement quietly stops measuring.
func (b BoundSkill) Invoke(action string, args map[string]any, impl func(map[string]any) (map[string]any, error)) map[string]any {
	if err := b.ValidateArgs(args); err != nil {
		// The implementation is NOT invoked. That is the requirement, and it is why validation is the
		// first statement in this function rather than a check the caller performs beforehand.
		return toolcontract.Error(b.Name, toolcontract.ErrorCodeValidationError, err.Error(), action, args)
	}
	if impl == nil {
		return toolcontract.Error(b.Name, toolcontract.ErrorCodeNotFound,
			fmt.Sprintf("skill %q resolved but its implementation handle is not bound", b.Name), action, args)
	}
	data, err := impl(args)
	if err != nil {
		return toolcontract.Error(b.Name, toolcontract.ErrorCodeCommandFailed, err.Error(), action, args)
	}
	if err := b.entry.ValidateOutput(anyOrNil(data)); err != nil {
		return toolcontract.Error(b.Name, toolcontract.ErrorCodeValidationError, err.Error(), action, args)
	}
	return toolcontract.Ok(b.Name, action, args, data)
}

// anyOrNil hands a nil map to the validator as a JSON `null` rather than as a typed nil map, so the
// contract sees what the wire would carry.
func anyOrNil(m map[string]any) any {
	if m == nil {
		return nil
	}
	return m
}
