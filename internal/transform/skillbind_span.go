package transform

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Skill materialization OUTSIDE Go — the spelling is per language, the SHAPE never is
// ───────────────────────────────────────────────────────────────────────────────────
//
// skillbind.go argued why constructing a tool value is safe at all: the shape comes from the pinned
// version's SEALED schema, and the result is a proposal the harness decides. Neither of those is a fact
// about Go, which is exactly why this file is short. What Go had and a span language does not is a typed
// tree; but a tool value is CONSTRUCTED rather than descended into, so the only thing the tree was
// buying — knowing which sub-expression is which — is not needed here. What is needed is:
//
//	how does THIS language, for THIS provider's SDK, at THIS generation, spell a tool list?
//
// That is a row. Adding Python cost a row and a literal renderer, not a second theory of binding.
//
// 🔴 The renderers below are the reason two languages cannot drift: each takes the SAME decoded sealed
// schema and emits its own language's literal for it. There is no per-language schema interpretation,
// no per-language field selection, and no place for one language to include a property another drops.
// TestBoundSkillContractParityAcrossLanguages asserts exactly that over a shared fixture.

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// The rows
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// init registers the syntactic spellings beside the Go ones, in the SAME table the Go rewriter refuses
// from. A second table for "the span languages" would be a second coverage source, which is the thing
// P13's contract exists to prevent — and it would drift in the usual direction, because the second table
// is always the one nobody re-reads.
func init() {
	for cell, form := range spanToolValueForms {
		if _, clash := toolValueForms[cell]; clash {
			// Two rows for one cell is a bug that would otherwise be decided by map iteration order — i.e.
			// silently, differently per run. Fail at init rather than emit a different diff on Tuesdays.
			panic(fmt.Sprintf("transform: duplicate tool-value form for (%s, %s)", cell.language, cell.provider))
		}
		toolValueForms[cell] = form
	}
}

// spanToolValueForms are the tool-value spellings for the syntactic languages.
//
// Each row names the SDK generation it targets, and that is part of the claim rather than a footnote: an
// anthropic Python call site on a pre-`input_schema` generation is not covered by the row below, and the
// refusal says which generation it was written against.
var spanToolValueForms = map[toolValueCell]toolValueForm{
	{"python", "anthropic"}: {
		openList: "[", closeList: "]",
		sdkNote: "anthropic-sdk-python v0.3x (tools as dicts with name/input_schema)",
		element: func(name string, schema jsonSchemaDoc) (string, error) {
			props, err := schema.wholeLiteralIn(litPython)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"name": %s, "input_schema": %s}`, strconv.Quote(name), props), nil
		},
	},
	{"python", "openai"}: {
		openList: "[", closeList: "]",
		sdkNote: "openai-python v1 (tools as dicts with type=function)",
		element: func(name string, schema jsonSchemaDoc) (string, error) {
			props, err := schema.wholeLiteralIn(litPython)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"type": "function", "function": {"name": %s, "parameters": %s}}`,
				strconv.Quote(name), props), nil
		},
	},
	{"typescript", "anthropic"}: tsAnthropic("@anthropic-ai/sdk v0.2x (tools as objects with name/input_schema)"),
	{"javascript", "anthropic"}: tsAnthropic("@anthropic-ai/sdk v0.2x (tools as objects with name/input_schema)"),
	{"typescript", "openai"}:    tsOpenAI("openai-node v4 (tools as objects with type: 'function')"),
	{"javascript", "openai"}:    tsOpenAI("openai-node v4 (tools as objects with type: 'function')"),
}

// tsAnthropic / tsOpenAI exist because TypeScript and JavaScript spell an object literal identically —
// the two rows are the same spelling under two language labels, and writing it once is what keeps them
// from acquiring an accidental difference. They are still TWO ROWS, because coverage is per cell and a
// reader asking about JavaScript must get an answer that names JavaScript.
func tsAnthropic(sdk string) toolValueForm {
	return toolValueForm{
		openList: "[", closeList: "]", sdkNote: sdk,
		element: func(name string, schema jsonSchemaDoc) (string, error) {
			props, err := schema.wholeLiteralIn(litJS)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(`{ name: %s, input_schema: %s }`, strconv.Quote(name), props), nil
		},
	}
}

func tsOpenAI(sdk string) toolValueForm {
	return toolValueForm{
		openList: "[", closeList: "]", sdkNote: sdk,
		element: func(name string, schema jsonSchemaDoc) (string, error) {
			props, err := schema.wholeLiteralIn(litJS)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(`{ type: "function", function: { name: %s, parameters: %s } }`,
				strconv.Quote(name), props), nil
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// One decoded schema, several literal dialects
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// literalDialect is how one language spells the four JSON scalars and its two containers. It is DATA
// for the same reason argumentForm is: the difference between Python and JavaScript here is `True` vs
// `true`, and a difference that small is exactly the kind that gets copied wrong into a second function.
type literalDialect struct {
	name              string
	null              string
	boolTrue          string
	boolFalse         string
	openMap, closeMap string
	// mapEntry renders one key/value pair: `"k": v` in Python and JSON, `"k" to v` in Kotlin.
	mapEntry            func(key, value string) string
	openList, closeList string
}

var litPython = literalDialect{
	name: "python", null: "None", boolTrue: "True", boolFalse: "False",
	openMap: "{", closeMap: "}", openList: "[", closeList: "]",
	mapEntry: func(k, v string) string { return k + ": " + v },
}

var litJS = literalDialect{
	name: "javascript", null: "null", boolTrue: "true", boolFalse: "false",
	openMap: "{ ", closeMap: " }", openList: "[", closeList: "]",
	mapEntry: func(k, v string) string { return k + ": " + v },
}

// wholeLiteralIn renders the whole sealed schema as a literal in one dialect.
//
// It refuses a schema with no `properties` for exactly the reason the Go renderer does: an empty
// property bag is a valid tool that accepts nothing, so it compiles and then fails every call the model
// makes against it — a change that degrades quality invisibly, which is the failure this whole axis is
// built to avoid.
func (d jsonSchemaDoc) wholeLiteralIn(dialect literalDialect) (string, error) {
	if _, ok := d.doc["properties"]; !ok {
		return "", fmt.Errorf("the sealed input schema declares no `properties`, so there is no argument "+
			"shape to construct a tool value from (schema keys: %v)", sortedMapKeys(d.doc))
	}
	return schemaLiteral(d.doc, dialect, 0)
}

// schemaLiteral is the shared walk. Keys are SORTED and numbers are emitted verbatim from the sealed
// bytes, both for the same reason the Go renderer does it: the emitted diff must be a function of the
// sealed contract, not of map iteration order or of a float round-trip.
func schemaLiteral(v any, d literalDialect, depth int) (string, error) {
	if depth > maxSchemaDepth {
		return "", fmt.Errorf("the sealed input schema nests deeper than %d levels; this engine will not "+
			"render it into a call-site literal", maxSchemaDepth)
	}
	switch x := v.(type) {
	case nil:
		return d.null, nil
	case bool:
		if x {
			return d.boolTrue, nil
		}
		return d.boolFalse, nil
	case string:
		return strconv.Quote(x), nil
	case json.Number:
		if !validGoNumber(x.String()) {
			return "", fmt.Errorf("the sealed input schema carries the number %q, whose literal spelling this "+
				"engine will not guess", x.String())
		}
		return x.String(), nil
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			p, err := schemaLiteral(e, d, depth+1)
			if err != nil {
				return "", err
			}
			parts = append(parts, p)
		}
		return d.openList + strings.Join(parts, ", ") + d.closeList, nil
	case map[string]any:
		keys := sortedMapKeys(x)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			p, err := schemaLiteral(x[k], d, depth+1)
			if err != nil {
				return "", err
			}
			parts = append(parts, d.mapEntry(strconv.Quote(k), p))
		}
		return d.openMap + strings.Join(parts, ", ") + d.closeMap, nil
	default:
		return "", fmt.Errorf("the sealed input schema carries a %T, which has no literal spelling here", v)
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// The syntactic rewriter
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// spanMaterializeSkills constructs the SDK tool value at a span-located call site.
//
// 🔴 The order of the questions is the whole of P14 FR29, and it is NOT the order that is easiest to
// write. The easiest order asks "does this language have a materializer?" first, because that check is
// one map lookup and it short-circuits everything. It is also the order that tells a Python author with
// `**kwargs` to wait for a Python rewriter — which they already have, and which would refuse them for
// the same reason it does today. So the language question is asked LAST, after the skill, the provider,
// the row and the call site have each had their say.
func spanMaterializeSkills(site discovery.SpanCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimSkills)

	// ── 1. a fact about the SKILL. No language is involved: an unpinned or contract-less skill refuses
	// identically everywhere, so telling the reader about their language would be a detour.
	if len(o.Skills) == 0 {
		return nil, refuseShape(site.NodeID, dim,
			"the skills dimension was dispatched with no resolved skill, so there is nothing to materialize")
	}

	// ── 2. a fact about the ROW and the PROVIDER's SDK. Bedrock's tools live inside a serialized body:
	// there is no tool value to construct in ANY language, so this is not a coverage gap and must not
	// wear one's wording.
	if hasOpacity(site.Opacity, "tools") {
		return nil, refuseShape(site.NodeID, dim,
			"this call site's SDK carries its tools inside an opaque serialized body (the registry row marks "+
				"it opaque), so there is no tool value to construct for skill(s) %s — in this or any language",
			skillNames(o))
	}
	loc := site.ArgMap.Tools
	if loc == nil {
		return nil, refuseShape(site.NodeID, dim,
			"the registry row matched for this call site declares no tools locator, so there is nowhere to "+
				"bind skill(s) %s: the SDK this row covers does not take tools at the call site", skillNames(o))
	}

	// ── 3. a fact about the CALL SITE's own source. An unpacking is not "the argument is missing" — the
	// call has an argument list and what is in it is unwritten, permanently.
	if u := site.KeywordUnpacking; u != nil {
		if _, written := site.Keywords[loc.Name]; !written {
			return nil, refuseShape(site.NodeID, dim,
				"this call site passes %s, so what it offers the model is assembled somewhere else in the "+
					"program and is not written here; appending a tools argument would not override that set, it "+
					"would collide with it. 🔴 This is a property of the call site, NOT of %s support: a "+
					"materializer for this language refuses it for exactly this reason too",
				u.Text, languageDisplay(site.Language))
		}
	}

	// ── 4. …and ONLY now, a fact about US: is there a spelling for this (language, provider)?
	form, ok := toolValueForms[toolValueKey(site.Language, site.ProviderHint)]
	if !ok {
		return nil, refuseNoMaterializer(site.NodeID, dim,
			"this engine has no declared tool-value spelling for (%s, %s), so there is no SDK shape to "+
				"construct skill(s) %s into; the cells it can materialize are %s. Binding here would mean "+
				"guessing an SDK's tool type, and a guess that parses is the failure mode with no downstream net",
			languageDisplay(site.Language), providerDisplay(site.ProviderHint), skillNames(o),
			describeSkillCells())
	}

	value, err := toolListLiteral(form, o.Skills)
	if err != nil {
		return nil, refuseShape(site.NodeID, dim, "%v", err)
	}

	// The tools argument is written: replace it, but only when what it wrote is a list the author wrote.
	if v, written := site.Keywords[loc.Name]; written {
		if err := checkSpan(site.NodeID, dim, v.Value, len(src)); err != nil {
			return nil, err
		}
		existing := string(src[v.Value.Start:v.Value.End])
		if !discovery.IsWrittenList(site.Language, existing) {
			return nil, refuseShape(site.NodeID, dim,
				"this call site's tool set is %s, not a list written here, so replacing it with skill(s) %s "+
					"would silently discard whatever it holds; this engine refuses rather than guess what the "+
					"runtime set contains", describeSpanValue(v, existing), skillNames(o))
		}
		return []edit{{Start: v.Value.Start, End: v.Value.End, New: value, NodeID: site.NodeID, Dim: dim}}, nil
	}

	// The tools argument is absent: insert it, if this call site offers anywhere to put one.
	ins := site.KeywordInsert
	if ins == nil {
		return nil, refuseShape(site.NodeID, dim,
			"skill(s) %s must be constructed at this call site, but it offers no place to add a %s argument",
			skillNames(o), loc.Name)
	}
	text := ins.Prefix + loc.Name + ins.Assign + value
	return []edit{{Start: ins.At, End: ins.At, New: text, NodeID: site.NodeID, Dim: dim}}, nil
}

// describeSkillCells renders the covered (language, provider) cells for a refusal that tells the reader
// what WOULD have worked. Read from the form table, so it can never name a cell the engine refuses.
func describeSkillCells() string {
	out := make([]string, 0, len(toolValueForms))
	for cell := range toolValueForms {
		out = append(out, fmt.Sprintf("(%s, %s)", cell.language, cell.provider))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func hasOpacity(opacity []string, field string) bool {
	for _, o := range opacity {
		if strings.EqualFold(strings.TrimSpace(o), field) {
			return true
		}
	}
	return false
}
