package herosagent

// stale.go is task 9.5: what happens to stored inferences when a tenant is disabled.
//
// # The decision, and that it is provisional
//
// Q5 is open and its assumed answer is RETAINED AND MARKED STALE. That assumption is implemented here
// rather than left as a comment, because the alternative — deciding later — means the first tenant who
// disables analysis gets whatever the code happens to do, which is delete-by-omission if nothing
// handles it at all.
//
// # Why retained rather than deleted
//
// Deleting is the tempting reading of "disabling returns every surface to rule-derived facts": no
// inferred facts, no rows, clean. It is wrong in two directions.
//
// A customer who disables analysis for a week and re-enables it would have paid twice for the same
// answers, because D2's pinning is a property of the STORE and deleting the store is deleting the
// pin. And an inference that turned out to be wrong is evidence — "which of these edges did the model
// write, and when" is the question an incident asks, and it is unanswerable against rows somebody
// removed to tidy up.
//
// # Why STALE rather than simply hidden
//
// A hidden row is a row a surface has to remember to hide, and the surfaces are many. A stale MARK is
// carried by the data, so a reader that forgets to filter shows a fact that says it is stale — which
// degrades honestly. The opposite default fails silently and looks correct.
//
// 🚫 And staleness is NOT computed from the placement at read time. That would make every stored
// inference for a disabled tenant read as stale forever, including the ones produced before the tenant
// was ever enabled — and, worse, it would make the mark disappear the moment somebody re-enabled,
// erasing the fact that a gap existed. The mark is written when the placement changes and it stays.

// StaleReason is why a stored inference is marked stale. A closed vocabulary, because "why is this
// graph out of date" has different answers with different next actions.
type StaleReason string

const (
	// StaleDisabled: analysis was switched off for this tenant after the inference was stored. The
	// facts were true when produced and nothing is maintaining them.
	StaleDisabled StaleReason = "analysis_disabled"
	// StaleDefinitionRetired: the definition that produced it is no longer active. The facts are still
	// attributable — the version row is immutable — and a re-run under the current definition may
	// differ.
	StaleDefinitionRetired StaleReason = "definition_retired"
)

// StaleReasons returns the closed set.
func StaleReasons() []StaleReason { return []StaleReason{StaleDisabled, StaleDefinitionRetired} }

// staleSentences is what a reader gets. Resolved here, like every other sentence in this package.
var staleSentences = map[StaleReason]string{
	StaleDisabled: "Analysis was switched off for this organization after these facts were produced. " +
		"They are kept and still attributed — they were true when the agent wrote them — and nothing " +
		"is keeping them current. Re-enabling analysis resumes maintenance; it does not re-run them.",
	StaleDefinitionRetired: "The agent definition that produced these facts is no longer the active " +
		"one. They remain attributable to it, and analysing this revision again under the current " +
		"definition may produce a different answer.",
}

// SentenceForStale returns the reader-facing sentence, or "" for an unknown reason.
//
// 🚫 No generic fallback, for the reason SentenceFor and SentenceForState have none.
func SentenceForStale(r StaleReason) string { return staleSentences[r] }
