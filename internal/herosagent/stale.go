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

// ── P36: a pin whose producing shape can no longer be authored (task 1.5, D4) ────────────────────

// PinState is how a stored inference stands against the definition serving today. A CLOSED set of
// three, because the failure this exists to prevent is a two-valued rendering: a console that knows
// only "current" and "absent" has to put a pin from a retired shape into one of them, and BOTH are
// wrong. Absent claims the agent never analysed this; current claims these facts describe the
// configuration running now.
type PinState string

const (
	// PinCurrent: produced by the definition that is serving inference right now.
	PinCurrent PinState = "current"
	// PinStale: produced by a definition that is no longer serving. The facts remain attributable —
	// the version row is immutable — and nothing is keeping them up to date.
	PinStale PinState = "stale"
	// PinUnattributable: the producing definition is not in the version store at all.
	//
	// 🔴 A DIFFERENT state from stale, not a worse one. Stale says "produced by that configuration,
	// which has been superseded" and names it. This says "produced by a hash this deployment cannot
	// resolve" — which is a different question with a different answer (a rolled-back migration, a
	// restored database, a definition published on another deployment) and sends a reader somewhere
	// else entirely.
	PinUnattributable PinState = "unattributable"
)

// PinStates returns the closed set.
func PinStates() []PinState { return []PinState{PinCurrent, PinStale, PinUnattributable} }

// PinStatus is one stored inference's standing, as every surface renders it.
type PinStatus struct {
	State PinState `json:"state"`
	// ProducingConfigHash is the definition that produced these facts. 🔴 ALWAYS populated, in every
	// state, including `unattributable` — it is the stored `agent_config_hash`, which is a fact about
	// the row rather than a lookup that can fail. A stale pin rendered without naming its producer is
	// a warning with nothing to act on.
	ProducingConfigHash string `json:"producing_config_hash"`
	// ProducingDisplay is the shortened hash a reader sees.
	ProducingDisplay string `json:"producing_display"`
	// Authorable reports whether the producing definition's shape can still be authored today.
	//
	// 🔴 FALSE is not an error. A definition published under a vocabulary that has since moved on is
	// still a real definition that produced real facts; what it is not is a shape somebody can
	// re-create from the editor, and a reader offered a "re-publish this" control that cannot work is
	// worse served than one told why.
	Authorable bool `json:"authorable"`
	// UnauthorableReason names WHY the shape can no longer be authored, and is empty when it can.
	UnauthorableReason string `json:"unauthorable_reason,omitempty"`
	// Reason is the stale vocabulary's own value, empty for a current pin.
	Reason StaleReason `json:"reason,omitempty"`
	// Sentence is what a reader gets. Always populated.
	Sentence string `json:"sentence"`
}

// ClassifyPin answers where one stored inference stands (task 1.5 / 9.4).
//
// `activeHash` is the definition serving inference, empty when none is. `producer` is the version row
// the pin's hash resolves to, and `known` is false when it resolves to nothing.
//
// 🚫 It never re-runs anything and never reads a provider. Classification is a pure function of what
// is stored, which is what makes it safe to call on every row of a list.
func ClassifyPin(st Stored, activeHash string, producer Version, known bool) PinStatus {
	out := PinStatus{
		ProducingConfigHash: st.AgentConfigHash,
		ProducingDisplay:    confighashDisplay(st.AgentConfigHash),
	}
	switch {
	case !known:
		out.State = PinUnattributable
		out.Authorable = false
		out.UnauthorableReason = "no published definition on this deployment has that config_hash"
		out.Sentence = "These facts name the configuration that produced them, and this deployment has " +
			"no published definition with that config_hash. They are shown as they were stored and are " +
			"NOT attributed to anything running now. That is different from stale: a stale pin names a " +
			"definition that has been superseded, and this one names a definition this deployment cannot " +
			"resolve at all — look for a rolled-back migration, a restored database, or a definition " +
			"published elsewhere."
		return out
	case activeHash != "" && st.AgentConfigHash == activeHash:
		out.State = PinCurrent
	default:
		out.State = PinStale
		out.Reason = StaleDefinitionRetired
	}

	// 🔴 The authorability question is asked of the PRODUCER's definition, by running today's
	// validator over it. Not by comparing version numbers, and not by a flag written at publish: a flag
	// records what was true when somebody wrote it, and the whole point is to detect a rule that
	// changed AFTERWARDS.
	if err := producer.Definition.Validate(); err != nil {
		out.Authorable = false
		out.UnauthorableReason = err.Error()
	} else {
		out.Authorable = true
	}

	// A stored StaleReason WINS over the derived one. `analysis_disabled` is written when a placement
	// changes and it records something the hashes cannot: that a gap exists. Deriving over it would
	// erase the gap the moment somebody re-enabled.
	if st.StaleReason != "" {
		out.State = PinStale
		out.Reason = st.StaleReason
	}

	switch {
	case out.State == PinCurrent && out.Authorable:
		out.Sentence = "These facts were produced by the definition serving inference now."
	case out.State == PinCurrent:
		out.Sentence = "These facts were produced by the definition serving inference now, and its shape " +
			"can no longer be authored from the editor: " + out.UnauthorableReason + ". It keeps serving " +
			"— an activated definition is not withdrawn by a vocabulary change — and editing it would " +
			"publish a new one."
	case !out.Authorable:
		out.Sentence = "These facts are STALE and remain attributed to the configuration named above, " +
			"which is no longer serving and whose shape can no longer be authored: " +
			out.UnauthorableReason + ". They were true when the agent wrote them and nothing is keeping " +
			"them current. Analysing this revision again under the current definition may produce a " +
			"different answer, and it is an explicit act that spends provider tokens."
	default:
		out.Sentence = SentenceForStale(out.Reason)
	}
	return out
}
