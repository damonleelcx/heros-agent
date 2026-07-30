package changedelivery

import (
	"fmt"

	"github.com/heros-foreal/agentd/internal/transform"
)

// source.go — the join between this package's delivery table and the SOURCE route's per-language
// answer (P13 §23.1).
//
// # Why this is a read and not a second table
//
// The runtime route's eligibility is language-independent: whether a change is data or program
// structure does not vary by frontend. The source route's is emphatically NOT — that is the whole
// subject of `transform.AxisCoverage()`, which is total over (axis × language × form).
//
// 🚫 So this file computes nothing. It looks up the cell the rewriter itself dispatches on and
// translates the shape, never the verdict. A hand-maintained copy of "which languages materialize
// model changes" is precisely the drift `transform/coverage.go` was written to end, and the copy is
// always the optimistic one.

// sourceForm maps a delivery change kind onto the coverage table's (axis, form) key.
//
// A change kind with no source cell (an empty form) is one the source route answers at a different
// granularity than the coverage table carries; the caller supplies the outcome directly.
type sourceKey struct {
	axis string
	// permanentlyRefused marks change kinds the source route refuses in EVERY language for a reason no
	// materializer would fix — a provider swap rewrites the SDK call, which is a gateway concern
	// (ADR-002), not a call-site argument.
	permanentlyRefused bool
	note               string
}

var sourceKeys = map[ChangeKind]sourceKey{
	ChangeModelWithinProvider: {axis: "model"},
	ChangeModelAcrossProvider: {axis: "model", permanentlyRefused: true,
		note: "A provider swap rewrites the SDK call itself, which ADR-002 places outside the codemod's remit in every language."},
	ChangeInferenceParams: {axis: "model"},
	ChangePromptVersion:   {axis: "prompt"},
	ChangeSkillBinding:    {axis: "skills"},
	ChangeToolSet:         {axis: "tools"},
	ChangeRetrievalParams: {axis: "context"},
	ChangeSelectionPolicy: {axis: "context"},
	ChangeMemoryStrategy:  {axis: "memory"},
	ChangeHarnessStrategy: {axis: "harness"},
	ChangeHarnessParams:   {axis: "harness"},
	ChangeWiring:          {axis: "wiring"},
}

// SourceOutcomeFor reads the source route's answer for one change at one CALL SITE.
//
// form is the call site's own coverage form — an SDK row like "py.openai.chat.completions.create", a
// policy name, a statement shape. Pass "" when the form is not known yet; see the mixed-language rule
// below, which is the interesting half of this function.
func SourceOutcomeFor(kind ChangeKind, language, form string) SourceOutcome {
	key, ok := sourceKeys[kind]
	if !ok {
		return SourceOutcome{Cause: "unknown-change-kind",
			Note: "the delivery table does not carry this change kind, so no source cell can be read for it"}
	}
	if key.permanentlyRefused {
		return SourceOutcome{
			Cause:     string(transform.CauseNotAtCallSite),
			Permanent: true,
			Note:      key.note,
		}
	}

	cells := transform.CoverageFor(key.axis)
	var (
		refusal    *transform.CoverageCell
		materializ int
		total      int
	)
	for i := range cells {
		c := cells[i]
		if c.Language != language {
			continue
		}
		if form != "" && c.Form != form {
			continue
		}
		total++
		if c.Status == transform.CoverageMaterializes {
			materializ++
			if form != "" {
				// An exact form match is the honest per-call-site answer, and it ends the search.
				return SourceOutcome{Materializes: true, Note: c.Note}
			}
			continue
		}
		// Keep the FIRST refusal. The coverage table is already sorted into a stable order, so picking
		// the first keeps two builds' reports byte-comparable.
		if refusal == nil {
			refusal = &cells[i]
		}
	}

	if total == 0 {
		// 🔴 No cell at all. Reported as the upstream totality violation it is, rather than defaulted in
		// either direction — defaulting to "materializes" would promise a diff that never arrives, and
		// defaulting to "refuses" would invent a cause nobody wrote.
		return SourceOutcome{Cause: "no-coverage-cell",
			Note: "transform.AxisCoverage() carries no cell for this (axis, language, form); coverage is meant to be total, so this is a defect rather than an answer"}
	}

	// ── the mixed case, and why it resolves conservatively
	//
	// A language is rarely all-or-nothing. Python materializes a model change at an `openai` call and
	// refuses one at a `langchain` call, because the second binds its model before the call. With no
	// form supplied there is no honest way to say "yes" — 🚫 and answering yes because SOME form works
	// is exactly the optimistic copy `transform/coverage.go` exists to prevent. It would promise a
	// reader a diff that their particular call site will never produce.
	//
	// So a mixed language with no form reports the REFUSAL, and names the split so the reader knows the
	// answer is about their call site rather than about their language.
	if refusal == nil {
		return SourceOutcome{Materializes: true}
	}
	out := SourceOutcome{
		Cause:           string(refusal.Cause),
		Permanent:       refusal.Cause != transform.CauseNoMaterializer,
		MissingArtifact: refusal.MissingArtifact,
		Note:            refusal.Note,
		// A call-site-shape refusal is a fact about the reader's own source, so the runtime route must
		// report it too rather than offering a schema field that would not help them.
		CallSiteCannotCarry: refusal.Cause == transform.CauseCallSiteShape,
	}
	if materializ > 0 {
		// 🔴 Varies, not merely refused. The conservative Materializes=false above is the right answer to
		// "will MY call site get a diff"; it is the wrong input to "is this change undeliverable
		// anywhere", because some call site plainly does. Marking it keeps one value from answering two
		// questions with opposite correct answers.
		out.Varies = true
		out.Note = fmt.Sprintf("%d of %d call-site forms in %s materialize this axis; the call site's own form decides, and it was not supplied. %s",
			materializ, total, language, refusal.Note)
	}
	if out.Permanent {
		// A permanent source refusal names no artifact, for the same reason a permanent runtime one
		// does not.
		out.MissingArtifact = ""
	}
	return out
}
