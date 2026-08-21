package assessment

import "sort"

// state.go holds the three closed vocabularies a finding switches on: how the claim was established
// (State), what produced it (Origin), and — when this build cannot assess the axis at all — which of
// exactly three things is missing (RefusalCause).

// ── State ────────────────────────────────────────────────────────────────────────────────────────

// State is how a finding's claim was established. Four, and the console renders four (task 5.1) — a
// component that renders three is wrong, and one that renders `not_measured` as a dimmer `observed` is
// wrong in the way this phase is most exposed to.
//
// # Why four rather than three (design D1)
//
// Collapsing `observed` into `measured` conflates *true by construction* (the code binds three tools)
// with *true by experiment* (this variant scored 0.81 ± 0.05). They warrant different confidence and
// support different actions.
//
// Collapsing `refused` into `not_measured` conflates "we could not" with "this build cannot", and only
// the second is actionable by us rather than by the customer.
type State string

const (
	// StateMeasured — a number from an eval run, carrying its interval and the size of the set behind
	// it. The only state that may carry an `EvalSetReport`.
	StateMeasured State = "measured"
	// StateObserved — read deterministically from the IR or the tree. True by construction.
	//
	// 🔴 An `observed` finding may state an ABSENCE — "no memory reads or writes were found" — and
	// that is deliberate (PRD §14 A3). Absence found by a total scan of the IR is an observation; the
	// opinion that the absence is a PROBLEM is a proposal, and it lives in P35 where verification can
	// decide it.
	StateObserved State = "observed"
	// StateNotMeasured — the axis was examined and no claim could be established. NAMES the missing
	// input. 🔴 Never rendered as zero, never omitted from the report.
	StateNotMeasured State = "not_measured"
	// StateRefused — this BUILD cannot assess this axis for this target, and the cause names which of
	// the frontend, the analysis or the language support is missing.
	StateRefused State = "refused"
)

var states = []State{StateMeasured, StateObserved, StateNotMeasured, StateRefused}

// States returns the four, in evidence-strength order — which is also the order `Ordered` sorts a
// report into (FR5). A copy.
func States() []State { return append([]State(nil), states...) }

// Valid reports membership.
func (s State) Valid() bool {
	for _, v := range states {
		if v == s {
			return true
		}
	}
	return false
}

// String makes State printable in an error.
func (s State) String() string { return string(s) }

// NeedsMissingInput reports whether this state must name what it lacked. Exactly `not_measured`:
// design D1's mandatory cause, expressed as a predicate so the constructor, the schema generator and
// the fence all ask the same question rather than each re-deciding it.
func (s State) NeedsMissingInput() bool { return s == StateNotMeasured }

// NeedsRefusalCause reports whether this state must name which of three things is missing.
func (s State) NeedsRefusalCause() bool { return s == StateRefused }

// IsHazard reports whether the console's hazard palette applies (task 5.6).
//
// 🔴 `refused` only. Absence is not a hazard, and a palette is legible only while it is rare: an
// assessment of a repository the platform has just met is MOSTLY absence, so painting `not_measured`
// in hazard colours would make the first report a wall of red and the palette worthless by the second.
func (s State) IsHazard() bool { return s == StateRefused }

// ── Origin ───────────────────────────────────────────────────────────────────────────────────────

// Origin is what produced a finding. Two, and the split is the phase's second product: a reader can
// see at a glance how much of the report was read from their code and how much was guessed about it.
type Origin string

const (
	// OriginStructural — deterministic extraction from the IR or the tree. No provider call was made.
	OriginStructural Origin = "structural"
	// OriginInferred — HEROS read source and concluded. 🔴 Carries the provider model version and the
	// content address of the pin, both required, and is marked persistently wherever it renders.
	OriginInferred Origin = "inferred"
)

var origins = []Origin{OriginStructural, OriginInferred}

// Origins returns the two. A copy.
func Origins() []Origin { return append([]Origin(nil), origins...) }

// Valid reports membership.
func (o Origin) Valid() bool {
	for _, v := range origins {
		if v == o {
			return true
		}
	}
	return false
}

// String makes Origin printable in an error.
func (o Origin) String() string { return string(o) }

// NeedsInferenceAddress reports whether this origin must carry a pin address and a model version.
func (o Origin) NeedsInferenceAddress() bool { return o == OriginInferred }

// ── RefusalCause ─────────────────────────────────────────────────────────────────────────────────

// RefusalCause names which of exactly three things is missing when a build cannot assess an axis.
//
// # Why three named causes and never a generic "unsupported" (PRD §7.5)
//
// The three send a reader to three different places, and only one of them is the customer's problem:
//
//	frontend  the language's parser emits nothing on this axis  → we ship a frontend improvement
//	analysis  the extractor for this axis does not exist yet    → we ship the extractor
//	language  the language is not registered at all             → the customer learns the boundary
//
// A single "unsupported" tells all three readers to do nothing, which is why the coverage contract
// already refuses one and why this enum repeats its shape rather than inventing a fourth vocabulary.
type RefusalCause string

const (
	// RefusalFrontend — the language's frontend does not emit what this axis needs. The most common,
	// and the one design D6 is about: zero edges from a frontend that emits none is a fact about the
	// TOOL, never about the subject.
	RefusalFrontend RefusalCause = "frontend"
	// RefusalAnalysis — the analysis for this axis does not exist in this build. This is what the two
	// P34 axes report until P34 lands.
	RefusalAnalysis RefusalCause = "analysis"
	// RefusalLanguage — the target's language is not registered at all, so no frontend ran.
	RefusalLanguage RefusalCause = "language"
)

var refusalCauses = []RefusalCause{RefusalFrontend, RefusalAnalysis, RefusalLanguage}

// RefusalCauses returns the three. A copy.
func RefusalCauses() []RefusalCause { return append([]RefusalCause(nil), refusalCauses...) }

// Valid reports membership.
func (c RefusalCause) Valid() bool {
	for _, v := range refusalCauses {
		if v == c {
			return true
		}
	}
	return false
}

// String makes RefusalCause printable in an error.
func (c RefusalCause) String() string { return string(c) }

// ── MissingInput ─────────────────────────────────────────────────────────────────────────────────

// MissingInput is the named input a `not_measured` finding lacked.
//
// # 🔴 Why this is a CLOSED enum and not the free string design D1 describes
//
// D1 requires the cause to be mandatory. It does not require it to be free text, and free text here
// fails twice. First, task 6.2 alerts on the RATE of assessments returning nine `not_measured`
// findings and task 6.1 breaks health out per axis and per state — both are group-bys, and a free
// string makes them a group-by over a set nobody can enumerate. Second, `eval-set-decisiveness`'s
// last requirement says the four eval reasons "stay four" and each renders a DISTINCT message; a
// console cannot hold four distinct messages against an open set.
//
// So the set is closed and every member below is a message the console has written. The finding's
// `Claim` carries the specifics — WHICH node the sandbox could not run — where cardinality is bounded
// by the customer's own repository rather than by an author's phrasing. That is also why no tenth
// field was added for it: a `not_measured` finding's claim IS "here is what is missing", and a
// separate `detail` would give two fields one job and let a renderer show the wrong one.
type MissingInput string

const (
	// ── The four `eval-set-decisiveness` requires to stay four ───────────────────────────────────
	//
	// 🔴 These four are the whole reason a workflow could not be MEASURED, and they are four because
	// a reader does four different things: write an entry point, supply a credential, ask us about the
	// sandbox, or learn that the language is out of scope.

	// MissingEntryPoint — nothing in the tree can be run as a program.
	MissingEntryPoint MissingInput = "no_runnable_entry_point"
	// MissingCredential — the workflow needs a provider credential the platform was not given.
	MissingCredential MissingInput = "missing_credential"
	// MissingSandboxRefusal — the sandbox declined to execute it (P3 posture, unchanged).
	MissingSandboxRefusal MissingInput = "sandbox_refusal"
	// MissingLanguageSupport — the language has no runner.
	MissingLanguageSupport MissingInput = "unsupported_language"

	// ── The structural ones ───────────────────────────────────────────────────────────────────────

	// MissingFrontendEdges — the language's frontend emits no edges, so topology could not be read.
	// 🔴 Design D6: this is the missing input that keeps "zero edges" a fact about the frontend.
	MissingFrontendEdges MissingInput = "frontend_emits_no_edges"
	// MissingUnresolvedField — the IR carries discovery's `unresolved` sentinel where the claim would
	// have come from, and inference did not resolve it either.
	MissingUnresolvedField MissingInput = "unresolved_in_ir"
	// MissingSourceRevision — no snapshot is held for the revision under assessment.
	MissingSourceRevision MissingInput = "no_source_snapshot"
	// MissingNoNodes — discovery found no LLM call sites at all, so seven of the nine axes have no
	// subject. Distinct from `unresolved_in_ir`: there is nothing to resolve, not something unresolved.
	MissingNoNodes MissingInput = "no_call_sites_discovered"
	// MissingNotVisibleStatically — the claim is about behaviour BETWEEN calls, which a static
	// call-site extraction cannot observe at all.
	//
	// 🔴 This member is design D6 applied to two axes where it is easy to miss, and it is the single
	// most important honesty control in the structural pass. `discovery` emits `memory: none` and
	// `harness: single-shot` for EVERY node, and says so in its own source: those are "a statement
	// about the EVIDENCE, not a placeholder ... the honest floor". An extractor that read those fields
	// and reported "this repository has no memory strategy" would be stating a property of the PARSER
	// as a property of the REPOSITORY — the exact inversion D6 exists to prevent, arriving on the two
	// axes the customer most wants an answer about.
	//
	// So the structural pass reports `not_measured` here and the residue is what inference is for.
	// Distinct from `unresolved_in_ir`: nothing is unresolved — the field resolved, to a floor that
	// means "not looked at".
	MissingNotVisibleStatically MissingInput = "not_visible_in_static_ir"

	// ── The two that are about US, not about the repository ───────────────────────────────────────

	// MissingBudgetExhausted — the assessment reached its spend cap before this axis (§7.3). 🔴 A
	// first-class outcome, not an error page, and the partial report is never presented as complete.
	MissingBudgetExhausted MissingInput = "budget_exhausted"
	// MissingInferenceAbstained — HEROS was asked and declined to conclude (FR10). It is a SUCCESS of
	// the abstention discipline, and §3.5 counts it as one; it is `not_measured` here because the
	// reader's position is unchanged either way.
	MissingInferenceAbstained MissingInput = "inference_abstained"
)

var missingInputs = []MissingInput{
	MissingEntryPoint, MissingCredential, MissingSandboxRefusal, MissingLanguageSupport,
	MissingFrontendEdges, MissingUnresolvedField, MissingSourceRevision, MissingNoNodes,
	MissingNotVisibleStatically,
	MissingBudgetExhausted, MissingInferenceAbstained,
}

// MissingInputs returns the closed set. A copy.
func MissingInputs() []MissingInput { return append([]MissingInput(nil), missingInputs...) }

// EvalMissingInputs returns exactly the four reasons a WORKFLOW could not be measured — the set
// `eval-set-decisiveness` requires to stay four. Read from the same constants, so "four" is a
// property of one list rather than a claim two lists make separately.
func EvalMissingInputs() []MissingInput {
	return []MissingInput{MissingEntryPoint, MissingCredential, MissingSandboxRefusal, MissingLanguageSupport}
}

// Valid reports membership.
func (m MissingInput) Valid() bool {
	for _, v := range missingInputs {
		if v == m {
			return true
		}
	}
	return false
}

// String makes MissingInput printable in an error.
func (m MissingInput) String() string { return string(m) }

// MissingInputNames returns the closed set as sorted plain strings, for a schema's `enum`.
func MissingInputNames() []string {
	out := make([]string, 0, len(missingInputs))
	for _, m := range missingInputs {
		out = append(out, string(m))
	}
	sort.Strings(out)
	return out
}
