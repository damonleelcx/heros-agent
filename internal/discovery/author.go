package discovery

// author.go carries P30 D4: every fact in the IR records WHO AUTHORED IT.
//
// # Why this is a new field and not the `Provenance` one already on IREdge
//
// 🔴 `IREdge.Provenance` is `internal/linkage`'s vocabulary — `framework`, `inferred_static`,
// `inferred_dynamic`, `runtime_only` — and it answers a completely different question: what STRENGTH OF
// EVIDENCE produced this edge. `linkage.Resolve` branches on those values to pick a winner when two
// sources disagree.
//
// D4 asks "who authored this edge", which is orthogonal. A `frontend`-authored edge can be
// framework-strength or inferred-static; a `heros`-authored one is always a hypothesis. Overloading one
// field with both vocabularies would break linkage's precedence the first time a `heros` value reached
// it — and, worse, would be the "two vocabularies for one concept" that the arbitration ladder puts
// above implementation cost. Two orthogonal facts, two fields, neither able to be read as the other.
//
// # Why an enum and not `is_inferred bool`
//
// `operator` is reserved and unused in P30. A fourth author is foreseeable — a human correcting an
// inferred edge is the obvious next request — and a boolean would have to be REPLACED rather than
// extended, which for a field written into stored documents means a migration over customer data.
//
// # Why the empty value reads as `legacy` and is NOT back-filled
//
// Every fact written before this field existed has an empty author, and the honest value for it is
// `legacy` — "nobody recorded who wrote this". Back-filling it to `frontend` would assert something
// about rows nobody examined. `legacy` is distinguishable in a query, which is the entire point: an
// incident asks "which of these edges did the model write", and a back-filled document cannot answer.

// FactAuthor is who wrote one fact into the IR. A closed vocabulary.
type FactAuthor string

const (
	// AuthorLegacy is the reading of an ABSENT author: the fact predates authorship being recorded.
	// It is never WRITTEN — a writer that has an author writes it, and a writer that does not is
	// refused (see RequireAuthor). It exists so a reader has a name for what it found.
	AuthorLegacy FactAuthor = "legacy"
	// AuthorFrontend: a language frontend established this fact by parsing the source.
	AuthorFrontend FactAuthor = "frontend"
	// AuthorDetector: a rule detector in internal/patternclassifier established it from topology.
	AuthorDetector FactAuthor = "detector"
	// AuthorHEROS: the platform's own analysis agent inferred it. Always carries a confidence and an
	// agent_config_hash; never overrides a frontend fact (P30 D3 fence 1).
	AuthorHEROS FactAuthor = "heros"
	// AuthorOperator: a human corrected it. RESERVED in P30 — nothing writes it, and the value exists
	// so that when something does, no stored document has to be migrated.
	AuthorOperator FactAuthor = "operator"
)

// authors is the closed set, for validation. A map rather than a switch so adding a member is one
// edit — the extensibility rule the ladder puts above implementation convenience.
var authors = map[FactAuthor]bool{
	AuthorFrontend: true,
	AuthorDetector: true,
	AuthorHEROS:    true,
	AuthorOperator: true,
	// 🚫 AuthorLegacy is deliberately ABSENT. It is a READING of an empty field, never a value a writer
	// may supply — a writer that stamps `legacy` is claiming it does not know who it is.
}

// ValidAuthor reports whether a is a value a WRITER may stamp.
func ValidAuthor(a FactAuthor) bool { return authors[a] }

// AuthorOf resolves a stored author, mapping the empty string to `legacy`.
//
// The accessor exists for the reason IRNode.MemoryDefault does: the two spellings arrive from different
// places — an omitted key from a pre-P30 document, an explicit value from a current writer — and a
// caller comparing against either literal would misread the other.
func AuthorOf(stored string) FactAuthor {
	if stored == "" {
		return AuthorLegacy
	}
	return FactAuthor(stored)
}

// Authored reports whether an author was actually recorded (i.e. this fact is not `legacy`).
func Authored(stored string) bool { return stored != "" }
