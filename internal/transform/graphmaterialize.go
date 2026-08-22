package transform

import (
	"sort"
	"strings"
)

// GRAPH TOPOLOGY MATERIALIZATION — the table, and what it means for it to be empty (P34 §5)
// ────────────────────────────────────────────────────────────────────────────────────────
//
// This file is the seam `graphCoverage` derives from, and it is deliberately its own file rather than a
// few rows inside coverage.go. The reason is the one coverage.go's own doc comment gives: every value in
// the coverage read is derived from the table the REWRITER dispatches on, never from a copy. So the
// table has to exist somewhere a rewriter would read it — here — and coverage has to read that, not a
// list beside itself.
//
// 🔴 An EMPTY row set for a language is a refusal with `CauseNoMaterializer`, never silence, and never a
// `materializes` cell that nothing backs. A `graph_groups` declaration that resolved, hashed, and then
// produced no diff would let a variant's `config_hash` be scored against unchanged source — FR16's
// dropped override, which is the one failure mode this axis cannot have.

// graphMaterializers is, per language, the topology forms this build can write into source.
//
// 🔴 Rows are added ONLY when a rewriter exists and a test drives it end to end. A row here is a promise
// that `Generate` emits a patch for that (language, form); coverage repeats the promise on every surface,
// and the promise is checked against the engine by TestGraphCoverageAgreesWithEngine.
var graphMaterializers = map[string][]string{
	// (empty today — see GraphMaterializerLanguages)
}

// GraphFormMaterializesIn reports whether a (language, form) cell can be materialized.
//
// Fail-closed on both an unknown language and an unknown form: a caller asking about something outside
// the closed set gets `false`, never a permissive default, because the consequence of a wrong `true`
// here is a spec that hashes a topology the source does not have.
func GraphFormMaterializesIn(language, form string) bool {
	for _, f := range graphMaterializers[strings.ToLower(language)] {
		if f == form {
			return true
		}
	}
	return false
}

// GraphMaterializerLanguages lists the languages with at least one topology rewriter, sorted.
//
// 🔴 It is EMPTY today, and that is the honest state rather than a placeholder. P34 ships the
// declaration, the validation, the typed-contract gate and the refusal; it ships no topology codemod in
// any language, so every cell refuses BY NAME with the artifact that would close it. PRD §12 stages it
// that way on purpose — "in the language with the strongest frontend first; every other language refuses
// by name" — and a `materializes` cell added ahead of the rewriter is precisely the dropped override
// FR16 forbids.
func GraphMaterializerLanguages() []string {
	out := make([]string, 0, len(graphMaterializers))
	for l := range graphMaterializers {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// graphMaterializerDisplay renders that list for a refusal message. "none yet" rather than an empty
// string: a sentence that trails off after "covered today:" reads as a bug in the message rather than as
// an answer, and a reader cannot tell the two apart.
func graphMaterializerDisplay() string {
	langs := GraphMaterializerLanguages()
	if len(langs) == 0 {
		return "none yet — the topology declaration and its gates have landed, the codemods have not"
	}
	return strings.Join(langs, ", ")
}
