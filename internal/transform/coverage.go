package transform

import (
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// COVERAGE — what this engine can apply, in which language, stated as a TOTAL function
// ────────────────────────────────────────────────────────────────────────────────────
//
// This file exists because of one property the previous coverage reads did not have: they listed the
// cells that WORK. `MaterializerCoverage()` returned two rows; `ContextMaterializerCoverage()` returned
// the languages with a splitter; `argumentForms` held the languages with a named-argument form. A reader
// asking "what happens to Rust?" found nothing — and *nothing* renders on every surface as "not
// applicable", which is a claim about the customer's code.
//
// 🔴 So absence is not a value here. AxisCoverage() emits a cell for EVERY registered language on EVERY
// axis, and a cell the engine cannot apply carries a CAUSE CLASS and the artifact whose absence explains
// it. TestCoverageIsTotalOverRegisteredLanguages fails the moment a frontend is added without one.
//
// 🚫 And it is a READ, never a second table. Every value below is derived from the table the rewriter
// itself dispatches on — toolValueForms, contextForms, spanContextMaterializers, statementResolvers, the
// signature registry's rows. A hand-maintained copy would be exactly the drift this file was written to
// end: the copy is always the optimistic one.

// CauseClass is the refusal taxonomy (P13 FR43). Three classes, because three different people are
// being told three different things — and a consumer must be able to tell which without reading prose.
type CauseClass string

const (
	// CauseNotAtCallSite — the change cannot be written into source in ANY language, because the value
	// does not exist until run time (a summarized context, a retrieved chunk set, a provider swap that
	// belongs at the gateway). Nobody's backlog; the refusal is permanent and identical everywhere.
	CauseNotAtCallSite CauseClass = "not-expressible-at-a-call-site"
	// CauseCallSiteShape — THIS call site's own source cannot express it: arguments unpacked from a
	// mapping, a tool list assembled at run time, no registry row locator, an SDK that binds in an
	// opaque body. The customer's engineer can act on it; a materializer would not change it.
	CauseCallSiteShape CauseClass = "call-site-cannot-carry-it"
	// CauseNoMaterializer — the platform has not landed the rewriter, splitter, resolver or form row for
	// this cell. 🔴 The ONLY class that names work we owe, which is why nothing else may borrow it: the
	// moment a call-site fact wears this label, a user is told to wait for something that will not help.
	CauseNoMaterializer CauseClass = "no-materializer-for-this-language"
)

// CauseClasses lists the three, in escalating specificity-of-ownership order (nobody / them / us).
func CauseClasses() []CauseClass {
	return []CauseClass{CauseNotAtCallSite, CauseCallSiteShape, CauseNoMaterializer}
}

// Valid reports whether c is one of the three. A refusal carrying anything else is a bug, not a fourth
// class: the set is closed so that a consumer's switch is exhaustive.
func (c CauseClass) Valid() bool {
	for _, k := range CauseClasses() {
		if c == k {
			return true
		}
	}
	return false
}

// CoverageStatus is what an axis does in one cell.
type CoverageStatus string

const (
	// CoverageMaterializes — the axis emits a change here.
	CoverageMaterializes CoverageStatus = "materializes"
	// CoverageRefuses — the axis refuses here, with Cause and MissingArtifact saying which kind of thing
	// is missing and what would close it.
	CoverageRefuses CoverageStatus = "refuses"
)

// CoverageCell is one (axis, language, form) answer.
//
// Form is what the axis binds against WITHIN a language, and it is not decoration: "go materializes
// skills" is not true of a Go call site whose provider has no declared spelling, and "python
// materializes context" is not true of a summarization policy. A cell without its form is a claim the
// engine cannot honour.
type CoverageCell struct {
	Axis     string         `json:"axis"`
	Language string         `json:"language"`
	Form     string         `json:"form"`
	Status   CoverageStatus `json:"status"`
	// Cause is empty when Status is materializes; one of the three classes otherwise.
	Cause CauseClass `json:"cause,omitempty"`
	// MissingArtifact names what would close a CauseNoMaterializer gap — a form row, a list splitter, a
	// statement resolver, a registry row, a frontend field. Empty for the other two classes, where there
	// is no artifact to build: that asymmetry is the point (P13 FR45).
	MissingArtifact string `json:"missing_artifact,omitempty"`
	// Note carries the cell's own sentence — the SDK generation a spelling targets, the reason a policy
	// is not expressible. It is the human half; Cause is the machine half.
	Note string `json:"note,omitempty"`
}

// Refused reports whether this cell is a refusal.
func (c CoverageCell) Refused() bool { return c.Status == CoverageRefuses }

// CoverageAxes are the axes that materialize at a call site, in dimension order. Wiring is included
// under its own label because it is an axis a user can request even though it is not a `Dimension`
// carried on a NodeOverride.
func CoverageAxes() []string {
	return []string{
		string(variantspec.DimModel),
		string(variantspec.DimPrompt),
		string(variantspec.DimSkills),
		string(variantspec.DimTools),
		string(variantspec.DimContext),
		wiringRefusalDim,
	}
}

// RegisteredLanguages is the language set coverage must be total over — read from the discovery
// frontends themselves, so adding a frontend adds a row to every axis's table and fails the totality
// test until each is answered. A literal list here would let a new language ship silently half-covered.
func RegisteredLanguages() []string {
	out := make([]string, 0, 8)
	for _, fe := range discovery.DefaultFrontends() {
		out = append(out, fe.Language())
	}
	sort.Strings(out)
	return out
}

// AxisCoverage is THE total coverage read: every axis × every registered language × every form that
// axis binds against.
//
// Sorted, so a doc, a console table and a CLI listing render identically and a diff between two builds
// is readable.
func AxisCoverage() []CoverageCell {
	var out []CoverageCell
	for _, lang := range RegisteredLanguages() {
		out = append(out, modelPromptCoverage(lang)...)
		out = append(out, skillCoverage(lang)...)
		out = append(out, toolPruneCoverage(lang)...)
		out = append(out, contextCoverage(lang)...)
		out = append(out, wiringCoverage(lang)...)
	}
	sortCoverage(out)
	return out
}

// CoverageFor filters the total read to one axis, for a caller that only cares about one.
func CoverageFor(axis string) []CoverageCell {
	var out []CoverageCell
	for _, c := range AxisCoverage() {
		if c.Axis == axis {
			out = append(out, c)
		}
	}
	return out
}

func sortCoverage(cells []CoverageCell) {
	sort.Slice(cells, func(i, j int) bool {
		a, b := cells[i], cells[j]
		if a.Axis != b.Axis {
			return a.Axis < b.Axis
		}
		if a.Language != b.Language {
			return a.Language < b.Language
		}
		return a.Form < b.Form
	})
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Per-axis reads. Each one derives from the table its rewriter dispatches on — never from a copy.
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// modelPromptCoverage reads the SIGNATURE REGISTRY, because that is what decides whether a model or
// prompt can be located: the engine has a row for all seven languages (engines.go), so what varies is
// whether any row for this language declares a locator for the dimension.
//
// The form is the registry row, which is why Kotlin's answer is legible: five rows, none declaring an
// arg_map, because those SDKs bind on a builder at construction. That is a fact about the SDK — the
// customer's call site genuinely has no model argument — so it is CauseCallSiteShape, and the artifact
// that would change it is a row declaring the binding form (P13 FR53), not a rewriter.
func modelPromptCoverage(lang string) []CoverageCell {
	reg, err := discovery.DefaultRegistry()
	if err != nil {
		// A registry that will not load is not a coverage answer; say so rather than reporting an empty
		// (and therefore optimistic-looking) table.
		return []CoverageCell{
			{Axis: string(variantspec.DimModel), Language: lang, Form: "signature registry",
				Status: CoverageRefuses, Cause: CauseNoMaterializer,
				MissingArtifact: "a loadable signature registry", Note: err.Error()},
			{Axis: string(variantspec.DimPrompt), Language: lang, Form: "signature registry",
				Status: CoverageRefuses, Cause: CauseNoMaterializer,
				MissingArtifact: "a loadable signature registry", Note: err.Error()},
		}
	}
	rows := reg.ForLanguage(lang).Rows
	var out []CoverageCell
	for _, axis := range []string{string(variantspec.DimModel), string(variantspec.DimPrompt)} {
		if len(rows) == 0 {
			out = append(out, CoverageCell{
				Axis: axis, Language: lang, Form: "any SDK",
				Status: CoverageRefuses, Cause: CauseNoMaterializer,
				MissingArtifact: "signature registry rows for " + lang,
				Note:            "discovery registers a " + lang + " frontend, but no registry row covers an SDK in it",
			})
			continue
		}
		for _, r := range rows {
			loc := locatorFor(r, axis)
			cell := CoverageCell{Axis: axis, Language: lang, Form: r.ID}
			if loc != nil {
				cell.Status = CoverageMaterializes
				cell.Note = "bound at " + bindingFormName(loc)
			} else {
				cell.Status = CoverageRefuses
				cell.Cause = CauseCallSiteShape
				cell.Note = "this row's SDK states no " + axis + " at a locatable binding site; " +
					"an arg_map entry naming where it binds would materialize it"
			}
			out = append(out, cell)
		}
	}
	return out
}

func locatorFor(r discovery.SignatureRow, axis string) *discovery.ArgLocator {
	switch axis {
	case string(variantspec.DimModel):
		return r.ArgMap.Model
	case string(variantspec.DimPrompt):
		return r.ArgMap.Prompt
	case string(variantspec.DimTools):
		return r.ArgMap.Tools
	}
	return nil
}

// bindingFormName names the SHAPE a locator points at, which is the thing P13 FR52 generalized: a named
// argument is one form of a binding site, a builder-chain call is a second, a request-value field a
// third. Naming it in coverage is what stops "Java has no named arguments" from being read as "Java
// cannot be rewritten".
func bindingFormName(l *discovery.ArgLocator) string {
	if l == nil {
		return "nowhere"
	}
	switch l.Form {
	case discovery.LocParamName:
		return "a named argument"
	case discovery.LocPositional:
		return "a positional argument"
	case discovery.LocFieldPath:
		return "a field of the options value"
	case discovery.LocOptionCtor:
		return "an option constructor"
	case discovery.LocBuilderCall:
		return "a builder-chain call"
	case discovery.LocRequestField:
		return "a field of the request value"
	case discovery.LocConst:
		return "a constant declared by the row"
	case discovery.LocOpaque:
		return "an opaque serialized body"
	}
	return string(l.Form)
}

// skillCoverage reads toolValueForms — the table materializeSkills dispatches on — and reports every
// (language, provider) pair, not only the ones with a spelling.
//
// The provider set comes from the registry's own provider hints, so a provider the platform can
// DISCOVER but not BIND appears as a refusal rather than as silence.
func skillCoverage(lang string) []CoverageCell {
	const axis = string(variantspec.DimSkills)
	var out []CoverageCell
	for _, p := range knownProviders() {
		cell := CoverageCell{Axis: axis, Language: lang, Form: p}
		if form, ok := toolValueForms[toolValueKey(lang, p)]; ok {
			cell.Status = CoverageMaterializes
			cell.Note = form.sdkNote
			out = append(out, cell)
			continue
		}
		cell.Status = CoverageRefuses
		if reason, opaque := opaqueToolProviders[p]; opaque {
			// 🔴 Not our backlog. Bedrock's tools live inside a serialized body: there is no tool value
			// to construct in ANY language, so a spelling row would have nothing to write.
			cell.Cause = CauseCallSiteShape
			cell.Note = reason
			out = append(out, cell)
			continue
		}
		cell.Cause = CauseNoMaterializer
		cell.MissingArtifact = fmt.Sprintf("a tool-value spelling row for (%s, %s) naming the SDK generation it targets", lang, p)
		cell.Note = "the sealed schema already supplies the SHAPE in every language; what is missing is how this SDK spells a tool list here"
		out = append(out, cell)
	}
	return out
}

// opaqueToolProviders are providers whose tools are not a call-site value in any language. Kept beside
// the coverage read rather than inside toolValueForms because they are the ABSENCE of a spelling for a
// stated reason, and a row in the form table would imply a form exists.
var opaqueToolProviders = map[string]string{
	"bedrock": "this SDK carries its tools inside an opaque serialized body (the registry row marks it " +
		"opaque), so there is no tool value to construct or delete at the call site in any language",
}

// knownProviders lists every provider any registry row declares, sorted. Derived, so a provider added to
// the registry immediately appears in coverage as an honest refusal instead of being invisible.
func knownProviders() []string {
	reg, err := discovery.DefaultRegistry()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, r := range reg.Rows {
		if r.ProviderHint != "" {
			seen[r.ProviderHint] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// toolPruneCoverage reports the DELETION half of P14, which is blocked somewhere else entirely: not on a
// rewriter (deleting a written element is the easiest edit in the engine) but on the discovery frontend
// recording WHICH written element is which tool. Stating that correctly is what sends the work to the
// right package.
func toolPruneCoverage(lang string) []CoverageCell {
	const axis = string(variantspec.DimTools)
	cell := CoverageCell{Axis: axis, Language: lang, Form: "a statically-written tool list"}
	if discovery.RecordsToolSplit(lang) {
		cell.Status = CoverageMaterializes
		cell.Note = "a tool the frontend recorded with a declaration location is deleted; one recorded without a location refuses"
		return []CoverageCell{cell}
	}
	cell.Status = CoverageRefuses
	cell.Cause = CauseNoMaterializer
	cell.MissingArtifact = "the " + lang + " frontend recording each tool's identifier and the location of its declaration"
	cell.Note = "deletion itself is language-neutral; what is missing is the recorded split to prune AGAINST"
	return []CoverageCell{cell}
}

// contextCoverage folds the existing per-policy read into the total table. The per-policy verdict is
// language-INDEPENDENT for everything except selection: whether a summary can be written into source is
// a fact about the policy, and no splitter will ever change it.
func contextCoverage(lang string) []CoverageCell {
	const axis = string(variantspec.DimContext)
	var out []CoverageCell
	hasSplitter := hasContextSelection(lang)
	for _, policy := range sortedPolicyNames() {
		form := contextForms[policy]
		cell := CoverageCell{Axis: axis, Language: lang, Form: policy}
		switch form.kind {
		case ctxIdentity:
			cell.Status = CoverageMaterializes
			cell.Note = "equivalent to the un-rewritten call site; nothing is deleted"
		case ctxNotAtCallSite:
			cell.Status = CoverageRefuses
			cell.Cause = CauseNotAtCallSite
			cell.Note = form.reason
		case ctxSelect:
			if hasSplitter {
				cell.Status = CoverageMaterializes
				cell.Note = "materialized by deleting the turns the policy does not retain"
			} else {
				cell.Status = CoverageRefuses
				cell.Cause = CauseNoMaterializer
				cell.MissingArtifact = "a " + lang + " list splitter (spanContextMaterializers)"
				cell.Note = "retention is decided by the shared policy code; what is missing is splitting this language's written list into its elements"
			}
		}
		out = append(out, cell)
	}
	return out
}

func sortedPolicyNames() []string {
	out := make([]string, 0, len(contextForms))
	for p := range contextForms {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// wiringCoverage reads statementResolvers — the table planWiringSwap dispatches on.
func wiringCoverage(lang string) []CoverageCell {
	cell := CoverageCell{Axis: wiringRefusalDim, Language: lang, Form: "an adjacent statement transposition"}
	if HasStatementMaterializer(lang) {
		cell.Status = CoverageMaterializes
		cell.Note = "a single transposition of two adjacent sibling statements; every other shape refuses by shape, in every language"
		return []CoverageCell{cell}
	}
	cell.Status = CoverageRefuses
	cell.Cause = CauseNoMaterializer
	cell.MissingArtifact = "a " + lang + " statement resolver (statementResolvers)"
	cell.Note = "the plan, the edge check, the coherence gate and the permutation invariant are language-neutral; what is missing is this language's statement boundaries"
	return []CoverageCell{cell}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Rendering — for a refusal, a console badge, a CLI listing and the published doc
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// CoverageTableVersion is the content version of the coverage table (P13 FR49).
//
// 🔴 It is a hash of the table's CONTENT, not a hand-bumped integer, because a version that only moves
// when a row is added would miss the change that actually confuses a user: a cell whose cause or note
// was narrowed. The offline CLI reports this string in a refusal, so "the CLI refused what the console
// accepted" is a one-line diagnosis instead of a mystery.
func CoverageTableVersion() string {
	var b strings.Builder
	for _, c := range AxisCoverage() {
		fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%s|%s\n",
			c.Axis, c.Language, c.Form, c.Status, c.Cause, c.MissingArtifact, c.Note)
	}
	return fmt.Sprintf("cov-%08x", fnv32(b.String()))
}

func fnv32(s string) uint32 {
	const (
		offset = 2166136261
		prime  = 16777619
	)
	h := uint32(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime
	}
	return h
}

// CoverageLanguagesFor lists the languages an axis materializes in, for a refusal that tells the reader
// what WOULD have worked. Derived from the same total read, so it cannot name a language the engine
// refuses.
func CoverageLanguagesFor(axis string) []string {
	seen := map[string]bool{}
	for _, c := range CoverageFor(axis) {
		if c.Status == CoverageMaterializes {
			seen[c.Language] = true
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}
