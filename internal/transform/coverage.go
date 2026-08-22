package transform

import (
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
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
		string(variantspec.DimMemory),
		string(variantspec.DimHarness),
		// P34: the axis split. `harness` above now answers for the EXECUTION ENVELOPE and `loop` answers
		// for the ITERATION POLICY — the strategies that used to be reported under `harness`.
		string(variantspec.DimLoop),
		wiringRefusalDim,
		// P34: topology. Like `wiring` it is not a `Dimension` — design D3 keeps it spec-level, because
		// every Dimension is a property of ONE node and topology is a property BETWEEN nodes — but it is
		// an axis a user can request, so it must have an answer in every language or it renders as "not
		// applicable", which is a claim about the customer's code.
		graphRefusalDim,
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
		out = append(out, memoryCoverage(lang)...)
		out = append(out, harnessCoverage(lang)...)
		out = append(out, loopCoverage(lang)...)
		out = append(out, wiringCoverage(lang)...)
		out = append(out, graphCoverage(lang)...)
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

// memoryCoverage reads the builtin strategy set and the MATERIALIZER TABLE, and answers for every
// (language, strategy) cell (P17, narrowed per-cell by P18 §5.1).
//
// 🔴 This read was UNIFORM under P17 and is not any more, and the change is the honest one rather than a
// loosening. P17's uniformity was a true claim about a true state: nothing was missing per-language,
// because a memory RUNTIME was missing everywhere. P18 shipped that runtime and a Python materializer,
// so the axis became asymmetric exactly like skills and context — and a table that kept saying "this is
// missing in every language" would now be lying in the OPPOSITE direction, over-refusing rather than
// over-claiming. Both are the same defect: a coverage claim that does not match the engine.
//
// It derives from HasMemoryMaterializer — the same table spanMaterializeMemory dispatches on — so the
// claim and the behaviour cannot disagree.
//
// `none` materializes everywhere, because the identity strategy is genuinely equivalent to the
// un-rewritten call site — exactly as `full`/identity does in the context axis. That is what makes
// "none ≡ absent" true at the coverage layer too, so a user selecting `none` is never told their no-op
// was refused.
//
// 🚫 A `materializes` cell is a claim about the (language, strategy) PAIR, not a promise about every call
// site in it: the record half needs a statement-level assignment, and a call site without one is refused
// by shape. That is the same granularity contextCoverage uses, and the Note says so rather than leaving a
// reader to discover it at apply time.
func memoryCoverage(lang string) []CoverageCell {
	const axis = string(variantspec.DimMemory)
	hasMaterializer := HasMemoryMaterializer(lang)
	var out []CoverageCell
	for _, st := range registry.BuiltinMemoryStrategies() {
		cell := CoverageCell{Axis: axis, Language: lang, Form: st.Name()}
		switch {
		case st.Name() == registry.StrategyNone:
			cell.Status = CoverageMaterializes
			cell.Note = "the identity strategy; equivalent to the un-rewritten call site, so nothing is emitted"
		case MemoryStrategyMaterializesIn(lang, st.Name()):
			cell.Status = CoverageMaterializes
			cell.Note = "materialized by wrapping the written message list with a recall and recording the " +
				"turn after the call — BOTH halves or the call site is refused, because a memory that reads " +
				"a store nothing fills behaves as `none`. Needs a written message list and a statement-level " +
				"assignment; a call site with neither is refused by shape. The generated module requires a " +
				"session id and raises rather than defaulting one" + goProviderCaveat(lang)
		case hasMaterializer:
			// The language HAS a materializer and this strategy still cannot run on it. Today that is Go's
			// content-reading strategies, and the cause is permanent rather than owed: a Go message is the
			// customer's SDK type, so reading its text would mean importing their SDK into generated code.
			//
			// 🔴 CauseNotAtCallSite carries NO missing artifact, and the asymmetry is the point (P13 FR45):
			// naming one would promise work that would not help, because there is nothing to build.
			cell.Status = CoverageRefuses
			cell.Cause = CauseNotAtCallSite
			cell.Note = "this strategy decides what to keep by reading message TEXT, and in " +
				languageDisplay(lang) + " a message is your SDK's own type — the generated module would have " +
				"to import your SDK to read a field off it. " + languageDisplay(lang) + " materializes the " +
				"strategies whose retention is a function of count; Python materializes this one, because " +
				"there a message is a dict"
		default:
			cell.Status = CoverageRefuses
			cell.Cause = CauseNoMaterializer
			cell.MissingArtifact = "a " + lang + " memory module and its call-site rewriter (memoryMaterializers)"
			cell.Note = "the memory runtime is shared and has landed; what is missing for this language is " +
				"the emitted module a rewritten call site would call, and the rewriter that emits it " +
				"(covered today: " + strings.Join(MemoryMaterializerLanguages(), ", ") + ")"
		}
		out = append(out, cell)
	}
	return out
}

// goProviderCaveat states the one extra precondition a Go cell carries: the record half needs a verified
// response conversion for the call site's provider, which is a per-provider fact the cell cannot know.
func goProviderCaveat(lang string) string {
	if !strings.EqualFold(lang, "go") {
		return ""
	}
	return ". In Go it additionally needs a verified response conversion for the call site's provider " +
		"(declared today: " + memoryResponseProvidersDisplay() + "), because the response and the stored " +
		"message are different static types"
}

// loopCoverage reads the builtin LOOP strategy set and the MATERIALIZER TABLE, and answers for every
// (language, strategy) cell (P34 task 2.7 / decisions.md D-34.7; formerly harnessCoverage, P18 §5).
//
// 🔴 This function did not change what it computes — it changed what AXIS it files the answer under. The
// strategies it reports are iteration policies, and after ADR-014 those live on `loop`. Filing them under
// `harness` would leave the split invisible on every surface that reads coverage, which is most of them.
//
// It derives from HasHarnessMaterializer and harnessHostService — the same two functions the rewriters
// dispatch on — so the claim and the behaviour cannot disagree. A hand-written copy would be the drift
// this file exists to end.
//
// Three distinct answers, and telling them apart is the whole value of the read:
//
//	single-shot                             MATERIALIZES everywhere. One turn is the un-rewritten call
//	                                        site, so there is nothing to emit and nothing to refuse.
//	react-loop/plan-execute/critic-loop      REFUSE everywhere, permanently, with CauseNotAtCallSite and
//	                                        NO missing artifact: each needs a host service a call site
//	                                        cannot inject, and that is true in every language before and
//	                                        after every rewriter lands.
//	reflexion                               the only cell whose answer depends on the LANGUAGE, because
//	                                        it is the only multi-turn strategy needing no second actor.
//
// 🚫 A `materializes` cell is a claim about the (language, strategy) PAIR, not a promise about every call
// site in it: the loop needs a re-invocable call whose answer is readable, and a call site without one is
// refused by shape. The Note says so rather than leaving a reader to discover it at apply time.
func loopCoverage(lang string) []CoverageCell {
	const axis = string(variantspec.DimLoop)
	var out []CoverageCell
	for _, st := range registry.BuiltinLoopStrategies() {
		cell := CoverageCell{Axis: axis, Language: lang, Form: st.Name()}
		switch {
		case st.Name() == registry.StrategySingleShot:
			cell.Status = CoverageMaterializes
			cell.Note = "the identity strategy; one turn is exactly the un-rewritten call site, so nothing " +
				"is emitted and nothing is refused"
		case harnessHostService(st.Name()) != "":
			// 🔴 Not our backlog, and permanently so. CauseNotAtCallSite carries NO missing artifact: there
			// is nothing to build, because a call site has nowhere to inject a tool executor, a planner or
			// a critic, and the generated module may not reach a provider (P18 decisions.md D-10).
			cell.Status = CoverageRefuses
			cell.Cause = CauseNotAtCallSite
			cell.Note = "this strategy needs " + harnessHostService(st.Name()) +
				", which no call site can supply in any language"
		case HarnessStrategyMaterializesIn(lang, st.Name()):
			cell.Status = CoverageMaterializes
			cell.Note = "materialized by wrapping the written call in a bounded loop that re-invokes it and " +
				"reads each answer against the stop condition — BOTH halves or the call site is refused, " +
				"because a loop that cannot decide when to stop is the same call priced N times. Needs a " +
				"re-invocable call whose result is assigned at statement level; a call site without one is " +
				"refused by shape"
		case HasHarnessMaterializer(lang):
			// The language HAS a materializer and this strategy still cannot run on it. Today that is Go's
			// answer-reading strategies, and the cause is permanent rather than owed.
			cell.Status = CoverageRefuses
			cell.Cause = CauseNotAtCallSite
			cell.Note = "this strategy decides whether to take another turn by reading the ANSWER's text, " +
				"and in " + languageDisplay(lang) + " a response is your SDK's own type — the generated " +
				"module would have to import your SDK to read a field off it. Python materializes this one, " +
				"because there a response is a dict"
		default:
			cell.Status = CoverageRefuses
			cell.Cause = CauseNoMaterializer
			cell.MissingArtifact = "a " + lang + " loop module and its call-site rewriter (harnessMaterializers)"
			cell.Note = "the loop runtime is shared and has landed; what is missing for this language is " +
				"the emitted module a rewritten call site would drive, and the rewriter that emits it " +
				"(covered today: " + harnessMaterializerDisplay() + ")"
		}
		out = append(out, cell)
	}
	return out
}

// harnessCoverage answers for the EXECUTION ENVELOPE — what the harness axis narrows to after P34's
// split (FR5, decisions.md D-34.7).
//
// 🔴 One cell per language, and it REFUSES in every one, permanently, with `CauseNotAtCallSite` and NO
// missing artifact. That is not a gap anybody is going to close, and saying so plainly is the point of
// the cause taxonomy: an envelope is sandbox posture, a spend ceiling, a timeout, a guardrail binding —
// facts about how a node is DEPLOYED. None of them is written at a call site in any language, so there
// is no rewriter that could ever emit one, and labelling this `CauseNoMaterializer` would tell every
// reader to wait for something that is not coming.
//
// 🚫 The envelope is NOT unenforced because it is unmaterialized, and the Note says so. It is checked at
// RESOLVE — the turn ceiling against the loop's chosen value, the host services against what the loop
// needs, the concurrency limit against a declared group — and again by the sandbox at execution. A
// reader who took "refuses" to mean "ignored" would draw exactly the wrong conclusion about their own
// blast radius, which is the one misreading this Note exists to prevent.
func harnessCoverage(lang string) []CoverageCell {
	return []CoverageCell{{
		Axis:     string(variantspec.DimHarness),
		Language: lang,
		Form:     registry.StrategyEnvelope,
		Status:   CoverageRefuses,
		Cause:    CauseNotAtCallSite,
		// 🔴 The wording here is load-bearing and is checked by a fence in internal/changedelivery: this
		// Note is read verbatim into a delivery refusal, and a delivery refusal that mentioned a "host
		// service" would send an operator to look at a process when the answer is that no source changed.
		// TestHostAbsentAndNotRuntimeResolvableAreDistinct caught exactly that in an earlier draft. Say
		// what the envelope PROVIDES without naming the runtime concept.
		Note: "an execution envelope — where a node may reach on the network, what it may spend, how long " +
			"it may run, how many of its steps may overlap, which guardrail and approval gate it answers " +
			"to — is a property of how the node is DEPLOYED, so there is nothing to write into your source " +
			"in any language. 🔴 That is not the same as unenforced: the envelope is checked at RESOLVE " +
			"(a loop's chosen turn count against the ceiling, a loop's required second actors against the " +
			"ones the envelope grants, a concurrent group against the concurrency limit) and again by the " +
			"sandbox at execution",
	}}
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

// ── graph topology (P34 §5, FR12–FR18) ──────────────────────────────────────────────────────────

// graphRefusalDim is the axis label for topology. Like wiringRefusalDim it is a string rather than a
// `variantspec.Dimension`, and that is design D3 rather than an omission: every Dimension is a property
// of ONE node, and topology is a property BETWEEN nodes, so graph lives at the spec level beside `order`
// and `edges`. It is still an axis a user can request, so it must answer in every language.
const graphRefusalDim = "graph"

// GraphForms are the three shapes a spec can declare on the graph axis. They are FORMS rather than one
// cell because they fail independently: a language may be able to route conditionally and be unable to
// run two calls concurrently, and one verdict for the axis would be wrong in one direction or the other.
func GraphForms() []string {
	return []string{"concurrent group", "conditional edge", "merge"}
}

// graphCoverage answers for every (language, form) cell on the topology axis.
//
// 🔴 FR18 requires a refusal to name WHICH of three things is missing — the frontend, the analysis, or
// the language support — and never a generic unsupported state. Graph is the first axis where those come
// apart, so the read distinguishes them rather than collapsing them:
//
//	frontend missing   discovery has no analyser for this language at all, so there is no IR to declare
//	                   topology over. Nothing the customer writes changes it and no topology rewriter
//	                   would help. (CauseNoMaterializer, artifact: a frontend.)
//	analysis missing   there IS a frontend, but a SYNTACTIC one: `discovery.AnalysisSyntactic`'s own doc
//	                   says it "emits NODES AND NO EDGES". A spec can declare a concurrent group over
//	                   nodes, but a merge or a predicate has to be validated against edges and typed
//	                   contracts this analysis never produced. (CauseNoMaterializer, artifact: a typed
//	                   analysis.)
//	support missing    frontend and typed analysis both exist; what is absent is this language's
//	                   topology rewriter. (CauseNoMaterializer, artifact: the rewriter.)
//
// 🚫 None of the three is CauseNotAtCallSite. Concurrency and conditional routing ARE expressible at a
// call site — that is what makes them a codemod rather than a policy — so claiming otherwise would tell a
// reader that a thing which is merely unbuilt can never be built. That is the one label the taxonomy
// reserves for permanence, and borrowing it here would spend the taxonomy's only irreversible word.
func graphCoverage(lang string) []CoverageCell {
	var out []CoverageCell
	missing, artifact, note := graphGap(lang)
	for _, form := range GraphForms() {
		cell := CoverageCell{Axis: graphRefusalDim, Language: lang, Form: form}
		if missing == "" && GraphFormMaterializesIn(lang, form) {
			cell.Status = CoverageMaterializes
			cell.Note = "declared over the existing order and validated through the typed-contract gate " +
				"before any codemod is generated"
			out = append(out, cell)
			continue
		}
		cell.Status = CoverageRefuses
		cell.Cause = CauseNoMaterializer
		cell.MissingArtifact = artifact
		cell.Note = note
		if missing == "" {
			cell.MissingArtifact = "a " + lang + " topology rewriter for a " + form + " (graphMaterializers)"
			cell.Note = "the declaration, the typed-contract gate and the merge validation are " +
				"language-neutral and have landed; what is missing for this language is the rewriter that " +
				"writes this form into source (covered today: " + graphMaterializerDisplay() + ")"
		}
		out = append(out, cell)
	}
	return out
}

// graphGap names which of the frontend or the analysis is missing for a language, or "" when neither is
// — in which case what is missing is the rewriter, and the caller says so.
//
// It reads `discovery`'s own frontend registry rather than a list here, for the reason every other read
// in this file does: a hand-written copy is the optimistic one.
func graphGap(lang string) (missing, artifact, note string) {
	for _, fe := range discovery.DefaultFrontends() {
		if !strings.EqualFold(fe.Language(), lang) {
			continue
		}
		if fe.AnalysisKind() == discovery.AnalysisSyntactic {
			return "analysis",
				"a typed " + lang + " analysis that emits edges and io_contracts (today it is syntactic)",
				"this build's " + languageDisplay(lang) + " frontend is a SYNTACTIC analyser: it enumerates " +
					"call sites and emits no edges at all. A topology declaration has to be validated " +
					"against edges and typed I/O contracts, and there are none to validate against — so " +
					"what is missing is the ANALYSIS, not the rewriter and not anything in your repository"
		}
		return "", "", ""
	}
	return "frontend",
		"a " + lang + " discovery frontend",
		"this build has no frontend for " + languageDisplay(lang) + ", so nothing is read from a " +
			"repository in this language and there is no graph to declare topology over. What is missing " +
			"is the FRONTEND — the language support itself — rather than a topology rewriter"
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

// ── The axis's own declared status ──────────────────────────────────────────────────────────────

// AxisStatus is what an axis declares about itself across every registered language.
//
// 🔴 It lives HERE, beside the coverage table it is derived from, and not in the console. A console
// that computed this would be a second opinion about coverage, and coverage is a claim about a
// customer's code — the one thing the reading surface must never form its own view of. Every consumer
// reads this function, so a doc, a CLI listing and an operator page cannot disagree.
type AxisStatus string

const (
	// StatusExists — every registered language materializes at least one form on this axis.
	StatusExists AxisStatus = "EXISTS"
	// StatusPartial — some languages materialize and some refuse. The commonest honest answer, and the
	// one a single boolean would have to round in one direction or the other.
	StatusPartial AxisStatus = "PARTIAL"
	// StatusAbsent — no language materializes anything on this axis. 🔴 It is NOT "not applicable": the
	// axis exists and the engine applies none of it, which is a fact about the PLATFORM.
	StatusAbsent AxisStatus = "ABSENT"
)

// StatusFor returns an axis's declared status, derived from the one coverage table.
//
// Derived rather than hand-declared on purpose: a hand-maintained status is the optimistic copy this
// file exists to end, and it would go stale the first time a materializer landed.
func StatusFor(axis string) AxisStatus {
	var materializes, refuses int
	byLanguage := map[string]bool{}
	for _, c := range CoverageFor(axis) {
		if c.Status == CoverageMaterializes {
			materializes++
			byLanguage[c.Language] = true
		} else {
			refuses++
		}
	}
	switch {
	case materializes == 0:
		return StatusAbsent
	case refuses == 0 && len(byLanguage) == len(RegisteredLanguages()):
		return StatusExists
	default:
		return StatusPartial
	}
}
